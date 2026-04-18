package eventstream

import (
	"encoding/json"

	"syrogo/internal/runtime"
)

type EventType string

type BlockType string

type StopReason string

const (
	EventTypeMessageStart      EventType = "message_start"
	EventTypeContentBlockStart EventType = "content_block_start"
	EventTypeContentBlockDelta EventType = "content_block_delta"
	EventTypeContentBlockStop  EventType = "content_block_stop"
	EventTypeMessageDelta      EventType = "message_delta"
	EventTypeMessageStop       EventType = "message_stop"
	EventTypeUsage             EventType = "usage"
	EventTypeError             EventType = "error"
)

const (
	BlockTypeText       BlockType = "text"
	BlockTypeToolUse    BlockType = "tool_use"
	BlockTypeToolResult BlockType = "tool_result"
	BlockTypeJSON       BlockType = "json"
	BlockTypeThinking   BlockType = "thinking"
)

const (
	StopReasonEndTurn  StopReason = "end_turn"
	StopReasonToolUse  StopReason = "tool_use"
	StopReasonMaxToken StopReason = "max_tokens"
	StopReasonError    StopReason = "error"
)

type RequestSnapshot struct {
	Model      string
	System     string
	MaxTokens  int
	Messages   []runtime.Message
	Tools      []runtime.ToolDefinition
	ToolChoice string
	Stream     bool
}

type ResponseSnapshot struct {
	ID           string
	Model        string
	Role         runtime.MessageRole
	Blocks       []ContentBlock
	FinishReason StopReason
	Usage        *runtime.Usage
}

type ContentBlock struct {
	Type       BlockType
	Text       string
	Data       json.RawMessage
	ToolCall   *ToolCallSnapshot
	ToolResult *ToolResultSnapshot
}

type ToolCallSnapshot struct {
	ID        string
	Name      string
	Arguments string
}

type ToolResultSnapshot struct {
	ToolCallID string
	Blocks     []ContentBlock
	IsError    bool
}

type Event struct {
	Type         EventType
	MessageID    string
	Model        string
	Role         runtime.MessageRole
	BlockIndex   int
	Block        *ContentBlock
	TextDelta    string
	ToolCall     *ToolCallSnapshot
	Usage        *runtime.Usage
	FinishReason StopReason
	Err          error
}

func StopReasonFromRuntime(reason runtime.FinishReason) StopReason {
	switch reason {
	case runtime.FinishReasonToolUse:
		return StopReasonToolUse
	case runtime.FinishReasonLength:
		return StopReasonMaxToken
	case runtime.FinishReasonError:
		return StopReasonError
	case runtime.FinishReasonEndTurn, runtime.FinishReasonStop, "":
		return StopReasonEndTurn
	default:
		return StopReasonEndTurn
	}
}

func StopReasonToRuntime(reason StopReason) runtime.FinishReason {
	switch reason {
	case StopReasonToolUse:
		return runtime.FinishReasonToolUse
	case StopReasonMaxToken:
		return runtime.FinishReasonLength
	case StopReasonError:
		return runtime.FinishReasonError
	case StopReasonEndTurn, "":
		return runtime.FinishReasonEndTurn
	default:
		return runtime.FinishReasonEndTurn
	}
}
