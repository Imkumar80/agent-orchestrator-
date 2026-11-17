package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Imkumar80/agent-orchestrator-/internal/cli/client"
	"github.com/Imkumar80/agent-orchestrator-/internal/domain"
)

type taskFormAPI struct {
	providers    []client.Provider
	providersErr error
	listSessions func(context.Context) ([]domain.Session, error)
	create       func(context.Context, domain.Task) (client.TaskResponse, error)
	get          func(context.Context, domain.SessionID) (domain.Session, error)
	cancel       func(context.Context, domain.SessionID) (client.CancelResponse, error)
	stream       func(context.Context, domain.SessionID, func(client.Event) error) error
}

func (f *taskFormAPI) Health(context.Context) (client.HealthResponse, error) {
	return client.HealthResponse{}, nil
}
func (f *taskFormAPI) ListSessions(ctx context.Context) ([]domain.Session, error) {
	if f.listSessions != nil {
		return f.listSessions(ctx)
	}
	return nil, nil
}
func (f *taskFormAPI) ListProviders(context.Context) ([]client.Provider, error) {
	return append([]client.Provider(nil), f.providers...), f.providersErr
}
func (f *taskFormAPI) CreateTask(ctx context.Context, task domain.Task) (client.TaskResponse, error) {
	if f.create != nil {
		return f.create(ctx, task)
	}
	return client.TaskResponse{}, errors.New("unexpected task submission")
}
func (f *taskFormAPI) GetSession(ctx context.Context, id domain.SessionID) (domain.Session, error) {
	if f.get != nil {
		return f.get(ctx, id)
	}
	return domain.Session{}, errors.New("unexpected session lookup")
}
func (f *taskFormAPI) CancelSession(ctx context.Context, id domain.SessionID) (client.CancelResponse, error) {
	if f.cancel != nil {
		return f.cancel(ctx, id)
	}
	return client.CancelResponse{}, errors.New("unexpected session cancellation")
}
func (f *taskFormAPI) StreamEvents(ctx context.Context, id domain.SessionID, consume func(client.Event) error) error {
	if f.stream != nil {
		return f.stream(ctx, id, consume)
	}
	<-ctx.Done()
	return ctx.Err()
}

func TestNewTaskFormInitializationAndProviderLoading(t *testing.T) {
	form := newTaskForm()
	if form.focus != taskFocusDescription || form.description.Value() != "" || form.directory.Value() == "" {
		t.Fatalf("form initialization: focus=%d description=%q directory=%q", form.focus, form.description.Value(), form.directory.Value())
	}
	api := &taskFormAPI{providers: []client.Provider{
		{Name: domain.ProviderCodex, Available: false},
		{Name: domain.ProviderOpenCode, Available: true},
	}}
	m := NewModel(api, "server", "test")
	m, _ = m.openNewTask()
	load := m.taskForm.beginProviderLoad(m.ctx, api)
	if !m.taskForm.loading || m.screen != ScreenNewTask {
		t.Fatalf("provider discovery did not start: loading=%v screen=%v", m.taskForm.loading, m.screen)
	}
	loaded := load()
	msg, ok := loaded.(taskProvidersLoadedMsg)
	if !ok {
		t.Fatalf("provider command returned %T", loaded)
	}
	m = updateModel(t, m, msg)
	if m.taskForm.loading || m.taskForm.loadError != nil || m.taskForm.provider != 1 {
		t.Fatalf("provider load result: loading=%v err=%v selected=%d", m.taskForm.loading, m.taskForm.loadError, m.taskForm.provider)
	}
	if !strings.Contains(m.taskForm.view(m.theme, 90), "unavailable") || !strings.Contains(m.taskForm.view(m.theme, 90), "available") {
		t.Fatal("provider availability was not shown")
	}
}

func TestProviderSelectionSkipsUnavailableChoices(t *testing.T) {
	form := newTaskForm()
	form.applyProviders([]client.Provider{
		{Name: domain.ProviderCodex, Available: false},
		{Name: domain.ProviderOpenCode, Available: true},
		{Name: domain.ProviderHermes, Available: false},
		{Name: domain.ProviderOpenClaw, Available: true},
	}, nil)
	if form.provider != 1 {
		t.Fatalf("initial provider = %d, want first available provider", form.provider)
	}
	form.moveProvider(1)
	if form.provider != 3 {
		t.Fatalf("next available provider = %d, want index 3", form.provider)
	}
	form.description.SetValue("do work")
	form.directory.SetValue(t.TempDir())
	form.provider = 2
	if got := form.validate(); got != "Select an available provider." {
		t.Fatalf("unavailable selection validation = %q", got)
	}
}

func TestTaskFormValidation(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, description, directory, want string
	}{
		{name: "empty description", description: " \t", directory: "unused", want: "Task description is required."},
		{name: "relative directory", description: "analyze", directory: ".", want: "Working directory must be an absolute path."},
		{name: "missing directory", description: "analyze", directory: filepath.Join(t.TempDir(), "missing"), want: "Cannot inspect working directory:"},
		{name: "path is a file", description: "analyze", directory: file, want: "Working directory must point to a directory."},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			form := newTaskForm()
			form.applyProviders([]client.Provider{{Name: domain.ProviderCodex, Available: true}}, nil)
			form.description.SetValue(test.description)
			form.directory.SetValue(test.directory)
			if got := form.validate(); !strings.Contains(got, test.want) {
				t.Fatalf("validation = %q, want substring %q", got, test.want)
			}
		})
	}
}

func TestValidTaskSubmissionUsesExactFieldsAndRendersAcceptedSession(t *testing.T) {
	workdir := t.TempDir()
	api := &taskFormAPI{providers: []client.Provider{{Name: domain.ProviderOpenCode, Available: true}}}
	var got domain.Task
	api.create = func(_ context.Context, task domain.Task) (client.TaskResponse, error) {
		got = task
		return client.TaskResponse{Accepted: true, Session: domain.Session{
			ID: "ses_created", Provider: domain.ProviderOpenCode, Status: domain.SessionRunning,
		}}, nil
	}
	m := openLoadedTaskForm(t, api)
	description := "  preserve this description exactly  "
	m.taskForm.description.SetValue(description)
	m.taskForm.directory.SetValue(workdir)
	for i := 0; i < 3; i++ {
		updated, _ := m.Update(keyMsg(tea.KeyEnter))
		m = updated.(Model)
	}
	if m.taskForm.focus != taskFocusSubmit {
		t.Fatalf("Enter navigation focus=%d, want submit", m.taskForm.focus)
	}
	updated, cmd := m.Update(keyMsg(tea.KeyEnter))
	m = updated.(Model)
	if cmd == nil || !m.taskForm.submitting {
		t.Fatalf("submit start: cmd=%v submitting=%v", cmd != nil, m.taskForm.submitting)
	}
	m = updateModel(t, m, cmd())
	if got.ID == "" || !strings.HasPrefix(string(got.ID), "task_") || got.Description != description ||
		got.Provider != domain.ProviderOpenCode || got.WorkingDirectory != workdir || got.CreatedAt.IsZero() {
		t.Fatalf("submitted task fields = %#v", got)
	}
	view := m.taskForm.view(m.theme, 100)
	for _, want := range []string{"Task accepted by the server", "ses_created", "opencode", "running", "does not mean the task is complete"} {
		if !strings.Contains(view, want) {
			t.Errorf("success view missing %q:\n%s", want, view)
		}
	}
}

func TestTaskSubmissionRendersAPIAndNetworkErrors(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want string
	}{
		{name: "api", err: &client.APIError{Status: 503, Code: "provider_unavailable", Message: "provider unavailable"}, want: "HTTP 503, provider_unavailable"},
		{name: "network", err: errors.New("connection refused"), want: "connection refused"},
	} {
		t.Run(test.name, func(t *testing.T) {
			api := &taskFormAPI{providers: []client.Provider{{Name: domain.ProviderCodex, Available: true}}}
			api.create = func(context.Context, domain.Task) (client.TaskResponse, error) {
				return client.TaskResponse{}, test.err
			}
			m := openLoadedTaskForm(t, api)
			m.taskForm.description.SetValue("inspect")
			m.taskForm.directory.SetValue(t.TempDir())
			m.taskForm.setFocus(taskFocusSubmit)
			cmd, err := m.taskForm.beginSubmit(m.ctx, api)
			if err != nil {
				t.Fatal(err)
			}
			m = updateModel(t, m, cmd())
			if !strings.Contains(m.taskForm.view(m.theme, 100), test.want) {
				t.Fatalf("error view missing %q:\n%s", test.want, m.taskForm.view(m.theme, 100))
			}
		})
	}
}

func TestTaskSubmissionPreventsDuplicates(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	api := &taskFormAPI{providers: []client.Provider{{Name: domain.ProviderCodex, Available: true}}}
	api.create = func(context.Context, domain.Task) (client.TaskResponse, error) {
		calls.Add(1)
		close(started)
		<-release
		return client.TaskResponse{Accepted: true, Session: domain.Session{ID: "ses_once"}}, nil
	}
	m := openLoadedTaskForm(t, api)
	m.taskForm.description.SetValue("inspect")
	m.taskForm.directory.SetValue(t.TempDir())
	m.taskForm.setFocus(taskFocusSubmit)
	cmd, err := m.taskForm.beginSubmit(m.ctx, api)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	waitTaskSignal(t, started, "first task submission")
	updated, duplicate := m.Update(keyMsg(tea.KeyEnter))
	m = updated.(Model)
	if duplicate != nil || calls.Load() != 1 {
		t.Fatalf("duplicate submission started: cmd=%v calls=%d", duplicate != nil, calls.Load())
	}
	close(release)
	m = updateModel(t, m, waitTaskResult(t, result))
	if calls.Load() != 1 || m.taskForm.result == nil {
		t.Fatalf("submission calls=%d result=%v", calls.Load(), m.taskForm.result)
	}
}

func TestTaskSuccessNavigationBack(t *testing.T) {
	for _, test := range []struct {
		key    string
		want   Screen
		cursor int
	}{{key: "esc", want: ScreenDashboard, cursor: int(ScreenNewTask)}, {key: "s", want: ScreenSessions, cursor: int(ScreenSessions)}} {
		m := NewModel(nil, "server", "test")
		m.screen = ScreenNewTask
		m.taskForm.result = &domain.Session{ID: "ses_result"}
		var key tea.KeyPressMsg
		if test.key == "esc" {
			key = tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape})
		} else {
			key = tea.KeyPressMsg(tea.Key{Code: []rune(test.key)[0]})
		}
		updated, _ := m.Update(key)
		m = updated.(Model)
		if m.screen != test.want {
			t.Errorf("key %q navigated to %v, want %v", test.key, m.screen, test.want)
		}
		if test.key == "s" && m.cursor != test.cursor {
			t.Errorf("Sessions cursor = %d, want %d", m.cursor, test.cursor)
		}
	}
}

func TestNewTaskFocusedTypingDoesNotTriggerGlobalShortcuts(t *testing.T) {
	api := &taskFormAPI{providers: []client.Provider{{Name: domain.ProviderCodex, Available: true}}}
	m := openLoadedTaskForm(t, api)
	updated, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'q', Text: "q"}))
	m = updated.(Model)
	if m.screen != ScreenNewTask || m.taskForm.description.Value() != "q" {
		t.Fatalf("focused q changed navigation or was not entered: screen=%v value=%q", m.screen, m.taskForm.description.Value())
	}
}

func TestTaskSubmissionRespectsContextCancellation(t *testing.T) {
	started := make(chan struct{})
	api := &taskFormAPI{providers: []client.Provider{{Name: domain.ProviderCodex, Available: true}}}
	api.create = func(ctx context.Context, _ domain.Task) (client.TaskResponse, error) {
		close(started)
		<-ctx.Done()
		return client.TaskResponse{}, ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := openLoadedTaskForm(t, api)
	m.ctx = ctx
	m.taskForm.description.SetValue("inspect")
	m.taskForm.directory.SetValue(t.TempDir())
	m.taskForm.setFocus(taskFocusSubmit)
	cmd, err := m.taskForm.beginSubmit(m.ctx, api)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	waitTaskSignal(t, started, "task submission")
	cancel()
	msg := waitTaskResult(t, result).(taskSubmittedMsg)
	if !errors.Is(msg.err, context.Canceled) {
		t.Fatalf("submission error = %v, want context.Canceled", msg.err)
	}
}

func TestEscapeCancelsTaskSubmissionAndReturnsToDashboard(t *testing.T) {
	started := make(chan struct{})
	api := &taskFormAPI{providers: []client.Provider{{Name: domain.ProviderCodex, Available: true}}}
	api.create = func(ctx context.Context, _ domain.Task) (client.TaskResponse, error) {
		close(started)
		<-ctx.Done()
		return client.TaskResponse{}, ctx.Err()
	}
	m := openLoadedTaskForm(t, api)
	m.taskForm.description.SetValue("inspect")
	m.taskForm.directory.SetValue(t.TempDir())
	m.taskForm.setFocus(taskFocusSubmit)
	cmd, err := m.taskForm.beginSubmit(m.ctx, api)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	waitTaskSignal(t, started, "task submission")
	updated, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	m = updated.(Model)
	if m.screen != ScreenDashboard || m.taskForm.submitting {
		t.Fatalf("Escape did not return to dashboard/cancel submission: screen=%v submitting=%v", m.screen, m.taskForm.submitting)
	}
	msg := waitTaskResult(t, result).(taskSubmittedMsg)
	if !errors.Is(msg.err, context.Canceled) {
		t.Fatalf("Escape cancellation error = %v, want context.Canceled", msg.err)
	}
}

func TestProviderListErrorCanBeRetried(t *testing.T) {
	api := &taskFormAPI{providersErr: fmt.Errorf("server unavailable")}
	m := openLoadedTaskForm(t, api)
	if m.taskForm.loadError == nil || !strings.Contains(m.taskForm.view(m.theme, 100), "Provider list error") {
		t.Fatal("provider-list error was not rendered")
	}
	api.providersErr = nil
	api.providers = []client.Provider{{Name: domain.ProviderCodex, Available: true}}
	updated, retry := m.updateTaskKey(tea.KeyPressMsg(tea.Key{Code: 'r', Mod: tea.ModCtrl}))
	if retry == nil {
		t.Fatal("Ctrl+R did not retry provider discovery")
	}
	m = updated
	retryMsg := retry()
	m = updateModel(t, m, retryMsg)
	if m.taskForm.loadError != nil || m.taskForm.loading || !m.taskForm.availableProvider(m.taskForm.provider) {
		t.Fatalf("provider retry failed: loading=%v error=%v selected=%d message=%#v active=%d", m.taskForm.loading, m.taskForm.loadError, m.taskForm.provider, retryMsg, m.taskForm.activeLoad)
	}
}

func openLoadedTaskForm(t *testing.T, api taskAPI) Model {
	t.Helper()
	m := NewModel(api, "server", "test")
	m, _ = m.openNewTask()
	load := m.taskForm.beginProviderLoad(m.ctx, api)
	if load == nil {
		t.Fatal("opening New Task did not load providers")
	}
	loaded := load()
	msg, ok := loaded.(taskProvidersLoadedMsg)
	if !ok {
		t.Fatalf("provider command returned %T", loaded)
	}
	return updateModel(t, m, msg)
}

func waitTaskSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

func waitTaskResult(t *testing.T, result <-chan tea.Msg) tea.Msg {
	t.Helper()
	select {
	case msg := <-result:
		return msg
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for task result")
		return nil
	}
}
