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
	Outbounds []OutboundSpec `yaml:"outbounds"`
}

type ServerConfig struct {
	Listen string `yaml:"listen"`
}

type ListenerSpec struct {
	Name     string   `yaml:"name"`
	Listen   string   `yaml:"listen"`
	Inbounds []string `yaml:"inbounds"`
}

type ClientSpec struct {
	Token string `yaml:"token"`
	Tag   string `yaml:"tag"`
}

type InboundSpec struct {
	Name     string       `yaml:"name"`
	Protocol string       `yaml:"protocol"`
	Path     string       `yaml:"path"`
	Clients  []ClientSpec `yaml:"clients"`
}

type RoutingRule struct {
	Name        string   `yaml:"name"`
	FromTags    []string `yaml:"from_tags"`
	ToTags      []string `yaml:"to_tags"`
	Strategy    string   `yaml:"strategy"`
	TargetModel string   `yaml:"target_model"`
}

type RoutingConfig struct {
	Rules []RoutingRule `yaml:"rules"`
}

type OutboundSpec struct {
	Name      string `yaml:"name"`
	Protocol  string `yaml:"protocol"`
	Endpoint  string `yaml:"endpoint"`
	AuthToken string `yaml:"auth_token"`
	Tag       string `yaml:"tag"`
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
	if len(c.Outbounds) == 0 {
		return fmt.Errorf("at least one outbound is required")
	}
	if len(c.Routing.Rules) == 0 {
		return fmt.Errorf("at least one routing rule is required")
	}

	inboundNames := make(map[string]struct{}, len(c.Inbounds))
	tokens := make(map[string]string)
	for _, inbound := range c.Inbounds {
		if inbound.Name == "" {
			return fmt.Errorf("inbounds.name is required")
		}
		if inbound.Protocol == "" {
			return fmt.Errorf("inbounds.%s.protocol is required", inbound.Name)
		}
		if inbound.Path == "" {
			return fmt.Errorf("inbounds.%s.path is required", inbound.Name)
		}
		if len(inbound.Clients) == 0 {
			return fmt.Errorf("inbounds.%s.clients is required", inbound.Name)
		}
		for i, client := range inbound.Clients {
			if client.Token == "" {
				return fmt.Errorf("inbounds.%s.clients[%d].token is required", inbound.Name, i)
			}
			if client.Tag == "" {
				return fmt.Errorf("inbounds.%s.clients[%d].tag is required", inbound.Name, i)
			}
			if owner, ok := tokens[client.Token]; ok {
				return fmt.Errorf("inbounds.%s.clients[%d].token duplicates token used by %s", inbound.Name, i, owner)
			}
			tokens[client.Token] = inbound.Name
		}
		inboundNames[inbound.Name] = struct{}{}
	}

	if len(c.Listeners) > 0 && len(c.Inbounds) == 0 {
		return fmt.Errorf("at least one inbound is required when listeners are configured")
	}
	for _, listener := range c.Listeners {
		if listener.Name == "" {
			return fmt.Errorf("listeners.name is required")
		}
		if listener.Listen == "" {
			return fmt.Errorf("listeners.%s.listen is required", listener.Name)
		}
		if len(listener.Inbounds) == 0 {
			return fmt.Errorf("listeners.%s.inbounds is required", listener.Name)
		}
		for _, inboundName := range listener.Inbounds {
			if _, ok := inboundNames[inboundName]; !ok {
				return fmt.Errorf("listeners.%s.inbound %q not found in inbounds", listener.Name, inboundName)
			}
		}
	}

	outboundNames := make(map[string]struct{}, len(c.Outbounds))
	outboundTags := make(map[string]struct{}, len(c.Outbounds))
	for _, outbound := range c.Outbounds {
		if outbound.Name == "" {
			return fmt.Errorf("outbounds.name is required")
		}
		if outbound.Protocol == "" {
			return fmt.Errorf("outbounds.%s.protocol is required", outbound.Name)
		}
		if outbound.Tag == "" {
			return fmt.Errorf("outbounds.%s.tag is required", outbound.Name)
		}
		switch outbound.Protocol {
		case "mock":
		case "openai_chat", "openai_responses", "anthropic_messages":
			if outbound.Endpoint == "" {
				return fmt.Errorf("outbounds.%s.endpoint is required", outbound.Name)
			}
			if outbound.AuthToken == "" {
				return fmt.Errorf("outbounds.%s.auth_token is required", outbound.Name)
			}
		default:
			return fmt.Errorf("outbounds.%s.protocol %q is unsupported", outbound.Name, outbound.Protocol)
		}
		outboundNames[outbound.Name] = struct{}{}
		outboundTags[outbound.Tag] = struct{}{}
	}

	for i, rule := range c.Routing.Rules {
		if len(rule.FromTags) == 0 {
			return fmt.Errorf("routing.rules[%d].from_tags is required", i)
		}
		if len(rule.ToTags) == 0 {
			return fmt.Errorf("routing.rules[%d].to_tags is required", i)
		}
		if rule.Strategy == "" {
			return fmt.Errorf("routing.rules[%d].strategy is required", i)
		}
		if rule.Strategy != "failover" && rule.Strategy != "round_robin" {
			return fmt.Errorf("routing.rules[%d].strategy %q is unsupported", i, rule.Strategy)
		}
		for _, tag := range rule.ToTags {
			if _, ok := outboundTags[tag]; !ok {
				return fmt.Errorf("routing.rules[%d].to_tags %q not found in outbounds", i, tag)
			}
		}
	}

	_ = outboundNames
	return nil
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

func (c Config) InboundByName(name string) InboundSpec {
	for _, inbound := range c.Inbounds {
		if inbound.Name == name {
			return inbound
		}
	}
	return InboundSpec{}
}

func (c Config) ListenerInbounds(listener ListenerSpec) []InboundSpec {
	inbounds := make([]InboundSpec, 0, len(listener.Inbounds))
	for _, name := range listener.Inbounds {
		inbound := c.InboundByName(name)
		if inbound.Name != "" {
			inbounds = append(inbounds, inbound)
		}
	}
	return inbounds
}
