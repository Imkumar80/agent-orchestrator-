# 0005. Use bounded in-memory session event broadcasting

- Status: Accepted
- Date: 2026-11-16
- Supersedes: None
- Superseded by: None

## Context

The provider-neutral `EventPublisher` port needs session-scoped streaming for
the local MVP. Publishing must not wait indefinitely for a slow consumer, and
one subscriber must not interfere with another. The current application has no
HTTP/SSE transport or durable event log. Session persistence in SQLite does not
persist event history.

## Decision

Implement the event publisher as a replaceable in-memory broker. Subscribers
are grouped by canonical session ID and each receives an independent bounded
channel. The default channel capacity is 64 events and may be configured when
constructing the broker.

Serialize publishing and subscription lifecycle changes under the broker's
short-held mutex. Publish to each active subscriber without blocking. When a
subscriber's buffer is full, remove it, clear its queued events, close its
stream, and record `ErrSlowConsumer` on the detailed subscription handle.
Publishing continues for other subscribers. Context cancellation removes a
subscription; broker shutdown closes all subscriptions and is idempotent.

Keep the existing `ports.EventPublisher` interface unchanged. Its channel-only
subscription method can observe stream closure but cannot expose the closure
reason; callers needing that detail may use the broker's concrete
`SubscribeDetailed` method. Do not make the Codex adapter depend on the broker.

## Consequences

- Event delivery is ordered by broker acceptance order for each subscriber.
- Bounded queues prevent unbounded memory growth and subscriber backpressure
  does not block the provider or other subscribers.
- A slow subscriber loses its queued events when disconnected, but the
  disconnection is observable through the detailed subscription error.
- Delivery is ephemeral and process-local. Restart loses unconsumed events;
  clients must refresh authoritative state from the application. This decision
  does not establish durable event history or replay semantics.
- The broker can be injected anywhere `ports.EventPublisher` is accepted and
  can later be replaced with a durable or transport-backed implementation.

## Alternatives considered

- **Wait for every subscriber to consume:** rejected because a stalled client
  could block provider output and unrelated sessions.
- **Drop individual events and keep the subscription open:** rejected because
  consumers would see gaps without knowing delivery was incomplete.
- **Add a durable event table or external message broker now:** deferred because
  this phase requires only local streaming and has no replay or distributed
  consumer contract.

## References

- [`SKILL.md`](../../SKILL.md), event delivery and bounded backpressure
  guidance.
- [`internal/events`](../../internal/events/README.md), implementation
  behavior and limitations.
- [`internal/ports/event_publisher.go`](../../internal/ports/event_publisher.go),
  existing publisher contract.
