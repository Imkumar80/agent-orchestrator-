package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Imkumar80/agent-orchestrator-/internal/domain"
	"github.com/Imkumar80/agent-orchestrator-/internal/events"
	"github.com/Imkumar80/agent-orchestrator-/internal/orchestrator"
	"github.com/Imkumar80/agent-orchestrator-/internal/ports"
)

func TestSSEHeadersAndMultipleEvents(t *testing.T) {
	handler, _, sessions, publisher := newTestHandler(t)
	sessions.values["ses-stream"] = validSession("ses-stream", domain.SessionRunning)
	publisher.stream.events <- streamTestEvent("evt-1", "provider.output", json.RawMessage(`{"stream":"stdout"}`))
	publisher.stream.events <- streamTestEvent("evt-2", "process.completed", json.RawMessage(`{"status":"completed"}`))
	publisher.stream.finish(nil)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/sessions/ses-stream/events", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("SSE status = %d: %s", response.Code, response.Body)
	}
	if response.Header().Get("Content-Type") != "text/event-stream" ||
		response.Header().Get("Cache-Control") != "no-cache" ||
		response.Header().Get("Connection") != "keep-alive" {
		t.Fatalf("SSE headers = %#v", response.Header())
	}
	body := response.Body.String()
	if !strings.Contains(body, "id: evt-1\nevent: provider.output\ndata: ") ||
		!strings.Contains(body, "id: evt-2\nevent: process.completed\ndata: ") {
		t.Fatalf("SSE event frames missing from body: %s", body)
	}
	for _, block := range strings.Split(strings.TrimSpace(body), "\n\n") {
		for _, line := range strings.Split(block, "\n") {
			if strings.HasPrefix(line, "data: ") {
				var event domain.AgentEvent
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
					t.Fatalf("decode SSE data JSON: %v", err)
				}
				if !json.Valid(event.Payload) {
					t.Fatalf("event payload is not valid JSON: %s", event.Payload)
				}
			}
		}
	}
	if !publisher.stream.cancelled {
		t.Fatal("SSE handler did not cancel detailed subscription")
	}
}

func TestSSEClientDisconnectCancelsSubscription(t *testing.T) {
	handler, _, sessions, publisher := newTestHandler(t)
	sessions.values["ses-disconnect"] = validSession("ses-disconnect", domain.SessionRunning)
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/ses-disconnect/events", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(response, request)
		close(done)
	}()
	var subCtx context.Context
	select {
	case subCtx = <-publisher.requested:
	case <-time.After(3 * time.Second):
		t.Fatal("event subscription was not started")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("SSE handler did not stop after client cancellation")
	}
	select {
	case <-subCtx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("subscription context was not cancelled")
	}
	if !publisher.stream.cancelled {
		t.Fatal("detailed event subscription was not released")
	}
}

func TestSSESlowSubscriberLogsTerminationReason(t *testing.T) {
	var logs strings.Builder
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	handler, _, sessions, publisher := newTestHandlerWithLogger(t, logger)
	sessions.values["ses-slow"] = validSession("ses-slow", domain.SessionRunning)
	publisher.stream.finish(events.ErrSlowConsumer)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/sessions/ses-slow/events", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("SSE status = %d", response.Code)
	}
	if !strings.Contains(logs.String(), events.ErrSlowConsumer.Error()) {
		t.Fatalf("slow-consumer termination not logged: %s", logs.String())
	}
}

func TestSSEMissingSessionDoesNotSubscribe(t *testing.T) {
	handler, _, _, publisher := newTestHandler(t)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/sessions/missing/events", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing session SSE status = %d", response.Code)
	}
	publisher.mu.Lock()
	calls := publisher.calls
	publisher.mu.Unlock()
	if calls != 0 {
		t.Fatalf("Subscribe was called %d times for missing session", calls)
	}
}

func TestSSEPublisherWithoutDiagnosticsStillStreams(t *testing.T) {
	sessions := &fakeSessions{values: map[domain.SessionID]domain.Session{
		"ses-basic": validSession("ses-basic", domain.SessionRunning),
	}}
	publisher := &basicFakeEvents{channel: make(chan domain.AgentEvent, 1)}
	publisher.channel <- streamTestEvent("evt-basic", "provider.output", json.RawMessage(`"opaque"`))
	close(publisher.channel)
	coordinator := &fakeCoordinator{}
	server, err := NewServer(Config{}, Dependencies{
		Coordinator: mustCoordinator(t, coordinator), Sessions: sessions, Events: publisher, ExecutionContext: context.Background(),
	})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/sessions/ses-basic/events", nil))
	if !strings.Contains(response.Body.String(), "id: evt-basic") {
		t.Fatalf("basic publisher event not streamed: %s", response.Body)
	}
}

func TestSSEEventFieldsCannotInjectFrames(t *testing.T) {
	event := streamTestEvent("bad\nid", "bad\nevent", json.RawMessage(`null`))
	response := httptest.NewRecorder()
	if err := writeSSE(response, response, event); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(response.Body.String(), "id: bad") || strings.Contains(response.Body.String(), "event: bad") {
		t.Fatalf("unsafe SSE field was emitted: %q", response.Body.String())
	}
}

func newTestHandlerWithLogger(t *testing.T, logger *slog.Logger) (http.Handler, *fakeCoordinator, *fakeSessions, *fakeEvents) {
	t.Helper()
	sessions := &fakeSessions{values: make(map[domain.SessionID]domain.Session)}
	coordinator := &fakeCoordinator{session: validSession("ses-new", domain.SessionRunning)}
	publisher := newFakeEvents()
	wrapped, err := orchestrator.NewCoordinator(nil, coordinator)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(Config{Logger: logger}, Dependencies{
		Coordinator: wrapped, Sessions: sessions, Events: publisher, ExecutionContext: context.Background(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return server.Handler(), coordinator, sessions, publisher
}

func streamTestEvent(id, eventType string, payload json.RawMessage) domain.AgentEvent {
	return domain.AgentEvent{
		ID: id, SessionID: "ses-stream", Type: domain.AgentEventType(eventType),
		Timestamp: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Payload: payload,
	}
}

type basicFakeEvents struct{ channel chan domain.AgentEvent }

func (*basicFakeEvents) Publish(context.Context, domain.AgentEvent) error { return nil }
func (f *basicFakeEvents) Subscribe(context.Context, domain.SessionID) (<-chan domain.AgentEvent, error) {
	return f.channel, nil
}

var _ ports.EventPublisher = (*basicFakeEvents)(nil)
