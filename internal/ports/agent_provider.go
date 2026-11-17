// Package ports defines provider-neutral interfaces consumed by orchestration
// code and implemented by provider adapters.
package ports

import (
	"context"

	"github.com/Imkumar80/agent-orchestrator-/internal/domain"
)

// ProviderStartupError carries safe, provider-neutral diagnostics for a
// provider that could not complete startup. Cause remains available to
// errors.Is/errors.As callers, while Error preserves the original text.
type ProviderStartupError struct {
	Stage          string
	Category       string
	ProcessCreated bool
	ThreadStarted  bool
	ExitCode       *int
	OSErrorSummary string
	Cause          error
}

func (e *ProviderStartupError) Error() string {
	if e == nil {
		return "provider startup failed"
	}
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return "provider startup failed"
}

func (e *ProviderStartupError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// AgentProvider controls one provider-native execution through a stable
// orchestrator session identity. Provider implementations own their native
// protocol and must not change the canonical SessionID.
type AgentProvider interface {
	Name() domain.AgentProvider
	Capabilities() domain.AgentCapabilities
	Start(ctx context.Context, request StartRequest) (SessionRef, error)
	Send(ctx context.Context, session SessionRef, input AgentInput) error
	Subscribe(ctx context.Context, session SessionRef) (<-chan domain.AgentEvent, error)
	Interrupt(ctx context.Context, session SessionRef) error
	Resume(ctx context.Context, session SessionRef) (SessionRef, error)
	Close(ctx context.Context, session SessionRef) error
}

// AgentExecutionWaiter is an optional capability for providers that can report
// when a started execution has exited. The session manager uses it to persist
// the final lifecycle state without interpreting provider event payloads.
type AgentExecutionWaiter interface {
	Wait(ctx context.Context, session SessionRef) error
}

// StartRequest contains the canonical session identity and task for a new
// provider execution.
type StartRequest struct {
	SessionID     domain.SessionID `json:"session_id"`
	Task          domain.Task      `json:"task"`
	MemoryContext string           `json:"memory_context,omitempty"`
}

// SessionRef maps the canonical orchestrator session to the provider-native
// session handle. NativeProviderSessionID may be empty before provider startup.
type SessionRef struct {
	SessionID               domain.SessionID `json:"session_id"`
	NativeProviderSessionID string           `json:"native_provider_session_id,omitempty"`
}

// AgentInput is provider-neutral user input sent to an active execution.
type AgentInput struct {
	Text string `json:"text"`
}
