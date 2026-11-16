package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Imkumar80/agent-orchestrator-/internal/domain"
	"github.com/Imkumar80/agent-orchestrator-/internal/orchestrator"
	"github.com/Imkumar80/agent-orchestrator-/internal/ports"
	"github.com/Imkumar80/agent-orchestrator-/internal/providers"
)

func TestHealthEndpoint(t *testing.T) {
	handler, _, _, _ := newTestHandler(t)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health", nil))
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("health response status/header = %d/%q", response.Code, response.Header().Get("Content-Type"))
	}
	if response.Body.String() != "{\"status\":\"ok\"}\n" {
		t.Fatalf("health response = %q", response.Body.String())
	}
}

func TestCreateTaskAcceptedAndUsesApplicationContext(t *testing.T) {
	handler, coordinator, _, _ := newTestHandler(t)
	request := taskRequest(t, map[string]any{
		"id": "task-123", "description": "Analyze this codebase", "provider": "codex",
		"working_directory": filepath.Clean(t.TempDir()),
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("POST status = %d, body %s", response.Code, response.Body)
	}
	var body taskResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Accepted || body.Session.ID == "" || body.Session.Status != domain.SessionRunning {
		t.Fatalf("task response = %#v", body)
	}
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if coordinator.task.ID != "task-123" || coordinator.task.CreatedAt.IsZero() {
		t.Fatalf("coordinator received task = %#v", coordinator.task)
	}
	if coordinator.ctx != context.Background() {
		t.Fatal("expected app execution context, not the short-lived HTTP request context")
	}
}

func TestCreateTaskRejectsMalformedAndInvalidRequests(t *testing.T) {
	handler, _, _, _ := newTestHandler(t)
	cases := []struct {
		name        string
		contentType string
		body        string
		wantStatus  int
		wantCode    string
	}{
		{"invalid JSON", "application/json", `{"task":`, http.StatusBadRequest, "invalid_request"},
		{"unknown field", "application/json", `{"task":{"id":"x","extra":true}}`, http.StatusBadRequest, "invalid_request"},
		{"wrong content type", "text/plain", `{}`, http.StatusBadRequest, "invalid_request"},
		{"invalid task", "application/json", `{"task":{"id":"x","description":" ","provider":"codex","working_directory":"C:/repo"}}`, http.StatusBadRequest, "invalid_request"},
		{"unsupported provider", "application/json", `{"task":{"id":"x","description":"do work","provider":"unknown","working_directory":"C:/repo"}}`, http.StatusUnprocessableEntity, "unsupported_provider"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(test.body))
			request.Header.Set("Content-Type", test.contentType)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d: %s", response.Code, test.wantStatus, response.Body)
			}
			assertErrorCode(t, response, test.wantCode)
		})
	}
}

func TestCreateTaskProviderAndStartupFailures(t *testing.T) {
	handler, coordinator, _, _ := newTestHandler(t)
	coordinator.err = providers.ErrProviderNotFound
	coordinator.session = domain.Session{ID: "ses-failed", Status: domain.SessionFailed}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, taskRequest(t, validTaskInput(t)))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing provider status = %d, want 503", response.Code)
	}
	assertErrorCode(t, response, "provider_unavailable")

	coordinator.err = errors.New("provider failure includes secret details")
	coordinator.session = domain.Session{ID: "ses-failed", Status: domain.SessionFailed}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, taskRequest(t, validTaskInput(t)))
	if response.Code != http.StatusBadGateway {
		t.Fatalf("startup failure status = %d, want 502", response.Code)
	}
	var body errorEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "provider_start_failed" || body.Error.SessionID != "ses-failed" || strings.Contains(response.Body.String(), "secret details") {
		t.Fatalf("startup error response = %s", response.Body)
	}
}

func TestGetSessionAndMissingSession(t *testing.T) {
	handler, _, sessions, _ := newTestHandler(t)
	sessions.values["ses-1"] = validSession("ses-1", domain.SessionRunning)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/sessions/ses-1", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("GET status = %d: %s", response.Code, response.Body)
	}
	var body sessionResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Session.ID != "ses-1" || body.Session.Status != domain.SessionRunning {
		t.Fatalf("GET session = %#v", body.Session)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/sessions/missing", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing GET status = %d", response.Code)
	}
	assertErrorCode(t, response, "session_not_found")
}

func TestCancelRunningAndTerminalSession(t *testing.T) {
	handler, _, sessions, _ := newTestHandler(t)
	sessions.values["running"] = validSession("running", domain.SessionRunning)
	sessions.values["done"] = validSession("done", domain.SessionCompleted)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/sessions/running/cancel", nil))
	if response.Code != http.StatusAccepted {
		t.Fatalf("cancel status = %d: %s", response.Code, response.Body)
	}
	var canceled cancelResponse
	if err := json.Unmarshal(response.Body.Bytes(), &canceled); err != nil {
		t.Fatal(err)
	}
	if !canceled.Accepted || canceled.AlreadyTerminal || canceled.Session.Status != domain.SessionInterrupted {
		t.Fatalf("cancel response = %#v", canceled)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/sessions/done/cancel", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("terminal cancel status = %d", response.Code)
	}
	if err := json.Unmarshal(response.Body.Bytes(), &canceled); err != nil {
		t.Fatal(err)
	}
	if canceled.Accepted || !canceled.AlreadyTerminal {
		t.Fatalf("terminal cancel response = %#v", canceled)
	}
}

func TestSessionListAndProviderAvailabilityEndpoints(t *testing.T) {
	handler, _, _, _ := newTestHandler(t)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"sessions":[]`) {
		t.Fatalf("GET sessions response = %d %s", response.Code, response.Body)
	}
	serverProviderResponse := httptest.NewRecorder()
	handler.ServeHTTP(serverProviderResponse, httptest.NewRequest(http.MethodGet, "/api/v1/providers", nil))
	if serverProviderResponse.Code != http.StatusOK || !strings.Contains(serverProviderResponse.Body.String(), `"name":"codex","available":true`) || !strings.Contains(serverProviderResponse.Body.String(), `"name":"opencode","available":false`) {
		t.Fatalf("GET providers response = %d %s", serverProviderResponse.Code, serverProviderResponse.Body)
	}
}

func TestRequestBodyLimit(t *testing.T) {
	_, coordinator, sessions, publisher := newTestHandler(t)
	configured, err := NewServer(Config{MaxRequestBodySize: 16}, Dependencies{
		Coordinator: mustCoordinator(t, coordinator), Sessions: sessions, Events: publisher, ExecutionContext: context.Background(),
	})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(`{"task":{"id":"this-is-too-long"}}`))
	request.Header.Set("Content-Type", "application/json")
	configured.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body status = %d", response.Code)
	}
	assertErrorCode(t, response, "request_too_large")
}

func newTestHandler(t *testing.T) (http.Handler, *fakeCoordinator, *fakeSessions, *fakeEvents) {
	t.Helper()
	sessions := &fakeSessions{values: make(map[domain.SessionID]domain.Session)}
	coordinator := &fakeCoordinator{session: validSession("ses-new", domain.SessionRunning)}
	publisher := newFakeEvents()
	plannerCoordinator := mustCoordinator(t, coordinator)
	server, err := NewServer(Config{}, Dependencies{
		Coordinator: plannerCoordinator, Sessions: sessions, Events: publisher,
		ExecutionContext: context.Background(), Providers: staticProviderCatalog{domain.ProviderCodex: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	return server.Handler(), coordinator, sessions, publisher
}

func mustCoordinator(t *testing.T, starter *fakeCoordinator) *orchestrator.Coordinator {
	t.Helper()
	coordinator, err := orchestrator.NewCoordinator(nil, starter)
	if err != nil {
		t.Fatal(err)
	}
	return coordinator
}

func taskRequest(t *testing.T, task map[string]any) *http.Request {
	t.Helper()
	body, err := json.Marshal(map[string]any{"task": task})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	return request
}

func validTaskInput(t *testing.T) map[string]any {
	t.Helper()
	return map[string]any{
		"id": "task-1", "description": "do work", "provider": "codex",
		"working_directory": filepath.Clean(t.TempDir()),
	}
}

func validSession(id domain.SessionID, status domain.SessionStatus) domain.Session {
	now := time.Now().UTC()
	return domain.Session{
		ID: id, TaskID: "task-1", Provider: domain.ProviderCodex,
		Status: status, CreatedAt: now, UpdatedAt: now,
	}
}

func assertErrorCode(t *testing.T, response *httptest.ResponseRecorder, want string) {
	t.Helper()
	var body errorEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if body.Error.Code != want || body.Error.Message == "" {
		t.Fatalf("error response = %#v, want code %q", body.Error, want)
	}
}

type fakeCoordinator struct {
	mu      sync.Mutex
	task    domain.Task
	ctx     context.Context
	session domain.Session
	err     error
}

func (f *fakeCoordinator) CreateAndStart(ctx context.Context, task domain.Task) (domain.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.task, f.ctx = task, ctx
	if err := task.Validate(); err != nil {
		return domain.Session{}, err
	}
	return f.session, f.err
}

type fakeSessions struct {
	mu     sync.Mutex
	values map[domain.SessionID]domain.Session
	getErr error
}

func (f *fakeSessions) Get(_ context.Context, id domain.SessionID) (domain.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return domain.Session{}, f.getErr
	}
	session, ok := f.values[id]
	if !ok {
		return domain.Session{}, domain.ErrSessionNotFound
	}
	return session, nil
}

func (f *fakeSessions) List(_ context.Context) ([]domain.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	items := make([]domain.Session, 0, len(f.values))
	for _, value := range f.values {
		items = append(items, value)
	}
	return items, nil
}

type staticProviderCatalog map[domain.AgentProvider]bool

func (s staticProviderCatalog) Has(name domain.AgentProvider) bool { return s[name] }

func (f *fakeSessions) Interrupt(_ context.Context, id domain.SessionID) (domain.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	session, ok := f.values[id]
	if !ok {
		return domain.Session{}, domain.ErrSessionNotFound
	}
	session.Status = domain.SessionInterrupted
	session.UpdatedAt = session.UpdatedAt.Add(time.Second)
	f.values[id] = session
	return session, nil
}

type fakeEvents struct {
	mu           sync.Mutex
	channel      chan domain.AgentEvent
	requested    chan context.Context
	stream       *fakeSubscription
	subscribeErr error
	calls        int
}

func newFakeEvents() *fakeEvents {
	return &fakeEvents{
		channel: make(chan domain.AgentEvent, 4), requested: make(chan context.Context, 1),
		stream: &fakeSubscription{events: make(chan domain.AgentEvent, 4), done: make(chan struct{})},
	}
}

func (f *fakeEvents) Publish(context.Context, domain.AgentEvent) error { return nil }

func (f *fakeEvents) Subscribe(ctx context.Context, _ domain.SessionID) (<-chan domain.AgentEvent, error) {
	f.mu.Lock()
	f.calls++
	defer f.mu.Unlock()
	f.requested <- ctx
	return f.channel, f.subscribeErr
}

func (f *fakeEvents) SubscribeStream(ctx context.Context, _ domain.SessionID) (ports.EventSubscription, error) {
	f.mu.Lock()
	f.calls++
	defer f.mu.Unlock()
	f.requested <- ctx
	return f.stream, f.subscribeErr
}

type fakeSubscription struct {
	mu        sync.Mutex
	events    chan domain.AgentEvent
	done      chan struct{}
	err       error
	cancelled bool
}

func (f *fakeSubscription) Events() <-chan domain.AgentEvent { return f.events }
func (f *fakeSubscription) Done() <-chan struct{}            { return f.done }
func (f *fakeSubscription) Err() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}
func (f *fakeSubscription) Cancel() {
	f.mu.Lock()
	if !f.cancelled {
		f.cancelled = true
		close(f.done)
		close(f.events)
	}
	f.mu.Unlock()
}
func (f *fakeSubscription) finish(err error) {
	f.mu.Lock()
	f.err = err
	if !f.cancelled {
		f.cancelled = true
		close(f.done)
		close(f.events)
	}
	f.mu.Unlock()
}

var _ ports.EventPublisher = (*fakeEvents)(nil)
