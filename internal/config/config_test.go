package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigValidateSuccess(t *testing.T) {
	cfg := Config{
		Server: ServerConfig{Listen: ":8080"},
		Routing: RoutingConfig{
			DefaultProvider:   "mock",
			FallbackProviders: []string{"backup"},
		},
		Provider: []ProviderSpec{{Name: "mock", Type: "mock"}, {Name: "backup", Type: "mock"}},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConfigValidateMissingListen(t *testing.T) {
	cfg := Config{
		Routing:  RoutingConfig{DefaultProvider: "mock"},
		Provider: []ProviderSpec{{Name: "mock", Type: "mock"}},
	}

	err := cfg.Validate()
	if err == nil || err.Error() != "server.listen is required" {
		t.Fatalf("Validate() error = %v, want server.listen is required", err)
	}
}

func TestConfigValidateMissingDefaultProvider(t *testing.T) {
	cfg := Config{
		Server:   ServerConfig{Listen: ":8080"},
		Provider: []ProviderSpec{{Name: "mock", Type: "mock"}},
	}

	err := cfg.Validate()
	if err == nil || err.Error() != "routing.default_provider is required" {
		t.Fatalf("Validate() error = %v, want routing.default_provider is required", err)
	}
}

func TestConfigValidateEmptyProviders(t *testing.T) {
	cfg := Config{
		Server:  ServerConfig{Listen: ":8080"},
		Routing: RoutingConfig{DefaultProvider: "mock"},
	}

	err := cfg.Validate()
	if err == nil || err.Error() != "at least one provider is required" {
		t.Fatalf("Validate() error = %v, want at least one provider is required", err)
	}
}

func TestConfigValidateDefaultProviderNotFound(t *testing.T) {
	cfg := Config{
		Server:   ServerConfig{Listen: ":8080"},
		Routing:  RoutingConfig{DefaultProvider: "missing"},
		Provider: []ProviderSpec{{Name: "mock", Type: "mock"}},
	}

	err := cfg.Validate()
	if err == nil || err.Error() != "routing.default_provider \"missing\" not found in providers" {
		t.Fatalf("Validate() error = %v, want missing default provider error", err)
	}
}

func TestConfigValidateFallbackProviderNotFound(t *testing.T) {
	cfg := Config{
		Server: ServerConfig{Listen: ":8080"},
		Routing: RoutingConfig{
			DefaultProvider:   "mock",
			FallbackProviders: []string{"backup"},
		},
		Provider: []ProviderSpec{{Name: "mock", Type: "mock"}},
	}

	err := cfg.Validate()
	if err == nil || err.Error() != "routing.fallback_providers contains unknown provider \"backup\"" {
		t.Fatalf("Validate() error = %v, want unknown fallback provider error", err)
	}
}

func TestLoadSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte("server:\n  listen: \":8080\"\nrouting:\n  default_provider: \"mock\"\n  fallback_providers:\n    - \"backup\"\n  model_providers:\n    default: \"mock\"\nproviders:\n  - name: \"mock\"\n    type: \"mock\"\n  - name: \"backup\"\n    type: \"mock\"\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.Listen != ":8080" {
		t.Fatalf("Server.Listen = %q, want :8080", cfg.Server.Listen)
	}
	if cfg.Routing.DefaultProvider != "mock" {
		t.Fatalf("Routing.DefaultProvider = %q, want mock", cfg.Routing.DefaultProvider)
	}
	if len(cfg.Routing.FallbackProviders) != 1 || cfg.Routing.FallbackProviders[0] != "backup" {
		t.Fatalf("Routing.FallbackProviders = %#v, want single backup fallback", cfg.Routing.FallbackProviders)
	}
	if got := cfg.Routing.ModelProviders["default"]; got != "mock" {
		t.Fatalf("Routing.ModelProviders[default] = %q, want mock", got)
	}
	if len(cfg.Provider) != 2 {
		t.Fatalf("len(Provider) = %d, want 2", len(cfg.Provider))
	}
}

func TestLoadFileNotFound(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err == nil {
		t.Fatal("Load() error = nil, want error")
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("server: ["), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want error")
	}
}

func TestLoadInvalidConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte("server:\n  listen: \"\"\nrouting:\n  default_provider: \"mock\"\nproviders:\n  - name: \"mock\"\n    type: \"mock\"\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := Load(path)
	if err == nil || err.Error() != "server.listen is required" {
		t.Fatalf("Load() error = %v, want server.listen is required", err)
	}
}
