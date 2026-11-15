package domain

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestTaskValidate(t *testing.T) {
	task := Task{
		ID:               "task-1",
		Description:      "Implement domain types",
		Provider:         ProviderCodex,
		WorkingDirectory: t.TempDir(),
		CreatedAt:        time.Now(),
	}
	if err := task.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	tests := []struct {
		name    string
		change  func(*Task)
		wantErr error
	}{
		{
			name: "missing ID",
			change: func(task *Task) {
				task.ID = " "
			},
			wantErr: ErrInvalidTask,
		},
		{
			name: "blank description",
			change: func(task *Task) {
				task.Description = "\t"
			},
			wantErr: ErrInvalidTask,
		},
		{
			name: "unsupported provider",
			change: func(task *Task) {
				task.Provider = "unknown"
			},
			wantErr: ErrUnsupportedProvider,
		},
		{
			name: "relative working directory",
			change: func(task *Task) {
				task.WorkingDirectory = filepath.Join("relative", "path")
			},
			wantErr: ErrInvalidTask,
		},
		{
			name: "missing creation time",
			change: func(task *Task) {
				task.CreatedAt = time.Time{}
			},
			wantErr: ErrInvalidTask,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := task
			test.change(&candidate)
			if err := candidate.Validate(); !errors.Is(err, test.wantErr) {
				t.Fatalf("Validate() error = %v, want errors.Is(_, %v)", err, test.wantErr)
			}
		})
	}
}
