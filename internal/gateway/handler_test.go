package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"syrogo/internal/config"
	"syrogo/internal/execution"
	"syrogo/internal/provider"
	"syrogo/internal/router"
	"syrogo/internal/runtime"
	"syrogo/internal/semantic"
)

type failingProvider struct {
	name string
	err  error
}

func (p *failingProvider) Name() string {
	return p.name
}

func (p *failingProvider) ChatCompletion(_ context.Context, _ runtime.Request) (runtime.Response, error) {
	return runtime.Response{}, p.err
}

func (p *failingProvider) StreamCompletion(_ context.Context, _ runtime.Request) (<-chan runtime.StreamEvent, error) {
	return nil, p.err
}

func testRoutingConfig() config.RoutingConfig {
	return config.RoutingConfig{Rules: []config.RoutingRule{{
		Name:     "office",
		FromTags: []string{"office"},
		ToTags:   []string{"mock-tag"},
		Strategy: "failover",
	}}}
}

func testInbounds() []config.InboundSpec {
	return []config.InboundSpec{{
		Name:     "openai-entry",
		Protocol: "openai_chat",
		Path:     "/v1/chat/completions",
		Clients:  []config.ClientSpec{{Token: "client-token", Tag: "office"}},
	}}
}

func testDualProtocolInbounds() []config.InboundSpec {
	return []config.InboundSpec{
		{Name: "openai-entry", Protocol: "openai_chat", Path: "/v1/chat/completions", Clients: []config.ClientSpec{{Token: "client-token", Tag: "office"}}},
		{Name: "responses-entry", Protocol: "openai_responses", Path: "/v1/responses", Clients: []config.ClientSpec{{Token: "responses-token", Tag: "office"}}},
		{Name: "anthropic-entry", Protocol: "anthropic_messages", Path: "/v1/messages", Clients: []config.ClientSpec{{Token: "anthropic-token", Tag: "office"}}},
	}
}

func testOutbounds() []config.OutboundSpec {
	return []config.OutboundSpec{{Name: "mock", Protocol: "mock", Tag: "mock-tag"}}
}

func newTestHandler(t *testing.T, providers map[string]provider.Provider, routing config.RoutingConfig, inbounds []config.InboundSpec, outbounds []config.OutboundSpec) *Handler {
	t.Helper()

	r, err := router.New(routing, providers, outbounds)
	if err != nil {
		t.Fatalf("router.New() error = %v", err)
	}

	return New(r, execution.NewDispatcher(), inbounds, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
}

func authorizedRequest(method, path, token string, body []byte) *http.Request {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

func TestHealthzReturnsOK(t *testing.T) {
	h := newTestHandler(t, map[string]provider.Provider{"mock": provider.NewMock("mock")}, testRoutingConfig(), testInbounds(), testOutbounds())

	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

func TestChatCompletionsRejectsMissingToken(t *testing.T) {
	h := newTestHandler(t, map[string]provider.Provider{"mock": provider.NewMock("mock")}, testRoutingConfig(), testInbounds(), testOutbounds())
	mux := http.NewServeMux()
	h.Register(mux)

	body := []byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hello"}]}`)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body)))

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestChatCompletionsRejectsInvalidJSON(t *testing.T) {
	h := newTestHandler(t, map[string]provider.Provider{"mock": provider.NewMock("mock")}, testRoutingConfig(), testInbounds(), testOutbounds())
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/chat/completions", "client-token", []byte("{")))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestChatCompletionsLogsDecodeFailure(t *testing.T) {
	providers := map[string]provider.Provider{"mock": provider.NewMock("mock")}
	r, err := router.New(testRoutingConfig(), providers, testOutbounds())
	if err != nil {
		t.Fatalf("router.New() error = %v", err)
	}

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	h := New(r, execution.NewDispatcher(), testInbounds(), logger)
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/chat/completions", "client-token", []byte("{")))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	got := logBuf.String()
	if !strings.Contains(got, "request decode failed") || !strings.Contains(got, "path=/v1/chat/completions") {
		t.Fatalf("logs = %q, want decode failure log with path", got)
	}
}

func TestChatCompletionsUsesDispatcherBackedPlan(t *testing.T) {
	h := newTestHandler(t, map[string]provider.Provider{"mock": provider.NewMock("mock")}, testRoutingConfig(), testInbounds(), testOutbounds())
	mux := http.NewServeMux()
	h.Register(mux)

	body, err := json.Marshal(map[string]any{"model": "gpt-4", "messages": []map[string]string{{"role": "user", "content": "hello"}}})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/chat/completions", "client-token", body))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
}

func TestChatCompletionsAcceptsStructuredContent(t *testing.T) {
	h := newTestHandler(t, map[string]provider.Provider{"mock": provider.NewMock("mock")}, testRoutingConfig(), testInbounds(), testOutbounds())
	mux := http.NewServeMux()
	h.Register(mux)

	body, err := json.Marshal(map[string]any{
		"model": "gpt-4",
		"messages": []map[string]any{{
			"role": "user",
			"content": []map[string]any{{
				"type": "text",
				"text": "hello",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/chat/completions", "client-token", body))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
}

func TestBuildRuntimeRequestPreservesOpenAIToolCallingFields(t *testing.T) {
	req := inboundRequest{
		Model: "gpt-4o-mini",
		Messages: []inboundMessage{{
			Role: "assistant",
			ToolCalls: []inboundToolCall{{
				ID:   "call_123",
				Type: "function",
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{Name: "get_weather", Arguments: `{"city":"shanghai"}`},
			}},
		}, {
			Role:       "tool",
			ToolCallID: "call_123",
			Content:    json.RawMessage(`"sunny"`),
		}},
	}

	got, err := buildRuntimeRequest(req)
	if err != nil {
		t.Fatalf("buildRuntimeRequest() error = %v", err)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("len(Messages) = %d, want 2", len(got.Messages))
	}
	if len(got.Messages[0].ToolCalls) != 1 {
		t.Fatalf("len(Messages[0].ToolCalls) = %d, want 1", len(got.Messages[0].ToolCalls))
	}
	if got.Messages[0].ToolCalls[0].ID != "call_123" || got.Messages[0].ToolCalls[0].Name != "get_weather" {
		t.Fatalf("Messages[0].ToolCalls = %#v, want preserved tool call", got.Messages[0].ToolCalls)
	}
	if got.Messages[1].ToolCallID != "call_123" {
		t.Fatalf("Messages[1].ToolCallID = %q, want call_123", got.Messages[1].ToolCallID)
	}
	if got.Messages[1].Parts[0].Text != "sunny" {
		t.Fatalf("Messages[1].Parts = %#v, want sunny result", got.Messages[1].Parts)
	}
}

func TestBuildRuntimeRequestPreservesAnthropicToolBlocks(t *testing.T) {
	req := inboundRequest{
		Model: "claude-sonnet-4-5",
		Messages: []inboundMessage{{
			Role: "assistant",
			Content: json.RawMessage(`[
				{"type":"tool_use","id":"toolu_123","name":"get_weather","input":{"city":"shanghai"}}
			]`),
		}, {
			Role: "tool",
			Content: json.RawMessage(`[
				{"type":"tool_result","tool_use_id":"toolu_123","content":[{"type":"text","text":"sunny"}]}
			]`),
		}},
	}

	got, err := buildRuntimeRequest(req)
	if err != nil {
		t.Fatalf("buildRuntimeRequest() error = %v", err)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("len(Messages) = %d, want 2", len(got.Messages))
	}
	if len(got.Messages[0].ToolCalls) != 1 {
		t.Fatalf("len(Messages[0].ToolCalls) = %d, want 1", len(got.Messages[0].ToolCalls))
	}
	if got.Messages[0].ToolCalls[0].ID != "toolu_123" || got.Messages[0].ToolCalls[0].Arguments != `{"city":"shanghai"}` {
		t.Fatalf("Messages[0].ToolCalls = %#v, want anthropic tool_use mapping", got.Messages[0].ToolCalls)
	}
	if got.Messages[1].ToolCallID != "toolu_123" {
		t.Fatalf("Messages[1].ToolCallID = %q, want toolu_123", got.Messages[1].ToolCallID)
	}
	if got.Messages[1].Parts[0].Text != "sunny" {
		t.Fatalf("Messages[1].Parts = %#v, want sunny", got.Messages[1].Parts)
	}
}

func TestBuildRuntimeRequestPreservesAnthropicJSONToolResultPayload(t *testing.T) {
	req := inboundRequest{
		Model: "claude-sonnet-4-5",
		Messages: []inboundMessage{{
			Role: "tool",
			Content: json.RawMessage(`[
				{"type":"tool_result","tool_use_id":"toolu_123","content":[{"type":"json","value":{"city":"shanghai","forecast":"sunny"}}]}
			]`),
		}},
	}

	got, err := buildRuntimeRequest(req)
	if err != nil {
		t.Fatalf("buildRuntimeRequest() error = %v", err)
	}
	if len(got.Messages) != 1 {
		t.Fatalf("len(Messages) = %d, want 1", len(got.Messages))
	}
	if got.Messages[0].ToolCallID != "toolu_123" {
		t.Fatalf("Messages[0].ToolCallID = %q, want toolu_123", got.Messages[0].ToolCallID)
	}
	if len(got.Messages[0].Parts) != 1 || got.Messages[0].Parts[0].Type != runtime.ContentPartTypeJSON {
		t.Fatalf("Messages[0].Parts = %#v, want single json content part", got.Messages[0].Parts)
	}
	if string(got.Messages[0].Parts[0].Data) != `{"city":"shanghai","forecast":"sunny"}` {
		t.Fatalf("Messages[0].Parts[0].Data = %s, want raw json payload", got.Messages[0].Parts[0].Data)
	}
}

func TestLowerSemanticTurnPreservesMixedTextAndToolCallOrderWithinRuntimeShape(t *testing.T) {
	message := lowerSemanticTurn(semantic.Turn{
		Role: semantic.RoleAssistant,
		Segments: []semantic.Segment{{
			Kind: semantic.SegmentText,
			Text: "thinking",
		}, {
			Kind: semantic.SegmentToolCall,
			ToolCall: &semantic.ToolCall{
				ID:        "call_123",
				Name:      "get_weather",
				Arguments: json.RawMessage(`{"city":"shanghai"}`),
			},
		}, {
			Kind: semantic.SegmentText,
			Text: "done",
		}},
	})

	if message.Role != runtime.MessageRoleAssistant {
		t.Fatalf("message.Role = %q, want assistant", message.Role)
	}
	if len(message.Parts) != 2 {
		t.Fatalf("len(message.Parts) = %d, want 2", len(message.Parts))
	}
	if message.Parts[0].Text != "thinking" || message.Parts[1].Text != "done" {
		t.Fatalf("message.Parts = %#v, want preserved text parts", message.Parts)
	}
	if len(message.ToolCalls) != 1 {
		t.Fatalf("len(message.ToolCalls) = %d, want 1", len(message.ToolCalls))
	}
	if message.ToolCalls[0].ID != "call_123" || message.ToolCalls[0].Name != "get_weather" || message.ToolCalls[0].Arguments != `{"city":"shanghai"}` {
		t.Fatalf("message.ToolCalls = %#v, want preserved tool call", message.ToolCalls)
	}
}

func TestLowerSemanticTurnLowersToolResultMixedContentToToolMessage(t *testing.T) {
	message := lowerSemanticTurn(semantic.Turn{
		Role: semantic.RoleTool,
		Segments: []semantic.Segment{{
			Kind: semantic.SegmentToolResult,
			ToolResult: &semantic.ToolResult{
				ToolCallID: "toolu_123",
				Content: []semantic.Segment{{
					Kind: semantic.SegmentText,
					Text: "lookup failed",
				}, {
					Kind: semantic.SegmentData,
					Data: &semantic.DataPart{
						Format: "json",
						Value:  json.RawMessage(`{"city":"shanghai","forecast":"sunny"}`),
					},
				}},
			},
		}},
	})

	if message.Role != runtime.MessageRoleTool {
		t.Fatalf("message.Role = %q, want tool", message.Role)
	}
	if message.ToolCallID != "toolu_123" {
		t.Fatalf("message.ToolCallID = %q, want toolu_123", message.ToolCallID)
	}
	if len(message.Parts) != 2 {
		t.Fatalf("len(message.Parts) = %d, want 2", len(message.Parts))
	}
	if message.Parts[0].Type != runtime.ContentPartTypeText || message.Parts[0].Text != "lookup failed" {
		t.Fatalf("message.Parts[0] = %#v, want text part", message.Parts[0])
	}
	if message.Parts[1].Type != runtime.ContentPartTypeJSON {
		t.Fatalf("message.Parts[1].Type = %q, want json", message.Parts[1].Type)
	}
	if string(message.Parts[1].Data) != `{"city":"shanghai","forecast":"sunny"}` {
		t.Fatalf("message.Parts[1].Data = %s, want raw json payload", message.Parts[1].Data)
	}
}

func TestLowerSemanticTurnFallsBackToEmptyTextWhenToolResultContentIsEmpty(t *testing.T) {
	message := lowerSemanticTurn(semantic.Turn{
		Role: semantic.RoleTool,
		Segments: []semantic.Segment{{
			Kind:       semantic.SegmentToolResult,
			ToolResult: &semantic.ToolResult{ToolCallID: "toolu_123"},
		}},
	})

	if message.ToolCallID != "toolu_123" {
		t.Fatalf("message.ToolCallID = %q, want toolu_123", message.ToolCallID)
	}
	if len(message.Parts) != 1 {
		t.Fatalf("len(message.Parts) = %d, want 1", len(message.Parts))
	}
	if message.Parts[0].Type != runtime.ContentPartTypeText || message.Parts[0].Text != "" {
		t.Fatalf("message.Parts[0] = %#v, want empty text fallback", message.Parts[0])
	}
}

func TestWriteAnthropicMessageResponseMapsToolUseStopReason(t *testing.T) {
	w := httptest.NewRecorder()
	writeAnthropicMessageResponse(w, runtime.Response{
		ID:           "msg_123",
		Object:       "message",
		Model:        "claude-sonnet-4-5",
		FinishReason: runtime.FinishReasonToolUse,
		Message: runtime.Message{
			Role:      runtime.MessageRoleAssistant,
			ToolCalls: []runtime.ToolCall{{ID: "tool_123", Name: "get_weather", Arguments: `{"city":"shanghai"}`}},
		},
	})

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body struct {
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"content"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if body.StopReason != "tool_use" {
		t.Fatalf("body.StopReason = %q, want tool_use", body.StopReason)
	}
	if len(body.Content) != 1 || body.Content[0].Type != "tool_use" || body.Content[0].ID != "tool_123" {
		t.Fatalf("body.Content = %#v, want assistant tool_use block", body.Content)
	}
}

func TestBuildRuntimeRequestPreservesAnthropicTools(t *testing.T) {
	req := inboundRequest{
		Model: "claude-sonnet-4-5",
		Tools: []inboundToolDefinition{{
			Name:        "get_weather",
			Description: "Query weather by city",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`),
		}},
		Messages: []inboundMessage{{
			Role:    "user",
			Content: json.RawMessage(`"hello"`),
		}},
	}

	got, err := buildRuntimeRequest(req)
	if err != nil {
		t.Fatalf("buildRuntimeRequest() error = %v", err)
	}
	if len(got.Tools) != 1 {
		t.Fatalf("len(got.Tools) = %d, want 1", len(got.Tools))
	}
	if got.Tools[0].Name != "get_weather" || got.Tools[0].Description != "Query weather by city" {
		t.Fatalf("got.Tools[0] = %#v, want preserved tool metadata", got.Tools[0])
	}
	var schema map[string]any
	if err := json.Unmarshal(got.Tools[0].InputSchema, &schema); err != nil {
		t.Fatalf("json.Unmarshal(got.Tools[0].InputSchema) error = %v", err)
	}
	if schema["type"] != "object" {
		t.Fatalf("schema[type] = %#v, want object", schema["type"])
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema[properties] = %#v, want object", schema["properties"])
	}
	city, ok := properties["city"].(map[string]any)
	if !ok || city["type"] != "string" {
		t.Fatalf("schema city = %#v, want string property", properties["city"])
	}
}

func TestBuildRuntimeRequestPreservesAnthropicSystemAndMaxTokens(t *testing.T) {
	req := inboundRequest{
		Model:     "claude-sonnet-4-5",
		System:    json.RawMessage(`"You are a careful assistant."`),
		MaxTokens: 256,
		Messages: []inboundMessage{{
			Role:    "user",
			Content: json.RawMessage(`"hello"`),
		}},
	}

	got, err := buildRuntimeRequest(req)
	if err != nil {
		t.Fatalf("buildRuntimeRequest() error = %v", err)
	}
	if got.System != "You are a careful assistant." {
		t.Fatalf("got.System = %q, want preserved system prompt", got.System)
	}
	if got.MaxTokens != 256 {
		t.Fatalf("got.MaxTokens = %d, want 256", got.MaxTokens)
	}
}

func TestBuildRuntimeRequestSupportsAnthropicSystemTextBlocks(t *testing.T) {
	req := inboundRequest{
		Model: "claude-sonnet-4-5",
		System: json.RawMessage(`[
			{"type":"text","text":"You are a careful assistant."},
			{"type":"text","text":"Answer briefly."}
		]`),
		Messages: []inboundMessage{{
			Role:    "user",
			Content: json.RawMessage(`"hello"`),
		}},
	}

	got, err := buildRuntimeRequest(req)
	if err != nil {
		t.Fatalf("buildRuntimeRequest() error = %v", err)
	}
	if got.System != "You are a careful assistant.\nAnswer briefly." {
		t.Fatalf("got.System = %q, want joined anthropic system text blocks", got.System)
	}
}

func TestBuildRuntimeRequestFromResponsesSupportsStringInput(t *testing.T) {
	req := openAIResponsesRequest{
		Model:  "gpt-4o-mini",
		Input:  json.RawMessage(`"hello"`),
		Stream: true,
	}

	got, err := buildRuntimeRequestFromResponses(req)
	if err != nil {
		t.Fatalf("buildRuntimeRequestFromResponses() error = %v", err)
	}
	if got.Model != "gpt-4o-mini" || !got.Stream {
		t.Fatalf("got = %#v, want model and stream preserved", got)
	}
	if len(got.Messages) != 1 || got.Messages[0].Role != runtime.MessageRoleUser || got.Messages[0].Parts[0].Text != "hello" {
		t.Fatalf("Messages = %#v, want single user hello message", got.Messages)
	}
}

func TestBuildRuntimeRequestFromResponsesFunctionOutputDropsNonTextParts(t *testing.T) {
	req := openAIResponsesRequest{
		Model: "gpt-4o-mini",
		Input: json.RawMessage(`[
			{"type":"function_call_output","call_id":"call_123","output":[
				{"type":"output_text","text":"partial text"},
				{"type":"json","value":{"city":"shanghai"}}
			]}
		]`),
	}

	got, err := buildRuntimeRequestFromResponses(req)
	if err != nil {
		t.Fatalf("buildRuntimeRequestFromResponses() error = %v", err)
	}
	if len(got.Messages) != 1 {
		t.Fatalf("len(got.Messages) = %d, want 1", len(got.Messages))
	}
	if got.Messages[0].Role != runtime.MessageRoleTool {
		t.Fatalf("got.Messages[0].Role = %q, want tool", got.Messages[0].Role)
	}
	if got.Messages[0].ToolCallID != "call_123" {
		t.Fatalf("got.Messages[0].ToolCallID = %q, want call_123", got.Messages[0].ToolCallID)
	}
	if len(got.Messages[0].Parts) != 1 {
		t.Fatalf("len(got.Messages[0].Parts) = %d, want 1", len(got.Messages[0].Parts))
	}
	if got.Messages[0].Parts[0].Type != runtime.ContentPartTypeText || got.Messages[0].Parts[0].Text != "partial text" {
		t.Fatalf("got.Messages[0].Parts = %#v, want only text part preserved", got.Messages[0].Parts)
	}
}

func TestBuildRuntimeRequestPreservesAnthropicMixedToolResultContentOrder(t *testing.T) {
	req := inboundRequest{
		Model: "claude-sonnet-4-5",
		Messages: []inboundMessage{{
			Role: "tool",
			Content: json.RawMessage(`[
				{"type":"tool_result","tool_use_id":"toolu_123","content":[
					{"type":"text","text":"lookup failed"},
					{"type":"json","value":{"city":"shanghai","forecast":"sunny"}}
				]}
			]`),
		}},
	}

	got, err := buildRuntimeRequest(req)
	if err != nil {
		t.Fatalf("buildRuntimeRequest() error = %v", err)
	}
	if len(got.Messages) != 1 {
		t.Fatalf("len(got.Messages) = %d, want 1", len(got.Messages))
	}
	if got.Messages[0].ToolCallID != "toolu_123" {
		t.Fatalf("got.Messages[0].ToolCallID = %q, want toolu_123", got.Messages[0].ToolCallID)
	}
	if len(got.Messages[0].Parts) != 2 {
		t.Fatalf("len(got.Messages[0].Parts) = %d, want 2", len(got.Messages[0].Parts))
	}
	if got.Messages[0].Parts[0].Type != runtime.ContentPartTypeText || got.Messages[0].Parts[0].Text != "lookup failed" {
		t.Fatalf("got.Messages[0].Parts[0] = %#v, want first text part", got.Messages[0].Parts[0])
	}
	if got.Messages[0].Parts[1].Type != runtime.ContentPartTypeJSON {
		t.Fatalf("got.Messages[0].Parts[1].Type = %q, want json", got.Messages[0].Parts[1].Type)
	}
	if string(got.Messages[0].Parts[1].Data) != `{"city":"shanghai","forecast":"sunny"}` {
		t.Fatalf("got.Messages[0].Parts[1].Data = %s, want raw json payload", got.Messages[0].Parts[1].Data)
	}
}

func TestBuildRuntimeRequestAnthropicMixedAssistantContentSplitsTextAndToolCall(t *testing.T) {
	req := inboundRequest{
		Model: "claude-sonnet-4-5",
		Messages: []inboundMessage{{
			Role: "assistant",
			Content: json.RawMessage(`[
				{"type":"text","text":"thinking"},
				{"type":"tool_use","id":"toolu_123","name":"get_weather","input":{"city":"shanghai"}},
				{"type":"text","text":"done"}
			]`),
		}},
	}

	got, err := buildRuntimeRequest(req)
	if err != nil {
		t.Fatalf("buildRuntimeRequest() error = %v", err)
	}
	if len(got.Messages) != 1 {
		t.Fatalf("len(got.Messages) = %d, want 1", len(got.Messages))
	}
	if got.Messages[0].Role != runtime.MessageRoleAssistant {
		t.Fatalf("got.Messages[0].Role = %q, want assistant", got.Messages[0].Role)
	}
	if len(got.Messages[0].Parts) != 2 {
		t.Fatalf("len(got.Messages[0].Parts) = %d, want 2", len(got.Messages[0].Parts))
	}
	if got.Messages[0].Parts[0].Text != "thinking" || got.Messages[0].Parts[1].Text != "done" {
		t.Fatalf("got.Messages[0].Parts = %#v, want preserved text parts", got.Messages[0].Parts)
	}
	if len(got.Messages[0].ToolCalls) != 1 {
		t.Fatalf("len(got.Messages[0].ToolCalls) = %d, want 1", len(got.Messages[0].ToolCalls))
	}
	if got.Messages[0].ToolCalls[0].ID != "toolu_123" || got.Messages[0].ToolCalls[0].Arguments != `{"city":"shanghai"}` {
		t.Fatalf("got.Messages[0].ToolCalls = %#v, want preserved tool call", got.Messages[0].ToolCalls)
	}
}

func TestBuildRuntimeRequestRejectsToolWithoutName(t *testing.T) {
	req := inboundRequest{
		Model: "claude-sonnet-4-5",
		Tools: []inboundToolDefinition{{
			Description: "missing name",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		}},
		Messages: []inboundMessage{{
			Role:    "user",
			Content: json.RawMessage(`"hello"`),
		}},
	}

	_, err := buildRuntimeRequest(req)
	if err == nil {
		t.Fatal("buildRuntimeRequest() error = nil, want error")
	}
	if got := err.Error(); got != "tool name is required" {
		t.Fatalf("buildRuntimeRequest() error = %q, want tool name is required", got)
	}
}

func TestBuildRuntimeRequestRejectsInvalidToolInputSchema(t *testing.T) {
	req := inboundRequest{
		Model: "claude-sonnet-4-5",
		Tools: []inboundToolDefinition{{
			Name:        "get_weather",
			Description: "invalid schema",
			InputSchema: json.RawMessage(`{"type":`),
		}},
		Messages: []inboundMessage{{
			Role:    "user",
			Content: json.RawMessage(`"hello"`),
		}},
	}

	_, err := buildRuntimeRequest(req)
	if err == nil {
		t.Fatal("buildRuntimeRequest() error = nil, want error")
	}
	if !strings.Contains(err.Error(), `invalid tool input_schema for "get_weather"`) {
		t.Fatalf("buildRuntimeRequest() error = %q, want invalid tool input_schema message", err.Error())
	}
}

func TestBuildRuntimeRequestFromResponsesSupportsItemArray(t *testing.T) {
	req := openAIResponsesRequest{
		Model: "gpt-4o-mini",
		Input: json.RawMessage(`[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]},
			{"type":"function_call","call_id":"call_123","name":"get_weather","input":{"city":"shanghai"}},
			{"type":"function_call_output","call_id":"call_123","output":[{"type":"output_text","text":"sunny"}]}
		]`),
	}

	got, err := buildRuntimeRequestFromResponses(req)
	if err != nil {
		t.Fatalf("buildRuntimeRequestFromResponses() error = %v", err)
	}
	if len(got.Messages) != 3 {
		t.Fatalf("len(Messages) = %d, want 3", len(got.Messages))
	}
	if got.Messages[0].Role != runtime.MessageRoleUser || got.Messages[0].Parts[0].Text != "hello" {
		t.Fatalf("Messages[0] = %#v, want user hello", got.Messages[0])
	}
	if len(got.Messages[1].ToolCalls) != 1 || got.Messages[1].ToolCalls[0].ID != "call_123" || got.Messages[1].ToolCalls[0].Arguments != `{"city":"shanghai"}` {
		t.Fatalf("Messages[1].ToolCalls = %#v, want function_call mapping", got.Messages[1].ToolCalls)
	}
	if got.Messages[2].ToolCallID != "call_123" || got.Messages[2].Parts[0].Text != "sunny" {
		t.Fatalf("Messages[2] = %#v, want tool output mapping", got.Messages[2])
	}
}

func TestResponsesRejectsUnsupportedInputItemType(t *testing.T) {
	h := newTestHandler(t, map[string]provider.Provider{"mock": provider.NewMock("mock")}, testRoutingConfig(), testDualProtocolInbounds(), testOutbounds())
	mux := http.NewServeMux()
	h.Register(mux)

	body, err := json.Marshal(map[string]any{
		"model": "gpt-4o-mini",
		"input": []map[string]any{{"type": "image", "url": "https://example.com/a.png"}},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/responses", "responses-token", body))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "unsupported responses input item type") {
		t.Fatalf("body = %q, want unsupported item type message", w.Body.String())
	}
}

func TestResponsesUsesDispatcherBackedPlan(t *testing.T) {
	h := newTestHandler(t, map[string]provider.Provider{"mock": provider.NewMock("mock")}, testRoutingConfig(), testDualProtocolInbounds(), testOutbounds())
	mux := http.NewServeMux()
	h.Register(mux)

	body, err := json.Marshal(map[string]any{
		"model": "gpt-4o-mini",
		"input": "hello",
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/responses", "responses-token", body))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"output"`) || !strings.Contains(w.Body.String(), `"type":"message"`) {
		t.Fatalf("body = %q, want responses output payload", w.Body.String())
	}
}

func TestResponsesStreamsSSE(t *testing.T) {
	h := newTestHandler(t, map[string]provider.Provider{"mock": provider.NewMock("mock")}, testRoutingConfig(), testDualProtocolInbounds(), testOutbounds())
	mux := http.NewServeMux()
	h.Register(mux)

	body, err := json.Marshal(map[string]any{
		"model":  "gpt-4o-mini",
		"stream": true,
		"input":  "hello",
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/responses", "responses-token", body))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}
	if !strings.Contains(w.Body.String(), "event: response.created") || !strings.Contains(w.Body.String(), "event: response.completed") {
		t.Fatalf("body = %q, want responses SSE lifecycle", w.Body.String())
	}
}

func TestResponsesUsesOpenAIResponsesProvider(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("path = %q, want /responses", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization = %q, want Bearer test-key", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "resp_123",
			"object": "response",
			"model":  "gpt-4o-mini",
			"output": []map[string]any{{
				"type": "message",
				"role": "assistant",
				"content": []map[string]any{{
					"type": "output_text",
					"text": "hello from responses upstream",
				}},
			}},
		})
	}))
	defer upstream.Close()

	h := newTestHandler(t, map[string]provider.Provider{
		"responses": provider.NewOpenAIResponsesCompatible("responses", upstream.URL, []string{"test-key"}, upstream.Client()),
	}, config.RoutingConfig{Rules: []config.RoutingRule{{Name: "office", FromTags: []string{"office"}, ToTags: []string{"responses-tag"}, Strategy: "failover"}}}, testDualProtocolInbounds(), []config.OutboundSpec{{Name: "responses", Protocol: "openai_responses", Endpoint: upstream.URL, AuthToken: "test-key", Tag: "responses-tag"}})
	mux := http.NewServeMux()
	h.Register(mux)

	body, err := json.Marshal(map[string]any{"model": "gpt-4o-mini", "input": "hello"})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/responses", "responses-token", body))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "hello from responses upstream") {
		t.Fatalf("body = %q, want upstream responses content", w.Body.String())
	}
}

func TestChatCompletionsStreamsSSE(t *testing.T) {
	h := newTestHandler(t, map[string]provider.Provider{"mock": provider.NewMock("mock")}, testRoutingConfig(), testInbounds(), testOutbounds())
	mux := http.NewServeMux()
	h.Register(mux)

	body, err := json.Marshal(map[string]any{"model": "gpt-4", "stream": true, "messages": []map[string]string{{"role": "user", "content": "hello"}}})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/chat/completions", "client-token", body))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}
	if !strings.Contains(w.Body.String(), "data: [DONE]\n\n") {
		t.Fatalf("body = %q, want done sentinel", w.Body.String())
	}
}

func TestAnthropicMessagesUsesDispatcherBackedPlan(t *testing.T) {
	h := newTestHandler(t, map[string]provider.Provider{"mock": provider.NewMock("mock")}, testRoutingConfig(), testDualProtocolInbounds(), testOutbounds())
	mux := http.NewServeMux()
	h.Register(mux)

	body, err := json.Marshal(map[string]any{"model": "claude-sonnet-4-5", "messages": []map[string]string{{"role": "user", "content": "hello"}}})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/messages", "anthropic-token", body))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	var resp struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if resp.Type != "message" || resp.Role != "assistant" || len(resp.Content) != 1 || resp.Content[0].Text == "" {
		t.Fatalf("resp = %#v, want anthropic message response", resp)
	}
}

func TestAnthropicMessagesStreamsToolUseAndSnakeCaseUsage(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          "msg_tool",
			"type":        "message",
			"role":        "assistant",
			"model":       "claude-sonnet-4-5",
			"stop_reason": "tool_use",
			"content": []map[string]any{{
				"type":  "tool_use",
				"id":    "toolu_123",
				"name":  "get_weather",
				"input": map[string]any{"city": "shanghai"},
			}},
			"usage": map[string]any{
				"input_tokens":  11,
				"output_tokens": 7,
			},
		})
	}))
	defer upstream.Close()

	h := newTestHandler(t, map[string]provider.Provider{
		"anthropic": provider.NewAnthropicMessagesCompatible("anthropic", upstream.URL, []string{"test-key"}, upstream.Client()),
	}, config.RoutingConfig{Rules: []config.RoutingRule{{Name: "office", FromTags: []string{"office"}, ToTags: []string{"anthropic-tag"}, Strategy: "failover"}}}, testDualProtocolInbounds(), []config.OutboundSpec{{Name: "anthropic", Protocol: "anthropic_messages", Endpoint: upstream.URL, AuthToken: "test-key", Tag: "anthropic-tag"}})
	mux := http.NewServeMux()
	h.Register(mux)

	body, err := json.Marshal(map[string]any{"model": "claude-sonnet-4-5", "stream": true, "messages": []map[string]string{{"role": "user", "content": "hello"}}})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/messages", "anthropic-token", body))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	got := w.Body.String()
	if !strings.Contains(got, "event: message_start\n") || !strings.Contains(got, `"usage":{"input_tokens":11,"output_tokens":0}`) {
		t.Fatalf("body = %q, want message_start with initial anthropic usage", got)
	}
	if !strings.Contains(got, "event: content_block_start\n") || !strings.Contains(got, `"type":"tool_use"`) {
		t.Fatalf("body = %q, want anthropic tool_use frame", got)
	}
	if !strings.Contains(got, `"index":0`) {
		t.Fatalf("body = %q, want content block index in anthropic frames", got)
	}
	if !strings.Contains(got, "event: content_block_stop\n") {
		t.Fatalf("body = %q, want content_block_stop frame", got)
	}
	if !strings.Contains(got, `"input_tokens":11`) || !strings.Contains(got, `"output_tokens":7`) || strings.Contains(got, `"InputTokens"`) {
		t.Fatalf("body = %q, want snake_case anthropic usage", got)
	}
	if !strings.Contains(got, `"stop_reason":"tool_use"`) {
		t.Fatalf("body = %q, want stop_reason in message_delta", got)
	}
	if !strings.Contains(got, "event: message_stop\n") || !strings.Contains(got, "event: done\n") {
		t.Fatalf("body = %q, want closing anthropic events", got)
	}
}

func TestAnthropicMessagesStreamingAlwaysEmitsUsageShape(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          "msg_text",
			"type":        "message",
			"role":        "assistant",
			"model":       "claude-sonnet-4-5",
			"stop_reason": "end_turn",
			"content": []map[string]any{{
				"type": "text",
				"text": "hello from upstream",
			}},
		})
	}))
	defer upstream.Close()

	h := newTestHandler(t, map[string]provider.Provider{
		"anthropic": provider.NewAnthropicMessagesCompatible("anthropic", upstream.URL, []string{"test-key"}, upstream.Client()),
	}, config.RoutingConfig{Rules: []config.RoutingRule{{Name: "office", FromTags: []string{"office"}, ToTags: []string{"anthropic-tag"}, Strategy: "failover"}}}, testDualProtocolInbounds(), []config.OutboundSpec{{Name: "anthropic", Protocol: "anthropic_messages", Endpoint: upstream.URL, AuthToken: "test-key", Tag: "anthropic-tag"}})
	mux := http.NewServeMux()
	h.Register(mux)

	body, err := json.Marshal(map[string]any{"model": "claude-sonnet-4-5", "stream": true, "messages": []map[string]string{{"role": "user", "content": "hello"}}})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/messages", "anthropic-token", body))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	got := w.Body.String()
	if !strings.Contains(got, `"usage":{"input_tokens":0,"output_tokens":0}`) {
		t.Fatalf("body = %q, want stable zero usage object for anthropic client compatibility", got)
	}
	if !strings.Contains(got, `"stop_reason":"end_turn"`) {
		t.Fatalf("body = %q, want normalized anthropic stop reason", got)
	}
}

func TestChatCompletionsStreamsToolCallDelta(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "chatcmpl-tool",
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
	defer upstream.Close()

	h := newTestHandler(t, map[string]provider.Provider{
		"openai": provider.NewOpenAICompatible("openai", upstream.URL, []string{"test-key"}, upstream.Client()),
	}, config.RoutingConfig{Rules: []config.RoutingRule{{Name: "office", FromTags: []string{"office"}, ToTags: []string{"openai-tag"}, Strategy: "failover"}}}, testInbounds(), []config.OutboundSpec{{Name: "openai", Protocol: "openai_chat", Endpoint: upstream.URL, AuthToken: "test-key", Tag: "openai-tag"}})
	mux := http.NewServeMux()
	h.Register(mux)

	body, err := json.Marshal(map[string]any{"model": "gpt-4o-mini", "stream": true, "messages": []map[string]string{{"role": "user", "content": "hello"}}})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/chat/completions", "client-token", body))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "data: {\"") {
		t.Fatalf("body = %q, want OpenAI SSE data frames", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "event: message_start") {
		t.Fatalf("body = %q, should not include custom event names for OpenAI SSE", w.Body.String())
	}
}

func TestAnthropicMessagesWritesDebugSnapshot(t *testing.T) {
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
	if err := os.Setenv("SYROGO_TRACE", "inbound"); err != nil {
		t.Fatalf("os.Setenv() error = %v", err)
	}

	h := newTestHandler(t, map[string]provider.Provider{"mock": provider.NewMock("mock")}, testRoutingConfig(), testDualProtocolInbounds(), testOutbounds())
	mux := http.NewServeMux()
	h.Register(mux)

	body, err := json.Marshal(map[string]any{
		"model":      "claude-sonnet-4-5",
		"system":     "You are a careful assistant.",
		"max_tokens": 128,
		"messages":   []map[string]string{{"role": "user", "content": "hello"}},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/messages", "anthropic-token", body))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
	}

	matches, err := filepath.Glob(filepath.Join(tmpDir, "tmp", "trace", "*.inbound.json"))
	if err != nil {
		t.Fatalf("filepath.Glob() error = %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("debug snapshot count = %d, want 1", len(matches))
	}

	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}

	var snap struct {
		Path         string         `json:"path"`
		Inbound      string         `json:"inbound"`
		ClientTag    string         `json:"client_tag"`
		RawBody      map[string]any `json:"raw_body"`
		Parsed       map[string]any `json:"parsed"`
		Runtime      map[string]any `json:"runtime"`
		PlannedModel string         `json:"planned_model"`
		ResolvedTo   []string       `json:"resolved_to"`
	}
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if snap.Path != "/v1/messages" || snap.Inbound != "anthropic-entry" || snap.ClientTag != "office" {
		t.Fatalf("snapshot meta = %#v, want anthropic request metadata", snap)
	}
	if snap.PlannedModel != "claude-sonnet-4-5" {
		t.Fatalf("snap.PlannedModel = %q, want claude-sonnet-4-5", snap.PlannedModel)
	}
	if len(snap.ResolvedTo) != 1 || snap.ResolvedTo[0] != "mock-tag" {
		t.Fatalf("snap.ResolvedTo = %#v, want [mock-tag]", snap.ResolvedTo)
	}
	if snap.RawBody["model"] != "claude-sonnet-4-5" {
		t.Fatalf("snap.RawBody = %#v, want model preserved", snap.RawBody)
	}
	if snap.Parsed["system"] != "You are a careful assistant." {
		t.Fatalf("snap.Parsed = %#v, want parsed system", snap.Parsed)
	}
	if snap.Runtime["system"] != "You are a careful assistant." {
		t.Fatalf("snap.Runtime = %#v, want runtime system", snap.Runtime)
	}
}

func TestAnthropicMessagesDebugSnapshotDisabledByDefault(t *testing.T) {
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
	_ = os.Unsetenv("SYROGO_TRACE")

	h := newTestHandler(t, map[string]provider.Provider{"mock": provider.NewMock("mock")}, testRoutingConfig(), testDualProtocolInbounds(), testOutbounds())
	mux := http.NewServeMux()
	h.Register(mux)

	body, err := json.Marshal(map[string]any{
		"model":      "claude-sonnet-4-5",
		"messages":   []map[string]string{{"role": "user", "content": "hello"}},
		"max_tokens": 64,
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/messages", "anthropic-token", body))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
	}

	matches, err := filepath.Glob(filepath.Join(tmpDir, "tmp", "trace", "*.inbound.json"))
	if err != nil {
		t.Fatalf("filepath.Glob() error = %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("debug snapshot count = %d, want 0 when trace disabled", len(matches))
	}
}
func TestChatCompletionsFallsBackToBackupProvider(t *testing.T) {
	routing := config.RoutingConfig{Rules: []config.RoutingRule{{Name: "office", FromTags: []string{"office"}, ToTags: []string{"primary-tag", "fallback-tag"}, Strategy: "failover"}}}
	outbounds := []config.OutboundSpec{{Name: "primary", Protocol: "mock", Tag: "primary-tag"}, {Name: "fallback", Protocol: "mock", Tag: "fallback-tag"}}
	h := newTestHandler(t, map[string]provider.Provider{
		"primary":  &failingProvider{name: "primary", err: provider.NewRetryableError(errors.New("temporary upstream failure"))},
		"fallback": provider.NewMock("fallback"),
	}, routing, testInbounds(), outbounds)
	mux := http.NewServeMux()
	h.Register(mux)

	body, err := json.Marshal(map[string]any{"model": "gpt-4", "messages": []map[string]string{{"role": "user", "content": "hello"}}})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/chat/completions", "client-token", body))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
}

func TestChatCompletionsReturnsBadGatewayForEmptyUpstreamResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "chatcmpl-empty",
			"object": "chat.completion",
			"model":  "gpt-4o-mini",
			"choices": []map[string]any{{
				"message": map[string]string{"role": "assistant"},
			}},
		})
	}))
	defer upstream.Close()

	h := newTestHandler(t, map[string]provider.Provider{
		"openai": provider.NewOpenAICompatible("openai", upstream.URL, []string{"test-key"}, upstream.Client()),
	}, config.RoutingConfig{Rules: []config.RoutingRule{{Name: "office", FromTags: []string{"office"}, ToTags: []string{"openai-tag"}, Strategy: "failover"}}}, testInbounds(), []config.OutboundSpec{{Name: "openai", Protocol: "openai_chat", Endpoint: upstream.URL, AuthToken: "test-key", Tag: "openai-tag"}})
	mux := http.NewServeMux()
	h.Register(mux)

	body, err := json.Marshal(map[string]any{"model": "gpt-4o-mini", "messages": []map[string]string{{"role": "user", "content": "hello"}}})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/chat/completions", "client-token", body))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502, body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "upstream returned no content and no tool calls") {
		t.Fatalf("body = %q, want explicit upstream empty response error", w.Body.String())
	}
}

func TestChatCompletionsStreamingReturnsBadGatewayForEmptyUpstreamResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "chatcmpl-empty",
			"object": "chat.completion",
			"model":  "gpt-4o-mini",
			"choices": []map[string]any{{
				"message": map[string]string{"role": "assistant"},
			}},
		})
	}))
	defer upstream.Close()

	h := newTestHandler(t, map[string]provider.Provider{
		"openai": provider.NewOpenAICompatible("openai", upstream.URL, []string{"test-key"}, upstream.Client()),
	}, config.RoutingConfig{Rules: []config.RoutingRule{{Name: "office", FromTags: []string{"office"}, ToTags: []string{"openai-tag"}, Strategy: "failover"}}}, testInbounds(), []config.OutboundSpec{{Name: "openai", Protocol: "openai_chat", Endpoint: upstream.URL, AuthToken: "test-key", Tag: "openai-tag"}})
	mux := http.NewServeMux()
	h.Register(mux)

	body, err := json.Marshal(map[string]any{"model": "gpt-4o-mini", "stream": true, "messages": []map[string]string{{"role": "user", "content": "hello"}}})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/chat/completions", "client-token", body))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502, body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "upstream returned no content and no tool calls") {
		t.Fatalf("body = %q, want explicit upstream empty response error", w.Body.String())
	}
}

func TestChatCompletionsUsesOpenAICompatibleProvider(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %q, want /chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization = %q, want Bearer test-key", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "chatcmpl-upstream", "object": "chat.completion", "model": "gpt-4o-mini", "choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": "hello from upstream gateway"}}}})
	}))
	defer upstream.Close()

	h := newTestHandler(t, map[string]provider.Provider{"openai": provider.NewOpenAICompatible("openai", upstream.URL, []string{"test-key"}, upstream.Client())}, config.RoutingConfig{Rules: []config.RoutingRule{{Name: "office", FromTags: []string{"office"}, ToTags: []string{"openai-tag"}, Strategy: "failover"}}}, testInbounds(), []config.OutboundSpec{{Name: "openai", Protocol: "openai_chat", Endpoint: upstream.URL, AuthToken: "test-key", Tag: "openai-tag"}})
	mux := http.NewServeMux()
	h.Register(mux)

	body, err := json.Marshal(map[string]any{"model": "gpt-4o-mini", "messages": []map[string]string{{"role": "user", "content": "hello"}}})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/chat/completions", "client-token", body))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
}
