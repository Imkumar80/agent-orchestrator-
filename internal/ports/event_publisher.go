package ports

import (
	"context"

	"github.com/Imkumar80/agent-orchestrator-/internal/domain"
)

// EventPublisher publishes normalized execution events and subscribes to
// events for one canonical orchestrator session. Implementations should close
// the returned channel when ctx is canceled or the subscription ends.
type EventPublisher interface {
	Publish(ctx context.Context, event domain.AgentEvent) error
	Subscribe(ctx context.Context, sessionID domain.SessionID) (<-chan domain.AgentEvent, error)
}
