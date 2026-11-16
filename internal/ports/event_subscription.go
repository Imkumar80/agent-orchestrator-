package ports

import (
	"context"

	"github.com/Imkumar80/agent-orchestrator-/internal/domain"
)

// EventSubscription is the optional diagnostic form of a publisher stream.
// EventPublisher remains the minimal compatibility contract; infrastructure
// that can report stream termination reasons may expose this extension.
type EventSubscription interface {
	Events() <-chan domain.AgentEvent
	Err() error
	Cancel()
}

// DetailedEventPublisher optionally exposes subscription termination reasons.
type DetailedEventPublisher interface {
	SubscribeStream(ctx context.Context, sessionID domain.SessionID) (EventSubscription, error)
}
