// Package orchestrator connects task intake and planning to session execution.
package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Imkumar80/agent-orchestrator-/internal/domain"
	"github.com/Imkumar80/agent-orchestrator-/internal/ports"
)

var (
	ErrInvalidDependency = errors.New("orchestrator dependency is nil")
	ErrInvalidPlan       = errors.New("planner returned an invalid plan")
	ErrProviderMismatch  = errors.New("planned provider does not match requested provider")
	ErrNilContext        = errors.New("context must not be nil")
)

// SessionStarter is the orchestration boundary to the existing session manager.
type SessionStarter interface {
	CreateAndStart(ctx context.Context, task domain.Task) (domain.Session, error)
}

type ContextualSessionStarter interface {
	CreateAndStartWithContext(ctx context.Context, task domain.Task, memoryContext string) (domain.Session, error)
}

type TaskMemoryContext interface {
	BuildTaskContext(ctx context.Context, task domain.Task) (string, error)
}

// Coordinator validates a request and proposal, then delegates execution.
// A nil planner disables planning and sends the requested task directly to the
// session manager.
type Coordinator struct {
	planner  ports.Planner
	sessions SessionStarter
	memory   TaskMemoryContext
}

// NewCoordinatorWithMemory is an additive constructor; memory retrieval remains
// optional and is owned by the injected application-level manager.
func NewCoordinatorWithMemory(planner ports.Planner, sessions SessionStarter, memory TaskMemoryContext) (*Coordinator, error) {
	c, err := NewCoordinator(planner, sessions)
	if err != nil {
		return nil, err
	}
	c.memory = memory
	return c, nil
}

// NewCoordinator wires dependencies without constructing providers,
// repositories, or publishers. The session manager owns provider resolution.
func NewCoordinator(planner ports.Planner, sessions SessionStarter) (*Coordinator, error) {
	if sessions == nil {
		return nil, ErrInvalidDependency
	}
	return &Coordinator{planner: planner, sessions: sessions}, nil
}

// Execute accepts one user task, optionally validates a planner proposal, and
// delegates creation and startup to the session manager.
func (c *Coordinator) Execute(ctx context.Context, task domain.Task) (domain.Session, error) {
	if c == nil || c.sessions == nil {
		return domain.Session{}, ErrInvalidDependency
	}
	if ctx == nil {
		return domain.Session{}, ErrNilContext
	}
	if err := ctx.Err(); err != nil {
		return domain.Session{}, err
	}
	if err := task.Validate(); err != nil {
		return domain.Session{}, fmt.Errorf("validate task %q: %w", task.ID, err)
	}

	if c.planner != nil {
		proposal, err := c.planner.Plan(ctx, ports.PlanRequest{
			Goal:               task.Description,
			WorkingDirectory:   task.WorkingDirectory,
			AvailableProviders: []domain.AgentProvider{task.Provider},
		})
		if err != nil {
			return domain.Session{}, fmt.Errorf("plan task %q: %w", task.ID, err)
		}
		if err := validatePlan(task, proposal); err != nil {
			return domain.Session{}, err
		}
		if err := ctx.Err(); err != nil {
			return domain.Session{}, err
		}
	}

	var created domain.Session
	var err error
	if c.memory != nil {
		memoryContext, contextErr := c.memory.BuildTaskContext(ctx, task)
		if contextErr != nil {
			return domain.Session{}, fmt.Errorf("retrieve task memory context: %w", contextErr)
		}
		if memoryContext != "" {
			starter, ok := c.sessions.(ContextualSessionStarter)
			if !ok {
				return domain.Session{}, errors.New("session starter cannot forward memory context")
			}
			created, err = starter.CreateAndStartWithContext(ctx, task, memoryContext)
		} else {
			created, err = c.sessions.CreateAndStart(ctx, task)
		}
	} else {
		created, err = c.sessions.CreateAndStart(ctx, task)
	}
	if err != nil {
		return created, fmt.Errorf("create and start session for task %q: %w", task.ID, err)
	}
	return created, nil
}

func validatePlan(task domain.Task, plan ports.PlanResponse) error {
	if len(plan.Tasks) != 1 {
		return fmt.Errorf("%w: expected exactly one executable task, got %d", ErrInvalidPlan, len(plan.Tasks))
	}
	planned := plan.Tasks[0]
	if strings.TrimSpace(planned.ID) == "" {
		return fmt.Errorf("%w: task ID is required", ErrInvalidPlan)
	}
	if strings.TrimSpace(planned.Description) == "" {
		return fmt.Errorf("%w: task description is required", ErrInvalidPlan)
	}
	if planned.Description != task.Description {
		return fmt.Errorf("%w: proposed task description differs from the requested task", ErrInvalidPlan)
	}
	if err := planned.Provider.Validate(); err != nil {
		return errors.Join(fmt.Errorf("%w: invalid task provider", ErrInvalidPlan), err)
	}
	if planned.Provider != task.Provider {
		return fmt.Errorf("%w: requested %q, proposed %q", ErrProviderMismatch, task.Provider, planned.Provider)
	}
	if len(planned.DependsOn) != 0 {
		return fmt.Errorf("%w: a single execution task cannot have dependencies", ErrInvalidPlan)
	}
	return nil
}
