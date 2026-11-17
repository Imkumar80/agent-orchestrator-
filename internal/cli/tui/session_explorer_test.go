package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Imkumar80/agent-orchestrator-/internal/cli/client"
	"github.com/Imkumar80/agent-orchestrator-/internal/domain"
)

func TestSessionsLoadSortStatusAndErrorRendering(t *testing.T) {
	base := time.Date(2026, time.November, 17, 9, 0, 0, 0, time.UTC)
	statuses := []domain.SessionStatus{
		domain.SessionPending, domain.SessionStarting, domain.SessionRunning,
		domain.SessionWaitingForInput, domain.SessionCompleted, domain.SessionFailed, domain.SessionInterrupted,
	}
	items := make([]domain.Session, 0, len(statuses))
	for i, status := range statuses {
		item := explorerSession(fmt.Sprintf("ses-%d", i), status, base.Add(time.Duration(i)*time.Minute))
		if status == domain.SessionFailed {
			item.Error = &domain.ErrorInfo{Code: "provider_execution_failed", Message: "safe failure detail"}
		}
		items = append(items, item)
	}
	api := &taskFormAPI{listSessions: func(context.Context) ([]domain.Session, error) { return items, nil }}
	m := loadSessionsForTest(t, api)
	if len(m.sessionList.items) != len(statuses) || m.sessionList.items[0].ID != "ses-6" {
		t.Fatalf("sessions were not sorted newest first: %#v", m.sessionList.items)
	}
	view := m.renderSessions(110)
	for _, value := range []string{"pending", "starting", "running", "waiting_for_input", "completed", "failed", "interrupted / cancelled", "provider_execution_failed", "safe failure detail", "CREATED", "UPDATED"} {
		if !strings.Contains(view, value) {
			t.Errorf("sessions view missing %q:\n%s", value, view)
		}
	}
}

func TestSessionsEmptyDisconnectedAndDataErrorStates(t *testing.T) {
	initial := NewModel(&taskFormAPI{}, "server", "test")
	initial, load := initial.openSessions()
	if !initial.sessionList.loading || !strings.Contains(initial.renderSessions(90), "Loading sessions") || load == nil {
		t.Fatal("initial loading state was not shown")
	}
	t.Run("empty", func(t *testing.T) {
		m := loadSessionsForTest(t, &taskFormAPI{})
		if !strings.Contains(m.renderSessions(90), "No sessions yet") {
			t.Fatal("empty list state was not shown")
		}
	})
	t.Run("disconnected", func(t *testing.T) {
		api := &taskFormAPI{listSessions: func(context.Context) ([]domain.Session, error) { return nil, errors.New("connection refused") }}
		m := loadSessionsForTest(t, api)
		view := m.renderSessions(90)
		if !m.sessionList.disconnected || !strings.Contains(view, "Server disconnected") || !strings.Contains(view, "connection refused") {
			t.Fatalf("disconnected state missing: %s", view)
		}
	})
	t.Run("server error", func(t *testing.T) {
		api := &taskFormAPI{listSessions: func(context.Context) ([]domain.Session, error) {
			return nil, &client.APIError{Status: 500, Code: "internal_error", Message: "failed"}
		}}
		m := loadSessionsForTest(t, api)
		view := m.renderSessions(90)
		if m.sessionList.disconnected || !strings.Contains(view, "Session data could not be loaded") {
			t.Fatalf("data error state misclassified: %s", view)
		}
	})
}

func TestSessionsRefreshPreservesSelectionAndClampsMissingSession(t *testing.T) {
	first := []domain.Session{
		explorerSession("ses-new", domain.SessionRunning, time.Now().UTC()),
		explorerSession("ses-mid", domain.SessionCompleted, time.Now().Add(-time.Minute).UTC()),
		explorerSession("ses-old", domain.SessionFailed, time.Now().Add(-2*time.Minute).UTC()),
	}
	var calls atomic.Int32
	api := &taskFormAPI{listSessions: func(context.Context) ([]domain.Session, error) {
		if calls.Add(1) == 1 {
			return first, nil
		}
		return []domain.Session{first[2], first[0], first[1]}, nil
	}}
	m := loadSessionsForTest(t, api)
	// Select the oldest row explicitly, independent of the sort tie-break.
	for i := range m.sessionList.items {
		if m.sessionList.items[i].ID == "ses-old" {
			m.sessionList.selected = i
			m.sessionList.selectedID = m.sessionList.items[i].ID
		}
	}
	selected := m.sessionList.selectedID
	updated, refresh := m.updateSessionsKey("r")
	m = updated
	msg := refresh().(sessionsLoadedMsg)
	m = updateModel(t, m, msg)
	if m.sessionList.selectedID != selected || m.sessionList.items[m.sessionList.selected].ID != selected {
		t.Fatalf("refresh lost selected session %q: selected=%q index=%d", selected, m.sessionList.selectedID, m.sessionList.selected)
	}
	api.listSessions = func(context.Context) ([]domain.Session, error) { return []domain.Session{first[0]}, nil }
	updated, refresh = m.updateSessionsKey("r")
	m = updateModel(t, updated, refresh().(sessionsLoadedMsg))
	if m.sessionList.selected != 0 || m.sessionList.selectedID != "ses-new" {
		t.Fatalf("selection did not clamp after removal: index=%d id=%q", m.sessionList.selected, m.sessionList.selectedID)
	}
}

func TestOpenAndCloseSessionDetailPreservesSelection(t *testing.T) {
	selected := explorerSession("ses-selected", domain.SessionCompleted, time.Now().UTC())
	other := explorerSession("ses-other", domain.SessionRunning, time.Now().Add(-time.Minute).UTC())
	var gotID domain.SessionID
	api := &taskFormAPI{
		listSessions: func(context.Context) ([]domain.Session, error) { return []domain.Session{selected, other}, nil },
		get: func(_ context.Context, id domain.SessionID) (domain.Session, error) {
			gotID = id
			if id == other.ID {
				return other, nil
			}
			return selected, nil
		},
	}
	m := loadSessionsForTest(t, api)
	m.moveSessionSelection(1)
	selectedID := m.sessionList.selectedID
	updated, load := m.Update(keyMsg(tea.KeyEnter))
	m = updated.(Model)
	if m.screen != ScreenSessionDetail || load == nil || gotID != "" {
		t.Fatalf("detail route was not opened asynchronously: screen=%v getID=%q", m.screen, gotID)
	}
	updated, next := m.Update(load().(tea.Msg))
	m = updated.(Model)
	if gotID != selectedID || m.detail.session.ID != selectedID || next == nil {
		t.Fatalf("detail loaded wrong session: got=%q detail=%q selected=%q next=%v", gotID, m.detail.session.ID, selectedID, next != nil)
	}
	m.cancelDetailWork()
	updated, listLoad := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	m = updated.(Model)
	if m.screen != ScreenSessions || m.sessionList.selectedID != selectedID || listLoad == nil {
		t.Fatalf("return to Sessions lost selection: screen=%v selected=%q", m.screen, m.sessionList.selectedID)
	}
}

func TestSessionDetailRendersAvailableMetadataOnly(t *testing.T) {
	session := explorerSession("ses-detail", domain.SessionFailed, time.Date(2026, 11, 17, 12, 30, 0, 0, time.UTC))
	session.TaskID = "task-detail"
	session.NativeProviderSessionID = "native-123"
	session.Error = &domain.ErrorInfo{Code: "provider_execution_failed", Message: "provider exited"}
	m := NewModel(nil, "server", "test")
	m.screen = ScreenSessionDetail
	m.detail.session = session
	m.detail.loaded = true
	view := m.renderSessionDetail(110)
	for _, want := range []string{"ses-detail", "task-detail", "codex", "native-123", "failed", "provider_execution_failed", "provider exited"} {
		if !strings.Contains(view, want) {
			t.Errorf("detail view missing %q:\n%s", want, view)
		}
	}
	for _, unavailable := range []string{"working directory", "task description", "historical logs"} {
		if strings.Contains(strings.ToLower(view), unavailable) {
			t.Errorf("detail view invented unavailable field %q", unavailable)
		}
	}
}

func TestSessionDetailManualRefreshLoadsAuthoritativeState(t *testing.T) {
	var calls atomic.Int32
	api := &taskFormAPI{get: func(context.Context, domain.SessionID) (domain.Session, error) {
		if calls.Add(1) == 1 {
			return explorerSession("ses-refresh", domain.SessionRunning, time.Now()), nil
		}
		return explorerSession("ses-refresh", domain.SessionCompleted, time.Now().Add(time.Second)), nil
	}}
	m, _ := openDetailForTest(t, api, "ses-refresh")
	updated, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: 'r', Text: "r"}))
	m = updated.(Model)
	if cmd == nil || !m.detail.loading {
		t.Fatal("r did not start an authoritative detail refresh")
	}
	loaded := cmd().(sessionDetailLoadedMsg)
	updated, _ = m.Update(loaded)
	m = updated.(Model)
	if calls.Load() != 2 || m.detail.session.Status != domain.SessionCompleted {
		t.Fatalf("manual refresh did not update session state: calls=%d status=%s", calls.Load(), m.detail.session.Status)
	}
	m.cancelDetailWork()
}

func TestStreamErrorIsVisibleAndRefreshesSession(t *testing.T) {
	var lookups atomic.Int32
	streamReturned := make(chan struct{})
	api := &taskFormAPI{
		get: func(context.Context, domain.SessionID) (domain.Session, error) {
			lookups.Add(1)
			return explorerSession("ses-stream-error", domain.SessionRunning, time.Now()), nil
		},
		stream: func(context.Context, domain.SessionID, func(client.Event) error) error {
			close(streamReturned)
			return errors.New("stream reset")
		},
	}
	m, streamCmd := openDetailForTest(t, api, "ses-stream-error")
	start, wait := splitBatch(t, streamCmd)
	m = updateModel(t, m, start())
	waitTaskSignal(t, streamReturned, "stream failure")
	ended := wait()
	updated, refresh := m.Update(ended)
	m = updated.(Model)
	if refresh == nil || m.detail.streamState != detailStreamFailed || !strings.Contains(m.renderSessionDetail(110), "Stream ended with error: stream reset") {
		t.Fatalf("stream failure was not visible or did not refresh state:\n%s", m.renderSessionDetail(110))
	}
	updated, _ = m.Update(refresh().(sessionDetailLoadedMsg))
	m = updated.(Model)
	if lookups.Load() != 2 {
		t.Fatalf("session state was not refreshed after stream failure: GetSession calls=%d", lookups.Load())
	}
}

func TestSessionDetailRendersIncomingEventAndBoundsEventList(t *testing.T) {
	m := NewModel(nil, "server", "test")
	m.screen = ScreenSessionDetail
	m.detail.id = "ses-events"
	m.detail.session = explorerSession("ses-events", domain.SessionRunning, time.Now().UTC())
	m.detail.activeStream = 4
	for i := 0; i < maxDetailEvents+7; i++ {
		event := client.Event{ID: fmt.Sprintf("evt-%d", i), SessionID: m.detail.id, Type: "provider.output", Timestamp: time.Now(), Payload: []byte(`{"stream":"stdout","data":{"text":"hello"}}`)}
		m.acceptDetailEvent(detailStreamEventMsg{streamID: 4, event: event})
	}
	if len(m.detail.events) != maxDetailEvents || m.detail.eventsDropped != 7 {
		t.Fatalf("event buffer size=%d dropped=%d", len(m.detail.events), m.detail.eventsDropped)
	}
	view := m.renderSessionDetail(110)
	for _, want := range []string{"provider.output", "hello", "Earlier in-memory events dropped from view: 7"} {
		if !strings.Contains(view, want) {
			t.Errorf("event view missing %q:\n%s", want, view)
		}
	}
	if got := eventPayloadText([]byte(`"plain provider payload"`)); !strings.Contains(got, "plain provider payload") {
		t.Fatalf("non-object payload was not displayed: %q", got)
	}
}

func TestStreamEOFRefreshesAuthoritativeSessionWithoutInferringCompletion(t *testing.T) {
	var lookups atomic.Int32
	api := &taskFormAPI{
		get: func(context.Context, domain.SessionID) (domain.Session, error) {
			if lookups.Add(1) == 1 {
				return explorerSession("ses-eof", domain.SessionRunning, time.Now().UTC()), nil
			}
			return explorerSession("ses-eof", domain.SessionCompleted, time.Now().Add(time.Second).UTC()), nil
		},
		stream: func(context.Context, domain.SessionID, func(client.Event) error) error { return nil },
	}
	m, streamCmd := openDetailForTest(t, api, "ses-eof")
	started, waiting := splitBatch(t, streamCmd)
	startedMsg := started()
	m = updateModel(t, m, startedMsg)
	ended := waiting()
	if msg, ok := ended.(detailStreamEndedMsg); !ok || msg.err != nil {
		t.Fatalf("EOF stream result = %#v", ended)
	}
	updated, refresh := m.Update(ended)
	m = updated.(Model)
	if refresh == nil || m.detail.session.Status != domain.SessionRunning {
		t.Fatalf("EOF incorrectly changed status or skipped state refresh: status=%s refresh=%v", m.detail.session.Status, refresh != nil)
	}
	loaded := refresh().(sessionDetailLoadedMsg)
	m = updateModel(t, m, loaded)
	if lookups.Load() != 2 || m.detail.session.Status != domain.SessionCompleted || m.detail.streamState != detailStreamIdle {
		t.Fatalf("authoritative completion not loaded: lookups=%d status=%s stream=%v", lookups.Load(), m.detail.session.Status, m.detail.streamState)
	}
}

func TestReconnectRefreshesSessionAndRestartsStream(t *testing.T) {
	var lookups, streamStarts atomic.Int32
	firstStarted := make(chan struct{})
	firstStopped := make(chan struct{})
	secondStarted := make(chan struct{})
	secondStopped := make(chan struct{})
	api := &taskFormAPI{
		get: func(context.Context, domain.SessionID) (domain.Session, error) {
			lookups.Add(1)
			return explorerSession("ses-reconnect", domain.SessionRunning, time.Now().UTC()), nil
		},
		stream: func(ctx context.Context, _ domain.SessionID, _ func(client.Event) error) error {
			n := streamStarts.Add(1)
			if n == 1 {
				close(firstStarted)
			}
			if n == 2 {
				close(secondStarted)
			}
			<-ctx.Done()
			if n == 1 {
				close(firstStopped)
			} else if n == 2 {
				close(secondStopped)
			}
			return ctx.Err()
		},
	}
	m, first := openDetailForTest(t, api, "ses-reconnect")
	start, _ := splitBatch(t, first)
	m = updateModel(t, m, start())
	waitTaskSignal(t, firstStarted, "initial SSE stream")
	updated, reconnect := m.Update(tea.KeyPressMsg(tea.Key{Code: 'e', Text: "e"}))
	m = updated.(Model)
	if reconnect == nil {
		t.Fatal("e did not request authoritative refresh and reconnect")
	}
	waitTaskSignal(t, firstStopped, "old stream cancellation")
	loaded := reconnect().(sessionDetailLoadedMsg)
	updated, nextStream := m.Update(loaded)
	m = updated.(Model)
	if lookups.Load() != 2 || nextStream == nil || m.detail.session.Status != domain.SessionRunning {
		t.Fatalf("reconnect did not refresh state: get calls=%d stream=%v status=%s", lookups.Load(), nextStream != nil, m.detail.session.Status)
	}
	start2, _ := splitBatch(t, nextStream)
	_ = start2()
	waitTaskSignal(t, secondStarted, "reconnected SSE stream")
	if m.detail.streamState != detailStreamConnecting {
		t.Fatalf("reconnect stream state = %v", m.detail.streamState)
	}
	// Leaving the route cancels the new stream context.
	updated, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	m = updated.(Model)
	waitTaskSignal(t, secondStopped, "reconnected stream teardown")
}

func TestLeavingDetailCancelsSSEAndStaleMessagesAreIgnored(t *testing.T) {
	streamExited := make(chan struct{})
	api := &taskFormAPI{
		get: func(context.Context, domain.SessionID) (domain.Session, error) {
			return explorerSession("ses-leave", domain.SessionRunning, time.Now().UTC()), nil
		},
		stream: func(ctx context.Context, _ domain.SessionID, _ func(client.Event) error) error {
			<-ctx.Done()
			close(streamExited)
			return ctx.Err()
		},
	}
	m, streamCmd := openDetailForTest(t, api, "ses-leave")
	start, _ := splitBatch(t, streamCmd)
	startedMsg := start()
	m = updateModel(t, m, startedMsg)
	updated, listCmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	m = updated.(Model)
	if m.screen != ScreenSessions || listCmd == nil {
		t.Fatalf("Escape did not return to Sessions: screen=%v", m.screen)
	}
	waitTaskSignal(t, streamExited, "SSE cancellation after leaving detail")
	before := m.detail.session.ID
	updated, _ = m.Update(detailStreamEventMsg{streamID: 1, event: client.Event{ID: "stale", Type: "provider.output"}})
	m = updated.(Model)
	if m.screen != ScreenSessions || m.detail.session.ID != before || len(m.detail.events) != 0 {
		t.Fatal("stale stream event changed the current screen")
	}
}

func TestCancellationEligibilityConfirmationSuccessAndAlreadyTerminal(t *testing.T) {
	t.Run("eligible confirmation and success", func(t *testing.T) {
		var calls atomic.Int32
		api := &taskFormAPI{
			cancel: func(_ context.Context, id domain.SessionID) (client.CancelResponse, error) {
				calls.Add(1)
				return client.CancelResponse{Accepted: true, Session: explorerSession(string(id), domain.SessionInterrupted, time.Now().UTC())}, nil
			},
			get: func(_ context.Context, id domain.SessionID) (domain.Session, error) {
				return explorerSession(string(id), domain.SessionInterrupted, time.Now().UTC()), nil
			},
		}
		m := detailModel(domain.SessionRunning, api)
		updated, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Text: "c"}))
		m = updated.(Model)
		if !m.detail.confirmCancel || calls.Load() != 0 || cmd != nil {
			t.Fatal("c did not require confirmation")
		}
		updated, cmd = m.Update(tea.KeyPressMsg(tea.Key{Code: 'y', Text: "y"}))
		m = updated.(Model)
		if m.detail.cancelling != true || cmd == nil || calls.Load() != 0 {
			t.Fatal("confirmation did not begin asynchronous cancellation")
		}
		if !strings.Contains(m.renderSessionDetail(110), "Cancelling") {
			t.Fatal("cancellation progress was not displayed")
		}
		result := cmd().(sessionCancelResultMsg)
		updated, refresh := m.Update(result)
		m = updated.(Model)
		if !strings.Contains(m.detail.cancelNote, "Cancellation confirmed") || m.detail.cancelling || refresh == nil {
			t.Fatalf("success was not shown after confirmation: note=%q cancelling=%v refresh=%v", m.detail.cancelNote, m.detail.cancelling, refresh != nil)
		}
		m = updateModel(t, m, refresh().(sessionDetailLoadedMsg))
		if calls.Load() != 1 || m.detail.session.Status != domain.SessionInterrupted || !strings.Contains(m.renderSessionDetail(110), "interrupted") {
			t.Fatalf("cancellation outcome not reflected: calls=%d state=%s", calls.Load(), m.detail.session.Status)
		}
	})
	t.Run("already terminal", func(t *testing.T) {
		api := &taskFormAPI{
			cancel: func(context.Context, domain.SessionID) (client.CancelResponse, error) {
				return client.CancelResponse{AlreadyTerminal: true, Session: explorerSession("ses-terminal", domain.SessionCompleted, time.Now().UTC())}, nil
			},
			get: func(context.Context, domain.SessionID) (domain.Session, error) {
				return explorerSession("ses-terminal", domain.SessionCompleted, time.Now().UTC()), nil
			},
		}
		m := detailModel(domain.SessionRunning, api)
		m.detail.confirmCancel = true
		updated, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: 'y', Text: "y"}))
		m = updated.(Model)
		result := cmd().(sessionCancelResultMsg)
		updated, refresh := m.Update(result)
		m = updated.(Model)
		if !strings.Contains(m.detail.cancelNote, "already terminal") || strings.Contains(m.detail.cancelNote, "Cancellation confirmed") {
			t.Fatalf("terminal response was misrepresented: %q", m.detail.cancelNote)
		}
		m = updateModel(t, m, refresh().(sessionDetailLoadedMsg))
		if m.detail.session.Status != domain.SessionCompleted {
			t.Fatalf("already-terminal state not refreshed: %s", m.detail.session.Status)
		}
	})
}

func TestCancellationEligibilityAndConfirmationDismissal(t *testing.T) {
	for _, status := range []domain.SessionStatus{domain.SessionPending, domain.SessionStarting, domain.SessionCompleted, domain.SessionFailed, domain.SessionInterrupted} {
		m := detailModel(status, &taskFormAPI{})
		updated, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Text: "c"}))
		m = updated.(Model)
		if m.detail.confirmCancel || strings.Contains(m.renderSessionDetail(110), "c: cancel") {
			t.Errorf("status %s exposed cancellation", status)
		}
	}
	m := detailModel(domain.SessionWaitingForInput, &taskFormAPI{})
	updated, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Text: "c"}))
	m = updated.(Model)
	if !m.detail.confirmCancel {
		t.Fatal("waiting_for_input session did not show confirmation")
	}
	updated, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'n', Text: "n"}))
	m = updated.(Model)
	if m.detail.confirmCancel {
		t.Fatal("n did not dismiss cancellation confirmation")
	}
}

func TestCancellationAPIErrorsAreExplained(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{name: "conflict", err: &client.APIError{Status: 409, Code: "invalid_session_state"}, want: "state changed"},
		{name: "unsupported", err: &client.APIError{Status: 501, Code: "unsupported_operation"}, want: "does not support cancellation"},
		{name: "unavailable", err: &client.APIError{Status: 503, Code: "provider_unavailable"}, want: "provider is unavailable"},
		{name: "timeout", err: context.DeadlineExceeded, want: "timed out"},
		{name: "canceled", err: context.Canceled, want: "cancelled"},
		{name: "generic", err: errors.New("permission denied"), want: "permission denied"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			api := &taskFormAPI{
				cancel: func(context.Context, domain.SessionID) (client.CancelResponse, error) {
					return client.CancelResponse{}, test.err
				},
				get: func(_ context.Context, id domain.SessionID) (domain.Session, error) {
					return explorerSession(string(id), domain.SessionRunning, time.Now()), nil
				},
			}
			m := detailModel(domain.SessionRunning, api)
			m, cmd := m.beginCancelSession()
			result := cmd().(sessionCancelResultMsg)
			updated, refresh := m.Update(result)
			m = updated.(Model)
			if refresh == nil {
				t.Fatal("failed cancellation did not refresh authoritative session state")
			}
			updated, _ = m.Update(refresh().(sessionDetailLoadedMsg))
			m = updated.(Model)
			if !strings.Contains(m.renderSessionDetail(110), test.want) {
				t.Fatalf("cancellation error missing %q:\n%s", test.want, m.renderSessionDetail(110))
			}
		})
	}
}

func TestStaleDetailLoadAndStreamMessagesAreIgnoredAfterNavigation(t *testing.T) {
	m := detailModel(domain.SessionRunning, &taskFormAPI{})
	m.detail.activeLoad = 22
	m.detail.activeStream = 12
	updated, _ := m.Update(sessionsLoadedMsg{requestID: 8, sessions: []domain.Session{explorerSession("stale", domain.SessionCompleted, time.Now())}})
	m = updated.(Model)
	updated, _ = m.Update(detailStreamEventMsg{streamID: 11, event: client.Event{ID: "stale", Type: "provider.output"}})
	m = updated.(Model)
	if m.detail.activeLoad != 22 || m.detail.activeStream != 12 || len(m.detail.events) != 0 {
		t.Fatal("stale session or event message changed current detail state")
	}
	updated, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	m = updated.(Model)
	updated, _ = m.Update(sessionDetailLoadedMsg{requestID: 22, session: explorerSession("stale", domain.SessionFailed, time.Now())})
	m = updated.(Model)
	if m.screen != ScreenSessions || m.detail.session.ID == "stale" {
		t.Fatal("stale detail response overwrote the Sessions screen")
	}
}

func TestLeavingDetailCancelsInFlightSessionLookup(t *testing.T) {
	started := make(chan struct{})
	finished := make(chan struct{})
	api := &taskFormAPI{
		get: func(ctx context.Context, _ domain.SessionID) (domain.Session, error) {
			close(started)
			<-ctx.Done()
			close(finished)
			return domain.Session{}, ctx.Err()
		},
	}
	m := NewModel(api, "server", "test")
	m.screen = ScreenSessions
	updated, load := m.openSessionDetail("ses-inflight")
	m = updated
	result := make(chan tea.Msg, 1)
	go func() { result <- load() }()
	waitTaskSignal(t, started, "detail lookup start")
	updatedModel, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	m = updatedModel.(Model)
	waitTaskSignal(t, finished, "detail lookup cancellation")
	stale := waitForChannel(t, result, "cancelled lookup result")
	updatedModel, _ = m.Update(stale)
	m = updatedModel.(Model)
	m.cancelSessionsRequest()
	if m.screen != ScreenSessions || m.detail.loaded {
		t.Fatal("stale in-flight lookup changed the current screen or detail")
	}
}

func loadSessionsForTest(t *testing.T, api taskAPI) Model {
	t.Helper()
	m := NewModel(api, "server", "test")
	var cmd tea.Cmd
	m, cmd = m.openSessions()
	if cmd == nil {
		t.Fatal("opening Sessions did not request data")
	}
	return updateModel(t, m, cmd().(sessionsLoadedMsg))
}

func openDetailForTest(t *testing.T, api taskAPI, id domain.SessionID) (Model, tea.Cmd) {
	t.Helper()
	m := NewModel(api, "server", "test")
	m.screen = ScreenSessions
	updated, cmd := m.openSessionDetail(id)
	if cmd == nil {
		t.Fatal("opening detail did not request session data")
	}
	loaded := cmd().(sessionDetailLoadedMsg)
	updatedModel, streamCmd := updated.Update(loaded)
	m = updatedModel.(Model)
	return m, streamCmd
}

func detailModel(status domain.SessionStatus, api taskAPI) Model {
	m := NewModel(api, "server", "test")
	m.screen = ScreenSessionDetail
	m.detail.id = "ses-detail"
	m.detail.session = explorerSession("ses-detail", status, time.Now().UTC())
	m.detail.loaded = true
	return m
}

func explorerSession(id string, status domain.SessionStatus, updated time.Time) domain.Session {
	return domain.Session{
		ID: domain.SessionID(id), TaskID: domain.TaskID("task-" + id), Provider: domain.ProviderCodex,
		Status: status, CreatedAt: updated.Add(-time.Minute), UpdatedAt: updated,
	}
}

func splitBatch(t *testing.T, cmd tea.Cmd) (tea.Cmd, tea.Cmd) {
	t.Helper()
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("stream start command = %#v, want two-command batch", msg)
	}
	return batch[0], batch[1]
}

func waitForChannel[T any](t *testing.T, ch <-chan T, name string) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(3 * time.Second):
		var zero T
		t.Fatalf("timed out waiting for %s", name)
		return zero
	}
}
