package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"syrogo/internal/runtime"
)

func TestEncodeOpenAIChatRequestUsesFirstTextPart(t *testing.T) {
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

	body, ok := payload.(map[string]any)
	if !ok {
		t.Fatalf("payload type = %T, want map[string]any", payload)
	}
	if body["model"] != "gpt-4o-mini" {
		t.Fatalf("payload model = %#v, want gpt-4o-mini", body["model"])
	}
	messages, ok := body["messages"].([]openAIChatMessage)
	if !ok {
		t.Fatalf("payload messages type = %T, want []openAIChatMessage", body["messages"])
	}
	if len(messages) != 1 || messages[0].Role != "user" || messages[0].Content != "hello" {
		t.Fatalf("payload messages = %#v, want single user hello message", messages)
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

	body := payload.(map[string]any)
	messages := body["messages"].([]openAIChatMessage)
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
			Message openAIChatMessage `json:"message"`
		}{
			{Message: openAIChatMessage{Role: "assistant", Content: "hello from upstream"}},
		},
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
}

func TestDecodeOpenAIChatResponseMapsAssistantToolCalls(t *testing.T) {
	resp, err := decodeOpenAIChatResponse(openAIChatResponseEnvelope{
		ID:     "chatcmpl-1",
		Object: "chat.completion",
		Model:  "gpt-4o-mini",
		Choices: []struct {
			Message openAIChatMessage `json:"message"`
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
}

func TestDecodeOpenAIChatResponseRejectsEmptyAssistantMessage(t *testing.T) {
	_, err := decodeOpenAIChatResponse(openAIChatResponseEnvelope{
		ID:     "chatcmpl-1",
		Object: "chat.completion",
		Model:  "gpt-4o-mini",
		Choices: []struct {
			Message openAIChatMessage `json:"message"`
		}{
			{Message: openAIChatMessage{Role: "assistant"}},
		},
	})
	if err == nil {
		t.Fatal("decodeOpenAIChatResponse() error = nil, want error")
	}
	if got := err.Error(); got != "upstream returned no content and no tool calls" {
		t.Fatalf("decodeOpenAIChatResponse() error = %q, want upstream returned no content and no tool calls", got)
	}
}

func TestEncodeOpenAIResponsesRequestMapsMessagesAndToolCalls(t *testing.T) {
	payload := encodeOpenAIResponsesRequest(runtime.Request{
		Model: "gpt-4o-mini",
		Messages: []runtime.Message{{
			Role:  runtime.MessageRoleUser,
			Parts: []runtime.ContentPart{{Type: runtime.ContentPartTypeText, Text: "hello"}},
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
	})

	body := payload.(map[string]any)
	input := body["input"].([]openAIResponsesInputItem)
	if len(input) != 3 {
		t.Fatalf("len(input) = %d, want 3", len(input))
	}
	if input[0].Type != "message" || input[0].Role != "user" {
		t.Fatalf("input[0] = %#v, want user message", input[0])
	}
	if len(input[0].Content) != 1 || input[0].Content[0].Type != "input_text" || input[0].Content[0].Text != "hello" {
		t.Fatalf("input[0].Content = %#v, want input_text hello", input[0].Content)
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

		var req struct {
			Model string `json:"model"`
			Input []struct {
				Type    string                    `json:"type"`
				Role    string                    `json:"role"`
				Content []openAIResponsesTextPart `json:"content"`
			} `json:"input"`
		}
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

	p := NewOpenAIResponsesCompatible("responses", server.URL, []string{"test-key"}, server.Client())
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

	p := NewOpenAIResponsesCompatible("responses", server.URL, []string{"test-key"}, server.Client())
	ch, err := p.StreamCompletion(context.Background(), runtime.Request{Model: "gpt-4o-mini"})
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
	if toolEvent.ToolCall.ID != "call_123" || toolEvent.ToolCall.Name != "get_weather" {
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

func TestOpenAICompatibleChatCompletionSendsToolCallingFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Role       string           `json:"role"`
				Content    string           `json:"content"`
				ToolCalls  []openAIToolCall `json:"tool_calls"`
				ToolCallID string           `json:"tool_call_id"`
			} `json:"messages"`
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
			Parts:      []runtime.ContentPart{{Type: runtime.ContentPartTypeText, Text: "sunny"}},
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

func TestOpenAICompatibleStreamCompletionEmitsToolCallDelta(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "chatcmpl-1",
			"object": "chat.completion",
			"model":  "gpt-4o-mini",
			"choices": []map[string]any{{
				"message": map[string]any{
					"role": "assistant",
					"tool_calls": []map[string]any{{
						"id":   "call_123",
						"type": "function",
						"function": map[string]any{
							"name":      "get_weather",
							"arguments": `{"city":"shanghai"}`,
						},
					}},
				},
			}},
		})
	}))
	defer server.Close()

	p := NewOpenAICompatible("openai", server.URL, []string{"test-key"}, server.Client())
	ch, err := p.StreamCompletion(context.Background(), runtime.Request{Model: "gpt-4o-mini"})
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
	if toolEvent.ToolCall.ID != "call_123" || toolEvent.ToolCall.Name != "get_weather" {
		t.Fatalf("toolEvent.ToolCall = %#v, want decoded tool call", toolEvent.ToolCall)
	}
}

func TestMockProviderStreamCompletionEmitsToolCallDelta(t *testing.T) {
	p := NewMock("mock")
	ch, err := p.StreamCompletion(context.Background(), runtime.Request{Model: "gpt-4o-mini"})
	if err != nil {
		t.Fatalf("StreamCompletion() error = %v", err)
	}

	var events []runtime.StreamEvent
	for event := range ch {
		events = append(events, event)
	}

	if len(events) != 3 {
		t.Fatalf("len(events) = %d, want 3", len(events))
	}
	if events[0].Type != runtime.StreamEventMessageStart {
		t.Fatalf("events[0].Type = %q, want message_start", events[0].Type)
	}
	if events[1].Type != runtime.StreamEventContentDelta {
		t.Fatalf("events[1].Type = %q, want content_delta", events[1].Type)
	}
	if events[1].Delta == nil || events[1].Delta.Text != "syrogo mock response" {
		t.Fatalf("events[1].Delta = %#v, want syrogo mock response", events[1].Delta)
	}
	if events[2].Type != runtime.StreamEventMessageEnd {
		t.Fatalf("events[2].Type = %q, want message_end", events[2].Type)
	}
	if events[2].FinishReason != runtime.FinishReasonStop {
		t.Fatalf("events[2].FinishReason = %q, want stop", events[2].FinishReason)
	}
}
