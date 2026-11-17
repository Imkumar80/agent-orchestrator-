package session

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Imkumar80/agent-orchestrator-/internal/domain"
	"github.com/Imkumar80/agent-orchestrator-/internal/ports"
	"github.com/Imkumar80/agent-orchestrator-/internal/providers"
)

const (
	errorCodeProviderUnavailable    = "provider_unavailable"
	errorCodeProviderStartFailed    = "provider_start_failed"
	errorCodeSessionPersistFailed   = "session_persistence_failed"
	errorCodeInvalidProviderSession = "invalid_provider_session"
)

// Manager coordinates durable sessions with registered agent providers.
type Manager struct {
	repository ports.SessionRepository
	registry   *providers.Registry
	locks      *sessionLocker
	lifecycle  *LifecycleManager
	now        func() time.Time
	newID      func() (domain.SessionID, error)
}

// NewManager creates a session manager using the given repository and registry.
func NewManager(repository ports.SessionRepository, registry *providers.Registry) (*Manager, error) {
	if repository == nil || registry == nil {
		return nil, ErrInvalidDependency
	}
	locks := newSessionLocker()
	now := func() time.Time { return time.Now().UTC() }
	lifecycle := newLifecycleManager(repository, locks, now)
	return &Manager{
		repository: repository,
		registry:   registry,
		locks:      locks,
		lifecycle:  lifecycle,
		now:        now,
		newID:      newSessionID,
	}, nil
}

// Create validates a task and persists a new pending session.
func (m *Manager) Create(ctx context.Context, task domain.Task) (domain.Session, error) {
	if m == nil || m.repository == nil {
		return domain.Session{}, ErrInvalidDependency
	}
	if err := ctx.Err(); err != nil {
		return domain.Session{}, err
	}
	if err := task.Validate(); err != nil {
		return domain.Session{}, err
	}
	id, err := m.newID()
	if err != nil {
		return domain.Session{}, err
	}
	now := m.now().UTC()
	session := domain.Session{
		ID:        id,
		TaskID:    task.ID,
		Provider:  task.Provider,
		Status:    domain.SessionPending,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := session.Validate(); err != nil {
		return domain.Session{}, err
	}
	if err := m.repository.Create(ctx, session); err != nil {
		return domain.Session{}, fmt.Errorf("create session for task %q: %w", task.ID, err)
	}
	return session, nil
}

// CreateAndStart creates a durable session before starting its provider.
// On provider failure it returns the persisted failed session with the cause.
func (m *Manager) CreateAndStart(ctx context.Context, task domain.Task) (domain.Session, error) {
	return m.CreateAndStartWithContext(ctx, task, "")
}

// CreateAndStartWithContext passes optional provider-neutral reference context
// through the existing provider request without changing the user task.
func (m *Manager) CreateAndStartWithContext(ctx context.Context, task domain.Task, memoryContext string) (domain.Session, error) {
	session, err := m.Create(ctx, task)
	if err != nil {
		return domain.Session{}, err
	}
	return m.StartWithContext(ctx, session.ID, task, memoryContext)
}

// Start starts a pending session. The task is supplied because the repository
// stores sessions only; its ID and provider must match the persisted session.
// Repeated calls for an already running or waiting session are harmless.
func (m *Manager) Start(ctx context.Context, id domain.SessionID, task domain.Task) (domain.Session, error) {
	return m.StartWithContext(ctx, id, task, "")
}

// StartWithContext starts a session with optional provider-neutral memory references.
func (m *Manager) StartWithContext(ctx context.Context, id domain.SessionID, task domain.Task, memoryContext string) (domain.Session, error) {
	if m == nil || m.repository == nil || m.registry == nil {
		return domain.Session{}, ErrInvalidDependency
	}
	if err := task.Validate(); err != nil {
		return domain.Session{}, err
	}
	unlock, err := m.locks.acquire(ctx, id)
	if err != nil {
		return domain.Session{}, fmt.Errorf("lock session %q for start: %w", id, err)
	}
	defer unlock()

	session, err := m.get(ctx, id)
	if err != nil {
		return domain.Session{}, err
	}
	if task.ID != session.TaskID || task.Provider != session.Provider {
		return session, fmt.Errorf("%w: task %q/provider %q do not match session", ErrTaskSessionMismatch, task.ID, task.Provider)
	}
	if session.Status == domain.SessionRunning || session.Status == domain.SessionWaitingForInput {
		return session, nil
	}
	if session.Status != domain.SessionPending {
		return session, fmt.Errorf("%w: cannot start session %q from %q", domain.ErrInvalidTransition, id, session.Status)
	}

	starting, err := m.lifecycle.transitionLocked(ctx, session, domain.SessionStarting, nil, nil)
	if err != nil {
		return session, err
	}
	provider, err := m.registry.Get(starting.Provider)
	if err != nil {
		cause := fmt.Errorf("resolve provider %q: %w", starting.Provider, err)
		return m.failLocked(ctx, starting, cause, errorCodeProviderUnavailable,
			fmt.Sprintf("provider %q is unavailable", starting.Provider))
	}

	ref, startErr := provider.Start(ctx, ports.StartRequest{SessionID: starting.ID, Task: task, MemoryContext: memoryContext})
	if startErr != nil {
		cleanupRef := ports.SessionRef{
			SessionID:               starting.ID,
			NativeProviderSessionID: ref.NativeProviderSessionID,
		}
		cleanupErr := provider.Close(ctx, cleanupRef)
		cause := fmt.Errorf("start provider %q: %w", starting.Provider, startErr)
		if cleanupErr != nil {
			cause = errors.Join(cause, fmt.Errorf("clean up failed provider start: %w", cleanupErr))
		}
		return m.failLocked(ctx, starting, cause, errorCodeProviderStartFailed,
			fmt.Sprintf("provider %q failed to start the session", starting.Provider))
	}
	if ref.SessionID != starting.ID {
		cause := fmt.Errorf("%w: got session ID %q, want %q", ErrInvalidProviderSession, ref.SessionID, starting.ID)
		cleanupRef := ports.SessionRef{
			SessionID:               starting.ID,
			NativeProviderSessionID: ref.NativeProviderSessionID,
		}
		cleanupErr := provider.Close(ctx, cleanupRef)
		if cleanupErr != nil {
			cause = errors.Join(cause, fmt.Errorf("clean up invalid provider session: %w", cleanupErr))
		}
		return m.failLocked(ctx, starting, cause, errorCodeInvalidProviderSession,
			"provider returned a session reference with a mismatched session ID")
	}

	running, err := m.lifecycle.transitionLocked(ctx, starting, domain.SessionRunning, nil, func(updated *domain.Session) {
		updated.NativeProviderSessionID = ref.NativeProviderSessionID
	})
	if err != nil {
		cleanupErr := provider.Close(ctx, ref)
		if cleanupErr == nil {
			failed, failureErr := m.lifecycle.transitionLocked(ctx, starting, domain.SessionFailed,
				&domain.ErrorInfo{
					Code:    errorCodeSessionPersistFailed,
					Message: "could not persist the provider-started session",
				}, nil)
			if failureErr == nil {
				return failed, err
			}
			return starting, errors.Join(err, failureErr)
		}
		return starting, errors.Join(err, fmt.Errorf("clean up unpersisted provider session: %w", cleanupErr))
	}
	if waiter, ok := provider.(ports.AgentExecutionWaiter); ok {
		go m.observeExecution(ctx, waiter, ref)
	}
	return running, nil
}

// observeExecution persists a provider's exit outcome without depending on
// provider-specific event names or payloads. Providers without the optional
// waiter capability retain the existing lifecycle behavior.
func (m *Manager) observeExecution(ctx context.Context, waiter ports.AgentExecutionWaiter, ref ports.SessionRef) {
	err := waiter.Wait(ctx, ref)
	if ctx.Err() != nil {
		return
	}
	unlock, lockErr := m.locks.acquire(ctx, ref.SessionID)
	if lockErr != nil {
		return
	}
	defer unlock()
	current, getErr := m.get(ctx, ref.SessionID)
	if getErr != nil || current.Status.Terminal() {
		return
	}
	next := domain.SessionCompleted
	var failure *domain.ErrorInfo
	if errors.Is(err, context.Canceled) {
		next = domain.SessionInterrupted
	} else if err != nil {
		next = domain.SessionFailed
		failure = &domain.ErrorInfo{Code: "provider_execution_failed", Message: "provider execution exited with an error; inspect the session events for details"}
	}
	if _, transitionErr := m.lifecycle.transitionLocked(ctx, current, next, failure, nil); transitionErr != nil {
		// The session can become terminal through a concurrent interrupt/close.
		// A failed persistence attempt is observable by the repository and leaves
		// recovery to reconcile the still-nonterminal persisted state.
		return
	}
}

// Get retrieves a validated session snapshot.
func (m *Manager) Get(ctx context.Context, id domain.SessionID) (domain.Session, error) {
	if m == nil || m.repository == nil {
		return domain.Session{}, ErrInvalidDependency
	}
	return m.get(ctx, id)
}

// List returns persisted session snapshots when the configured repository
// provides the optional listing capability.
func (m *Manager) List(ctx context.Context) ([]domain.Session, error) {
	if m == nil || m.repository == nil {
		return nil, ErrInvalidDependency
	}
	lister, ok := m.repository.(ports.SessionLister)
	if !ok {
		return nil, errors.New("session repository does not support listing")
	}
	items, err := lister.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	return items, nil
}

// Interrupt interrupts an active provider session and persists its terminal state.
// Repeated interruption of an already interrupted session is a no-op.
func (m *Manager) Interrupt(ctx context.Context, id domain.SessionID) (domain.Session, error) {
	if m == nil || m.repository == nil || m.registry == nil {
		return domain.Session{}, ErrInvalidDependency
	}
	unlock, err := m.locks.acquire(ctx, id)
	if err != nil {
		return domain.Session{}, fmt.Errorf("lock session %q for interrupt: %w", id, err)
	}
	defer unlock()

	session, err := m.get(ctx, id)
	if err != nil {
		return domain.Session{}, err
	}
	if session.Status == domain.SessionInterrupted {
		return session, nil
	}
	if session.Status != domain.SessionRunning && session.Status != domain.SessionWaitingForInput {
		return session, fmt.Errorf("%w: cannot interrupt session %q from %q", domain.ErrInvalidTransition, id, session.Status)
	}
	provider, err := m.registry.Get(session.Provider)
	if err != nil {
		return session, fmt.Errorf("resolve provider %q for interrupt: %w", session.Provider, err)
	}
	if !provider.Capabilities().Interruption {
		return session, fmt.Errorf("%w: provider %q cannot interrupt", ErrUnsupportedCapability, session.Provider)
	}
	if err := provider.Interrupt(ctx, providerSessionRef(session)); err != nil {
		return session, fmt.Errorf("interrupt provider session %q: %w", id, err)
	}
	return m.lifecycle.transitionLocked(ctx, session, domain.SessionInterrupted, nil, nil)
}

// Resume resumes a waiting, nonterminal provider session when the provider
// advertises resume support. Interrupted sessions remain terminal.
func (m *Manager) Resume(ctx context.Context, id domain.SessionID) (domain.Session, error) {
	if m == nil || m.repository == nil || m.registry == nil {
		return domain.Session{}, ErrInvalidDependency
	}
	unlock, err := m.locks.acquire(ctx, id)
	if err != nil {
		return domain.Session{}, fmt.Errorf("lock session %q for resume: %w", id, err)
	}
	defer unlock()

	session, err := m.get(ctx, id)
	if err != nil {
		return domain.Session{}, err
	}
	if session.Status == domain.SessionRunning {
		return session, nil
	}
	if session.Status != domain.SessionWaitingForInput {
		return session, fmt.Errorf("%w: cannot resume session %q from %q", domain.ErrInvalidTransition, id, session.Status)
	}
	provider, err := m.registry.Get(session.Provider)
	if err != nil {
		return session, fmt.Errorf("resolve provider %q for resume: %w", session.Provider, err)
	}
	if !provider.Capabilities().Resume {
		return session, fmt.Errorf("%w: provider %q cannot resume", ErrUnsupportedCapability, session.Provider)
	}

	ref, err := provider.Resume(ctx, providerSessionRef(session))
	if err != nil {
		return session, fmt.Errorf("resume provider session %q: %w", id, err)
	}
	if ref.SessionID != session.ID {
		cause := fmt.Errorf("%w: got session ID %q, want %q", ErrInvalidProviderSession, ref.SessionID, session.ID)
		cleanupRef := ports.SessionRef{
			SessionID:               session.ID,
			NativeProviderSessionID: ref.NativeProviderSessionID,
		}
		if cleanupErr := provider.Interrupt(ctx, cleanupRef); cleanupErr != nil {
			cause = errors.Join(cause, fmt.Errorf("interrupt invalid resumed provider session: %w", cleanupErr))
		}
		return session, cause
	}
	running, err := m.lifecycle.transitionLocked(ctx, session, domain.SessionRunning, nil, func(updated *domain.Session) {
		if ref.NativeProviderSessionID != "" {
			updated.NativeProviderSessionID = ref.NativeProviderSessionID
		}
	})
	if err != nil {
		cleanupErr := provider.Interrupt(ctx, ref)
		if cleanupErr != nil {
			return session, errors.Join(err, fmt.Errorf("interrupt unpersisted resumed session: %w", cleanupErr))
		}
		return session, err
	}
	return running, nil
}

// Close releases provider resources. A nonterminal session becomes interrupted
// only after the provider confirms closure; completed or failed states remain.
func (m *Manager) Close(ctx context.Context, id domain.SessionID) error {
	if m == nil || m.repository == nil || m.registry == nil {
		return ErrInvalidDependency
	}
	unlock, err := m.locks.acquire(ctx, id)
	if err != nil {
		return fmt.Errorf("lock session %q for close: %w", id, err)
	}
	defer unlock()

	session, err := m.get(ctx, id)
	if err != nil {
		return err
	}
	if session.Status == domain.SessionPending {
		_, err := m.lifecycle.transitionLocked(ctx, session, domain.SessionInterrupted, nil, nil)
		return err
	}
	provider, err := m.registry.Get(session.Provider)
	if err != nil {
		return fmt.Errorf("resolve provider %q for close: %w", session.Provider, err)
	}
	if err := provider.Close(ctx, providerSessionRef(session)); err != nil {
		return fmt.Errorf("close provider session %q: %w", id, err)
	}
	if session.Status.Terminal() {
		return nil
	}
	_, err = m.lifecycle.transitionLocked(ctx, session, domain.SessionInterrupted, nil, nil)
	return err
}

func (m *Manager) get(ctx context.Context, id domain.SessionID) (domain.Session, error) {
	if err := ctx.Err(); err != nil {
		return domain.Session{}, err
	}
	session, err := m.repository.Get(ctx, id)
	if err != nil {
		return domain.Session{}, fmt.Errorf("get session %q: %w", id, err)
	}
	if err := session.Validate(); err != nil {
		return domain.Session{}, fmt.Errorf("validate stored session %q: %w", id, err)
	}
	return session, nil
}

func (m *Manager) failLocked(ctx context.Context, current domain.Session, cause error, code, message string) (domain.Session, error) {
	failed, err := m.lifecycle.transitionLocked(ctx, current, domain.SessionFailed,
		&domain.ErrorInfo{Code: code, Message: message}, nil)
	if err != nil {
		return current, errors.Join(cause, fmt.Errorf("persist failed session %q: %w", current.ID, err))
	}
	return failed, cause
}

func providerSessionRef(session domain.Session) ports.SessionRef {
	return ports.SessionRef{
		SessionID:               session.ID,
		NativeProviderSessionID: session.NativeProviderSessionID,
	}
}
