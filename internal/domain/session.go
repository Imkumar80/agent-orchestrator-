package domain

import (
	"fmt"
	"strings"
	"time"
)

type SessionID string

type SessionStatus string

const (
	SessionPending         SessionStatus = "pending"
	SessionStarting        SessionStatus = "starting"
	SessionRunning         SessionStatus = "running"
	SessionWaitingForInput SessionStatus = "waiting_for_input"
	SessionCompleted       SessionStatus = "completed"
	SessionFailed          SessionStatus = "failed"
	SessionInterrupted     SessionStatus = "interrupted"
)

// Valid reports whether s is a defined session lifecycle state.
func (s SessionStatus) Valid() bool {
	switch s {
	case SessionPending, SessionStarting, SessionRunning, SessionWaitingForInput,
		SessionCompleted, SessionFailed, SessionInterrupted:
		return true
	default:
		return false
	}
}

// Terminal reports whether s is a completed lifecycle state.
func (s SessionStatus) Terminal() bool {
	switch s {
	case SessionCompleted, SessionFailed, SessionInterrupted:
		return true
	default:
		return false
	}
}

// This is the central representation of an execution session
// Session is the durable, provider-independent identity for one task run.
type Session struct {
	ID                      SessionID     `json:"id"` //orchestrator id of session
	TaskID                  TaskID        `json:"task_id"`
	Provider                AgentProvider `json:"provider"`
	NativeProviderSessionID string        `json:"native_provider_session_id,omitempty"` //id aassigned by the actuall agent provider
	Status                  SessionStatus `json:"status"`
	CreatedAt               time.Time     `json:"created_at"`
	UpdatedAt               time.Time     `json:"updated_at"`
	Error                   *ErrorInfo    `json:"error,omitempty"`
}

// Validate checks the session's durable invariants.
func (s Session) Validate() error {
	if strings.TrimSpace(string(s.ID)) == "" {
		return fmt.Errorf("%w: ID is required", ErrInvalidSession)
	}
	if strings.TrimSpace(string(s.TaskID)) == "" {
		return fmt.Errorf("%w: task ID is required", ErrInvalidSession)
	}
	if err := s.Provider.Validate(); err != nil {
		return err
	}
	if !s.Status.Valid() {
		return fmt.Errorf("%w: unknown status %q", ErrInvalidSession, s.Status)
	}
	if s.CreatedAt.IsZero() || s.UpdatedAt.IsZero() {
		return fmt.Errorf("%w: creation and update timestamps are required", ErrInvalidSession)
	}
	if s.UpdatedAt.Before(s.CreatedAt) {
		return fmt.Errorf("%w: update timestamp precedes creation timestamp", ErrInvalidSession)
	}
	if s.Error != nil {
		if err := s.Error.validate(); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidSession, err)
		}
	}
	return nil
}

// Transition moves the session to next if the lifecycle permits it.
func (s *Session) Transition(next SessionStatus, at time.Time) error {
	if s == nil {
		return fmt.Errorf("%w: session is nil", ErrInvalidSession)
	}
	if err := s.Validate(); err != nil {
		return err
	}
	if at.IsZero() || at.Before(s.UpdatedAt) || !canTransition(s.Status, next) {
		return fmt.Errorf("%w: %q to %q at %s", ErrInvalidTransition, s.Status, next, at)
	}
	s.Status = next
	s.UpdatedAt = at
	return nil
}

func canTransition(from, to SessionStatus) bool {
	switch from {
	case SessionPending:
		return to == SessionStarting || to == SessionFailed || to == SessionInterrupted
	case SessionStarting:
		return to == SessionRunning || to == SessionWaitingForInput ||
			to == SessionFailed || to == SessionInterrupted
	case SessionRunning:
		return to == SessionWaitingForInput || to == SessionCompleted ||
			to == SessionFailed || to == SessionInterrupted
	case SessionWaitingForInput:
		return to == SessionRunning || to == SessionCompleted ||
			to == SessionFailed || to == SessionInterrupted
	default:
		return false
	}
}
