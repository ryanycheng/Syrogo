package config

import (
	"os"
	"path/filepath"
	"testing"
)

func validConfig() Config {
	return Config{
		Listeners: []ListenerSpec{{Name: "public", Listen: ":8080", Inbounds: []string{"openai-entry"}}},
		Inbounds: []InboundSpec{{
			Name:     "openai-entry",
			Protocol: "openai_chat",
			Path:     "/v1/chat/completions",
			Clients:  []ClientSpec{{Name: "office-key", Token: "client-token", Tag: "office"}},
		}},
		Routing: RoutingConfig{Rules: []RoutingRule{{
			Name:     "office-route",
			FromTags: []string{"office"},
			ToTags:   []string{"mock-tag"},
			Strategy: "failover",
		}}},
		Outbounds: []OutboundSpec{{Name: "mock", Protocol: "mock", Tag: "mock-tag"}},
	}
}

func TestConfigValidateSuccess(t *testing.T) {
	cfg := validConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConfigListenAddressesUsesListeners(t *testing.T) {
	cfg := Config{
		Listeners: []ListenerSpec{
			{Name: "public", Listen: ":8080", Inbounds: []string{"openai-entry"}},
			{Name: "private", Listen: ":8081", Inbounds: []string{"openai-entry"}},
		},
	}

	got := cfg.ListenAddresses()
	if len(got) != 2 || got[0] != ":8080" || got[1] != ":8081" {
		t.Fatalf("ListenAddresses() = %#v, want [:8080 :8081]", got)
	}
}

func TestConfigInboundByNameReturnsMatchedInbound(t *testing.T) {
	cfg := Config{
		Inbounds: []InboundSpec{{
			Name:     "office-entry",
			Protocol: "openai_chat",
			Path:     "/v1/chat/completions",
			Clients:  []ClientSpec{{Name: "office-key", Token: "token", Tag: "office"}},
		}},
	}

	got := cfg.InboundByName("office-entry")
	if got.Name != "office-entry" {
		t.Fatalf("InboundByName() name = %q, want office-entry", got.Name)
	}
	if got.Clients[0].Tag != "office" {
		t.Fatalf("InboundByName() clients = %#v, want office tag", got.Clients)
	}
}

func TestConfigListenerInboundsReturnsAllMatchedInbounds(t *testing.T) {
	cfg := Config{
		Inbounds: []InboundSpec{
			{Name: "openai-entry", Protocol: "openai_chat", Path: "/v1/chat/completions", Clients: []ClientSpec{{Name: "office-key", Token: "a", Tag: "office"}}},
			{Name: "anthropic-entry", Protocol: "anthropic_messages", Path: "/v1/messages", Clients: []ClientSpec{{Name: "thinking-key", Token: "b", Tag: "thinking"}}},
		},
	}

	got := cfg.ListenerInbounds(ListenerSpec{Name: "public", Inbounds: []string{"openai-entry", "anthropic-entry"}})
	if len(got) != 2 {
		t.Fatalf("len(ListenerInbounds()) = %d, want 2", len(got))
	}
	if got[1].Protocol != "anthropic_messages" {
		t.Fatalf("ListenerInbounds()[1].Protocol = %q, want anthropic_messages", got[1].Protocol)
	}
}

func TestConfigValidateListenerInboundNotFound(t *testing.T) {
	cfg := validConfig()
	cfg.Listeners[0].Inbounds = []string{"missing"}

	err := cfg.Validate()
	if err == nil || err.Error() != "listeners.public.inbound \"missing\" not found in inbounds" {
		t.Fatalf("Validate() error = %v, want missing inbound error", err)
	}
}

func TestConfigValidateRequiresInboundProtocol(t *testing.T) {
	cfg := validConfig()
	cfg.Inbounds[0].Protocol = ""

	err := cfg.Validate()
	if err == nil || err.Error() != "inbounds.openai-entry.protocol is required" {
		t.Fatalf("Validate() error = %v, want missing protocol error", err)
	}
}

func TestConfigValidateRejectsUnsupportedInboundProtocol(t *testing.T) {
	cfg := validConfig()
	cfg.Inbounds[0].Protocol = "unsupported"

	err := cfg.Validate()
	if err == nil || err.Error() != "inbounds.openai-entry.protocol \"unsupported\" is unsupported" {
		t.Fatalf("Validate() error = %v, want unsupported inbound protocol error", err)
	}
}

func TestConfigValidateSupportsAnthropicInboundProtocol(t *testing.T) {
	cfg := validConfig()
	cfg.Inbounds = append(cfg.Inbounds, InboundSpec{
		Name:     "anthropic-entry",
		Protocol: "anthropic_messages",
		Path:     "/v1/messages",
		Clients:  []ClientSpec{{Name: "anthropic-key", Token: "anthropic-token", Tag: "office"}},
	})
	cfg.Listeners[0].Inbounds = append(cfg.Listeners[0].Inbounds, "anthropic-entry")

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConfigValidateRequiresClientName(t *testing.T) {
	cfg := validConfig()
	cfg.Inbounds[0].Clients[0].Name = ""

	err := cfg.Validate()
	if err == nil || err.Error() != "inbounds.openai-entry.clients[0].name is required" {
		t.Fatalf("Validate() error = %v, want missing client name error", err)
	}
}

func TestConfigValidateRequiresClientToken(t *testing.T) {
	cfg := validConfig()
	cfg.Inbounds[0].Clients[0].Token = ""

	err := cfg.Validate()
	if err == nil || err.Error() != "inbounds.openai-entry.clients[0].token is required" {
		t.Fatalf("Validate() error = %v, want missing client token error", err)
	}
}

func TestConfigValidateRequiresClientTag(t *testing.T) {
	cfg := validConfig()
	cfg.Inbounds[0].Clients[0].Tag = ""

	err := cfg.Validate()
	if err == nil || err.Error() != "inbounds.openai-entry.clients[0].tag is required" {
		t.Fatalf("Validate() error = %v, want missing client tag error", err)
	}
}

func TestConfigValidateRejectsDuplicateClientName(t *testing.T) {
	cfg := validConfig()
	cfg.Inbounds = append(cfg.Inbounds, InboundSpec{
		Name:     "other-entry",
		Protocol: "openai_chat",
		Path:     "/other",
		Clients:  []ClientSpec{{Name: "office-key", Token: "other-token", Tag: "other"}},
	})
	cfg.Listeners[0].Inbounds = append(cfg.Listeners[0].Inbounds, "other-entry")

	err := cfg.Validate()
	if err == nil || err.Error() != "inbounds.other-entry.clients[0].name duplicates client name used by openai-entry" {
		t.Fatalf("Validate() error = %v, want duplicate client name error", err)
	}
}

func TestConfigValidateRejectsDuplicateClientToken(t *testing.T) {
	cfg := validConfig()
	cfg.Inbounds = append(cfg.Inbounds, InboundSpec{
		Name:     "other-entry",
		Protocol: "openai_chat",
		Path:     "/other",
		Clients:  []ClientSpec{{Name: "other-key", Token: "client-token", Tag: "other"}},
	})
	cfg.Listeners[0].Inbounds = append(cfg.Listeners[0].Inbounds, "other-entry")

	err := cfg.Validate()
	if err == nil || err.Error() != "inbounds.other-entry.clients[0].token duplicates token used by openai-entry" {
		t.Fatalf("Validate() error = %v, want duplicate token error", err)
	}
}

func TestConfigValidateSupportsClientQuotaWindows(t *testing.T) {
	cfg := validConfig()
	cfg.Inbounds[0].Clients[0].Quota = ClientQuotaConfig{
		Enabled: true,
		Windows: []QuotaWindowConfig{
			{Name: "hourly", Duration: DurationValue("1h"), MaxRequests: 100},
			{Name: "daily", Duration: DurationValue("24h"), MaxRequests: 1000},
		},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConfigValidateRequiresClientQuotaWindows(t *testing.T) {
	cfg := validConfig()
	cfg.Inbounds[0].Clients[0].Quota = ClientQuotaConfig{Enabled: true}

	err := cfg.Validate()
	if err == nil || err.Error() != "inbounds.openai-entry.clients[0].quota.windows is required when quota is enabled" {
		t.Fatalf("Validate() error = %v, want missing client quota windows error", err)
	}
}

func TestConfigValidateRejectsDuplicateClientQuotaWindowName(t *testing.T) {
	cfg := validConfig()
	cfg.Inbounds[0].Clients[0].Quota = ClientQuotaConfig{
		Enabled: true,
		Windows: []QuotaWindowConfig{
			{Name: "daily", Duration: DurationValue("24h"), MaxRequests: 100},
			{Name: "daily", Duration: DurationValue("168h"), MaxRequests: 1000},
		},
	}

	err := cfg.Validate()
	if err == nil || err.Error() != "inbounds.openai-entry.clients[0].quota.windows[1].name duplicates \"daily\"" {
		t.Fatalf("Validate() error = %v, want duplicate client quota window name error", err)
	}
}

func TestConfigValidateRequiresPositiveClientQuotaDuration(t *testing.T) {
	cfg := validConfig()
	cfg.Inbounds[0].Clients[0].Quota = ClientQuotaConfig{
		Enabled: true,
		Windows: []QuotaWindowConfig{{Name: "daily", Duration: DurationValue("0s"), MaxRequests: 100}},
	}

	err := cfg.Validate()
	if err == nil || err.Error() != "inbounds.openai-entry.clients[0].quota.windows[0].duration must be a positive duration" {
		t.Fatalf("Validate() error = %v, want invalid client quota duration error", err)
	}
}

func TestConfigValidateRequiresPositiveClientQuotaLimit(t *testing.T) {
	cfg := validConfig()
	cfg.Inbounds[0].Clients[0].Quota = ClientQuotaConfig{
		Enabled: true,
		Windows: []QuotaWindowConfig{{Name: "daily", Duration: DurationValue("24h"), MaxRequests: 0}},
	}

	err := cfg.Validate()
	if err == nil || err.Error() != "inbounds.openai-entry.clients[0].quota.windows[0].max_requests must be greater than 0" {
		t.Fatalf("Validate() error = %v, want invalid client quota limit error", err)
	}
}

func TestConfigValidateSupportsGovernanceQuotaSnapshot(t *testing.T) {
	cfg := validConfig()
	cfg.Governance.Quota.Snapshot = GovernanceQuotaSnapshotConfig{Enabled: true, Dir: "./tmp/quota", FlushInterval: DurationValue("2s")}
	cfg.Governance.Quota.Events = GovernanceQuotaEventsConfig{Enabled: true, MaxEntries: 100}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConfigValidateRequiresGovernanceQuotaSnapshotDir(t *testing.T) {
	cfg := validConfig()
	cfg.Governance.Quota.Snapshot = GovernanceQuotaSnapshotConfig{Enabled: true, FlushInterval: DurationValue("2s")}

	err := cfg.Validate()
	if err == nil || err.Error() != "governance.quota.snapshot.dir is required when governance.quota.snapshot.enabled=true" {
		t.Fatalf("Validate() error = %v, want missing governance quota snapshot dir error", err)
	}
}

func TestConfigValidateRequiresGovernanceQuotaSnapshotFlushInterval(t *testing.T) {
	cfg := validConfig()
	cfg.Governance.Quota.Snapshot = GovernanceQuotaSnapshotConfig{Enabled: true, Dir: "./tmp/quota"}

	err := cfg.Validate()
	if err == nil || err.Error() != "governance.quota.snapshot.flush_interval must be a positive duration" {
		t.Fatalf("Validate() error = %v, want invalid governance quota snapshot flush interval error", err)
	}
}

func TestConfigValidateRequiresPositiveGovernanceQuotaEventLimit(t *testing.T) {
	cfg := validConfig()
	cfg.Governance.Quota.Events = GovernanceQuotaEventsConfig{Enabled: true}

	err := cfg.Validate()
	if err == nil || err.Error() != "governance.quota.events.max_entries must be greater than 0" {
		t.Fatalf("Validate() error = %v, want invalid governance quota events max entries error", err)
	}
}

func TestConfigValidateRequiresOutboundTag(t *testing.T) {
	cfg := validConfig()
	cfg.Outbounds[0].Tag = ""

	err := cfg.Validate()
	if err == nil || err.Error() != "outbounds.mock.tag is required" {
		t.Fatalf("Validate() error = %v, want missing outbound tag error", err)
	}
}

func TestConfigValidateRejectsUnsupportedOutboundProtocol(t *testing.T) {
	cfg := validConfig()
	cfg.Outbounds[0] = OutboundSpec{Name: "unknown", Protocol: "unsupported", Tag: "unknown-tag"}
	cfg.Routing.Rules[0].ToTags = []string{"unknown-tag"}

	err := cfg.Validate()
	if err == nil || err.Error() != "outbounds.unknown.protocol \"unsupported\" is unsupported" {
		t.Fatalf("Validate() error = %v, want unsupported outbound protocol error", err)
	}
}

func TestConfigValidateOpenAIChatRequiresEndpointAndAuthToken(t *testing.T) {
	cfg := validConfig()
	cfg.Outbounds[0] = OutboundSpec{Name: "openai", Protocol: "openai_chat", Tag: "openai-tag"}
	cfg.Routing.Rules[0].ToTags = []string{"openai-tag"}

	err := cfg.Validate()
	if err == nil || err.Error() != "outbounds.openai.endpoint is required" {
		t.Fatalf("Validate() error = %v, want missing endpoint error", err)
	}

	cfg.Outbounds[0].Endpoint = "https://example.com/v1"
	err = cfg.Validate()
	if err == nil || err.Error() != "outbounds.openai.auth_token is required" {
		t.Fatalf("Validate() error = %v, want missing auth_token error", err)
	}
}

func TestConfigValidateSupportsOpenAIResponsesOutbound(t *testing.T) {
	cfg := validConfig()
	cfg.Outbounds[0] = OutboundSpec{Name: "responses", Protocol: "openai_responses", Tag: "responses-tag", Endpoint: "https://example.com/v1", AuthToken: "key-1"}
	cfg.Routing.Rules[0].ToTags = []string{"responses-tag"}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConfigValidateRejectsCapabilitiesOnNonResponsesOutbound(t *testing.T) {
	cfg := validConfig()
	disabled := false
	cfg.Outbounds[0] = OutboundSpec{
		Name:      "openai",
		Protocol:  "openai_chat",
		Tag:       "openai-tag",
		Endpoint:  "https://example.com/v1",
		AuthToken: "key-1",
		Capabilities: OutboundCapabilities{
			ResponsesBuiltinTools: &disabled,
		},
	}
	cfg.Routing.Rules[0].ToTags = []string{"openai-tag"}

	err := cfg.Validate()
	if err == nil || err.Error() != "outbounds.openai.responses capabilities are only supported for openai_responses" {
		t.Fatalf("Validate() error = %v, want unsupported capabilities error", err)
	}
}

func TestConfigLoadParsesOutboundCapabilities(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte(`listeners:
  - name: "public"
    listen: ":8080"
    inbounds:
      - "openai-entry"
inbounds:
  - name: "openai-entry"
    protocol: "openai_chat"
    path: "/v1/chat/completions"
    clients:
      - name: "office-key"
        token: "client-token"
        tag: "office"
routing:
  rules:
    - name: "office-route"
      from_tags:
        - "office"
      to_tags:
        - "responses-tag"
      strategy: "failover"
outbounds:
  - name: "responses"
    protocol: "openai_responses"
    endpoint: "https://example.com/v1"
    auth_token: "key-1"
    tag: "responses-tag"
    capabilities:
      responses_previous_response_id: false
      responses_builtin_tools: true
      responses_tool_result_status_error: false
      responses_assistant_history_native: true
`)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	caps := cfg.Outbounds[0].Capabilities
	if caps.ResponsesPreviousResponseID == nil || *caps.ResponsesPreviousResponseID {
		t.Fatalf("ResponsesPreviousResponseID = %#v, want false", caps.ResponsesPreviousResponseID)
	}
	if caps.ResponsesBuiltinTools == nil || !*caps.ResponsesBuiltinTools {
		t.Fatalf("ResponsesBuiltinTools = %#v, want true", caps.ResponsesBuiltinTools)
	}
	if caps.ResponsesToolResultStatusError == nil || *caps.ResponsesToolResultStatusError {
		t.Fatalf("ResponsesToolResultStatusError = %#v, want false", caps.ResponsesToolResultStatusError)
	}
	if caps.ResponsesAssistantHistoryNative == nil || !*caps.ResponsesAssistantHistoryNative {
		t.Fatalf("ResponsesAssistantHistoryNative = %#v, want true", caps.ResponsesAssistantHistoryNative)
	}
}

func TestConfigValidateSupportsAnthropicMessagesOutbound(t *testing.T) {
	cfg := validConfig()
	cfg.Outbounds[0] = OutboundSpec{Name: "anthropic", Protocol: "anthropic_messages", Tag: "anthropic-tag", Endpoint: "https://example.com/v1", AuthToken: "key-1"}
	cfg.Routing.Rules[0].ToTags = []string{"anthropic-tag"}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConfigValidateAnthropicMessagesRequiresEndpointAndAuthToken(t *testing.T) {
	cfg := validConfig()
	cfg.Outbounds[0] = OutboundSpec{Name: "anthropic", Protocol: "anthropic_messages", Tag: "anthropic-tag"}
	cfg.Routing.Rules[0].ToTags = []string{"anthropic-tag"}

	err := cfg.Validate()
	if err == nil || err.Error() != "outbounds.anthropic.endpoint is required" {
		t.Fatalf("Validate() error = %v, want missing endpoint error", err)
	}

	cfg.Outbounds[0].Endpoint = "https://example.com/v1"
	err = cfg.Validate()
	if err == nil || err.Error() != "outbounds.anthropic.auth_token is required" {
		t.Fatalf("Validate() error = %v, want missing auth_token error", err)
	}
}

func TestConfigValidateRequiresRuleStrategy(t *testing.T) {
	cfg := validConfig()
	cfg.Routing.Rules[0].Strategy = ""

	err := cfg.Validate()
	if err == nil || err.Error() != "routing.rules[0].strategy is required" {
		t.Fatalf("Validate() error = %v, want missing strategy error", err)
	}
}

func TestConfigValidateRejectsUnsupportedStrategy(t *testing.T) {
	cfg := validConfig()
	cfg.Routing.Rules[0].Strategy = "random"

	err := cfg.Validate()
	if err == nil || err.Error() != "routing.rules[0].strategy \"random\" is unsupported" {
		t.Fatalf("Validate() error = %v, want unsupported strategy error", err)
	}
}

func TestConfigValidateRejectsUnknownToTag(t *testing.T) {
	cfg := validConfig()
	cfg.Routing.Rules[0].ToTags = []string{"missing-tag"}

	err := cfg.Validate()
	if err == nil || err.Error() != "routing.rules[0].to_tags \"missing-tag\" not found in outbounds" {
		t.Fatalf("Validate() error = %v, want missing to_tag error", err)
	}
}

func TestConfigValidateSupportsRoutingModelMap(t *testing.T) {
	cfg := validConfig()
	cfg.Routing.Rules[0].ModelMap = map[string]string{"gpt-4": "gpt-4o-mini"}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConfigValidateRejectsRoutingModelMapWithTargetModel(t *testing.T) {
	cfg := validConfig()
	cfg.Routing.Rules[0].TargetModel = "gpt-4o-mini"
	cfg.Routing.Rules[0].ModelMap = map[string]string{"gpt-4": "gpt-4o-mini"}

	err := cfg.Validate()
	if err == nil || err.Error() != "routing.rules[0].model_map cannot be used with target_model" {
		t.Fatalf("Validate() error = %v, want model_map target_model conflict error", err)
	}
}

func TestConfigValidateRejectsRoutingModelMapEmptyTarget(t *testing.T) {
	cfg := validConfig()
	cfg.Routing.Rules[0].ModelMap = map[string]string{"gpt-4": ""}

	err := cfg.Validate()
	if err == nil || err.Error() != "routing.rules[0].model_map.gpt-4 must not be empty" {
		t.Fatalf("Validate() error = %v, want empty model_map target error", err)
	}
}

func TestConfigValidateSupportsWeightedRoundRobin(t *testing.T) {
	cfg := validConfig()
	cfg.Routing.Rules[0].Strategy = "weighted_round_robin"
	cfg.Routing.Rules[0].ToTags = []string{"mock-tag"}
	cfg.Routing.Rules[0].Weights = map[string]int{"mock-tag": 2}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConfigValidateWeightedRoundRobinRequiresWeights(t *testing.T) {
	cfg := validConfig()
	cfg.Routing.Rules[0].Strategy = "weighted_round_robin"
	cfg.Routing.Rules[0].Weights = nil

	err := cfg.Validate()
	if err == nil || err.Error() != "routing.rules[0].weights is required when strategy=weighted_round_robin" {
		t.Fatalf("Validate() error = %v, want missing weights error", err)
	}
}

func TestConfigValidateWeightedRoundRobinRequiresWeightForEachToTag(t *testing.T) {
	cfg := validConfig()
	cfg.Outbounds = append(cfg.Outbounds, OutboundSpec{Name: "mock-2", Protocol: "mock", Tag: "mock-tag-2"})
	cfg.Routing.Rules[0].Strategy = "weighted_round_robin"
	cfg.Routing.Rules[0].ToTags = []string{"mock-tag", "mock-tag-2"}
	cfg.Routing.Rules[0].Weights = map[string]int{"mock-tag": 2}

	err := cfg.Validate()
	if err == nil || err.Error() != "routing.rules[0].weights.mock-tag-2 is required" {
		t.Fatalf("Validate() error = %v, want missing per-tag weight error", err)
	}
}

func TestConfigValidateWeightedRoundRobinRejectsUnknownWeightTag(t *testing.T) {
	cfg := validConfig()
	cfg.Routing.Rules[0].Strategy = "weighted_round_robin"
	cfg.Routing.Rules[0].Weights = map[string]int{"mock-tag": 2, "other-tag": 1}

	err := cfg.Validate()
	if err == nil || err.Error() != "routing.rules[0].weights.other-tag does not match any to_tags entry" {
		t.Fatalf("Validate() error = %v, want unknown weight tag error", err)
	}
}

func TestConfigValidateWeightedRoundRobinRejectsNonPositiveWeight(t *testing.T) {
	cfg := validConfig()
	cfg.Routing.Rules[0].Strategy = "weighted_round_robin"
	cfg.Routing.Rules[0].Weights = map[string]int{"mock-tag": 0}

	err := cfg.Validate()
	if err == nil || err.Error() != "routing.rules[0].weights.mock-tag must be greater than 0" {
		t.Fatalf("Validate() error = %v, want invalid weight error", err)
	}
}

func TestConfigValidateRejectsWeightsOnNonWeightedRoundRobin(t *testing.T) {
	cfg := validConfig()
	cfg.Routing.Rules[0].Strategy = "failover"
	cfg.Routing.Rules[0].Weights = map[string]int{"mock-tag": 1}

	err := cfg.Validate()
	if err == nil || err.Error() != "routing.rules[0].weights is only supported when strategy=weighted_round_robin" {
		t.Fatalf("Validate() error = %v, want unexpected weights error", err)
	}
}

func TestConfigValidateMissingListen(t *testing.T) {
	cfg := validConfig()
	cfg.Listeners = nil
	cfg.Server.Listen = ""

	err := cfg.Validate()
	if err == nil || err.Error() != "server.listen or listeners is required" {
		t.Fatalf("Validate() error = %v, want server.listen or listeners is required", err)
	}
}

func TestConfigValidateRequiresOutbound(t *testing.T) {
	cfg := validConfig()
	cfg.Outbounds = nil

	err := cfg.Validate()
	if err == nil || err.Error() != "at least one outbound is required" {
		t.Fatalf("Validate() error = %v, want at least one outbound is required", err)
	}
}

func TestConfigValidateRequiresRoutingRule(t *testing.T) {
	cfg := validConfig()
	cfg.Routing.Rules = nil

	err := cfg.Validate()
	if err == nil || err.Error() != "at least one routing rule is required" {
		t.Fatalf("Validate() error = %v, want at least one routing rule is required", err)
	}
}

func TestConfigValidateSupportsAccountingLocalFile(t *testing.T) {
	cfg := validConfig()
	cfg.Accounting = AccountingConfig{
		Enabled:    true,
		Backend:    "local_file",
		ExposeHTTP: true,
		AdminToken: "admin-token",
		LocalFile: AccountingLocalFileConfig{
			Dir:                   t.TempDir(),
			RotateMaxSizeMB:       16,
			RetentionDays:         7,
			SnapshotRetentionDays: 7,
			WriteBufferRecords:    64,
			FlushInterval:         DurationValue("1s"),
			QueueSize:             1024,
		},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConfigValidateRequiresAccountingLocalFileDir(t *testing.T) {
	cfg := validConfig()
	cfg.Accounting = AccountingConfig{
		Enabled: true,
		Backend: "local_file",
		LocalFile: AccountingLocalFileConfig{
			RotateMaxSizeMB:    16,
			WriteBufferRecords: 64,
			FlushInterval:      DurationValue("1s"),
			QueueSize:          1024,
		},
	}

	err := cfg.Validate()
	if err == nil || err.Error() != "accounting.local_file.dir is required when accounting.backend=local_file" {
		t.Fatalf("Validate() error = %v, want missing local_file dir error", err)
	}
}

func TestConfigValidateRequiresPositiveAccountingLocalFileFlushInterval(t *testing.T) {
	cfg := validConfig()
	cfg.Accounting = AccountingConfig{
		Enabled: true,
		Backend: "local_file",
		LocalFile: AccountingLocalFileConfig{
			Dir:                t.TempDir(),
			RotateMaxSizeMB:    16,
			WriteBufferRecords: 64,
			FlushInterval:      DurationValue("0s"),
			QueueSize:          1024,
		},
	}

	err := cfg.Validate()
	if err == nil || err.Error() != "accounting.local_file.flush_interval must be a positive duration" {
		t.Fatalf("Validate() error = %v, want invalid flush_interval error", err)
	}
}
func TestConfigValidateSupportsOutboundQuotaWindows(t *testing.T) {
	cfg := validConfig()
	cfg.Outbounds[0].Quota = OutboundQuotaConfig{
		Enabled:       true,
		Cooldown:      DurationValue("10m"),
		ProbeInterval: DurationValue("1m"),
		Windows: []OutboundQuotaWindowConfig{
			{Name: "five-hour", Duration: DurationValue("5h"), MaxRequests: 100},
			{Name: "weekly", Duration: DurationValue("168h"), MaxRequests: 1000},
		},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConfigValidateRequiresOutboundQuotaWindows(t *testing.T) {
	cfg := validConfig()
	cfg.Outbounds[0].Quota = OutboundQuotaConfig{Enabled: true, Cooldown: DurationValue("10m"), ProbeInterval: DurationValue("1m")}

	err := cfg.Validate()
	if err == nil || err.Error() != "outbounds.mock.quota.windows is required when quota is enabled" {
		t.Fatalf("Validate() error = %v, want missing quota windows error", err)
	}
}

func TestConfigValidateRejectsDuplicateOutboundQuotaWindowName(t *testing.T) {
	cfg := validConfig()
	cfg.Outbounds[0].Quota = OutboundQuotaConfig{
		Enabled:       true,
		Cooldown:      DurationValue("10m"),
		ProbeInterval: DurationValue("1m"),
		Windows: []OutboundQuotaWindowConfig{
			{Name: "daily", Duration: DurationValue("24h"), MaxRequests: 100},
			{Name: "daily", Duration: DurationValue("168h"), MaxRequests: 500},
		},
	}

	err := cfg.Validate()
	if err == nil || err.Error() != "outbounds.mock.quota.windows[1].name duplicates \"daily\"" {
		t.Fatalf("Validate() error = %v, want duplicate quota window name error", err)
	}
}

func TestConfigValidateRequiresPositiveOutboundQuotaDuration(t *testing.T) {
	cfg := validConfig()
	cfg.Outbounds[0].Quota = OutboundQuotaConfig{
		Enabled:       true,
		Cooldown:      DurationValue("10m"),
		ProbeInterval: DurationValue("1m"),
		Windows:       []OutboundQuotaWindowConfig{{Name: "daily", Duration: DurationValue("0s"), MaxRequests: 100}},
	}

	err := cfg.Validate()
	if err == nil || err.Error() != "outbounds.mock.quota.windows[0].duration must be a positive duration" {
		t.Fatalf("Validate() error = %v, want invalid quota duration error", err)
	}
}

func TestConfigValidateRequiresPositiveOutboundQuotaLimit(t *testing.T) {
	cfg := validConfig()
	cfg.Outbounds[0].Quota = OutboundQuotaConfig{
		Enabled:       true,
		Cooldown:      DurationValue("10m"),
		ProbeInterval: DurationValue("1m"),
		Windows:       []OutboundQuotaWindowConfig{{Name: "daily", Duration: DurationValue("24h"), MaxRequests: 0}},
	}

	err := cfg.Validate()
	if err == nil || err.Error() != "outbounds.mock.quota.windows[0].max_requests must be greater than 0" {
		t.Fatalf("Validate() error = %v, want invalid quota limit error", err)
	}
}

func TestConfigValidateRequiresPositiveOutboundQuotaCooldown(t *testing.T) {
	cfg := validConfig()
	cfg.Outbounds[0].Quota = OutboundQuotaConfig{
		Enabled:       true,
		Cooldown:      DurationValue("0s"),
		ProbeInterval: DurationValue("1m"),
		Windows:       []OutboundQuotaWindowConfig{{Name: "daily", Duration: DurationValue("24h"), MaxRequests: 100}},
	}

	err := cfg.Validate()
	if err == nil || err.Error() != "outbounds.mock.quota.cooldown must be a positive duration" {
		t.Fatalf("Validate() error = %v, want invalid quota cooldown error", err)
	}
}

func TestConfigValidateRequiresPositiveOutboundQuotaProbeInterval(t *testing.T) {
	cfg := validConfig()
	cfg.Outbounds[0].Quota = OutboundQuotaConfig{
		Enabled:       true,
		Cooldown:      DurationValue("10m"),
		ProbeInterval: DurationValue("0s"),
		Windows:       []OutboundQuotaWindowConfig{{Name: "daily", Duration: DurationValue("24h"), MaxRequests: 100}},
	}

	err := cfg.Validate()
	if err == nil || err.Error() != "outbounds.mock.quota.probe_interval must be a positive duration" {
		t.Fatalf("Validate() error = %v, want invalid quota probe interval error", err)
	}
}

func TestLoadParsesOutboundQuotaWindows(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte("listeners:\n  - name: \"public\"\n    listen: \":8080\"\n    inbounds:\n      - \"openai-entry\"\ninbounds:\n  - name: \"openai-entry\"\n    protocol: \"openai_chat\"\n    path: \"/v1/chat/completions\"\n    clients:\n      - name: \"office-key\"\n        token: \"client-token\"\n        tag: \"office\"\nrouting:\n  rules:\n    - name: \"office-route\"\n      from_tags:\n        - \"office\"\n      to_tags:\n        - \"mock-tag\"\n      strategy: \"failover\"\noutbounds:\n  - name: \"mock\"\n    protocol: \"mock\"\n    tag: \"mock-tag\"\n    quota:\n      enabled: true\n      cooldown: \"10m\"\n      probe_interval: \"1m\"\n      windows:\n        - name: \"five-hour\"\n          duration: \"5h\"\n          max_requests: 100\n        - name: \"weekly\"\n          duration: \"168h\"\n          max_requests: 1000\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	quota := cfg.Outbounds[0].Quota
	if !quota.Enabled || quota.Cooldown != "10m" || quota.ProbeInterval != "1m" {
		t.Fatalf("quota = %#v, want enabled cooldown/probe", quota)
	}
	if len(quota.Windows) != 2 || quota.Windows[0].Name != "five-hour" || quota.Windows[1].MaxRequests != 1000 {
		t.Fatalf("quota.Windows = %#v, want parsed windows", quota.Windows)
	}
}

func TestConfigValidateSupportsUsageEstimationForOpenAIChat(t *testing.T) {
	cfg := validConfig()
	cfg.Outbounds[0] = OutboundSpec{
		Name:      "openai",
		Protocol:  "openai_chat",
		Tag:       "openai-tag",
		Endpoint:  "https://example.com/v1",
		AuthToken: "key-1",
		Capabilities: OutboundCapabilities{
			UsageEstimation:     true,
			UsageEstimationMode: "heuristic",
		},
	}
	cfg.Routing.Rules[0].ToTags = []string{"openai-tag"}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConfigValidateRejectsUsageEstimationModeWithoutEnable(t *testing.T) {
	cfg := validConfig()
	cfg.Outbounds[0] = OutboundSpec{
		Name:      "openai",
		Protocol:  "openai_chat",
		Tag:       "openai-tag",
		Endpoint:  "https://example.com/v1",
		AuthToken: "key-1",
		Capabilities: OutboundCapabilities{
			UsageEstimationMode: "heuristic",
		},
	}
	cfg.Routing.Rules[0].ToTags = []string{"openai-tag"}

	err := cfg.Validate()
	if err == nil || err.Error() != "outbounds.openai.usage_estimation_mode requires usage_estimation=true" {
		t.Fatalf("Validate() error = %v, want usage estimation enable error", err)
	}
}

func TestConfigValidateRejectsUsageEstimationForUnsupportedProtocol(t *testing.T) {
	cfg := validConfig()
	cfg.Outbounds[0] = OutboundSpec{
		Name:      "responses",
		Protocol:  "openai_responses",
		Tag:       "responses-tag",
		Endpoint:  "https://example.com/v1",
		AuthToken: "key-1",
		Capabilities: OutboundCapabilities{
			UsageEstimation:     true,
			UsageEstimationMode: "heuristic",
		},
	}
	cfg.Routing.Rules[0].ToTags = []string{"responses-tag"}

	err := cfg.Validate()
	if err == nil || err.Error() != "outbounds.responses.usage_estimation is only supported for openai_chat and anthropic_messages" {
		t.Fatalf("Validate() error = %v, want usage estimation protocol error", err)
	}
}

func TestConfigValidateRejectsUnsupportedUsageEstimationMode(t *testing.T) {
	cfg := validConfig()
	cfg.Outbounds[0] = OutboundSpec{
		Name:      "anthropic",
		Protocol:  "anthropic_messages",
		Tag:       "anthropic-tag",
		Endpoint:  "https://example.com/v1",
		AuthToken: "key-1",
		Capabilities: OutboundCapabilities{
			UsageEstimation:     true,
			UsageEstimationMode: "exact",
		},
	}
	cfg.Routing.Rules[0].ToTags = []string{"anthropic-tag"}

	err := cfg.Validate()
	if err == nil || err.Error() != "outbounds.anthropic.usage_estimation_mode \"exact\" is unsupported" {
		t.Fatalf("Validate() error = %v, want unsupported mode error", err)
	}
}

func TestConfigValidateRequiresAccountingAdminTokenWhenExposeHTTPEnabled(t *testing.T) {
	cfg := validConfig()
	cfg.Accounting = AccountingConfig{Enabled: true, Backend: "memory", ExposeHTTP: true}

	err := cfg.Validate()
	if err == nil || err.Error() != "accounting.admin_token is required when accounting.expose_http is enabled" {
		t.Fatalf("Validate() error = %v, want missing accounting admin token error", err)
	}
}

func TestConfigValidateRejectsUnsupportedAccountingBackend(t *testing.T) {
	cfg := validConfig()
	cfg.Accounting = AccountingConfig{Enabled: true, Backend: "redis"}

	err := cfg.Validate()
	if err == nil || err.Error() != "accounting.backend \"redis\" is unsupported" {
		t.Fatalf("Validate() error = %v, want unsupported accounting backend error", err)
	}
}

func TestConfigValidateSupportsMemoryAccounting(t *testing.T) {
	cfg := validConfig()
	cfg.Accounting = AccountingConfig{Enabled: true, Backend: "memory", ExposeHTTP: true, AdminToken: "admin-token"}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestLoadSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte("listeners:\n  - name: \"public\"\n    listen: \":8080\"\n    inbounds:\n      - \"openai-entry\"\ninbounds:\n  - name: \"openai-entry\"\n    protocol: \"openai_chat\"\n    path: \"/v1/chat/completions\"\n    clients:\n      - name: \"office-key\"\n        token: \"client-token\"\n        tag: \"office\"\nrouting:\n  rules:\n    - name: \"office-route\"\n      from_tags:\n        - \"office\"\n      to_tags:\n        - \"mock-tag\"\n      strategy: \"failover\"\noutbounds:\n  - name: \"mock\"\n    protocol: \"mock\"\n    tag: \"mock-tag\"\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Listeners[0].Name != "public" {
		t.Fatalf("Listeners[0].Name = %q, want public", cfg.Listeners[0].Name)
	}
	if cfg.Routing.Rules[0].Strategy != "failover" {
		t.Fatalf("Routing.Rules[0].Strategy = %q, want failover", cfg.Routing.Rules[0].Strategy)
	}
}
