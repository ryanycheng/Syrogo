package runtime

import (
	"context"
	"encoding/json"
)

type ContentPartType string

type MessageRole string

type StepType string

type FallbackCondition string

type FinishReason string

type StreamEventType string

type RoutingStrategy string

type UsageSource string

type UsageStatus string

type contextKey string

const (
	ContentPartTypeText ContentPartType = "text"
	ContentPartTypeJSON ContentPartType = "json"

	MessageRoleSystem    MessageRole = "system"
	MessageRoleUser      MessageRole = "user"
	MessageRoleAssistant MessageRole = "assistant"
	MessageRoleTool      MessageRole = "tool"

	StepTypeOutbound StepType = "outbound"

	FallbackAlways          FallbackCondition = "always"
	FallbackOnRetryable     FallbackCondition = "retryable"
	FallbackOnQuotaExceeded FallbackCondition = "quota_exceeded"

	FinishReasonStop    FinishReason = "stop"
	FinishReasonLength  FinishReason = "length"
	FinishReasonError   FinishReason = "error"
	FinishReasonToolUse FinishReason = "tool_use"
	FinishReasonEndTurn FinishReason = "end_turn"

	StreamEventMessageStart StreamEventType = "message_start"
	StreamEventContentDelta StreamEventType = "content_delta"
	StreamEventMessageEnd   StreamEventType = "message_end"
	StreamEventUsage        StreamEventType = "usage"
	StreamEventError        StreamEventType = "error"

	RoutingStrategyFailover           RoutingStrategy = "failover"
	RoutingStrategyRoundRobin         RoutingStrategy = "round_robin"
	RoutingStrategyWeightedRoundRobin RoutingStrategy = "weighted_round_robin"

	UsageSourceProvider    UsageSource = "provider"
	UsageSourceProviderAPI UsageSource = "provider_count_api"
	UsageSourceEstimated   UsageSource = "estimated"

	UsageStatusSuccess UsageStatus = "success"
	UsageStatusError   UsageStatus = "error"

	ContextKeyRequestID contextKey = "request_id"
	ContextKeySessionID contextKey = "session_id"
	ContextKeyAgent     contextKey = "agent"
)

type ContentPart struct {
	Type ContentPartType
	Text string
	Data json.RawMessage
}

type ToolCall struct {
	ID        string
	Type      string
	Name      string
	Arguments string
	Input     string
}

type ToolDefinition struct {
	Type        string
	Name        string
	Description string
	InputSchema json.RawMessage
	Format      json.RawMessage
	Raw         json.RawMessage
}

type Message struct {
	Role              MessageRole
	Parts             []ContentPart
	ToolCalls         []ToolCall
	ToolCallID        string
	ToolCallType      string
	ToolResultIsError bool
}

type Request struct {
	Model              string
	System             string
	MaxTokens          int
	Messages           []Message
	Tools              []ToolDefinition
	ToolChoice         json.RawMessage
	Stream             bool
	PreviousResponseID string
	Metadata           json.RawMessage
	ThinkingType       string
	ContextManagement  json.RawMessage
	OutputEffort       string
}

type Usage struct {
	InputTokens            int
	OutputTokens           int
	CachedInputReadTokens  int
	CachedInputWriteTokens int
	TotalTokens            int
	Source                 UsageSource
}

type UsageBreakdown struct {
	RequestCount           int
	InputTokens            int
	OutputTokens           int
	CachedInputReadTokens  int
	CachedInputWriteTokens int
	TotalTokens            int
	ToolUnits              map[string]float64
}

type UsageRecord struct {
	RequestID        string
	ClientName       string
	InboundName      string
	InboundProtocol  string
	ActiveTag        string
	MatchedRule      string
	Strategy         RoutingStrategy
	OutboundName     string
	OutboundProtocol string
	ProviderName     string
	RequestedModel   string
	ExecutedModel    string
	UsageSource      UsageSource
	Status           UsageStatus
	ErrorKind        string
	SessionID        string
	Agent            string
	CostUSD          float64
	Priced           bool `json:"-"`
	Breakdown        UsageBreakdown
	StartedAt        string
	FinishedAt       string
	LatencyMs        int64
	FallbackCount    int
}

type Response struct {
	ID           string
	Object       string
	Model        string
	Message      Message
	FinishReason FinishReason
	Usage        *Usage
}

type StreamEvent struct {
	Type          StreamEventType
	ResponseID    string
	Model         string
	MessageRole   MessageRole
	Delta         *ContentPart
	ToolCall      *ToolCall
	ToolCallIndex int
	FinishReason  FinishReason
	Usage         *Usage
	Err           error
}

type CompletionProvider interface {
	Name() string
	ChatCompletion(ctx context.Context, req Request) (Response, error)
	StreamCompletion(ctx context.Context, req Request) (<-chan StreamEvent, error)
}

type RouteContext struct {
	Request         Request
	ClientName      string
	InboundName     string
	InboundProtocol string
	ActiveTag       string
}

type ExecutionStep struct {
	Type             StepType
	OutboundName     string
	OutboundProtocol string
	OutboundTarget   CompletionProvider
	Model            string
	ModelUnavailable bool
	OnError          FallbackCondition
}

type ExecutionPlan struct {
	ClientName      string
	InboundName     string
	InboundProtocol string
	ActiveTag       string
	RequestedModel  string
	MatchedRule     string
	Strategy        RoutingStrategy
	ResolvedToTags  []string
	Steps           []ExecutionStep
}
