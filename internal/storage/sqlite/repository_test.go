package sqlite

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Imkumar80/agent-orchestrator-/internal/domain"
)

func TestOpenCreateGetAndRoundTrip(t *testing.T) {
	repo := openTestRepository(t, filepath.Join(t.TempDir(), "nested", "sessions.db"))
	ctx := context.Background()
	session := validSession()
	session.CreatedAt = time.Date(2025, 2, 3, 4, 5, 6, 123456789, time.FixedZone("test", 5*60*60+30*60))
	session.UpdatedAt = session.CreatedAt.Add(15 * time.Second)
	session.Provider = domain.ProviderCodex
	session.Status = domain.SessionWaitingForInput
	session.NativeProviderSessionID = "native-123"
	session.Error = &domain.ErrorInfo{Code: "provider_warning", Message: "detail preserved"}

	if err := repo.Create(ctx, session); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	got, err := repo.Get(ctx, session.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.ID != session.ID || got.TaskID != session.TaskID || got.Provider != session.Provider ||
		got.NativeProviderSessionID != session.NativeProviderSessionID || got.Status != session.Status {
		t.Fatalf("Get() identity fields = %#v, want %#v", got, session)
	}
	if !got.CreatedAt.Equal(session.CreatedAt) || !got.UpdatedAt.Equal(session.UpdatedAt) {
		t.Fatalf("Get() timestamps = (%s, %s), want (%s, %s)", got.CreatedAt, got.UpdatedAt, session.CreatedAt, session.UpdatedAt)
	}
	if got.CreatedAt.Location() != time.UTC || got.UpdatedAt.Location() != time.UTC {
		t.Fatalf("timestamps should be normalized to UTC, got %v and %v", got.CreatedAt.Location(), got.UpdatedAt.Location())
	}
	if got.Error == nil || *got.Error != *session.Error {
		t.Fatalf("Get() Error = %#v, want %#v", got.Error, session.Error)
	}

	var mode string
	if err := repo.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("read journal mode: %v", err)
	}
	if mode != "wal" {
		t.Fatalf("journal mode = %q, want wal", mode)
	}
}

func TestUpdateAndStaleWrite(t *testing.T) {
	repo := openTestRepository(t, filepath.Join(t.TempDir(), "sessions.db"))
	ctx := context.Background()
	session := validSession()
	if err := repo.Create(ctx, session); err != nil {
		t.Fatal(err)
	}

	updated := session
	updated.Status = domain.SessionRunning
	updated.UpdatedAt = session.UpdatedAt.Add(time.Second)
	updated.NativeProviderSessionID = "native-456"
	if err := repo.Update(ctx, updated); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	got, err := repo.Get(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != updated.Status || got.NativeProviderSessionID != updated.NativeProviderSessionID {
		t.Fatalf("updated session = %#v, want %#v", got, updated)
	}
	if err := repo.Update(ctx, session); !errors.Is(err, ErrStaleSessionUpdate) {
		t.Fatalf("stale Update() error = %v, want ErrStaleSessionUpdate", err)
	}
}

func TestListAndStartupLease(t *testing.T) {
	repo := openTestRepository(t, filepath.Join(t.TempDir(), "lease.db"))
	ctx := context.Background()
	if err := repo.Create(ctx, validSession()); err != nil {
		t.Fatal(err)
	}
	items, err := repo.List(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("List() = %d, %v", len(items), err)
	}
	now := time.Now().UTC()
	ok, err := repo.AcquireStartupLease(ctx, "first", now, now.Add(time.Minute))
	if err != nil || !ok {
		t.Fatalf("first acquire = %v, %v", ok, err)
	}
	ok, err = repo.AcquireStartupLease(ctx, "second", now, now.Add(time.Minute))
	if err != nil || ok {
		t.Fatalf("contending acquire = %v, %v", ok, err)
	}
	ok, err = repo.RenewStartupLease(ctx, "first", now.Add(time.Second), now.Add(2*time.Minute))
	if err != nil || !ok {
		t.Fatalf("renew = %v, %v", ok, err)
	}
	if err := repo.ReleaseStartupLease(ctx, "second"); err != nil {
		t.Fatal(err)
	}
	ok, err = repo.AcquireStartupLease(ctx, "second", now.Add(2*time.Second), now.Add(time.Minute))
	if err != nil || ok {
		t.Fatalf("foreign release must not free lease: %v, %v", ok, err)
	}
	if err := repo.ReleaseStartupLease(ctx, "first"); err != nil {
		t.Fatal(err)
	}
	ok, err = repo.AcquireStartupLease(ctx, "second", now.Add(2*time.Second), now.Add(time.Minute))
	if err != nil || !ok {
		t.Fatalf("acquire after owner release = %v, %v", ok, err)
	}
}

func TestRepositoryErrors(t *testing.T) {
	repo := openTestRepository(t, filepath.Join(t.TempDir(), "sessions.db"))
	ctx := context.Background()
	if _, err := repo.Get(ctx, "missing"); !errors.Is(err, domain.ErrSessionNotFound) {
		t.Errorf("missing Get() error = %v, want ErrSessionNotFound", err)
	}
	if err := repo.Update(ctx, validSession()); !errors.Is(err, domain.ErrSessionNotFound) {
		t.Errorf("missing Update() error = %v, want ErrSessionNotFound", err)
	}
	session := validSession()
	if err := repo.Create(ctx, session); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, session); !errors.Is(err, ErrDuplicateSession) {
		t.Errorf("duplicate Create() error = %v, want ErrDuplicateSession", err)
	}
	invalid := validSession()
	invalid.TaskID = " "
	if err := repo.Create(ctx, invalid); !errors.Is(err, domain.ErrInvalidSession) {
		t.Errorf("invalid Create() error = %v, want ErrInvalidSession", err)
	}
}

func TestMigrationsRepeatWithoutDiscardingData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.db")
	repo := openTestRepository(t, path)
	session := validSession()
	if err := repo.Create(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	memory := validMemory("memory-1")
	if err := repo.CreateMemory(context.Background(), memory); err != nil {
		t.Fatal(err)
	}
	if err := applyMigrations(context.Background(), repo.db); err != nil {
		t.Fatalf("reapply migrations: %v", err)
	}
	var count int
	if err := repo.db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("migration count = %d, want 3", count)
	}
	if _, err := repo.Get(context.Background(), session.ID); err != nil {
		t.Fatalf("session lost after reapplying migrations: %v", err)
	}
	if _, err := repo.GetMemory(context.Background(), memory.ID); err != nil {
		t.Fatalf("memory lost after reapplying migrations: %v", err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(context.Background(), Config{Path: path})
	if err != nil {
		t.Fatalf("reopen database: %v", err)
	}
	defer reopened.Close()
	if _, err := reopened.Get(context.Background(), session.ID); err != nil {
		t.Fatalf("session lost after reopen: %v", err)
	}
	if _, err := reopened.GetMemory(context.Background(), memory.ID); err != nil {
		t.Fatalf("memory lost after reopen: %v", err)
	}
}

func TestMemoryRepositoryRoundTripSearchAndLifecycle(t *testing.T) {
	repo := openTestRepository(t, filepath.Join(t.TempDir(), "memories.db"))
	ctx := context.Background()
	m := validMemory("memory-1")
	m.Tags = []string{"database", "go"}
	m.Metadata = []byte(`{"reviewed":true}`)
	if err := repo.CreateMemory(ctx, m); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetMemory(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != m.Content || got.Scope != m.Scope || got.Type != m.Type || got.Source != m.Source || got.Importance != m.Importance || !got.CreatedAt.Equal(m.CreatedAt) || len(got.Tags) != 2 || string(got.Metadata) != string(m.Metadata) {
		t.Fatalf("memory round trip = %#v", got)
	}
	items, err := repo.SearchMemories(ctx, domain.MemorySearchQuery{Query: "sqlite", Scope: domain.MemoryScopeGlobal, Tags: []string{"database"}, Limit: 10})
	if err != nil || len(items) != 1 {
		t.Fatalf("memory search = %d, %v", len(items), err)
	}
	got.Content = "Updated SQLite storage notes"
	got.UpdatedAt = got.UpdatedAt.Add(time.Second)
	if err := repo.UpdateMemory(ctx, got); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetMemory(ctx, "missing"); !errors.Is(err, domain.ErrMemoryNotFound) {
		t.Fatalf("missing memory error = %v", err)
	}
	if err := repo.ArchiveMemory(ctx, m.ID, got.UpdatedAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	items, err = repo.SearchMemories(ctx, domain.MemorySearchQuery{Limit: 10})
	if err != nil || len(items) != 0 {
		t.Fatalf("archived memory search = %d, %v", len(items), err)
	}
	if err := repo.DeleteMemory(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryRepositoryExpiresWorkingRecords(t *testing.T) {
	repo := openTestRepository(t, filepath.Join(t.TempDir(), "expiry.db"))
	now := time.Now().UTC()
	expires := now.Add(time.Second)
	m := validMemory("working-1")
	m.Type = domain.MemoryWorking
	m.ExpiresAt = &expires
	if err := repo.CreateMemory(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	count, err := repo.ExpireWorkingMemories(context.Background(), expires)
	if err != nil || count != 1 {
		t.Fatalf("expire = %d, %v", count, err)
	}
	got, err := repo.GetMemory(context.Background(), m.ID)
	if err != nil || got.ArchivedAt == nil {
		t.Fatalf("expired memory = %#v, %v", got, err)
	}
}

func TestConcurrentIndependentCreates(t *testing.T) {
	repo := openTestRepository(t, filepath.Join(t.TempDir(), "sessions.db"))
	const n = 20
	var wg sync.WaitGroup
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			session := validSession()
			session.ID = domain.SessionID(fmt.Sprintf("session-%02d", i))
			session.TaskID = domain.TaskID(fmt.Sprintf("task-%02d", i))
			if err := repo.Create(context.Background(), session); err != nil {
				errCh <- err
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent Create(): %v", err)
	}
}

func TestCloseAndInvalidConfiguration(t *testing.T) {
	if _, err := Open(context.Background(), Config{Path: " "}); err == nil {
		t.Fatal("Open() accepted an empty path")
	}
	repo := openTestRepository(t, filepath.Join(t.TempDir(), "sessions.db"))
	if err := repo.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := repo.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if _, err := repo.Get(context.Background(), "any"); err == nil {
		t.Fatal("Get() succeeded after Close()")
	}
}

func openTestRepository(t *testing.T, path string) *Repository {
	t.Helper()
	repo, err := Open(context.Background(), Config{Path: path})
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	t.Cleanup(func() {
		if err := repo.Close(); err != nil {
			t.Errorf("close repository: %v", err)
		}
	})
	return repo
}

func validSession() domain.Session {
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	return domain.Session{
		ID:        "session-1",
		TaskID:    "task-1",
		Provider:  domain.ProviderCodex,
		Status:    domain.SessionPending,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func validMemory(id domain.MemoryID) domain.Memory {
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	return domain.Memory{ID: id, Scope: domain.MemoryScopeGlobal, Type: domain.MemorySemantic, Content: "SQLite repository architecture", Summary: "SQLite repository", Source: domain.MemorySourceUser, Confidence: 1, Importance: 3, Metadata: []byte(`{}`), CreatedAt: now, UpdatedAt: now}
}
