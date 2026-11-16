// Package events provides in-memory, session-scoped event delivery.
package events

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/Imkumar80/agent-orchestrator-/internal/domain"
	"github.com/Imkumar80/agent-orchestrator-/internal/ports"
)

var (
	ErrBrokerClosed       = errors.New("event broker is closed")
	ErrSubscriptionClosed = errors.New("event subscription is closed")
	ErrSlowConsumer       = errors.New("event subscription closed because its buffer was exhausted")
	ErrInvalidConfig      = errors.New("invalid event broker configuration")
)

const defaultBufferCapacity = 64

// Config controls the bounded per-subscriber event buffer.
type Config struct {
	// BufferCapacity is the number of events each subscriber can queue. Zero
	// selects 64; negative values are rejected.
	BufferCapacity int
}

// Broker implements the provider-neutral event publisher port. It uses one
// short-held mutex to serialize session-scoped delivery and subscription
// lifecycle operations; it never waits for a subscriber to read.
type Broker struct {
	mu       sync.Mutex
	closed   bool
	nextID   uint64
	capacity int
	sessions map[domain.SessionID]map[uint64]*Subscription
}

var _ ports.EventPublisher = (*Broker)(nil)
var _ ports.DetailedEventPublisher = (*Broker)(nil)

// NewBroker returns an in-memory event broker with bounded subscriber queues.
func NewBroker(config Config) (*Broker, error) {
	if config.BufferCapacity < 0 {
		return nil, fmt.Errorf("%w: buffer capacity must not be negative", ErrInvalidConfig)
	}
	if config.BufferCapacity == 0 {
		config.BufferCapacity = defaultBufferCapacity
	}
	return &Broker{
		capacity: config.BufferCapacity,
		sessions: make(map[domain.SessionID]map[uint64]*Subscription),
	}, nil
}

// Publish sends an event to each current subscriber for its session. A slow
// subscriber is disconnected when its bounded queue is full; other subscribers
// continue independently. With no subscribers, publishing succeeds.
func (b *Broker) Publish(ctx context.Context, event domain.AgentEvent) error {
	if b == nil {
		return ErrBrokerClosed
	}
	if ctx == nil {
		return errors.New("event publish context must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := event.Validate(); err != nil {
		return fmt.Errorf("validate event before publish: %w", err)
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return ErrBrokerClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, sub := range b.sessions[event.SessionID] {
		if err := sub.ctx.Err(); err != nil {
			b.removeLocked(sub, err)
			continue
		}
		select {
		case sub.events <- cloneEvent(event):
		default:
			b.removeLocked(sub, ErrSlowConsumer)
		}
	}
	return nil
}

// Subscribe implements ports.EventPublisher. Use SubscribeDetailed when the
// caller needs to inspect why a stream ended (for example, ErrSlowConsumer).
func (b *Broker) Subscribe(ctx context.Context, sessionID domain.SessionID) (<-chan domain.AgentEvent, error) {
	sub, err := b.SubscribeDetailed(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return sub.Events(), nil
}

// SubscribeStream exposes a port-level subscription handle with termination
// diagnostics while preserving the existing EventPublisher contract.
func (b *Broker) SubscribeStream(ctx context.Context, sessionID domain.SessionID) (ports.EventSubscription, error) {
	return b.SubscribeDetailed(ctx, sessionID)
}

// SubscribeDetailed registers an independently buffered session subscription.
// Its context cancellation closes Events; Err reports cancellation, shutdown,
// manual cancellation, or slow-consumer disconnection.
func (b *Broker) SubscribeDetailed(ctx context.Context, sessionID domain.SessionID) (*Subscription, error) {
	if b == nil {
		return nil, ErrBrokerClosed
	}
	if ctx == nil {
		return nil, errors.New("event subscription context must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if sessionID == "" {
		return nil, fmt.Errorf("%w: session ID is required", domain.ErrInvalidAgentEvent)
	}

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil, ErrBrokerClosed
	}
	b.nextID++
	sub := &Subscription{
		broker: b, id: b.nextID, sessionID: sessionID,
		ctx: ctx, events: make(chan domain.AgentEvent, b.capacity), done: make(chan struct{}),
	}
	if b.sessions[sessionID] == nil {
		b.sessions[sessionID] = make(map[uint64]*Subscription)
	}
	b.sessions[sessionID][sub.id] = sub
	b.mu.Unlock()

	go func() {
		select {
		case <-ctx.Done():
			b.unsubscribe(sub, ctx.Err())
		case <-sub.done:
		}
	}()
	return sub, nil
}

// Shutdown closes the broker and all active subscriptions. It is idempotent.
func (b *Broker) Shutdown() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	for _, subscribers := range b.sessions {
		for _, sub := range subscribers {
			b.removeLocked(sub, ErrBrokerClosed)
		}
	}
	return nil
}

func (b *Broker) unsubscribe(sub *Subscription, reason error) {
	b.mu.Lock()
	b.removeLocked(sub, reason)
	b.mu.Unlock()
}

// removeLocked removes and closes exactly once. All sends and closes are
// serialized by Broker.mu, so callers cannot race a send against close.
func (b *Broker) removeLocked(sub *Subscription, reason error) {
	subscribers := b.sessions[sub.sessionID]
	if subscribers == nil || subscribers[sub.id] != sub {
		return
	}
	delete(subscribers, sub.id)
	if len(subscribers) == 0 {
		delete(b.sessions, sub.sessionID)
	}
	sub.err = reason
	for {
		select {
		case <-sub.events:
		default:
			close(sub.events)
			close(sub.done)
			return
		}
	}
}

func cloneEvent(event domain.AgentEvent) domain.AgentEvent {
	event.Payload = append(event.Payload[:0:0], event.Payload...)
	return event
}
