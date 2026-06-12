package config

import (
	"fmt"
	"os"
	"time"

	"github.com/ryanycheng/Syrogo/internal/protocol"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server     ServerConfig     `yaml:"server"`
	Listeners  []ListenerSpec   `yaml:"listeners"`
	Inbounds   []InboundSpec    `yaml:"inbounds"`
	Routing    RoutingConfig    `yaml:"routing"`
	Outbounds  []OutboundSpec   `yaml:"outbounds"`
	Accounting AccountingConfig `yaml:"accounting"`
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
	Name  string            `yaml:"name"`
	Token string            `yaml:"token"`
	Tag   string            `yaml:"tag"`
	Quota ClientQuotaConfig `yaml:"quota"`
}

type ClientQuotaConfig struct {
	Enabled bool                `yaml:"enabled"`
	Windows []QuotaWindowConfig `yaml:"windows"`
}

type QuotaWindowConfig struct {
	Name        string        `yaml:"name"`
	Duration    DurationValue `yaml:"duration"`
	MaxRequests int           `yaml:"max_requests"`
}

type OutboundQuotaWindowConfig = QuotaWindowConfig

type InboundSpec struct {
	Name     string       `yaml:"name"`
	Protocol string       `yaml:"protocol"`
	Path     string       `yaml:"path"`
	Clients  []ClientSpec `yaml:"clients"`
}

type RoutingRule struct {
	Name        string         `yaml:"name"`
	FromTags    []string       `yaml:"from_tags"`
	ToTags      []string       `yaml:"to_tags"`
	Strategy    string         `yaml:"strategy"`
	Weights     map[string]int `yaml:"weights"`
	TargetModel string         `yaml:"target_model"`
}

type RoutingConfig struct {
	Rules []RoutingRule `yaml:"rules"`
}

type OutboundSpec struct {
	Name         string               `yaml:"name"`
	Protocol     string               `yaml:"protocol"`
	Endpoint     string               `yaml:"endpoint"`
	AuthToken    string               `yaml:"auth_token"`
	Tag          string               `yaml:"tag"`
	Capabilities OutboundCapabilities `yaml:"capabilities"`
	Quota        OutboundQuotaConfig  `yaml:"quota"`
}

type OutboundQuotaConfig struct {
	Enabled       bool                `yaml:"enabled"`
	Windows       []QuotaWindowConfig `yaml:"windows"`
	Cooldown      DurationValue       `yaml:"cooldown"`
	ProbeInterval DurationValue       `yaml:"probe_interval"`
}

type OutboundCapabilities struct {
	ResponsesPreviousResponseID     *bool  `yaml:"responses_previous_response_id"`
	ResponsesBuiltinTools           *bool  `yaml:"responses_builtin_tools"`
	ResponsesToolResultStatusError  *bool  `yaml:"responses_tool_result_status_error"`
	ResponsesAssistantHistoryNative *bool  `yaml:"responses_assistant_history_native"`
	UsageEstimation                 bool   `yaml:"usage_estimation"`
	UsageEstimationMode             string `yaml:"usage_estimation_mode"`
}

type DurationValue string

func (d DurationValue) Duration() time.Duration {
	if d == "" {
		return 0
	}
	parsed, err := time.ParseDuration(string(d))
	if err != nil {
		return 0
	}
	return parsed
}

type AccountingConfig struct {
	Enabled    bool                      `yaml:"enabled"`
	Backend    string                    `yaml:"backend"`
	ExposeHTTP bool                      `yaml:"expose_http"`
	AdminToken string                    `yaml:"admin_token"`
	LocalFile  AccountingLocalFileConfig `yaml:"local_file"`
}

type AccountingLocalFileConfig struct {
	Dir                   string        `yaml:"dir"`
	RotateMaxSizeMB       int           `yaml:"rotate_max_size_mb"`
	RetentionDays         int           `yaml:"retention_days"`
	SnapshotInterval      DurationValue `yaml:"snapshot_interval"`
	SnapshotRetentionDays int           `yaml:"snapshot_retention_days"`
	WriteBufferRecords    int           `yaml:"write_buffer_records"`
	FlushInterval         DurationValue `yaml:"flush_interval"`
	QueueSize             int           `yaml:"queue_size"`
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

	inputNames := make(map[string]struct{}, len(c.Inbounds))
	tokens := make(map[string]string)
	clientNames := make(map[string]string)
	for _, inbound := range c.Inbounds {
		if inbound.Name == "" {
			return fmt.Errorf("inbounds.name is required")
		}
		if inbound.Protocol == "" {
			return fmt.Errorf("inbounds.%s.protocol is required", inbound.Name)
		}
		if !protocol.IsSupportedInbound(inbound.Protocol) {
			return fmt.Errorf("inbounds.%s.protocol %q is unsupported", inbound.Name, inbound.Protocol)
		}
		if inbound.Path == "" {
			return fmt.Errorf("inbounds.%s.path is required", inbound.Name)
		}
		if len(inbound.Clients) == 0 {
			return fmt.Errorf("inbounds.%s.clients is required", inbound.Name)
		}
		for i, client := range inbound.Clients {
			if client.Name == "" {
				return fmt.Errorf("inbounds.%s.clients[%d].name is required", inbound.Name, i)
			}
			if client.Token == "" {
				return fmt.Errorf("inbounds.%s.clients[%d].token is required", inbound.Name, i)
			}
			if client.Tag == "" {
				return fmt.Errorf("inbounds.%s.clients[%d].tag is required", inbound.Name, i)
			}
			if owner, ok := tokens[client.Token]; ok {
				return fmt.Errorf("inbounds.%s.clients[%d].token duplicates token used by %s", inbound.Name, i, owner)
			}
			if owner, ok := clientNames[client.Name]; ok {
				return fmt.Errorf("inbounds.%s.clients[%d].name duplicates client name used by %s", inbound.Name, i, owner)
			}
			if err := validateClientQuota(inbound.Name, i, client); err != nil {
				return err
			}
			tokens[client.Token] = inbound.Name
			clientNames[client.Name] = inbound.Name
		}
		inputNames[inbound.Name] = struct{}{}
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
			if _, ok := inputNames[inboundName]; !ok {
				return fmt.Errorf("listeners.%s.inbound %q not found in inbounds", listener.Name, inboundName)
			}
		}
	}

	if c.Accounting.Enabled {
		if c.Accounting.Backend == "" {
			c.Accounting.Backend = "memory"
		}
		switch c.Accounting.Backend {
		case "memory":
		case "local_file":
			if err := validateAccountingLocalFile(c.Accounting.LocalFile); err != nil {
				return err
			}
		default:
			return fmt.Errorf("accounting.backend %q is unsupported", c.Accounting.Backend)
		}
		if c.Accounting.ExposeHTTP && c.Accounting.AdminToken == "" {
			return fmt.Errorf("accounting.admin_token is required when accounting.expose_http is enabled")
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
		if !protocol.IsSupportedOutbound(outbound.Protocol) {
			return fmt.Errorf("outbounds.%s.protocol %q is unsupported", outbound.Name, outbound.Protocol)
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
		}
		switch outbound.Protocol {
		case "openai_responses":
		case "openai_chat", "anthropic_messages":
			if outbound.Capabilities.ResponsesPreviousResponseID != nil || outbound.Capabilities.ResponsesBuiltinTools != nil || outbound.Capabilities.ResponsesToolResultStatusError != nil || outbound.Capabilities.ResponsesAssistantHistoryNative != nil {
				return fmt.Errorf("outbounds.%s.responses capabilities are only supported for openai_responses", outbound.Name)
			}
		case "mock":
			if outbound.Capabilities != (OutboundCapabilities{}) {
				return fmt.Errorf("outbounds.%s.capabilities is unsupported for mock", outbound.Name)
			}
		}
		if outbound.Capabilities.UsageEstimationMode != "" && !outbound.Capabilities.UsageEstimation {
			return fmt.Errorf("outbounds.%s.usage_estimation_mode requires usage_estimation=true", outbound.Name)
		}
		if outbound.Capabilities.UsageEstimation {
			if outbound.Protocol != "openai_chat" && outbound.Protocol != "anthropic_messages" {
				return fmt.Errorf("outbounds.%s.usage_estimation is only supported for openai_chat and anthropic_messages", outbound.Name)
			}
			if outbound.Capabilities.UsageEstimationMode == "" {
				return fmt.Errorf("outbounds.%s.usage_estimation_mode is required when usage_estimation is enabled", outbound.Name)
			}
			if outbound.Capabilities.UsageEstimationMode != "heuristic" {
				return fmt.Errorf("outbounds.%s.usage_estimation_mode %q is unsupported", outbound.Name, outbound.Capabilities.UsageEstimationMode)
			}
		}
		if err := validateOutboundQuota(outbound); err != nil {
			return err
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
		if rule.Strategy != "failover" && rule.Strategy != "round_robin" && rule.Strategy != "weighted_round_robin" {
			return fmt.Errorf("routing.rules[%d].strategy %q is unsupported", i, rule.Strategy)
		}
		for _, tag := range rule.ToTags {
			if _, ok := outboundTags[tag]; !ok {
				return fmt.Errorf("routing.rules[%d].to_tags %q not found in outbounds", i, tag)
			}
		}
		if rule.Strategy == "weighted_round_robin" {
			if len(rule.Weights) == 0 {
				return fmt.Errorf("routing.rules[%d].weights is required when strategy=weighted_round_robin", i)
			}
			declared := make(map[string]struct{}, len(rule.ToTags))
			for _, tag := range rule.ToTags {
				declared[tag] = struct{}{}
				weight, ok := rule.Weights[tag]
				if !ok {
					return fmt.Errorf("routing.rules[%d].weights.%s is required", i, tag)
				}
				if weight <= 0 {
					return fmt.Errorf("routing.rules[%d].weights.%s must be greater than 0", i, tag)
				}
			}
			for tag := range rule.Weights {
				if _, ok := declared[tag]; !ok {
					return fmt.Errorf("routing.rules[%d].weights.%s does not match any to_tags entry", i, tag)
				}
			}
		} else if len(rule.Weights) > 0 {
			return fmt.Errorf("routing.rules[%d].weights is only supported when strategy=weighted_round_robin", i)
		}
	}

	_ = outboundNames
	return nil
}

func validateClientQuota(inboundName string, index int, client ClientSpec) error {
	quota := client.Quota
	if !quota.Enabled {
		return nil
	}
	return validateQuotaWindows(fmt.Sprintf("inbounds.%s.clients[%d].quota", inboundName, index), quota.Windows)
}

func validateOutboundQuota(outbound OutboundSpec) error {
	quota := outbound.Quota
	if !quota.Enabled {
		return nil
	}
	if err := validateQuotaWindows(fmt.Sprintf("outbounds.%s.quota", outbound.Name), quota.Windows); err != nil {
		return err
	}
	if quota.Cooldown.Duration() <= 0 {
		return fmt.Errorf("outbounds.%s.quota.cooldown must be a positive duration", outbound.Name)
	}
	if quota.ProbeInterval.Duration() <= 0 {
		return fmt.Errorf("outbounds.%s.quota.probe_interval must be a positive duration", outbound.Name)
	}
	return nil
}

func validateQuotaWindows(prefix string, windows []QuotaWindowConfig) error {
	if len(windows) == 0 {
		return fmt.Errorf("%s.windows is required when quota is enabled", prefix)
	}
	seen := make(map[string]struct{}, len(windows))
	for i, window := range windows {
		if window.Name == "" {
			return fmt.Errorf("%s.windows[%d].name is required", prefix, i)
		}
		if _, ok := seen[window.Name]; ok {
			return fmt.Errorf("%s.windows[%d].name duplicates %q", prefix, i, window.Name)
		}
		seen[window.Name] = struct{}{}
		if window.Duration.Duration() <= 0 {
			return fmt.Errorf("%s.windows[%d].duration must be a positive duration", prefix, i)
		}
		if window.MaxRequests <= 0 {
			return fmt.Errorf("%s.windows[%d].max_requests must be greater than 0", prefix, i)
		}
	}
	return nil
}

func validateAccountingLocalFile(cfg AccountingLocalFileConfig) error {
	if cfg.Dir == "" {
		return fmt.Errorf("accounting.local_file.dir is required when accounting.backend=local_file")
	}
	if cfg.RotateMaxSizeMB <= 0 {
		return fmt.Errorf("accounting.local_file.rotate_max_size_mb must be greater than 0")
	}
	if cfg.RetentionDays < 0 {
		return fmt.Errorf("accounting.local_file.retention_days must be greater than or equal to 0")
	}
	if cfg.SnapshotRetentionDays < 0 {
		return fmt.Errorf("accounting.local_file.snapshot_retention_days must be greater than or equal to 0")
	}
	if cfg.WriteBufferRecords <= 0 {
		return fmt.Errorf("accounting.local_file.write_buffer_records must be greater than 0")
	}
	if cfg.QueueSize <= 0 {
		return fmt.Errorf("accounting.local_file.queue_size must be greater than 0")
	}
	if cfg.FlushInterval.Duration() <= 0 {
		return fmt.Errorf("accounting.local_file.flush_interval must be a positive duration")
	}
	if cfg.SnapshotInterval != "" && cfg.SnapshotInterval.Duration() <= 0 {
		return fmt.Errorf("accounting.local_file.snapshot_interval must be a positive duration")
	}
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
