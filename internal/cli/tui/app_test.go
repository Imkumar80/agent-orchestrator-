package tui

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Imkumar80/agent-orchestrator-/internal/cli/client"
	"github.com/Imkumar80/agent-orchestrator-/internal/domain"
)

func TestRootModelInitializationAndNavigation(t *testing.T) {
	m := NewModel(nil, "http://127.0.0.1:8080", "test")
	if m.screen != ScreenDashboard || m.loading != true || m.state != connectionLoading {
		t.Fatalf("initial model = screen %v loading %v state %v", m.screen, m.loading, m.state)
	}
	m = updateModel(t, m, keyMsg('n'))
	if m.screen != ScreenNewTask || m.cursor != int(ScreenNewTask) {
		t.Fatalf("n navigation = screen %v cursor %d", m.screen, m.cursor)
	}
	m = updateModel(t, m, keyMsg(tea.KeyEscape))
	if m.screen != ScreenDashboard {
		t.Fatalf("escape returned to %v", m.screen)
	}
	m = updateModel(t, m, keyMsg(tea.KeyDown))
	if m.cursor != 1 {
		t.Fatalf("down cursor = %d, want 1", m.cursor)
	}
	m = updateModel(t, m, keyMsg(tea.KeyEnter))
	if m.screen != ScreenNewTask {
		t.Fatalf("enter opened %v, want new task", m.screen)
	}
}

func TestKeyboardHelpAndGracefulQuit(t *testing.T) {
	m := NewModel(nil, "server", "test")
	m = updateModel(t, m, keyMsg('?'))
	if !m.showHelp || !strings.Contains(m.View().Content, "Keyboard help") {
		t.Fatal("help overlay did not open")
	}
	m = updateModel(t, m, keyMsg(tea.KeyEscape))
	if m.showHelp {
		t.Fatal("escape did not close help")
	}
	_, cmd := m.Update(keyMsg('q'))
	if cmd == nil {
		t.Fatal("q did not request graceful Bubble Tea quit")
	}
}

func TestProgramExitsCleanlyOnQuitKey(t *testing.T) {
	program := tea.NewProgram(
		NewModel(nil, "server", "test"),
		tea.WithInput(strings.NewReader("q")),
		tea.WithOutput(io.Discard),
		tea.WithoutSignalHandler(),
	)
	if _, err := program.Run(); err != nil {
		t.Fatalf("program did not exit cleanly: %v", err)
	}
}

func TestGlobalShortcutsDoNotConsumeFocusedInput(t *testing.T) {
	m := NewModel(nil, "server", "test")
	m.inputFocused = true
	for _, key := range []string{"n", "r", "q", "?", "esc"} {
		updated, cmd := m.updateKey(key)
		if cmd != nil {
			t.Fatalf("shortcut %q ran a global command while input was focused", key)
		}
		m = updated
		if m.screen != ScreenDashboard || m.showHelp {
			t.Fatalf("shortcut %q changed shell navigation while input was focused", key)
		}
	}
}

func TestRefreshShortcutStartsLoading(t *testing.T) {
	m := NewModel(nil, "server", "test")
	m.loading = false
	m = updateModel(t, m, dashboardLoadedMsg{requestID: m.activeRequest, health: client.HealthResponse{Status: "ok"}, reachable: true})
	updated, cmd := m.Update(keyMsg('r'))
	if cmd == nil {
		t.Fatal("r did not request a refresh")
	}
	refreshed, ok := updated.(Model)
	if !ok || !refreshed.loading {
		t.Fatal("refresh did not set loading state")
	}
}

func TestTerminalResizeUpdatesResponsiveLayout(t *testing.T) {
	m := NewModel(nil, "server", "test")
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 72, Height: 20})
	if m.width != 72 || m.height != 20 {
		t.Fatalf("size = %dx%d", m.width, m.height)
	}
	compact := m.View().Content
	if strings.Contains(compact, "WORKSPACE") {
		t.Fatal("compact layout should use horizontal navigation")
	}
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	if !strings.Contains(m.View().Content, "WORKSPACE") {
		t.Fatal("wide layout should show navigation rail")
	}
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 40, Height: 12})
	if !strings.Contains(m.View().Content, "Resize to at least 48 columns") {
		t.Fatal("very narrow layout should show a readable resize hint")
	}
}

func TestDashboardFetchesHealthSessionsAndProvidersAndRendersData(t *testing.T) {
	updated := time.Date(2026, time.November, 17, 10, 0, 0, 0, time.UTC).Format(time.RFC3339)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/health":
			_, _ = io.WriteString(w, `{"status":"ok"}`)
		case "/api/v1/sessions":
			_, _ = io.WriteString(w, `{"sessions":[{"id":"ses_running_12345678901234567890","task_id":"task_a","provider":"codex","status":"running","created_at":"`+updated+`","updated_at":"`+updated+`"},{"id":"ses_done","task_id":"task_b","provider":"codex","status":"completed","created_at":"`+updated+`","updated_at":"`+updated+`"}]}`)
		case "/api/v1/providers":
			_, _ = io.WriteString(w, `{"providers":[{"name":"codex","available":true},{"name":"opencode","available":false}]}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	api, err := client.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	m := NewModel(api, server.URL, "0.1.0")
	msg := m.refreshCmd(m.activeRequest)().(dashboardLoadedMsg)
	m = updateModel(t, m, msg)
	if m.state != connectionConnected || m.loading || len(m.sessions) != 2 || len(m.providers) != 2 {
		t.Fatalf("loaded model state=%v loading=%v sessions=%d providers=%d err=%v", m.state, m.loading, len(m.sessions), len(m.providers), m.err)
	}
	view := m.View().Content
	for _, expected := range []string{"AGENT ORCHESTRATOR", server.URL, "v0.1.0", "ACTIVE SESSIONS", "TOTAL SESSIONS", "PROVIDERS READY", "1", "2", "ses_running_12345678901234567890"[:21], "running", "completed"} {
		if !strings.Contains(view, expected) {
			t.Errorf("dashboard missing %q in:\n%s", expected, view)
		}
	}
	if m.sessions[0].ID != "ses_running_12345678901234567890" {
		t.Fatalf("recent sessions not sorted newest first: %#v", m.sessions)
	}
}

func TestDashboardDisconnectedAndEmptyStates(t *testing.T) {
	api, err := client.New("http://127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	m := NewModel(api, api.BaseURL(), "0.1.0")
	m = updateModel(t, m, m.refreshCmd(m.activeRequest)())
	if m.state != connectionDisconnected || !strings.Contains(m.View().Content, "Server is disconnected") {
		t.Fatalf("disconnected state not rendered: state=%v view=%s", m.state, m.View().Content)
	}
	m, _ = m.beginRefresh()
	m = updateModel(t, m, dashboardLoadedMsg{
		requestID: m.activeRequest, health: client.HealthResponse{Status: "ok"},
		sessions: []domain.Session{}, providers: []client.Provider{}, reachable: true,
	})
	if !strings.Contains(m.View().Content, "No sessions yet") {
		t.Fatalf("empty dashboard missing empty state: %s", m.View().Content)
	}
}

func TestSlowRefreshSkipsFiveSecondTickAndManualRefresh(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var healthCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/health":
			if healthCalls.Add(1) == 1 {
				started <- struct{}{}
				<-release
			}
			_, _ = io.WriteString(w, `{"status":"ok"}`)
		case "/api/v1/sessions":
			_, _ = io.WriteString(w, `{"sessions":[]}`)
		case "/api/v1/providers":
			_, _ = io.WriteString(w, `{"providers":[]}`)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	api, err := client.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	m := NewModel(api, server.URL, "test")
	result := make(chan tea.Msg, 1)
	go func() { result <- m.refreshCmd(m.activeRequest)() }()
	waitForSignal(t, started, "initial health request")

	updated, tick := m.Update(refreshTickMsg{}) // The five-second poll message.
	m = updated.(Model)
	if tick == nil || !m.refreshing {
		t.Fatal("poll tick did not preserve the active refresh and schedule the next tick")
	}
	next, manual := m.updateKey("r")
	m = next
	if manual != nil || !m.refreshing {
		t.Fatal("manual refresh should be ignored while another refresh is active")
	}
	if calls := healthCalls.Load(); calls != 1 {
		t.Fatalf("started %d health requests during one active refresh, want 1", calls)
	}

	close(release)
	response := waitForResult(t, result)
	m = updateModel(t, m, response)
	if m.refreshing || m.state != connectionConnected {
		t.Fatalf("refresh did not finish cleanly: refreshing=%v state=%v", m.refreshing, m.state)
	}
	if refreshInterval != 5*time.Second {
		t.Fatalf("refresh interval changed to %s", refreshInterval)
	}
}

func TestObsoleteRefreshResponseCannotOverwriteNewRequestOrNavigation(t *testing.T) {
	m := NewModel(nil, "server", "test")
	m.screen = ScreenProviders
	m.cursor = int(ScreenProviders)
	m = updateModel(t, m, dashboardLoadedMsg{
		requestID: 1, health: client.HealthResponse{Status: "ok"},
		sessions: []domain.Session{{ID: "ses_old", Status: domain.SessionCompleted}}, reachable: true,
	})
	if m.screen != ScreenProviders || m.cursor != int(ScreenProviders) {
		t.Fatal("refresh result changed screen or navigation selection")
	}
	updated, cmd := m.updateKey("r")
	m = updated
	if cmd == nil || m.activeRequest != 2 || !m.refreshing {
		t.Fatalf("new refresh not started: request=%d refreshing=%v", m.activeRequest, m.refreshing)
	}
	before := m
	m = updateModel(t, m, dashboardLoadedMsg{
		requestID: 1, health: client.HealthResponse{Status: "ok"},
		sessions: []domain.Session{{ID: "ses_stale", Status: domain.SessionFailed}}, reachable: true,
	})
	if !m.refreshing || m.activeRequest != 2 || len(m.sessions) != 1 || m.sessions[0].ID != "ses_old" {
		t.Fatalf("obsolete response changed current state: %#v", m)
	}
	if m.screen != before.screen || m.cursor != before.cursor {
		t.Fatal("obsolete response changed navigation")
	}
	m = updateModel(t, m, dashboardLoadedMsg{
		requestID: 2, health: client.HealthResponse{Status: "ok"},
		sessions: []domain.Session{{ID: "ses_new", Status: domain.SessionRunning}}, reachable: true,
	})
	if m.refreshing || m.sessions[0].ID != "ses_new" || m.screen != ScreenProviders || m.cursor != int(ScreenProviders) {
		t.Fatalf("current response did not update data while preserving navigation: %#v", m)
	}
}

func TestRefreshFailureAllowsNextRefreshToSucceed(t *testing.T) {
	var healthCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/health":
			if healthCalls.Add(1) == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = io.WriteString(w, `{"error":{"code":"unavailable","message":"offline"}}`)
				return
			}
			_, _ = io.WriteString(w, `{"status":"ok"}`)
		case "/api/v1/sessions":
			_, _ = io.WriteString(w, `{"sessions":[]}`)
		case "/api/v1/providers":
			_, _ = io.WriteString(w, `{"providers":[]}`)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	api, err := client.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	m := NewModel(api, server.URL, "test")
	m = updateModel(t, m, m.refreshCmd(m.activeRequest)())
	if m.refreshing || m.state != connectionDisconnected || m.err == nil {
		t.Fatalf("failed refresh state: refreshing=%v state=%v err=%v", m.refreshing, m.state, m.err)
	}
	updated, retry := m.updateKey("r")
	m = updated
	if retry == nil || !m.refreshing {
		t.Fatal("refresh failure left future refreshes blocked")
	}
	m = updateModel(t, m, retry())
	if m.refreshing || m.state != connectionConnected || m.err != nil || healthCalls.Load() != 2 {
		t.Fatalf("retry did not recover: refreshing=%v state=%v err=%v health_calls=%d", m.refreshing, m.state, m.err, healthCalls.Load())
	}
}

func TestRefreshRequestStopsWhenModelContextIsCancelled(t *testing.T) {
	started := make(chan struct{}, 1)
	serverObservedCancel := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Errorf("unexpected request after cancellation: %s", r.URL.Path)
			return
		}
		started <- struct{}{}
		<-r.Context().Done()
		close(serverObservedCancel)
	}))
	defer server.Close()
	api, err := client.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := NewModel(api, server.URL, "test")
	m.ctx = ctx
	result := make(chan tea.Msg, 1)
	go func() { result <- m.refreshCmd(m.activeRequest)() }()
	waitForSignal(t, started, "cancellable health request")
	cancel()
	response := waitForResult(t, result).(dashboardLoadedMsg)
	if !errors.Is(response.err, context.Canceled) {
		t.Fatalf("cancelled refresh error = %v, want context.Canceled", response.err)
	}
	select {
	case <-serverObservedCancel:
	case <-time.After(3 * time.Second):
		t.Fatal("server did not observe request cancellation")
	}
	m = updateModel(t, m, response)
	if m.refreshing {
		t.Fatal("cancelled refresh left the in-flight guard set")
	}
}

func updateModel(t *testing.T, model Model, msg tea.Msg) Model {
	t.Helper()
	updated, _ := model.Update(msg)
	result, ok := updated.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want tui.Model", updated)
	}
	return result
}

func keyMsg(code rune) tea.KeyPressMsg { return tea.KeyPressMsg(tea.Key{Code: code}) }

func waitForSignal(t *testing.T, signal <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %s", label)
	}
}

func waitForResult(t *testing.T, result <-chan tea.Msg) tea.Msg {
	t.Helper()
	select {
	case msg := <-result:
		return msg
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for refresh result")
		return nil
	}
}
