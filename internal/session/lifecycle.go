package session

import (
	"context"
	"fmt"
	"time"

	"github.com/Imkumar80/agent-orchestrator-/internal/domain"
	"github.com/Imkumar80/agent-orchestrator-/internal/ports"
)

// LifecycleManager persists valid domain session transitions.
type LifecycleManager struct {
	repository ports.SessionRepository
	locks      *sessionLocker
	now        func() time.Time
}

// NewLifecycleManager creates a lifecycle manager for the repository.
func NewLifecycleManager(repository ports.SessionRepository) (*LifecycleManager, error) {
	if repository == nil {
		return nil, ErrInvalidDependency
	}
	return newLifecycleManager(repository, newSessionLocker(), time.Now), nil
}

func newLifecycleManager(repository ports.SessionRepository, locks *sessionLocker, now func() time.Time) *LifecycleManager {
	return &LifecycleManager{repository: repository, locks: locks, now: now}
}

// Transition applies and persists a valid transition for the identified session.
func (l *LifecycleManager) Transition(ctx context.Context, id domain.SessionID, next domain.SessionStatus, failure *domain.ErrorInfo) (domain.Session, error) {
	if l == nil || l.repository == nil || l.locks == nil {
		return domain.Session{}, ErrInvalidDependency
	}
	unlock, err := l.locks.acquire(ctx, id)
	if err != nil {
		return domain.Session{}, fmt.Errorf("lock session %q: %w", id, err)
	}
	defer unlock()

	current, err := l.repository.Get(ctx, id)
	if err != nil {
		return domain.Session{}, fmt.Errorf("get session %q for transition: %w", id, err)
	}
	return l.transitionLocked(ctx, current, next, failure, nil)
}

// transitionLocked expects the caller to hold the lock for current.ID.
func (l *LifecycleManager) transitionLocked(ctx context.Context, current domain.Session, next domain.SessionStatus, failure *domain.ErrorInfo, update func(*domain.Session)) (domain.Session, error) {
	if err := ctx.Err(); err != nil {
		return current, err
	}
	updated := current
	if update != nil {
		update(&updated)
	}
	if err := updated.Transition(next, l.now().UTC()); err != nil {
		return current, err
	}
	if failure != nil {
		updated.Error = failure
	}
	if err := updated.Validate(); err != nil {
		return current, err
	}
	if err := l.repository.Update(ctx, updated); err != nil {
		return current, fmt.Errorf("persist session %q transition to %q: %w", current.ID, next, err)
	}
	return updated, nil
}
