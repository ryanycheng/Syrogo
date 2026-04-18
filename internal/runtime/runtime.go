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

	RoutingStrategyFailover   RoutingStrategy = "failover"
	RoutingStrategyRoundRobin RoutingStrategy = "round_robin"

	ContextKeyRequestID contextKey = "request_id"
)

type ContentPart struct {
	Type ContentPartType
	Text string
	Data json.RawMessage
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

type ToolDefinition struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

type Message struct {
	Role              MessageRole
	Parts             []ContentPart
	ToolCalls         []ToolCall
	ToolCallID        string
	ToolResultIsError bool
}

type Request struct {
	Model     string
	System    string
	MaxTokens int
	Messages  []Message
	Tools     []ToolDefinition
	Stream    bool
}

type Usage struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
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
	InboundName     string
	InboundProtocol string
	ActiveTag       string
}

type ExecutionStep struct {
	Type           StepType
	OutboundName   string
	OutboundTarget CompletionProvider
	Model          string
	OnError        FallbackCondition
}

type ExecutionPlan struct {
	MatchedRule    string
	Strategy       RoutingStrategy
	ResolvedToTags []string
	Steps          []ExecutionStep
}
