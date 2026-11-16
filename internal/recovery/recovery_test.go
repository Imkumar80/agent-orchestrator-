package recovery

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Imkumar80/agent-orchestrator-/internal/domain"
	"github.com/Imkumar80/agent-orchestrator-/internal/ports"
	"github.com/Imkumar80/agent-orchestrator-/internal/providers"
	"github.com/Imkumar80/agent-orchestrator-/internal/session"
)

type memoryRepo struct {
	mu    sync.Mutex
	items map[domain.SessionID]domain.Session
}

func (r *memoryRepo) Create(_ context.Context, s domain.Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items[s.ID] = s
	return nil
}
func (r *memoryRepo) Get(_ context.Context, id domain.SessionID) (domain.Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.items[id]
	if !ok {
		return domain.Session{}, domain.ErrSessionNotFound
	}
	return s, nil
}
func (r *memoryRepo) Update(_ context.Context, s domain.Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.items[s.ID]; !ok {
		return domain.ErrSessionNotFound
	}
	r.items[s.ID] = s
	return nil
}
func (r *memoryRepo) List(_ context.Context) ([]domain.Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]domain.Session, 0, len(r.items))
	for _, s := range r.items {
		out = append(out, s)
	}
	return out, nil
}

type fakeProvider struct {
	resume    bool
	resumeErr error
	ref       ports.SessionRef
	calls     int
	mu        sync.Mutex
}

func (*fakeProvider) Name() domain.AgentProvider { return domain.ProviderCodex }
func (p *fakeProvider) Capabilities() domain.AgentCapabilities {
	return domain.AgentCapabilities{Resume: p.resume}
}
func (*fakeProvider) Start(context.Context, ports.StartRequest) (ports.SessionRef, error) {
	return ports.SessionRef{}, errors.New("unexpected Start")
}
func (*fakeProvider) Send(context.Context, ports.SessionRef, ports.AgentInput) error { return nil }
func (*fakeProvider) Subscribe(context.Context, ports.SessionRef) (<-chan domain.AgentEvent, error) {
	return nil, nil
}
func (*fakeProvider) Interrupt(context.Context, ports.SessionRef) error { return nil }
func (p *fakeProvider) Resume(_ context.Context, ref ports.SessionRef) (ports.SessionRef, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.resumeErr != nil {
		return ports.SessionRef{}, p.resumeErr
	}
	if p.ref.SessionID == "" {
		return ref, nil
	}
	return p.ref, nil
}
func (*fakeProvider) Close(context.Context, ports.SessionRef) error { return nil }

func TestRecoveryInterruptsUnsupportedAndSkipsTerminal(t *testing.T) {
	repo := newRepo()
	stale := testSession("stale", domain.SessionRunning)
	terminal := testSession("done", domain.SessionCompleted)
	repo.items[stale.ID] = stale
	repo.items[terminal.ID] = terminal
	registry := providers.NewRegistry()
	p := &fakeProvider{}
	if err := registry.Register(p); err != nil {
		t.Fatal(err)
	}
	lifecycle, _ := session.NewLifecycleManager(repo)
	svc, _ := New(repo, repo, registry, lifecycle, nil)
	report, err := svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got, _ := repo.Get(context.Background(), stale.ID)
	if got.Status != domain.SessionInterrupted || got.Error == nil || got.Error.Code != errorUnavailable {
		t.Fatalf("stale state = %#v", got)
	}
	unchanged, _ := repo.Get(context.Background(), terminal.ID)
	if unchanged.Status != terminal.Status {
		t.Fatalf("terminal changed: %s", unchanged.Status)
	}
	if report.Inspected != 2 || report.TerminalSkipped != 1 || report.Unrecoverable != 1 {
		t.Fatalf("report = %+v", report)
	}
}

func TestRecoveryRequiresProviderConfirmation(t *testing.T) {
	repo := newRepo()
	s := testSession("resume", domain.SessionWaitingForInput)
	s.NativeProviderSessionID = "native-1"
	repo.items[s.ID] = s
	registry := providers.NewRegistry()
	p := &fakeProvider{resume: true, ref: ports.SessionRef{SessionID: s.ID, NativeProviderSessionID: "native-1"}}
	_ = registry.Register(p)
	lifecycle, _ := session.NewLifecycleManager(repo)
	svc, _ := New(repo, repo, registry, lifecycle, nil)
	first, err := svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got, _ := repo.Get(context.Background(), s.ID)
	if got.Status != domain.SessionRunning || first.Reconciled != 1 {
		t.Fatalf("session=%+v report=%+v", got, first)
	}
	second, err := svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second.AlreadyHandled != 1 || p.calls != 1 {
		t.Fatalf("second=%+v resume calls=%d", second, p.calls)
	}
}

func TestRecoveryProviderErrorBecomesInterrupted(t *testing.T) {
	repo := newRepo()
	s := testSession("failure", domain.SessionRunning)
	s.NativeProviderSessionID = "native"
	repo.items[s.ID] = s
	registry := providers.NewRegistry()
	p := &fakeProvider{resume: true, resumeErr: errors.New("provider offline")}
	_ = registry.Register(p)
	lifecycle, _ := session.NewLifecycleManager(repo)
	svc, _ := New(repo, repo, registry, lifecycle, nil)
	report, err := svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got, _ := repo.Get(context.Background(), s.ID)
	if got.Status != domain.SessionInterrupted || report.Unrecoverable != 1 || p.calls != 1 {
		t.Fatalf("session=%+v report=%+v calls=%d", got, report, p.calls)
	}
}

func TestRecoveryConcurrentCallsDoNotStartExecutions(t *testing.T) {
	repo := newRepo()
	s := testSession("concurrent", domain.SessionRunning)
	s.NativeProviderSessionID = "native"
	repo.items[s.ID] = s
	registry := providers.NewRegistry()
	p := &fakeProvider{resume: true}
	_ = registry.Register(p)
	lifecycle, _ := session.NewLifecycleManager(repo)
	svc, _ := New(repo, repo, registry, lifecycle, nil)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := svc.Run(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if p.calls != 1 {
		t.Fatalf("resume confirmations=%d", p.calls)
	}
}

func newRepo() *memoryRepo { return &memoryRepo{items: map[domain.SessionID]domain.Session{}} }
func testSession(id string, status domain.SessionStatus) domain.Session {
	now := time.Now().UTC()
	return domain.Session{ID: domain.SessionID(id), TaskID: domain.TaskID("task-" + id), Provider: domain.ProviderCodex, Status: status, CreatedAt: now.Add(-time.Second), UpdatedAt: now}
}
