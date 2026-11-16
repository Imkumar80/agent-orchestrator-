// Package recovery reconciles durable sessions with provider runtime state.
package recovery

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Imkumar80/agent-orchestrator-/internal/domain"
	"github.com/Imkumar80/agent-orchestrator-/internal/ports"
	"github.com/Imkumar80/agent-orchestrator-/internal/providers"
	"github.com/Imkumar80/agent-orchestrator-/internal/session"
)

const (
	EventRecovered      domain.AgentEventType = "session.recovered"
	EventRecoveryFailed domain.AgentEventType = "session.recovery_failed"
	errorUnavailable                          = "recovery_unavailable"
	errorFailed                               = "recovery_failed"
)

// Issue contains a safe, actionable per-session recovery failure.
type Issue struct {
	SessionID domain.SessionID     `json:"session_id"`
	Provider  domain.AgentProvider `json:"provider"`
	Category  string               `json:"category"`
	Error     string               `json:"error"`
}

// Report summarizes one recovery pass. Event failures are included in Errors
// and Issues but do not roll back an already persisted lifecycle outcome.
type Report struct {
	Inspected           int                `json:"inspected"`
	TerminalSkipped     int                `json:"terminal_skipped"`
	Reconciled          int                `json:"reconciled"`
	Unrecoverable       int                `json:"unrecoverable"`
	Errors              int                `json:"errors"`
	AlreadyHandled      int                `json:"already_handled"`
	Duration            time.Duration      `json:"duration"`
	Issues              []Issue            `json:"issues,omitempty"`
	RecoveredSessionIDs []domain.SessionID `json:"-"`
}

// Service performs one serialized recovery pass over persisted sessions.
type Service struct {
	repo      ports.SessionRepository
	lister    ports.SessionLister
	registry  *providers.Registry
	lifecycle *session.LifecycleManager
	events    ports.EventPublisher
	now       func() time.Time
	mu        sync.Mutex
	processed map[domain.SessionID]struct{}
}

func New(repo ports.SessionRepository, lister ports.SessionLister, registry *providers.Registry, lifecycle *session.LifecycleManager, publisher ports.EventPublisher) (*Service, error) {
	if repo == nil || lister == nil || registry == nil || lifecycle == nil {
		return nil, errors.New("recovery requires repository, session lister, registry, and lifecycle manager")
	}
	return &Service{repo: repo, lister: lister, registry: registry, lifecycle: lifecycle, events: publisher, now: func() time.Time { return time.Now().UTC() }, processed: make(map[domain.SessionID]struct{})}, nil
}

// Run reconciles only nonterminal sessions. Unsupported or unverifiable runs
// are interrupted; work is never restarted automatically.
func (s *Service) Run(ctx context.Context) (Report, error) {
	if s == nil {
		return Report{}, errors.New("recovery service is nil")
	}
	if ctx == nil {
		return Report{}, errors.New("recovery context is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	started := s.now()
	report := Report{}
	items, listErr := s.lister.List(ctx)
	if listErr != nil {
		// Repositories may return valid rows together with a partial-list error.
		// Continue reconciling those rows while making the incomplete inventory
		// visible to startup diagnostics.
		report.Errors++
		report.Issues = append(report.Issues, Issue{Category: "session_list_incomplete", Error: listErr.Error()})
		if len(items) == 0 {
			report.Duration = s.now().Sub(started)
			return report, fmt.Errorf("list sessions for recovery: %w", listErr)
		}
	}
	report.Inspected = len(items)
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			report.Errors++
			report.Issues = append(report.Issues, Issue{SessionID: item.ID, Provider: item.Provider, Category: "context_cancelled", Error: err.Error()})
			break
		}
		if err := item.Validate(); err != nil {
			s.issue(&report, item, "invalid_session", err)
			continue
		}
		if item.Status.Terminal() {
			report.TerminalSkipped++
			continue
		}
		if _, handled := s.processed[item.ID]; handled {
			report.AlreadyHandled++
			continue
		}
		if err := s.reconcile(ctx, item, &report); err != nil {
			s.issue(&report, item, "persistence_or_provider_error", err)
		} else {
			s.processed[item.ID] = struct{}{}
		}
	}
	report.Duration = s.now().Sub(started)
	return report, nil
}

func (s *Service) reconcile(ctx context.Context, item domain.Session, report *Report) error {
	if item.Status == domain.SessionRunning || item.Status == domain.SessionWaitingForInput {
		provider, err := s.registry.Get(item.Provider)
		if err == nil && provider.Capabilities().Resume && item.NativeProviderSessionID != "" {
			ref, resumeErr := provider.Resume(ctx, ports.SessionRef{SessionID: item.ID, NativeProviderSessionID: item.NativeProviderSessionID})
			if resumeErr == nil && ref.SessionID == item.ID && ref.NativeProviderSessionID == item.NativeProviderSessionID {
				if item.Status == domain.SessionWaitingForInput {
					if _, err := s.lifecycle.Transition(ctx, item.ID, domain.SessionRunning, nil); err != nil {
						return fmt.Errorf("persist confirmed resumed state: %w", err)
					}
				}
				report.Reconciled++
				report.RecoveredSessionIDs = append(report.RecoveredSessionIDs, item.ID)
				s.publish(ctx, item, EventRecovered, report)
				return nil
			}
			if resumeErr != nil {
				err = fmt.Errorf("provider resume confirmation failed: %w", resumeErr)
			} else {
				err = errors.New("provider returned a different session identity")
			}
			return s.interrupt(ctx, item, report, errorFailed, "provider recovery could not confirm the original execution", err)
		}
		if err != nil {
			return s.interrupt(ctx, item, report, errorUnavailable, "provider is unavailable during recovery", err)
		}
		if !provider.Capabilities().Resume {
			return s.interrupt(ctx, item, report, errorUnavailable, "provider does not support session recovery", errors.New("resume capability is not supported"))
		}
		return s.interrupt(ctx, item, report, errorUnavailable, "provider session identity is missing", errors.New("native provider session ID is empty"))
	}
	return s.interrupt(ctx, item, report, errorUnavailable, "session was not confirmed active after restart", errors.New("pending or starting executions are not automatically restarted"))
}

func (s *Service) interrupt(ctx context.Context, item domain.Session, report *Report, code, message string, cause error) error {
	info := &domain.ErrorInfo{Code: code, Message: message}
	if _, err := s.lifecycle.Transition(ctx, item.ID, domain.SessionInterrupted, info); err != nil {
		return errors.Join(fmt.Errorf("mark stale session interrupted: %w", err), cause)
	}
	report.Unrecoverable++
	s.publish(ctx, item, EventRecoveryFailed, report)
	return nil
}

func (s *Service) publish(ctx context.Context, item domain.Session, typ domain.AgentEventType, report *Report) {
	if s.events == nil {
		return
	}
	payload, _ := json.Marshal(map[string]string{"outcome": string(typ)})
	event := domain.AgentEvent{ID: fmt.Sprintf("recovery-%s-%d", item.ID, s.now().UnixNano()), SessionID: item.ID, Type: typ, Timestamp: s.now(), Payload: payload}
	if err := s.events.Publish(ctx, event); err != nil {
		s.issue(report, item, "event_publish_failed", err)
	}
}

func (s *Service) issue(report *Report, item domain.Session, category string, err error) {
	report.Errors++
	report.Issues = append(report.Issues, Issue{SessionID: item.ID, Provider: item.Provider, Category: category, Error: err.Error()})
}

// NewOwnerID returns a process-unique lease owner token.
func NewOwnerID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", fmt.Errorf("generate startup lease owner: %w", err)
	}
	return hex.EncodeToString(bytes[:]), nil
}
