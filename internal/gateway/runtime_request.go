package gateway

import (
	"encoding/json"
	"fmt"
	"strings"

	"syrogo/internal/runtime"
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

type openAIResponsesRequest struct {
	Model  string          `json:"model"`
	Input  json.RawMessage `json:"input"`
	Stream bool            `json:"stream"`
}

type openAIResponsesInputItem struct {
	Type    string          `json:"type"`
	Role    string          `json:"role,omitempty"`
	Content json.RawMessage `json:"content,omitempty"`
	CallID  string          `json:"call_id,omitempty"`
	Name    string          `json:"name,omitempty"`
	Input   json.RawMessage `json:"input,omitempty"`
	Output  json.RawMessage `json:"output,omitempty"`
}

type openAIResponsesContentPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

func buildRuntimeRequest(req inboundRequest) (runtime.Request, error) {
	system, err := parseInboundSystem(req.System)
	if err != nil {
		return runtime.Request{}, err
	}
	tools, err := parseInboundTools(req.Tools)
	if err != nil {
		return runtime.Request{}, err
	}

	internalReq := runtime.Request{
		Model:     req.Model,
		System:    system,
		MaxTokens: req.MaxTokens,
		Messages:  make([]runtime.Message, 0, len(req.Messages)),
		Tools:     tools,
		Stream:    req.Stream,
	}

	for _, msg := range req.Messages {
		parts, toolCalls, toolCallID, err := parseInboundMessage(msg)
		if err != nil {
			return runtime.Request{}, err
		}
		internalReq.Messages = append(internalReq.Messages, runtime.Message{
			Role:       runtime.MessageRole(msg.Role),
			Parts:      parts,
			ToolCalls:  toolCalls,
			ToolCallID: toolCallID,
		})
	}

	return internalReq, nil
}

func parseInboundSystem(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}

	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		parts := make([]string, 0, len(blocks))
		for _, block := range blocks {
			if block.Type == "text" {
				parts = append(parts, block.Text)
			}
		}
		if len(parts) == 0 {
			return "", fmt.Errorf("system content must include at least one text block")
		}
		return strings.Join(parts, "\n"), nil
	}

	return "", fmt.Errorf("unsupported system content")
}

func parseInboundTools(raw []inboundToolDefinition) ([]runtime.ToolDefinition, error) {
	tools := make([]runtime.ToolDefinition, 0, len(raw))
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
		tools = append(tools, runtime.ToolDefinition{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: inputSchema,
		})
	}
	return tools, nil
}

func buildRuntimeRequestFromResponses(req openAIResponsesRequest) (runtime.Request, error) {
	messages, err := parseOpenAIResponsesInput(req.Input)
	if err != nil {
		return runtime.Request{}, err
	}
	return runtime.Request{Model: req.Model, Messages: messages, Stream: req.Stream}, nil
}

func parseInboundMessage(msg inboundMessage) ([]runtime.ContentPart, []runtime.ToolCall, string, error) {
	parts, toolCalls, toolCallID, err := parseInboundContent(msg.Role, msg.Content)
	if err != nil {
		return nil, nil, "", err
	}
	if len(msg.ToolCalls) > 0 {
		toolCalls = make([]runtime.ToolCall, 0, len(msg.ToolCalls))
		for _, call := range msg.ToolCalls {
			if call.Type != "" && call.Type != "function" {
				return nil, nil, "", fmt.Errorf("unsupported tool call type %q", call.Type)
			}
			toolCalls = append(toolCalls, runtime.ToolCall{
				ID:        call.ID,
				Name:      call.Function.Name,
				Arguments: call.Function.Arguments,
			})
		}
	}
	if msg.ToolCallID != "" {
		toolCallID = msg.ToolCallID
	}
	return parts, toolCalls, toolCallID, nil
}

func parseInboundContent(role string, raw json.RawMessage) ([]runtime.ContentPart, []runtime.ToolCall, string, error) {
	if len(raw) == 0 {
		return []runtime.ContentPart{{Type: runtime.ContentPartTypeText, Text: ""}}, nil, "", nil
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return []runtime.ContentPart{{Type: runtime.ContentPartTypeText, Text: text}}, nil, "", nil
	}

	var blocks []struct {
		Type      string          `json:"type"`
		Text      string          `json:"text"`
		ID        string          `json:"id"`
		ToolUseID string          `json:"tool_use_id"`
		Name      string          `json:"name"`
		Input     json.RawMessage `json:"input"`
		Content   json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		parts := make([]runtime.ContentPart, 0, len(blocks))
		toolCalls := make([]runtime.ToolCall, 0, len(blocks))
		toolCallID := ""
		for _, block := range blocks {
			switch block.Type {
			case "text":
				parts = append(parts, runtime.ContentPart{Type: runtime.ContentPartTypeText, Text: block.Text})
			case "tool_use":
				arguments, err := marshalCompactJSON(block.Input)
				if err != nil {
					return nil, nil, "", fmt.Errorf("invalid tool_use input: %w", err)
				}
				toolCalls = append(toolCalls, runtime.ToolCall{ID: block.ID, Name: block.Name, Arguments: arguments})
			case "tool_result":
				toolCallID = block.ToolUseID
				resultText, err := parseToolResultContent(block.Content)
				if err != nil {
					return nil, nil, "", err
				}
				parts = append(parts, runtime.ContentPart{Type: runtime.ContentPartTypeText, Text: resultText})
			}
		}
		if len(parts) == 0 && len(toolCalls) == 0 {
			return nil, nil, "", fmt.Errorf("message content must include at least one text or tool block")
		}
		if role == string(runtime.MessageRoleTool) && toolCallID == "" {
			return nil, nil, "", fmt.Errorf("tool message must include tool_call_id")
		}
		return parts, toolCalls, toolCallID, nil
	}

	return nil, nil, "", fmt.Errorf("unsupported message content")
}

func parseOpenAIResponsesInput(raw json.RawMessage) ([]runtime.Message, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return []runtime.Message{{
			Role:  runtime.MessageRoleUser,
			Parts: []runtime.ContentPart{{Type: runtime.ContentPartTypeText, Text: text}},
		}}, nil
	}

	var items []openAIResponsesInputItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("unsupported responses input")
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("input is required")
	}

	messages := make([]runtime.Message, 0, len(items))
	for _, item := range items {
		switch item.Type {
		case "message":
			parts, err := parseOpenAIResponsesMessageContent(item.Content)
			if err != nil {
				return nil, err
			}
			role := runtime.MessageRole(item.Role)
			if role == "" {
				role = runtime.MessageRoleUser
			}
			messages = append(messages, runtime.Message{Role: role, Parts: parts})
		case "function_call":
			arguments, err := marshalCompactJSON(item.Input)
			if err != nil {
				return nil, fmt.Errorf("invalid function_call input: %w", err)
			}
			messages = append(messages, runtime.Message{
				Role: runtime.MessageRoleAssistant,
				ToolCalls: []runtime.ToolCall{{
					ID:        item.CallID,
					Name:      item.Name,
					Arguments: arguments,
				}},
			})
		case "function_call_output":
			output, err := parseOpenAIResponsesFunctionOutput(item.Output)
			if err != nil {
				return nil, err
			}
			if item.CallID == "" {
				return nil, fmt.Errorf("function_call_output.call_id is required")
			}
			messages = append(messages, runtime.Message{
				Role:       runtime.MessageRoleTool,
				ToolCallID: item.CallID,
				Parts:      []runtime.ContentPart{{Type: runtime.ContentPartTypeText, Text: output}},
			})
		default:
			return nil, fmt.Errorf("unsupported responses input item type %q", item.Type)
		}
	}
	return messages, nil
}

func parseOpenAIResponsesMessageContent(raw json.RawMessage) ([]runtime.ContentPart, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return []runtime.ContentPart{{Type: runtime.ContentPartTypeText, Text: text}}, nil
	}

	var parts []openAIResponsesContentPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, fmt.Errorf("unsupported responses message content")
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("message content must include at least one text part")
	}

	result := make([]runtime.ContentPart, 0, len(parts))
	for _, part := range parts {
		if part.Type != "input_text" && part.Type != "output_text" && part.Type != "text" {
			return nil, fmt.Errorf("unsupported responses content part type %q", part.Type)
		}
		result = append(result, runtime.ContentPart{Type: runtime.ContentPartTypeText, Text: part.Text})
	}
	return result, nil
}

func parseOpenAIResponsesFunctionOutput(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}
	var parts []openAIResponsesContentPart
	if err := json.Unmarshal(raw, &parts); err == nil {
		texts := make([]string, 0, len(parts))
		for _, part := range parts {
			if part.Type == "output_text" || part.Type == "text" || part.Type == "input_text" {
				texts = append(texts, part.Text)
			}
		}
		if len(texts) == 0 {
			return "", fmt.Errorf("function_call_output.output must include at least one text part")
		}
		return strings.Join(texts, "\n"), nil
	}
	return "", fmt.Errorf("unsupported function_call_output.output")
}

func parseToolResultContent(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		parts := make([]string, 0, len(blocks))
		for _, block := range blocks {
			if block.Type == "text" {
				parts = append(parts, block.Text)
			}
		}
		if len(parts) == 0 {
			return "", fmt.Errorf("tool_result content must include at least one text block")
		}
		return strings.Join(parts, "\n"), nil
	}
	return "", fmt.Errorf("unsupported tool_result content")
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
