package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/Imkumar80/agent-orchestrator-/internal/api"
	"github.com/Imkumar80/agent-orchestrator-/internal/config"
	"github.com/Imkumar80/agent-orchestrator-/internal/domain"
	"github.com/Imkumar80/agent-orchestrator-/internal/events"
	"github.com/Imkumar80/agent-orchestrator-/internal/memory"
	"github.com/Imkumar80/agent-orchestrator-/internal/orchestrator"
	"github.com/Imkumar80/agent-orchestrator-/internal/providers"
	"github.com/Imkumar80/agent-orchestrator-/internal/providers/codex"
	"github.com/Imkumar80/agent-orchestrator-/internal/recovery"
	"github.com/Imkumar80/agent-orchestrator-/internal/session"
	"github.com/Imkumar80/agent-orchestrator-/internal/storage/sqlite"
)

// Run builds the application composition and serves HTTP until ctx is cancelled.
func Run(shutdownSignal context.Context) error {
	logger := slog.Default()
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	executionCtx, cancelExecution := context.WithCancel(context.Background())
	defer cancelExecution()

	repository, err := sqlite.Open(executionCtx, sqlite.Config{Path: cfg.SQLitePath})
	if err != nil {
		return fmt.Errorf("open session database: %w", err)
	}
	owner, err := recovery.NewOwnerID()
	if err != nil {
		_ = repository.Close()
		return err
	}
	leaseNow := time.Now().UTC()
	leaseTTL := 30 * time.Second
	acquired, err := repository.AcquireStartupLease(executionCtx, owner, leaseNow, leaseNow.Add(leaseTTL))
	if err != nil {
		_ = repository.Close()
		return fmt.Errorf("acquire daemon startup lease: %w", err)
	}
	if !acquired {
		_ = repository.Close()
		return errors.New("another orchestrator instance owns the SQLite database lease")
	}
	leaseCtx, stopLease := context.WithCancel(executionCtx)
	leaseLost := make(chan error, 1)
	go renewLease(leaseCtx, repository, owner, leaseTTL, cancelExecution, leaseLost, logger)
	broker, err := events.NewBroker(events.Config{})
	if err != nil {
		stopLease()
		_ = repository.ReleaseStartupLease(context.Background(), owner)
		_ = repository.Close()
		return fmt.Errorf("create event broker: %w", err)
	}
	registry := providers.NewRegistry()
	for _, providerName := range cfg.EnabledProviders {
		if providerName != domain.ProviderCodex {
			_ = broker.Shutdown()
			stopLease()
			_ = repository.ReleaseStartupLease(context.Background(), owner)
			_ = repository.Close()
			return fmt.Errorf("provider %q is enabled but has no adapter; currently only codex is implemented", providerName)
		}
		provider, err := codex.NewProvider(codex.Config{
			ExecutablePath: cfg.CodexExecutable,
			Publisher:      broker,
		})
		if err != nil {
			_ = broker.Shutdown()
			stopLease()
			_ = repository.ReleaseStartupLease(context.Background(), owner)
			_ = repository.Close()
			return fmt.Errorf("initialize codex provider: %w", err)
		}
		if err := registry.Register(provider); err != nil {
			_ = broker.Shutdown()
			stopLease()
			_ = repository.ReleaseStartupLease(context.Background(), owner)
			_ = repository.Close()
			return fmt.Errorf("register provider %q: %w", providerName, err)
		}
	}
	manager, err := session.NewManager(repository, registry)
	if err != nil {
		_ = broker.Shutdown()
		stopLease()
		_ = repository.ReleaseStartupLease(context.Background(), owner)
		_ = repository.Close()
		return fmt.Errorf("create session manager: %w", err)
	}
	memoryManager, err := memory.NewManager(repository, memory.Config{Enabled: cfg.MemoryEnabled, ContextMaxBytes: cfg.MemoryContextMaxBytes})
	if err != nil {
		_ = broker.Shutdown()
		stopLease()
		_ = repository.ReleaseStartupLease(context.Background(), owner)
		_ = repository.Close()
		return fmt.Errorf("create memory manager: %w", err)
	}
	lifecycle, err := session.NewLifecycleManager(repository)
	if err != nil {
		_ = broker.Shutdown()
		stopLease()
		_ = repository.ReleaseStartupLease(context.Background(), owner)
		_ = repository.Close()
		return fmt.Errorf("create lifecycle manager: %w", err)
	}
	recoveryService, err := recovery.New(repository, repository, registry, lifecycle, broker)
	if err != nil {
		_ = broker.Shutdown()
		stopLease()
		_ = repository.ReleaseStartupLease(context.Background(), owner)
		_ = repository.Close()
		return fmt.Errorf("create recovery service: %w", err)
	}
	recoveryReport, err := recoveryService.Run(executionCtx)
	if err != nil {
		_ = broker.Shutdown()
		stopLease()
		_ = repository.ReleaseStartupLease(context.Background(), owner)
		_ = repository.Close()
		return fmt.Errorf("recover persisted sessions: %w", err)
	}
	logger.Info("startup session recovery finished", "inspected", recoveryReport.Inspected, "terminal_skipped", recoveryReport.TerminalSkipped, "reconciled", recoveryReport.Reconciled, "unrecoverable", recoveryReport.Unrecoverable, "errors", recoveryReport.Errors, "duration", recoveryReport.Duration)
	for _, issue := range recoveryReport.Issues {
		logger.Warn("session recovery issue", "session_id", issue.SessionID, "provider", issue.Provider, "category", issue.Category, "error", issue.Error)
	}
	tracked := newTrackedSessions(manager, logger)
	for _, id := range recoveryReport.RecoveredSessionIDs {
		tracked.Track(id)
	}
	coordinator, err := orchestrator.NewCoordinatorWithMemory(orchestrator.NewDeterministicPlanner(), tracked, memoryManager)
	if err != nil {
		_ = broker.Shutdown()
		stopLease()
		_ = repository.ReleaseStartupLease(context.Background(), owner)
		_ = repository.Close()
		return fmt.Errorf("create coordinator: %w", err)
	}
	server, err := api.NewServer(api.Config{Address: cfg.HTTPAddress, Logger: logger}, api.Dependencies{
		Coordinator: coordinator, Sessions: tracked, Events: broker, ExecutionContext: executionCtx, Providers: registry,
		Memories: memoryManager,
	})
	if err != nil {
		_ = broker.Shutdown()
		stopLease()
		_ = repository.ReleaseStartupLease(context.Background(), owner)
		_ = repository.Close()
		return fmt.Errorf("create HTTP server: %w", err)
	}

	serveResult := make(chan error, 1)
	go func() { serveResult <- server.ListenAndServe() }()
	logger.Info("HTTP server listening", "address", cfg.HTTPAddress, "providers", cfg.EnabledProviders)
	var serveErr error
	select {
	case err := <-serveResult:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server failed", "error", err)
			serveErr = err
		}
	case <-shutdownSignal.Done():
		logger.Info("shutdown requested")
	case leaseErr := <-leaseLost:
		logger.Error("daemon startup lease was lost", "error", leaseErr)
		serveErr = fmt.Errorf("daemon lease lost: %w", leaseErr)
	}

	graceCtx, cancelGrace := context.WithTimeout(context.Background(), cfg.GracefulShutdown)
	shutdownErr := server.Shutdown(graceCtx)
	cancelGrace()
	if shutdownErr != nil {
		logger.Warn("HTTP graceful shutdown required forced connection close", "error", shutdownErr)
	}

	// Accepted provider work is owned by executionCtx, not by the HTTP request.
	// Close tracked sessions through the session manager before closing storage.
	cancelExecution()
	cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), cfg.GracefulShutdown)
	trackedErr := tracked.CloseAll(cleanupCtx)
	cancelCleanup()
	if trackedErr != nil {
		logger.Error("session cleanup failed", "error", trackedErr)
	}
	brokerErr := broker.Shutdown()
	stopLease()
	leaseErr := repository.ReleaseStartupLease(context.Background(), owner)
	databaseErr := repository.Close()
	return errors.Join(serveErr, stageError("HTTP shutdown", shutdownErr), stageError("session cleanup", trackedErr), stageError("event broker shutdown", brokerErr), stageError("release daemon lease", leaseErr), stageError("database close", databaseErr))
}

func stageError(stage string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", stage, err)
}

func renewLease(ctx context.Context, repository *sqlite.Repository, owner string, ttl time.Duration, cancel context.CancelFunc, lost chan<- error, logger *slog.Logger) {
	ticker := time.NewTicker(ttl / 3)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now().UTC()
			ok, err := repository.RenewStartupLease(ctx, owner, now, now.Add(ttl))
			if err == nil && ok {
				continue
			}
			if err == nil {
				err = errors.New("lease ownership expired or changed")
			}
			wrapped := fmt.Errorf("renew daemon lease: %w", err)
			logger.Error("daemon lease renewal failed; stopping execution", "error", wrapped)
			select {
			case lost <- wrapped:
			default:
			}
			cancel()
			return
		}
	}
}

// trackedSessions preserves the session manager's lifecycle ownership while
// allowing the process composition root to close all sessions started through
// this HTTP server at shutdown. It does not add repository list semantics.
type trackedSessions struct {
	manager   *session.Manager
	logger    *slog.Logger
	mu        sync.Mutex
	closing   bool
	active    sync.WaitGroup
	ids       map[domain.SessionID]struct{}
	closeOnce sync.Once
	closeErr  error
}

func newTrackedSessions(manager *session.Manager, logger *slog.Logger) *trackedSessions {
	return &trackedSessions{manager: manager, logger: logger, ids: make(map[domain.SessionID]struct{})}
}

func (s *trackedSessions) Track(id domain.SessionID) {
	if id == "" {
		return
	}
	s.mu.Lock()
	s.ids[id] = struct{}{}
	s.mu.Unlock()
}

func (s *trackedSessions) CreateAndStart(ctx context.Context, task domain.Task) (domain.Session, error) {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return domain.Session{}, errors.New("application is shutting down")
	}
	s.active.Add(1)
	s.mu.Unlock()
	defer s.active.Done()

	created, err := s.manager.CreateAndStart(ctx, task)
	if created.ID != "" {
		s.mu.Lock()
		s.ids[created.ID] = struct{}{}
		s.mu.Unlock()
	}
	return created, err
}

func (s *trackedSessions) CreateAndStartWithContext(ctx context.Context, task domain.Task, memoryContext string) (domain.Session, error) {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return domain.Session{}, errors.New("application is shutting down")
	}
	s.active.Add(1)
	s.mu.Unlock()
	defer s.active.Done()
	created, err := s.manager.CreateAndStartWithContext(ctx, task, memoryContext)
	if created.ID != "" {
		s.mu.Lock()
		s.ids[created.ID] = struct{}{}
		s.mu.Unlock()
	}
	return created, err
}

func (s *trackedSessions) Get(ctx context.Context, id domain.SessionID) (domain.Session, error) {
	return s.manager.Get(ctx, id)
}

func (s *trackedSessions) List(ctx context.Context) ([]domain.Session, error) {
	return s.manager.List(ctx)
}

func (s *trackedSessions) Interrupt(ctx context.Context, id domain.SessionID) (domain.Session, error) {
	return s.manager.Interrupt(ctx, id)
}

func (s *trackedSessions) CloseAll(ctx context.Context) error {
	s.closeOnce.Do(func() { s.closeErr = s.closeAll(ctx) })
	return s.closeErr
}

func (s *trackedSessions) closeAll(ctx context.Context) error {
	s.mu.Lock()
	s.closing = true
	s.mu.Unlock()
	done := make(chan struct{})
	go func() {
		s.active.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	s.mu.Lock()
	ids := make([]domain.SessionID, 0, len(s.ids))
	for id := range s.ids {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	var result error
	for _, id := range ids {
		if err := s.manager.Close(ctx, id); err != nil {
			s.logger.Warn("could not close session during shutdown", "session_id", id, "error", err)
			result = errors.Join(result, fmt.Errorf("close session %q: %w", id, err))
		}
	}
	return result
}
