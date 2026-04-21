package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ryanycheng/Syrogo/internal/config"
	"github.com/ryanycheng/Syrogo/internal/runtime"
)

func boolPtr(v bool) *bool {
	return &v
}

func TestEncodeOpenAIChatRequestUsesJoinedTextParts(t *testing.T) {
	payload := encodeOpenAIChatRequest(runtime.Request{
		Model: "gpt-4o-mini",
		Messages: []runtime.Message{{
			Role: runtime.MessageRoleUser,
			Parts: []runtime.ContentPart{{
				Type: runtime.ContentPartTypeText,
				Text: "hello",
			}, {
				Type: runtime.ContentPartTypeText,
				Text: "ignored",
			}},
		}},
	})

	body, ok := payload.(openAIChatRequest)
	if !ok {
		t.Fatalf("payload type = %T, want openAIChatRequest", payload)
	}
	if body.Model != "gpt-4o-mini" {
		t.Fatalf("payload model = %#v, want gpt-4o-mini", body.Model)
	}
	messages := body.Messages
	if len(messages) != 1 || messages[0].Role != "user" || messages[0].Content != "hello\nignored" {
		t.Fatalf("payload messages = %#v, want single user joined message", messages)
	}
}

func TestEncodeOpenAIChatRequestUsesSystemJoinedTextPartsAndMaxTokens(t *testing.T) {
	payload := encodeOpenAIChatRequest(runtime.Request{
		Model:     "gpt-4o-mini",
		System:    "follow system",
		MaxTokens: 512,
		Messages: []runtime.Message{{
			Role: runtime.MessageRoleUser,
			Parts: []runtime.ContentPart{{
				Type: runtime.ContentPartTypeText,
				Text: "hello",
			}, {
				Type: runtime.ContentPartTypeText,
				Text: "world",
			}},
		}},
	})

	body, ok := payload.(openAIChatRequest)
	if !ok {
		t.Fatalf("payload type = %T, want openAIChatRequest", payload)
	}
	if body.MaxTokens != 512 {
		t.Fatalf("payload max_tokens = %#v, want 512", body.MaxTokens)
	}
	messages := body.Messages
	if len(messages) != 2 {
		t.Fatalf("len(messages) = %d, want 2", len(messages))
	}
	if messages[0].Role != "system" || messages[0].Content != "follow system" {
		t.Fatalf("messages[0] = %#v, want system message", messages[0])
	}
	if messages[1].Role != "user" || messages[1].Content != "hello\nworld" {
		t.Fatalf("messages[1] = %#v, want joined user text", messages[1])
	}
}

func TestEncodeOpenAIChatRequestIncludesToolDefinitions(t *testing.T) {
	payload := encodeOpenAIChatRequest(runtime.Request{
		Model: "gpt-4o-mini",
		Tools: []runtime.ToolDefinition{{
			Name:        "get_weather",
			Description: "Query weather by city",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`),
		}},
		Messages: []runtime.Message{{
			Role:  runtime.MessageRoleUser,
			Parts: []runtime.ContentPart{{Type: runtime.ContentPartTypeText, Text: "hello"}},
		}},
	})

	body, ok := payload.(openAIChatRequest)
	if !ok {
		t.Fatalf("payload type = %T, want openAIChatRequest", payload)
	}
	tools := body.Tools
	if len(tools) != 1 {
		t.Fatalf("len(tools) = %d, want 1", len(tools))
	}
	if string(body.ToolChoice) != `"auto"` {
		t.Fatalf("body.ToolChoice = %s, want \"auto\"", string(body.ToolChoice))
	}
	if tools[0].Type != "function" || tools[0].Function.Name != "get_weather" || tools[0].Function.Description != "Query weather by city" {
		t.Fatalf("tools[0] = %#v, want encoded tool definition", tools[0])
	}
	var params map[string]any
	if err := json.Unmarshal(tools[0].Function.Parameters, &params); err != nil {
		t.Fatalf("json.Unmarshal(tools[0].Function.Parameters) error = %v", err)
	}
	if params["type"] != "object" {
		t.Fatalf("params[type] = %#v, want object", params["type"])
	}
}

func TestEncodeOpenAIChatRequestKeepsClaudeCodeBuiltinTools(t *testing.T) {
	payload := encodeOpenAIChatRequest(runtime.Request{
		Model: "gpt-4o-mini",
		Tools: []runtime.ToolDefinition{{
			Name:        "CronCreate",
			Description: "Claude Code control scheduler",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"cron":{"type":"string"}},"required":["cron"]}`),
		}, {
			Name:        "Read",
			Description: "Read file contents",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"file_path":{"type":"string"}},"required":["file_path"]}`),
		}, {
			Name:        "TodoWrite",
			Description: "Manage todo list",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"todos":{"type":"array"}},"required":["todos"]}`),
		}, {
			Name:        "get_weather",
			Description: "Query weather by city",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`),
		}},
	})

	body, ok := payload.(openAIChatRequest)
	if !ok {
		t.Fatalf("payload type = %T, want openAIChatRequest", payload)
	}
	if len(body.Tools) != 4 {
		t.Fatalf("len(body.Tools) = %d, want 4", len(body.Tools))
	}
	if body.Tools[0].Function.Name != "CronCreate" || body.Tools[1].Function.Name != "Read" || body.Tools[2].Function.Name != "TodoWrite" || body.Tools[3].Function.Name != "get_weather" {
		t.Fatalf("body.Tools = %#v, want builtin tools preserved", body.Tools)
	}
	if string(body.ToolChoice) != `"auto"` {
		t.Fatalf("body.ToolChoice = %s, want \"auto\"", string(body.ToolChoice))
	}
}

func TestEncodeOpenAIChatRequestDropsNonObjectSchemaTools(t *testing.T) {
	payload := encodeOpenAIChatRequest(runtime.Request{
		Model: "gpt-4o-mini",
		Tools: []runtime.ToolDefinition{{
			Name:        "bad_tool",
			Description: "Has array schema",
			InputSchema: json.RawMessage(`{"type":"array","items":{"type":"string"}}`),
		}},
	})

	body, ok := payload.(openAIChatRequest)
	if !ok {
		t.Fatalf("payload type = %T, want openAIChatRequest", payload)
	}
	if len(body.Tools) != 0 {
		t.Fatalf("len(body.Tools) = %d, want 0", len(body.Tools))
	}
	if len(body.ToolChoice) != 0 {
		t.Fatalf("body.ToolChoice = %s, want empty", string(body.ToolChoice))
	}
}

func TestEncodeOpenAIChatRequestPreservesToolCallingFields(t *testing.T) {
	payload := encodeOpenAIChatRequest(runtime.Request{
		Model: "gpt-4o-mini",
		Messages: []runtime.Message{{
			Role: runtime.MessageRoleAssistant,
			ToolCalls: []runtime.ToolCall{{
				ID:        "call_123",
				Name:      "get_weather",
				Arguments: `{"city":"shanghai"}`,
			}},
		}, {
			Role:       runtime.MessageRoleTool,
			ToolCallID: "call_123",
			Parts: []runtime.ContentPart{{
				Type: runtime.ContentPartTypeText,
				Text: "sunny",
			}},
		}},
	})

	body, ok := payload.(openAIChatRequest)
	if !ok {
		t.Fatalf("payload type = %T, want openAIChatRequest", payload)
	}
	messages := body.Messages
	if len(messages) != 2 {
		t.Fatalf("len(messages) = %d, want 2", len(messages))
	}
	if len(messages[0].ToolCalls) != 1 {
		t.Fatalf("len(messages[0].ToolCalls) = %d, want 1", len(messages[0].ToolCalls))
	}
	if messages[0].ToolCalls[0].ID != "call_123" || messages[0].ToolCalls[0].Function.Name != "get_weather" {
		t.Fatalf("messages[0].ToolCalls = %#v, want encoded assistant tool call", messages[0].ToolCalls)
	}
	if messages[1].ToolCallID != "call_123" {
		t.Fatalf("messages[1].ToolCallID = %q, want call_123", messages[1].ToolCallID)
	}
	if messages[1].Content != "sunny" {
		t.Fatalf("messages[1].Content = %q, want sunny", messages[1].Content)
	}
}

func TestDecodeOpenAIChatResponseMapsAssistantMessage(t *testing.T) {
	resp, err := decodeOpenAIChatResponse(openAIChatResponseEnvelope{
		ID:     "chatcmpl-1",
		Object: "chat.completion",
		Model:  "gpt-4o-mini",
		Choices: []struct {
			FinishReason string            `json:"finish_reason"`
			Message      openAIChatMessage `json:"message"`
		}{
			{Message: openAIChatMessage{Role: "assistant", Content: "hello from upstream"}},
		},
		Usage: &struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		}{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	})
	if err != nil {
		t.Fatalf("decodeOpenAIChatResponse() error = %v", err)
	}
	if resp.Message.Role != runtime.MessageRoleAssistant {
		t.Fatalf("resp.Message.Role = %q, want assistant", resp.Message.Role)
	}
	if got := resp.Message.Parts[0].Text; got != "hello from upstream" {
		t.Fatalf("resp.Message.Parts[0].Text = %q, want hello from upstream", got)
	}
	if resp.Usage == nil || resp.Usage.TotalTokens != 15 {
		t.Fatalf("resp.Usage = %#v, want total tokens 15", resp.Usage)
	}
}

func TestDecodeOpenAIChatResponseMapsAssistantToolCalls(t *testing.T) {
	resp, err := decodeOpenAIChatResponse(openAIChatResponseEnvelope{
		ID:     "chatcmpl-1",
		Object: "chat.completion",
		Model:  "gpt-4o-mini",
		Choices: []struct {
			FinishReason string            `json:"finish_reason"`
			Message      openAIChatMessage `json:"message"`
		}{
			{Message: openAIChatMessage{
				Role: "assistant",
				ToolCalls: []openAIToolCall{{
					ID:   "call_123",
					Type: "function",
					Function: openAIToolCallFunction{
						Name:      "get_weather",
						Arguments: `{"city":"shanghai"}`,
					},
				}},
			}},
		},
	})
	if err != nil {
		t.Fatalf("decodeOpenAIChatResponse() error = %v", err)
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("len(resp.Message.ToolCalls) = %d, want 1", len(resp.Message.ToolCalls))
	}
	if resp.Message.ToolCalls[0].ID != "call_123" || resp.Message.ToolCalls[0].Name != "get_weather" {
		t.Fatalf("resp.Message.ToolCalls = %#v, want decoded tool call", resp.Message.ToolCalls)
	}
	if resp.FinishReason != runtime.FinishReasonToolUse {
		t.Fatalf("resp.FinishReason = %q, want tool_use", resp.FinishReason)
	}
}

func TestEncodeOpenAIChatRequestPreservesJSONToolResultPayload(t *testing.T) {
	payload := encodeOpenAIChatRequest(runtime.Request{
		Model: "gpt-4o-mini",
		Messages: []runtime.Message{{
			Role:       runtime.MessageRoleTool,
			ToolCallID: "call_123",
			Parts: []runtime.ContentPart{{
				Type: runtime.ContentPartTypeText,
				Text: "lookup failed",
			}, {
				Type: runtime.ContentPartTypeJSON,
				Data: json.RawMessage(`{"city":"shanghai","forecast":"sunny"}`),
			}},
		}},
	})

	body := payload.(openAIChatRequest)
	if len(body.Messages) != 1 {
		t.Fatalf("len(body.Messages) = %d, want 1", len(body.Messages))
	}
	if body.Messages[0].Role != "tool" || body.Messages[0].ToolCallID != "call_123" {
		t.Fatalf("body.Messages[0] = %#v, want tool message with tool_call_id", body.Messages[0])
	}
	if body.Messages[0].Content != "lookup failed\n{\"city\":\"shanghai\",\"forecast\":\"sunny\"}" {
		t.Fatalf("body.Messages[0].Content = %q, want text plus compact json", body.Messages[0].Content)
	}
}

func TestEncodeOpenAIResponsesRequestPreservesMixedToolResultParts(t *testing.T) {
	payload := encodeOpenAIResponsesRequest(runtime.Request{
		Model: "gpt-4o-mini",
		Messages: []runtime.Message{{
			Role:       runtime.MessageRoleTool,
			ToolCallID: "call_123",
			Parts: []runtime.ContentPart{{
				Type: runtime.ContentPartTypeText,
				Text: "first text",
			}, {
				Type: runtime.ContentPartTypeJSON,
				Data: json.RawMessage(`{"city":"shanghai"}`),
			}, {
				Type: runtime.ContentPartTypeText,
				Text: "second text",
			}},
		}},
	}, openAIResponsesCompatibility{})

	body := payload.(openAIResponsesRequest)
	input := body.Input
	if len(input) != 1 {
		t.Fatalf("len(input) = %d, want 1", len(input))
	}
	if input[0].Type != "function_call_output" || input[0].CallID != "call_123" {
		t.Fatalf("input[0] = %#v, want function_call_output", input[0])
	}
	want := "first text\n{\"city\":\"shanghai\"}\nsecond text"
	if input[0].Output != want {
		t.Fatalf("input[0].Output = %q, want %q", input[0].Output, want)
	}
}

func TestEncodeAnthropicMessagesRequestPreservesMixedToolResultOrder(t *testing.T) {
	payload := encodeAnthropicMessagesRequest(runtime.Request{
		Model: "claude-sonnet-4-5",
		Messages: []runtime.Message{{
			Role:       runtime.MessageRoleTool,
			ToolCallID: "tool_123",
			Parts: []runtime.ContentPart{{
				Type: runtime.ContentPartTypeText,
				Text: "lookup failed",
			}, {
				Type: runtime.ContentPartTypeJSON,
				Data: json.RawMessage(`{"city":"shanghai","forecast":"sunny"}`),
			}},
		}},
	})

	body, ok := payload.(anthropicMessagesRequest)
	if !ok {
		t.Fatalf("payload type = %T, want anthropicMessagesRequest", payload)
	}
	got := body.Messages[0].Content[0]
	blocks, ok := got.Content.([]map[string]any)
	if !ok {
		t.Fatalf("got.Content type = %T, want []map[string]any", got.Content)
	}
	if len(blocks) != 2 {
		t.Fatalf("len(blocks) = %d, want 2", len(blocks))
	}
	if blocks[0]["type"] != "text" || blocks[0]["text"] != "lookup failed" {
		t.Fatalf("blocks[0] = %#v, want first text block", blocks[0])
	}
	if blocks[1]["type"] != "json" {
		t.Fatalf("blocks[1] = %#v, want json block", blocks[1])
	}
	value, ok := blocks[1]["value"].(map[string]any)
	if !ok {
		t.Fatalf("blocks[1][value] = %#v, want object", blocks[1]["value"])
	}
	if value["city"] != "shanghai" || value["forecast"] != "sunny" {
		t.Fatalf("value = %#v, want preserved json payload", value)
	}
}

func TestDecodeAnthropicMessagesResponseSplitsMixedTextAndToolUse(t *testing.T) {
	resp, err := decodeAnthropicMessagesResponse(anthropicMessagesEnvelope{
		ID:         "msg_123",
		Type:       "message",
		Role:       "assistant",
		Model:      "claude-sonnet-4-5",
		StopReason: "tool_use",
		Content: []anthropicContentBlock{{
			Type: "text",
			Text: "thinking",
		}, {
			Type:  "tool_use",
			ID:    "tool_123",
			Name:  "get_weather",
			Input: json.RawMessage(`{"city":"shanghai"}`),
		}, {
			Type: "text",
			Text: "done",
		}},
	})
	if err != nil {
		t.Fatalf("decodeAnthropicMessagesResponse() error = %v", err)
	}
	if len(resp.Message.Parts) != 2 {
		t.Fatalf("len(resp.Message.Parts) = %d, want 2", len(resp.Message.Parts))
	}
	if resp.Message.Parts[0].Text != "thinking" || resp.Message.Parts[1].Text != "done" {
		t.Fatalf("resp.Message.Parts = %#v, want preserved text parts", resp.Message.Parts)
	}
	if len(resp.Message.ToolCalls) != 1 || resp.Message.ToolCalls[0].ID != "tool_123" || resp.Message.ToolCalls[0].Arguments != `{"city":"shanghai"}` {
		t.Fatalf("resp.Message.ToolCalls = %#v, want preserved tool call", resp.Message.ToolCalls)
	}
}

func TestDecodeOpenAIResponsesResponseDropsEmptyTextParts(t *testing.T) {
	resp, err := decodeOpenAIResponsesResponse(openAIResponsesEnvelope{
		ID:     "resp_123",
		Object: "response",
		Model:  "gpt-4o-mini",
		Output: []openAIResponsesOutputItem{{
			Type: "message",
			Role: "assistant",
			Content: []openAIResponsesTextPart{{
				Type: "output_text",
				Text: "",
			}, {
				Type: "output_text",
				Text: "hello from upstream",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("decodeOpenAIResponsesResponse() error = %v", err)
	}
	if len(resp.Message.Parts) != 1 {
		t.Fatalf("len(resp.Message.Parts) = %d, want 1", len(resp.Message.Parts))
	}
	if resp.Message.Parts[0].Text != "hello from upstream" {
		t.Fatalf("resp.Message.Parts[0].Text = %q, want hello from upstream", resp.Message.Parts[0].Text)
	}
}

func TestEncodeOpenAIResponsesRequestMapsMessagesAndToolCalls(t *testing.T) {
	payload := encodeOpenAIResponsesRequest(runtime.Request{
		Model:              "gpt-4o-mini",
		System:             "be concise",
		MaxTokens:          256,
		PreviousResponseID: "resp_prev_123",
		Metadata:           json.RawMessage(`{"user_id":"u_123"}`),
		ThinkingType:       "adaptive",
		ContextManagement:  json.RawMessage(`{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]}`),
		OutputEffort:       "high",
		Tools: []runtime.ToolDefinition{{
			Name:        "Read",
			Description: "Read file contents",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"file_path":{"type":"string"}},"required":["file_path"]}`),
		}, {
			Name:        "get_weather",
			Description: "Query weather by city",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`),
		}},
		Messages: []runtime.Message{{
			Role: runtime.MessageRoleUser,
			Parts: []runtime.ContentPart{{Type: runtime.ContentPartTypeText, Text: "system reminder"}, {
				Type: runtime.ContentPartTypeText,
				Text: "hello",
			}},
		}, {
			Role: runtime.MessageRoleAssistant,
			ToolCalls: []runtime.ToolCall{{
				ID:        "call_123",
				Name:      "get_weather",
				Arguments: `{"city":"shanghai"}`,
			}},
		}, {
			Role:       runtime.MessageRoleTool,
			ToolCallID: "call_123",
			Parts:      []runtime.ContentPart{{Type: runtime.ContentPartTypeText, Text: "sunny"}},
		}},
	}, openAIResponsesCompatibility{})

	body := payload.(openAIResponsesRequest)
	if body.Model != "gpt-4o-mini" {
		t.Fatalf("body.Model = %q, want gpt-4o-mini", body.Model)
	}
	if body.Instructions != "be concise" {
		t.Fatalf("body.Instructions = %q, want be concise", body.Instructions)
	}
	if body.MaxOutputTokens != 256 {
		t.Fatalf("body.MaxOutputTokens = %d, want 256", body.MaxOutputTokens)
	}
	if body.PreviousResponseID != "resp_prev_123" {
		t.Fatalf("body.PreviousResponseID = %q, want resp_prev_123", body.PreviousResponseID)
	}
	if string(body.Metadata) != `{"user_id":"u_123"}` {
		t.Fatalf("body.Metadata = %s, want preserved metadata", string(body.Metadata))
	}
	if body.Reasoning == nil || body.Reasoning.Effort != "high" {
		t.Fatalf("body.Reasoning = %#v, want effort high", body.Reasoning)
	}
	var contextManagement map[string]any
	if err := json.Unmarshal(body.ContextManagement, &contextManagement); err != nil {
		t.Fatalf("json.Unmarshal(body.ContextManagement) error = %v", err)
	}
	edits, ok := contextManagement["edits"].([]any)
	if !ok || len(edits) != 1 {
		t.Fatalf("contextManagement[edits] = %#v, want one edit", contextManagement["edits"])
	}
	edit, ok := edits[0].(map[string]any)
	if !ok || edit["type"] != "clear_thinking_20251015" || edit["keep"] != "all" {
		t.Fatalf("edit = %#v, want preserved context_management edit", edits[0])
	}
	if body.ToolChoice != "auto" {
		t.Fatalf("body.ToolChoice = %#v, want auto", body.ToolChoice)
	}
	if len(body.Tools) != 2 {
		t.Fatalf("len(body.Tools) = %d, want 2", len(body.Tools))
	}
	tool0, ok := body.Tools[0].(openAIResponsesTool)
	if !ok {
		t.Fatalf("body.Tools[0] type = %T, want openAIResponsesTool", body.Tools[0])
	}
	tool1, ok := body.Tools[1].(openAIResponsesTool)
	if !ok {
		t.Fatalf("body.Tools[1] type = %T, want openAIResponsesTool", body.Tools[1])
	}
	if tool0.Name != "Read" || tool1.Name != "get_weather" {
		t.Fatalf("body.Tools = %#v, want builtin Read and custom get_weather", body.Tools)
	}
	input := body.Input
	if len(input) != 3 {
		t.Fatalf("len(input) = %d, want 3", len(input))
	}
	if input[0].Type != "message" || input[0].Role != "user" {
		t.Fatalf("input[0] = %#v, want user message", input[0])
	}
	if len(input[0].Content) != 1 || input[0].Content[0].Type != "input_text" || input[0].Content[0].Text != "system reminder\nhello" {
		t.Fatalf("input[0].Content = %#v, want joined input_text", input[0].Content)
	}
	if input[1].Type != "function_call" || input[1].CallID != "call_123" || input[1].Name != "get_weather" {
		t.Fatalf("input[1] = %#v, want function_call", input[1])
	}
	if input[1].Arguments != `{"city":"shanghai"}` {
		t.Fatalf("input[1].Arguments = %s, want compact JSON string", input[1].Arguments)
	}
	if input[2].Type != "function_call_output" || input[2].CallID != "call_123" || input[2].Output != "sunny" {
		t.Fatalf("input[2] = %#v, want function_call_output", input[2])
	}
}

func TestEncodeOpenAIResponsesRequestPreservesBuiltinResponsesTools(t *testing.T) {
	payload := encodeOpenAIResponsesRequest(runtime.Request{
		Model: "gpt-4o-mini",
		Tools: []runtime.ToolDefinition{{
			Type: "web_search",
			Raw:  json.RawMessage(`{"type":"web_search","user_location":{"type":"approximate","country":"US"}}`),
		}},
	}, openAIResponsesCompatibility{})

	body := payload.(openAIResponsesRequest)
	if len(body.Tools) != 1 {
		t.Fatalf("len(body.Tools) = %d, want 1", len(body.Tools))
	}
	raw, err := json.Marshal(body.Tools[0])
	if err != nil {
		t.Fatalf("json.Marshal(body.Tools[0]) error = %v", err)
	}
	var tool map[string]any
	if err := json.Unmarshal(raw, &tool); err != nil {
		t.Fatalf("json.Unmarshal(raw) error = %v", err)
	}
	if tool["type"] != "web_search" {
		t.Fatalf("tool[type] = %#v, want web_search", tool["type"])
	}
	userLocation, ok := tool["user_location"].(map[string]any)
	if !ok || userLocation["type"] != "approximate" || userLocation["country"] != "US" {
		t.Fatalf("tool[user_location] = %#v, want approximate/US", tool["user_location"])
	}
}

func TestEncodeOpenAIResponsesRequestDropsIncompatibleFieldsForPaypalAI(t *testing.T) {
	payload := encodeOpenAIResponsesRequest(runtime.Request{
		Model:             "gpt-4o-mini",
		Metadata:          json.RawMessage(`{"user_id":"u_123"}`),
		ContextManagement: json.RawMessage(`{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]}`),
		OutputEffort:      "high",
		Messages: []runtime.Message{{
			Role:  runtime.MessageRoleUser,
			Parts: []runtime.ContentPart{{Type: runtime.ContentPartTypeText, Text: "hello"}},
		}, {
			Role:  runtime.MessageRoleAssistant,
			Parts: []runtime.ContentPart{{Type: runtime.ContentPartTypeText, Text: "assistant reply"}},
		}},
	}, openAIResponsesCompatibility{
		DropMetadata:           true,
		DropContextManagement:  true,
		RewriteAssistantToUser: true,
	})

	body := payload.(openAIResponsesRequest)
	if len(body.Metadata) != 0 {
		t.Fatalf("body.Metadata = %s, want omitted", string(body.Metadata))
	}
	if len(body.ContextManagement) != 0 {
		t.Fatalf("body.ContextManagement = %s, want omitted", string(body.ContextManagement))
	}
	if body.Reasoning == nil || body.Reasoning.Effort != "high" {
		t.Fatalf("body.Reasoning = %#v, want preserved reasoning", body.Reasoning)
	}
	if len(body.Input) != 2 {
		t.Fatalf("len(body.Input) = %d, want 2", len(body.Input))
	}
	if body.Input[1].Role != "user" {
		t.Fatalf("body.Input[1].Role = %q, want user", body.Input[1].Role)
	}
	if body.Input[1].Content[0].Text != "Previous assistant message:\nassistant reply" {
		t.Fatalf("body.Input[1].Content[0].Text = %q, want rewritten assistant history", body.Input[1].Content[0].Text)
	}
}

func TestDetectOpenAIResponsesCompatibilityPaypalAI(t *testing.T) {
	compat := detectOpenAIResponsesCompatibility("https://api.paypal-ai.com/v1")
	if !compat.DropMetadata || !compat.DropContextManagement || !compat.RewriteAssistantToUser || !compat.DropToolErrorStatus {
		t.Fatalf("compat = %#v, want metadata/context_management dropped, assistant rewritten, and tool error status omitted", compat)
	}
}

func TestDetectOpenAIResponsesCompatibilityOfficialOpenAI(t *testing.T) {
	compat := detectOpenAIResponsesCompatibility("https://api.openai.com/v1")
	if compat.DropMetadata || compat.DropContextManagement || compat.RewriteAssistantToUser || compat.DropToolErrorStatus {
		t.Fatalf("compat = %#v, want full responses support", compat)
	}
}

func TestMergeOpenAIResponsesCompatibilityUsesConfigOverrides(t *testing.T) {
	falseValue := false
	trueValue := true
	compat := mergeOpenAIResponsesCompatibility(detectOpenAIResponsesCompatibility("https://api.paypal-ai.com/v1"), config.OutboundCapabilities{
		ResponsesPreviousResponseID:     &trueValue,
		ResponsesBuiltinTools:           &trueValue,
		ResponsesToolResultStatusError:  &trueValue,
		ResponsesAssistantHistoryNative: &trueValue,
	})
	if compat.RejectPreviousResponse || compat.RejectBuiltinTools || compat.DropToolErrorStatus || compat.RewriteAssistantToUser {
		t.Fatalf("compat = %#v, want config overrides to disable paypal defaults", compat)
	}
	compat = mergeOpenAIResponsesCompatibility(openAIResponsesCompatibility{}, config.OutboundCapabilities{
		ResponsesPreviousResponseID:     &falseValue,
		ResponsesBuiltinTools:           &falseValue,
		ResponsesToolResultStatusError:  &falseValue,
		ResponsesAssistantHistoryNative: &falseValue,
	})
	if !compat.RejectPreviousResponse || !compat.RejectBuiltinTools || !compat.DropToolErrorStatus || !compat.RewriteAssistantToUser {
		t.Fatalf("compat = %#v, want false capabilities to enforce guards/rewrites", compat)
	}
}

func TestOpenAIResponsesRejectsPreviousResponseWhenCapabilityDisabled(t *testing.T) {
	p := NewOpenAIResponsesCompatible("responses", "https://example.com/v1", []string{"test-key"}, config.OutboundCapabilities{
		ResponsesPreviousResponseID: boolPtr(false),
	}, nil)
	_, err := p.ChatCompletion(context.Background(), runtime.Request{Model: "gpt-4o-mini", PreviousResponseID: "resp_123"})
	if err == nil || NormalizeError(err) != ErrorKindFatal {
		t.Fatalf("err = %v, want fatal unsupported previous_response_id", err)
	}
	if got := err.Error(); got != "outbound does not support responses previous_response_id continuation" {
		t.Fatalf("err.Error() = %q, want previous_response_id capability error", got)
	}
}

func TestOpenAIResponsesRejectsBuiltinToolsWhenCapabilityDisabled(t *testing.T) {
	p := NewOpenAIResponsesCompatible("responses", "https://example.com/v1", []string{"test-key"}, config.OutboundCapabilities{
		ResponsesBuiltinTools: boolPtr(false),
	}, nil)
	_, err := p.ChatCompletion(context.Background(), runtime.Request{Model: "gpt-4o-mini", Tools: []runtime.ToolDefinition{{Type: "web_search"}}})
	if err == nil || NormalizeError(err) != ErrorKindFatal {
		t.Fatalf("err = %v, want fatal unsupported builtin tool error", err)
	}
	if got := err.Error(); got != "outbound does not support responses builtin tools" {
		t.Fatalf("err.Error() = %q, want builtin tools capability error", got)
	}
}

func TestEncodeOpenAIResponsesRequestPreservesClaudeCodePromptAfterReminder(t *testing.T) {
	payload := encodeOpenAIResponsesRequest(runtime.Request{
		Model: "gpt-4o-mini",
		Messages: []runtime.Message{{
			Role: runtime.MessageRoleUser,
			Parts: []runtime.ContentPart{{
				Type: runtime.ContentPartTypeText,
				Text: "<system-reminder>today is 2026/04/18</system-reminder>",
			}, {
				Type: runtime.ContentPartTypeText,
				Text: "hi",
			}},
		}},
	}, openAIResponsesCompatibility{})

	body := payload.(openAIResponsesRequest)
	if len(body.Input) != 1 {
		t.Fatalf("len(body.Input) = %d, want 1", len(body.Input))
	}
	if len(body.Input[0].Content) != 1 {
		t.Fatalf("len(body.Input[0].Content) = %d, want 1", len(body.Input[0].Content))
	}
	want := "<system-reminder>today is 2026/04/18</system-reminder>\nhi"
	if got := body.Input[0].Content[0].Text; got != want {
		t.Fatalf("body.Input[0].Content[0].Text = %q, want %q", got, want)
	}
}

func TestEncodeOpenAIResponsesRequestPreservesToolResultErrorStatus(t *testing.T) {
	payload := encodeOpenAIResponsesRequest(runtime.Request{
		Model: "gpt-4o-mini",
		Messages: []runtime.Message{{
			Role:              runtime.MessageRoleTool,
			ToolCallID:        "call_123",
			ToolResultIsError: true,
			Parts:             []runtime.ContentPart{{Type: runtime.ContentPartTypeText, Text: "lookup failed"}},
		}},
	}, openAIResponsesCompatibility{})

	body := payload.(openAIResponsesRequest)
	if len(body.Input) != 1 {
		t.Fatalf("len(body.Input) = %d, want 1", len(body.Input))
	}
	if got := body.Input[0].Status; got != "error" {
		t.Fatalf("body.Input[0].Status = %q, want error", got)
	}
}

func TestEncodeOpenAIResponsesRequestDropsToolResultErrorStatusForPaypalAI(t *testing.T) {
	payload := encodeOpenAIResponsesRequest(runtime.Request{
		Model: "gpt-4o-mini",
		Messages: []runtime.Message{{
			Role:              runtime.MessageRoleTool,
			ToolCallID:        "call_123",
			ToolResultIsError: true,
			Parts:             []runtime.ContentPart{{Type: runtime.ContentPartTypeText, Text: "lookup failed"}},
		}},
	}, openAIResponsesCompatibility{
		DropToolErrorStatus: true,
	})

	body := payload.(openAIResponsesRequest)
	if len(body.Input) != 1 {
		t.Fatalf("len(body.Input) = %d, want 1", len(body.Input))
	}
	if got := body.Input[0].Status; got != "" {
		t.Fatalf("body.Input[0].Status = %q, want omitted", got)
	}
	if got := body.Input[0].Output; got != "lookup failed" {
		t.Fatalf("body.Input[0].Output = %q, want lookup failed", got)
	}
}

func TestEncodeAnthropicMessagesRequestMapsSystemToolsAndMaxTokens(t *testing.T) {
	payload := encodeAnthropicMessagesRequest(runtime.Request{
		Model:     "claude-sonnet-4-5",
		System:    "be concise",
		MaxTokens: 256,
		Stream:    true,
		Messages: []runtime.Message{{
			Role:  runtime.MessageRoleUser,
			Parts: []runtime.ContentPart{{Type: runtime.ContentPartTypeText, Text: "hello"}},
		}, {
			Role: runtime.MessageRoleAssistant,
			ToolCalls: []runtime.ToolCall{{
				ID:        "tool_123",
				Name:      "get_weather",
				Arguments: `{"city":"shanghai"}`,
			}},
		}, {
			Role:       runtime.MessageRoleTool,
			ToolCallID: "tool_123",
			Parts:      []runtime.ContentPart{{Type: runtime.ContentPartTypeText, Text: "sunny"}},
		}},
	})

	body, ok := payload.(anthropicMessagesRequest)
	if !ok {
		t.Fatalf("payload type = %T, want anthropicMessagesRequest", payload)
	}
	if body.Model != "claude-sonnet-4-5" || body.System != "be concise" || body.MaxTokens != 256 || !body.Stream {
		t.Fatalf("payload header = %#v, want model/system/max_tokens/stream preserved", body)
	}
	if len(body.Messages) != 3 {
		t.Fatalf("len(body.Messages) = %d, want 3", len(body.Messages))
	}
	if body.Messages[0].Role != "user" || body.Messages[0].Content[0].Type != "text" || body.Messages[0].Content[0].Text != "hello" {
		t.Fatalf("body.Messages[0] = %#v, want user text", body.Messages[0])
	}
	if body.Messages[1].Content[0].Type != "tool_use" || body.Messages[1].Content[0].ID != "tool_123" || body.Messages[1].Content[0].Name != "get_weather" {
		t.Fatalf("body.Messages[1] = %#v, want assistant tool_use", body.Messages[1])
	}
	if body.Messages[2].Role != "user" || body.Messages[2].Content[0].Type != "tool_result" || body.Messages[2].Content[0].ToolUseID != "tool_123" {
		t.Fatalf("body.Messages[2] = %#v, want user tool_result", body.Messages[2])
	}
}

func TestEncodeAnthropicMessagesRequestIncludesToolDefinitions(t *testing.T) {
	payload := encodeAnthropicMessagesRequest(runtime.Request{
		Model:     "claude-sonnet-4-5",
		MaxTokens: 256,
		Tools: []runtime.ToolDefinition{{
			Name:        "get_weather",
			Description: "Query weather by city",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`),
		}},
		Messages: []runtime.Message{{
			Role:  runtime.MessageRoleUser,
			Parts: []runtime.ContentPart{{Type: runtime.ContentPartTypeText, Text: "hello"}},
		}},
	})

	body, ok := payload.(anthropicMessagesRequest)
	if !ok {
		t.Fatalf("payload type = %T, want anthropicMessagesRequest", payload)
	}
	if len(body.Tools) != 1 {
		t.Fatalf("len(body.Tools) = %d, want 1", len(body.Tools))
	}
	if body.Tools[0].Name != "get_weather" || body.Tools[0].Description != "Query weather by city" {
		t.Fatalf("body.Tools[0] = %#v, want encoded anthropic tool definition", body.Tools[0])
	}
	var schema map[string]any
	if err := json.Unmarshal(body.Tools[0].InputSchema, &schema); err != nil {
		t.Fatalf("json.Unmarshal(body.Tools[0].InputSchema) error = %v", err)
	}
	if schema["type"] != "object" {
		t.Fatalf("schema[type] = %#v, want object", schema["type"])
	}
}

func TestEncodeAnthropicMessagesRequestPreservesJSONToolResultPayload(t *testing.T) {
	payload := encodeAnthropicMessagesRequest(runtime.Request{
		Model: "claude-sonnet-4-5",
		Messages: []runtime.Message{{
			Role:       runtime.MessageRoleTool,
			ToolCallID: "tool_123",
			Parts: []runtime.ContentPart{{
				Type: runtime.ContentPartTypeJSON,
				Data: json.RawMessage(`{"city":"shanghai","forecast":"sunny"}`),
			}},
		}},
	})

	body, ok := payload.(anthropicMessagesRequest)
	if !ok {
		t.Fatalf("payload type = %T, want anthropicMessagesRequest", payload)
	}
	if len(body.Messages) != 1 {
		t.Fatalf("len(body.Messages) = %d, want 1", len(body.Messages))
	}
	got := body.Messages[0].Content[0]
	if got.Type != "tool_result" || got.ToolUseID != "tool_123" {
		t.Fatalf("body.Messages[0].Content[0] = %#v, want tool_result with tool use id", got)
	}
	blocks, ok := got.Content.([]map[string]any)
	if !ok {
		t.Fatalf("got.Content type = %T, want []map[string]any", got.Content)
	}
	if len(blocks) != 1 || blocks[0]["type"] != "json" {
		t.Fatalf("blocks = %#v, want single json tool result block", blocks)
	}
	value, ok := blocks[0]["value"].(map[string]any)
	if !ok {
		t.Fatalf("blocks[0][value] = %#v, want object", blocks[0]["value"])
	}
	if value["city"] != "shanghai" || value["forecast"] != "sunny" {
		t.Fatalf("value = %#v, want original json payload", value)
	}
}

func TestEncodeAnthropicMessagesRequestPreservesToolResultErrorFlag(t *testing.T) {
	payload := encodeAnthropicMessagesRequest(runtime.Request{
		Model: "claude-sonnet-4-5",
		Messages: []runtime.Message{{
			Role:              runtime.MessageRoleTool,
			ToolCallID:        "tool_123",
			ToolResultIsError: true,
			Parts:             []runtime.ContentPart{{Type: runtime.ContentPartTypeText, Text: "lookup failed"}},
		}},
	})

	body, ok := payload.(anthropicMessagesRequest)
	if !ok {
		t.Fatalf("payload type = %T, want anthropicMessagesRequest", payload)
	}
	if len(body.Messages) != 1 {
		t.Fatalf("len(body.Messages) = %d, want 1", len(body.Messages))
	}
	got := body.Messages[0].Content[0]
	if got.Type != "tool_result" || got.ToolUseID != "tool_123" {
		t.Fatalf("body.Messages[0].Content[0] = %#v, want tool_result with tool use id", got)
	}
	if !got.IsError {
		t.Fatalf("got.IsError = %v, want true", got.IsError)
	}
}

func TestDecodeAnthropicMessagesResponseMapsToolUseStopReason(t *testing.T) {
	resp, err := decodeAnthropicMessagesResponse(anthropicMessagesEnvelope{
		ID:         "msg_123",
		Type:       "message",
		Role:       "assistant",
		Model:      "claude-sonnet-4-5",
		StopReason: "tool_use",
		Content: []anthropicContentBlock{{
			Type:  "tool_use",
			ID:    "tool_123",
			Name:  "get_weather",
			Input: json.RawMessage(`{"city":"shanghai"}`),
		}},
	})
	if err != nil {
		t.Fatalf("decodeAnthropicMessagesResponse() error = %v", err)
	}
	if resp.FinishReason != runtime.FinishReasonToolUse {
		t.Fatalf("resp.FinishReason = %q, want tool_use", resp.FinishReason)
	}
}

func TestDecodeOpenAIResponsesResponseMapsTextAndToolCalls(t *testing.T) {
	resp, err := decodeOpenAIResponsesResponse(openAIResponsesEnvelope{
		ID:     "resp_123",
		Object: "response",
		Model:  "gpt-4o-mini",
		Output: []openAIResponsesOutputItem{{
			Type: "message",
			Role: "assistant",
			Content: []openAIResponsesTextPart{{
				Type: "output_text",
				Text: "hello from upstream",
			}},
		}, {
			Type:      "function_call",
			CallID:    "call_123",
			Name:      "get_weather",
			Arguments: `{"city":"shanghai"}`,
		}},
		Usage: &struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
			TotalTokens  int `json:"total_tokens"`
		}{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
	})
	if err != nil {
		t.Fatalf("decodeOpenAIResponsesResponse() error = %v", err)
	}
	if resp.Message.Role != runtime.MessageRoleAssistant {
		t.Fatalf("resp.Message.Role = %q, want assistant", resp.Message.Role)
	}
	if len(resp.Message.Parts) != 1 || resp.Message.Parts[0].Text != "hello from upstream" {
		t.Fatalf("resp.Message.Parts = %#v, want decoded text", resp.Message.Parts)
	}
	if len(resp.Message.ToolCalls) != 1 || resp.Message.ToolCalls[0].ID != "call_123" || resp.Message.ToolCalls[0].Arguments != `{"city":"shanghai"}` {
		t.Fatalf("resp.Message.ToolCalls = %#v, want decoded function_call", resp.Message.ToolCalls)
	}
	if resp.Usage == nil || resp.Usage.TotalTokens != 15 {
		t.Fatalf("resp.Usage = %#v, want total tokens", resp.Usage)
	}
	if resp.FinishReason != runtime.FinishReasonToolUse {
		t.Fatalf("resp.FinishReason = %q, want tool_use", resp.FinishReason)
	}
}

func TestDecodeOpenAIResponsesResponseDefaultsToStopWithoutToolCalls(t *testing.T) {
	resp, err := decodeOpenAIResponsesResponse(openAIResponsesEnvelope{
		ID:     "resp_456",
		Object: "response",
		Model:  "gpt-4o-mini",
		Status: "completed",
		Output: []openAIResponsesOutputItem{{
			Type: "message",
			Role: "assistant",
			Content: []openAIResponsesTextPart{{
				Type: "output_text",
				Text: "final answer",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("decodeOpenAIResponsesResponse() error = %v", err)
	}
	if resp.FinishReason != runtime.FinishReasonStop {
		t.Fatalf("resp.FinishReason = %q, want stop", resp.FinishReason)
	}
}

func TestDecodeOpenAIResponsesResponseRejectsEmptyOutput(t *testing.T) {
	_, err := decodeOpenAIResponsesResponse(openAIResponsesEnvelope{ID: "resp_123", Object: "response", Model: "gpt-4o-mini"})
	if err == nil {
		t.Fatal("decodeOpenAIResponsesResponse() error = nil, want error")
	}
	if got := err.Error(); got != "upstream response missing output" {
		t.Fatalf("decodeOpenAIResponsesResponse() error = %q, want upstream response missing output", got)
	}
}

func TestOpenAIResponsesCompatibleChatCompletionSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("path = %q, want /responses", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization = %q, want Bearer test-key", got)
		}

		var req openAIResponsesRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		if req.Model != "gpt-4o-mini" {
			t.Fatalf("req.Model = %q, want gpt-4o-mini", req.Model)
		}
		if len(req.Input) != 1 || req.Input[0].Type != "message" || req.Input[0].Role != "user" || req.Input[0].Content[0].Text != "hello" {
			t.Fatalf("req.Input = %#v, want single message input", req.Input)
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "resp_123",
			"object": "response",
			"model":  req.Model,
			"output": []map[string]any{{
				"type": "message",
				"role": "assistant",
				"content": []map[string]any{{
					"type": "output_text",
					"text": "hello from upstream",
				}},
			}},
		})
	}))
	defer server.Close()

	p := NewOpenAIResponsesCompatible("responses", server.URL, []string{"test-key"}, config.OutboundCapabilities{}, server.Client())
	resp, err := p.ChatCompletion(context.Background(), runtime.Request{
		Model: "gpt-4o-mini",
		Messages: []runtime.Message{{
			Role:  runtime.MessageRoleUser,
			Parts: []runtime.ContentPart{{Type: runtime.ContentPartTypeText, Text: "hello"}},
		}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion() error = %v", err)
	}
	if got := resp.Message.Parts[0].Text; got != "hello from upstream" {
		t.Fatalf("resp.Message.Parts[0].Text = %q, want hello from upstream", got)
	}
}

func TestOpenAIResponsesCompatibleStreamCompletionEmitsToolCallDelta(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "resp_123",
			"object": "response",
			"model":  "gpt-4o-mini",
			"output": []map[string]any{{
				"type":    "function_call",
				"call_id": "call_123",
				"name":    "get_weather",
				"input": map[string]any{
					"city": "shanghai",
				},
			}},
		})
	}))
	defer server.Close()

	p := NewOpenAIResponsesCompatible("responses", server.URL, []string{"test-key"}, config.OutboundCapabilities{}, server.Client())
	ch, err := p.StreamCompletion(context.Background(), runtime.Request{Model: "gpt-4o-mini"})
	if err != nil {
		t.Fatalf("StreamCompletion() error = %v", err)
	}

	var toolEvent *runtime.StreamEvent
	var endEvent *runtime.StreamEvent
	for event := range ch {
		if event.ToolCall != nil {
			e := event
			toolEvent = &e
		}
		if event.Type == runtime.StreamEventMessageEnd {
			e := event
			endEvent = &e
		}
	}
	if toolEvent == nil {
		t.Fatal("toolEvent = nil, want tool call delta")
	}
	if toolEvent.ToolCall.ID != "call_123" || toolEvent.ToolCall.Name != "get_weather" {
		t.Fatalf("toolEvent.ToolCall = %#v, want decoded tool call", toolEvent.ToolCall)
	}
	if endEvent == nil {
		t.Fatal("endEvent = nil, want message end")
	}
	if endEvent.FinishReason != runtime.FinishReasonToolUse {
		t.Fatalf("endEvent.FinishReason = %q, want tool_use", endEvent.FinishReason)
	}
}

func TestAnthropicMessagesCompatibleChatCompletionSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/messages" {
			t.Fatalf("path = %q, want /messages", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "test-key" {
			t.Fatalf("x-api-key = %q, want test-key", got)
		}
		if got := r.Header.Get("anthropic-version"); got != "2023-06-01" {
			t.Fatalf("anthropic-version = %q, want 2023-06-01", got)
		}

		var req anthropicMessagesRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		if req.Model != "claude-sonnet-4-5" || req.System != "be concise" || req.MaxTokens != 256 {
			t.Fatalf("req = %#v, want model/system/max_tokens preserved", req)
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          "msg_123",
			"type":        "message",
			"role":        "assistant",
			"model":       req.Model,
			"stop_reason": "end_turn",
			"content": []map[string]any{{
				"type": "text",
				"text": "hello from upstream",
			}},
			"usage": map[string]any{
				"input_tokens":  10,
				"output_tokens": 5,
			},
		})
	}))
	defer server.Close()

	p := NewAnthropicMessagesCompatible("anthropic", server.URL, []string{"test-key"}, server.Client())
	resp, err := p.ChatCompletion(context.Background(), runtime.Request{
		Model:     "claude-sonnet-4-5",
		System:    "be concise",
		MaxTokens: 256,
		Messages: []runtime.Message{{
			Role:  runtime.MessageRoleUser,
			Parts: []runtime.ContentPart{{Type: runtime.ContentPartTypeText, Text: "hello"}},
		}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion() error = %v", err)
	}
	if got := resp.Message.Parts[0].Text; got != "hello from upstream" {
		t.Fatalf("resp.Message.Parts[0].Text = %q, want hello from upstream", got)
	}
	if resp.Usage == nil || resp.Usage.TotalTokens != 15 {
		t.Fatalf("resp.Usage = %#v, want total tokens 15", resp.Usage)
	}
}

func TestAnthropicMessagesCompatibleChatCompletionQuotaExceeded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	p := NewAnthropicMessagesCompatible("anthropic", server.URL, []string{"test-key"}, server.Client())
	_, err := p.ChatCompletion(context.Background(), runtime.Request{Model: "claude-sonnet-4-5"})
	if err == nil {
		t.Fatal("ChatCompletion() error = nil, want error")
	}
	if NormalizeError(err) != ErrorKindQuotaExceeded {
		t.Fatalf("NormalizeError() = %q, want quota_exceeded", NormalizeError(err))
	}
}

func TestAnthropicMessagesCompatibleStreamCompletionDoesNotSendStreamToUpstream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req anthropicMessagesRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		if req.Stream {
			t.Fatalf("req.Stream = %v, want false for local replay streaming", req.Stream)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          "msg_123",
			"type":        "message",
			"role":        "assistant",
			"model":       req.Model,
			"stop_reason": "tool_use",
			"content": []map[string]any{{
				"type":  "tool_use",
				"id":    "tool_123",
				"name":  "get_weather",
				"input": map[string]any{"city": "shanghai"},
			}},
		})
	}))
	defer server.Close()

	p := NewAnthropicMessagesCompatible("anthropic", server.URL, []string{"test-key"}, server.Client())
	ch, err := p.StreamCompletion(context.Background(), runtime.Request{Model: "claude-sonnet-4-5", Stream: true})
	if err != nil {
		t.Fatalf("StreamCompletion() error = %v", err)
	}
	for range ch {
	}
}

func TestAnthropicMessagesCompatibleStreamCompletionEmitsToolCallDelta(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          "msg_123",
			"type":        "message",
			"role":        "assistant",
			"model":       "claude-sonnet-4-5",
			"stop_reason": "tool_use",
			"content": []map[string]any{{
				"type":  "tool_use",
				"id":    "tool_123",
				"name":  "get_weather",
				"input": map[string]any{"city": "shanghai"},
			}},
		})
	}))
	defer server.Close()

	p := NewAnthropicMessagesCompatible("anthropic", server.URL, []string{"test-key"}, server.Client())
	ch, err := p.StreamCompletion(context.Background(), runtime.Request{Model: "claude-sonnet-4-5"})
	if err != nil {
		t.Fatalf("StreamCompletion() error = %v", err)
	}

	var toolEvent *runtime.StreamEvent
	for event := range ch {
		if event.ToolCall != nil {
			e := event
			toolEvent = &e
		}
	}
	if toolEvent == nil {
		t.Fatal("toolEvent = nil, want tool call delta")
	}
	if toolEvent.ToolCall.ID != "tool_123" || toolEvent.ToolCall.Name != "get_weather" {
		t.Fatalf("toolEvent.ToolCall = %#v, want decoded tool call", toolEvent.ToolCall)
	}
}

func TestOpenAICompatibleChatCompletionSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %q, want /chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization = %q, want Bearer test-key", got)
		}

		var req struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		if req.Model != "gpt-4o-mini" {
			t.Fatalf("req.Model = %q, want gpt-4o-mini", req.Model)
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "chatcmpl-1",
			"object": "chat.completion",
			"model":  req.Model,
			"choices": []map[string]any{{
				"message": map[string]string{
					"role":    "assistant",
					"content": "hello from upstream",
				},
			}},
		})
	}))
	defer server.Close()

	p := NewOpenAICompatible("openai", server.URL, []string{"test-key"}, server.Client())
	resp, err := p.ChatCompletion(context.Background(), runtime.Request{
		Model: "gpt-4o-mini",
		Messages: []runtime.Message{{
			Role:  runtime.MessageRoleUser,
			Parts: []runtime.ContentPart{{Type: runtime.ContentPartTypeText, Text: "hello"}},
		}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion() error = %v", err)
	}
	if got := resp.Message.Parts[0].Text; got != "hello from upstream" {
		t.Fatalf("resp.Message.Parts[0].Text = %q, want hello from upstream", got)
	}
	if resp.FinishReason != runtime.FinishReasonStop {
		t.Fatalf("resp.FinishReason = %q, want stop", resp.FinishReason)
	}
}

func TestOpenAICompatibleChatCompletionWritesTraceWhenEnabled(t *testing.T) {
	tmpDir := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd() error = %v", err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("os.Chdir() error = %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()
	defer func() { _ = os.Unsetenv("SYROGO_TRACE") }()
	if err := os.Setenv("SYROGO_TRACE", "full"); err != nil {
		t.Fatalf("os.Setenv() error = %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "chatcmpl-1",
			"object": "chat.completion",
			"model":  "gpt-4o-mini",
			"choices": []map[string]any{{
				"message": map[string]string{"role": "assistant", "content": "hello from upstream"},
			}},
		})
	}))
	defer server.Close()

	ctx := context.WithValue(context.Background(), runtime.ContextKeyRequestID, "req-trace-openai")
	p := NewOpenAICompatible("openai", server.URL, []string{"test-key"}, server.Client())
	_, err = p.ChatCompletion(ctx, runtime.Request{
		Model: "gpt-4o-mini",
		Messages: []runtime.Message{{
			Role:  runtime.MessageRoleUser,
			Parts: []runtime.ContentPart{{Type: runtime.ContentPartTypeText, Text: "hello"}},
		}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion() error = %v", err)
	}

	tracePath := filepath.Join(tmpDir, "tmp", "trace", "req-trace-openai.outbound-openai-openai_chat.json")
	data, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v", tracePath, err)
	}

	var snap struct {
		RequestID string            `json:"request_id"`
		Protocol  string            `json:"protocol"`
		Headers   map[string]string `json:"headers"`
		Status    int               `json:"status"`
	}
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if snap.RequestID != "req-trace-openai" || snap.Protocol != "openai_chat" || snap.Status != http.StatusOK {
		t.Fatalf("trace snapshot = %#v, want request_id/protocol/status preserved", snap)
	}
	if snap.Headers["Authorization"] != "Bearer ***" {
		t.Fatalf("Authorization header = %q, want Bearer ***", snap.Headers["Authorization"])
	}
}

func TestOpenAICompatibleChatCompletionSendsToolCallingFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Role       string           `json:"role"`
				Content    string           `json:"content"`
				ToolCalls  []openAIToolCall `json:"tool_calls"`
				ToolCallID string           `json:"tool_call_id"`
				Status     string           `json:"status"`
			} `json:"messages"`
			Tools      []openAIToolDefinition `json:"tools"`
			ToolChoice string                 `json:"tool_choice"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		if len(req.Messages) != 2 {
			t.Fatalf("len(req.Messages) = %d, want 2", len(req.Messages))
		}
		if len(req.Messages[0].ToolCalls) != 1 || req.Messages[0].ToolCalls[0].ID != "call_123" {
			t.Fatalf("req.Messages[0].ToolCalls = %#v, want encoded tool call", req.Messages[0].ToolCalls)
		}
		if req.Messages[1].ToolCallID != "call_123" {
			t.Fatalf("req.Messages[1].ToolCallID = %q, want call_123", req.Messages[1].ToolCallID)
		}
		if req.Messages[1].Status != "error" {
			t.Fatalf("req.Messages[1].Status = %q, want error", req.Messages[1].Status)
		}
		if len(req.Tools) != 1 || req.Tools[0].Function.Name != "get_weather" {
			t.Fatalf("req.Tools = %#v, want single get_weather tool", req.Tools)
		}
		if req.ToolChoice != "auto" {
			t.Fatalf("req.ToolChoice = %q, want auto", req.ToolChoice)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "chatcmpl-1",
			"object": "chat.completion",
			"model":  "gpt-4o-mini",
			"choices": []map[string]any{{
				"message": map[string]string{
					"role":    "assistant",
					"content": "done",
				},
			}},
		})
	}))
	defer server.Close()

	p := NewOpenAICompatible("openai", server.URL, []string{"test-key"}, server.Client())
	_, err := p.ChatCompletion(context.Background(), runtime.Request{
		Model: "gpt-4o-mini",
		Tools: []runtime.ToolDefinition{{
			Name:        "get_weather",
			Description: "Query weather by city",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`),
		}},
		Messages: []runtime.Message{{
			Role: runtime.MessageRoleAssistant,
			ToolCalls: []runtime.ToolCall{{
				ID:        "call_123",
				Name:      "get_weather",
				Arguments: `{"city":"shanghai"}`,
			}},
		}, {
			Role:              runtime.MessageRoleTool,
			ToolCallID:        "call_123",
			ToolResultIsError: true,
			Parts:             []runtime.ContentPart{{Type: runtime.ContentPartTypeText, Text: "sunny"}},
		}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion() error = %v", err)
	}
}

func TestOpenAICompatibleChatCompletionRotatesAPIKeyOnQuotaExceeded(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch calls {
		case 1:
			if got := r.Header.Get("Authorization"); got != "Bearer key-1" {
				t.Fatalf("Authorization = %q, want Bearer key-1", got)
			}
			w.WriteHeader(http.StatusTooManyRequests)
		case 2:
			if got := r.Header.Get("Authorization"); got != "Bearer key-2" {
				t.Fatalf("Authorization = %q, want Bearer key-2", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":     "chatcmpl-rotated",
				"object": "chat.completion",
				"model":  "gpt-4o-mini",
				"choices": []map[string]any{{
					"message": map[string]string{
						"role":    "assistant",
						"content": "hello from second key",
					},
				}},
			})
		default:
			t.Fatalf("unexpected call count %d", calls)
		}
	}))
	defer server.Close()

	p := NewOpenAICompatible("openai", server.URL, []string{"key-1", "key-2"}, server.Client())
	resp, err := p.ChatCompletion(context.Background(), runtime.Request{Model: "gpt-4o-mini"})
	if err != nil {
		t.Fatalf("ChatCompletion() error = %v", err)
	}
	if got := resp.Message.Parts[0].Text; got != "hello from second key" {
		t.Fatalf("resp.Message.Parts[0].Text = %q, want hello from second key", got)
	}
}

func TestOpenAICompatibleChatCompletionQuotaExceeded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	p := NewOpenAICompatible("openai", server.URL, []string{"test-key"}, server.Client())
	_, err := p.ChatCompletion(context.Background(), runtime.Request{Model: "gpt-4o-mini"})
	if err == nil {
		t.Fatal("ChatCompletion() error = nil, want error")
	}
	if NormalizeError(err) != ErrorKindQuotaExceeded {
		t.Fatalf("NormalizeError() = %q, want quota_exceeded", NormalizeError(err))
	}
}

func TestOpenAICompatibleChatCompletionRetryableOnServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	p := NewOpenAICompatible("openai", server.URL, []string{"test-key"}, server.Client())
	_, err := p.ChatCompletion(context.Background(), runtime.Request{Model: "gpt-4o-mini"})
	if err == nil {
		t.Fatal("ChatCompletion() error = nil, want error")
	}
	if NormalizeError(err) != ErrorKindRetryable {
		t.Fatalf("NormalizeError() = %q, want retryable", NormalizeError(err))
	}
}

func TestOpenAICompatibleChatCompletionFatalOnBadRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"tool_choice is required"}`))
	}))
	defer server.Close()

	p := NewOpenAICompatible("openai", server.URL, []string{"test-key"}, server.Client())
	_, err := p.ChatCompletion(context.Background(), runtime.Request{Model: "gpt-4o-mini"})
	if err == nil {
		t.Fatal("ChatCompletion() error = nil, want error")
	}
	if NormalizeError(err) != ErrorKindFatal {
		t.Fatalf("NormalizeError() = %q, want fatal", NormalizeError(err))
	}
	if !strings.Contains(err.Error(), `tool_choice is required`) {
		t.Fatalf("err = %q, want upstream response body", err)
	}
}

func TestOpenAICompatibleChatCompletionResumesRoundRobinAfterSuccessfulCall(t *testing.T) {
	var authHeaders []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeaders = append(authHeaders, r.Header.Get("Authorization"))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "chatcmpl-round-robin",
			"object": "chat.completion",
			"model":  "gpt-4o-mini",
			"choices": []map[string]any{{
				"message": map[string]string{
					"role":    "assistant",
					"content": "hello",
				},
			}},
		})
	}))
	defer server.Close()

	p := NewOpenAICompatible("openai", server.URL, []string{"key-1", "key-2"}, server.Client())
	p.setNextAPIKey(1)

	for range 2 {
		_, err := p.ChatCompletion(context.Background(), runtime.Request{Model: "gpt-4o-mini"})
		if err != nil {
			t.Fatalf("ChatCompletion() error = %v", err)
		}
	}

	if len(authHeaders) != 2 {
		t.Fatalf("len(authHeaders) = %d, want 2", len(authHeaders))
	}
	if authHeaders[0] != "Bearer key-2" || authHeaders[1] != "Bearer key-1" {
		t.Fatalf("Authorization headers = %#v, want key-2 then key-1", authHeaders)
	}
}

func TestOpenAICompatibleStreamCompletionWritesRawSSETraceWhenEnabled(t *testing.T) {
	tmpDir := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd() error = %v", err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("os.Chdir() error = %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()
	defer func() { _ = os.Unsetenv("SYROGO_TRACE") }()
	if err := os.Setenv("SYROGO_TRACE", "full"); err != nil {
		t.Fatalf("os.Setenv() error = %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-4o-mini\",\"choices\":[{\"delta\":{\"role\":\"assistant\"},\"finish_reason\":\"\"}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-4o-mini\",\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_123\",\"type\":\"function\",\"function\":{\"name\":\"Read\",\"arguments\":\"{\\\"file_path\\\":\\\"/tmp/a\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	ctx := context.WithValue(context.Background(), runtime.ContextKeyRequestID, "req-trace-openai-stream")
	p := NewOpenAICompatible("openai", server.URL, []string{"test-key"}, server.Client())
	ch, err := p.StreamCompletion(ctx, runtime.Request{Model: "gpt-4o-mini"})
	if err != nil {
		t.Fatalf("StreamCompletion() error = %v", err)
	}
	for range ch {
	}

	tracePath := filepath.Join(tmpDir, "tmp", "trace", "req-trace-openai-stream.outbound-openai-openai_chat.stream.txt")
	data, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v", tracePath, err)
	}
	if !strings.Contains(string(data), `"tool_calls"`) || !strings.Contains(string(data), `data: [DONE]`) {
		t.Fatalf("raw stream trace = %q, want tool_calls and done frame", string(data))
	}
}
