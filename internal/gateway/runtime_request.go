package gateway

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ryanycheng/Syrogo/internal/runtime"
	"github.com/ryanycheng/Syrogo/internal/semantic"
)

type inboundMessage struct {
	Role       string            `json:"role"`
	Content    json.RawMessage   `json:"content"`
	ToolCalls  []inboundToolCall `json:"tool_calls"`
	ToolCallID string            `json:"tool_call_id"`
}

type inboundToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type inboundToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type openAIChatToolDefinition struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type inboundRequest struct {
	Model              string                  `json:"model"`
	System             json.RawMessage         `json:"system"`
	MaxTokens          int                     `json:"max_tokens"`
	Messages           []inboundMessage        `json:"messages"`
	Tools              []inboundToolDefinition `json:"tools"`
	Stream             bool                    `json:"stream"`
	PreviousResponseID string                  `json:"previous_response_id"`
	Metadata           json.RawMessage         `json:"metadata"`
	Thinking           json.RawMessage         `json:"thinking"`
	ContextManagement  json.RawMessage         `json:"context_management"`
	OutputConfig       json.RawMessage         `json:"output_config"`
}

func buildRuntimeRequestFromOpenAIChat(req inboundRequest, tools []openAIChatToolDefinition) (runtime.Request, error) {
	semanticReq, err := buildSemanticRequest(req, func() ([]semantic.ToolDefinition, error) {
		return parseOpenAIChatTools(tools)
	})
	if err != nil {
		return runtime.Request{}, err
	}
	return lowerSemanticRequest(semanticReq), nil
}

func buildRuntimeRequest(req inboundRequest) (runtime.Request, error) {
	semanticReq, err := buildSemanticRequest(req, func() ([]semantic.ToolDefinition, error) {
		return parseInboundTools(req.Tools)
	})
	if err != nil {
		return runtime.Request{}, err
	}
	return lowerSemanticRequest(semanticReq), nil
}

func buildSemanticRequest(req inboundRequest, toolsParser func() ([]semantic.ToolDefinition, error)) (semantic.Request, error) {
	instructions, systemText, err := parseInboundSystem(req.System)
	if err != nil {
		return semantic.Request{}, err
	}
	tools, err := toolsParser()
	if err != nil {
		return semantic.Request{}, err
	}

	result := semantic.Request{
		Model:        req.Model,
		Instructions: instructions,
		Tools:        tools,
		Options: semantic.GenerateOptions{
			MaxTokens:          req.MaxTokens,
			Stream:             req.Stream,
			PreviousResponseID: req.PreviousResponseID,
			Metadata:           normalizedOptionalJSONObject(req.Metadata),
			ThinkingType:       parseThinkingType(req.Thinking),
			ContextManagement:  normalizedOptionalJSONObject(req.ContextManagement),
			OutputEffort:       parseOutputEffort(req.OutputConfig),
		},
	}
	if systemText != "" && len(result.Instructions) == 0 {
		result.Instructions = []semantic.Segment{{Kind: semantic.SegmentText, Text: systemText}}
	}

	for _, msg := range req.Messages {
		msg = stripLeadingSystemReminderText(msg)
		if shouldSkipInboundMessage(msg) {
			continue
		}
		turns, err := parseInboundMessages(msg)
		if err != nil {
			return semantic.Request{}, err
		}
		result.Turns = append(result.Turns, turns...)
	}
	return result, nil
}

func parseInboundSystem(raw json.RawMessage) ([]semantic.Segment, string, error) {
	if len(raw) == 0 {
		return nil, "", nil
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return []semantic.Segment{{Kind: semantic.SegmentText, Text: text}}, text, nil
	}

	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		segments := make([]semantic.Segment, 0, len(blocks))
		parts := make([]string, 0, len(blocks))
		for _, block := range blocks {
			if block.Type == "text" {
				segments = append(segments, semantic.Segment{Kind: semantic.SegmentText, Text: block.Text})
				parts = append(parts, block.Text)
			}
		}
		if len(parts) == 0 {
			return nil, "", fmt.Errorf("system content must include at least one text block")
		}
		return segments, strings.Join(parts, "\n"), nil
	}

	return nil, "", fmt.Errorf("unsupported system content")
}

func parseOpenAIChatTools(raw []openAIChatToolDefinition) ([]semantic.ToolDefinition, error) {
	tools := make([]semantic.ToolDefinition, 0, len(raw))
	for _, tool := range raw {
		if tool.Type != "" && tool.Type != "function" {
			return nil, fmt.Errorf("unsupported tool type %q", tool.Type)
		}
		if tool.Function.Name == "" {
			return nil, fmt.Errorf("tool name is required")
		}
		inputSchema := json.RawMessage(`{}`)
		if len(tool.Function.Parameters) > 0 {
			var schema any
			if err := json.Unmarshal(tool.Function.Parameters, &schema); err != nil {
				return nil, fmt.Errorf("invalid tool parameters for %q: %w", tool.Function.Name, err)
			}
			encoded, err := json.Marshal(schema)
			if err != nil {
				return nil, fmt.Errorf("marshal tool parameters for %q: %w", tool.Function.Name, err)
			}
			inputSchema = encoded
		}
		tools = append(tools, semantic.ToolDefinition{
			Type:        "function",
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			InputSchema: inputSchema,
		})
	}
	return tools, nil
}

func parseInboundTools(raw []inboundToolDefinition) ([]semantic.ToolDefinition, error) {
	tools := make([]semantic.ToolDefinition, 0, len(raw))
	for _, tool := range raw {
		if tool.Name == "" {
			return nil, fmt.Errorf("tool name is required")
		}
		inputSchema := json.RawMessage(`{}`)
		if len(tool.InputSchema) > 0 {
			var schema any
			if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
				return nil, fmt.Errorf("invalid tool input_schema for %q: %w", tool.Name, err)
			}
			encoded, err := json.Marshal(schema)
			if err != nil {
				return nil, fmt.Errorf("marshal tool input_schema for %q: %w", tool.Name, err)
			}
			inputSchema = encoded
		}
		tools = append(tools, semantic.ToolDefinition{
			Type:        "function",
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: inputSchema,
		})
	}
	return tools, nil
}

func parseInboundMessages(msg inboundMessage) ([]semantic.Turn, error) {
	segments, toolCallID, err := parseInboundContent(msg.Role, msg.Content)
	if err != nil {
		return nil, err
	}
	for _, call := range msg.ToolCalls {
		if call.Type != "" && call.Type != "function" {
			return nil, fmt.Errorf("unsupported tool call type %q", call.Type)
		}
		segments = append(segments, semantic.Segment{Kind: semantic.SegmentToolCall, ToolCall: &semantic.ToolCall{
			ID:        call.ID,
			Name:      call.Function.Name,
			Arguments: normalizedJSONOrRaw(json.RawMessage(call.Function.Arguments)),
		}})
	}
	if msg.ToolCallID != "" {
		toolCallID = msg.ToolCallID
	}
	role := semantic.Role(msg.Role)
	if role != semantic.RoleTool && hasMultipleToolResultSegments(segments) {
		turns := make([]semantic.Turn, 0, len(segments))
		for _, segment := range segments {
			if segment.Kind == semantic.SegmentToolResult && segment.ToolResult != nil {
				turns = append(turns, semantic.Turn{Role: semantic.RoleTool, Segments: []semantic.Segment{segment}})
				continue
			}
			if len(turns) == 0 || turns[len(turns)-1].Role != role {
				turns = append(turns, semantic.Turn{Role: role})
			}
			turns[len(turns)-1].Segments = append(turns[len(turns)-1].Segments, segment)
		}
		return turns, nil
	}
	if toolCallID != "" {
		segments = normalizeToolTurnSegments(toolCallID, segments)
		role = semantic.RoleTool
	}
	return []semantic.Turn{{Role: role, Segments: segments}}, nil
}

func hasMultipleToolResultSegments(segments []semantic.Segment) bool {
	count := 0
	for _, segment := range segments {
		if segment.Kind == semantic.SegmentToolResult && segment.ToolResult != nil {
			count++
			if count > 1 {
				return true
			}
		}
	}
	return false
}

func normalizeToolTurnSegments(toolCallID string, segments []semantic.Segment) []semantic.Segment {
	if len(segments) == 1 && segments[0].Kind == semantic.SegmentToolResult && segments[0].ToolResult != nil {
		segments[0].ToolResult.ToolCallID = toolCallID
		return segments
	}
	return []semantic.Segment{{
		Kind: semantic.SegmentToolResult,
		ToolResult: &semantic.ToolResult{
			ToolCallID: toolCallID,
			Content:    cloneSegmentsAsToolResultContent(segments),
		},
	}}
}

func parseInboundContent(role string, raw json.RawMessage) ([]semantic.Segment, string, error) {
	if len(raw) == 0 {
		return []semantic.Segment{{Kind: semantic.SegmentText, Text: ""}}, "", nil
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return []semantic.Segment{{Kind: semantic.SegmentText, Text: text}}, "", nil
	}

	var blocks []struct {
		Type      string          `json:"type"`
		Text      string          `json:"text"`
		ID        string          `json:"id"`
		ToolUseID string          `json:"tool_use_id"`
		Name      string          `json:"name"`
		Input     json.RawMessage `json:"input"`
		Content   json.RawMessage `json:"content"`
		IsError   bool            `json:"is_error"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		segments := make([]semantic.Segment, 0, len(blocks))
		toolCallID := ""
		for _, block := range blocks {
			switch block.Type {
			case "text":
				segments = append(segments, semantic.Segment{Kind: semantic.SegmentText, Text: block.Text})
			case "tool_use":
				segments = append(segments, semantic.Segment{Kind: semantic.SegmentToolCall, ToolCall: &semantic.ToolCall{
					ID:        block.ID,
					Type:      "function",
					Name:      block.Name,
					Arguments: normalizedJSONOrRaw(block.Input),
				}})
			case "tool_result":
				toolCallID = block.ToolUseID
				content, err := parseToolResultContent(block.Content)
				if err != nil {
					return nil, "", err
				}
				segments = append(segments, semantic.Segment{Kind: semantic.SegmentToolResult, ToolResult: &semantic.ToolResult{
					ToolCallID:   block.ToolUseID,
					ToolCallType: "function",
					Content:      content,
					IsError:      block.IsError,
				}})
			}
		}
		if len(segments) == 0 {
			return nil, "", fmt.Errorf("message content must include at least one text or tool block")
		}
		if role == string(runtime.MessageRoleTool) && toolCallID == "" {
			return nil, "", fmt.Errorf("tool message must include tool_call_id")
		}
		return segments, toolCallID, nil
	}

	return nil, "", fmt.Errorf("unsupported message content")
}

func shouldSkipInboundMessage(msg inboundMessage) bool {
	if len(msg.ToolCalls) > 0 || msg.ToolCallID != "" {
		return false
	}
	if len(msg.Content) == 0 {
		return true
	}
	var blocks []json.RawMessage
	if err := json.Unmarshal(msg.Content, &blocks); err == nil {
		return len(blocks) == 0
	}
	return false
}

func stripLeadingSystemReminderText(msg inboundMessage) inboundMessage {
	if msg.Role != string(semantic.RoleUser) || len(msg.Content) == 0 {
		return msg
	}

	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(msg.Content, &blocks); err != nil {
		return msg
	}

	idx := 0
	for idx < len(blocks) && blocks[idx].Type == "text" && isSystemReminderText(blocks[idx].Text) {
		idx++
	}
	if idx == 0 {
		return msg
	}

	remaining, err := json.Marshal(blocks[idx:])
	if err != nil {
		return msg
	}
	msg.Content = remaining
	return msg
}

func isSystemReminderText(text string) bool {
	trimmed := strings.TrimSpace(text)
	return strings.HasPrefix(trimmed, "<system-reminder>") && strings.Contains(trimmed, "</system-reminder>")
}

func parseToolResultContent(raw json.RawMessage) ([]semantic.Segment, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return []semantic.Segment{{Kind: semantic.SegmentText, Text: text}}, nil
	}
	var blocks []struct {
		Type    string          `json:"type"`
		Text    string          `json:"text"`
		Value   json.RawMessage `json:"value"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		segments := make([]semantic.Segment, 0, len(blocks))
		for _, block := range blocks {
			if block.Type == "text" {
				segments = append(segments, semantic.Segment{Kind: semantic.SegmentText, Text: block.Text})
				continue
			}
			payload := block.Value
			if len(payload) == 0 {
				payload = block.Content
			}
			if len(payload) == 0 {
				payload = json.RawMessage(`{}`)
			}
			segments = append(segments, semantic.Segment{Kind: semantic.SegmentData, Data: &semantic.DataPart{Format: block.Type, Value: append(json.RawMessage(nil), payload...)}})
		}
		if len(segments) == 0 {
			return nil, fmt.Errorf("tool_result content must include at least one block")
		}
		return segments, nil
	}
	return []semantic.Segment{{Kind: semantic.SegmentData, Data: &semantic.DataPart{Format: "json", Value: normalizedJSONOrRaw(raw)}}}, nil
}

func lowerSemanticRequest(req semantic.Request) runtime.Request {
	result := runtime.Request{
		Model:              req.Model,
		System:             joinSegmentTexts(req.Instructions),
		MaxTokens:          req.Options.MaxTokens,
		Stream:             req.Options.Stream,
		PreviousResponseID: req.Options.PreviousResponseID,
		Metadata:           append(json.RawMessage(nil), req.Options.Metadata...),
		ThinkingType:       req.Options.ThinkingType,
		ContextManagement:  append(json.RawMessage(nil), req.Options.ContextManagement...),
		OutputEffort:       req.Options.OutputEffort,
		Messages:           make([]runtime.Message, 0, len(req.Turns)),
		Tools:              make([]runtime.ToolDefinition, 0, len(req.Tools)),
	}
	for _, tool := range req.Tools {
		result.Tools = append(result.Tools, runtime.ToolDefinition{Type: tool.Type, Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema, Format: append(json.RawMessage(nil), tool.Format...), Raw: append(json.RawMessage(nil), tool.Raw...)})
	}
	for _, turn := range req.Turns {
		result.Messages = append(result.Messages, lowerSemanticTurn(turn))
	}
	return result
}

func lowerSemanticTurn(turn semantic.Turn) runtime.Message {
	message := runtime.Message{Role: runtime.MessageRole(turn.Role)}
	for _, segment := range turn.Segments {
		switch segment.Kind {
		case semantic.SegmentText:
			message.Parts = append(message.Parts, runtime.ContentPart{Type: runtime.ContentPartTypeText, Text: segment.Text})
		case semantic.SegmentToolCall:
			if segment.ToolCall != nil {
				message.ToolCalls = append(message.ToolCalls, runtime.ToolCall{ID: segment.ToolCall.ID, Type: segment.ToolCall.Type, Name: segment.ToolCall.Name, Arguments: marshalCompactJSONOrEmpty(segment.ToolCall.Arguments), Input: segment.ToolCall.Input})
			}
		case semantic.SegmentToolResult:
			if segment.ToolResult != nil {
				message.ToolCallID = segment.ToolResult.ToolCallID
				message.ToolCallType = segment.ToolResult.ToolCallType
				message.ToolResultIsError = segment.ToolResult.IsError
				message.Parts = append(message.Parts, lowerToolResultContent(segment.ToolResult.Content)...)
			}
		case semantic.SegmentData:
			if segment.Data != nil {
				message.Parts = append(message.Parts, runtime.ContentPart{Type: runtime.ContentPartTypeJSON, Data: append(json.RawMessage(nil), segment.Data.Value...)})
			}
		}
	}
	if len(message.Parts) == 0 && len(message.ToolCalls) == 0 {
		message.Parts = append(message.Parts, runtime.ContentPart{Type: runtime.ContentPartTypeText, Text: ""})
	}
	return message
}

func lowerToolResultContent(segments []semantic.Segment) []runtime.ContentPart {
	parts := make([]runtime.ContentPart, 0, len(segments))
	for _, segment := range segments {
		switch segment.Kind {
		case semantic.SegmentText:
			parts = append(parts, runtime.ContentPart{Type: runtime.ContentPartTypeText, Text: segment.Text})
		case semantic.SegmentData:
			if segment.Data != nil {
				parts = append(parts, runtime.ContentPart{Type: runtime.ContentPartTypeJSON, Data: append(json.RawMessage(nil), segment.Data.Value...)})
			}
		}
	}
	if len(parts) == 0 {
		parts = append(parts, runtime.ContentPart{Type: runtime.ContentPartTypeText, Text: ""})
	}
	return parts
}

func cloneSegmentsAsToolResultContent(segments []semantic.Segment) []semantic.Segment {
	cloned := make([]semantic.Segment, 0, len(segments))
	for _, segment := range segments {
		copySegment := segment
		if segment.ToolCall != nil {
			copySegment.ToolCall = &semantic.ToolCall{ID: segment.ToolCall.ID, Type: segment.ToolCall.Type, Name: segment.ToolCall.Name, Arguments: append(json.RawMessage(nil), segment.ToolCall.Arguments...), Input: segment.ToolCall.Input}
		}
		if segment.ToolResult != nil {
			copySegment.ToolResult = &semantic.ToolResult{ToolCallID: segment.ToolResult.ToolCallID, ToolCallType: segment.ToolResult.ToolCallType, Content: cloneSegmentsAsToolResultContent(segment.ToolResult.Content), IsError: segment.ToolResult.IsError}
		}
		if segment.Data != nil {
			copySegment.Data = &semantic.DataPart{Format: segment.Data.Format, Value: append(json.RawMessage(nil), segment.Data.Value...)}
		}
		cloned = append(cloned, copySegment)
	}
	return cloned
}

func joinSegmentTexts(segments []semantic.Segment) string {
	parts := make([]string, 0, len(segments))
	for _, segment := range segments {
		if segment.Kind == semantic.SegmentText && segment.Text != "" {
			parts = append(parts, segment.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func normalizedJSONOrRaw(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return append(json.RawMessage(nil), raw...)
	}
	encoded, err := json.Marshal(v)
	if err != nil {
		return append(json.RawMessage(nil), raw...)
	}
	return encoded
}

func normalizedOptionalJSONObject(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return encoded
}

func parseThinkingType(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var value struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return value.Type
}

func parseOutputEffort(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var value struct {
		Effort string `json:"effort"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return value.Effort
}

func marshalCompactJSON(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "{}", nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func marshalCompactJSONOrEmpty(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	encoded, err := marshalCompactJSON(raw)
	if err != nil {
		return string(raw)
	}
	return encoded
}
