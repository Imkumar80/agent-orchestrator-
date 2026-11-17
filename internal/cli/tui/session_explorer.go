package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Imkumar80/agent-orchestrator-/internal/cli/client"
	"github.com/Imkumar80/agent-orchestrator-/internal/domain"
)

const (
	maxDetailEvents       = 100
	streamChannelCapacity = 16
	maxPayloadRunes       = 1200
)

type sessionsState struct {
	items        []domain.Session
	selected     int
	selectedID   domain.SessionID
	loaded       bool
	loading      bool
	disconnected bool
	err          error
	requestSeq   uint64
	activeReq    uint64
	cancel       context.CancelFunc
}

type detailStreamState int

const (
	detailStreamIdle detailStreamState = iota
	detailStreamConnecting
	detailStreamReceiving
	detailStreamEnded
	detailStreamFailed
)

type detailState struct {
	id               domain.SessionID
	session          domain.Session
	loaded           bool
	loading          bool
	loadDisconnected bool
	loadErr          error
	loadSeq          uint64
	activeLoad       uint64
	loadCancel       context.CancelFunc
	connectOnLoad    bool

	streamSeq     uint64
	activeStream  uint64
	streamCancel  context.CancelFunc
	streamCtx     context.Context
	streamItems   chan detailStreamItem
	streamState   detailStreamState
	streamErr     error
	events        []client.Event
	eventsDropped uint64

	confirmCancel bool
	cancelling    bool
	cancelSeq     uint64
	activeCancel  uint64
	cancelCancel  context.CancelFunc
	cancelErr     error
	cancelNote    string
}

type detailStreamItem struct {
	event *client.Event
	err   error
	ended bool
}

func (m Model) openSessions() (Model, tea.Cmd) {
	m.cancelSessionsRequest()
	m.cancelDetailWork()
	m.screen = ScreenSessions
	m.cursor = int(ScreenSessions)
	m.inputFocused = false
	return m.beginSessionsLoad()
}

func (m Model) beginSessionsLoad() (Model, tea.Cmd) {
	if m.sessionList.cancel != nil {
		m.sessionList.cancel()
	}
	m.sessionList.requestSeq++
	m.sessionList.activeReq = m.sessionList.requestSeq
	m.sessionList.loading = true
	m.sessionList.err = nil
	m.sessionList.disconnected = false
	requestID := m.sessionList.activeReq
	ctx, cancel := context.WithCancel(m.ctx)
	m.sessionList.cancel = cancel
	api := m.api
	return m, func() tea.Msg {
		if api == nil {
			return sessionsLoadedMsg{requestID: requestID, err: fmt.Errorf("HTTP client is unavailable")}
		}
		items, err := api.ListSessions(ctx)
		return sessionsLoadedMsg{requestID: requestID, sessions: items, err: err}
	}
}

func (m *Model) acceptSessions(msg sessionsLoadedMsg) {
	if m.sessionList.cancel != nil {
		m.sessionList.cancel()
		m.sessionList.cancel = nil
	}
	m.sessionList.activeReq = 0
	m.sessionList.loading = false
	m.sessionList.loaded = true
	m.sessionList.err = msg.err
	if msg.err != nil {
		m.sessionList.disconnected = sessionLoadErrorKind(msg.err)
		return
	}
	m.sessionList.disconnected = false
	m.sessionList.items = append([]domain.Session(nil), msg.sessions...)
	sort.Slice(m.sessionList.items, func(i, j int) bool {
		if m.sessionList.items[i].UpdatedAt.Equal(m.sessionList.items[j].UpdatedAt) {
			return m.sessionList.items[i].ID < m.sessionList.items[j].ID
		}
		return m.sessionList.items[i].UpdatedAt.After(m.sessionList.items[j].UpdatedAt)
	})
	if len(m.sessionList.items) == 0 {
		m.sessionList.selected = 0
		m.sessionList.selectedID = ""
		return
	}
	if m.sessionList.selectedID != "" {
		for i := range m.sessionList.items {
			if m.sessionList.items[i].ID == m.sessionList.selectedID {
				m.sessionList.selected = i
				return
			}
		}
	}
	if m.sessionList.selected >= len(m.sessionList.items) {
		m.sessionList.selected = len(m.sessionList.items) - 1
	}
	if m.sessionList.selected < 0 {
		m.sessionList.selected = 0
	}
	m.sessionList.selectedID = m.sessionList.items[m.sessionList.selected].ID
}

func (m *Model) moveSessionSelection(delta int) {
	if len(m.sessionList.items) == 0 {
		return
	}
	m.sessionList.selected += delta
	if m.sessionList.selected < 0 {
		m.sessionList.selected = 0
	}
	if m.sessionList.selected >= len(m.sessionList.items) {
		m.sessionList.selected = len(m.sessionList.items) - 1
	}
	m.sessionList.selectedID = m.sessionList.items[m.sessionList.selected].ID
}

func (m Model) openSessionDetail(id domain.SessionID) (Model, tea.Cmd) {
	m.cancelSessionsRequest()
	m.cancelDetailWork()
	m.detail = detailState{id: id, streamState: detailStreamIdle}
	m.screen = ScreenSessionDetail
	m.inputFocused = false
	return m.beginDetailLoad(true)
}

func (m Model) beginDetailLoad(connect bool) (Model, tea.Cmd) {
	if m.detail.loadCancel != nil {
		m.detail.loadCancel()
	}
	m.detail.loadSeq++
	m.detail.activeLoad = m.detail.loadSeq
	m.detail.loading = true
	m.detail.loadErr = nil
	m.detail.loadDisconnected = false
	m.detail.connectOnLoad = connect
	requestID := m.detail.activeLoad
	ctx, cancel := context.WithCancel(m.ctx)
	m.detail.loadCancel = cancel
	id := m.detail.id
	api := m.api
	return m, func() tea.Msg {
		if api == nil {
			return sessionDetailLoadedMsg{requestID: requestID, connect: connect, err: fmt.Errorf("HTTP client is unavailable")}
		}
		session, err := api.GetSession(ctx, id)
		return sessionDetailLoadedMsg{requestID: requestID, session: session, connect: connect, err: err}
	}
}

func (m *Model) acceptDetail(msg sessionDetailLoadedMsg) tea.Cmd {
	if m.detail.loadCancel != nil {
		m.detail.loadCancel()
		m.detail.loadCancel = nil
	}
	m.detail.activeLoad = 0
	m.detail.loading = false
	m.detail.loadErr = msg.err
	m.detail.connectOnLoad = false
	if msg.err != nil {
		m.detail.loadDisconnected = sessionLoadErrorKind(msg.err)
		return nil
	}
	m.detail.loadDisconnected = false
	m.detail.loaded = true
	m.detail.session = msg.session
	if msg.session.Status.Terminal() {
		m.stopDetailStream()
		return nil
	}
	if msg.connect && isActiveSession(msg.session.Status) {
		return m.startDetailStream()
	}
	return nil
}

func (m *Model) startDetailStream() tea.Cmd {
	if m.api == nil || m.detail.id == "" || !isActiveSession(m.detail.session.Status) {
		m.detail.streamState = detailStreamIdle
		return nil
	}
	m.stopDetailStream()
	ctx, cancel := context.WithCancel(m.ctx)
	m.detail.streamSeq++
	m.detail.activeStream = m.detail.streamSeq
	m.detail.streamCancel = cancel
	m.detail.streamCtx = ctx
	m.detail.streamItems = make(chan detailStreamItem, streamChannelCapacity)
	m.detail.streamState = detailStreamConnecting
	m.detail.streamErr = nil
	streamID := m.detail.activeStream
	items := m.detail.streamItems
	id := m.detail.id
	api := m.api
	return tea.Batch(startDetailStreamCmd(ctx, api, id, streamID, items), waitDetailStreamCmd(ctx, streamID, items))
}

func startDetailStreamCmd(ctx context.Context, api taskAPI, id domain.SessionID, streamID uint64, items chan<- detailStreamItem) tea.Cmd {
	return func() tea.Msg {
		go func() {
			err := api.StreamEvents(ctx, id, func(event client.Event) error {
				copy := event
				select {
				case items <- detailStreamItem{event: &copy}:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			})
			select {
			case items <- detailStreamItem{err: err, ended: true}:
			case <-ctx.Done():
			}
		}()
		return detailStreamStartedMsg{streamID: streamID}
	}
}

func waitDetailStreamCmd(ctx context.Context, streamID uint64, items <-chan detailStreamItem) tea.Cmd {
	return func() tea.Msg {
		select {
		case item := <-items:
			if item.ended {
				return detailStreamEndedMsg{streamID: streamID, err: item.err}
			}
			return detailStreamEventMsg{streamID: streamID, event: *item.event}
		case <-ctx.Done():
			return detailStreamEndedMsg{streamID: streamID, err: ctx.Err(), canceled: true}
		}
	}
}

func (m *Model) acceptDetailEvent(msg detailStreamEventMsg) tea.Cmd {
	if msg.streamID != m.detail.activeStream {
		return nil
	}
	m.detail.streamState = detailStreamReceiving
	m.detail.events = append(m.detail.events, msg.event)
	if len(m.detail.events) > maxDetailEvents {
		dropped := len(m.detail.events) - maxDetailEvents
		m.detail.eventsDropped += uint64(dropped)
		m.detail.events = append([]client.Event(nil), m.detail.events[dropped:]...)
	}
	return waitDetailStreamCmd(m.detail.streamCtx, msg.streamID, m.detail.streamItems)
}

func (m *Model) acceptDetailStreamEnd(msg detailStreamEndedMsg) tea.Cmd {
	if msg.streamID != m.detail.activeStream {
		return nil
	}
	if m.detail.streamCancel != nil {
		m.detail.streamCancel()
	}
	m.detail.streamCancel = nil
	m.detail.streamCtx = nil
	m.detail.streamItems = nil
	m.detail.activeStream = 0
	if !msg.canceled {
		m.detail.streamErr = msg.err
		if msg.err != nil {
			m.detail.streamState = detailStreamFailed
		} else {
			m.detail.streamState = detailStreamEnded
		}
		// EOF is not a completion signal. Always retrieve persisted session state.
		updated, cmd := m.beginDetailLoad(false)
		*m = updated
		return cmd
	}
	m.detail.streamState = detailStreamIdle
	return nil
}

func (m *Model) stopDetailStream() {
	if m.detail.streamCancel != nil {
		m.detail.streamCancel()
	}
	m.detail.streamCancel = nil
	m.detail.streamCtx = nil
	m.detail.streamItems = nil
	m.detail.activeStream = 0
	m.detail.streamState = detailStreamIdle
}

func (m *Model) cancelSessionsRequest() {
	if m.sessionList.cancel != nil {
		m.sessionList.cancel()
		m.sessionList.cancel = nil
	}
	m.sessionList.activeReq = 0
	m.sessionList.loading = false
}

func (m *Model) cancelDetailWork() {
	if m.detail.loadCancel != nil {
		m.detail.loadCancel()
	}
	if m.detail.cancelCancel != nil {
		m.detail.cancelCancel()
	}
	m.detail.loadCancel = nil
	m.detail.cancelCancel = nil
	m.detail.activeLoad = 0
	m.detail.activeCancel = 0
	m.detail.loading = false
	m.detail.cancelling = false
	m.detail.confirmCancel = false
	m.stopDetailStream()
}

func (m Model) beginCancelSession() (Model, tea.Cmd) {
	if m.detail.cancelling || !cancellationEligible(m.detail.session.Status) || m.api == nil {
		return m, nil
	}
	m.detail.cancelSeq++
	m.detail.activeCancel = m.detail.cancelSeq
	m.detail.cancelling = true
	m.detail.cancelErr = nil
	m.detail.cancelNote = ""
	requestID := m.detail.activeCancel
	ctx, cancel := context.WithCancel(m.ctx)
	m.detail.cancelCancel = cancel
	id, api := m.detail.id, m.api
	return m, func() tea.Msg {
		response, err := api.CancelSession(ctx, id)
		return sessionCancelResultMsg{requestID: requestID, response: response, err: err}
	}
}

func (m *Model) acceptCancelResult(msg sessionCancelResultMsg) tea.Cmd {
	if m.detail.cancelCancel != nil {
		m.detail.cancelCancel()
		m.detail.cancelCancel = nil
	}
	m.detail.activeCancel = 0
	m.detail.cancelling = false
	if msg.err != nil {
		m.detail.cancelErr = msg.err
		// A failed or timed-out cancellation request can still have reached the
		// server. Refresh the authoritative session state without claiming success.
		updated, cmd := m.beginDetailLoad(false)
		*m = updated
		return cmd
	}
	m.detail.session = msg.response.Session
	if msg.response.AlreadyTerminal {
		m.detail.cancelNote = fmt.Sprintf("Session was already terminal (%s); no interruption was performed.", msg.response.Session.Status)
	} else if msg.response.Accepted {
		m.detail.cancelNote = fmt.Sprintf("Cancellation confirmed by the server; returned status: %s.", msg.response.Session.Status)
	} else {
		m.detail.cancelErr = errors.New("server did not confirm cancellation")
	}
	updated, cmd := m.beginDetailLoad(false)
	*m = updated
	return cmd
}

func (m Model) updateSessionsKey(key string) (Model, tea.Cmd) {
	switch key {
	case "q", "ctrl+c":
		m.cancelSessionsRequest()
		m.cancelDetailWork()
		return m, tea.Quit
	case "esc":
		m.cancelSessionsRequest()
		m.screen = ScreenDashboard
		m.cursor = int(ScreenDashboard)
		m.inputFocused = false
		return m, nil
	case "up", "k":
		m.moveSessionSelection(-1)
	case "down", "j":
		m.moveSessionSelection(1)
	case "enter":
		if len(m.sessionList.items) > 0 {
			return m.openSessionDetail(m.sessionList.items[m.sessionList.selected].ID)
		}
	case "r":
		return m.beginSessionsLoad()
	case "?":
		m.showHelp = true
	}
	return m, nil
}

func (m Model) updateDetailKey(key string) (Model, tea.Cmd) {
	if m.detail.confirmCancel {
		switch key {
		case "y", "Y":
			m.detail.confirmCancel = false
			m.inputFocused = false
			return m.beginCancelSession()
		case "n", "N", "esc":
			m.detail.confirmCancel = false
			m.inputFocused = false
		}
		return m, nil
	}
	switch key {
	case "q", "ctrl+c":
		m.cancelSessionsRequest()
		m.cancelDetailWork()
		return m, tea.Quit
	case "esc":
		m.cancelDetailWork()
		m.screen = ScreenSessions
		m.cursor = int(ScreenSessions)
		m.inputFocused = false
		return m.beginSessionsLoad()
	case "r":
		return m.beginDetailLoad(false)
	case "e":
		m.stopDetailStream()
		m.detail.streamErr = nil
		return m.beginDetailLoad(true)
	case "c":
		if cancellationEligible(m.detail.session.Status) && !m.detail.cancelling {
			m.detail.confirmCancel = true
			m.inputFocused = true
		}
	case "?":
		m.showHelp = true
	}
	return m, nil
}

func (m Model) renderSessions(width int) string {
	s := m.sessionList
	if !s.loaded && s.loading {
		return m.theme.panel.Width(width - 4).Render(m.theme.title.Render("Sessions") + "\n\nLoading sessions…")
	}
	if s.err != nil && s.disconnected {
		return m.theme.panel.Width(width - 4).Render(m.theme.title.Render("Sessions") + "\n\n" + m.theme.danger.Render("Server disconnected: "+s.err.Error()) + "\n\nPress r to retry.")
	}
	if s.err != nil {
		return m.theme.panel.Width(width - 4).Render(m.theme.title.Render("Sessions") + "\n\n" + m.theme.warning.Render("Session data could not be loaded: "+s.err.Error()) + "\n\nPress r to retry.")
	}
	rows := []string{m.theme.title.Render("Sessions")}
	if s.loading {
		rows = append(rows, m.theme.dim.Render("Refreshing sessions…"))
	}
	if len(s.items) == 0 {
		rows = append(rows, "", m.theme.dim.Render("No sessions yet."), "", "r: refresh   Esc: dashboard")
		return m.theme.panel.Width(width - 4).Render(strings.Join(rows, "\n"))
	}
	rows = append(rows, "", m.theme.dim.Render(fmt.Sprintf("%-28s %-10s %-22s %-20s %s", "SESSION ID", "PROVIDER", "STATUS", "UPDATED", "CREATED")))
	for i, session := range s.items {
		id := string(session.ID)
		if len(id) > 28 {
			id = id[:25] + "..."
		}
		row := fmt.Sprintf("%-28s %-10s %-22s %-20s %s", id, session.Provider, statusStyle(m.theme, session.Status).Render(displaySessionStatus(session.Status)), session.UpdatedAt.Local().Format("Jan 02 15:04:05"), session.CreatedAt.Local().Format("Jan 02 15:04:05"))
		if i == s.selected {
			rows = append(rows, m.theme.selected.Width(width-8).Render("> "+row))
		} else {
			rows = append(rows, "  "+row)
		}
		if session.Error != nil {
			rows = append(rows, m.theme.warning.Render("    Error "+session.Error.Code+": "+session.Error.Message))
		}
	}
	rows = append(rows, "", "↑/↓: select   Enter: details   r: refresh   Esc: dashboard")
	return m.theme.panel.Width(width - 4).Render(strings.Join(rows, "\n"))
}

func (m Model) renderSessionDetail(width int) string {
	d := m.detail
	rows := []string{m.theme.title.Render("Session Detail"), ""}
	if d.loading && !d.loaded {
		rows = append(rows, "Loading session…")
		return m.theme.panel.Width(width - 4).Render(strings.Join(rows, "\n"))
	}
	if d.loadErr != nil && !d.loaded {
		label := "Session could not be loaded: "
		style := m.theme.warning
		if d.loadDisconnected {
			label = "Server disconnected: "
			style = m.theme.danger
		}
		rows = append(rows, style.Render(label+d.loadErr.Error()), "", "r: retry   Esc: sessions")
		return m.theme.panel.Width(width - 4).Render(strings.Join(rows, "\n"))
	}
	s := d.session
	rows = append(rows,
		fmt.Sprintf("Session ID                 %s", s.ID),
		fmt.Sprintf("Task ID                    %s", s.TaskID),
		fmt.Sprintf("Provider                   %s", s.Provider),
		fmt.Sprintf("Status                     %s", statusStyle(m.theme, s.Status).Render(displaySessionStatus(s.Status))),
		fmt.Sprintf("Created                    %s", s.CreatedAt.Local().Format(time.RFC3339)),
		fmt.Sprintf("Updated                    %s", s.UpdatedAt.Local().Format(time.RFC3339)),
	)
	if s.NativeProviderSessionID != "" {
		rows = append(rows, fmt.Sprintf("Native provider session ID %s", s.NativeProviderSessionID))
	}
	if s.Error != nil {
		rows = append(rows, m.theme.warning.Render("Error code: "+s.Error.Code), m.theme.warning.Render("Error: "+s.Error.Message))
	}
	if d.loading {
		rows = append(rows, m.theme.dim.Render("Refreshing session state…"))
	}
	if d.loadErr != nil {
		label := "Session refresh error: "
		style := m.theme.warning
		if d.loadDisconnected {
			label = "Server disconnected: "
			style = m.theme.danger
		}
		rows = append(rows, style.Render(label+d.loadErr.Error()))
	}
	rows = append(rows, "", m.theme.title.Render("Live events"), m.theme.dim.Render(streamStatusText(d)))
	if d.streamErr != nil {
		rows = append(rows, m.theme.warning.Render("Stream ended with error: "+d.streamErr.Error()))
	}
	if d.eventsDropped > 0 {
		rows = append(rows, m.theme.dim.Render(fmt.Sprintf("Earlier in-memory events dropped from view: %d", d.eventsDropped)))
	}
	if len(d.events) == 0 {
		rows = append(rows, m.theme.dim.Render("No live events received. Events are not replayed."))
	} else {
		start := 0
		if len(d.events) > 12 {
			start = len(d.events) - 12
		}
		for _, event := range d.events[start:] {
			rows = append(rows, fmt.Sprintf("[%s] %s", event.Timestamp.Local().Format("15:04:05"), event.Type))
			payload := eventPayloadText(event.Payload)
			if payload != "" {
				rows = append(rows, m.theme.dim.Render("  "+payload))
			}
		}
	}
	if d.cancelErr != nil {
		rows = append(rows, "", m.theme.danger.Render("Cancellation error: "+formatSessionActionError(d.cancelErr)))
	}
	if d.cancelNote != "" {
		rows = append(rows, "", m.theme.success.Render(d.cancelNote))
	}
	if d.confirmCancel {
		rows = append(rows, "", m.theme.warning.Bold(true).Render("Cancel this session? Press y to confirm or n/Esc to keep it running."))
	} else if d.cancelling {
		rows = append(rows, "", m.theme.warning.Render("Cancelling…"))
	}
	shortcuts := "r: refresh   e: reconnect events   Esc: sessions"
	if cancellationEligible(s.Status) && !d.cancelling {
		shortcuts += "   c: cancel"
	}
	rows = append(rows, "", shortcuts)
	return m.theme.panel.Width(width - 4).Render(strings.Join(rows, "\n"))
}

func streamStatusText(d detailState) string {
	switch d.streamState {
	case detailStreamConnecting:
		return "Stream request started; the client does not expose connection confirmation, waiting for the first event."
	case detailStreamReceiving:
		return "Receiving live events."
	case detailStreamEnded:
		return "Stream ended; session state was refreshed. Press e to reconnect."
	case detailStreamFailed:
		return "Stream disconnected; session state was refreshed. Press e to reconnect."
	default:
		if d.session.Status.Terminal() {
			return "Session is terminal; no live stream is active."
		}
		return "No live stream. Press e to connect. Events are live-only and cannot be replayed."
	}
}

func eventPayloadText(payload json.RawMessage) string {
	if len(payload) == 0 || string(payload) == "null" {
		return ""
	}
	var compact bytes.Buffer
	if json.Compact(&compact, payload) == nil {
		payload = compact.Bytes()
	}
	text := string(payload)
	runes := []rune(text)
	if len(runes) > maxPayloadRunes {
		text = string(runes[:maxPayloadRunes]) + "…"
	}
	return text
}

func formatSessionActionError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "request timed out; refresh the session to confirm its current state"
	}
	if errors.Is(err, context.Canceled) {
		return "request was cancelled; refresh the session to confirm its current state"
	}
	var apiErr *client.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Status {
		case 409:
			return "session state changed; refresh and try again if it is still active"
		case 501:
			return "this provider does not support cancellation"
		case 503:
			return "the provider is unavailable on the server"
		default:
			return fmt.Sprintf("server returned HTTP %d (%s): %s", apiErr.Status, apiErr.Code, apiErr.Message)
		}
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return "request timed out; refresh the session to confirm its current state"
	}
	return err.Error()
}

func sessionLoadErrorKind(err error) (disconnected bool) {
	var apiErr *client.APIError
	return err != nil && !errors.As(err, &apiErr) && !errors.Is(err, context.Canceled)
}

func isActiveSession(status domain.SessionStatus) bool { return !status.Terminal() }

func cancellationEligible(status domain.SessionStatus) bool {
	return status == domain.SessionRunning || status == domain.SessionWaitingForInput
}

func displaySessionStatus(status domain.SessionStatus) string {
	if status == domain.SessionInterrupted {
		return "interrupted / cancelled"
	}
	return string(status)
}
