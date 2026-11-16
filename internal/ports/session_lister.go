package ports

import (
	"context"
	"time"

	"github.com/Imkumar80/agent-orchestrator-/internal/domain"
)

// SessionLister is an additive repository capability used by startup recovery.
// It leaves the core SessionRepository contract intact for existing adapters.
type SessionLister interface {
	List(ctx context.Context) ([]domain.Session, error)
}

// StartupLeaseStore coordinates recovery and runtime ownership for processes
// sharing one persistent store.
type StartupLeaseStore interface {
	AcquireStartupLease(ctx context.Context, owner string, now, expires time.Time) (bool, error)
	RenewStartupLease(ctx context.Context, owner string, now, expires time.Time) (bool, error)
	ReleaseStartupLease(ctx context.Context, owner string) error
}
