package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/Imkumar80/agent-orchestrator-/internal/domain"
)

func TestNewServerRequiresInjectedDependencies(t *testing.T) {
	if _, err := NewServer(Config{}, Dependencies{}); !errors.Is(err, ErrInvalidDependency) {
		t.Fatalf("NewServer() error = %v, want ErrInvalidDependency", err)
	}
	coordinator := &fakeCoordinator{}
	sessions := &fakeSessions{values: make(map[domain.SessionID]domain.Session)}
	publisher := newFakeEvents()
	if _, err := NewServer(Config{ReadTimeout: -time.Second}, Dependencies{
		Coordinator: mustCoordinator(t, coordinator), Sessions: sessions, Events: publisher, ExecutionContext: context.Background(),
	}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("negative timeout error = %v, want ErrInvalidConfig", err)
	}
}

func TestServerListenAndGracefulShutdown(t *testing.T) {
	_, coordinator, sessions, publisher := newTestHandler(t)
	server, err := NewServer(Config{Address: "127.0.0.1:0"}, Dependencies{
		Coordinator: mustCoordinator(t, coordinator), Sessions: sessions, Events: publisher, ExecutionContext: context.Background(),
	})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	response, err := http.Get("http://" + listener.Addr().String() + "/health")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown(): %v", err)
	}
	select {
	case err := <-serveDone:
		if !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("Serve() error = %v, want http.ErrServerClosed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("HTTP server did not stop after Shutdown()")
	}
}
