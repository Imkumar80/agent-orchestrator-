package domain

import (
	"errors"
	"testing"
	"time"
)

func TestSessionValidate(t *testing.T) {
	now := time.Now()
	session := Session{
		ID:        "session-1",
		TaskID:    "task-1",
		Provider:  ProviderCodex,
		Status:    SessionPending,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := session.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	invalid := session
	invalid.UpdatedAt = now.Add(-time.Second)
	if err := invalid.Validate(); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("Validate() error = %v, want ErrInvalidSession", err)
	}

	invalid = session
	invalid.Status = "unknown"
	if err := invalid.Validate(); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("Validate() error = %v, want ErrInvalidSession", err)
	}

	invalid = session
	invalid.Error = &ErrorInfo{Code: " ", Message: "launch failed"}
	if err := invalid.Validate(); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("Validate() error = %v, want ErrInvalidSession for invalid error info", err)
	}
}

func TestSessionTransitionAllowedLifecycle(t *testing.T) {
	now := time.Now()
	session := Session{
		ID:        "session-1",
		TaskID:    "task-1",
		Provider:  ProviderCodex,
		Status:    SessionPending,
		CreatedAt: now,
		UpdatedAt: now,
	}

	transitions := []SessionStatus{
		SessionStarting,
		SessionRunning,
		SessionWaitingForInput,
		SessionRunning,
		SessionCompleted,
	}
	for i, next := range transitions {
		at := now.Add(time.Duration(i+1) * time.Second)
		if err := session.Transition(next, at); err != nil {
			t.Fatalf("Transition(%q) error = %v", next, err)
		}
		if session.Status != next || !session.UpdatedAt.Equal(at) {
			t.Fatalf("Transition(%q) left status=%q updated_at=%s", next, session.Status, session.UpdatedAt)
		}
	}
}

func TestSessionTransitionRejectsInvalidTransitions(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name   string
		status SessionStatus
		next   SessionStatus
		at     time.Time
	}{
		{name: "skip starting", status: SessionPending, next: SessionRunning, at: now.Add(time.Second)},
		{name: "unknown target", status: SessionRunning, next: "unknown", at: now.Add(time.Second)},
		{name: "terminal state cannot transition", status: SessionCompleted, next: SessionRunning, at: now.Add(time.Second)},
		{name: "timestamp moves backwards", status: SessionRunning, next: SessionCompleted, at: now.Add(-time.Second)},
		{name: "zero timestamp", status: SessionRunning, next: SessionCompleted, at: time.Time{}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			session := Session{
				ID:        "session-1",
				TaskID:    "task-1",
				Provider:  ProviderCodex,
				Status:    test.status,
				CreatedAt: now,
				UpdatedAt: now,
			}
			if err := session.Transition(test.next, test.at); !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("Transition(%q) error = %v, want ErrInvalidTransition", test.next, err)
			}
			if session.Status != test.status {
				t.Fatalf("failed transition changed status to %q", session.Status)
			}
		})
	}
}

func TestSessionTransitionRejectsNilSession(t *testing.T) {
	var session *Session
	if err := session.Transition(SessionStarting, time.Now()); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("Transition() error = %v, want ErrInvalidSession", err)
	}
}
