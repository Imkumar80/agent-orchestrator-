package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestHelpVersionAndServerURLConfiguration(t *testing.T) {
	var out strings.Builder
	r := Runner{Out: &out, Err: io.Discard, LookupEnv: func(key string) (string, bool) {
		if key == "ORCHESTRATOR_SERVER_URL" {
			return "http://example.invalid", true
		}
		return "", false
	}}
	if err := r.Run(context.Background(), nil); err != nil || !strings.Contains(out.String(), "session logs") {
		t.Fatalf("help = %q, %v", out.String(), err)
	}
	out.Reset()
	if err := r.Run(context.Background(), []string{"version"}); err != nil || !strings.Contains(out.String(), Version) {
		t.Fatalf("version=%q err=%v", out.String(), err)
	}
	out.Reset()
	if err := r.Run(context.Background(), []string{"doctor"}); err == nil || !strings.Contains(err.Error(), "example.invalid") {
		t.Fatalf("doctor should use environment URL, got %v", err)
	}
}

func TestTaskCreateAndValidation(t *testing.T) {
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/v1/tasks" {
			t.Errorf("request %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Error(err)
		}
		requestTask := received["task"].(map[string]any)
		if requestTask["provider"] == "opencode" {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"error":{"code":"provider_unavailable","message":"The requested provider is not enabled."}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"accepted":true,"session":{"id":"sess_1","task_id":"task_1","provider":"codex","status":"running","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}}`)
	}))
	defer server.Close()
	var out strings.Builder
	r := Runner{Out: &out, Err: io.Discard, LookupEnv: emptyEnv}
	if err := r.Run(context.Background(), []string{"--server", server.URL, "task", "create", "--description", "Analyze", "--workdir", ".", "--json"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"accepted": true`) || !strings.Contains(out.String(), "sess_1") {
		t.Fatalf("output=%q", out.String())
	}
	task := received["task"].(map[string]any)
	if task["working_directory"] == "." || task["created_at"] != nil {
		t.Fatalf("request task=%#v", task)
	}
	if err := r.Run(context.Background(), []string{"task", "create", "--provider", "codex"}); err == nil {
		t.Fatal("missing description should fail")
	}
	if err := r.Run(context.Background(), []string{"--server", server.URL, "task", "create", "--description", "x", "--provider", "opencode"}); err == nil || !strings.Contains(err.Error(), "provider is not enabled") {
		t.Fatalf("unsupported provider error=%v", err)
	}
}

func TestSessionProviderDoctorCommands(t *testing.T) {
	now := time.Now().UTC().Format(time.RFC3339)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			_, _ = io.WriteString(w, `{"status":"ok"}`)
		case "/api/v1/sessions":
			_, _ = io.WriteString(w, `{"sessions":[]}`)
		case "/api/v1/providers":
			_, _ = io.WriteString(w, `{"providers":[{"name":"codex","available":true},{"name":"hermes","available":false}]}`)
		case "/api/v1/sessions/s1":
			_, _ = io.WriteString(w, `{"session":{"id":"s1","task_id":"t1","provider":"codex","status":"completed","created_at":"`+now+`","updated_at":"`+now+`"}}`)
		case "/api/v1/sessions/s1/cancel":
			_, _ = io.WriteString(w, `{"already_terminal":true,"session":{"id":"s1","status":"completed"}}`)
		case "/api/v1/sessions/missing":
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":{"code":"session_not_found","message":"The session was not found."}}`)
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	for _, args := range [][]string{{"session", "get", "s1"}, {"session", "cancel", "s1"}, {"session", "list"}, {"provider", "list"}, {"doctor"}} {
		var out strings.Builder
		r := Runner{Out: &out, Err: io.Discard, LookupEnv: func(string) (string, bool) { return server.URL, true }}
		if err := r.Run(context.Background(), args); err != nil {
			t.Errorf("%v: %v", args, err)
		}
	}
	var out strings.Builder
	r := Runner{Out: &out, Err: io.Discard, LookupEnv: func(string) (string, bool) { return server.URL, true }}
	if err := r.Run(context.Background(), []string{"session", "get", "missing"}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing session error=%v", err)
	}
}

func TestLogsStreamsMultipleEventsAndStopsOnDisconnect(t *testing.T) {
	streamClosed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for _, event := range []string{"event: provider.output\ndata: {\"id\":\"1\",\"type\":\"provider.output\",\"payload\":{\"n\":1}}\n\n", "event: process.completed\ndata: {\"id\":\"2\",\"type\":\"process.completed\",\"payload\":{\"status\":\"completed\"}}\n\n"} {
			if _, err := io.WriteString(w, event); err != nil {
				return
			}
			flusher.Flush()
		}
		<-r.Context().Done()
		close(streamClosed)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	out := &signalWriter{changed: make(chan struct{}, 8)}
	r := Runner{Out: out, Err: io.Discard, LookupEnv: func(string) (string, bool) { return server.URL, true }}
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx, []string{"session", "logs", "s1"}) }()
	deadline := time.After(3 * time.Second)
	for !strings.Contains(out.String(), "process.completed") {
		select {
		case <-deadline:
			t.Fatalf("events not displayed: %s", out.String())
		case <-out.changed:
		}
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("logs result=%v", err)
	}
	select {
	case <-streamClosed:
	case <-time.After(time.Second):
		t.Fatal("event response did not close after cancellation")
	}
}

func emptyEnv(string) (string, bool) { return "", false }

type signalWriter struct {
	mu      sync.Mutex
	value   strings.Builder
	changed chan struct{}
}

func (w *signalWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	n, err := w.value.Write(p)
	w.mu.Unlock()
	select {
	case w.changed <- struct{}{}:
	default:
	}
	return n, err
}
func (w *signalWriter) String() string { w.mu.Lock(); defer w.mu.Unlock(); return w.value.String() }
