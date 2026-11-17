package memory

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Imkumar80/agent-orchestrator-/internal/domain"
)

type memoryRepoFake struct {
	values map[domain.MemoryID]domain.Memory
}

func newMemoryRepoFake() *memoryRepoFake {
	return &memoryRepoFake{values: make(map[domain.MemoryID]domain.Memory)}
}
func (r *memoryRepoFake) CreateMemory(_ context.Context, m domain.Memory) error {
	if _, exists := r.values[m.ID]; exists {
		return errors.New("duplicate")
	}
	r.values[m.ID] = m
	return nil
}
func (r *memoryRepoFake) GetMemory(_ context.Context, id domain.MemoryID) (domain.Memory, error) {
	m, ok := r.values[id]
	if !ok {
		return domain.Memory{}, domain.ErrMemoryNotFound
	}
	return m, nil
}
func (r *memoryRepoFake) UpdateMemory(_ context.Context, m domain.Memory) error {
	if _, ok := r.values[m.ID]; !ok {
		return domain.ErrMemoryNotFound
	}
	r.values[m.ID] = m
	return nil
}
func (r *memoryRepoFake) SearchMemories(_ context.Context, q domain.MemorySearchQuery) ([]domain.Memory, error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	out := make([]domain.Memory, 0)
	for _, m := range r.values {
		if m.ArchivedAt != nil || (m.ExpiresAt != nil && !m.ExpiresAt.After(q.At)) {
			continue
		}
		if q.Scope != "" && (m.Scope != q.Scope || (q.Scope != domain.MemoryScopeGlobal && m.ScopeID != q.ScopeID)) {
			continue
		}
		if q.MinImportance > 0 && m.Importance < q.MinImportance {
			continue
		}
		if len(q.Types) > 0 {
			match := false
			for _, kind := range q.Types {
				if kind == m.Type {
					match = true
				}
			}
			if !match {
				continue
			}
		}
		if len(q.Tags) > 0 && !testHasMemoryTags(m.Tags, q.Tags) {
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}

func testHasMemoryTags(have, want []string) bool {
	for _, tag := range want {
		found := false
		for _, value := range have {
			if strings.EqualFold(tag, value) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
func (r *memoryRepoFake) ArchiveMemory(ctx context.Context, id domain.MemoryID, at time.Time) error {
	m, e := r.GetMemory(ctx, id)
	if e != nil {
		return e
	}
	m.ArchivedAt = &at
	m.UpdatedAt = at
	r.values[id] = m
	return nil
}
func (r *memoryRepoFake) DeleteMemory(_ context.Context, id domain.MemoryID) error {
	if _, ok := r.values[id]; !ok {
		return domain.ErrMemoryNotFound
	}
	delete(r.values, id)
	return nil
}
func (r *memoryRepoFake) ExpireWorkingMemories(_ context.Context, now time.Time) (int64, error) {
	var n int64
	for id, m := range r.values {
		if m.Type == domain.MemoryWorking && m.ExpiresAt != nil && !m.ExpiresAt.After(now) && m.ArchivedAt == nil {
			m.ArchivedAt = &now
			m.UpdatedAt = now
			r.values[id] = m
			n++
		}
	}
	return n, nil
}

func TestStoreSearchUpdateArchiveAndDelete(t *testing.T) {
	repo := newMemoryRepoFake()
	manager, _ := NewManager(repo, Config{Enabled: true, ContextMaxBytes: 200})
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	manager.now = func() time.Time { return now }
	stored, err := manager.Store(context.Background(), domain.Memory{ID: "m1", Scope: domain.MemoryScopeGlobal, Type: domain.MemorySemantic, Content: "SQLite uses one shared repository connection", Summary: "SQLite repository", Source: domain.MemorySourceUser, Confidence: 1, Importance: 4, Metadata: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	results, err := manager.Search(context.Background(), domain.MemorySearchQuery{Query: "SQLite", Scope: domain.MemoryScopeGlobal, Limit: 5})
	if err != nil || len(results) != 1 {
		t.Fatalf("search=%#v, %v", results, err)
	}
	if results[0].Score <= 0 || len(results[0].Reasons) == 0 {
		t.Fatalf("ranking explanation missing: %#v", results[0])
	}
	stored.Content = "SQLite repository is shared"
	updated, err := manager.Update(context.Background(), stored)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.UpdatedAt.After(stored.CreatedAt) {
		t.Fatalf("update timestamp did not advance: %#v", updated)
	}
	archived, err := manager.Archive(context.Background(), updated.ID)
	if err != nil || archived.ArchivedAt == nil {
		t.Fatalf("archive=%#v, %v", archived, err)
	}
	results, err = manager.Search(context.Background(), domain.MemorySearchQuery{Query: "SQLite", Limit: 5})
	if err != nil || len(results) != 0 {
		t.Fatalf("archived memory returned in search: %#v, %v", results, err)
	}
	if err := manager.Delete(context.Background(), stored.ID); err != nil {
		t.Fatal(err)
	}
}

func TestBuildTaskContextScopeLimitAndDisabled(t *testing.T) {
	repo := newMemoryRepoFake()
	manager, _ := NewManager(repo, Config{Enabled: true, ContextMaxBytes: 160})
	for _, record := range []domain.Memory{
		{ID: "global", Scope: domain.MemoryScopeGlobal, Type: domain.MemorySemantic, Content: "shared Go architecture notes", Source: domain.MemorySourceUser, Confidence: 1, Importance: 5, Metadata: []byte(`{}`)},
		{ID: "other-task", Scope: domain.MemoryScopeTask, ScopeID: "other", TaskID: taskID("other"), Type: domain.MemorySemantic, Content: "Go architecture private notes", Source: domain.MemorySourceUser, Confidence: 1, Importance: 5, Metadata: []byte(`{}`)},
	} {
		if _, err := manager.Store(context.Background(), record); err != nil {
			t.Fatal(err)
		}
	}
	ctx, err := manager.BuildTaskContext(context.Background(), domain.Task{ID: "current", Description: "Go architecture"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ctx) > 160 {
		t.Fatalf("context is %d bytes, limit 160", len(ctx))
	}
	if !contains(ctx, "memory global") || contains(ctx, "other-task") {
		t.Fatalf("scope leakage in context: %q", ctx)
	}
	disabled, _ := NewManager(repo, Config{Enabled: false, ContextMaxBytes: 100})
	ctx, err = disabled.BuildTaskContext(context.Background(), domain.Task{ID: "current", Description: "Go"})
	if err != nil || ctx != "" {
		t.Fatalf("disabled context=%q, %v", ctx, err)
	}
}

func TestSearchExpiresWorkingMemory(t *testing.T) {
	repo := newMemoryRepoFake()
	now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	manager, _ := NewManager(repo, Config{})
	manager.now = func() time.Time { return now }
	expires := now.Add(time.Hour)
	if _, err := manager.Store(context.Background(), domain.Memory{ID: "working", Scope: domain.MemoryScopeGlobal, Type: domain.MemoryWorking, Content: "temporary", Source: domain.MemorySourceSystem, Confidence: 1, Importance: 1, Metadata: []byte(`{}`), ExpiresAt: &expires}); err != nil {
		t.Fatal(err)
	}
	now = expires
	items, err := manager.Search(context.Background(), domain.MemorySearchQuery{Limit: 10})
	if err != nil || len(items) != 0 {
		t.Fatalf("expired search=%#v, %v", items, err)
	}
	got, _ := repo.GetMemory(context.Background(), "working")
	if got.ArchivedAt == nil {
		t.Fatal("expired working memory not archived")
	}
}

func taskID(value string) *domain.TaskID { id := domain.TaskID(value); return &id }
func contains(value, part string) bool   { return len(part) == 0 || stringContains(value, part) }
func stringContains(value, part string) bool {
	for i := 0; i+len(part) <= len(value); i++ {
		if value[i:i+len(part)] == part {
			return true
		}
	}
	return false
}
