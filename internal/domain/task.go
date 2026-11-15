package domain

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

type TaskID string

// Task is a user request assigned to one selected agent provider.
type Task struct {
	ID               TaskID        `json:"id"`
	Description      string        `json:"description"`
	Provider         AgentProvider `json:"provider"`
	WorkingDirectory string        `json:"working_directory"`
	CreatedAt        time.Time     `json:"created_at"`
}

// Validate checks the task's provider-neutral invariants. It does not access
// the filesystem or require the working directory to exist.
func (t Task) Validate() error {
	if strings.TrimSpace(string(t.ID)) == "" {
		return fmt.Errorf("%w: ID is required", ErrInvalidTask)
	}
	if strings.TrimSpace(t.Description) == "" {
		return fmt.Errorf("%w: description is required", ErrInvalidTask)
	}
	if err := t.Provider.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(t.WorkingDirectory) == "" || !filepath.IsAbs(t.WorkingDirectory) {
		return fmt.Errorf("%w: working directory must be an absolute path", ErrInvalidTask)
	}
	if t.CreatedAt.IsZero() {
		return fmt.Errorf("%w: creation timestamp is required", ErrInvalidTask)
	}
	return nil
}
