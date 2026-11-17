package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Imkumar80/agent-orchestrator-/internal/cli/client"
	"github.com/Imkumar80/agent-orchestrator-/internal/domain"
)

const (
	taskFocusDescription = iota
	taskFocusProvider
	taskFocusDirectory
	taskFocusSubmit
)

type taskForm struct {
	description  textinput.Model
	directory    textinput.Model
	focus        int
	providers    []client.Provider
	provider     int
	loading      bool
	loadError    error
	loadSeq      uint64
	activeLoad   uint64
	submitting   bool
	submitSeq    uint64
	activeSubmit uint64
	submitCancel context.CancelFunc
	validation   string
	requestError error
	result       *domain.Session
}

func newTaskForm() taskForm {
	description := textinput.New()
	description.Placeholder = "Describe the task to run"
	description.SetWidth(72)
	description.Prompt = ""
	description.Focus()

	directory := textinput.New()
	directory.Placeholder = "Absolute path to an existing directory"
	directory.SetWidth(72)
	directory.Prompt = ""

	if cwd, err := os.Getwd(); err == nil {
		directory.SetValue(cwd)
	}
	return taskForm{description: description, directory: directory, focus: taskFocusDescription}
}

func (f *taskForm) setFocus(focus int) tea.Cmd {
	f.focus = (focus + 4) % 4
	f.description.Blur()
	f.directory.Blur()
	switch f.focus {
	case taskFocusDescription:
		return f.description.Focus()
	case taskFocusDirectory:
		return f.directory.Focus()
	}
	return nil
}

func (f taskForm) availableProvider(index int) bool {
	return index >= 0 && index < len(f.providers) && f.providers[index].Available && f.providers[index].Name.Validate() == nil
}

func (f *taskForm) moveProvider(delta int) {
	if f.loading || len(f.providers) == 0 {
		return
	}
	for n := 0; n < len(f.providers); n++ {
		f.provider = (f.provider + delta + len(f.providers)) % len(f.providers)
		if f.availableProvider(f.provider) {
			return
		}
	}
}

func (f *taskForm) applyProviders(providers []client.Provider, err error) {
	f.providers = append([]client.Provider(nil), providers...)
	f.loading = false
	f.loadError = err
	if f.availableProvider(f.provider) {
		return
	}
	f.provider = -1
	for i := range f.providers {
		if f.availableProvider(i) {
			f.provider = i
			return
		}
	}
}

func (f *taskForm) validate() string {
	if strings.TrimSpace(f.description.Value()) == "" {
		return "Task description is required."
	}
	if f.loading {
		return "Wait for provider availability to load."
	}
	if f.loadError != nil {
		return "Provider availability could not be loaded. Press Ctrl+R to retry."
	}
	if !f.availableProvider(f.provider) {
		return "Select an available provider."
	}
	if err := f.providers[f.provider].Name.Validate(); err != nil {
		return err.Error()
	}
	path := f.directory.Value()
	if strings.TrimSpace(path) == "" {
		return "Working directory is required."
	}
	if !filepath.IsAbs(path) {
		return "Working directory must be an absolute path."
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Sprintf("Cannot inspect working directory: %v", err)
	}
	if !info.IsDir() {
		return "Working directory must point to a directory."
	}
	return ""
}

func (f *taskForm) updateInput(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	switch f.focus {
	case taskFocusDescription:
		f.description, cmd = f.description.Update(msg)
	case taskFocusDirectory:
		f.directory, cmd = f.directory.Update(msg)
	}
	return cmd
}

func (f taskForm) view(theme theme, width int) string {
	if width < 52 {
		width = 52
	}
	inputWidth := width - 20
	f.description.SetWidth(inputWidth)
	f.directory.SetWidth(inputWidth)
	rows := []string{theme.title.Render("New Task"), "", "Describe the work for the selected provider."}
	rows = append(rows, "", taskField(theme, "TASK DESCRIPTION", f.focus == taskFocusDescription, f.description.View()))
	rows = append(rows, "", taskField(theme, "PROVIDER", f.focus == taskFocusProvider, f.providerView(theme)))
	rows = append(rows, "", taskField(theme, "WORKING DIRECTORY", f.focus == taskFocusDirectory, f.directory.View()))
	if f.validation != "" {
		rows = append(rows, "", theme.warning.Render(f.validation))
	}
	if f.loadError != nil {
		rows = append(rows, "", theme.danger.Render("Provider list error: "+f.loadError.Error()))
	}
	if f.requestError != nil {
		rows = append(rows, "", theme.danger.Render("Submission failed: "+f.submissionErrorText()))
	}
	if f.submitting {
		rows = append(rows, "", theme.accent.Render("Submitting task…"))
	} else if f.result != nil {
		rows = append(rows, "", theme.success.Bold(true).Render("Task accepted by the server"))
		rows = append(rows, fmt.Sprintf("Session ID : %s", f.result.ID))
		rows = append(rows, fmt.Sprintf("Provider   : %s", f.result.Provider))
		rows = append(rows, fmt.Sprintf("Status     : %s", f.result.Status))
		rows = append(rows, theme.dim.Render("Acceptance means execution was accepted; it does not mean the task is complete."))
		rows = append(rows, "", "Esc: dashboard   s: Sessions")
	} else {
		submitLabel := "  Submit task"
		if f.focus == taskFocusSubmit {
			submitLabel = "> Submit task"
		}
		rows = append(rows, "", theme.accent.Bold(f.focus == taskFocusSubmit).Render(submitLabel))
		rows = append(rows, "", "Tab/Shift+Tab: move   Enter: continue/submit   Esc: dashboard")
		if f.focus == taskFocusProvider {
			rows = append(rows, "↑/↓: choose an available provider")
		}
		if f.loadError != nil {
			rows = append(rows, "Ctrl+R: retry provider list")
		}
	}
	return theme.panel.Width(width - 4).Render(strings.Join(rows, "\n"))
}

func (f taskForm) submissionErrorText() string {
	var apiErr *client.APIError
	if errors.As(f.requestError, &apiErr) {
		if apiErr.Code != "" {
			return fmt.Sprintf("server rejected the task (HTTP %d, %s): %s", apiErr.Status, apiErr.Code, apiErr.Message)
		}
		return fmt.Sprintf("server rejected the task (HTTP %d): %s", apiErr.Status, apiErr.Message)
	}
	if errors.Is(f.requestError, context.Canceled) {
		return "submission was cancelled"
	}
	return f.requestError.Error()
}

func taskField(theme theme, label string, focused bool, content string) string {
	name := theme.dim.Render(label)
	if focused {
		name = theme.accent.Bold(true).Render(label)
	}
	return name + "\n" + content
}

func (f taskForm) providerView(theme theme) string {
	if f.loading {
		return theme.dim.Render("Loading provider availability…")
	}
	if f.loadError != nil {
		return theme.dim.Render("Unavailable until the provider list can be loaded")
	}
	if len(f.providers) == 0 {
		return theme.warning.Render("No providers were returned by the server.")
	}
	var rows []string
	for i, provider := range f.providers {
		status := theme.danger.Render("unavailable")
		if provider.Available && provider.Name.Validate() == nil {
			status = theme.success.Render("available")
		}
		name := string(provider.Name)
		if i == f.provider {
			prefix := "  "
			if f.focus == taskFocusProvider {
				prefix = "> "
			}
			rows = append(rows, theme.selected.Render(prefix+name)+"  "+status)
		} else {
			rows = append(rows, "  "+name+"  "+status)
		}
	}
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

func (f *taskForm) beginProviderLoad(ctx context.Context, api taskAPI) tea.Cmd {
	f.loadSeq++
	f.activeLoad = f.loadSeq
	f.loading = true
	f.loadError = nil
	requestID := f.activeLoad
	return func() tea.Msg {
		if api == nil {
			return taskProvidersLoadedMsg{requestID: requestID, err: fmt.Errorf("HTTP client is unavailable")}
		}
		providers, err := api.ListProviders(ctx)
		return taskProvidersLoadedMsg{requestID: requestID, providers: providers, err: err}
	}
}

func (f *taskForm) beginSubmit(ctx context.Context, api taskAPI) (tea.Cmd, error) {
	if f.submitting {
		return nil, nil
	}
	if api == nil {
		f.requestError = fmt.Errorf("HTTP client is unavailable")
		return nil, f.requestError
	}
	if validation := f.validate(); validation != "" {
		f.validation = validation
		return nil, nil
	}
	id, err := client.NewTaskID()
	if err != nil {
		f.requestError = err
		return nil, err
	}
	description := f.description.Value()
	provider := f.providers[f.provider].Name
	workingDirectory := f.directory.Value()
	task := domain.Task{
		ID: id, Description: description, Provider: provider,
		WorkingDirectory: workingDirectory, CreatedAt: time.Now().UTC(),
	}
	requestCtx, cancel := context.WithCancel(ctx)
	f.submitCancel = cancel
	f.submitSeq++
	f.activeSubmit = f.submitSeq
	f.submitting = true
	f.validation = ""
	f.requestError = nil
	f.result = nil
	requestID := f.activeSubmit
	return func() tea.Msg {
		response, err := api.CreateTask(requestCtx, task)
		return taskSubmittedMsg{requestID: requestID, response: response, err: err}
	}, nil
}
