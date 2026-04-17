package provider

import (
	"encoding/json"
	"fmt"

	"syrogo/internal/runtime"
)

type openAIChatMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type openAIChatRequest struct {
	Model      string                 `json:"model"`
	Messages   []openAIChatMessage    `json:"messages"`
	MaxTokens  int                    `json:"max_tokens,omitempty"`
	Tools      []openAIToolDefinition `json:"tools,omitempty"`
	ToolChoice string                 `json:"tool_choice,omitempty"`
}

type openAIToolDefinition struct {
	Type     string                   `json:"type"`
	Function openAIToolSpecDefinition `json:"function"`
}

type openAIToolSpecDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
	Strict      bool            `json:"strict,omitempty"`
}

type openAIToolCall struct {
	ID       string                 `json:"id,omitempty"`
	Type     string                 `json:"type"`
	Function openAIToolCallFunction `json:"function"`
}

type openAIToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openAIChatResponseEnvelope struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Model   string `json:"model"`
	Choices []struct {
		Message openAIChatMessage `json:"message"`
	} `json:"choices"`
}

func encodeOpenAIChatRequest(req runtime.Request) any {
	messages := make([]openAIChatMessage, 0, len(req.Messages)+1)
	if req.System != "" {
		messages = append(messages, openAIChatMessage{
			Role:    string(runtime.MessageRoleSystem),
			Content: req.System,
		})
	}
	for _, msg := range req.Messages {
		encoded := openAIChatMessage{
			Role:       string(msg.Role),
			Content:    joinedTextParts(msg),
			ToolCallID: msg.ToolCallID,
		}
		if len(msg.ToolCalls) > 0 {
			encoded.ToolCalls = make([]openAIToolCall, 0, len(msg.ToolCalls))
			for _, call := range msg.ToolCalls {
				encoded.ToolCalls = append(encoded.ToolCalls, openAIToolCall{
					ID:   call.ID,
					Type: "function",
					Function: openAIToolCallFunction{
						Name:      call.Name,
						Arguments: call.Arguments,
					},
				})
			}
		}
		messages = append(messages, encoded)
	}

	payload := openAIChatRequest{
		Model:    req.Model,
		Messages: messages,
	}
	if req.MaxTokens > 0 {
		payload.MaxTokens = req.MaxTokens
	}
	if len(req.Tools) > 0 {
		payload.Tools = make([]openAIToolDefinition, 0, len(req.Tools))
		for _, tool := range req.Tools {
			if shouldDropOpenAIChatTool(tool) {
				continue
			}
			payload.Tools = append(payload.Tools, openAIToolDefinition{
				Type: "function",
				Function: openAIToolSpecDefinition{
					Name:        tool.Name,
					Description: tool.Description,
					Parameters:  normalizedToolSchema(tool.InputSchema),
					Strict:      true,
				},
			})
		}
		if len(payload.Tools) > 0 {
			payload.ToolChoice = "auto"
		}
	}
	return payload
}

func shouldDropOpenAIChatTool(tool runtime.ToolDefinition) bool {
	if _, ok := claudeCodeControlToolNames[tool.Name]; ok {
		return true
	}
	if isClaudeCodeBuiltinToolName(tool.Name) {
		return true
	}

	var schema map[string]any
	if len(tool.InputSchema) > 0 && json.Unmarshal(tool.InputSchema, &schema) == nil {
		if kind, _ := schema["type"].(string); kind != "" && kind != "object" {
			return true
		}
	}

	return false
}

func isClaudeCodeBuiltinToolName(name string) bool {
	switch name {
	case "Agent", "AskUserQuestion", "Bash", "CronCreate", "CronDelete", "CronList", "Edit", "EnterPlanMode", "EnterWorktree", "ExitPlanMode", "ExitWorktree", "Glob", "Grep", "LSP", "NotebookEdit", "Read", "ScheduleWakeup", "Skill", "TaskCreate", "TaskGet", "TaskList", "TaskOutput", "TaskStop", "TaskUpdate", "WebFetch", "WebSearch", "Write", "multi_tool_use", "parallel":
		return true
	default:
		return false
	}
}

func decodeOpenAIChatResponse(resp openAIChatResponseEnvelope) (runtime.Response, error) {
	if len(resp.Choices) == 0 {
		return runtime.Response{}, NewFatalError(fmt.Errorf("upstream response missing choices"))
	}

	message := runtime.Message{Role: runtime.MessageRole(resp.Choices[0].Message.Role)}
	if resp.Choices[0].Message.Content != "" {
		message.Parts = []runtime.ContentPart{{
			Type: runtime.ContentPartTypeText,
			Text: resp.Choices[0].Message.Content,
		}}
	}
	if len(resp.Choices[0].Message.ToolCalls) > 0 {
		message.ToolCalls = make([]runtime.ToolCall, 0, len(resp.Choices[0].Message.ToolCalls))
		for _, call := range resp.Choices[0].Message.ToolCalls {
			message.ToolCalls = append(message.ToolCalls, runtime.ToolCall{
				ID:        call.ID,
				Name:      call.Function.Name,
				Arguments: call.Function.Arguments,
			})
		}
	}
	if len(message.Parts) == 0 && len(message.ToolCalls) == 0 {
		return runtime.Response{}, NewFatalError(fmt.Errorf("upstream returned no content and no tool calls"))
	}

	return runtime.Response{
		ID:           resp.ID,
		Object:       resp.Object,
		Model:        resp.Model,
		FinishReason: runtime.FinishReasonStop,
		Message:      message,
	}, nil
}
