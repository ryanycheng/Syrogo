package gateway

import (
	"encoding/json"
	"fmt"
	"strings"

	"syrogo/internal/runtime"
	"syrogo/internal/semantic"
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

type inboundRequest struct {
	Model     string                  `json:"model"`
	System    json.RawMessage         `json:"system"`
	MaxTokens int                     `json:"max_tokens"`
	Messages  []inboundMessage        `json:"messages"`
	Tools     []inboundToolDefinition `json:"tools"`
	Stream    bool                    `json:"stream"`
}

func buildRuntimeRequest(req inboundRequest) (runtime.Request, error) {
	semanticReq, err := buildSemanticRequest(req)
	if err != nil {
		return runtime.Request{}, err
	}
	return lowerSemanticRequest(semanticReq), nil
}

func buildSemanticRequest(req inboundRequest) (semantic.Request, error) {
	instructions, systemText, err := parseInboundSystem(req.System)
	if err != nil {
		return semantic.Request{}, err
	}
	tools, err := parseInboundTools(req.Tools)
	if err != nil {
		return semantic.Request{}, err
	}

	result := semantic.Request{
		Model:        req.Model,
		Instructions: instructions,
		Tools:        tools,
		Options: semantic.GenerateOptions{
			MaxTokens: req.MaxTokens,
			Stream:    req.Stream,
		},
	}
	if systemText != "" && len(result.Instructions) == 0 {
		result.Instructions = []semantic.Segment{{Kind: semantic.SegmentText, Text: systemText}}
	}

	for _, msg := range req.Messages {
		turn, err := parseInboundMessage(msg)
		if err != nil {
			return semantic.Request{}, err
		}
		result.Turns = append(result.Turns, turn)
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
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: inputSchema,
		})
	}
	return tools, nil
}

func parseInboundMessage(msg inboundMessage) (semantic.Turn, error) {
	segments, toolCallID, err := parseInboundContent(msg.Role, msg.Content)
	if err != nil {
		return semantic.Turn{}, err
	}
	for _, call := range msg.ToolCalls {
		if call.Type != "" && call.Type != "function" {
			return semantic.Turn{}, fmt.Errorf("unsupported tool call type %q", call.Type)
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
	turn := semantic.Turn{Role: semantic.Role(msg.Role), Segments: segments}
	if toolCallID != "" {
		turn.Segments = normalizeToolTurnSegments(toolCallID, turn.Segments)
	}
	return turn, nil
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
					ToolCallID: block.ToolUseID,
					Content:    content,
					IsError:    block.IsError,
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
		Model:     req.Model,
		System:    joinSegmentTexts(req.Instructions),
		MaxTokens: req.Options.MaxTokens,
		Stream:    req.Options.Stream,
		Messages:  make([]runtime.Message, 0, len(req.Turns)),
		Tools:     make([]runtime.ToolDefinition, 0, len(req.Tools)),
	}
	for _, tool := range req.Tools {
		result.Tools = append(result.Tools, runtime.ToolDefinition{Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema})
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
				message.ToolCalls = append(message.ToolCalls, runtime.ToolCall{ID: segment.ToolCall.ID, Name: segment.ToolCall.Name, Arguments: marshalCompactJSONOrEmpty(segment.ToolCall.Arguments)})
			}
		case semantic.SegmentToolResult:
			if segment.ToolResult != nil {
				message.ToolCallID = segment.ToolResult.ToolCallID
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
			copySegment.ToolCall = &semantic.ToolCall{ID: segment.ToolCall.ID, Name: segment.ToolCall.Name, Arguments: append(json.RawMessage(nil), segment.ToolCall.Arguments...)}
		}
		if segment.ToolResult != nil {
			copySegment.ToolResult = &semantic.ToolResult{ToolCallID: segment.ToolResult.ToolCallID, Content: cloneSegmentsAsToolResultContent(segment.ToolResult.Content), IsError: segment.ToolResult.IsError}
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
