package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Imkumar80/agent-orchestrator-/internal/domain"
	"github.com/Imkumar80/agent-orchestrator-/internal/ports"
)

var _ ports.EventPublisher = (*Broker)(nil)

func TestPublishToOneSubscriber(t *testing.T) {
	broker := newTestBroker(t, Config{BufferCapacity: 2})
	sub, err := broker.SubscribeDetailed(context.Background(), "session-a")
	if err != nil {
		t.Fatal(err)
	}
	want := testEvent("session-a", "one")
	if err := broker.Publish(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	if got := receiveEvent(t, sub.Events()); got.ID != want.ID {
		t.Fatalf("event ID = %q, want %q", got.ID, want.ID)
	}
}

func TestSubscribersReceiveIndependentEventsAndSessionFiltering(t *testing.T) {
	broker := newTestBroker(t, Config{BufferCapacity: 4})
	first, err := broker.SubscribeDetailed(context.Background(), "session-a")
	if err != nil {
		t.Fatal(err)
	}
	second, err := broker.SubscribeDetailed(context.Background(), "session-a")
	if err != nil {
		t.Fatal(err)
	}
	other, err := broker.SubscribeDetailed(context.Background(), "session-b")
	if err != nil {
		t.Fatal(err)
	}
	event := testEvent("session-a", "shared")
	if err := broker.Publish(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	firstEvent := receiveEvent(t, first.Events())
	secondEvent := receiveEvent(t, second.Events())
	if firstEvent.Payload[0] == 'x' {
		t.Fatal("event payload unexpectedly mutated")
	}
	firstEvent.Payload[0] = 'x'
	if string(secondEvent.Payload) != string(event.Payload) {
		t.Fatalf("subscriber payloads share storage: %q versus %q", firstEvent.Payload, secondEvent.Payload)
	}
	select {
	case got := <-other.Events():
		t.Fatalf("different-session subscriber received event: %#v", got)
	default:
	}
	if err := broker.Publish(context.Background(), testEvent("session-b", "other")); err != nil {
		t.Fatal(err)
	}
	if got := receiveEvent(t, other.Events()); got.SessionID != "session-b" {
		t.Fatalf("session ID = %q, want session-b", got.SessionID)
	}
}

func TestPerSubscriberEventOrder(t *testing.T) {
	broker := newTestBroker(t, Config{BufferCapacity: 8})
	first, _ := broker.SubscribeDetailed(context.Background(), "session-a")
	second, _ := broker.SubscribeDetailed(context.Background(), "session-a")
	for i := 0; i < 8; i++ {
		if err := broker.Publish(context.Background(), testEvent("session-a", fmt.Sprintf("%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 8; i++ {
		a, b := receiveEvent(t, first.Events()), receiveEvent(t, second.Events())
		want := fmt.Sprintf("%d", i)
		if a.ID != want || b.ID != want {
			t.Fatalf("event order at %d = %q and %q, want %q", i, a.ID, b.ID, want)
		}
	}
}

func TestConcurrentPublishIsSafeAndOrderedConsistently(t *testing.T) {
	const count = 128
	broker := newTestBroker(t, Config{BufferCapacity: count})
	first, _ := broker.SubscribeDetailed(context.Background(), "session-a")
	second, _ := broker.SubscribeDetailed(context.Background(), "session-a")
	var wg sync.WaitGroup
	errCh := make(chan error, count)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := broker.Publish(context.Background(), testEvent("session-a", fmt.Sprintf("%03d", i))); err != nil {
				errCh <- err
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("Publish(): %v", err)
	}
	for i := 0; i < count; i++ {
		a, b := receiveEvent(t, first.Events()), receiveEvent(t, second.Events())
		if a.ID != b.ID {
			t.Fatalf("subscriber order differs at %d: %q != %q", i, a.ID, b.ID)
		}
	}
}

func TestCancellationAndIndependentSubscriptions(t *testing.T) {
	broker := newTestBroker(t, Config{BufferCapacity: 2})
	ctx, cancel := context.WithCancel(context.Background())
	cancelled, err := broker.SubscribeDetailed(ctx, "session-a")
	if err != nil {
		t.Fatal(err)
	}
	active, err := broker.SubscribeDetailed(context.Background(), "session-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := broker.Publish(context.Background(), testEvent("session-a", "queued-before-cancel")); err != nil {
		t.Fatal(err)
	}
	cancel()
	waitDone(t, cancelled.Done())
	if !errors.Is(cancelled.Err(), context.Canceled) {
		t.Fatalf("cancelled subscription Err() = %v", cancelled.Err())
	}
	if err := broker.Publish(context.Background(), testEvent("session-a", "still-active")); err != nil {
		t.Fatal(err)
	}
	if got := receiveEvent(t, active.Events()); got.ID != "queued-before-cancel" {
		t.Fatalf("active subscription received %q", got.ID)
	}
	if got := receiveEvent(t, active.Events()); got.ID != "still-active" {
		t.Fatalf("active subscription received %q", got.ID)
	}
	select {
	case _, open := <-cancelled.Events():
		if open {
			t.Fatal("cancelled subscription delivered another event")
		}
	default:
		t.Fatal("cancelled subscription channel was not closed")
	}
	active.Cancel()
	waitDone(t, active.Done())
	active.Cancel()
}

func TestSlowSubscriberIsDisconnectedWithoutBlockingOthers(t *testing.T) {
	broker := newTestBroker(t, Config{BufferCapacity: 1})
	slow, _ := broker.SubscribeDetailed(context.Background(), "session-a")
	fast, _ := broker.SubscribeDetailed(context.Background(), "session-a")
	if err := broker.Publish(context.Background(), testEvent("session-a", "first")); err != nil {
		t.Fatal(err)
	}
	if got := receiveEvent(t, fast.Events()); got.ID != "first" {
		t.Fatal(got.ID)
	}
	if err := broker.Publish(context.Background(), testEvent("session-a", "second")); err != nil {
		t.Fatalf("slow subscriber blocked Publish: %v", err)
	}
	if got := receiveEvent(t, fast.Events()); got.ID != "second" {
		t.Fatalf("fast subscriber got %q", got.ID)
	}
	waitDone(t, slow.Done())
	if !errors.Is(slow.Err(), ErrSlowConsumer) {
		t.Fatalf("slow subscription Err() = %v, want ErrSlowConsumer", slow.Err())
	}
	select {
	case _, open := <-slow.Events():
		if open {
			t.Fatal("slow subscription retained queued events after disconnection")
		}
	default:
		t.Fatal("slow subscription channel was not closed")
	}
}

func TestShutdownAndPostShutdownErrors(t *testing.T) {
	broker := newTestBroker(t, Config{BufferCapacity: 1})
	sub, err := broker.SubscribeDetailed(context.Background(), "session-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := broker.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if err := broker.Shutdown(); err != nil {
		t.Fatalf("second Shutdown(): %v", err)
	}
	waitDone(t, sub.Done())
	if !errors.Is(sub.Err(), ErrBrokerClosed) {
		t.Fatalf("subscription Err() = %v", sub.Err())
	}
	if err := broker.Publish(context.Background(), testEvent("session-a", "after-close")); !errors.Is(err, ErrBrokerClosed) {
		t.Fatalf("Publish() after shutdown = %v", err)
	}
	if _, err := broker.SubscribeDetailed(context.Background(), "session-a"); !errors.Is(err, ErrBrokerClosed) {
		t.Fatalf("Subscribe() after shutdown = %v", err)
	}
}

func TestRepeatedSubscribeCancelReleasesSubscriptions(t *testing.T) {
	broker := newTestBroker(t, Config{BufferCapacity: 1})
	for i := 0; i < 500; i++ {
		sub, err := broker.SubscribeDetailed(context.Background(), "session-a")
		if err != nil {
			t.Fatal(err)
		}
		sub.Cancel()
		waitDone(t, sub.Done())
	}
	broker.mu.Lock()
	defer broker.mu.Unlock()
	if len(broker.sessions) != 0 {
		t.Fatalf("broker retains %d session subscription sets", len(broker.sessions))
	}
}

func TestValidationAndConfiguration(t *testing.T) {
	if _, err := NewBroker(Config{BufferCapacity: -1}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("negative capacity error = %v", err)
	}
	broker := newTestBroker(t, Config{})
	if _, err := broker.SubscribeDetailed(context.Background(), ""); err == nil {
		t.Fatal("SubscribeDetailed() accepted empty session ID")
	}
	if err := broker.Publish(context.Background(), domain.AgentEvent{}); !errors.Is(err, domain.ErrInvalidAgentEvent) {
		t.Fatalf("invalid event error = %v", err)
	}
}

func newTestBroker(t *testing.T, config Config) *Broker {
	t.Helper()
	broker, err := NewBroker(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := broker.Shutdown(); err != nil {
			t.Errorf("Shutdown(): %v", err)
		}
	})
	return broker
}

func testEvent(sessionID, id string) domain.AgentEvent {
	return domain.AgentEvent{
		ID: id, SessionID: domain.SessionID(sessionID), Type: "test.event",
		Timestamp: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		Payload:   json.RawMessage(`{"value":"payload"}`),
	}
}

func receiveEvent(t *testing.T, events <-chan domain.AgentEvent) domain.AgentEvent {
	t.Helper()
	select {
	case event, open := <-events:
		if !open {
			t.Fatal("event channel closed before event arrived")
		}
		return event
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for event")
		return domain.AgentEvent{}
	}
}

func waitDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for subscription to close")
	}
}
