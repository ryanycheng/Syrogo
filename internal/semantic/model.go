package semantic

import "encoding/json"

type Role string

type SegmentKind string

type Request struct {
	Model        string
	Instructions []Segment
	Turns        []Turn
	Tools        []ToolDefinition
	Options      GenerateOptions
}

type Turn struct {
	Role     Role
	Segments []Segment
}

type Segment struct {
	Kind       SegmentKind
	Text       string
	ToolCall   *ToolCall
	ToolResult *ToolResult
	Data       *DataPart
	Meta       SegmentMeta
}

type ToolCall struct {
	ID        string
	Type      string
	Name      string
	Arguments json.RawMessage
	Input     string
}

type ToolResult struct {
	ToolCallID   string
	ToolCallType string
	Content      []Segment
	IsError      bool
}

type DataPart struct {
	Format string
	Value  json.RawMessage
}

type SegmentMeta struct {
	Provider map[string]any
}

type ToolDefinition struct {
	Type        string
	Name        string
	Description string
	InputSchema json.RawMessage
	Format      json.RawMessage
	Raw         json.RawMessage
}

type GenerateOptions struct {
	MaxTokens          int
	Stream             bool
	PreviousResponseID string
	Metadata           json.RawMessage
	ThinkingType       string
	ContextManagement  json.RawMessage
	OutputEffort       string
	ToolChoice         json.RawMessage
}

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

const (
	SegmentText       SegmentKind = "text"
	SegmentToolCall   SegmentKind = "tool_call"
	SegmentToolResult SegmentKind = "tool_result"
	SegmentData       SegmentKind = "data"
)
