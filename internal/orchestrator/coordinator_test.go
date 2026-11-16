package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Imkumar80/agent-orchestrator-/internal/domain"
	"github.com/Imkumar80/agent-orchestrator-/internal/memory"
	"github.com/Imkumar80/agent-orchestrator-/internal/ports"
	"github.com/Imkumar80/agent-orchestrator-/internal/providers"
	"github.com/Imkumar80/agent-orchestrator-/internal/session"
)

var (
	errPlanning       = errors.New("planner unavailable")
	errSessionStartup = errors.New("provider startup failed")
)

type plannerFunc func(context.Context, ports.PlanRequest) (ports.PlanResponse, error)

func (f plannerFunc) Plan(ctx context.Context, request ports.PlanRequest) (ports.PlanResponse, error) {
	return f(ctx, request)
}

type sessionStarterFunc func(context.Context, domain.Task) (domain.Session, error)

func (f sessionStarterFunc) CreateAndStart(ctx context.Context, task domain.Task) (domain.Session, error) {
	return f(ctx, task)
}

func coordinatorTask() domain.Task {
	return domain.Task{
		ID:               "task-user-selected",
		Description:      "implement the requested feature",
		Provider:         domain.ProviderOpenCode,
		WorkingDirectory: testWorkingDirectory,
		CreatedAt:        time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
	}
}

var testWorkingDirectory = func() string {
	path, err := filepath.Abs(".")
	if err != nil {
		panic(err)
	}
	return path
}()

func validProposal(task domain.Task) ports.PlanResponse {
	return ports.PlanResponse{Tasks: []ports.PlannedTask{{
		ID:          "execution-1",
		Description: task.Description,
		Provider:    task.Provider,
	}}}
}

func TestCoordinatorPlansAndStartsSession(t *testing.T) {
	task := coordinatorTask()
	plannerCalls, managerCalls := 0, 0
	planner := plannerFunc(func(_ context.Context, request ports.PlanRequest) (ports.PlanResponse, error) {
		plannerCalls++
		if request.Goal != task.Description || request.WorkingDirectory != task.WorkingDirectory {
			t.Fatalf("planner request = %#v", request)
		}
		if len(request.AvailableProviders) != 1 || request.AvailableProviders[0] != task.Provider {
			t.Fatalf("available providers = %#v, want only requested %q", request.AvailableProviders, task.Provider)
		}
		return validProposal(task), nil
	})
	wantSession := domain.Session{ID: "session-1", TaskID: task.ID, Provider: task.Provider, Status: domain.SessionRunning}
	manager := sessionStarterFunc(func(_ context.Context, got domain.Task) (domain.Session, error) {
		managerCalls++
		if got != task {
			t.Fatalf("CreateAndStart task = %#v, want unchanged task %#v", got, task)
		}
		return wantSession, nil
	})
	coordinator, err := NewCoordinator(planner, manager)
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}

	got, err := coordinator.Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got != wantSession || plannerCalls != 1 || managerCalls != 1 {
		t.Fatalf("Execute() = %#v; planner calls=%d manager calls=%d", got, plannerCalls, managerCalls)
	}
}

func TestDeterministicPlannerUsesSelectedProvider(t *testing.T) {
	planner := NewDeterministicPlanner()
	request := ports.PlanRequest{
		Goal:               "perform the task",
		WorkingDirectory:   testWorkingDirectory,
		AvailableProviders: []domain.AgentProvider{domain.ProviderOpenCode},
	}
	one, err := planner.Plan(context.Background(), request)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	two, err := planner.Plan(context.Background(), request)
	if err != nil {
		t.Fatalf("second Plan() error = %v", err)
	}
	if fmt.Sprint(one) != fmt.Sprint(two) {
		t.Fatalf("planner output is not deterministic: %#v != %#v", one, two)
	}
	if len(one.Tasks) != 1 || one.Tasks[0].Provider != domain.ProviderOpenCode {
		t.Fatalf("plan = %#v, want one step on the selected provider", one)
	}
}

func TestCoordinatorWithoutPlannerStartsDirectly(t *testing.T) {
	task := coordinatorTask()
	calls := 0
	coordinator, err := NewCoordinator(nil, sessionStarterFunc(func(_ context.Context, got domain.Task) (domain.Session, error) {
		calls++
		return domain.Session{TaskID: got.ID, Provider: got.Provider}, nil
	}))
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	if _, err := coordinator.Execute(context.Background(), task); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("session manager calls = %d, want 1", calls)
	}
}

func TestCoordinatorRejectsInvalidTaskBeforePlanning(t *testing.T) {
	task := coordinatorTask()
	task.Description = "  "
	plannerCalls, managerCalls := 0, 0
	coordinator, err := NewCoordinator(plannerFunc(func(context.Context, ports.PlanRequest) (ports.PlanResponse, error) {
		plannerCalls++
		return ports.PlanResponse{}, nil
	}), sessionStarterFunc(func(context.Context, domain.Task) (domain.Session, error) {
		managerCalls++
		return domain.Session{}, nil
	}))
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	if _, err := coordinator.Execute(context.Background(), task); !errors.Is(err, domain.ErrInvalidTask) {
		t.Fatalf("Execute() error = %v, want ErrInvalidTask", err)
	}
	if plannerCalls != 0 || managerCalls != 0 {
		t.Fatalf("calls after invalid task: planner=%d manager=%d", plannerCalls, managerCalls)
	}
}

func TestCoordinatorPreservesPlannerFailure(t *testing.T) {
	task := coordinatorTask()
	managerCalls := 0
	coordinator, err := NewCoordinator(plannerFunc(func(context.Context, ports.PlanRequest) (ports.PlanResponse, error) {
		return ports.PlanResponse{}, errPlanning
	}), sessionStarterFunc(func(context.Context, domain.Task) (domain.Session, error) {
		managerCalls++
		return domain.Session{}, nil
	}))
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	if _, err := coordinator.Execute(context.Background(), task); !errors.Is(err, errPlanning) {
		t.Fatalf("Execute() error = %v, want planner error", err)
	}
	if managerCalls != 0 {
		t.Fatalf("session manager calls = %d, want 0", managerCalls)
	}
}

func TestCoordinatorRejectsInvalidPlans(t *testing.T) {
	task := coordinatorTask()
	tests := []struct {
		name string
		plan ports.PlanResponse
	}{
		{name: "no task"},
		{name: "multiple tasks", plan: ports.PlanResponse{Tasks: []ports.PlannedTask{{ID: "one", Description: task.Description, Provider: task.Provider}, {ID: "two", Description: task.Description, Provider: task.Provider}}}},
		{name: "missing task ID", plan: ports.PlanResponse{Tasks: []ports.PlannedTask{{Description: task.Description, Provider: task.Provider}}}},
		{name: "missing description", plan: ports.PlanResponse{Tasks: []ports.PlannedTask{{ID: "one", Provider: task.Provider}}}},
		{name: "changed description", plan: ports.PlanResponse{Tasks: []ports.PlannedTask{{ID: "one", Description: "different scope", Provider: task.Provider}}}},
		{name: "unsupported provider", plan: ports.PlanResponse{Tasks: []ports.PlannedTask{{ID: "one", Description: task.Description, Provider: "unknown"}}}},
		{name: "dependency", plan: ports.PlanResponse{Tasks: []ports.PlannedTask{{ID: "one", Description: task.Description, Provider: task.Provider, DependsOn: []string{"other"}}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			managerCalls := 0
			coordinator, err := NewCoordinator(plannerFunc(func(context.Context, ports.PlanRequest) (ports.PlanResponse, error) {
				return test.plan, nil
			}), sessionStarterFunc(func(context.Context, domain.Task) (domain.Session, error) {
				managerCalls++
				return domain.Session{}, nil
			}))
			if err != nil {
				t.Fatalf("NewCoordinator() error = %v", err)
			}
			if _, err := coordinator.Execute(context.Background(), task); !errors.Is(err, ErrInvalidPlan) {
				t.Fatalf("Execute() error = %v, want ErrInvalidPlan", err)
			}
			if managerCalls != 0 {
				t.Fatalf("session manager calls = %d, want 0", managerCalls)
			}
		})
	}
}

func TestCoordinatorRejectsProviderMismatch(t *testing.T) {
	task := coordinatorTask()
	proposal := validProposal(task)
	proposal.Tasks[0].Provider = domain.ProviderCodex
	managerCalls := 0
	coordinator, err := NewCoordinator(plannerFunc(func(context.Context, ports.PlanRequest) (ports.PlanResponse, error) {
		return proposal, nil
	}), sessionStarterFunc(func(context.Context, domain.Task) (domain.Session, error) {
		managerCalls++
		return domain.Session{}, nil
	}))
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	if _, err := coordinator.Execute(context.Background(), task); !errors.Is(err, ErrProviderMismatch) {
		t.Fatalf("Execute() error = %v, want ErrProviderMismatch", err)
	}
	if managerCalls != 0 {
		t.Fatalf("session manager calls = %d, want 0", managerCalls)
	}
}

func TestCoordinatorPreservesSessionManagerFailure(t *testing.T) {
	task := coordinatorTask()
	coordinator, err := NewCoordinator(NewDeterministicPlanner(), sessionStarterFunc(func(context.Context, domain.Task) (domain.Session, error) {
		return domain.Session{ID: "failed-session", Status: domain.SessionFailed}, errSessionStartup
	}))
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	got, err := coordinator.Execute(context.Background(), task)
	if !errors.Is(err, errSessionStartup) {
		t.Fatalf("Execute() error = %v, want session manager error", err)
	}
	if got.ID != "failed-session" || got.Status != domain.SessionFailed {
		t.Fatalf("returned session = %#v, want failure result preserved", got)
	}
}

func TestCoordinatorHonorsContextCancellation(t *testing.T) {
	task := coordinatorTask()
	plannerCalls, managerCalls := 0, 0
	ctx, cancel := context.WithCancel(context.Background())
	coordinator, err := NewCoordinator(plannerFunc(func(context.Context, ports.PlanRequest) (ports.PlanResponse, error) {
		plannerCalls++
		cancel()
		return validProposal(task), nil
	}), sessionStarterFunc(func(context.Context, domain.Task) (domain.Session, error) {
		managerCalls++
		return domain.Session{}, nil
	}))
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	if _, err := coordinator.Execute(ctx, task); !errors.Is(err, context.Canceled) {
		t.Fatalf("Execute() error = %v, want context.Canceled", err)
	}
	if plannerCalls != 1 || managerCalls != 0 {
		t.Fatalf("calls after planner cancellation: planner=%d manager=%d", plannerCalls, managerCalls)
	}

	preCanceled, preCancel := context.WithCancel(context.Background())
	preCancel()
	if _, err := coordinator.Execute(preCanceled, task); !errors.Is(err, context.Canceled) {
		t.Fatalf("Execute() pre-canceled error = %v, want context.Canceled", err)
	}
}

func TestCoordinatorRequiresSessionManager(t *testing.T) {
	if _, err := NewCoordinator(nil, nil); !errors.Is(err, ErrInvalidDependency) {
		t.Fatalf("NewCoordinator() error = %v, want ErrInvalidDependency", err)
	}
}

type memoryContextBuilderFunc func(context.Context, domain.Task) (string, error)

func (f memoryContextBuilderFunc) BuildTaskContext(ctx context.Context, task domain.Task) (string, error) {
	return f(ctx, task)
}

type contextualStarter struct {
	calls   int
	context string
	task    domain.Task
}

func (s *contextualStarter) CreateAndStart(_ context.Context, task domain.Task) (domain.Session, error) {
	s.task = task
	return domain.Session{ID: "plain"}, nil
}
func (s *contextualStarter) CreateAndStartWithContext(_ context.Context, task domain.Task, context string) (domain.Session, error) {
	s.calls++
	s.task = task
	s.context = context
	return domain.Session{ID: "with-memory"}, nil
}

func TestCoordinatorRetrievesAndForwardsMemoryContext(t *testing.T) {
	task := coordinatorTask()
	starter := &contextualStarter{}
	builder := memoryContextBuilderFunc(func(_ context.Context, got domain.Task) (string, error) {
		if got != task {
			t.Fatalf("memory task=%#v", got)
		}
		return "reference data", nil
	})
	coordinator, err := NewCoordinatorWithMemory(NewDeterministicPlanner(), starter, builder)
	if err != nil {
		t.Fatal(err)
	}
	created, err := coordinator.Execute(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != "with-memory" || starter.calls != 1 || starter.context != "reference data" || starter.task != task {
		t.Fatalf("session=%#v starter=%#v", created, starter)
	}
}

type memoryPortFake struct{ record domain.Memory }

func (r *memoryPortFake) CreateMemory(_ context.Context, m domain.Memory) error {
	r.record = m
	return nil
}
func (r *memoryPortFake) GetMemory(_ context.Context, id domain.MemoryID) (domain.Memory, error) {
	if r.record.ID != id {
		return domain.Memory{}, domain.ErrMemoryNotFound
	}
	return r.record, nil
}
func (r *memoryPortFake) UpdateMemory(_ context.Context, m domain.Memory) error {
	r.record = m
	return nil
}
func (r *memoryPortFake) SearchMemories(_ context.Context, q domain.MemorySearchQuery) ([]domain.Memory, error) {
	match := false
	for _, term := range strings.Fields(strings.ToLower(q.Query)) {
		if strings.Contains(strings.ToLower(r.record.Content), term) {
			match = true
		}
	}
	if r.record.ID == "" || r.record.Scope != q.Scope || !match {
		return nil, nil
	}
	return []domain.Memory{r.record}, nil
}
func (r *memoryPortFake) ArchiveMemory(context.Context, domain.MemoryID, time.Time) error { return nil }
func (r *memoryPortFake) DeleteMemory(context.Context, domain.MemoryID) error             { return nil }
func (r *memoryPortFake) ExpireWorkingMemories(context.Context, time.Time) (int64, error) {
	return 0, nil
}

func TestStoredMemoryReachesProviderThroughCoordinator(t *testing.T) {
	task := coordinatorTask()
	task.Description = "implement the requested feature"
	store := &memoryPortFake{}
	memoryManager, err := memory.NewManager(store, memory.Config{Enabled: true, ContextMaxBytes: 500})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := memoryManager.Store(context.Background(), domain.Memory{ID: "memory-architecture", Scope: domain.MemoryScopeGlobal, Type: domain.MemorySemantic, Content: "The requested feature uses a stable architecture", Source: domain.MemorySourceUser, Confidence: 1, Importance: 4, Metadata: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	agent := &coordinatorAgent{}
	registry := providers.NewRegistry()
	if err := registry.Register(agent); err != nil {
		t.Fatal(err)
	}
	sessionManager, err := session.NewManager(newCoordinatorRepository(), registry)
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := NewCoordinatorWithMemory(NewDeterministicPlanner(), sessionManager, memoryManager)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Execute(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if agent.got.Task.Description != task.Description || !strings.Contains(agent.got.MemoryContext, "stable architecture") || !strings.Contains(agent.got.MemoryContext, "untrusted reference data") {
		t.Fatalf("provider request did not receive task plus memory context: %#v", agent.got)
	}
}

type coordinatorRepository struct {
	mu       sync.RWMutex
	sessions map[domain.SessionID]domain.Session
}

func newCoordinatorRepository() *coordinatorRepository {
	return &coordinatorRepository{sessions: make(map[domain.SessionID]domain.Session)}
}

func (r *coordinatorRepository) Create(_ context.Context, value domain.Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.sessions[value.ID]; exists {
		return fmt.Errorf("duplicate session %q", value.ID)
	}
	r.sessions[value.ID] = value
	return nil
}

func (r *coordinatorRepository) Get(_ context.Context, id domain.SessionID) (domain.Session, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	value, exists := r.sessions[id]
	if !exists {
		return domain.Session{}, fmt.Errorf("%w: %q", domain.ErrSessionNotFound, id)
	}
	return value, nil
}

func (r *coordinatorRepository) Update(_ context.Context, value domain.Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.sessions[value.ID]; !exists {
		return domain.ErrSessionNotFound
	}
	r.sessions[value.ID] = value
	return nil
}

type coordinatorAgent struct {
	starts int
	got    ports.StartRequest
}

func (*coordinatorAgent) Name() domain.AgentProvider { return domain.ProviderOpenCode }
func (*coordinatorAgent) Capabilities() domain.AgentCapabilities {
	return domain.AgentCapabilities{}
}
func (p *coordinatorAgent) Start(_ context.Context, request ports.StartRequest) (ports.SessionRef, error) {
	p.starts++
	p.got = request
	return ports.SessionRef{SessionID: request.SessionID, NativeProviderSessionID: "fake-provider-session"}, nil
}
func (*coordinatorAgent) Send(context.Context, ports.SessionRef, ports.AgentInput) error {
	return errors.New("not supported in test")
}
func (*coordinatorAgent) Subscribe(context.Context, ports.SessionRef) (<-chan domain.AgentEvent, error) {
	return nil, errors.New("not supported in test")
}
func (*coordinatorAgent) Interrupt(context.Context, ports.SessionRef) error {
	return errors.New("not supported in test")
}
func (*coordinatorAgent) Resume(context.Context, ports.SessionRef) (ports.SessionRef, error) {
	return ports.SessionRef{}, errors.New("not supported in test")
}
func (*coordinatorAgent) Close(context.Context, ports.SessionRef) error { return nil }

func TestCoordinatorRunsThroughSessionManagerAndRuntimeRegistry(t *testing.T) {
	task := coordinatorTask()
	agent := &coordinatorAgent{}
	registry := providers.NewRegistry()
	if err := registry.Register(agent); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	manager, err := session.NewManager(newCoordinatorRepository(), registry)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	coordinator, err := NewCoordinator(NewDeterministicPlanner(), manager)
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}

	created, err := coordinator.Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if created.Status != domain.SessionRunning || created.Provider != task.Provider {
		t.Fatalf("session = %#v, want running on requested provider %q", created, task.Provider)
	}
	if agent.starts != 1 || agent.got.Task.Provider != domain.ProviderOpenCode {
		t.Fatalf("provider starts=%d request=%#v", agent.starts, agent.got)
	}
}
