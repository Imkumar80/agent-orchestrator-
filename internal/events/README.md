# In-memory event broker

The broker implements `ports.EventPublisher`. Providers publish normalized
`domain.AgentEvent` values; subscriptions are registered against the
canonical session ID and receive only that session's events. A composition
root can construct one with `events.NewBroker(events.Config{BufferCapacity: 64})`
and inject it wherever `ports.EventPublisher` is accepted.

Each subscriber has its own bounded queue. `BufferCapacity` is a per-subscriber
event count and defaults to 64. Publishing never waits for subscribers. When a
queue is full, the broker closes and removes that subscriber, clears its queued
events, and reports the reason through the detailed subscription handle; other subscribers continue receiving. Callers using the
concrete broker can use `SubscribeDetailed` and inspect `Subscription.Err()` for
`events.ErrSlowConsumer`. The port's channel-only `Subscribe` reports closure,
but the existing interface has no way to return a stream termination reason.

Cancel the supplied context or call `Subscription.Cancel()` to stop delivery
and release the subscription. `Broker.Shutdown()` is idempotent, closes active
subscriptions, and causes later publishes/subscriptions to return
`events.ErrBrokerClosed`. Sends and channel closure are serialized under a
short-held broker mutex; events are copied for each subscriber and queued in
broker acceptance order.

Events are in memory only. A process restart loses events that have not been
consumed; SQLite persists session records only and does not store event history.
This broker is for local streaming and is not a durable event log or a replay
mechanism.
