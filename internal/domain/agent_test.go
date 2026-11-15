package domain

import (
	"errors"
	"testing"
)

func TestAgentProviderValidate(t *testing.T) {
	for _, provider := range SupportedProviders() {
		if err := provider.Validate(); err != nil {
			t.Errorf("Validate(%q) error = %v", provider, err)
		}
	}
	if err := AgentProvider("other").Validate(); !errors.Is(err, ErrUnsupportedProvider) {
		t.Fatalf("Validate() error = %v, want ErrUnsupportedProvider", err)
	}
}

func TestSupportedProvidersReturnsIndependentSlice(t *testing.T) {
	providers := SupportedProviders()
	providers[0] = "changed"
	if err := SupportedProviders()[0].Validate(); err != nil {
		t.Fatalf("mutating returned slice changed provider definitions: %v", err)
	}
}
