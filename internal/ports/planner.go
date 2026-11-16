package ports

import (
	"context"

	"github.com/Imkumar80/agent-orchestrator-/internal/domain"
)

// Planner proposes a provider-neutral task breakdown. The caller remains
// responsible for validating and authorizing the proposal before execution.
type Planner interface {
	Plan(ctx context.Context, request PlanRequest) (PlanResponse, error)
}

// PlanRequest contains bounded information needed to decompose a goal.
type PlanRequest struct {
	Goal               string                 `json:"goal"`
	WorkingDirectory   string                 `json:"working_directory"`
	AvailableProviders []domain.AgentProvider `json:"available_providers,omitempty"`
}

// PlanResponse is a proposed set of tasks; it does not authorize execution.
type PlanResponse struct {
	Tasks []PlannedTask `json:"tasks"`
}

// PlannedTask describes one proposed unit of work and its dependencies.
type PlannedTask struct {
	ID          string               `json:"id"`
	Description string               `json:"description"`
	Provider    domain.AgentProvider `json:"provider"`
	DependsOn   []string             `json:"depends_on,omitempty"`
}
