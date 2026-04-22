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

	"github.com/ryanycheng/Syrogo/internal/config"
	"github.com/ryanycheng/Syrogo/internal/execution"
	"github.com/ryanycheng/Syrogo/internal/provider"
	"github.com/ryanycheng/Syrogo/internal/router"
	"github.com/ryanycheng/Syrogo/internal/runtime"
	"github.com/ryanycheng/Syrogo/internal/semantic"
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

func TestBuildRuntimeRequestNormalizesAnthropicUserToolResultToToolMessage(t *testing.T) {
	req := inboundRequest{
		Model: "claude-sonnet-4-5",
		Messages: []inboundMessage{{
			Role: "assistant",
			Content: json.RawMessage(`[
				{"type":"tool_use","id":"call_123","name":"Read","input":{"file_path":"/tmp/x"}}
			]`),
		}, {
			Role: "user",
			Content: json.RawMessage(`[
				{"type":"tool_result","tool_use_id":"call_123","content":"MIT License","is_error":false}
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
	if got.Messages[1].Role != runtime.MessageRoleTool {
		t.Fatalf("Messages[1].Role = %q, want tool", got.Messages[1].Role)
	}
	if got.Messages[1].ToolCallID != "call_123" {
		t.Fatalf("Messages[1].ToolCallID = %q, want call_123", got.Messages[1].ToolCallID)
	}
	if len(got.Messages[1].Parts) != 1 || got.Messages[1].Parts[0].Text != "MIT License" {
		t.Fatalf("Messages[1].Parts = %#v, want MIT License", got.Messages[1].Parts)
	}
}

func TestBuildRuntimeRequestSplitsAnthropicUserToolResultsIntoSeparateToolMessages(t *testing.T) {
	req := inboundRequest{
		Model: "claude-sonnet-4-5",
		Messages: []inboundMessage{{
			Role: "user",
			Content: json.RawMessage(`[
				{"type":"tool_result","tool_use_id":"call_todo","content":"todo error","is_error":true},
				{"type":"tool_result","tool_use_id":"call_read","content":"read error","is_error":true}
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
	if got.Messages[0].Role != runtime.MessageRoleTool || got.Messages[1].Role != runtime.MessageRoleTool {
		t.Fatalf("Messages roles = %#v, want two tool messages", []runtime.MessageRole{got.Messages[0].Role, got.Messages[1].Role})
	}
	if got.Messages[0].ToolCallID != "call_todo" || got.Messages[1].ToolCallID != "call_read" {
		t.Fatalf("Messages ToolCallID = %#v, want split tool ids", []string{got.Messages[0].ToolCallID, got.Messages[1].ToolCallID})
	}
	if !got.Messages[0].ToolResultIsError || !got.Messages[1].ToolResultIsError {
		t.Fatalf("Messages ToolResultIsError = %#v, want true for both", []bool{got.Messages[0].ToolResultIsError, got.Messages[1].ToolResultIsError})
	}
	if got.Messages[0].Parts[0].Text != "todo error" || got.Messages[1].Parts[0].Text != "read error" {
		t.Fatalf("Messages Parts = %#v, want preserved tool results", []string{got.Messages[0].Parts[0].Text, got.Messages[1].Parts[0].Text})
	}
}

func TestBuildRuntimeRequestPreservesAnthropicJSONToolResultPayload(t *testing.T) {
	req := inboundRequest{
		Model: "claude-sonnet-4-5",
		Messages: []inboundMessage{{
			Role: "user",
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
	if got.Messages[0].Role != runtime.MessageRoleTool {
		t.Fatalf("Messages[0].Role = %q, want tool", got.Messages[0].Role)
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

func TestLowerSemanticTurnPreservesToolResultErrorFlag(t *testing.T) {
	message := lowerSemanticTurn(semantic.Turn{
		Role: semantic.RoleTool,
		Segments: []semantic.Segment{{
			Kind: semantic.SegmentToolResult,
			ToolResult: &semantic.ToolResult{
				ToolCallID: "toolu_error",
				IsError:    true,
				Content: []semantic.Segment{{
					Kind: semantic.SegmentText,
					Text: "lookup failed",
				}},
			},
		}},
	})

	if !message.ToolResultIsError {
		t.Fatalf("message.ToolResultIsError = %v, want true", message.ToolResultIsError)
	}
	if message.ToolCallID != "toolu_error" {
		t.Fatalf("message.ToolCallID = %q, want toolu_error", message.ToolCallID)
	}
}

func TestWriteAnthropicMessageResponsePreservesMixedContent(t *testing.T) {
	w := httptest.NewRecorder()
	writeAnthropicMessageResponse(w, runtime.Response{
		ID:           "msg_123",
		Object:       "message",
		Model:        "claude-sonnet-4-5",
		FinishReason: runtime.FinishReasonToolUse,
		Message: runtime.Message{
			Role: runtime.MessageRoleAssistant,
			Parts: []runtime.ContentPart{{
				Type: runtime.ContentPartTypeText,
				Text: "lookup failed",
			}, {
				Type: runtime.ContentPartTypeJSON,
				Data: json.RawMessage(`{"city":"shanghai","forecast":"sunny"}`),
			}},
			ToolCalls: []runtime.ToolCall{{ID: "tool_123", Name: "get_weather", Arguments: `{"city":"shanghai"}`}},
		},
	})

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body struct {
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type  string         `json:"type"`
			Text  string         `json:"text"`
			ID    string         `json:"id"`
			Name  string         `json:"name"`
			Value map[string]any `json:"value"`
		} `json:"content"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if body.StopReason != "tool_use" {
		t.Fatalf("body.StopReason = %q, want tool_use", body.StopReason)
	}
	if len(body.Content) != 3 {
		t.Fatalf("len(body.Content) = %d, want 3", len(body.Content))
	}
	if body.Content[0].Type != "text" || body.Content[0].Text != "lookup failed" {
		t.Fatalf("body.Content[0] = %#v, want text block", body.Content[0])
	}
	if body.Content[1].Type != "json" || body.Content[1].Value["city"] != "shanghai" || body.Content[1].Value["forecast"] != "sunny" {
		t.Fatalf("body.Content[1] = %#v, want json block", body.Content[1])
	}
	if body.Content[2].Type != "tool_use" || body.Content[2].ID != "tool_123" || body.Content[2].Name != "get_weather" {
		t.Fatalf("body.Content[2] = %#v, want tool_use block", body.Content[2])
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

func TestBuildRuntimeRequestStripsLeadingSystemReminderText(t *testing.T) {
	req := inboundRequest{
		Model:  "claude-sonnet-4-5",
		System: json.RawMessage(`"base system"`),
		Messages: []inboundMessage{{
			Role: "user",
			Content: json.RawMessage(`[
				{"type":"text","text":"<system-reminder>\nremember repo rules\n</system-reminder>\n"},
				{"type":"text","text":"hi"}
			]`),
		}},
	}

	got, err := buildRuntimeRequest(req)
	if err != nil {
		t.Fatalf("buildRuntimeRequest() error = %v", err)
	}
	if got.System != "base system" {
		t.Fatalf("got.System = %q, want original system only", got.System)
	}
	if len(got.Messages) != 1 {
		t.Fatalf("len(got.Messages) = %d, want 1", len(got.Messages))
	}
	if got.Messages[0].Role != runtime.MessageRoleUser {
		t.Fatalf("got.Messages[0].Role = %q, want user", got.Messages[0].Role)
	}
	if len(got.Messages[0].Parts) != 1 || got.Messages[0].Parts[0].Text != "hi" {
		t.Fatalf("got.Messages[0].Parts = %#v, want remaining user hi", got.Messages[0].Parts)
	}
}

func TestBuildRuntimeRequestPreservesAnthropicSystemAndMaxTokens(t *testing.T) {
	req := inboundRequest{
		Model:             "claude-sonnet-4-5",
		System:            json.RawMessage(`"You are a careful assistant."`),
		MaxTokens:         256,
		Metadata:          json.RawMessage(`{"user_id":"u_123"}`),
		Thinking:          json.RawMessage(`{"type":"adaptive"}`),
		ContextManagement: json.RawMessage(`{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]}`),
		OutputConfig:      json.RawMessage(`{"effort":"high"}`),
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
	if got.ThinkingType != "adaptive" {
		t.Fatalf("got.ThinkingType = %q, want adaptive", got.ThinkingType)
	}
	if got.OutputEffort != "high" {
		t.Fatalf("got.OutputEffort = %q, want high", got.OutputEffort)
	}
	if string(got.Metadata) != `{"user_id":"u_123"}` {
		t.Fatalf("got.Metadata = %s, want preserved metadata", string(got.Metadata))
	}
	if string(got.ContextManagement) != `{"edits":[{"keep":"all","type":"clear_thinking_20251015"}]}` {
		t.Fatalf("got.ContextManagement = %s, want preserved context_management", string(got.ContextManagement))
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
		Model:              "gpt-4o-mini",
		Input:              json.RawMessage(`"hello"`),
		Stream:             true,
		PreviousResponseID: "resp_prev_123",
	}

	got, err := buildRuntimeRequestFromResponses(req)
	if err != nil {
		t.Fatalf("buildRuntimeRequestFromResponses() error = %v", err)
	}
	if got.Model != "gpt-4o-mini" || !got.Stream || got.PreviousResponseID != "resp_prev_123" {
		t.Fatalf("got = %#v, want model, stream, and previous_response_id preserved", got)
	}
	if len(got.Messages) != 1 || got.Messages[0].Role != runtime.MessageRoleUser || got.Messages[0].Parts[0].Text != "hello" {
		t.Fatalf("Messages = %#v, want single user hello message", got.Messages)
	}
}

func TestBuildRuntimeRequestFromResponsesAllowsBuiltinResponsesToolsWithoutName(t *testing.T) {
	req := openAIResponsesRequest{
		Model: "gpt-4o-mini",
		Input: json.RawMessage(`"hello"`),
		Tools: []openAIResponsesTool{{
			Type: "web_search",
			Raw:  json.RawMessage(`{"type":"web_search","user_location":{"type":"approximate","country":"US"}}`),
		}},
	}

	got, err := buildRuntimeRequestFromResponses(req)
	if err != nil {
		t.Fatalf("buildRuntimeRequestFromResponses() error = %v", err)
	}
	if len(got.Tools) != 1 {
		t.Fatalf("len(got.Tools) = %d, want 1", len(got.Tools))
	}
	if got.Tools[0].Name != "web_search" {
		t.Fatalf("got.Tools[0].Name = %q, want web_search", got.Tools[0].Name)
	}
}

func TestBuildRuntimeRequestFromResponsesPreservesBuiltinResponsesTools(t *testing.T) {
	req := openAIResponsesRequest{
		Model: "gpt-4o-mini",
		Input: json.RawMessage(`"hello"`),
		Tools: []openAIResponsesTool{{
			Type:        "web_search",
			Name:        "web_search",
			Description: "Search the web",
			Raw:         json.RawMessage(`{"type":"web_search","name":"web_search","description":"Search the web","user_location":{"type":"approximate","country":"US"}}`),
		}},
	}

	got, err := buildRuntimeRequestFromResponses(req)
	if err != nil {
		t.Fatalf("buildRuntimeRequestFromResponses() error = %v", err)
	}
	if len(got.Tools) != 1 {
		t.Fatalf("len(got.Tools) = %d, want 1", len(got.Tools))
	}
	if got.Tools[0].Type != "web_search" {
		t.Fatalf("got.Tools[0].Type = %q, want web_search", got.Tools[0].Type)
	}
	if len(got.Tools[0].Raw) == 0 {
		t.Fatal("got.Tools[0].Raw = empty, want preserved raw tool spec")
	}
	var rawTool map[string]any
	if err := json.Unmarshal(got.Tools[0].Raw, &rawTool); err != nil {
		t.Fatalf("json.Unmarshal(got.Tools[0].Raw) error = %v", err)
	}
	userLocation, ok := rawTool["user_location"].(map[string]any)
	if !ok || userLocation["type"] != "approximate" || userLocation["country"] != "US" {
		t.Fatalf("rawTool[user_location] = %#v, want approximate/US", rawTool["user_location"])
	}
}

func TestBuildRuntimeRequestFromResponsesPreservesTools(t *testing.T) {
	req := openAIResponsesRequest{
		Model: "gpt-4o-mini",
		Input: json.RawMessage(`"hello"`),
		Tools: []openAIResponsesTool{
			{
				Type:        "function",
				Name:        "get_weather",
				Description: "Query weather by city",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`),
			},
		},
	}

	got, err := buildRuntimeRequestFromResponses(req)
	if err != nil {
		t.Fatalf("buildRuntimeRequestFromResponses() error = %v", err)
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
}

func TestBuildRuntimeRequestFromResponsesFunctionOutputPreservesMixedContentOrder(t *testing.T) {
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
	if len(got.Messages[0].Parts) != 2 {
		t.Fatalf("len(got.Messages[0].Parts) = %d, want 2", len(got.Messages[0].Parts))
	}
	if got.Messages[0].Parts[0].Type != runtime.ContentPartTypeText || got.Messages[0].Parts[0].Text != "partial text" {
		t.Fatalf("got.Messages[0].Parts[0] = %#v, want first text part", got.Messages[0].Parts[0])
	}
	if got.Messages[0].Parts[1].Type != runtime.ContentPartTypeJSON {
		t.Fatalf("got.Messages[0].Parts[1] = %#v, want json part", got.Messages[0].Parts[1])
	}
	if string(got.Messages[0].Parts[1].Data) != `{"city":"shanghai"}` {
		t.Fatalf("got.Messages[0].Parts[1].Data = %s, want raw json payload", got.Messages[0].Parts[1].Data)
	}
}

func TestBuildRuntimeRequestFromResponsesFunctionOutputPreservesErrorStatus(t *testing.T) {
	req := openAIResponsesRequest{
		Model: "gpt-4o-mini",
		Input: json.RawMessage(`[
			{"type":"function_call_output","call_id":"call_123","status":"error","output":[{"type":"output_text","text":"lookup failed"}]}
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
	if !got.Messages[0].ToolResultIsError {
		t.Fatalf("got.Messages[0].ToolResultIsError = %v, want true", got.Messages[0].ToolResultIsError)
	}
	if len(got.Messages[0].Parts) != 1 || got.Messages[0].Parts[0].Text != "lookup failed" {
		t.Fatalf("got.Messages[0].Parts = %#v, want single lookup failed text part", got.Messages[0].Parts)
	}
}

func TestBuildRuntimeRequestAllowsEmptyAnthropicToolResultContent(t *testing.T) {
	req := inboundRequest{
		Model: "claude-sonnet-4-5",
		Messages: []inboundMessage{{
			Role: "tool",
			Content: json.RawMessage(`[
				{"type":"tool_result","tool_use_id":"toolu_empty","content":[]}
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
	if got.Messages[0].Role != runtime.MessageRoleTool {
		t.Fatalf("got.Messages[0].Role = %q, want tool", got.Messages[0].Role)
	}
	if got.Messages[0].ToolCallID != "toolu_empty" {
		t.Fatalf("got.Messages[0].ToolCallID = %q, want toolu_empty", got.Messages[0].ToolCallID)
	}
	if len(got.Messages[0].Parts) != 1 || got.Messages[0].Parts[0].Type != runtime.ContentPartTypeText || got.Messages[0].Parts[0].Text != "" {
		t.Fatalf("got.Messages[0].Parts = %#v, want synthesized empty tool result part", got.Messages[0].Parts)
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

func TestBuildRuntimeRequestFromOpenAIChatParsesOfficialToolsFormat(t *testing.T) {
	req := inboundRequest{
		Model: "gpt-4o-mini",
		Messages: []inboundMessage{{
			Role:    "user",
			Content: json.RawMessage(`"hello"`),
		}},
	}
	tools := []openAIChatToolDefinition{{Type: "function"}}
	tools[0].Function.Name = "get_weather"
	tools[0].Function.Description = "Query weather by city"
	tools[0].Function.Parameters = json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`)

	got, err := buildRuntimeRequestFromOpenAIChat(req, tools)
	if err != nil {
		t.Fatalf("buildRuntimeRequestFromOpenAIChat() error = %v", err)
	}
	if len(got.Tools) != 1 {
		t.Fatalf("len(got.Tools) = %d, want 1", len(got.Tools))
	}
	if got.Tools[0].Name != "get_weather" || got.Tools[0].Description != "Query weather by city" {
		t.Fatalf("got.Tools[0] = %#v, want parsed OpenAI tool metadata", got.Tools[0])
	}
	var schema map[string]any
	if err := json.Unmarshal(got.Tools[0].InputSchema, &schema); err != nil {
		t.Fatalf("json.Unmarshal(got.Tools[0].InputSchema) error = %v", err)
	}
	if schema["type"] != "object" {
		t.Fatalf("schema[type] = %#v, want object", schema["type"])
	}
	props, _ := schema["properties"].(map[string]any)
	if _, ok := props["city"]; !ok {
		t.Fatalf("schema[properties] = %#v, want city property", schema["properties"])
	}
}

func TestBuildRuntimeRequestFromOpenAIChatPreservesToolChoiceAndNullContent(t *testing.T) {
	req := inboundRequest{
		Model:      "gpt-4o-mini",
		ToolChoice: json.RawMessage(`{"type":"function","function":{"name":"get_weather"}}`),
		Messages: []inboundMessage{{
			Role:    "assistant",
			Content: json.RawMessage(`null`),
			ToolCalls: []inboundToolCall{{
				ID:   "call_123",
				Type: "function",
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{Name: "get_weather", Arguments: `{"city":"shanghai"}`},
			}},
		}},
	}

	got, err := buildRuntimeRequestFromOpenAIChat(req, nil)
	if err != nil {
		t.Fatalf("buildRuntimeRequestFromOpenAIChat() error = %v", err)
	}
	if string(got.ToolChoice) != `{"type":"function","function":{"name":"get_weather"}}` {
		t.Fatalf("got.ToolChoice = %s, want preserved function tool_choice", string(got.ToolChoice))
	}
	if len(got.Messages) != 1 {
		t.Fatalf("len(got.Messages) = %d, want 1", len(got.Messages))
	}
	if got.Messages[0].Role != runtime.MessageRoleAssistant {
		t.Fatalf("got.Messages[0].Role = %q, want assistant", got.Messages[0].Role)
	}
	if len(got.Messages[0].ToolCalls) != 1 || got.Messages[0].ToolCalls[0].Name != "get_weather" {
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

func TestResponsesStreamsSSEPreservesJSONContentPart(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("path = %q, want /responses", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&map[string]any{}); err != nil {
			t.Fatalf("json.NewDecoder() error = %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "resp_json_stream_123",
			"object": "response",
			"model":  "gpt-4o-mini",
			"status": "completed",
			"output": []map[string]any{{
				"type": "message",
				"role": "assistant",
				"content": []map[string]any{{
					"type": "output_text",
					"text": "before json",
				}, {
					"type":  "json",
					"value": map[string]any{"city": "shanghai", "forecast": "sunny"},
				}},
			}},
			"usage": map[string]any{"input_tokens": 9, "output_tokens": 4, "total_tokens": 13},
		})
	}))
	defer upstream.Close()

	h := newTestHandler(t, map[string]provider.Provider{
		"responses": provider.NewOpenAIResponsesCompatible("responses", upstream.URL, []string{"test-key"}, config.OutboundCapabilities{}, upstream.Client()),
	}, config.RoutingConfig{Rules: []config.RoutingRule{{Name: "office", FromTags: []string{"office"}, ToTags: []string{"responses-tag"}, Strategy: "failover"}}}, testDualProtocolInbounds(), []config.OutboundSpec{{Name: "responses", Protocol: "openai_responses", Endpoint: upstream.URL, AuthToken: "test-key", Tag: "responses-tag"}})
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
	got := w.Body.String()
	textIndex := strings.Index(got, `"text":"before json"`)
	jsonIndex := strings.Index(got, `"value":{"city":"shanghai","forecast":"sunny"}`)
	if !strings.Contains(got, `event: response.content_part.added`) || !strings.Contains(got, `"type":"json"`) || jsonIndex == -1 {
		t.Fatalf("body = %q, want json content part frames", got)
	}
	if textIndex == -1 || textIndex > jsonIndex {
		t.Fatalf("body = %q, want text block before json block", got)
	}
	if !strings.Contains(got, `event: response.completed`) {
		t.Fatalf("body = %q, want completed frame", got)
	}
}

func TestResponsesForwardsToolsToOpenAIResponsesUpstream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("path = %q, want /responses", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization = %q, want Bearer test-key", got)
		}
		var upstreamBody struct {
			Tools []struct {
				Type        string          `json:"type"`
				Name        string          `json:"name"`
				Description string          `json:"description"`
				Parameters  json.RawMessage `json:"parameters"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&upstreamBody); err != nil {
			t.Fatalf("json.NewDecoder() error = %v", err)
		}
		if len(upstreamBody.Tools) != 1 {
			t.Fatalf("len(upstreamBody.Tools) = %d, want 1", len(upstreamBody.Tools))
		}
		if upstreamBody.Tools[0].Name != "get_weather" || upstreamBody.Tools[0].Description != "Query weather by city" {
			t.Fatalf("upstreamBody.Tools[0] = %#v, want forwarded tool metadata", upstreamBody.Tools[0])
		}
		var schema map[string]any
		if err := json.Unmarshal(upstreamBody.Tools[0].Parameters, &schema); err != nil {
			t.Fatalf("json.Unmarshal(upstreamBody.Tools[0].Parameters) error = %v", err)
		}
		if schema["type"] != "object" {
			t.Fatalf("schema[type] = %#v, want object", schema["type"])
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
		"responses": provider.NewOpenAIResponsesCompatible("responses", upstream.URL, []string{"test-key"}, config.OutboundCapabilities{}, upstream.Client()),
	}, config.RoutingConfig{Rules: []config.RoutingRule{{Name: "office", FromTags: []string{"office"}, ToTags: []string{"responses-tag"}, Strategy: "failover"}}}, testDualProtocolInbounds(), []config.OutboundSpec{{Name: "responses", Protocol: "openai_responses", Endpoint: upstream.URL, AuthToken: "test-key", Tag: "responses-tag"}})
	mux := http.NewServeMux()
	h.Register(mux)

	body, err := json.Marshal(map[string]any{"model": "gpt-4o-mini", "input": "hello", "tools": []map[string]any{{"type": "function", "name": "get_weather", "description": "Query weather by city", "parameters": map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}}, "required": []string{"city"}}}}})
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

func TestResponsesWritesMixedAssistantContentWithoutDroppingJSON(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("path = %q, want /responses", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&map[string]any{}); err != nil {
			t.Fatalf("json.NewDecoder() error = %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "resp_json_123",
			"object": "response",
			"model":  "gpt-4o-mini",
			"output": []map[string]any{{
				"type": "message",
				"role": "assistant",
				"content": []map[string]any{{
					"type": "output_text",
					"text": "before json",
				}, {
					"type":  "json",
					"value": map[string]any{"city": "shanghai", "forecast": "sunny"},
				}},
			}},
		})
	}))
	defer upstream.Close()

	h := newTestHandler(t, map[string]provider.Provider{
		"responses": provider.NewOpenAIResponsesCompatible("responses", upstream.URL, []string{"test-key"}, config.OutboundCapabilities{}, upstream.Client()),
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
	if !strings.Contains(w.Body.String(), `"type":"output_text"`) || !strings.Contains(w.Body.String(), `"text":"before json"`) {
		t.Fatalf("body = %q, want text content block", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"type":"json"`) || !strings.Contains(w.Body.String(), `"value":{"city":"shanghai","forecast":"sunny"}`) {
		t.Fatalf("body = %q, want json content block", w.Body.String())
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

func TestChatCompletionsAcceptsOfficialToolsFormat(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Tools []struct {
				Type     string `json:"type"`
				Function struct {
					Name        string          `json:"name"`
					Description string          `json:"description"`
					Parameters  json.RawMessage `json:"parameters"`
				} `json:"function"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("json.NewDecoder() error = %v", err)
		}
		if len(req.Tools) != 1 || req.Tools[0].Type != "function" || req.Tools[0].Function.Name != "get_weather" {
			t.Fatalf("req.Tools = %#v, want official OpenAI chat tools forwarded", req.Tools)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "chatcmpl_123",
			"object": "chat.completion",
			"model":  "gpt-4o-mini",
			"choices": []map[string]any{{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": "hello from upstream",
				},
				"finish_reason": "stop",
			}},
		})
	}))
	defer upstream.Close()

	h := newTestHandler(t, map[string]provider.Provider{
		"openai": provider.NewOpenAICompatible("openai", upstream.URL, []string{"test-key"}, upstream.Client()),
	}, testRoutingConfig(), testInbounds(), []config.OutboundSpec{{
		Name:      "openai",
		Protocol:  "openai_chat",
		Endpoint:  upstream.URL,
		AuthToken: "test-key",
		Tag:       "mock-tag",
	}})
	mux := http.NewServeMux()
	h.Register(mux)

	body, err := json.Marshal(map[string]any{
		"model": "gpt-4o-mini",
		"messages": []map[string]any{{
			"role":    "user",
			"content": "hello",
		}},
		"tools": []map[string]any{{
			"type": "function",
			"function": map[string]any{
				"name":        "get_weather",
				"description": "Query weather by city",
				"parameters": map[string]any{
					"type":       "object",
					"properties": map[string]any{"city": map[string]any{"type": "string"}},
					"required":   []string{"city"},
				},
			},
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
	if !strings.Contains(w.Body.String(), "hello from upstream") {
		t.Fatalf("body = %q, want upstream response", w.Body.String())
	}
}

func TestChatCompletionsStreamingCompatibilityChunks(t *testing.T) {
	tests := []struct {
		name   string
		event  runtime.StreamEvent
		check  func(t *testing.T, body string)
		shared map[int]string
	}{
		{
			name: "usage field names",
			event: runtime.StreamEvent{
				Type:       runtime.StreamEventUsage,
				ResponseID: "chatcmpl_123",
				Model:      "gpt-4o-mini",
				Usage:      &runtime.Usage{InputTokens: 11, OutputTokens: 7, TotalTokens: 18},
			},
			check: func(t *testing.T, body string) {
				t.Helper()
				if !strings.Contains(body, `"prompt_tokens":11`) || !strings.Contains(body, `"completion_tokens":7`) || !strings.Contains(body, `"total_tokens":18`) {
					t.Fatalf("chunk = %s, want OpenAI usage field names", body)
				}
				if strings.Contains(body, `"InputTokens"`) || strings.Contains(body, `"OutputTokens"`) || strings.Contains(body, `"TotalTokens"`) {
					t.Fatalf("chunk = %s, want no runtime usage field names", body)
				}
			},
			shared: map[int]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunk := openAIStreamChunkWithArgumentsDelta(tt.event, tt.shared)
			body, err := json.Marshal(chunk)
			if err != nil {
				t.Fatalf("json.Marshal(chunk) error = %v", err)
			}
			tt.check(t, string(body))
		})
	}

	t.Run("tool call arguments delta", func(t *testing.T) {
		snapshots := map[int]string{}
		first := openAIStreamChunkWithArgumentsDelta(runtime.StreamEvent{
			Type:          runtime.StreamEventContentDelta,
			ResponseID:    "chatcmpl_123",
			Model:         "gpt-4o-mini",
			ToolCallIndex: 0,
			ToolCall: &runtime.ToolCall{
				ID:        "call_123",
				Name:      "get_weather",
				Arguments: `{"city":"sha`,
			},
		}, snapshots)
		second := openAIStreamChunkWithArgumentsDelta(runtime.StreamEvent{
			Type:          runtime.StreamEventContentDelta,
			ResponseID:    "chatcmpl_123",
			Model:         "gpt-4o-mini",
			ToolCallIndex: 0,
			ToolCall: &runtime.ToolCall{
				ID:        "call_123",
				Name:      "get_weather",
				Arguments: `{"city":"shanghai"}`,
			},
		}, snapshots)

		firstBody, err := json.Marshal(first)
		if err != nil {
			t.Fatalf("json.Marshal(first) error = %v", err)
		}
		secondBody, err := json.Marshal(second)
		if err != nil {
			t.Fatalf("json.Marshal(second) error = %v", err)
		}
		if !strings.Contains(string(firstBody), `"arguments":"{\"city\":\"sha"`) {
			t.Fatalf("first chunk = %s, want first arguments delta", firstBody)
		}
		if !strings.Contains(string(secondBody), `"arguments":"nghai\"}"`) {
			t.Fatalf("second chunk = %s, want incremental arguments delta", secondBody)
		}
		if strings.Contains(string(secondBody), `"arguments":"{\"city\":\"shanghai\"}"`) {
			t.Fatalf("second chunk = %s, want delta not full snapshot", secondBody)
		}
	})
}

func TestChatCompletionsWritesFinishReasonUsageAndNullContentForToolCalls(t *testing.T) {
	h := newTestHandler(t, map[string]provider.Provider{"mock": provider.NewMock("mock")}, testRoutingConfig(), testInbounds(), testOutbounds())
	w := httptest.NewRecorder()
	writeOpenAIChatResponse(w, runtime.Response{
		ID:           "chatcmpl_123",
		Object:       "chat.completion",
		Model:        "gpt-4o-mini",
		FinishReason: runtime.FinishReasonToolUse,
		Message: runtime.Message{
			Role: runtime.MessageRoleAssistant,
			ToolCalls: []runtime.ToolCall{{
				ID:        "call_123",
				Name:      "get_weather",
				Arguments: `{"city":"shanghai"}`,
			}},
		},
		Usage: &runtime.Usage{InputTokens: 11, OutputTokens: 7, TotalTokens: 18},
	})
	_ = h
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	got := w.Body.String()
	if !strings.Contains(got, `"finish_reason":"tool_calls"`) {
		t.Fatalf("body = %q, want tool_calls finish_reason", got)
	}
	if !strings.Contains(got, `"content":null`) {
		t.Fatalf("body = %q, want null content for tool call message", got)
	}
	if !strings.Contains(got, `"prompt_tokens":11`) || !strings.Contains(got, `"completion_tokens":7`) || !strings.Contains(got, `"total_tokens":18`) {
		t.Fatalf("body = %q, want OpenAI usage object", got)
	}
}

func TestAnthropicMessagesCompatibilityInputs(t *testing.T) {
	tests := []struct {
		name string
		body map[string]any
	}{
		{
			name: "accepts empty tool_result content",
			body: map[string]any{
				"model": "claude-sonnet-4-5",
				"messages": []map[string]any{{
					"role": "tool",
					"content": []map[string]any{{
						"type":        "tool_result",
						"tool_use_id": "toolu_empty",
						"content":     []any{},
					}},
				}},
			},
		},
	}

	h := newTestHandler(t, map[string]provider.Provider{"mock": provider.NewMock("mock")}, testRoutingConfig(), testDualProtocolInbounds(), testOutbounds())
	mux := http.NewServeMux()
	h.Register(mux)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := json.Marshal(tt.body)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/messages", "anthropic-token", body))
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
			}
		})
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

func TestAnthropicMessagesUsesOpenAIResponsesProvider(t *testing.T) {
	var upstreamBody struct {
		Model        string `json:"model"`
		Instructions string `json:"instructions"`
		Input        []struct {
			Type      string `json:"type"`
			Role      string `json:"role"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
			Content   []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"input"`
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("path = %q, want /responses", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization = %q, want Bearer test-key", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&upstreamBody); err != nil {
			t.Fatalf("json.NewDecoder().Decode() error = %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "resp_123",
			"object": "response",
			"model":  "gpt-5.4",
			"status": "completed",
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
		"responses": provider.NewOpenAIResponsesCompatible("responses", upstream.URL, []string{"test-key"}, config.OutboundCapabilities{}, upstream.Client()),
	}, config.RoutingConfig{Rules: []config.RoutingRule{{
		Name:        "anthropic-to-responses",
		FromTags:    []string{"office"},
		ToTags:      []string{"responses-tag"},
		Strategy:    "failover",
		TargetModel: "gpt-5.4",
	}}}, testDualProtocolInbounds(), []config.OutboundSpec{{
		Name:      "responses",
		Protocol:  "openai_responses",
		Endpoint:  upstream.URL,
		AuthToken: "test-key",
		Tag:       "responses-tag",
	}})
	mux := http.NewServeMux()
	h.Register(mux)

	body, err := json.Marshal(map[string]any{
		"model": "claude-sonnet-4-5",
		"system": []map[string]any{{
			"type": "text",
			"text": "You are Claude Code, Anthropic's official CLI for Claude.",
		}},
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
	mux.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/messages", "anthropic-token", body))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	if upstreamBody.Model != "gpt-5.4" {
		t.Fatalf("upstream model = %q, want gpt-5.4", upstreamBody.Model)
	}
	if upstreamBody.Instructions != "You are Claude Code, Anthropic's official CLI for Claude." {
		t.Fatalf("upstream instructions = %q, want bridged system text", upstreamBody.Instructions)
	}
	if len(upstreamBody.Input) != 1 {
		t.Fatalf("len(upstream input) = %d, want 1", len(upstreamBody.Input))
	}
	if upstreamBody.Input[0].Type != "message" || upstreamBody.Input[0].Role != "user" {
		t.Fatalf("upstream input[0] = %#v, want user message input item", upstreamBody.Input[0])
	}
	if len(upstreamBody.Input[0].Content) != 1 || upstreamBody.Input[0].Content[0].Type != "input_text" || upstreamBody.Input[0].Content[0].Text != "hello" {
		t.Fatalf("upstream input[0].Content = %#v, want input_text hello", upstreamBody.Input[0].Content)
	}
	if !strings.Contains(w.Body.String(), `"type":"message"`) || !strings.Contains(w.Body.String(), `"text":"hello from responses upstream"`) {
		t.Fatalf("body = %q, want anthropic message response bridged from responses upstream", w.Body.String())
	}
}

func TestAnthropicMessagesToolLoopUsesOpenAIResponsesProvider(t *testing.T) {
	var upstreamRequests []struct {
		Model string `json:"model"`
		Input []struct {
			Type      string `json:"type"`
			Role      string `json:"role"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
			Output    string `json:"output"`
			Content   []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"input"`
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("path = %q, want /responses", r.URL.Path)
		}
		var body struct {
			Model string `json:"model"`
			Input []struct {
				Type      string `json:"type"`
				Role      string `json:"role"`
				CallID    string `json:"call_id"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
				Output    string `json:"output"`
				Content   []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("json.NewDecoder().Decode() error = %v", err)
		}
		upstreamRequests = append(upstreamRequests, body)
		if len(upstreamRequests) == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":     "resp_tool_1",
				"object": "response",
				"model":  body.Model,
				"output": []map[string]any{{
					"type":      "function_call",
					"call_id":   "call_123",
					"name":      "get_weather",
					"arguments": `{"city":"shanghai"}`,
				}},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "resp_tool_2",
			"object": "response",
			"model":  body.Model,
			"status": "completed",
			"output": []map[string]any{{
				"type": "message",
				"role": "assistant",
				"content": []map[string]any{{
					"type": "output_text",
					"text": "shanghai is sunny",
				}},
			}},
		})
	}))
	defer upstream.Close()

	h := newTestHandler(t, map[string]provider.Provider{
		"responses": provider.NewOpenAIResponsesCompatible("responses", upstream.URL, []string{"test-key"}, config.OutboundCapabilities{}, upstream.Client()),
	}, config.RoutingConfig{Rules: []config.RoutingRule{{
		Name:        "anthropic-to-responses",
		FromTags:    []string{"office"},
		ToTags:      []string{"responses-tag"},
		Strategy:    "failover",
		TargetModel: "gpt-5.4",
	}}}, testDualProtocolInbounds(), []config.OutboundSpec{{
		Name:      "responses",
		Protocol:  "openai_responses",
		Endpoint:  upstream.URL,
		AuthToken: "test-key",
		Tag:       "responses-tag",
	}})
	mux := http.NewServeMux()
	h.Register(mux)

	firstBody, err := json.Marshal(map[string]any{
		"model": "claude-sonnet-4-5",
		"tools": []map[string]any{{
			"name":        "get_weather",
			"description": "Get weather by city",
			"input_schema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"city": map[string]any{"type": "string"}},
				"required":   []string{"city"},
			},
		}},
		"messages": []map[string]any{{
			"role": "user",
			"content": []map[string]any{{
				"type": "text",
				"text": "check shanghai weather",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	firstResp := httptest.NewRecorder()
	mux.ServeHTTP(firstResp, authorizedRequest(http.MethodPost, "/v1/messages", "anthropic-token", firstBody))
	if firstResp.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200, body = %s", firstResp.Code, firstResp.Body.String())
	}
	if !strings.Contains(firstResp.Body.String(), `"type":"tool_use"`) || !strings.Contains(firstResp.Body.String(), `"id":"call_123"`) {
		t.Fatalf("first body = %q, want anthropic tool_use response", firstResp.Body.String())
	}

	secondBody, err := json.Marshal(map[string]any{
		"model": "claude-sonnet-4-5",
		"messages": []map[string]any{{
			"role": "assistant",
			"content": []map[string]any{{
				"type":  "tool_use",
				"id":    "call_123",
				"name":  "get_weather",
				"input": map[string]any{"city": "shanghai"},
			}},
		}, {
			"role": "tool",
			"content": []map[string]any{{
				"type":        "tool_result",
				"tool_use_id": "call_123",
				"content": []map[string]any{{
					"type": "text",
					"text": "sunny",
				}},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	secondResp := httptest.NewRecorder()
	mux.ServeHTTP(secondResp, authorizedRequest(http.MethodPost, "/v1/messages", "anthropic-token", secondBody))
	if secondResp.Code != http.StatusOK {
		t.Fatalf("second status = %d, want 200, body = %s", secondResp.Code, secondResp.Body.String())
	}
	if !strings.Contains(secondResp.Body.String(), `"type":"text"`) || !strings.Contains(secondResp.Body.String(), `"text":"shanghai is sunny"`) {
		t.Fatalf("second body = %q, want final anthropic assistant text", secondResp.Body.String())
	}

	if len(upstreamRequests) != 2 {
		t.Fatalf("len(upstreamRequests) = %d, want 2", len(upstreamRequests))
	}
	if upstreamRequests[0].Model != "gpt-5.4" {
		t.Fatalf("upstreamRequests[0].Model = %q, want gpt-5.4", upstreamRequests[0].Model)
	}
	if len(upstreamRequests[0].Input) != 1 || upstreamRequests[0].Input[0].Type != "message" || upstreamRequests[0].Input[0].Role != "user" {
		t.Fatalf("upstreamRequests[0].Input = %#v, want initial user message item", upstreamRequests[0].Input)
	}
	if len(upstreamRequests[1].Input) != 2 {
		t.Fatalf("len(upstreamRequests[1].Input) = %d, want 2", len(upstreamRequests[1].Input))
	}
	if upstreamRequests[1].Input[0].Type != "function_call" || upstreamRequests[1].Input[0].CallID != "call_123" || upstreamRequests[1].Input[0].Name != "get_weather" {
		t.Fatalf("upstreamRequests[1].Input[0] = %#v, want bridged function_call history", upstreamRequests[1].Input[0])
	}
	if upstreamRequests[1].Input[0].Arguments != `{"city":"shanghai"}` {
		t.Fatalf("upstreamRequests[1].Input[0].Arguments = %q, want tool arguments JSON", upstreamRequests[1].Input[0].Arguments)
	}
	if upstreamRequests[1].Input[1].Type != "function_call_output" || upstreamRequests[1].Input[1].CallID != "call_123" || upstreamRequests[1].Input[1].Output != "sunny" {
		t.Fatalf("upstreamRequests[1].Input[1] = %#v, want bridged function_call_output", upstreamRequests[1].Input[1])
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

func TestAnthropicMessagesStreamingUsesInputJSONDeltaForToolUse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "chatcmpl-tool",
			"object": "chat.completion",
			"model":  "gpt-4o-mini",
			"choices": []map[string]any{{
				"index": 0,
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
				"finish_reason": "tool_calls",
			}},
			"usage": map[string]any{"prompt_tokens": 11, "completion_tokens": 7, "total_tokens": 18},
		})
	}))
	defer upstream.Close()

	h := newTestHandler(t, map[string]provider.Provider{
		"openai": provider.NewOpenAICompatible("openai", upstream.URL, []string{"test-key"}, upstream.Client()),
	}, config.RoutingConfig{Rules: []config.RoutingRule{{Name: "office", FromTags: []string{"office"}, ToTags: []string{"openai-tag"}, Strategy: "failover"}}}, testDualProtocolInbounds(), []config.OutboundSpec{{Name: "openai", Protocol: "openai_chat", Endpoint: upstream.URL, AuthToken: "test-key", Tag: "openai-tag"}})
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
	if !strings.Contains(got, `event: content_block_start
`) || !strings.Contains(got, `"id":"call_123"`) || !strings.Contains(got, `"name":"get_weather"`) || !strings.Contains(got, `"input":{}`) {
		t.Fatalf("body = %q, want empty tool_use input at content_block_start", got)
	}
	if !strings.Contains(got, `"type":"input_json_delta"`) || (!strings.Contains(got, `"partial_json":"{\"city\":\"shanghai\"}"`) && (!strings.Contains(got, `"partial_json":"{\"city\":\"sh"`) || !strings.Contains(got, `"partial_json":"anghai\"}"`))) {
		t.Fatalf("body = %q, want input_json_delta partial_json tool stream", got)
	}
}

func TestAnthropicMessagesStreamingPreservesJSONContentBlock(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          "msg_json",
			"type":        "message",
			"role":        "assistant",
			"model":       "claude-sonnet-4-5",
			"stop_reason": "end_turn",
			"content": []map[string]any{{
				"type": "text",
				"text": "before json",
			}, {
				"type":  "json",
				"value": map[string]any{"city": "shanghai", "forecast": "sunny"},
			}},
			"usage": map[string]any{"input_tokens": 11, "output_tokens": 7},
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
	textIndex := strings.Index(got, `"text":"before json"`)
	jsonIndex := strings.Index(got, `"value":{"city":"shanghai","forecast":"sunny"}`)
	if !strings.Contains(got, `"type":"json"`) || jsonIndex == -1 {
		t.Fatalf("body = %q, want anthropic json content block", got)
	}
	if textIndex == -1 || textIndex > jsonIndex {
		t.Fatalf("body = %q, want text block before json block", got)
	}
	if strings.Contains(got, `"text":"{\"city\":\"shanghai\",\"forecast\":\"sunny\"}"`) {
		t.Fatalf("body = %q, want no json block downgraded to text", got)
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

func TestAnthropicMessagesWritesStreamTrace(t *testing.T) {
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
	if err := os.Setenv("SYROGO_TRACE", "anthropic_stream"); err != nil {
		t.Fatalf("os.Setenv() error = %v", err)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "chatcmpl-tool",
			"object": "chat.completion",
			"model":  "gpt-4o-mini",
			"choices": []map[string]any{{
				"index": 0,
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
				"finish_reason": "tool_calls",
			}},
			"usage": map[string]any{"prompt_tokens": 11, "completion_tokens": 7, "total_tokens": 18},
		})
	}))
	defer upstream.Close()

	h := newTestHandler(t, map[string]provider.Provider{
		"openai": provider.NewOpenAICompatible("openai", upstream.URL, []string{"test-key"}, upstream.Client()),
	}, config.RoutingConfig{Rules: []config.RoutingRule{{Name: "office", FromTags: []string{"office"}, ToTags: []string{"openai-tag"}, Strategy: "failover"}}}, testDualProtocolInbounds(), []config.OutboundSpec{{Name: "openai", Protocol: "openai_chat", Endpoint: upstream.URL, AuthToken: "test-key", Tag: "openai-tag"}})
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

	matches, err := filepath.Glob(filepath.Join(tmpDir, "tmp", "trace", "*.gateway-anthropic.stream.txt"))
	if err != nil {
		t.Fatalf("filepath.Glob() error = %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("stream trace count = %d, want 1", len(matches))
	}

	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "event: message_start\n") {
		t.Fatalf("trace = %q, want message_start", got)
	}
	if !strings.Contains(got, `"type":"input_json_delta"`) {
		t.Fatalf("trace = %q, want input_json_delta", got)
	}
	if !strings.Contains(got, `"stop_reason":"tool_use"`) {
		t.Fatalf("trace = %q, want tool_use stop reason", got)
	}
	if !strings.Contains(got, "event: done\n") {
		t.Fatalf("trace = %q, want done event", got)
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
