package providers

import (
	"context"
	"errors"
	"testing"

	"github.com/Imkumar80/agent-orchestrator-/internal/domain"
	"github.com/Imkumar80/agent-orchestrator-/internal/ports"
)

var _ ports.AgentProvider = (*mockProvider)(nil)

type mockProvider struct {
	name domain.AgentProvider
}

func (m *mockProvider) Name() domain.AgentProvider {
	return m.name
}

func (m *mockProvider) Capabilities() domain.AgentCapabilities {
	return domain.AgentCapabilities{Streaming: true}
}

func (m *mockProvider) Start(context.Context, ports.StartRequest) (ports.SessionRef, error) {
	return ports.SessionRef{}, nil
}

func (m *mockProvider) Send(context.Context, ports.SessionRef, ports.AgentInput) error {
	return nil
}

func (m *mockProvider) Subscribe(context.Context, ports.SessionRef) (<-chan domain.AgentEvent, error) {
	return nil, nil
}

func (m *mockProvider) Interrupt(context.Context, ports.SessionRef) error {
	return nil
}

func (m *mockProvider) Resume(_ context.Context, session ports.SessionRef) (ports.SessionRef, error) {
	return session, nil
}

func (m *mockProvider) Close(context.Context, ports.SessionRef) error {
	return nil
}

func TestRegistryRegisterAndGet(t *testing.T) {
	registry := NewRegistry()
	want := &mockProvider{name: domain.ProviderCodex}
	if err := registry.Register(want); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	got, err := registry.Get(domain.ProviderCodex)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got != want {
		t.Fatalf("Get() = %v, want registered provider %v", got, want)
	}
}

func TestRegistryRejectsDuplicateProvider(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(&mockProvider{name: domain.ProviderCodex}); err != nil {
		t.Fatalf("first Register() error = %v", err)
	}
	if err := registry.Register(&mockProvider{name: domain.ProviderCodex}); !errors.Is(err, ErrDuplicateProvider) {
		t.Fatalf("second Register() error = %v, want ErrDuplicateProvider", err)
	}
}

func TestRegistryGetMissingProvider(t *testing.T) {
	registry := NewRegistry()
	_, err := registry.Get(domain.ProviderOpenCode)
	if !errors.Is(err, ErrProviderNotFound) {
		t.Fatalf("Get() error = %v, want ErrProviderNotFound", err)
	}
}

func TestRegistryRejectsUnsupportedProvider(t *testing.T) {
	registry := NewRegistry()
	err := registry.Register(&mockProvider{name: "unknown"})
	if !errors.Is(err, domain.ErrUnsupportedProvider) {
		t.Fatalf("Register() error = %v, want domain.ErrUnsupportedProvider", err)
	}
}

func TestRegistryRejectsNilProvider(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(nil); !errors.Is(err, ErrNilProvider) {
		t.Fatalf("Register() error = %v, want ErrNilProvider", err)
	}
}
