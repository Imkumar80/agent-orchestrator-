package ports

import (
	"context"

	"github.com/Imkumar80/agent-orchestrator-/internal/domain"
)

// SessionRepository persists canonical session records. Get and Update should
// return domain.ErrSessionNotFound when the requested session does not exist.
type SessionRepository interface {
	Create(ctx context.Context, session domain.Session) error
	Get(ctx context.Context, id domain.SessionID) (domain.Session, error)
	Update(ctx context.Context, session domain.Session) error
}
