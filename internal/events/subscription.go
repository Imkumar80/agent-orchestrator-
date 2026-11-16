package events

import (
	"context"

	"github.com/Imkumar80/agent-orchestrator-/internal/domain"
)

// Subscription is one subscriber's bounded event stream. Call Cancel or
// cancel the context passed to SubscribeDetailed to release its resources.
type Subscription struct {
	broker    *Broker
	id        uint64
	sessionID domain.SessionID
	ctx       context.Context
	events    chan domain.AgentEvent
	done      chan struct{}
	err       error // guarded by broker.mu
}

// Events returns this subscription's receive-only event stream.
func (s *Subscription) Events() <-chan domain.AgentEvent {
	if s == nil {
		return nil
	}
	return s.events
}

// Done is closed when this subscription is cancelled, disconnected, or its
// broker shuts down. It is useful for synchronizing lifecycle tests/callers.
func (s *Subscription) Done() <-chan struct{} {
	if s == nil {
		return nil
	}
	return s.done
}

// Err reports the reason the subscription ended. It returns nil while active.
func (s *Subscription) Err() error {
	if s == nil || s.broker == nil {
		return ErrSubscriptionClosed
	}
	s.broker.mu.Lock()
	defer s.broker.mu.Unlock()
	return s.err
}

// Cancel removes and closes this subscription. Repeated cancellation is safe.
func (s *Subscription) Cancel() {
	if s == nil || s.broker == nil {
		return
	}
	s.broker.unsubscribe(s, ErrSubscriptionClosed)
}
