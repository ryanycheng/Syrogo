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
	Name      string
	Arguments json.RawMessage
}

type ToolResult struct {
	ToolCallID string
	Content    []Segment
	IsError    bool
}

type DataPart struct {
	Format string
	Value  json.RawMessage
}

type SegmentMeta struct {
	Provider map[string]any
}

type ToolDefinition struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

type GenerateOptions struct {
	MaxTokens          int
	Stream             bool
	PreviousResponseID string
	Metadata           json.RawMessage
	ThinkingType       string
	ContextManagement  json.RawMessage
	OutputEffort       string
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
