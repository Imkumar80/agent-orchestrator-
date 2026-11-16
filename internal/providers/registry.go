// Package providers contains provider selection and registration logic.
package providers

import (
	"errors"
	"fmt"
	"sync"

	"github.com/Imkumar80/agent-orchestrator-/internal/domain"
	"github.com/Imkumar80/agent-orchestrator-/internal/ports"
)

var (
	ErrDuplicateProvider = errors.New("provider already registered")
	ErrProviderNotFound  = errors.New("provider not found")
	ErrNilProvider       = errors.New("provider is nil")
)

// Registry resolves a registered provider by its domain identifier.
type Registry struct {
	mu        sync.RWMutex
	providers map[domain.AgentProvider]ports.AgentProvider
}

// NewRegistry creates an empty provider registry.
func NewRegistry() *Registry {
	return &Registry{providers: make(map[domain.AgentProvider]ports.AgentProvider)}
}

// Register adds provider unless another implementation has the same ID.
func (r *Registry) Register(provider ports.AgentProvider) error {
	if r == nil {
		return errors.New("provider registry is nil")
	}
	if provider == nil {
		return ErrNilProvider
	}

	name := provider.Name()
	if err := name.Validate(); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.providers == nil {
		r.providers = make(map[domain.AgentProvider]ports.AgentProvider)
	}
	if _, exists := r.providers[name]; exists {
		return fmt.Errorf("%w: %q", ErrDuplicateProvider, name)
	}
	r.providers[name] = provider
	return nil
}

// Get returns the implementation registered for name.
func (r *Registry) Get(name domain.AgentProvider) (ports.AgentProvider, error) {
	if r == nil {
		return nil, fmt.Errorf("%w: %q", ErrProviderNotFound, name)
	}
	r.mu.RLock()
	provider, exists := r.providers[name]
	r.mu.RUnlock()
	if !exists {
		return nil, fmt.Errorf("%w: %q", ErrProviderNotFound, name)
	}
	return provider, nil
}
