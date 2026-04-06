package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server    ServerConfig   `yaml:"server"`
	Listeners []ListenerSpec `yaml:"listeners"`
	Inbounds  []InboundSpec  `yaml:"inbounds"`
	Routing   RoutingConfig  `yaml:"routing"`
	Outbound  []ProviderSpec `yaml:"outbounds"`
	Provider  []ProviderSpec `yaml:"providers"`
}

type ServerConfig struct {
	Listen string `yaml:"listen"`
}

type ListenerSpec struct {
	Name    string `yaml:"name"`
	Listen  string `yaml:"listen"`
	Inbound string `yaml:"inbound"`
}

type InboundSpec struct {
	Name   string            `yaml:"name"`
	Type   string            `yaml:"type"`
	Labels map[string]string `yaml:"labels"`
}

type RoutingConfig struct {
	DefaultProvider   string            `yaml:"default_provider"`
	DefaultOutbound   string            `yaml:"default_outbound"`
	ModelProviders    map[string]string `yaml:"model_providers"`
	ModelOutbounds    map[string]string `yaml:"model_outbounds"`
	InboundProviders  map[string]string `yaml:"inbound_providers"`
	InboundOutbounds  map[string]string `yaml:"inbound_outbounds"`
	FallbackProviders []string          `yaml:"fallback_providers"`
	FallbackOutbounds []string          `yaml:"fallback_outbounds"`
}

type ProviderSpec struct {
	Name    string   `yaml:"name"`
	Type    string   `yaml:"type"`
	BaseURL string   `yaml:"base_url"`
	APIKey  string   `yaml:"api_key"`
	APIKeys []string `yaml:"api_keys"`
	Models  []string `yaml:"models"`
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
	if len(c.ListenAddresses()) == 0 {
		return fmt.Errorf("server.listen or listeners is required")
	}
	if c.Routing.DefaultTarget() == "" {
		return fmt.Errorf("routing.default_provider or routing.default_outbound is required")
	}

	outbounds := c.OutboundSpecs()
	if len(outbounds) == 0 {
		return fmt.Errorf("at least one outbound is required")
	}

	inboundNames := make(map[string]struct{}, len(c.Inbounds))
	for _, inbound := range c.Inbounds {
		if inbound.Name == "" {
			return fmt.Errorf("inbounds.name is required")
		}
		if inbound.Type == "" {
			return fmt.Errorf("inbounds.%s.type is required", inbound.Name)
		}
		inboundNames[inbound.Name] = struct{}{}
	}

	if len(c.Listeners) > 0 {
		if len(c.Inbounds) == 0 {
			return fmt.Errorf("at least one inbound is required when listeners are configured")
		}
		for _, listener := range c.Listeners {
			if listener.Name == "" {
				return fmt.Errorf("listeners.name is required")
			}
			if listener.Listen == "" {
				return fmt.Errorf("listeners.%s.listen is required", listener.Name)
			}
			if listener.Inbound == "" {
				return fmt.Errorf("listeners.%s.inbound is required", listener.Name)
			}
			if _, ok := inboundNames[listener.Inbound]; !ok {
				return fmt.Errorf("listeners.%s.inbound %q not found in inbounds", listener.Name, listener.Inbound)
			}
		}
	}

	outboundNames := make(map[string]struct{}, len(outbounds))
	for _, spec := range outbounds {
		outboundNames[spec.Name] = struct{}{}
		switch spec.Type {
		case "mock":
		case "openai_compatible":
			if spec.BaseURL == "" {
				return fmt.Errorf("outbounds.%s.base_url is required", spec.Name)
			}
			if spec.APIKey == "" && len(spec.APIKeys) == 0 {
				return fmt.Errorf("outbounds.%s.api_key or api_keys is required", spec.Name)
			}
		default:
			return fmt.Errorf("outbounds.%s.type %q is unsupported", spec.Name, spec.Type)
		}
	}
	if _, ok := outboundNames[c.Routing.DefaultTarget()]; !ok {
		return fmt.Errorf("routing default target %q not found in outbounds", c.Routing.DefaultTarget())
	}
	for _, name := range c.Routing.FallbackTargets() {
		if _, ok := outboundNames[name]; !ok {
			return fmt.Errorf("routing fallback target %q not found in outbounds", name)
		}
	}
	for inbound, target := range c.Routing.InboundTargets() {
		if _, ok := inboundNames[inbound]; !ok {
			return fmt.Errorf("routing inbound target %q not found in inbounds", inbound)
		}
		if _, ok := outboundNames[target]; !ok {
			return fmt.Errorf("routing inbound %q target %q not found in outbounds", inbound, target)
		}
	}

	return nil
}

func (c Config) OutboundSpecs() []ProviderSpec {
	if len(c.Outbound) > 0 {
		return c.Outbound
	}
	return c.Provider
}

func (c Config) ListenAddress() string {
	addresses := c.ListenAddresses()
	if len(addresses) == 0 {
		return ""
	}
	return addresses[0]
}

func (c Config) ListenAddresses() []string {
	if len(c.Listeners) > 0 {
		addresses := make([]string, 0, len(c.Listeners))
		for _, listener := range c.Listeners {
			addresses = append(addresses, listener.Listen)
		}
		return addresses
	}
	if c.Server.Listen == "" {
		return nil
	}
	return []string{c.Server.Listen}
}

func (c Config) PrimaryInbound() InboundSpec {
	if len(c.Listeners) == 0 {
		return InboundSpec{}
	}
	return c.InboundByName(c.Listeners[0].Inbound)
}

func (c Config) InboundByName(name string) InboundSpec {
	for _, inbound := range c.Inbounds {
		if inbound.Name == name {
			return inbound
		}
	}
	return InboundSpec{}
}

func (r RoutingConfig) DefaultTarget() string {
	if r.DefaultOutbound != "" {
		return r.DefaultOutbound
	}
	return r.DefaultProvider
}

func (r RoutingConfig) ModelTargets() map[string]string {
	if len(r.ModelOutbounds) > 0 {
		return r.ModelOutbounds
	}
	return r.ModelProviders
}

func (r RoutingConfig) InboundTargets() map[string]string {
	if len(r.InboundOutbounds) > 0 {
		return r.InboundOutbounds
	}
	return r.InboundProviders
}

func (r RoutingConfig) FallbackTargets() []string {
	if len(r.FallbackOutbounds) > 0 {
		return r.FallbackOutbounds
	}
	return r.FallbackProviders
}
