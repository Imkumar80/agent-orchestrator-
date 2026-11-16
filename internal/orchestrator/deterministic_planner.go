package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Imkumar80/agent-orchestrator-/internal/ports"
)

// DeterministicPlanner returns one execution step using the first provider
// explicitly supplied in AvailableProviders. It makes no external calls.
type DeterministicPlanner struct{}

var _ ports.Planner = (*DeterministicPlanner)(nil)

func NewDeterministicPlanner() *DeterministicPlanner {
	return &DeterministicPlanner{}
}

func (*DeterministicPlanner) Plan(ctx context.Context, request ports.PlanRequest) (ports.PlanResponse, error) {
	if ctx == nil {
		return ports.PlanResponse{}, ErrNilContext
	}
	if err := ctx.Err(); err != nil {
		return ports.PlanResponse{}, err
	}
	if strings.TrimSpace(request.Goal) == "" {
		return ports.PlanResponse{}, fmt.Errorf("%w: goal is required", ErrInvalidPlan)
	}
	if strings.TrimSpace(request.WorkingDirectory) == "" || !filepath.IsAbs(request.WorkingDirectory) {
		return ports.PlanResponse{}, fmt.Errorf("%w: an absolute working directory is required", ErrInvalidPlan)
	}
	if len(request.AvailableProviders) == 0 {
		return ports.PlanResponse{}, fmt.Errorf("%w: at least one provider must be selected", ErrInvalidPlan)
	}
	for _, provider := range request.AvailableProviders {
		if err := provider.Validate(); err != nil {
			return ports.PlanResponse{}, errors.Join(fmt.Errorf("%w: invalid available provider", ErrInvalidPlan), err)
		}
	}
	provider := request.AvailableProviders[0]
	return ports.PlanResponse{Tasks: []ports.PlannedTask{{
		ID:          "execution-1",
		Description: request.Goal,
		Provider:    provider,
	}}}, nil
}
