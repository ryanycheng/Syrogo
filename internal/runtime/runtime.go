package runtime

import "context"

type ContentPartType string

type MessageRole string

type StepType string

type FallbackCondition string

type FinishReason string

type StreamEventType string

const (
	ContentPartTypeText ContentPartType = "text"

	MessageRoleSystem    MessageRole = "system"
	MessageRoleUser      MessageRole = "user"
	MessageRoleAssistant MessageRole = "assistant"
	MessageRoleTool      MessageRole = "tool"

	StepTypeOutbound StepType = "outbound"

	FallbackAlways          FallbackCondition = "always"
	FallbackOnRetryable     FallbackCondition = "retryable"
	FallbackOnQuotaExceeded FallbackCondition = "quota_exceeded"

	FinishReasonStop   FinishReason = "stop"
	FinishReasonLength FinishReason = "length"
	FinishReasonError  FinishReason = "error"

	StreamEventMessageStart StreamEventType = "message_start"
	StreamEventContentDelta StreamEventType = "content_delta"
	StreamEventMessageEnd   StreamEventType = "message_end"
	StreamEventUsage        StreamEventType = "usage"
	StreamEventError        StreamEventType = "error"
)

type ContentPart struct {
	Type ContentPartType
	Text string
}

type Message struct {
	Role  MessageRole
	Parts []ContentPart
}

type Request struct {
	Model    string
	Messages []Message
	Stream   bool
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
	Type         StreamEventType
	ResponseID   string
	Model        string
	MessageRole  MessageRole
	Delta        *ContentPart
	FinishReason FinishReason
	Usage        *Usage
	Err          error
}

type CompletionProvider interface {
	Name() string
	ChatCompletion(ctx context.Context, req Request) (Response, error)
	StreamCompletion(ctx context.Context, req Request) (<-chan StreamEvent, error)
}

type RouteContext struct {
	Request       Request
	InboundName   string
	InboundType   string
	InboundLabels map[string]string
}

type ExecutionStep struct {
	Type           StepType
	ProviderName   string
	ProviderTarget CompletionProvider
	Model          string
	OnError        FallbackCondition
}

type ExecutionPlan struct {
	MatchedRoute string
	Steps        []ExecutionStep
}
