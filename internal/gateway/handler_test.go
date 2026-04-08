package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"syrogo/internal/config"
	"syrogo/internal/execution"
	"syrogo/internal/provider"
	"syrogo/internal/router"
	"syrogo/internal/runtime"
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

func TestAnthropicMessagesAcceptsStructuredContent(t *testing.T) {
	h := newTestHandler(t, map[string]provider.Provider{"mock": provider.NewMock("mock")}, testRoutingConfig(), testDualProtocolInbounds(), testOutbounds())
	mux := http.NewServeMux()
	h.Register(mux)

	body, err := json.Marshal(map[string]any{
		"model": "claude-sonnet-4-5",
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

func TestAnthropicMessagesStreamsSSE(t *testing.T) {
	h := newTestHandler(t, map[string]provider.Provider{"mock": provider.NewMock("mock")}, testRoutingConfig(), testDualProtocolInbounds(), testOutbounds())
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
	if !strings.Contains(w.Body.String(), `"type":"message_start"`) {
		t.Fatalf("body = %q, want anthropic message_start payload", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "event: done\ndata: {}\n\n") {
		t.Fatalf("body = %q, want anthropic done event", w.Body.String())
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
