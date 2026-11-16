package session

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Imkumar80/agent-orchestrator-/internal/domain"
	"github.com/Imkumar80/agent-orchestrator-/internal/ports"
	"github.com/Imkumar80/agent-orchestrator-/internal/providers"
)

var (
	errProviderStart = errors.New("provider start failed")
	errInterrupt     = errors.New("provider interrupt failed")
	errRepoUpdate    = errors.New("repository update failed")
)

var _ ports.AgentProvider = (*mockAgentProvider)(nil)
var _ ports.SessionRepository = (*memorySessionRepository)(nil)

type memorySessionRepository struct {
	mu          sync.RWMutex
	sessions    map[domain.SessionID]domain.Session
	createErr   error
	updateErrAt map[int]error
	updateCalls int
}

func newMemorySessionRepository() *memorySessionRepository {
	return &memorySessionRepository{
		sessions:    make(map[domain.SessionID]domain.Session),
		updateErrAt: make(map[int]error),
	}
}

func (r *memorySessionRepository) Create(_ context.Context, session domain.Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.createErr != nil {
		return r.createErr
	}
	if _, exists := r.sessions[session.ID]; exists {
		return fmt.Errorf("session %q already exists", session.ID)
	}
	r.sessions[session.ID] = session
	return nil
}

func (r *memorySessionRepository) Get(_ context.Context, id domain.SessionID) (domain.Session, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	session, exists := r.sessions[id]
	if !exists {
		return domain.Session{}, fmt.Errorf("%w: %q", domain.ErrSessionNotFound, id)
	}
	return session, nil
}

func (r *memorySessionRepository) Update(_ context.Context, session domain.Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updateCalls++
	if err := r.updateErrAt[r.updateCalls]; err != nil {
		return err
	}
	if _, exists := r.sessions[session.ID]; !exists {
		return fmt.Errorf("%w: %q", domain.ErrSessionNotFound, session.ID)
	}
	r.sessions[session.ID] = session
	return nil
}

type mockAgentProvider struct {
	mu               sync.Mutex
	name             domain.AgentProvider
	capabilities     domain.AgentCapabilities
	startErr         error
	interruptErr     error
	resumeErr        error
	closeErr         error
	startEntered     chan struct{}
	startRelease     <-chan struct{}
	interruptEntered chan struct{}
	interruptRelease <-chan struct{}
	startCalls       int
	interruptCalls   int
	resumeCalls      int
	closeCalls       int
}

func (p *mockAgentProvider) Name() domain.AgentProvider {
	return p.name
}

func (p *mockAgentProvider) Capabilities() domain.AgentCapabilities {
	return p.capabilities
}

func (p *mockAgentProvider) Start(ctx context.Context, request ports.StartRequest) (ports.SessionRef, error) {
	p.mu.Lock()
	p.startCalls++
	entered := p.startEntered
	release := p.startRelease
	startErr := p.startErr
	p.mu.Unlock()
	if entered != nil {
		select {
		case entered <- struct{}{}:
		default:
		}
	}
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return ports.SessionRef{}, ctx.Err()
		}
	}
	if startErr != nil {
		return ports.SessionRef{}, startErr
	}
	return ports.SessionRef{
		SessionID:               request.SessionID,
		NativeProviderSessionID: "native-session-1",
	}, nil
}

func (p *mockAgentProvider) Send(context.Context, ports.SessionRef, ports.AgentInput) error {
	return nil
}

func (p *mockAgentProvider) Subscribe(context.Context, ports.SessionRef) (<-chan domain.AgentEvent, error) {
	return nil, nil
}

func (p *mockAgentProvider) Interrupt(context.Context, ports.SessionRef) error {
	p.mu.Lock()
	p.interruptCalls++
	entered := p.interruptEntered
	release := p.interruptRelease
	interruptErr := p.interruptErr
	p.mu.Unlock()
	if entered != nil {
		select {
		case entered <- struct{}{}:
		default:
		}
	}
	if release != nil {
		<-release
	}
	return interruptErr
}

func (p *mockAgentProvider) Resume(_ context.Context, session ports.SessionRef) (ports.SessionRef, error) {
	p.mu.Lock()
	p.resumeCalls++
	resumeErr := p.resumeErr
	p.mu.Unlock()
	if resumeErr != nil {
		return ports.SessionRef{}, resumeErr
	}
	return session, nil
}

func (p *mockAgentProvider) Close(context.Context, ports.SessionRef) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closeCalls++
	return p.closeErr
}

func (p *mockAgentProvider) counts() (starts, interrupts, resumes, closes int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.startCalls, p.interruptCalls, p.resumeCalls, p.closeCalls
}

func newTestManager(t *testing.T, repository *memorySessionRepository, capability domain.AgentCapabilities) (*Manager, *mockAgentProvider) {
	t.Helper()
	registry := providers.NewRegistry()
	provider := &mockAgentProvider{name: domain.ProviderCodex, capabilities: capability}
	if err := registry.Register(provider); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	manager, err := NewManager(repository, registry)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	return manager, provider
}

func testTask(t *testing.T) domain.Task {
	t.Helper()
	return domain.Task{
		ID:               "task-1",
		Description:      "implement a focused task",
		Provider:         domain.ProviderCodex,
		WorkingDirectory: t.TempDir(),
		CreatedAt:        time.Now().UTC(),
	}
}

func TestCreateAndStartSession(t *testing.T) {
	repository := newMemorySessionRepository()
	manager, provider := newTestManager(t, repository, domain.AgentCapabilities{Interruption: true})

	session, err := manager.CreateAndStart(context.Background(), testTask(t))
	if err != nil {
		t.Fatalf("CreateAndStart() error = %v", err)
	}
	if session.Status != domain.SessionRunning {
		t.Fatalf("session status = %q, want running", session.Status)
	}
	if session.UpdatedAt.Before(session.CreatedAt) {
		t.Fatalf("updated_at %s precedes created_at %s", session.UpdatedAt, session.CreatedAt)
	}
	if session.NativeProviderSessionID != "native-session-1" {
		t.Fatalf("native session ID = %q, want native-session-1", session.NativeProviderSessionID)
	}
	stored, err := manager.Get(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if stored.Status != domain.SessionRunning {
		t.Fatalf("stored status = %q, want running", stored.Status)
	}
	starts, _, _, _ := provider.counts()
	if starts != 1 {
		t.Fatalf("provider start calls = %d, want 1", starts)
	}
}

func TestCreateAndStartMissingProvider(t *testing.T) {
	repository := newMemorySessionRepository()
	manager, err := NewManager(repository, providers.NewRegistry())
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	session, err := manager.CreateAndStart(context.Background(), testTask(t))
	if !errors.Is(err, providers.ErrProviderNotFound) {
		t.Fatalf("CreateAndStart() error = %v, want ErrProviderNotFound", err)
	}
	if session.Status != domain.SessionFailed {
		t.Fatalf("session status = %q, want failed", session.Status)
	}
	if session.Error == nil || session.Error.Code != errorCodeProviderUnavailable {
		t.Fatalf("session error = %#v, want provider unavailable", session.Error)
	}
}

func TestCreateAndStartProviderFailure(t *testing.T) {
	repository := newMemorySessionRepository()
	manager, provider := newTestManager(t, repository, domain.AgentCapabilities{})
	provider.startErr = errProviderStart

	session, err := manager.CreateAndStart(context.Background(), testTask(t))
	if !errors.Is(err, errProviderStart) {
		t.Fatalf("CreateAndStart() error = %v, want provider start cause", err)
	}
	if session.Status != domain.SessionFailed {
		t.Fatalf("session status = %q, want failed", session.Status)
	}
	if session.Error == nil || session.Error.Code != errorCodeProviderStartFailed {
		t.Fatalf("session error = %#v, want provider start failure", session.Error)
	}
	starts, _, _, closes := provider.counts()
	if starts != 1 || closes != 1 {
		t.Fatalf("provider calls: starts=%d closes=%d, want 1 each", starts, closes)
	}
}

func TestCreateAndStartPersistenceFailures(t *testing.T) {
	t.Run("create fails before provider starts", func(t *testing.T) {
		repository := newMemorySessionRepository()
		repository.createErr = errRepoUpdate
		manager, provider := newTestManager(t, repository, domain.AgentCapabilities{})
		if _, err := manager.CreateAndStart(context.Background(), testTask(t)); !errors.Is(err, errRepoUpdate) {
			t.Fatalf("CreateAndStart() error = %v, want repository error", err)
		}
		starts, _, _, _ := provider.counts()
		if starts != 0 {
			t.Fatalf("provider start calls = %d, want 0", starts)
		}
	})

	t.Run("starting transition fails before provider starts", func(t *testing.T) {
		repository := newMemorySessionRepository()
		repository.updateErrAt[1] = errRepoUpdate
		manager, provider := newTestManager(t, repository, domain.AgentCapabilities{})
		task := testTask(t)
		session, err := manager.Create(context.Background(), task)
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		if _, err := manager.Start(context.Background(), session.ID, task); !errors.Is(err, errRepoUpdate) {
			t.Fatalf("Start() error = %v, want repository error", err)
		}
		stored, _ := repository.Get(context.Background(), session.ID)
		if stored.Status != domain.SessionPending {
			t.Fatalf("stored status = %q, want pending", stored.Status)
		}
		starts, _, _, _ := provider.counts()
		if starts != 0 {
			t.Fatalf("provider start calls = %d, want 0", starts)
		}
	})

	t.Run("running transition failure closes provider and records failure", func(t *testing.T) {
		repository := newMemorySessionRepository()
		repository.updateErrAt[2] = errRepoUpdate
		manager, provider := newTestManager(t, repository, domain.AgentCapabilities{})

		session, err := manager.CreateAndStart(context.Background(), testTask(t))
		if !errors.Is(err, errRepoUpdate) {
			t.Fatalf("CreateAndStart() error = %v, want repository error", err)
		}
		if session.Status != domain.SessionFailed {
			t.Fatalf("returned status = %q, want failed", session.Status)
		}
		stored, getErr := repository.Get(context.Background(), session.ID)
		if getErr != nil {
			t.Fatalf("repository Get() error = %v", getErr)
		}
		if stored.Status != domain.SessionFailed || stored.Error == nil || stored.Error.Code != errorCodeSessionPersistFailed {
			t.Fatalf("stored session = %#v, want failed with persistence error", stored)
		}
		starts, _, _, closes := provider.counts()
		if starts != 1 || closes != 1 {
			t.Fatalf("provider calls: starts=%d closes=%d, want 1 each", starts, closes)
		}
	})
}

func TestInterruptRunningSession(t *testing.T) {
	manager, provider := newTestManager(t, newMemorySessionRepository(), domain.AgentCapabilities{Interruption: true})
	session, err := manager.CreateAndStart(context.Background(), testTask(t))
	if err != nil {
		t.Fatalf("CreateAndStart() error = %v", err)
	}

	interrupted, err := manager.Interrupt(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("Interrupt() error = %v", err)
	}
	if interrupted.Status != domain.SessionInterrupted {
		t.Fatalf("status = %q, want interrupted", interrupted.Status)
	}
	_, interrupts, _, _ := provider.counts()
	if interrupts != 1 {
		t.Fatalf("provider interrupt calls = %d, want 1", interrupts)
	}
}

func TestInterruptProviderFailureLeavesSessionRunning(t *testing.T) {
	manager, provider := newTestManager(t, newMemorySessionRepository(), domain.AgentCapabilities{Interruption: true})
	session, err := manager.CreateAndStart(context.Background(), testTask(t))
	if err != nil {
		t.Fatalf("CreateAndStart() error = %v", err)
	}
	provider.interruptErr = errInterrupt

	if _, err := manager.Interrupt(context.Background(), session.ID); !errors.Is(err, errInterrupt) {
		t.Fatalf("Interrupt() error = %v, want provider error", err)
	}
	stored, err := manager.Get(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if stored.Status != domain.SessionRunning {
		t.Fatalf("status = %q, want running after provider failure", stored.Status)
	}
}

func TestResumeRequiresCapabilityAndNonterminalState(t *testing.T) {
	manager, provider := newTestManager(t, newMemorySessionRepository(), domain.AgentCapabilities{Interruption: true})
	session, err := manager.CreateAndStart(context.Background(), testTask(t))
	if err != nil {
		t.Fatalf("CreateAndStart() error = %v", err)
	}
	waiting, err := manager.lifecycle.Transition(context.Background(), session.ID, domain.SessionWaitingForInput, nil)
	if err != nil {
		t.Fatalf("Transition() error = %v", err)
	}

	if _, err := manager.Resume(context.Background(), waiting.ID); !errors.Is(err, ErrUnsupportedCapability) {
		t.Fatalf("Resume() error = %v, want ErrUnsupportedCapability", err)
	}
	_, _, resumes, _ := provider.counts()
	if resumes != 0 {
		t.Fatalf("provider resume calls = %d, want 0", resumes)
	}

	if _, err := manager.Interrupt(context.Background(), waiting.ID); err != nil {
		t.Fatalf("Interrupt() error = %v", err)
	}
	if _, err := manager.Resume(context.Background(), waiting.ID); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("Resume() of terminal session error = %v, want ErrInvalidTransition", err)
	}
	_, _, resumes, _ = provider.counts()
	if resumes != 0 {
		t.Fatalf("provider resume calls after terminal attempt = %d, want 0", resumes)
	}
}

func TestResumeWaitingSessionWhenSupported(t *testing.T) {
	manager, provider := newTestManager(t, newMemorySessionRepository(), domain.AgentCapabilities{Resume: true})
	session, err := manager.CreateAndStart(context.Background(), testTask(t))
	if err != nil {
		t.Fatalf("CreateAndStart() error = %v", err)
	}
	waiting, err := manager.lifecycle.Transition(context.Background(), session.ID, domain.SessionWaitingForInput, nil)
	if err != nil {
		t.Fatalf("Transition() error = %v", err)
	}

	resumed, err := manager.Resume(context.Background(), waiting.ID)
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if resumed.Status != domain.SessionRunning {
		t.Fatalf("status = %q, want running", resumed.Status)
	}
	_, _, resumes, _ := provider.counts()
	if resumes != 1 {
		t.Fatalf("provider resume calls = %d, want 1", resumes)
	}
}

func TestLifecycleRejectsInvalidTransition(t *testing.T) {
	repository := newMemorySessionRepository()
	manager, _ := newTestManager(t, repository, domain.AgentCapabilities{})
	session, err := manager.Create(context.Background(), testTask(t))
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := manager.lifecycle.Transition(context.Background(), session.ID, domain.SessionCompleted, nil); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("Transition() error = %v, want ErrInvalidTransition", err)
	}
	stored, _ := repository.Get(context.Background(), session.ID)
	if stored.Status != domain.SessionPending {
		t.Fatalf("stored status = %q, want pending", stored.Status)
	}
}

func TestCloseReleasesProviderAndInterruptsActiveSession(t *testing.T) {
	manager, provider := newTestManager(t, newMemorySessionRepository(), domain.AgentCapabilities{})
	session, err := manager.CreateAndStart(context.Background(), testTask(t))
	if err != nil {
		t.Fatalf("CreateAndStart() error = %v", err)
	}
	if err := manager.Close(context.Background(), session.ID); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	stored, err := manager.Get(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if stored.Status != domain.SessionInterrupted {
		t.Fatalf("status = %q, want interrupted", stored.Status)
	}
	_, _, _, closes := provider.counts()
	if closes != 1 {
		t.Fatalf("provider close calls = %d, want 1", closes)
	}
}

func TestConcurrentStartIsSerializedPerSession(t *testing.T) {
	repository := newMemorySessionRepository()
	manager, provider := newTestManager(t, repository, domain.AgentCapabilities{})
	provider.startEntered = make(chan struct{}, 1)
	startRelease := make(chan struct{})
	provider.startRelease = startRelease
	task := testTask(t)
	session, err := manager.Create(context.Background(), task)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	type result struct {
		session domain.Session
		err     error
	}
	results := make(chan result, 2)
	go func() {
		got, err := manager.Start(context.Background(), session.ID, task)
		results <- result{session: got, err: err}
	}()
	<-provider.startEntered
	go func() {
		got, err := manager.Start(context.Background(), session.ID, task)
		results <- result{session: got, err: err}
	}()
	close(startRelease)

	for i := 0; i < 2; i++ {
		got := <-results
		if got.err != nil {
			t.Fatalf("Start() error = %v", got.err)
		}
		if got.session.Status != domain.SessionRunning {
			t.Fatalf("returned status = %q, want running", got.session.Status)
		}
	}
	starts, _, _, _ := provider.counts()
	if starts != 1 {
		t.Fatalf("provider start calls = %d, want 1", starts)
	}
}

func TestConcurrentInterruptIsSerializedPerSession(t *testing.T) {
	manager, provider := newTestManager(t, newMemorySessionRepository(), domain.AgentCapabilities{Interruption: true})
	session, err := manager.CreateAndStart(context.Background(), testTask(t))
	if err != nil {
		t.Fatalf("CreateAndStart() error = %v", err)
	}
	provider.interruptEntered = make(chan struct{}, 1)
	interruptRelease := make(chan struct{})
	provider.interruptRelease = interruptRelease

	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, err := manager.Interrupt(context.Background(), session.ID)
			results <- err
		}()
		if i == 0 {
			<-provider.interruptEntered
		}
	}
	close(interruptRelease)
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Fatalf("Interrupt() error = %v", err)
		}
	}
	_, interrupts, _, _ := provider.counts()
	if interrupts != 1 {
		t.Fatalf("provider interrupt calls = %d, want 1", interrupts)
	}
}

func TestSessionLockerCancellationAndDifferentSessionScope(t *testing.T) {
	locks := newSessionLocker()
	firstUnlock, err := locks.acquire(context.Background(), "session-a")
	if err != nil {
		t.Fatalf("first acquire() error = %v", err)
	}
	otherUnlock, err := locks.acquire(context.Background(), "session-b")
	if err != nil {
		t.Fatalf("different session acquire() error = %v", err)
	}
	otherUnlock()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := locks.acquire(ctx, "session-a"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled acquire() error = %v, want context.Canceled", err)
	}
	firstUnlock()
}

func TestCreateValidatesTask(t *testing.T) {
	manager, _ := newTestManager(t, newMemorySessionRepository(), domain.AgentCapabilities{})
	task := testTask(t)
	task.WorkingDirectory = filepath.Join("relative", "path")
	if _, err := manager.Create(context.Background(), task); !errors.Is(err, domain.ErrInvalidTask) {
		t.Fatalf("Create() error = %v, want ErrInvalidTask", err)
	}
}

func TestGetMissingSessionPreservesDomainError(t *testing.T) {
	manager, _ := newTestManager(t, newMemorySessionRepository(), domain.AgentCapabilities{})
	if _, err := manager.Get(context.Background(), "missing"); !errors.Is(err, domain.ErrSessionNotFound) {
		t.Fatalf("Get() error = %v, want domain.ErrSessionNotFound", err)
	}
}

func TestCloseFailureDoesNotMarkSessionInterrupted(t *testing.T) {
	manager, provider := newTestManager(t, newMemorySessionRepository(), domain.AgentCapabilities{})
	session, err := manager.CreateAndStart(context.Background(), testTask(t))
	if err != nil {
		t.Fatalf("CreateAndStart() error = %v", err)
	}
	provider.closeErr = errors.New("provider close failed")

	if err := manager.Close(context.Background(), session.ID); err == nil {
		t.Fatal("Close() error = nil, want provider close error")
	}
	stored, err := manager.Get(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if stored.Status != domain.SessionRunning {
		t.Fatalf("status = %q, want running after close failure", stored.Status)
	}
}
