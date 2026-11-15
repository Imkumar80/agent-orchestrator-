package domain

import (
	"errors"
	"testing"
	"time"
)

func TestAgentEventValidate(t *testing.T) {
	event := AgentEvent{
		ID:        "event-1",
		SessionID: "session-1",
		Type:      "assistant.message.completed",
		Timestamp: time.Now(),
		Payload:   []byte(`{"text":"done"}`),
	}
	if err := event.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	invalid := event
	invalid.Payload = []byte(`{"text":`)
	if err := invalid.Validate(); !errors.Is(err, ErrInvalidAgentEvent) {
		t.Fatalf("Validate() error = %v, want ErrInvalidAgentEvent", err)
	}

	invalid = event
	invalid.Timestamp = time.Time{}
	if err := invalid.Validate(); !errors.Is(err, ErrInvalidAgentEvent) {
		t.Fatalf("Validate() error = %v, want ErrInvalidAgentEvent", err)
	}
}
