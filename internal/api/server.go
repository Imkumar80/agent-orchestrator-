// Package api exposes the orchestration application over HTTP.
package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/Imkumar80/agent-orchestrator-/internal/domain"
	"github.com/Imkumar80/agent-orchestrator-/internal/memory"
	"github.com/Imkumar80/agent-orchestrator-/internal/ports"
)

const (
	defaultAddress         = "127.0.0.1:8080"
	defaultReadHeader      = 5 * time.Second
	defaultReadTimeout     = 15 * time.Second
	defaultIdleTimeout     = 60 * time.Second
	defaultMaxRequestBytes = 1 << 20
)

// Config controls HTTP listener and bounded request handling.
type Config struct {
	Address            string
	ReadHeaderTimeout  time.Duration
	ReadTimeout        time.Duration
	IdleTimeout        time.Duration
	MaxRequestBodySize int64
	Logger             *slog.Logger
}

// TaskCoordinator is the narrow orchestration entry point used by handlers.
type TaskCoordinator interface {
	Execute(context.Context, domain.Task) (domain.Session, error)
}

// SessionManager contains only the session reads and lifecycle action used by
// this API. The HTTP layer never manipulates provider processes directly.
type SessionManager interface {
	Get(context.Context, domain.SessionID) (domain.Session, error)
	Interrupt(context.Context, domain.SessionID) (domain.Session, error)
}

type ProviderCatalog interface {
	Has(domain.AgentProvider) bool
}

type MemoryManager interface {
	Store(context.Context, domain.Memory) (domain.Memory, error)
	Get(context.Context, domain.MemoryID) (domain.Memory, error)
	Update(context.Context, domain.Memory) (domain.Memory, error)
	Search(context.Context, domain.MemorySearchQuery) ([]memory.Result, error)
	Archive(context.Context, domain.MemoryID) (domain.Memory, error)
	Delete(context.Context, domain.MemoryID) error
}

// Dependencies are provided by the application's composition root.
type Dependencies struct {
	Coordinator      TaskCoordinator
	Sessions         SessionManager
	Events           ports.EventPublisher
	ExecutionContext context.Context
	Providers        ProviderCatalog
	Memories         MemoryManager
}

// Server owns HTTP routing and timeouts but not application dependencies.
type Server struct {
	httpServer *http.Server
	handler    *Handler
}

// NewServer wires handlers to injected application ports. WriteTimeout is
// intentionally left unset: it applies to a response's entire lifetime and
// would terminate long-lived SSE streams.
func NewServer(config Config, dependencies Dependencies) (*Server, error) {
	if dependencies.Coordinator == nil || dependencies.Sessions == nil ||
		dependencies.Events == nil || dependencies.ExecutionContext == nil {
		return nil, fmt.Errorf("%w: coordinator, session manager, event publisher, and execution context are required", ErrInvalidDependency)
	}
	if config.Address == "" {
		config.Address = defaultAddress
	}
	if config.ReadHeaderTimeout < 0 || config.ReadTimeout < 0 || config.IdleTimeout < 0 || config.MaxRequestBodySize < 0 {
		return nil, fmt.Errorf("%w: HTTP timeouts and request limit must not be negative", ErrInvalidConfig)
	}
	if config.ReadHeaderTimeout == 0 {
		config.ReadHeaderTimeout = defaultReadHeader
	}
	if config.ReadTimeout == 0 {
		config.ReadTimeout = defaultReadTimeout
	}
	if config.IdleTimeout == 0 {
		config.IdleTimeout = defaultIdleTimeout
	}
	if config.MaxRequestBodySize == 0 {
		config.MaxRequestBodySize = defaultMaxRequestBytes
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	handler := newHandler(config.MaxRequestBodySize, dependencies, config.Logger)
	return &Server{
		handler: handler,
		httpServer: &http.Server{
			Addr:              config.Address,
			Handler:           handler.routes(),
			ReadHeaderTimeout: config.ReadHeaderTimeout,
			ReadTimeout:       config.ReadTimeout,
			IdleTimeout:       config.IdleTimeout,
		},
	}, nil
}

// Handler exposes the configured handler for embedding and httptest use.
func (s *Server) Handler() http.Handler {
	if s == nil || s.httpServer == nil {
		return http.NotFoundHandler()
	}
	return s.httpServer.Handler
}

// ListenAndServe starts the configured HTTP listener.
func (s *Server) ListenAndServe() error {
	if s == nil || s.httpServer == nil {
		return ErrInvalidDependency
	}
	return s.httpServer.ListenAndServe()
}

// Serve accepts an already-created listener, primarily for controlled hosting
// and tests.
func (s *Server) Serve(listener net.Listener) error {
	if s == nil || s.httpServer == nil {
		return ErrInvalidDependency
	}
	if listener == nil {
		return errors.New("HTTP listener must not be nil")
	}
	return s.httpServer.Serve(listener)
}

// Shutdown stops accepting connections and waits for active requests. If the
// deadline expires, it force-closes active connections so SSE contexts end.
func (s *Server) Shutdown(ctx context.Context) error {
	if s == nil || s.httpServer == nil {
		return ErrInvalidDependency
	}
	if ctx == nil {
		return errors.New("HTTP shutdown context must not be nil")
	}
	err := s.httpServer.Shutdown(ctx)
	if err != nil {
		closeErr := s.httpServer.Close()
		return errors.Join(fmt.Errorf("gracefully shut down HTTP server: %w", err), closeErr)
	}
	return nil
}
