package domain

import "fmt"

// AgentProvider identifies an agent execution provider without exposing its SDK.
type AgentProvider string

const (
	ProviderCodex    AgentProvider = "codex"
	ProviderOpenCode AgentProvider = "opencode"
	ProviderHermes   AgentProvider = "hermes"
	ProviderOpenClaw AgentProvider = "openclaw"
)

// SupportedProviders returns the provider identifiers recognized by the domain.
func SupportedProviders() []AgentProvider {
	return []AgentProvider{
		ProviderCodex,
		ProviderOpenCode,
		ProviderHermes,
		ProviderOpenClaw,
	}
}

// Validate reports whether p is one of the providers selectable by the system.
func (p AgentProvider) Validate() error {
	switch p {
	case ProviderCodex, ProviderOpenCode, ProviderHermes, ProviderOpenClaw:
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrUnsupportedProvider, p)
	}
}

// AgentCapabilities describes provider-neutral execution features.
type AgentCapabilities struct {
	Streaming            bool `json:"streaming"`
	Resume               bool `json:"resume"`
	InteractiveExecution bool `json:"interactive_execution"`
	ToolEvents           bool `json:"tool_events"`
	Interruption         bool `json:"interruption"`
}
