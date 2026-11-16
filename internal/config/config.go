// Package config loads the server's environment-based configuration.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Imkumar80/agent-orchestrator-/internal/domain"
)

const (
	EnvHTTPAddress               = "ORCHESTRATOR_HTTP_ADDR"
	EnvDatabasePath              = "ORCHESTRATOR_SQLITE_PATH"
	EnvEnabledProviders          = "ORCHESTRATOR_ENABLED_PROVIDERS"
	EnvCodexExecutable           = "CODEX_EXECUTABLE"
	EnvShutdownTimeout           = "ORCHESTRATOR_SHUTDOWN_TIMEOUT"
	EnvMemoryEnabled             = "ORCHESTRATOR_MEMORY_ENABLED"
	EnvMemoryContextBytes        = "ORCHESTRATOR_MEMORY_CONTEXT_MAX_BYTES"
	DefaultHTTPAddress           = "127.0.0.1:8080"
	DefaultDatabasePath          = "./data/orchestrator.db"
	DefaultEnabledAgents         = "codex"
	DefaultShutdownWindow        = 15 * time.Second
	DefaultMemoryContextMaxBytes = 4000
)

// Config contains only deployment-level application settings.
type Config struct {
	HTTPAddress           string
	SQLitePath            string
	EnabledProviders      []domain.AgentProvider
	CodexExecutable       string
	GracefulShutdown      time.Duration
	MemoryEnabled         bool
	MemoryContextMaxBytes int
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
		HTTPAddress:           get(EnvHTTPAddress, DefaultHTTPAddress),
		SQLitePath:            get(EnvDatabasePath, DefaultDatabasePath),
		CodexExecutable:       get(EnvCodexExecutable, ""),
		GracefulShutdown:      DefaultShutdownWindow,
		MemoryEnabled:         true,
		MemoryContextMaxBytes: DefaultMemoryContextMaxBytes,
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
	if value, ok := lookup(EnvMemoryEnabled); ok {
		enabled, err := strconv.ParseBool(strings.TrimSpace(value))
		if err != nil {
			return Config{}, fmt.Errorf("%s must be true or false", EnvMemoryEnabled)
		}
		config.MemoryEnabled = enabled
	}
	if value, ok := lookup(EnvMemoryContextBytes); ok {
		limit, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || limit <= 0 {
			return Config{}, fmt.Errorf("%s must be a positive integer", EnvMemoryContextBytes)
		}
		config.MemoryContextMaxBytes = limit
	}
	return config, nil
}
