package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
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
	Governance GovernanceConfig `yaml:"governance"`
	Admin      AdminConfig      `yaml:"admin"`
}

type AdminConfig struct {
	Enabled bool            `yaml:"enabled"`
	Token   string          `yaml:"token"`
	Logs    AdminLogsConfig `yaml:"logs"`
}

type AdminLogsConfig struct {
	Enabled  bool                    `yaml:"enabled"`
	Path     string                  `yaml:"path"`
	MaxBytes int                     `yaml:"max_bytes"`
	Rotation AdminLogsRotationConfig `yaml:"rotation"`
}

type AdminLogsRotationConfig struct {
	MaxSizeMB      int   `yaml:"max_size_mb"`
	MaxFiles       int   `yaml:"max_files"`
	MaxAgeDays     int   `yaml:"max_age_days"`
	MaxTotalSizeMB int   `yaml:"max_total_size_mb"`
	Compress       *bool `yaml:"compress"`
}

func (c AdminLogsRotationConfig) CompressionEnabled() bool {
	return c.Compress == nil || *c.Compress
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
	Quota ClientQuotaConfig `yaml:"quota" json:"quota"`
}

type ClientQuotaConfig struct {
	Enabled bool                `yaml:"enabled" json:"enabled"`
	Windows []QuotaWindowConfig `yaml:"windows" json:"windows"`
}

type QuotaWindowConfig struct {
	Name        string        `yaml:"name" json:"name"`
	Duration    DurationValue `yaml:"duration" json:"duration"`
	MaxRequests int           `yaml:"max_requests" json:"max_requests"`
}

type OutboundQuotaWindowConfig struct {
	Name        string                   `yaml:"name" json:"name"`
	Reset       string                   `yaml:"reset" json:"reset"`
	Duration    DurationValue            `yaml:"duration" json:"duration"`
	Fixed       QuotaFixedScheduleConfig `yaml:"fixed" json:"fixed"`
	MaxRequests int                      `yaml:"max_requests" json:"max_requests"`
	MaxTokens   int                      `yaml:"max_tokens" json:"max_tokens"`
}

type QuotaFixedScheduleConfig struct {
	Period   string `yaml:"period" json:"period"`
	Anchor   string `yaml:"anchor" json:"anchor"`
	Time     string `yaml:"time" json:"time"`
	Timezone string `yaml:"timezone" json:"timezone"`
	Weekday  string `yaml:"weekday" json:"weekday"`
}

type QuotaResetAllConfig struct {
	Enabled  bool                     `yaml:"enabled" json:"enabled"`
	Schedule QuotaResetScheduleConfig `yaml:"schedule" json:"schedule"`
}

type QuotaResetScheduleConfig struct {
	Period   string        `yaml:"period" json:"period"`
	Duration DurationValue `yaml:"duration" json:"duration"`
	Anchor   string        `yaml:"anchor" json:"anchor"`
	Time     string        `yaml:"time" json:"time"`
	Timezone string        `yaml:"timezone" json:"timezone"`
	Weekday  string        `yaml:"weekday" json:"weekday"`
}

type InboundSpec struct {
	Name     string       `yaml:"name"`
	Protocol string       `yaml:"protocol"`
	Path     string       `yaml:"path"`
	Clients  []ClientSpec `yaml:"clients"`
}

type RoutingRule struct {
	Name        string            `yaml:"name"`
	FromTags    []string          `yaml:"from_tags"`
	ToTags      []string          `yaml:"to_tags"`
	Strategy    string            `yaml:"strategy"`
	Weights     map[string]int    `yaml:"weights"`
	TargetModel string            `yaml:"target_model"`
	ModelMap    map[string]string `yaml:"model_map"`
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
	Enabled      *bool                `yaml:"enabled"`
	Models       []OutboundModelSpec  `yaml:"models" json:"models"`
	Capabilities OutboundCapabilities `yaml:"capabilities"`
	Quota        OutboundQuotaConfig  `yaml:"quota" json:"quota"`
	Proxy        OutboundProxyConfig  `yaml:"proxy"`
}

type OutboundModelSpec struct {
	Name    string   `yaml:"name" json:"name"`
	Aliases []string `yaml:"aliases" json:"aliases"`
}

type OutboundProxyConfig struct {
	URL string `yaml:"url" json:"url"`
}

type OutboundQuotaConfig struct {
	Enabled       bool                        `yaml:"enabled" json:"enabled"`
	Windows       []OutboundQuotaWindowConfig `yaml:"windows" json:"windows"`
	Cooldown      DurationValue               `yaml:"cooldown" json:"cooldown"`
	ProbeInterval DurationValue               `yaml:"probe_interval" json:"probe_interval"`
	ResetAll      QuotaResetAllConfig         `yaml:"reset_all" json:"reset_all"`
}

type OutboundCapabilities struct {
	ResponsesPreviousResponseID     *bool  `yaml:"responses_previous_response_id" json:"responses_previous_response_id"`
	ResponsesBuiltinTools           *bool  `yaml:"responses_builtin_tools" json:"responses_builtin_tools"`
	ResponsesToolResultStatusError  *bool  `yaml:"responses_tool_result_status_error" json:"responses_tool_result_status_error"`
	ResponsesAssistantHistoryNative *bool  `yaml:"responses_assistant_history_native" json:"responses_assistant_history_native"`
	UsageEstimation                 bool   `yaml:"usage_estimation" json:"usage_estimation"`
	UsageEstimationMode             string `yaml:"usage_estimation_mode" json:"usage_estimation_mode"`
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

type GovernanceConfig struct {
	Quota GovernanceQuotaConfig `yaml:"quota"`
}

type GovernanceQuotaConfig struct {
	Snapshot GovernanceQuotaSnapshotConfig `yaml:"snapshot"`
	Events   GovernanceQuotaEventsConfig   `yaml:"events"`
}

type GovernanceQuotaSnapshotConfig struct {
	Enabled       bool          `yaml:"enabled"`
	Dir           string        `yaml:"dir"`
	FlushInterval DurationValue `yaml:"flush_interval"`
}

type GovernanceQuotaEventsConfig struct {
	Enabled    bool `yaml:"enabled"`
	MaxEntries int  `yaml:"max_entries"`
}

type AccountingConfig struct {
	Enabled    bool                      `yaml:"enabled"`
	Backend    string                    `yaml:"backend"`
	ExposeHTTP bool                      `yaml:"expose_http"`
	AdminToken string                    `yaml:"admin_token"`
	LocalFile  AccountingLocalFileConfig `yaml:"local_file"`
	Pricing    []AccountingPriceConfig   `yaml:"pricing"`
}

type AccountingPriceConfig struct {
	Provider                 string  `yaml:"provider" json:"provider"`
	Model                    string  `yaml:"model" json:"model"`
	InputPerMillionUSD       float64 `yaml:"input_per_million_usd" json:"input_per_million_usd"`
	OutputPerMillionUSD      float64 `yaml:"output_per_million_usd" json:"output_per_million_usd"`
	CacheCreatePerMillionUSD float64 `yaml:"cache_create_per_million_usd" json:"cache_create_per_million_usd"`
	CacheReadPerMillionUSD   float64 `yaml:"cache_read_per_million_usd" json:"cache_read_per_million_usd"`
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
	return ParseBytes(data)
}

func WriteValidatedFile(path string, data []byte) error {
	if _, err := ParseBytes(data); err != nil {
		return err
	}
	if path == "" {
		return fmt.Errorf("config path is required")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temp config: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp config: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp config: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}

func ParseBytes(data []byte) (Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("unmarshal config: %w", err)
	}
	cfg.applyDefaults()

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Admin.Logs.Path == "" {
		c.Admin.Logs.Path = "tmp/dev.log"
	}
	if c.Admin.Logs.MaxBytes == 0 {
		c.Admin.Logs.MaxBytes = 65536
	}
	rotation := &c.Admin.Logs.Rotation
	if rotation.MaxSizeMB == 0 {
		rotation.MaxSizeMB = 100
	}
	if rotation.MaxFiles == 0 {
		rotation.MaxFiles = 20
	}
	if rotation.MaxAgeDays == 0 {
		rotation.MaxAgeDays = 14
	}
	if rotation.MaxTotalSizeMB == 0 {
		rotation.MaxTotalSizeMB = 1024
	}
	for i := range c.Outbounds {
		for j := range c.Outbounds[i].Quota.Windows {
			window := &c.Outbounds[i].Quota.Windows[j]
			if window.Reset == "" {
				window.Reset = "rolling"
			}
		}
	}
}

func (c Config) Validate() error {
	c.applyDefaults()
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

	if err := validateAdmin(c.Admin, tokens, c.Accounting); err != nil {
		return err
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
		if err := validateAccountingPricing(c.Accounting.Pricing); err != nil {
			return err
		}
	}

	if err := validateGovernance(c.Governance); err != nil {
		return err
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
		if err := validateOutboundModels(outbound); err != nil {
			return err
		}
		if err := validateOutboundProxy(outbound); err != nil {
			return err
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
		if rule.TargetModel != "" && len(rule.ModelMap) > 0 {
			return fmt.Errorf("routing.rules[%d].model_map cannot be used with target_model", i)
		}
		for from, to := range rule.ModelMap {
			if from == "" {
				return fmt.Errorf("routing.rules[%d].model_map contains empty source model", i)
			}
			if to == "" {
				return fmt.Errorf("routing.rules[%d].model_map.%s must not be empty", i, from)
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

func validateAdmin(cfg AdminConfig, clientTokens map[string]string, accounting AccountingConfig) error {
	if cfg.Logs.Enabled {
		if cfg.Logs.Path == "" {
			return fmt.Errorf("admin.logs.path is required when admin.logs.enabled=true")
		}
		if cfg.Logs.MaxBytes <= 0 {
			return fmt.Errorf("admin.logs.max_bytes must be greater than 0")
		}
		if cfg.Logs.Rotation.MaxSizeMB <= 0 {
			return fmt.Errorf("admin.logs.rotation.max_size_mb must be greater than 0")
		}
		if cfg.Logs.Rotation.MaxFiles <= 0 {
			return fmt.Errorf("admin.logs.rotation.max_files must be greater than 0")
		}
		if cfg.Logs.Rotation.MaxAgeDays <= 0 {
			return fmt.Errorf("admin.logs.rotation.max_age_days must be greater than 0")
		}
		if cfg.Logs.Rotation.MaxTotalSizeMB <= 0 {
			return fmt.Errorf("admin.logs.rotation.max_total_size_mb must be greater than 0")
		}
	}
	if !cfg.Enabled {
		return nil
	}
	if cfg.Token == "" {
		return fmt.Errorf("admin.token is required when admin.enabled=true")
	}
	if owner, ok := clientTokens[cfg.Token]; ok {
		return fmt.Errorf("admin.token duplicates inbound client token used by %s", owner)
	}
	if accounting.AdminToken != "" && cfg.Token == accounting.AdminToken {
		return fmt.Errorf("admin.token must be different from accounting.admin_token")
	}
	return nil
}

func validateGovernance(cfg GovernanceConfig) error {
	if cfg.Quota.Snapshot.Enabled {
		if cfg.Quota.Snapshot.Dir == "" {
			return fmt.Errorf("governance.quota.snapshot.dir is required when governance.quota.snapshot.enabled=true")
		}
		if cfg.Quota.Snapshot.FlushInterval.Duration() <= 0 {
			return fmt.Errorf("governance.quota.snapshot.flush_interval must be a positive duration")
		}
	}
	if cfg.Quota.Events.Enabled && cfg.Quota.Events.MaxEntries <= 0 {
		return fmt.Errorf("governance.quota.events.max_entries must be greater than 0")
	}
	return nil
}

func validateClientQuota(inboundName string, index int, client ClientSpec) error {
	quota := client.Quota
	if !quota.Enabled {
		return nil
	}
	return validateQuotaWindows(fmt.Sprintf("inbounds.%s.clients[%d].quota", inboundName, index), quota.Windows)
}

func validateOutboundModels(outbound OutboundSpec) error {
	seen := make(map[string]string)
	for i, model := range outbound.Models {
		prefix := fmt.Sprintf("outbounds.%s.models[%d]", outbound.Name, i)
		if model.Name == "" || strings.TrimSpace(model.Name) != model.Name {
			return fmt.Errorf("%s.name must be non-empty and trimmed", prefix)
		}
		if owner, ok := seen[model.Name]; ok {
			return fmt.Errorf("%s.name %q conflicts with %s", prefix, model.Name, owner)
		}
		seen[model.Name] = prefix + ".name"
		for j, alias := range model.Aliases {
			field := fmt.Sprintf("%s.aliases[%d]", prefix, j)
			if alias == "" || strings.TrimSpace(alias) != alias {
				return fmt.Errorf("%s must be non-empty and trimmed", field)
			}
			if owner, ok := seen[alias]; ok {
				return fmt.Errorf("%s %q conflicts with %s", field, alias, owner)
			}
			seen[alias] = field
		}
	}
	return nil
}

func validateOutboundProxy(outbound OutboundSpec) error {
	proxyURL := strings.TrimSpace(outbound.Proxy.URL)
	if proxyURL == "" {
		return nil
	}
	parsed, err := url.Parse(proxyURL)
	if err != nil {
		return fmt.Errorf("outbounds.%s.proxy.url is invalid: %w", outbound.Name, err)
	}
	if parsed.Host == "" {
		return fmt.Errorf("outbounds.%s.proxy.url host is required", outbound.Name)
	}
	switch parsed.Scheme {
	case "http", "https", "socks5":
		return nil
	default:
		return fmt.Errorf("outbounds.%s.proxy.url has unsupported scheme %q", outbound.Name, parsed.Scheme)
	}
}

func validateOutboundQuota(outbound OutboundSpec) error {
	quota := outbound.Quota
	if !quota.Enabled {
		return nil
	}
	prefix := fmt.Sprintf("outbounds.%s.quota", outbound.Name)
	if err := validateOutboundQuotaWindows(prefix, quota.Windows); err != nil {
		return err
	}
	if quota.Cooldown.Duration() <= 0 {
		return fmt.Errorf("outbounds.%s.quota.cooldown must be a positive duration", outbound.Name)
	}
	if quota.ProbeInterval.Duration() <= 0 {
		return fmt.Errorf("outbounds.%s.quota.probe_interval must be a positive duration", outbound.Name)
	}
	if quota.ResetAll.Enabled || quota.ResetAll.Schedule != (QuotaResetScheduleConfig{}) {
		if err := validateResetSchedule(prefix+".reset_all.schedule", quota.ResetAll.Schedule); err != nil {
			return err
		}
	}
	return nil
}

func validateOutboundQuotaWindows(prefix string, windows []OutboundQuotaWindowConfig) error {
	if len(windows) == 0 {
		return fmt.Errorf("%s.windows is required when quota is enabled", prefix)
	}
	seen := make(map[string]struct{}, len(windows))
	for i, window := range windows {
		windowPrefix := fmt.Sprintf("%s.windows[%d]", prefix, i)
		if window.Name == "" {
			return fmt.Errorf("%s.name is required", windowPrefix)
		}
		if _, ok := seen[window.Name]; ok {
			return fmt.Errorf("%s.name duplicates %q", windowPrefix, window.Name)
		}
		seen[window.Name] = struct{}{}
		if window.MaxRequests < 0 {
			return fmt.Errorf("%s.max_requests must be greater than or equal to 0", windowPrefix)
		}
		if window.MaxTokens < 0 {
			return fmt.Errorf("%s.max_tokens must be greater than or equal to 0", windowPrefix)
		}
		if window.MaxRequests == 0 && window.MaxTokens == 0 {
			return fmt.Errorf("%s requires max_requests or max_tokens greater than 0", windowPrefix)
		}
		switch window.Reset {
		case "rolling":
			if window.Duration.Duration() <= 0 {
				return fmt.Errorf("%s.duration must be a positive duration when reset=rolling", windowPrefix)
			}
			if window.Fixed != (QuotaFixedScheduleConfig{}) {
				return fmt.Errorf("%s.fixed is only supported when reset=fixed", windowPrefix)
			}
		case "fixed":
			if err := validateFixedWindowSchedule(windowPrefix, window); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%s.reset %q is unsupported", windowPrefix, window.Reset)
		}
	}
	return nil
}

func validateFixedWindowSchedule(prefix string, window OutboundQuotaWindowConfig) error {
	fixed := window.Fixed
	if fixed.Period == "" {
		return fmt.Errorf("%s.fixed.period is required when reset=fixed", prefix)
	}
	switch fixed.Period {
	case "interval":
		if window.Duration.Duration() <= 0 {
			return fmt.Errorf("%s.duration must be a positive duration when fixed.period=interval", prefix)
		}
		if err := validateRFC3339Anchor(prefix+".fixed.anchor", fixed.Anchor); err != nil {
			return err
		}
		if fixed.Time != "" || fixed.Timezone != "" || fixed.Weekday != "" {
			return fmt.Errorf("%s.fixed time, timezone, and weekday are not supported when period=interval", prefix)
		}
	case "daily":
		if window.Duration != "" || fixed.Anchor != "" || fixed.Weekday != "" {
			return fmt.Errorf("%s duration, fixed.anchor, and fixed.weekday are not supported when fixed.period=daily", prefix)
		}
		return validateWallClock(prefix+".fixed", fixed.Time, fixed.Timezone)
	case "weekly":
		if window.Duration != "" || fixed.Anchor != "" {
			return fmt.Errorf("%s duration and fixed.anchor are not supported when fixed.period=weekly", prefix)
		}
		if err := validateWallClock(prefix+".fixed", fixed.Time, fixed.Timezone); err != nil {
			return err
		}
		if !validWeekday(fixed.Weekday) {
			return fmt.Errorf("%s.fixed.weekday must be monday-sunday", prefix)
		}
	default:
		return fmt.Errorf("%s.fixed.period %q is unsupported", prefix, fixed.Period)
	}
	return nil
}

func validateResetSchedule(prefix string, schedule QuotaResetScheduleConfig) error {
	switch schedule.Period {
	case "interval":
		if schedule.Duration.Duration() <= 0 {
			return fmt.Errorf("%s.duration must be a positive duration when period=interval", prefix)
		}
		if err := validateRFC3339Anchor(prefix+".anchor", schedule.Anchor); err != nil {
			return err
		}
		if schedule.Time != "" || schedule.Timezone != "" || schedule.Weekday != "" {
			return fmt.Errorf("%s time, timezone, and weekday are not supported when period=interval", prefix)
		}
	case "daily":
		if schedule.Duration != "" || schedule.Anchor != "" || schedule.Weekday != "" {
			return fmt.Errorf("%s duration, anchor, and weekday are not supported when period=daily", prefix)
		}
		return validateWallClock(prefix, schedule.Time, schedule.Timezone)
	case "weekly":
		if schedule.Duration != "" || schedule.Anchor != "" {
			return fmt.Errorf("%s duration and anchor are not supported when period=weekly", prefix)
		}
		if err := validateWallClock(prefix, schedule.Time, schedule.Timezone); err != nil {
			return err
		}
		if !validWeekday(schedule.Weekday) {
			return fmt.Errorf("%s.weekday must be monday-sunday", prefix)
		}
	default:
		return fmt.Errorf("%s.period %q is unsupported", prefix, schedule.Period)
	}
	return nil
}

func validateRFC3339Anchor(field, value string) error {
	if value == "" {
		return fmt.Errorf("%s is required", field)
	}
	if _, err := time.Parse(time.RFC3339, value); err != nil {
		return fmt.Errorf("%s must be RFC3339: %w", field, err)
	}
	if !strings.HasSuffix(value, "Z") && (len(value) < 6 || (value[len(value)-6] != '+' && value[len(value)-6] != '-')) {
		return fmt.Errorf("%s must include an explicit offset", field)
	}
	return nil
}

func validateWallClock(prefix, value, timezone string) error {
	if value == "" {
		return fmt.Errorf("%s.time is required", prefix)
	}
	if _, err := time.Parse("15:04", value); err != nil {
		if _, secondsErr := time.Parse("15:04:05", value); secondsErr != nil {
			return fmt.Errorf("%s.time must use HH:MM[:SS]", prefix)
		}
	}
	if timezone == "" {
		return fmt.Errorf("%s.timezone is required", prefix)
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		return fmt.Errorf("%s.timezone must be a valid IANA timezone", prefix)
	}
	return nil
}

func validWeekday(value string) bool {
	switch value {
	case "monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday":
		return true
	default:
		return false
	}
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

func validateAccountingPricing(items []AccountingPriceConfig) error {
	for i, item := range items {
		if item.Model == "" {
			return fmt.Errorf("accounting.pricing[%d].model is required", i)
		}
		if item.InputPerMillionUSD < 0 || item.OutputPerMillionUSD < 0 || item.CacheCreatePerMillionUSD < 0 || item.CacheReadPerMillionUSD < 0 {
			return fmt.Errorf("accounting.pricing[%d] rates must be greater than or equal to 0", i)
		}
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
