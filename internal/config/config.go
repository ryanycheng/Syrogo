package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Routing  RoutingConfig  `yaml:"routing"`
	Provider []ProviderSpec `yaml:"providers"`
}

type ServerConfig struct {
	Listen string `yaml:"listen"`
}

type RoutingConfig struct {
	DefaultProvider   string            `yaml:"default_provider"`
	ModelProviders    map[string]string `yaml:"model_providers"`
	FallbackProviders []string          `yaml:"fallback_providers"`
}

type ProviderSpec struct {
	Name string `yaml:"name"`
	Type string `yaml:"type"`
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("unmarshal config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func (c Config) Validate() error {
	if c.Server.Listen == "" {
		return fmt.Errorf("server.listen is required")
	}
	if c.Routing.DefaultProvider == "" {
		return fmt.Errorf("routing.default_provider is required")
	}
	if len(c.Provider) == 0 {
		return fmt.Errorf("at least one provider is required")
	}

	providerNames := make(map[string]struct{}, len(c.Provider))
	for _, spec := range c.Provider {
		providerNames[spec.Name] = struct{}{}
	}
	if _, ok := providerNames[c.Routing.DefaultProvider]; !ok {
		return fmt.Errorf("routing.default_provider %q not found in providers", c.Routing.DefaultProvider)
	}
	for _, name := range c.Routing.FallbackProviders {
		if _, ok := providerNames[name]; !ok {
			return fmt.Errorf("routing.fallback_providers contains unknown provider %q", name)
		}
	}

	return nil
}
