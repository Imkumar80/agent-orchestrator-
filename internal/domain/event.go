package domain

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type AgentEventType string

// AgentEvent is a normalized provider event. Payload remains opaque to the
// domain while still being representable as JSON.
type AgentEvent struct {
	ID        string          `json:"id"`
	SessionID SessionID       `json:"session_id"`
	Type      AgentEventType  `json:"type"`
	Timestamp time.Time       `json:"timestamp"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

// Validate checks required event metadata and JSON payload validity.
func (e AgentEvent) Validate() error {
	if strings.TrimSpace(e.ID) == "" {
		return fmt.Errorf("%w: ID is required", ErrInvalidAgentEvent)
	}
	if strings.TrimSpace(string(e.SessionID)) == "" {
		return fmt.Errorf("%w: session ID is required", ErrInvalidAgentEvent)
	}
	if strings.TrimSpace(string(e.Type)) == "" {
		return fmt.Errorf("%w: event type is required", ErrInvalidAgentEvent)
	}
	if e.Timestamp.IsZero() {
		return fmt.Errorf("%w: timestamp is required", ErrInvalidAgentEvent)
	}
	if len(e.Payload) > 0 && !json.Valid(e.Payload) {
		return fmt.Errorf("%w: payload must be valid JSON", ErrInvalidAgentEvent)
	}
	return nil
}
