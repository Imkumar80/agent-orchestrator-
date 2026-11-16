package ports

import (
	"context"
	"time"

	"github.com/Imkumar80/agent-orchestrator-/internal/domain"
)

// MemoryRepository persists provider-neutral memory records.
type MemoryRepository interface {
	CreateMemory(context.Context, domain.Memory) error
	GetMemory(context.Context, domain.MemoryID) (domain.Memory, error)
	UpdateMemory(context.Context, domain.Memory) error
	SearchMemories(context.Context, domain.MemorySearchQuery) ([]domain.Memory, error)
	ArchiveMemory(context.Context, domain.MemoryID, time.Time) error
	DeleteMemory(context.Context, domain.MemoryID) error
	ExpireWorkingMemories(context.Context, time.Time) (int64, error)
}
