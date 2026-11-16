package config

import (
	"testing"
	"time"
)

func TestLoadFromLookupDefaults(t *testing.T) {
	config, err := LoadFromLookup(func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	if config.HTTPAddress != DefaultHTTPAddress || config.SQLitePath != DefaultDatabasePath ||
		len(config.EnabledProviders) != 1 || config.EnabledProviders[0] != "codex" || config.GracefulShutdown != DefaultShutdownWindow {
		t.Fatalf("unexpected defaults: %#v", config)
	}
}

func TestLoadFromLookupEnvironmentOverrides(t *testing.T) {
	values := map[string]string{
		EnvHTTPAddress:      "localhost:9090",
		EnvDatabasePath:     "custom/store.db",
		EnvEnabledProviders: "codex",
		EnvCodexExecutable:  "C:/tools/codex.exe",
		EnvShutdownTimeout:  "30s",
	}
	config, err := LoadFromLookup(func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.HTTPAddress != "localhost:9090" || config.SQLitePath != "custom/store.db" ||
		config.CodexExecutable != "C:/tools/codex.exe" || config.GracefulShutdown != 30*time.Second {
		t.Fatalf("environment override not applied: %#v", config)
	}
}

func TestLoadFromLookupRejectsInvalidConfiguration(t *testing.T) {
	for name, values := range map[string]map[string]string{
		"empty provider list": {EnvEnabledProviders: ""},
		"unknown provider":    {EnvEnabledProviders: "codex,other"},
		"duplicate provider":  {EnvEnabledProviders: "codex,codex"},
		"invalid shutdown":    {EnvShutdownTimeout: "soon"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := LoadFromLookup(func(key string) (string, bool) {
				value, ok := values[key]
				return value, ok
			})
			if err == nil {
				t.Fatal("LoadFromLookup() error = nil")
			}
		})
	}
}
