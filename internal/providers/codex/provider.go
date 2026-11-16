// Package codex provides the Codex CLI implementation of the provider port.
package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Imkumar80/agent-orchestrator-/internal/domain"
	"github.com/Imkumar80/agent-orchestrator-/internal/ports"
)

const (
	ProviderName domain.AgentProvider = domain.ProviderCodex

	EventOutput          domain.AgentEventType = "provider.output"
	EventProcessComplete domain.AgentEventType = "process.completed"
	EventProcessFailed   domain.AgentEventType = "process.failed"
	EventProcessStopped  domain.AgentEventType = "process.interrupted"

	defaultEventBuffer  = 32
	maxOutputLine       = 1024 * 1024
	defaultStartTimeout = 30 * time.Second
)

var (
	ErrUnknownSession       = errors.New("unknown Codex provider session")
	ErrSessionAlreadyExists = errors.New("Codex provider session already exists")
	ErrSessionClosed        = errors.New("Codex provider session is already closed")
	ErrUnsupportedOperation = errors.New("Codex CLI operation is unsupported")
	ErrProcessFailed        = errors.New("Codex process failed")
)

var _ ports.AgentProvider = (*Provider)(nil)

// Config contains Codex-specific settings, kept outside the domain model.
// An empty ExecutablePath resolves "codex" through PATH.
type Config struct {
	ExecutablePath string
	Model          string
	Sandbox        string
	ApprovalPolicy string
	Publisher      ports.EventPublisher
	EventBuffer    int
	StartupTimeout time.Duration
}

// Provider runs one non-interactive Codex CLI execution per orchestrator session.
type Provider struct {
	config  Config
	command func(context.Context, string, ...string) *exec.Cmd
	mu      sync.RWMutex
	runs    map[domain.SessionID]*processRun
}

type processRun struct {
	ref        ports.SessionRef
	cmd        *exec.Cmd
	cancel     context.CancelFunc
	done       chan struct{}
	started    chan struct{}
	startOnce  sync.Once
	mu         sync.Mutex
	finished   bool
	stopping   bool
	resultErr  error
	publishErr error
	threadID   string
	lastEvent  time.Time
	events     []domain.AgentEvent
	subs       map[uint64]chan domain.AgentEvent
	nextSub    uint64
}

// ProcessError records a non-zero Codex process exit while preserving errors.Is.
type ProcessError struct {
	ExitCode int
	Cause    error
}

func (e *ProcessError) Error() string {
	return fmt.Sprintf("%v: exit code %d: %v", ErrProcessFailed, e.ExitCode, e.Cause)
}

func (e *ProcessError) Unwrap() []error { return []error{ErrProcessFailed, e.Cause} }

// NewProvider validates configuration and resolves the executable without
// starting Codex or making any network/API calls.
func NewProvider(config Config) (*Provider, error) {
	if config.Sandbox == "" {
		config.Sandbox = "workspace-write"
	}
	if config.ApprovalPolicy == "" {
		config.ApprovalPolicy = "never"
	}
	if !validSandbox(config.Sandbox) {
		return nil, fmt.Errorf("invalid Codex sandbox mode %q", config.Sandbox)
	}
	if config.ApprovalPolicy != "never" && config.ApprovalPolicy != "on-request" {
		return nil, fmt.Errorf("invalid Codex approval policy %q", config.ApprovalPolicy)
	}
	if config.Model != "" && strings.TrimSpace(config.Model) != config.Model {
		return nil, errors.New("Codex model must not contain leading or trailing whitespace")
	}

	executable := config.ExecutablePath
	if executable == "" {
		var err error
		executable, err = exec.LookPath("codex")
		if err != nil {
			return nil, fmt.Errorf("locate Codex CLI: %w", err)
		}
	} else {
		resolved, err := exec.LookPath(executable)
		if err != nil {
			return nil, fmt.Errorf("validate Codex CLI executable %q: %w", executable, err)
		}
		executable = resolved
	}
	info, err := os.Stat(executable)
	if err != nil {
		return nil, fmt.Errorf("inspect Codex CLI executable %q: %w", executable, err)
	}
	if info.IsDir() || !isExecutable(info) {
		return nil, fmt.Errorf("Codex CLI path %q is not an executable file", executable)
	}
	config.ExecutablePath = executable
	if config.EventBuffer <= 0 {
		config.EventBuffer = defaultEventBuffer
	}
	if config.StartupTimeout <= 0 {
		config.StartupTimeout = defaultStartTimeout
	}
	return &Provider{
		config: config,
		command: func(ctx context.Context, executable string, args ...string) *exec.Cmd {
			return exec.CommandContext(ctx, executable, args...)
		},
		runs: make(map[domain.SessionID]*processRun),
	}, nil
}

func validSandbox(value string) bool {
	switch value {
	case "read-only", "workspace-write", "danger-full-access":
		return true
	default:
		return false
	}
}

func isExecutable(info os.FileInfo) bool {
	if filepath.Ext(info.Name()) == ".exe" || filepath.Ext(info.Name()) == ".com" {
		return true
	}
	return info.Mode().Perm()&0o111 != 0
}

func (p *Provider) Name() domain.AgentProvider { return ProviderName }

func (p *Provider) Capabilities() domain.AgentCapabilities {
	return domain.AgentCapabilities{Streaming: true, Interruption: true}
}

// Start launches `codex exec --json` with the prompt on stdin. The caller's
// context owns the process lifetime; callers should provide a session-lifetime
// context and use Interrupt/Close for explicit lifecycle operations.
func (p *Provider) Start(ctx context.Context, request ports.StartRequest) (ports.SessionRef, error) {
	if p == nil {
		return ports.SessionRef{}, errors.New("Codex provider is nil")
	}
	if ctx == nil {
		return ports.SessionRef{}, errors.New("context must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return ports.SessionRef{}, err
	}
	if strings.TrimSpace(string(request.SessionID)) == "" {
		return ports.SessionRef{}, errors.New("session ID is required")
	}
	if err := request.Task.Validate(); err != nil {
		return ports.SessionRef{}, fmt.Errorf("validate Codex task: %w", err)
	}
	if request.Task.Provider != ProviderName {
		return ports.SessionRef{}, fmt.Errorf("Codex provider cannot run task for provider %q", request.Task.Provider)
	}
	if !filepath.IsAbs(request.Task.WorkingDirectory) {
		return ports.SessionRef{}, errors.New("Codex working directory must be absolute")
	}
	info, err := os.Stat(request.Task.WorkingDirectory)
	if err != nil {
		return ports.SessionRef{}, fmt.Errorf("inspect Codex working directory: %w", err)
	}
	if !info.IsDir() {
		return ports.SessionRef{}, fmt.Errorf("Codex working directory %q is not a directory", request.Task.WorkingDirectory)
	}
	if p.config.ExecutablePath == "" {
		return ports.SessionRef{}, errors.New("Codex executable was not resolved")
	}

	nativeID := "codex-cli:" + string(request.SessionID)
	ref := ports.SessionRef{SessionID: request.SessionID, NativeProviderSessionID: nativeID}
	runCtx, cancel := context.WithCancel(ctx)
	run := &processRun{
		ref: ref, cancel: cancel, done: make(chan struct{}), started: make(chan struct{}),
		subs: make(map[uint64]chan domain.AgentEvent),
	}
	p.mu.Lock()
	if _, exists := p.runs[request.SessionID]; exists {
		p.mu.Unlock()
		cancel()
		return ports.SessionRef{}, fmt.Errorf("%w: %q", ErrSessionAlreadyExists, request.SessionID)
	}
	p.runs[request.SessionID] = run
	p.mu.Unlock()
	rollback := func() {
		cancel()
		p.mu.Lock()
		if p.runs[request.SessionID] == run {
			delete(p.runs, request.SessionID)
		}
		p.mu.Unlock()
	}
	args := []string{"exec", "--json", "--cd", request.Task.WorkingDirectory, "--sandbox", p.config.Sandbox, "--ask-for-approval", p.config.ApprovalPolicy}
	if p.config.Model != "" {
		args = append(args, "--model", p.config.Model)
	}
	args = append(args, "-")
	cmd := p.command(runCtx, p.config.ExecutablePath, args...)
	cmd.Dir = request.Task.WorkingDirectory
	cmd.Stdin = strings.NewReader(request.Task.Description)
	configureProcessTree(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		rollback()
		return ports.SessionRef{}, fmt.Errorf("create Codex stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		rollback()
		return ports.SessionRef{}, fmt.Errorf("create Codex stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		rollback()
		return ports.SessionRef{}, fmt.Errorf("start Codex CLI: %w", err)
	}
	run.cmd = cmd

	var readers sync.WaitGroup
	readers.Add(2)
	go func() { defer readers.Done(); p.readOutput(run, stdout, "stdout") }()
	go func() { defer readers.Done(); p.readOutput(run, stderr, "stderr") }()
	go func() {
		readers.Wait()
		p.finish(run)
	}()
	timer := time.NewTimer(p.config.StartupTimeout)
	defer timer.Stop()
	select {
	case <-run.started:
		run.mu.Lock()
		threadID := run.threadID
		resultErr := run.resultErr
		finished := run.finished
		runRef := run.ref
		run.mu.Unlock()
		if threadID != "" {
			return runRef, nil
		}
		if !finished {
			_ = waitForRun(context.Background(), run)
		}
		run.mu.Lock()
		resultErr = run.resultErr
		if resultErr == nil && run.cmd.ProcessState != nil {
			if waitErr := run.cmd.ProcessState.ExitCode(); waitErr != 0 {
				resultErr = &ProcessError{ExitCode: waitErr, Cause: fmt.Errorf("Codex exited with status %d before startup", waitErr)}
			}
		}
		run.mu.Unlock()
		p.removeRun(request.SessionID, run)
		if resultErr == nil {
			resultErr = errors.New("Codex process exited before reporting a thread ID")
		}
		return ports.SessionRef{}, fmt.Errorf("Codex session startup failed: %w", resultErr)
	case <-timer.C:
		cancel()
		_ = waitForRun(context.Background(), run)
		p.removeRun(request.SessionID, run)
		return ports.SessionRef{}, fmt.Errorf("Codex session startup timed out after %s", p.config.StartupTimeout)
	case <-ctx.Done():
		cancel()
		_ = waitForRun(context.Background(), run)
		p.removeRun(request.SessionID, run)
		return ports.SessionRef{}, ctx.Err()
	}
}

func (p *Provider) readOutput(run *processRun, reader io.Reader, stream string) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), maxOutputLine)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		var value any
		if stream == "stdout" && json.Valid(line) {
			value = json.RawMessage(line)
			p.observeProtocolStart(run, line)
		} else {
			value = string(line)
		}
		payload, err := json.Marshal(map[string]any{"stream": stream, "data": value})
		if err != nil {
			p.addRunError(run, fmt.Errorf("encode Codex %s event: %w", stream, err))
			continue
		}
		p.emit(run, EventOutput, payload)
	}
	if err := scanner.Err(); err != nil {
		p.addRunError(run, fmt.Errorf("read Codex %s: %w", stream, err))
		run.cancel()
	}
}

func (p *Provider) observeProtocolStart(run *processRun, line []byte) {
	var event struct {
		Type     string `json:"type"`
		ThreadID string `json:"thread_id"`
	}
	if err := json.Unmarshal(line, &event); err != nil || event.Type != "thread.started" || event.ThreadID == "" {
		return
	}
	run.mu.Lock()
	run.threadID = event.ThreadID
	run.ref.NativeProviderSessionID = event.ThreadID
	run.mu.Unlock()
	run.startOnce.Do(func() { close(run.started) })
}

func (p *Provider) finish(run *processRun) {
	err := run.cmd.Wait()
	run.mu.Lock()
	stopping := run.stopping
	readErr := run.resultErr
	run.mu.Unlock()
	run.startOnce.Do(func() { close(run.started) })

	var eventType domain.AgentEventType
	var payload []byte
	switch {
	case stopping || (errors.Is(err, context.Canceled) && readErr == nil):
		eventType = EventProcessStopped
		payload, _ = json.Marshal(map[string]string{"status": "interrupted"})
		if stopping {
			err = nil
		}
	case err == nil && readErr == nil:
		eventType = EventProcessComplete
		payload, _ = json.Marshal(map[string]string{"status": "completed", "native_session_id": run.ref.NativeProviderSessionID})
	default:
		eventType = EventProcessFailed
		if err == nil || errors.Is(err, context.Canceled) {
			err = readErr
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			err = &ProcessError{ExitCode: exitErr.ExitCode(), Cause: exitErr}
		}
		payload, _ = json.Marshal(map[string]string{"status": "failed", "error": err.Error()})
	}
	p.emit(run, eventType, payload)

	run.mu.Lock()
	run.resultErr = errors.Join(run.resultErr, err)
	if run.publishErr != nil {
		run.resultErr = errors.Join(run.resultErr, fmt.Errorf("publish Codex events: %w", run.publishErr))
	}
	run.finished = true
	for id, ch := range run.subs {
		close(ch)
		delete(run.subs, id)
	}
	close(run.done)
	run.mu.Unlock()
	cancelSafely(run.cancel)
}

func cancelSafely(cancel context.CancelFunc) {
	if cancel != nil {
		cancel()
	}
}

func (p *Provider) addRunError(run *processRun, err error) {
	run.mu.Lock()
	run.resultErr = errors.Join(run.resultErr, err)
	run.mu.Unlock()
}

func (p *Provider) emit(run *processRun, eventType domain.AgentEventType, payload json.RawMessage) {
	run.mu.Lock()
	run.lastEvent = time.Now().UTC()
	stamp := run.lastEvent
	run.mu.Unlock()
	event := domain.AgentEvent{
		ID:        fmt.Sprintf("codex-%s-%d", run.ref.SessionID, stamp.UnixNano()),
		SessionID: run.ref.SessionID,
		Type:      eventType,
		Timestamp: stamp,
		Payload:   payload,
	}
	if err := event.Validate(); err != nil {
		p.addRunError(run, err)
		return
	}
	if p.config.Publisher != nil {
		publishCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := p.config.Publisher.Publish(publishCtx, event); err != nil {
			run.mu.Lock()
			run.publishErr = errors.Join(run.publishErr, err)
			run.mu.Unlock()
		}
		cancel()
	}
	run.mu.Lock()
	run.events = append(run.events, event)
	if len(run.events) > p.config.EventBuffer {
		run.events = append([]domain.AgentEvent(nil), run.events[len(run.events)-p.config.EventBuffer:]...)
	}
	for _, ch := range run.subs {
		if len(ch) == cap(ch) {
			select {
			case <-ch:
			default:
			}
		}
		ch <- event
	}
	run.mu.Unlock()
}

func (p *Provider) getRun(ref ports.SessionRef) (*processRun, error) {
	if p == nil {
		return nil, errors.New("Codex provider is nil")
	}
	p.mu.RLock()
	run := p.runs[ref.SessionID]
	p.mu.RUnlock()
	if run == nil {
		return nil, fmt.Errorf("%w: %q", ErrUnknownSession, ref.SessionID)
	}
	if ref.NativeProviderSessionID != "" && ref.NativeProviderSessionID != run.ref.NativeProviderSessionID {
		return nil, fmt.Errorf("%w: provider session reference does not match", ErrUnknownSession)
	}
	return run, nil
}

func (p *Provider) removeRun(id domain.SessionID, run *processRun) {
	p.mu.Lock()
	if p.runs[id] == run {
		delete(p.runs, id)
	}
	p.mu.Unlock()
}

func (p *Provider) Send(_ context.Context, ref ports.SessionRef, _ ports.AgentInput) error {
	run, err := p.getRun(ref)
	if err != nil {
		return err
	}
	if run.isFinished() {
		return ErrSessionClosed
	}
	return ErrUnsupportedOperation
}

// Subscribe returns a session-scoped event stream. Slow subscribers retain only
// the most recent configured number of events; older queued events are dropped.
func (p *Provider) Subscribe(ctx context.Context, ref ports.SessionRef) (<-chan domain.AgentEvent, error) {
	if ctx == nil {
		return nil, errors.New("context must not be nil")
	}
	run, err := p.getRun(ref)
	if err != nil {
		return nil, err
	}
	run.mu.Lock()
	run.nextSub++
	id := run.nextSub
	ch := make(chan domain.AgentEvent, p.config.EventBuffer)
	for _, event := range run.events {
		ch <- event
	}
	if run.finished {
		close(ch)
	} else {
		run.subs[id] = ch
	}
	run.mu.Unlock()
	if !run.isFinished() {
		go func() {
			select {
			case <-ctx.Done():
				run.removeSubscriber(id)
			case <-run.done:
			}
		}()
	}
	return ch, nil
}

func (run *processRun) isFinished() bool {
	run.mu.Lock()
	defer run.mu.Unlock()
	return run.finished
}

func (run *processRun) removeSubscriber(id uint64) {
	run.mu.Lock()
	if ch, exists := run.subs[id]; exists {
		delete(run.subs, id)
		close(ch)
	}
	run.mu.Unlock()
}

func (p *Provider) Interrupt(ctx context.Context, ref ports.SessionRef) error {
	run, err := p.getRun(ref)
	if err != nil {
		return err
	}
	run.mu.Lock()
	if run.finished {
		run.mu.Unlock()
		return ErrSessionClosed
	}
	run.stopping = true
	run.mu.Unlock()
	run.cancel()
	err = waitForRun(ctx, run)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func (p *Provider) Resume(_ context.Context, ref ports.SessionRef) (ports.SessionRef, error) {
	run, err := p.getRun(ref)
	if err != nil {
		return ports.SessionRef{}, err
	}
	if run.isFinished() {
		return ports.SessionRef{}, ErrSessionClosed
	}
	return ports.SessionRef{}, ErrUnsupportedOperation
}

func (p *Provider) Close(ctx context.Context, ref ports.SessionRef) error {
	run, err := p.getRun(ref)
	if err != nil {
		return err
	}
	run.mu.Lock()
	if !run.finished {
		run.stopping = true
	}
	run.mu.Unlock()
	run.cancel()
	waitErr := waitForRun(ctx, run)
	if errors.Is(waitErr, context.Canceled) {
		waitErr = nil
	}
	if waitErr != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	p.mu.Lock()
	if p.runs[ref.SessionID] == run {
		delete(p.runs, ref.SessionID)
	}
	p.mu.Unlock()
	return waitErr
}

func (p *Provider) Wait(ctx context.Context, ref ports.SessionRef) error {
	run, err := p.getRun(ref)
	if err != nil {
		return err
	}
	return waitForRun(ctx, run)
}

func waitForRun(ctx context.Context, run *processRun) error {
	if ctx == nil {
		return errors.New("context must not be nil")
	}
	select {
	case <-run.done:
		run.mu.Lock()
		defer run.mu.Unlock()
		return run.resultErr
	case <-ctx.Done():
		return ctx.Err()
	}
}
