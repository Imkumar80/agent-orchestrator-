// Package config loads the server's environment-based configuration.
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Imkumar80/agent-orchestrator-/internal/domain"
)

const (
	EnvHTTPAddress        = "ORCHESTRATOR_HTTP_ADDR"
	EnvDatabasePath       = "ORCHESTRATOR_SQLITE_PATH"
	EnvEnabledProviders   = "ORCHESTRATOR_ENABLED_PROVIDERS"
	EnvCodexExecutable    = "CODEX_EXECUTABLE"
	EnvShutdownTimeout    = "ORCHESTRATOR_SHUTDOWN_TIMEOUT"
	DefaultHTTPAddress    = "127.0.0.1:8080"
	DefaultDatabasePath   = "./data/orchestrator.db"
	DefaultEnabledAgents  = "codex"
	DefaultShutdownWindow = 15 * time.Second
)

// Config contains only deployment-level application settings.
type Config struct {
	HTTPAddress      string
	SQLitePath       string
	EnabledProviders []domain.AgentProvider
	CodexExecutable  string
	GracefulShutdown time.Duration
}

// Load reads configuration from the process environment.
func Load() (Config, error) {
	return LoadFromLookup(os.LookupEnv)
}

// LoadFromLookup parses environment settings with an injectable lookup for
// deterministic tests.
func LoadFromLookup(lookup func(string) (string, bool)) (Config, error) {
	if lookup == nil {
		return Config{}, errors.New("configuration environment lookup is nil")
	}
	get := func(key, fallback string) string {
		value, ok := lookup(key)
		if !ok {
			return fallback
		}
		return strings.TrimSpace(value)
	}
	config := Config{
		HTTPAddress:      get(EnvHTTPAddress, DefaultHTTPAddress),
		SQLitePath:       get(EnvDatabasePath, DefaultDatabasePath),
		CodexExecutable:  get(EnvCodexExecutable, ""),
		GracefulShutdown: DefaultShutdownWindow,
	}
	if config.HTTPAddress == "" {
		return Config{}, fmt.Errorf("%s must not be empty", EnvHTTPAddress)
	}
	if config.SQLitePath == "" {
		return Config{}, fmt.Errorf("%s must not be empty", EnvDatabasePath)
	}
	providersValue := get(EnvEnabledProviders, DefaultEnabledAgents)
	if providersValue == "" {
		return Config{}, fmt.Errorf("%s must contain at least one provider", EnvEnabledProviders)
	}
	seen := make(map[domain.AgentProvider]struct{})
	for _, item := range strings.Split(providersValue, ",") {
		provider := domain.AgentProvider(strings.TrimSpace(item))
		if err := provider.Validate(); err != nil {
			return Config{}, fmt.Errorf("parse %s: %w", EnvEnabledProviders, err)
		}
		if _, exists := seen[provider]; exists {
			return Config{}, fmt.Errorf("%s contains duplicate provider %q", EnvEnabledProviders, provider)
		}
		seen[provider] = struct{}{}
		config.EnabledProviders = append(config.EnabledProviders, provider)
	}
	if value, ok := lookup(EnvShutdownTimeout); ok {
		parsed, err := time.ParseDuration(strings.TrimSpace(value))
		if err != nil || parsed <= 0 {
			return Config{}, fmt.Errorf("%s must be a positive duration", EnvShutdownTimeout)
		}
		config.GracefulShutdown = parsed
	}
	return config, nil
}
