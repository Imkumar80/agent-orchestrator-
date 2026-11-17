package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Imkumar80/agent-orchestrator-/internal/cli/client"
	"github.com/Imkumar80/agent-orchestrator-/internal/cli/tui/screens"
	"github.com/Imkumar80/agent-orchestrator-/internal/domain"
)

const refreshInterval = 5 * time.Second

type connectionState int

const (
	connectionLoading connectionState = iota
	connectionConnected
	connectionDisconnected
	connectionError
)

type taskAPI interface {
	Health(context.Context) (client.HealthResponse, error)
	ListSessions(context.Context) ([]domain.Session, error)
	ListProviders(context.Context) ([]client.Provider, error)
	CreateTask(context.Context, domain.Task) (client.TaskResponse, error)
	GetSession(context.Context, domain.SessionID) (domain.Session, error)
	CancelSession(context.Context, domain.SessionID) (client.CancelResponse, error)
	StreamEvents(context.Context, domain.SessionID, func(client.Event) error) error
}

// Model owns only presentation state. All application data comes from the
// reusable HTTP client; it never accesses orchestration or persistence layers.
type Model struct {
	ctx           context.Context
	api           taskAPI
	server        string
	version       string
	screen        Screen
	cursor        int
	width         int
	height        int
	state         connectionState
	loading       bool
	refreshing    bool
	requestSeq    uint64
	activeRequest uint64
	err           error
	health        client.HealthResponse
	sessions      []domain.Session
	providers     []client.Provider
	sessionList   sessionsState
	detail        detailState
	showHelp      bool
	inputFocused  bool
	taskForm      taskForm
	keys          keyMap
	help          help.Model
	theme         theme
}

func NewModel(api taskAPI, server, version string) Model {
	return Model{
		ctx: context.Background(), api: api, server: server, version: version,
		screen: ScreenDashboard, state: connectionLoading, loading: true,
		refreshing: true, requestSeq: 1, activeRequest: 1,
		taskForm: newTaskForm(),
		keys:     newKeyMap(), help: help.New(), theme: newTheme(),
	}
}

// Run starts Bubble Tea on the caller's streams and restores terminal modes
// when the program exits. Bubble Tea handles terminal cleanup on normal quit,
// Ctrl+C, and context cancellation.
func Run(ctx context.Context, api *client.Client, server, version string) error {
	uiContext, cancel := context.WithCancel(ctx)
	defer cancel()
	model := NewModel(api, server, version)
	model.ctx = uiContext
	program := tea.NewProgram(model, tea.WithContext(uiContext))
	_, err := program.Run()
	if err == tea.ErrInterrupted || uiContext.Err() != nil {
		return nil
	}
	return err
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.refreshCmd(m.activeRequest), tickCmd())
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.help.SetWidth(msg.Width)
		return m, nil
	case refreshTickMsg:
		if m.refreshing {
			return m, tickCmd()
		}
		m, refresh := m.beginRefresh()
		return m, tea.Batch(refresh, tickCmd())
	case dashboardLoadedMsg:
		if !m.refreshing || msg.requestID != m.activeRequest {
			return m, nil
		}
		m.refreshing = false
		m.activeRequest = 0
		m.loading = false
		m.err = msg.err
		if msg.err != nil {
			if msg.reachable {
				m.state = connectionError
			} else {
				m.state = connectionDisconnected
			}
			return m, nil
		}
		m.state = connectionConnected
		m.health = msg.health
		m.sessions = append([]domain.Session(nil), msg.sessions...)
		m.providers = append([]client.Provider(nil), msg.providers...)
		sort.Slice(m.sessions, func(i, j int) bool {
			return m.sessions[i].UpdatedAt.After(m.sessions[j].UpdatedAt)
		})
		return m, nil
	case taskProvidersLoadedMsg:
		if m.screen != ScreenNewTask || msg.requestID != m.taskForm.activeLoad {
			return m, nil
		}
		m.taskForm.activeLoad = 0
		m.taskForm.applyProviders(msg.providers, msg.err)
		return m, nil
	case taskSubmittedMsg:
		if m.screen != ScreenNewTask || msg.requestID != m.taskForm.activeSubmit {
			return m, nil
		}
		if m.taskForm.submitCancel != nil {
			m.taskForm.submitCancel()
			m.taskForm.submitCancel = nil
		}
		m.taskForm.activeSubmit = 0
		m.taskForm.submitting = false
		if msg.err != nil {
			m.taskForm.requestError = msg.err
			return m, nil
		}
		if !msg.response.Accepted {
			m.taskForm.requestError = fmt.Errorf("server did not confirm task acceptance")
			return m, nil
		}
		m.taskForm.result = &msg.response.Session
		return m, nil
	case sessionsLoadedMsg:
		if m.screen != ScreenSessions || msg.requestID != m.sessionList.activeReq {
			return m, nil
		}
		m.acceptSessions(msg)
		return m, nil
	case sessionDetailLoadedMsg:
		if m.screen != ScreenSessionDetail || msg.requestID != m.detail.activeLoad {
			return m, nil
		}
		return m, m.acceptDetail(msg)
	case detailStreamStartedMsg:
		if m.screen == ScreenSessionDetail && msg.streamID == m.detail.activeStream {
			m.detail.streamState = detailStreamConnecting
		}
		return m, nil
	case detailStreamEventMsg:
		if m.screen != ScreenSessionDetail {
			return m, nil
		}
		return m, m.acceptDetailEvent(msg)
	case detailStreamEndedMsg:
		if m.screen != ScreenSessionDetail {
			return m, nil
		}
		return m, m.acceptDetailStreamEnd(msg)
	case sessionCancelResultMsg:
		if m.screen != ScreenSessionDetail || msg.requestID != m.detail.activeCancel {
			return m, nil
		}
		return m, m.acceptCancelResult(msg)
	case tea.KeyPressMsg:
		if m.showHelp {
			return m.updateKey(msg.String())
		}
		if m.screen == ScreenNewTask {
			return m.updateTaskKey(msg)
		}
		if m.screen == ScreenSessions {
			return m.updateSessionsKey(msg.String())
		}
		if m.screen == ScreenSessionDetail {
			return m.updateDetailKey(msg.String())
		}
		return m.updateKey(msg.String())
	default:
		return m, nil
	}
}

func (m Model) updateKey(key string) (Model, tea.Cmd) {
	if m.showHelp {
		if key == "?" || key == "esc" || key == "q" {
			m.showHelp = false
			if key == "q" {
				return m, tea.Quit
			}
		}
		return m, nil
	}
	if m.inputFocused {
		return m, nil
	}
	switch key {
	case "q", "ctrl+c":
		m.shutdownRequests()
		return m, tea.Quit
	case "?":
		m.showHelp = true
	case "up", "k":
		m.cursor = (m.cursor + len(navigation) - 1) % len(navigation)
	case "down", "j":
		m.cursor = (m.cursor + 1) % len(navigation)
	case "enter":
		switch navigation[m.cursor].Screen {
		case ScreenNewTask:
			return m.openNewTask()
		case ScreenSessions:
			return m.openSessions()
		default:
			m.screen = navigation[m.cursor].Screen
		}
	case "n":
		return m.openNewTask()
	case "esc":
		m.screen = ScreenDashboard
		m.cursor = int(ScreenDashboard)
	case "r":
		if m.refreshing {
			// A manual refresh is ignored while the current sequence is running.
			return m, nil
		}
		return m.beginRefresh()
	}
	return m, nil
}

func (m *Model) shutdownRequests() {
	m.cancelSessionsRequest()
	m.cancelDetailWork()
	if m.taskForm.submitCancel != nil {
		m.taskForm.submitCancel()
		m.taskForm.submitCancel = nil
	}
}

func (m Model) openNewTask() (Model, tea.Cmd) {
	m.screen = ScreenNewTask
	m.cursor = int(ScreenNewTask)
	m.inputFocused = true
	focusCmd := m.taskForm.setFocus(taskFocusDescription)
	cmd := m.taskForm.beginProviderLoad(m.ctx, m.api)
	return m, tea.Batch(focusCmd, cmd)
}

func (m Model) updateTaskKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	f := &m.taskForm
	key := msg.String()
	if f.result != nil {
		switch key {
		case "esc", "d":
			m.screen = ScreenDashboard
			m.cursor = int(ScreenDashboard)
			m.inputFocused = false
		case "s":
			return m.openSessions()
		case "q", "ctrl+c":
			m.shutdownRequests()
			return m, tea.Quit
		}
		return m, nil
	}
	switch key {
	case "ctrl+c":
		if f.submitCancel != nil {
			f.submitCancel()
		}
		m.shutdownRequests()
		return m, tea.Quit
	case "esc":
		if f.submitCancel != nil {
			f.submitCancel()
			f.submitCancel = nil
		}
		f.activeSubmit = 0
		f.submitting = false
		m.screen = ScreenDashboard
		m.cursor = int(ScreenDashboard)
		m.inputFocused = false
		return m, nil
	case "tab":
		return m, f.setFocus(f.focus + 1)
	case "shift+tab":
		return m, f.setFocus(f.focus - 1)
	case "ctrl+r":
		if f.loadError != nil && !f.loading && m.api != nil {
			return m, f.beginProviderLoad(m.ctx, m.api)
		}
		return m, nil
	case "up", "left":
		if f.focus == taskFocusProvider {
			f.moveProvider(-1)
			f.validation = ""
			return m, nil
		}
	case "down", "right":
		if f.focus == taskFocusProvider {
			f.moveProvider(1)
			f.validation = ""
			return m, nil
		}
	case "enter":
		switch f.focus {
		case taskFocusDescription, taskFocusProvider:
			return m, f.setFocus(f.focus + 1)
		case taskFocusDirectory:
			return m, f.setFocus(taskFocusSubmit)
		case taskFocusSubmit:
			cmd, err := f.beginSubmit(m.ctx, m.api)
			if err != nil {
				return m, nil
			}
			return m, cmd
		}
	}
	if f.submitting || f.loading && f.focus == taskFocusProvider {
		return m, nil
	}
	if f.focus == taskFocusSubmit {
		return m, nil
	}
	cmd := f.updateInput(msg)
	f.validation = ""
	return m, cmd
}

func (m Model) beginRefresh() (Model, tea.Cmd) {
	if m.refreshing {
		return m, nil
	}
	m.requestSeq++
	m.activeRequest = m.requestSeq
	m.refreshing = true
	m.loading = true
	m.err = nil
	return m, m.refreshCmd(m.activeRequest)
}

func (m Model) refreshCmd(requestID uint64) tea.Cmd {
	return func() tea.Msg {
		if m.api == nil {
			return dashboardLoadedMsg{requestID: requestID, err: fmt.Errorf("HTTP client is unavailable")}
		}
		ctx := m.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		health, err := m.api.Health(ctx)
		if err != nil {
			return dashboardLoadedMsg{requestID: requestID, err: err}
		}
		sessions, err := m.api.ListSessions(ctx)
		if err != nil {
			return dashboardLoadedMsg{requestID: requestID, health: health, reachable: true, err: err}
		}
		providers, err := m.api.ListProviders(ctx)
		if err != nil {
			return dashboardLoadedMsg{requestID: requestID, health: health, reachable: true, err: err}
		}
		return dashboardLoadedMsg{requestID: requestID, health: health, sessions: sessions, providers: providers, reachable: true}
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(refreshInterval, func(time.Time) tea.Msg { return refreshTickMsg{} })
}

func (m Model) View() tea.View {
	var view tea.View
	if m.width <= 0 {
		m.width = 100
	}
	if m.height <= 0 {
		m.height = 30
	}
	if m.width < 48 {
		view.SetContent(m.theme.brand.Render("AGENT ORCHESTRATOR") + "\n" + m.theme.dim.Render(fmt.Sprintf("v%s | %s", m.version, m.connectionLabel())) + "\n\n" +
			m.theme.warning.Render(fmt.Sprintf("Terminal is %d columns wide. Resize to at least 48 columns to use the dashboard.", m.width)))
		view.AltScreen = true
		return view
	}
	content := m.render()
	view.SetContent(content)
	view.AltScreen = true
	return view
}

func (m Model) render() string {
	width := m.width
	header := m.renderHeader(width)
	var body string
	if m.width >= 100 {
		navWidth := 21
		mainWidth := width - navWidth - 3
		body = lipgloss.JoinHorizontal(lipgloss.Top, m.renderNavigation(navWidth-4), "  ", m.renderScreen(mainWidth-4))
	} else {
		body = lipgloss.JoinVertical(lipgloss.Left, m.renderCompactNavigation(width), m.renderScreen(width-4))
	}
	helpText := m.help.View(m.keys)
	footer := m.theme.dim.Render(fmt.Sprintf("%s  |  %s  |  %s", helpText, m.screen.String(), m.connectionLabel()))
	if m.showHelp {
		footer = m.theme.panel.Width(width - 6).Render("Keyboard help\n\n" + m.help.FullHelpView(m.keys.FullHelp()) + "\n\nEsc or ? closes help")
	}
	return lipgloss.JoinVertical(lipgloss.Left, header, "", body, "", footer)
}

func (m Model) renderHeader(width int) string {
	brand := m.theme.brand.Render("AGENT ORCHESTRATOR")
	connection := m.connectionLabel()
	line := lipgloss.JoinHorizontal(lipgloss.Center, brand, "  ", connection)
	detailText := fmt.Sprintf("%s   v%s", m.server, m.version)
	if m.loading && m.state != connectionLoading {
		detailText += "   refreshing..."
	}
	detail := m.theme.dim.Render(detailText)
	return lipgloss.NewStyle().Width(width).Render(lipgloss.JoinVertical(lipgloss.Left, line, detail))
}

func (m Model) renderNavigation(width int) string {
	rows := []string{m.theme.title.Render("WORKSPACE")}
	for i, item := range navigation {
		label := item.Label
		if i == m.cursor {
			label = m.theme.selected.Width(width - 2).Render("> " + label)
		} else {
			label = "  " + m.theme.dim.Render(label)
		}
		rows = append(rows, label)
	}
	return m.theme.panel.Width(width).Height(16).Render(strings.Join(rows, "\n"))
}

func (m Model) renderCompactNavigation(width int) string {
	parts := make([]string, 0, len(navigation))
	for i, item := range navigation {
		label := item.Label
		if i == m.cursor {
			parts = append(parts, m.theme.selected.Render(label))
		} else {
			parts = append(parts, m.theme.dim.Render(label))
		}
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(parts, "  "))
}

func (m Model) renderScreen(width int) string {
	if m.screen == ScreenNewTask {
		return m.taskForm.view(m.theme, width)
	}
	if m.screen == ScreenSessions {
		return m.renderSessions(width)
	}
	if m.screen == ScreenSessionDetail {
		return m.renderSessionDetail(width)
	}
	if m.screen != ScreenDashboard {
		return m.theme.panel.Width(width - 4).Height(16).Render(m.theme.title.Render(m.screen.String()) + "\n\n" + placeholder(m.screen))
	}
	return m.renderDashboard(width)
}

func placeholder(screen Screen) string {
	switch screen {
	case ScreenNewTask:
		return screens.PlaceholderContent("New Task")
	case ScreenSessions:
		return screens.PlaceholderContent("Sessions")
	case ScreenProviders:
		return screens.PlaceholderContent("Providers")
	case ScreenSettings:
		return screens.PlaceholderContent("Settings")
	default:
		return screens.PlaceholderContent("")
	}
}

func (m Model) renderDashboard(width int) string {
	active := 0
	for _, session := range m.sessions {
		if !session.Status.Terminal() {
			active++
		}
	}
	available := 0
	for _, provider := range m.providers {
		if provider.Available {
			available++
		}
	}
	if m.loading && len(m.sessions) == 0 && m.state == connectionLoading {
		return m.theme.panel.Width(width - 4).Height(16).Render(m.theme.title.Render("Dashboard") + "\n\nConnecting to server...")
	}
	if m.state == connectionDisconnected {
		message := "Server is disconnected. Check the server and press r to retry."
		if m.err != nil {
			message += "\n\n" + m.err.Error()
		}
		return m.theme.panel.Width(width - 4).Height(16).Render(m.theme.title.Render("Dashboard") + "\n\n" + m.theme.danger.Render(message))
	}
	if m.state == connectionError {
		message := "The server is reachable, but dashboard data could not be loaded. Press r to retry."
		if m.err != nil {
			message += "\n\n" + m.err.Error()
		}
		return m.theme.panel.Width(width - 4).Height(16).Render(m.theme.title.Render("Dashboard") + "\n\n" + m.theme.warning.Render(message))
	}
	cardWidth := max(12, (width-2)/3-4)
	stats := lipgloss.JoinHorizontal(lipgloss.Top,
		m.statCard("ACTIVE SESSIONS", fmt.Sprint(active), cardWidth), " ",
		m.statCard("TOTAL SESSIONS", fmt.Sprint(len(m.sessions)), cardWidth), " ",
		m.statCard("PROVIDERS READY", fmt.Sprint(available), cardWidth))
	rows := []string{m.theme.title.Render("Recent sessions")}
	if len(m.sessions) == 0 {
		rows = append(rows, m.theme.dim.Render("No sessions yet. Create one with n or the task CLI command."))
	} else {
		rows = append(rows, m.theme.dim.Render(fmt.Sprintf("%-24s %-10s %-18s %s", "SESSION", "PROVIDER", "STATUS", "UPDATED")))
		limit := min(len(m.sessions), 6)
		if m.height > 0 && m.height < 24 {
			limit = min(limit, 3)
		}
		for _, session := range m.sessions[:limit] {
			id := string(session.ID)
			if len(id) > 24 {
				id = id[:21] + "..."
			}
			updated := session.UpdatedAt.Local().Format("Jan 02 15:04")
			rows = append(rows, fmt.Sprintf("%-24s %-10s %-18s %s", id, session.Provider, statusStyle(m.theme, session.Status).Render(string(session.Status)), updated))
		}
	}
	listPanel := m.theme.panel.Width(width - 4).Height(9).Render(strings.Join(rows, "\n"))
	status := m.theme.dim.Render("Data refreshes every 5 seconds. Session events are live-only and are not replayed here.")
	return lipgloss.JoinVertical(lipgloss.Left, stats, "", listPanel, "", status)
}

func (m Model) statCard(label, value string, width int) string {
	if width < 12 {
		width = 12
	}
	return m.theme.panel.Width(width).Render(m.theme.dim.Render(label) + "\n" + m.theme.accent.Bold(true).Render(value))
}

func (m Model) connectionLabel() string {
	switch m.state {
	case connectionConnected:
		return m.theme.success.Render("[connected]")
	case connectionDisconnected:
		return m.theme.danger.Render("[disconnected]")
	case connectionError:
		return m.theme.warning.Render("data error")
	default:
		return m.theme.warning.Render("connecting")
	}
}

func statusStyle(t theme, status domain.SessionStatus) lipgloss.Style {
	switch status {
	case domain.SessionCompleted:
		return t.success
	case domain.SessionFailed:
		return t.danger
	case domain.SessionInterrupted:
		return t.warning
	default:
		return t.accent
	}
}
