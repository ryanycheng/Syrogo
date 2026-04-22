package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ryanycheng/Syrogo/internal/config"
)

func baseConfig() config.Config {
	return config.Config{
		Listeners: []config.ListenerSpec{{Name: "public", Listen: ":8080", Inbounds: []string{"openai-entry"}}},
		Inbounds: []config.InboundSpec{{
			Name:     "openai-entry",
			Protocol: "openai_chat",
			Path:     "/v1/chat/completions",
			Clients:  []config.ClientSpec{{Token: "client-token", Tag: "office"}},
		}},
		Routing: config.RoutingConfig{Rules: []config.RoutingRule{{
			Name:     "office-route",
			FromTags: []string{"office"},
			ToTags:   []string{"mock-tag"},
			Strategy: "failover",
		}}},
		Outbounds: []config.OutboundSpec{{Name: "mock", Protocol: "mock", Tag: "mock-tag"}},
	}
}

func baseDualProtocolConfig() config.Config {
	cfg := baseConfig()
	cfg.Inbounds = append(cfg.Inbounds, config.InboundSpec{
		Name:     "anthropic-entry",
		Protocol: "anthropic_messages",
		Path:     "/v1/messages",
		Clients:  []config.ClientSpec{{Token: "anthropic-token", Tag: "office"}},
	})
	cfg.Listeners[0].Inbounds = append(cfg.Listeners[0].Inbounds, "anthropic-entry")
	return cfg
}

func authorizedRequest(method, path, token string, body []byte) *http.Request {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

func TestNewSucceedsWithMockProvider(t *testing.T) {
	app, err := New(baseConfig())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if app == nil || app.Server == nil {
		t.Fatal("New() returned nil app or server")
	}
}

func TestNewSucceedsWithOpenAICompatibleProvider(t *testing.T) {
	cfg := baseConfig()
	cfg.Outbounds = []config.OutboundSpec{{
		Name:      "openai",
		Protocol:  "openai_chat",
		Endpoint:  "https://example.com/v1",
		AuthToken: "key-1",
		Tag:       "openai-tag",
	}}
	cfg.Routing.Rules[0].ToTags = []string{"openai-tag"}

	app, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if app == nil || app.Server == nil {
		t.Fatal("New() returned nil app or server")
	}
}

func TestNewSucceedsWithAnthropicMessagesProvider(t *testing.T) {
	cfg := baseConfig()
	cfg.Outbounds = []config.OutboundSpec{{
		Name:      "anthropic",
		Protocol:  "anthropic_messages",
		Endpoint:  "https://example.com/v1",
		AuthToken: "key-1",
		Tag:       "anthropic-tag",
	}}
	cfg.Routing.Rules[0].ToTags = []string{"anthropic-tag"}

	app, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if app == nil || app.Server == nil {
		t.Fatal("New() returned nil app or server")
	}
}

func TestNewBindsEachListenerToItsInbounds(t *testing.T) {
	cfg := baseConfig()
	cfg.Listeners = []config.ListenerSpec{
		{Name: "public", Listen: ":8080", Inbounds: []string{"openai-entry"}},
		{Name: "office", Listen: ":8081", Inbounds: []string{"openai-entry"}},
	}

	app, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	listeners := app.Server.Listeners()
	if got := len(listeners); got != 2 {
		t.Fatalf("len(app.Server.Listeners()) = %d, want 2", got)
	}
}

func TestNewRoutesByClientTag(t *testing.T) {
	app, err := New(baseConfig())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	body, err := json.Marshal(map[string]any{
		"model": "gpt-4",
		"messages": []map[string]string{{
			"role":    "user",
			"content": "hello",
		}},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	listeners := app.Server.Listeners()
	w := httptest.NewRecorder()
	listeners[0].Handler.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/chat/completions", "client-token", body))
	if w.Code != http.StatusOK {
		t.Fatalf("route status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
}

func TestNewRoutesWithRuleTargetModel(t *testing.T) {
	cfg := baseConfig()
	cfg.Routing.Rules[0].TargetModel = "gpt-4o-mini"
	app, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	body, err := json.Marshal(map[string]any{
		"model": "claude-sonnet-4-5",
		"messages": []map[string]string{{
			"role":    "user",
			"content": "hello",
		}},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	listeners := app.Server.Listeners()
	w := httptest.NewRecorder()
	listeners[0].Handler.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/chat/completions", "client-token", body))
	if w.Code != http.StatusOK {
		t.Fatalf("rule target model status = %d, want 200, body = %s", w.Code, w.Body.String())
	}

	var resp struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if resp.Model != "gpt-4o-mini" {
		t.Fatalf("resp.Model = %q, want gpt-4o-mini", resp.Model)
	}
}

func TestNewStreamsSSEFromListener(t *testing.T) {
	app, err := New(baseConfig())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	body, err := json.Marshal(map[string]any{
		"model":  "gpt-4",
		"stream": true,
		"messages": []map[string]string{{
			"role":    "user",
			"content": "hello",
		}},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	listeners := app.Server.Listeners()
	w := httptest.NewRecorder()
	listeners[0].Handler.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/chat/completions", "client-token", body))
	if w.Code != http.StatusOK {
		t.Fatalf("stream status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}
	if !strings.Contains(w.Body.String(), "data: {\"") || strings.Contains(w.Body.String(), "event: message_start\n") || !strings.Contains(w.Body.String(), "data: [DONE]\n\n") {
		t.Fatalf("body = %q, want OpenAI SSE data frames + DONE", w.Body.String())
	}
}

func TestNewStreamsOpenAIUsageShapeFromListener(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %q, want /v1/chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Accept"); got != "text/event-stream" {
			t.Fatalf("Accept = %q, want text/event-stream", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, strings.Join([]string{
			`data: {"id":"chatcmpl-stream-usage","object":"chat.completion.chunk","model":"gpt-4o-mini","choices":[{"delta":{"role":"assistant"}}]}`,
			`data: {"id":"chatcmpl-stream-usage","object":"chat.completion.chunk","model":"gpt-4o-mini","choices":[{"delta":{"content":"pong"}}]}`,
			`data: {"id":"chatcmpl-stream-usage","object":"chat.completion.chunk","model":"gpt-4o-mini","choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"input_tokens":11,"output_tokens":7,"total_tokens":18}}`,
			`data: [DONE]`,
		}, "\n\n"))
	}))
	defer upstream.Close()

	cfg := baseConfig()
	cfg.Outbounds = []config.OutboundSpec{{
		Name:      "openai-stream",
		Protocol:  "openai_chat",
		Endpoint:  upstream.URL + "/v1",
		AuthToken: "stream-key",
		Tag:       "openai-tag",
	}}
	cfg.Routing.Rules[0].ToTags = []string{"openai-tag"}
	cfg.Routing.Rules[0].TargetModel = "gpt-4o-mini"

	app, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	body, err := json.Marshal(map[string]any{
		"model":  "gpt-4",
		"stream": true,
		"messages": []map[string]string{{
			"role":    "user",
			"content": "hello",
		}},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	listeners := app.Server.Listeners()
	w := httptest.NewRecorder()
	listeners[0].Handler.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/chat/completions", "client-token", body))
	if w.Code != http.StatusOK {
		t.Fatalf("stream status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}

	got := w.Body.String()
	if !strings.Contains(got, `"prompt_tokens":11`) || !strings.Contains(got, `"completion_tokens":7`) || !strings.Contains(got, `"total_tokens":18`) {
		t.Fatalf("body = %q, want OpenAI usage field names", got)
	}
	if strings.Contains(got, `"input_tokens":`) || strings.Contains(got, `"output_tokens":`) {
		t.Fatalf("body = %q, want no provider usage field names", got)
	}
	if !strings.Contains(got, `"finish_reason":"stop"`) || !strings.Contains(got, "data: [DONE]\n\n") {
		t.Fatalf("body = %q, want stop finish_reason and DONE frame", got)
	}
}

func TestNewStreamsResponsesJSONContentPartFromResponsesOutbound(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Fatalf("path = %q, want /v1/responses", r.URL.Path)
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

	cfg := baseDualProtocolConfig()
	cfg.Inbounds = append(cfg.Inbounds, config.InboundSpec{
		Name:     "responses-entry",
		Protocol: "openai_responses",
		Path:     "/v1/responses",
		Clients:  []config.ClientSpec{{Token: "responses-token", Tag: "responses-office"}},
	})
	cfg.Listeners[0].Inbounds = append(cfg.Listeners[0].Inbounds, "responses-entry")
	cfg.Routing.Rules = []config.RoutingRule{{
		Name:        "responses-stream-route",
		FromTags:    []string{"responses-office"},
		ToTags:      []string{"responses-primary"},
		Strategy:    "failover",
		TargetModel: "gpt-4o-mini",
	}}
	cfg.Outbounds = []config.OutboundSpec{{
		Name:      "cliproxy-responses",
		Protocol:  "openai_responses",
		Endpoint:  upstream.URL + "/v1",
		AuthToken: "bridge-key",
		Tag:       "responses-primary",
	}}

	app, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	body, err := json.Marshal(map[string]any{
		"model":  "gpt-4o-mini",
		"stream": true,
		"input":  "hello",
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	listeners := app.Server.Listeners()
	w := httptest.NewRecorder()
	listeners[0].Handler.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/responses", "responses-token", body))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	got := w.Body.String()
	if !strings.Contains(got, `event: response.content_part.added`) || !strings.Contains(got, `"type":"json"`) || !strings.Contains(got, `"value":{"city":"shanghai","forecast":"sunny"}`) {
		t.Fatalf("body = %q, want json content part frames", got)
	}
	if !strings.Contains(got, `event: response.completed`) {
		t.Fatalf("body = %q, want completed frame", got)
	}
}

func TestNewBridgesAnthropicInboundToOpenAIResponsesOutbound(t *testing.T) {
	app, err := New(baseDualProtocolConfig())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	body, err := json.Marshal(map[string]any{
		"model": "claude-sonnet-4-5",
		"messages": []map[string]string{{
			"role":    "user",
			"content": "hello",
		}},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	listeners := app.Server.Listeners()
	w := httptest.NewRecorder()
	listeners[0].Handler.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/messages", "anthropic-token", body))
	if w.Code != http.StatusOK {
		t.Fatalf("route status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"type":"message"`) {
		t.Fatalf("body = %q, want anthropic message response", w.Body.String())
	}
}

func TestNewBridgesAnthropicInboundToOpenAIChatOutbound(t *testing.T) {
	var upstreamBody struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		MaxTokens int `json:"max_tokens"`
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %q, want /v1/chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer bridge-key" {
			t.Fatalf("Authorization = %q, want Bearer bridge-key", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&upstreamBody); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "chatcmpl-1",
			"object": "chat.completion",
			"model":  upstreamBody.Model,
			"choices": []map[string]any{{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": "pong",
				},
			}},
		})
	}))
	defer upstream.Close()

	cfg := baseDualProtocolConfig()
	cfg.Inbounds[1].Clients[0].Tag = "anthropic-to-chat"
	cfg.Routing.Rules = []config.RoutingRule{{
		Name:        "anthropic-to-chat-route",
		FromTags:    []string{"anthropic-to-chat"},
		ToTags:      []string{"openai-primary"},
		Strategy:    "failover",
		TargetModel: "gpt-5.4",
	}}
	cfg.Outbounds = []config.OutboundSpec{{
		Name:      "cliproxy-chat",
		Protocol:  "openai_chat",
		Endpoint:  upstream.URL + "/v1",
		AuthToken: "bridge-key",
		Tag:       "openai-primary",
	}}

	app, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	body, err := json.Marshal(map[string]any{
		"model":      "claude-sonnet-4-6",
		"max_tokens": 256,
		"system": []map[string]any{{
			"type": "text",
			"text": "You are Claude Code, Anthropic's official CLI for Claude.",
		}},
		"messages": []map[string]any{{
			"role": "user",
			"content": []map[string]any{{
				"type": "text",
				"text": "只回复 pong",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	listeners := app.Server.Listeners()
	w := httptest.NewRecorder()
	listeners[0].Handler.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/messages", "anthropic-token", body))
	if w.Code != http.StatusOK {
		t.Fatalf("route status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	if upstreamBody.Model != "gpt-5.4" {
		t.Fatalf("upstream model = %q, want gpt-5.4", upstreamBody.Model)
	}
	if upstreamBody.MaxTokens != 256 {
		t.Fatalf("upstream max_tokens = %d, want 256", upstreamBody.MaxTokens)
	}
	if len(upstreamBody.Messages) != 2 {
		t.Fatalf("len(upstream messages) = %d, want 2", len(upstreamBody.Messages))
	}
	if upstreamBody.Messages[0].Role != "system" || upstreamBody.Messages[0].Content != "You are Claude Code, Anthropic's official CLI for Claude." {
		t.Fatalf("upstream system = %#v, want bridged system message", upstreamBody.Messages[0])
	}
	if upstreamBody.Messages[1].Role != "user" || upstreamBody.Messages[1].Content != "只回复 pong" {
		t.Fatalf("upstream user = %#v, want bridged user message", upstreamBody.Messages[1])
	}
	if !strings.Contains(w.Body.String(), `"type":"message"`) || !strings.Contains(w.Body.String(), `"text":"pong"`) {
		t.Fatalf("body = %q, want anthropic message response with pong", w.Body.String())
	}
}

func TestNewBridgesAnthropicToolsToOpenAIChatOutbound(t *testing.T) {
	var upstreamBody struct {
		Model    string `json:"model"`
		Messages []struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			ToolCalls []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
			ToolCallID string `json:"tool_call_id"`
		} `json:"messages"`
		Tools []struct {
			Type     string `json:"type"`
			Function struct {
				Name        string          `json:"name"`
				Description string          `json:"description"`
				Parameters  json.RawMessage `json:"parameters"`
				Strict      bool            `json:"strict"`
			} `json:"function"`
		} `json:"tools"`
		ToolChoice string `json:"tool_choice"`
		MaxTokens  int    `json:"max_tokens"`
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&upstreamBody); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "chatcmpl-tools-1",
			"object": "chat.completion",
			"model":  upstreamBody.Model,
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
			}},
		})
	}))
	defer upstream.Close()

	cfg := baseDualProtocolConfig()
	cfg.Inbounds[1].Clients[0].Tag = "anthropic-to-chat"
	cfg.Routing.Rules = []config.RoutingRule{{
		Name:        "anthropic-to-chat-route",
		FromTags:    []string{"anthropic-to-chat"},
		ToTags:      []string{"openai-primary"},
		Strategy:    "failover",
		TargetModel: "gpt-5.4",
	}}
	cfg.Outbounds = []config.OutboundSpec{{
		Name:      "cliproxy-chat",
		Protocol:  "openai_chat",
		Endpoint:  upstream.URL + "/v1",
		AuthToken: "bridge-key",
		Tag:       "openai-primary",
	}}

	app, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	body, err := json.Marshal(map[string]any{
		"model":      "claude-sonnet-4-6",
		"max_tokens": 256,
		"tools": []map[string]any{{
			"name":        "Bash",
			"description": "Execute shell commands",
			"input_schema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"command": map[string]any{"type": "string"}},
			},
		}, {
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
				"text": "查询上海天气，必要时调用工具。",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	listeners := app.Server.Listeners()
	w := httptest.NewRecorder()
	listeners[0].Handler.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/messages", "anthropic-token", body))
	if w.Code != http.StatusOK {
		t.Fatalf("route status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	if upstreamBody.Model != "gpt-5.4" || upstreamBody.MaxTokens != 256 {
		t.Fatalf("upstream header = %#v, want bridged model and max_tokens", upstreamBody)
	}
	if upstreamBody.ToolChoice != "auto" {
		t.Fatalf("upstream tool_choice = %q, want auto", upstreamBody.ToolChoice)
	}
	if len(upstreamBody.Tools) != 2 {
		t.Fatalf("len(upstream tools) = %d, want 2 including one builtin tool", len(upstreamBody.Tools))
	}
	toolNames := []string{upstreamBody.Tools[0].Function.Name, upstreamBody.Tools[1].Function.Name}
	hasBuiltin := toolNames[0] != "get_weather" || toolNames[1] != "get_weather"
	hasCustom := toolNames[0] == "get_weather" || toolNames[1] == "get_weather"
	if !hasBuiltin || !hasCustom {
		t.Fatalf("upstream tool names = %#v, want get_weather plus one builtin tool", toolNames)
	}
	if !strings.Contains(w.Body.String(), `"type":"tool_use"`) || !strings.Contains(w.Body.String(), `"id":"call_123"`) || !strings.Contains(w.Body.String(), `"name":"get_weather"`) {
		t.Fatalf("body = %q, want anthropic tool_use response", w.Body.String())
	}
}

func TestNewStreamsAnthropicToolsFromOpenAIChatOutbound(t *testing.T) {
	var upstreamBody struct {
		Model    string `json:"model"`
		Stream   bool   `json:"stream"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %q, want /v1/chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Accept"); got != "text/event-stream" {
			t.Fatalf("Accept = %q, want text/event-stream", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&upstreamBody); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, strings.Join([]string{
			`data: {"id":"chatcmpl-stream-1","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"delta":{"role":"assistant"},"finish_reason":""}]}`,
			`data: {"id":"chatcmpl-stream-1","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_123","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"sh"}}]},"finish_reason":""}]}`,
			`data: {"id":"chatcmpl-stream-1","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"anghai\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"input_tokens":11,"output_tokens":7,"total_tokens":18}}`,
			`data: [DONE]`,
		}, "\n\n"))
	}))
	defer upstream.Close()

	cfg := baseDualProtocolConfig()
	cfg.Inbounds[1].Clients[0].Tag = "anthropic-to-chat"
	cfg.Routing.Rules = []config.RoutingRule{{
		Name:        "anthropic-to-chat-route",
		FromTags:    []string{"anthropic-to-chat"},
		ToTags:      []string{"openai-primary"},
		Strategy:    "failover",
		TargetModel: "gpt-5.4",
	}}
	cfg.Outbounds = []config.OutboundSpec{{
		Name:      "cliproxy-chat",
		Protocol:  "openai_chat",
		Endpoint:  upstream.URL + "/v1",
		AuthToken: "bridge-key",
		Tag:       "openai-primary",
	}}

	app, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	body, err := json.Marshal(map[string]any{
		"model":  "claude-sonnet-4-6",
		"stream": true,
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
				"text": "查询上海天气，必要时调用工具。",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	listeners := app.Server.Listeners()
	w := httptest.NewRecorder()
	listeners[0].Handler.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/messages", "anthropic-token", body))
	if w.Code != http.StatusOK {
		t.Fatalf("stream status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	if !upstreamBody.Stream {
		t.Fatalf("upstream stream = %v, want true", upstreamBody.Stream)
	}
	got := w.Body.String()
	if !strings.Contains(got, `event: content_block_start
`) || !strings.Contains(got, `"type":"tool_use"`) {
		t.Fatalf("body = %q, want anthropic tool_use streaming frame", got)
	}
	if !strings.Contains(got, `"name":"get_weather"`) || !strings.Contains(got, `"input":{}`) {
		t.Fatalf("body = %q, want empty tool input at anthropic content_block_start", got)
	}
	if !strings.Contains(got, `"type":"input_json_delta"`) || (!strings.Contains(got, `"partial_json":"{\"city\":\"shanghai\"}"`) && (!strings.Contains(got, `"partial_json":"{\"city\":\"sh"`) || !strings.Contains(got, `"partial_json":"anghai\"}"`))) {
		t.Fatalf("body = %q, want anthropic input_json_delta tool stream", got)
	}
	if !strings.Contains(got, `"stop_reason":"tool_use"`) {
		t.Fatalf("body = %q, want tool_use stop_reason", got)
	}
	if !strings.Contains(got, `"input_tokens":11`) || !strings.Contains(got, `"output_tokens":7`) {
		t.Fatalf("body = %q, want snake_case usage in stream", got)
	}
}

func TestNewBridgesAnthropicToolResultToOpenAIChatHistory(t *testing.T) {
	requests := make([]struct {
		Messages []struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			ToolCalls []struct {
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
			ToolCallID string `json:"tool_call_id"`
			Status     string `json:"status"`
		} `json:"messages"`
	}, 0, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role      string `json:"role"`
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
				ToolCallID string `json:"tool_call_id"`
				Status     string `json:"status"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		requests = append(requests, body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "chatcmpl-tools-2",
			"object": "chat.completion",
			"model":  "gpt-5.4",
			"choices": []map[string]any{{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": "收到结果",
				},
			}},
		})
	}))
	defer upstream.Close()

	cfg := baseDualProtocolConfig()
	cfg.Inbounds[1].Clients[0].Tag = "anthropic-to-chat"
	cfg.Routing.Rules = []config.RoutingRule{{
		Name:        "anthropic-to-chat-route",
		FromTags:    []string{"anthropic-to-chat"},
		ToTags:      []string{"openai-primary"},
		Strategy:    "failover",
		TargetModel: "gpt-5.4",
	}}
	cfg.Outbounds = []config.OutboundSpec{{
		Name:      "cliproxy-chat",
		Protocol:  "openai_chat",
		Endpoint:  upstream.URL + "/v1",
		AuthToken: "bridge-key",
		Tag:       "openai-primary",
	}}

	app, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	listeners := app.Server.Listeners()

	firstBody, err := json.Marshal(map[string]any{
		"model": "claude-sonnet-4-6",
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

	w := httptest.NewRecorder()
	listeners[0].Handler.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/messages", "anthropic-token", firstBody))
	if w.Code != http.StatusOK {
		t.Fatalf("route status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	if len(requests) != 1 {
		t.Fatalf("len(requests) = %d, want 1", len(requests))
	}
	if len(requests[0].Messages) != 2 {
		t.Fatalf("len(requests[0].Messages) = %d, want 2", len(requests[0].Messages))
	}
	if len(requests[0].Messages[0].ToolCalls) != 1 || requests[0].Messages[0].ToolCalls[0].ID != "call_123" {
		t.Fatalf("requests[0].Messages[0] = %#v, want assistant tool_calls history", requests[0].Messages[0])
	}
	if requests[0].Messages[1].Role != "tool" || requests[0].Messages[1].ToolCallID != "call_123" || requests[0].Messages[1].Content != "sunny" {
		t.Fatalf("requests[0].Messages[1] = %#v, want tool result history", requests[0].Messages[1])
	}
}

func TestNewCompletesAnthropicToolLoopThroughOpenAIChatOutbound(t *testing.T) {
	requests := make([]struct {
		Messages []struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			ToolCalls []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
			ToolCallID string `json:"tool_call_id"`
		} `json:"messages"`
	}, 0, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role      string `json:"role"`
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
				ToolCallID string `json:"tool_call_id"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		requests = append(requests, body)
		if len(requests) == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":     "chatcmpl-tools-1",
				"object": "chat.completion",
				"model":  "gpt-5.4",
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
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "chatcmpl-tools-2",
			"object": "chat.completion",
			"model":  "gpt-5.4",
			"choices": []map[string]any{{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": "上海天气晴朗。",
				},
				"finish_reason": "stop",
			}},
		})
	}))
	defer upstream.Close()

	cfg := baseDualProtocolConfig()
	cfg.Inbounds[1].Clients[0].Tag = "anthropic-to-chat"
	cfg.Routing.Rules = []config.RoutingRule{{
		Name:        "anthropic-to-chat-route",
		FromTags:    []string{"anthropic-to-chat"},
		ToTags:      []string{"openai-primary"},
		Strategy:    "failover",
		TargetModel: "gpt-5.4",
	}}
	cfg.Outbounds = []config.OutboundSpec{{
		Name:      "cliproxy-chat",
		Protocol:  "openai_chat",
		Endpoint:  upstream.URL + "/v1",
		AuthToken: "bridge-key",
		Tag:       "openai-primary",
	}}

	app, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	listeners := app.Server.Listeners()

	firstBody, err := json.Marshal(map[string]any{
		"model": "claude-sonnet-4-6",
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
				"text": "查询上海天气，必要时调用工具。",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	firstResp := httptest.NewRecorder()
	listeners[0].Handler.ServeHTTP(firstResp, authorizedRequest(http.MethodPost, "/v1/messages", "anthropic-token", firstBody))
	if firstResp.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200, body = %s", firstResp.Code, firstResp.Body.String())
	}
	if !strings.Contains(firstResp.Body.String(), `"type":"tool_use"`) || !strings.Contains(firstResp.Body.String(), `"id":"call_123"`) {
		t.Fatalf("first body = %q, want anthropic tool_use response", firstResp.Body.String())
	}

	secondBody, err := json.Marshal(map[string]any{
		"model": "claude-sonnet-4-6",
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
					"text": "上海天气晴朗。",
				}},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	secondResp := httptest.NewRecorder()
	listeners[0].Handler.ServeHTTP(secondResp, authorizedRequest(http.MethodPost, "/v1/messages", "anthropic-token", secondBody))
	if secondResp.Code != http.StatusOK {
		t.Fatalf("second status = %d, want 200, body = %s", secondResp.Code, secondResp.Body.String())
	}
	if !strings.Contains(secondResp.Body.String(), `"type":"text"`) || !strings.Contains(secondResp.Body.String(), `"text":"上海天气晴朗。"`) {
		t.Fatalf("second body = %q, want final anthropic assistant text", secondResp.Body.String())
	}
	if len(requests) != 2 {
		t.Fatalf("len(requests) = %d, want 2 upstream calls", len(requests))
	}
	if len(requests[0].Messages) != 1 || requests[0].Messages[0].Role != "user" {
		t.Fatalf("requests[0].Messages = %#v, want initial user-only turn", requests[0].Messages)
	}
	if len(requests[1].Messages) != 2 {
		t.Fatalf("len(requests[1].Messages) = %d, want 2 for tool loop continuation", len(requests[1].Messages))
	}
	if len(requests[1].Messages[0].ToolCalls) != 1 || requests[1].Messages[0].ToolCalls[0].ID != "call_123" {
		t.Fatalf("requests[1].Messages[0] = %#v, want assistant tool call history", requests[1].Messages[0])
	}
	if requests[1].Messages[1].Role != "tool" || requests[1].Messages[1].ToolCallID != "call_123" || requests[1].Messages[1].Content != "上海天气晴朗。" {
		t.Fatalf("requests[1].Messages[1] = %#v, want bridged tool result history", requests[1].Messages[1])
	}
}

func TestNewStreamsAnthropicToolsFromAnthropicMessagesOutbound(t *testing.T) {
	var upstreamBody anthropicMessagesUpstreamRequest
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("path = %q, want /v1/messages", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "bridge-key" {
			t.Fatalf("x-api-key = %q, want bridge-key", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&upstreamBody); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          "msg_stream_1",
			"type":        "message",
			"role":        "assistant",
			"model":       upstreamBody.Model,
			"stop_reason": "tool_use",
			"content": []map[string]any{{
				"type":  "tool_use",
				"id":    "tool_123",
				"name":  "get_weather",
				"input": map[string]any{"city": "shanghai"},
			}},
			"usage": map[string]any{"input_tokens": 9, "output_tokens": 4},
		})
	}))
	defer upstream.Close()

	cfg := baseDualProtocolConfig()
	cfg.Inbounds[1].Clients[0].Tag = "anthropic-to-messages"
	cfg.Routing.Rules = []config.RoutingRule{{
		Name:        "anthropic-to-messages-route",
		FromTags:    []string{"anthropic-to-messages"},
		ToTags:      []string{"anthropic-primary"},
		Strategy:    "failover",
		TargetModel: "claude-sonnet-4-6",
	}}
	cfg.Outbounds = []config.OutboundSpec{{
		Name:      "cliproxy-messages",
		Protocol:  "anthropic_messages",
		Endpoint:  upstream.URL + "/v1",
		AuthToken: "bridge-key",
		Tag:       "anthropic-primary",
	}}

	app, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	body, err := json.Marshal(map[string]any{
		"model":  "claude-sonnet-4-5",
		"stream": true,
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
				"text": "查询上海天气，必要时调用工具。",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	listeners := app.Server.Listeners()
	w := httptest.NewRecorder()
	listeners[0].Handler.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/messages", "anthropic-token", body))
	if w.Code != http.StatusOK {
		t.Fatalf("stream status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	if upstreamBody.Stream {
		t.Fatalf("upstream stream = %v, want false for local replay streaming", upstreamBody.Stream)
	}
	got := w.Body.String()
	if !strings.Contains(got, "event: message_start") || !strings.Contains(got, `"type":"tool_use"`) {
		t.Fatalf("body = %q, want anthropic tool_use streaming frame", got)
	}
	if !strings.Contains(got, `"name":"get_weather"`) || !strings.Contains(got, `"input":{}`) {
		t.Fatalf("body = %q, want empty tool input at anthropic content_block_start", got)
	}
	if !strings.Contains(got, `"type":"input_json_delta"`) || !strings.Contains(got, `"partial_json":"{\"city\":\"shanghai\"}"`) {
		t.Fatalf("body = %q, want anthropic input_json_delta tool stream", got)
	}
	if !strings.Contains(got, `"stop_reason":"tool_use"`) {
		t.Fatalf("body = %q, want tool_use stop_reason", got)
	}
	if !strings.Contains(got, `"input_tokens":9`) || !strings.Contains(got, `"output_tokens":4`) {
		t.Fatalf("body = %q, want snake_case usage in stream", got)
	}
}

func TestNewStreamsAnthropicJSONBlockFromAnthropicMessagesOutbound(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          "msg_json_stream",
			"type":        "message",
			"role":        "assistant",
			"model":       "claude-sonnet-4-6",
			"stop_reason": "end_turn",
			"content": []map[string]any{{
				"type": "text",
				"text": "before json",
			}, {
				"type":  "json",
				"value": map[string]any{"city": "shanghai", "forecast": "sunny"},
			}},
			"usage": map[string]any{"input_tokens": 9, "output_tokens": 4},
		})
	}))
	defer upstream.Close()

	cfg := baseDualProtocolConfig()
	cfg.Inbounds[1].Clients[0].Tag = "anthropic-to-messages"
	cfg.Routing.Rules = []config.RoutingRule{{
		Name:        "anthropic-to-messages-route",
		FromTags:    []string{"anthropic-to-messages"},
		ToTags:      []string{"anthropic-primary"},
		Strategy:    "failover",
		TargetModel: "claude-sonnet-4-6",
	}}
	cfg.Outbounds = []config.OutboundSpec{{
		Name:      "cliproxy-messages",
		Protocol:  "anthropic_messages",
		Endpoint:  upstream.URL + "/v1",
		AuthToken: "bridge-key",
		Tag:       "anthropic-primary",
	}}

	app, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	body, err := json.Marshal(map[string]any{
		"model":  "claude-sonnet-4-5",
		"stream": true,
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

	listeners := app.Server.Listeners()
	w := httptest.NewRecorder()
	listeners[0].Handler.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/messages", "anthropic-token", body))
	if w.Code != http.StatusOK {
		t.Fatalf("stream status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	got := w.Body.String()
	if !strings.Contains(got, `"type":"json"`) || !strings.Contains(got, `"value":{"city":"shanghai","forecast":"sunny"}`) {
		t.Fatalf("body = %q, want anthropic json content block in listener stream", got)
	}
	if strings.Contains(got, `"text":"{\"city\":\"shanghai\",\"forecast\":\"sunny\"}"`) {
		t.Fatalf("body = %q, want no downgraded json text block", got)
	}
}

func TestNewCompletesAnthropicToolLoopThroughAnthropicMessagesOutbound(t *testing.T) {
	requests := make([]anthropicMessagesUpstreamRequest, 0, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body anthropicMessagesUpstreamRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		requests = append(requests, body)
		if len(requests) == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":          "msg_tools_1",
				"type":        "message",
				"role":        "assistant",
				"model":       body.Model,
				"stop_reason": "tool_use",
				"content": []map[string]any{{
					"type":  "tool_use",
					"id":    "tool_123",
					"name":  "get_weather",
					"input": map[string]any{"city": "shanghai"},
				}},
				"usage": map[string]any{"input_tokens": 11, "output_tokens": 6},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          "msg_tools_2",
			"type":        "message",
			"role":        "assistant",
			"model":       body.Model,
			"stop_reason": "end_turn",
			"content": []map[string]any{{
				"type": "text",
				"text": "上海天气晴朗。",
			}},
			"usage": map[string]any{"input_tokens": 18, "output_tokens": 7},
		})
	}))
	defer upstream.Close()

	cfg := baseDualProtocolConfig()
	cfg.Inbounds[1].Clients[0].Tag = "anthropic-to-messages"
	cfg.Routing.Rules = []config.RoutingRule{{
		Name:        "anthropic-to-messages-route",
		FromTags:    []string{"anthropic-to-messages"},
		ToTags:      []string{"anthropic-primary"},
		Strategy:    "failover",
		TargetModel: "claude-sonnet-4-6",
	}}
	cfg.Outbounds = []config.OutboundSpec{{
		Name:      "cliproxy-messages",
		Protocol:  "anthropic_messages",
		Endpoint:  upstream.URL + "/v1",
		AuthToken: "bridge-key",
		Tag:       "anthropic-primary",
	}}

	app, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	listeners := app.Server.Listeners()

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
				"text": "查询上海天气，必要时调用工具。",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	firstResp := httptest.NewRecorder()
	listeners[0].Handler.ServeHTTP(firstResp, authorizedRequest(http.MethodPost, "/v1/messages", "anthropic-token", firstBody))
	if firstResp.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200, body = %s", firstResp.Code, firstResp.Body.String())
	}
	if !strings.Contains(firstResp.Body.String(), `"type":"tool_use"`) || !strings.Contains(firstResp.Body.String(), `"id":"tool_123"`) {
		t.Fatalf("first body = %q, want anthropic tool_use response", firstResp.Body.String())
	}

	secondBody, err := json.Marshal(map[string]any{
		"model": "claude-sonnet-4-5",
		"messages": []map[string]any{{
			"role": "assistant",
			"content": []map[string]any{{
				"type":  "tool_use",
				"id":    "tool_123",
				"name":  "get_weather",
				"input": map[string]any{"city": "shanghai"},
			}},
		}, {
			"role": "tool",
			"content": []map[string]any{{
				"type":        "tool_result",
				"tool_use_id": "tool_123",
				"is_error":    true,
				"content": []map[string]any{{
					"type": "text",
					"text": "lookup failed once",
				}, {
					"type":  "json",
					"value": map[string]any{"city": "shanghai", "retryable": true},
				}},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	secondResp := httptest.NewRecorder()
	listeners[0].Handler.ServeHTTP(secondResp, authorizedRequest(http.MethodPost, "/v1/messages", "anthropic-token", secondBody))
	if secondResp.Code != http.StatusOK {
		t.Fatalf("second status = %d, want 200, body = %s", secondResp.Code, secondResp.Body.String())
	}
	if !strings.Contains(secondResp.Body.String(), `"type":"text"`) || !strings.Contains(secondResp.Body.String(), `"text":"上海天气晴朗。"`) {
		t.Fatalf("second body = %q, want final anthropic assistant text", secondResp.Body.String())
	}
	if len(requests) != 2 {
		t.Fatalf("len(requests) = %d, want 2 upstream calls", len(requests))
	}
	if len(requests[0].Messages) != 1 || requests[0].Messages[0].Role != "user" {
		t.Fatalf("requests[0].Messages = %#v, want initial user-only turn", requests[0].Messages)
	}
	if len(requests[0].Tools) != 1 || requests[0].Tools[0].Name != "get_weather" {
		t.Fatalf("requests[0].Tools = %#v, want single get_weather tool", requests[0].Tools)
	}
	if len(requests[1].Messages) != 2 {
		t.Fatalf("len(requests[1].Messages) = %d, want 2 for tool loop continuation", len(requests[1].Messages))
	}
	if requests[1].Messages[0].Role != "assistant" || len(requests[1].Messages[0].Content) != 1 || requests[1].Messages[0].Content[0].Type != "tool_use" || requests[1].Messages[0].Content[0].ID != "tool_123" {
		t.Fatalf("requests[1].Messages[0] = %#v, want assistant tool_use history", requests[1].Messages[0])
	}
	if requests[1].Messages[1].Role != "user" || len(requests[1].Messages[1].Content) != 1 {
		t.Fatalf("requests[1].Messages[1] = %#v, want user tool_result history", requests[1].Messages[1])
	}
	toolResult := requests[1].Messages[1].Content[0]
	if toolResult.Type != "tool_result" || toolResult.ToolUseID != "tool_123" || !toolResult.IsError {
		t.Fatalf("toolResult = %#v, want tool_result with is_error", toolResult)
	}
}

type anthropicMessagesUpstreamRequest struct {
	Model     string                     `json:"model"`
	System    string                     `json:"system"`
	MaxTokens int                        `json:"max_tokens"`
	Messages  []anthropicUpstreamMessage `json:"messages"`
	Tools     []anthropicUpstreamTool    `json:"tools"`
	Stream    bool                       `json:"stream"`
}

type anthropicUpstreamTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthropicUpstreamMessage struct {
	Role    string                   `json:"role"`
	Content []anthropicUpstreamBlock `json:"content"`
}

type anthropicUpstreamBlock struct {
	Type      string `json:"type"`
	Text      string `json:"text"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	ToolUseID string `json:"tool_use_id"`
	IsError   bool   `json:"is_error"`
	Content   any    `json:"content"`
}

func TestNewStreamsAnthropicSSEFromListener(t *testing.T) {
	app, err := New(baseDualProtocolConfig())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	body, err := json.Marshal(map[string]any{
		"model":  "claude-sonnet-4-5",
		"stream": true,
		"messages": []map[string]string{{
			"role":    "user",
			"content": "hello",
		}},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	listeners := app.Server.Listeners()
	w := httptest.NewRecorder()
	listeners[0].Handler.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/messages", "anthropic-token", body))
	if w.Code != http.StatusOK {
		t.Fatalf("stream status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}
	if !strings.Contains(w.Body.String(), `"type":"message_start"`) || !strings.Contains(w.Body.String(), "event: done\ndata: {}\n\n") {
		t.Fatalf("body = %q, want anthropic SSE lifecycle + done", w.Body.String())
	}
}

func TestNewBridgesAnthropicToolLoopToOpenAIResponsesOutbound(t *testing.T) {
	requests := make([]struct {
		Model           string          `json:"model"`
		Instructions    string          `json:"instructions"`
		MaxOutputTokens int             `json:"max_output_tokens"`
		Metadata        json.RawMessage `json:"metadata"`
		Reasoning       *struct {
			Effort string `json:"effort"`
		} `json:"reasoning"`
		ContextManagement json.RawMessage `json:"context_management"`
		Input             []struct {
			Type      string `json:"type"`
			Role      string `json:"role"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
			Output    string `json:"output"`
			Status    string `json:"status"`
			Content   []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"input"`
	}, 0, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Fatalf("path = %q, want /v1/responses", r.URL.Path)
		}
		var body struct {
			Model           string          `json:"model"`
			Instructions    string          `json:"instructions"`
			MaxOutputTokens int             `json:"max_output_tokens"`
			Metadata        json.RawMessage `json:"metadata"`
			Reasoning       *struct {
				Effort string `json:"effort"`
			} `json:"reasoning"`
			ContextManagement json.RawMessage `json:"context_management"`
			Input             []struct {
				Type      string `json:"type"`
				Role      string `json:"role"`
				CallID    string `json:"call_id"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
				Output    string `json:"output"`
				Status    string `json:"status"`
				Content   []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		requests = append(requests, body)
		if len(requests) == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":     "resp_tools_1",
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
			"id":     "resp_tools_2",
			"object": "response",
			"model":  body.Model,
			"status": "completed",
			"output": []map[string]any{{
				"type": "message",
				"role": "assistant",
				"content": []map[string]any{{
					"type": "output_text",
					"text": "上海天气晴朗。",
				}},
			}},
		})
	}))
	defer upstream.Close()

	cfg := baseDualProtocolConfig()
	cfg.Inbounds[1].Clients[0].Tag = "anthropic-to-responses"
	cfg.Routing.Rules = []config.RoutingRule{{
		Name:        "anthropic-to-responses-route",
		FromTags:    []string{"anthropic-to-responses"},
		ToTags:      []string{"responses-primary"},
		Strategy:    "failover",
		TargetModel: "gpt-5.4",
	}}
	cfg.Outbounds = []config.OutboundSpec{{
		Name:      "cliproxy-responses",
		Protocol:  "openai_responses",
		Endpoint:  upstream.URL + "/v1",
		AuthToken: "bridge-key",
		Tag:       "responses-primary",
	}}

	app, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	listeners := app.Server.Listeners()

	firstBody, err := json.Marshal(map[string]any{
		"model": "claude-sonnet-4-6",
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
				"text": "查询上海天气，必要时调用工具。",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	firstResp := httptest.NewRecorder()
	listeners[0].Handler.ServeHTTP(firstResp, authorizedRequest(http.MethodPost, "/v1/messages", "anthropic-token", firstBody))
	if firstResp.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200, body = %s", firstResp.Code, firstResp.Body.String())
	}
	if !strings.Contains(firstResp.Body.String(), `"type":"tool_use"`) || !strings.Contains(firstResp.Body.String(), `"id":"call_123"`) {
		t.Fatalf("first body = %q, want anthropic tool_use response", firstResp.Body.String())
	}

	secondBody, err := json.Marshal(map[string]any{
		"model": "claude-sonnet-4-6",
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
				"is_error":    true,
				"content": []map[string]any{{
					"type": "text",
					"text": "lookup failed once",
				}, {
					"type":  "json",
					"value": map[string]any{"city": "shanghai", "retryable": true},
				}, {
					"type": "text",
					"text": "retry suggested",
				}},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	secondResp := httptest.NewRecorder()
	listeners[0].Handler.ServeHTTP(secondResp, authorizedRequest(http.MethodPost, "/v1/messages", "anthropic-token", secondBody))
	if secondResp.Code != http.StatusOK {
		t.Fatalf("second status = %d, want 200, body = %s", secondResp.Code, secondResp.Body.String())
	}
	if !strings.Contains(secondResp.Body.String(), `"type":"text"`) || !strings.Contains(secondResp.Body.String(), `"text":"上海天气晴朗。"`) {
		t.Fatalf("second body = %q, want final anthropic assistant text", secondResp.Body.String())
	}

	if len(requests) != 2 {
		t.Fatalf("len(requests) = %d, want 2 upstream calls", len(requests))
	}
	if len(requests[0].Input) != 1 || requests[0].Input[0].Type != "message" || requests[0].Input[0].Role != "user" {
		t.Fatalf("requests[0].Input = %#v, want initial user-only turn", requests[0].Input)
	}
	if len(requests[1].Input) != 2 {
		t.Fatalf("len(requests[1].Input) = %d, want 2 for tool loop continuation", len(requests[1].Input))
	}
	if requests[1].Input[0].Type != "function_call" || requests[1].Input[0].CallID != "call_123" {
		t.Fatalf("requests[1].Input[0] = %#v, want function_call history", requests[1].Input[0])
	}
	if requests[1].Input[1].Type != "function_call_output" || requests[1].Input[1].CallID != "call_123" {
		t.Fatalf("requests[1].Input[1] = %#v, want function_call_output history", requests[1].Input[1])
	}
	wantOutput := "lookup failed once\n{\"city\":\"shanghai\",\"retryable\":true}\nretry suggested"
	if requests[1].Input[1].Output != wantOutput {
		t.Fatalf("requests[1].Input[1].Output = %q, want %q", requests[1].Input[1].Output, wantOutput)
	}
	if requests[1].Input[1].Status != "error" {
		t.Fatalf("requests[1].Input[1].Status = %q, want error", requests[1].Input[1].Status)
	}
}

func TestNewBridgesAnthropicToolsToOpenAIResponsesOutbound(t *testing.T) {

	var upstreamBody struct {
		Model              string          `json:"model"`
		Instructions       string          `json:"instructions"`
		MaxOutputTokens    int             `json:"max_output_tokens"`
		PreviousResponseID string          `json:"previous_response_id"`
		Metadata           json.RawMessage `json:"metadata"`
		Reasoning          *struct {
			Effort string `json:"effort"`
		} `json:"reasoning"`
		ContextManagement json.RawMessage `json:"context_management"`
		Input             []struct {
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
		Tools []struct {
			Type        string          `json:"type"`
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
		} `json:"tools"`
		ToolChoice string `json:"tool_choice"`
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Fatalf("path = %q, want /v1/responses", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&upstreamBody); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "resp_123",
			"object": "response",
			"model":  upstreamBody.Model,
			"output": []map[string]any{{
				"type":      "function_call",
				"call_id":   "call_123",
				"name":      "get_weather",
				"arguments": `{"city":"shanghai"}`,
			}},
		})
	}))
	defer upstream.Close()

	cfg := baseDualProtocolConfig()
	cfg.Inbounds[1].Clients[0].Tag = "anthropic-to-responses"
	cfg.Routing.Rules = []config.RoutingRule{{
		Name:        "anthropic-to-responses-route",
		FromTags:    []string{"anthropic-to-responses"},
		ToTags:      []string{"responses-primary"},
		Strategy:    "failover",
		TargetModel: "gpt-5.4",
	}}
	cfg.Outbounds = []config.OutboundSpec{{
		Name:      "cliproxy-responses",
		Protocol:  "openai_responses",
		Endpoint:  upstream.URL + "/v1",
		AuthToken: "bridge-key",
		Tag:       "responses-primary",
	}}

	app, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	body, err := json.Marshal(map[string]any{
		"model":                "claude-sonnet-4-6",
		"max_tokens":           256,
		"previous_response_id": "resp_prev_123",
		"metadata":             map[string]any{"user_id": "u_123"},
		"thinking":             map[string]any{"type": "adaptive"},
		"context_management": map[string]any{
			"edits": []map[string]any{{"type": "clear_thinking_20251015", "keep": "all"}},
		},
		"output_config": map[string]any{"effort": "high"},
		"system": []map[string]any{{
			"type": "text",
			"text": "You are Claude Code, Anthropic's official CLI for Claude.",
		}},
		"tools": []map[string]any{{
			"name":        "Read",
			"description": "Read file contents",
			"input_schema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"file_path": map[string]any{"type": "string"}},
			},
		}, {
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
				"text": "查询上海天气，必要时调用工具。",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	listeners := app.Server.Listeners()
	w := httptest.NewRecorder()
	listeners[0].Handler.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/messages", "anthropic-token", body))
	if w.Code != http.StatusOK {
		t.Fatalf("route status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	if upstreamBody.Model != "gpt-5.4" {
		t.Fatalf("upstream model = %q, want gpt-5.4", upstreamBody.Model)
	}
	if upstreamBody.Instructions != "You are Claude Code, Anthropic's official CLI for Claude." {
		t.Fatalf("upstream instructions = %q, want bridged system", upstreamBody.Instructions)
	}
	if upstreamBody.MaxOutputTokens != 256 {
		t.Fatalf("upstream max_output_tokens = %d, want 256", upstreamBody.MaxOutputTokens)
	}
	if upstreamBody.PreviousResponseID != "resp_prev_123" {
		t.Fatalf("upstream previous_response_id = %q, want resp_prev_123", upstreamBody.PreviousResponseID)
	}
	if string(upstreamBody.Metadata) != `{"user_id":"u_123"}` {
		t.Fatalf("upstream metadata = %s, want preserved metadata", string(upstreamBody.Metadata))
	}
	if upstreamBody.Reasoning == nil || upstreamBody.Reasoning.Effort != "high" {
		t.Fatalf("upstream reasoning = %#v, want effort high", upstreamBody.Reasoning)
	}
	var upstreamContextManagement map[string]any
	if err := json.Unmarshal(upstreamBody.ContextManagement, &upstreamContextManagement); err != nil {
		t.Fatalf("json.Unmarshal(upstreamBody.ContextManagement) error = %v", err)
	}
	edits, ok := upstreamContextManagement["edits"].([]any)
	if !ok || len(edits) != 1 {
		t.Fatalf("upstream context_management edits = %#v, want one edit", upstreamContextManagement["edits"])
	}
	edit, ok := edits[0].(map[string]any)
	if !ok || edit["type"] != "clear_thinking_20251015" || edit["keep"] != "all" {
		t.Fatalf("upstream context_management edit = %#v, want preserved edit", edits[0])
	}
	if upstreamBody.ToolChoice != "auto" {
		t.Fatalf("upstream tool_choice = %#v, want auto", upstreamBody.ToolChoice)
	}
	if len(upstreamBody.Tools) != 2 {
		t.Fatalf("len(upstream tools) = %d, want 2 including builtin Read", len(upstreamBody.Tools))
	}
	if upstreamBody.Tools[0].Name != "Read" || upstreamBody.Tools[1].Name != "get_weather" {
		t.Fatalf("upstream tools = %#v, want Read and get_weather", upstreamBody.Tools)
	}
	if upstreamBody.Instructions != "You are Claude Code, Anthropic's official CLI for Claude." {
		t.Fatalf("upstream instructions = %q, want bridged system", upstreamBody.Instructions)
	}
	if len(upstreamBody.Input) != 1 || upstreamBody.Input[0].Type != "message" || upstreamBody.Input[0].Role != "user" || upstreamBody.Input[0].Content[0].Text != "查询上海天气，必要时调用工具。" {
		t.Fatalf("upstream input = %#v, want bridged user message", upstreamBody.Input)
	}
	if !strings.Contains(w.Body.String(), `"type":"tool_use"`) || !strings.Contains(w.Body.String(), `"name":"get_weather"`) {
		t.Fatalf("body = %q, want anthropic tool_use response", w.Body.String())
	}
}

func TestNewBridgesResponsesFunctionCallOutputMixedContentToAnthropicOutbound(t *testing.T) {
	var requests []struct {
		Model    string                     `json:"model"`
		Messages []anthropicUpstreamMessage `json:"messages"`
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("path = %q, want /v1/messages", r.URL.Path)
		}
		var body struct {
			Model    string                     `json:"model"`
			Messages []anthropicUpstreamMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		requests = append(requests, body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          "msg_resp_1",
			"type":        "message",
			"role":        "assistant",
			"model":       body.Model,
			"stop_reason": "end_turn",
			"content": []map[string]any{{
				"type": "text",
				"text": "done",
			}},
		})
	}))
	defer upstream.Close()

	cfg := baseDualProtocolConfig()
	cfg.Inbounds = append(cfg.Inbounds, config.InboundSpec{
		Name:     "responses-entry",
		Protocol: "openai_responses",
		Path:     "/v1/responses",
		Clients:  []config.ClientSpec{{Token: "responses-token", Tag: "responses-to-anthropic"}},
	})
	cfg.Listeners[0].Inbounds = append(cfg.Listeners[0].Inbounds, "responses-entry")
	cfg.Inbounds[0].Clients[0].Tag = "office"
	cfg.Routing.Rules = []config.RoutingRule{{
		Name:        "responses-to-anthropic-route",
		FromTags:    []string{"responses-to-anthropic"},
		ToTags:      []string{"anthropic-primary"},
		Strategy:    "failover",
		TargetModel: "claude-sonnet-4-6",
	}}
	cfg.Outbounds = []config.OutboundSpec{{
		Name:      "anthropic-primary",
		Protocol:  "anthropic_messages",
		Endpoint:  upstream.URL + "/v1",
		AuthToken: "bridge-key",
		Tag:       "anthropic-primary",
	}}

	app, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	body, err := json.Marshal(map[string]any{
		"model": "gpt-4o-mini",
		"input": []map[string]any{{
			"type":    "function_call_output",
			"call_id": "call_123",
			"output": []map[string]any{{
				"type": "output_text",
				"text": "lookup failed",
			}, {
				"type":  "json",
				"value": map[string]any{"city": "shanghai", "retryable": true},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	listeners := app.Server.Listeners()
	w := httptest.NewRecorder()
	listeners[0].Handler.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/responses", "responses-token", body))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	if len(requests) != 1 {
		t.Fatalf("len(requests) = %d, want 1", len(requests))
	}
	if len(requests[0].Messages) != 1 {
		t.Fatalf("len(requests[0].Messages) = %d, want 1", len(requests[0].Messages))
	}
	if requests[0].Messages[0].Role != "user" {
		t.Fatalf("requests[0].Messages[0].Role = %q, want user", requests[0].Messages[0].Role)
	}
	if len(requests[0].Messages[0].Content) != 1 {
		t.Fatalf("len(requests[0].Messages[0].Content) = %d, want 1", len(requests[0].Messages[0].Content))
	}
	toolResult := requests[0].Messages[0].Content[0]
	if toolResult.Type != "tool_result" || toolResult.ToolUseID != "call_123" {
		t.Fatalf("toolResult = %#v, want tool_result with tool_use_id", toolResult)
	}
	blocks, ok := toolResult.Content.([]any)
	if !ok {
		t.Fatalf("toolResult.Content type = %T, want []any", toolResult.Content)
	}
	if len(blocks) != 2 {
		t.Fatalf("len(blocks) = %d, want 2", len(blocks))
	}
	first, ok := blocks[0].(map[string]any)
	if !ok || first["type"] != "text" || first["text"] != "lookup failed" {
		t.Fatalf("blocks[0] = %#v, want first text block", blocks[0])
	}
	second, ok := blocks[1].(map[string]any)
	if !ok || second["type"] != "json" {
		t.Fatalf("blocks[1] = %#v, want second json block", blocks[1])
	}
	value, ok := second["value"].(map[string]any)
	if !ok || value["city"] != "shanghai" || value["retryable"] != true {
		t.Fatalf("blocks[1].value = %#v, want preserved json payload", second["value"])
	}
}

func TestNewBridgesAnthropicLeadingSystemReminderToOpenAIResponsesWithoutInstructionsLeak(t *testing.T) {
	var upstreamBody struct {
		Instructions string `json:"instructions"`
		Input        []struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"input"`
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Fatalf("path = %q, want /v1/responses", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&upstreamBody); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "resp_1",
			"object": "response",
			"model":  "gpt-5.4",
			"status": "completed",
			"output": []map[string]any{{
				"type": "message",
				"role": "assistant",
				"content": []map[string]any{{
					"type": "output_text",
					"text": "hello",
				}},
			}},
		})
	}))
	defer upstream.Close()

	cfg := baseDualProtocolConfig()
	cfg.Inbounds[1].Clients[0].Tag = "anthropic-to-responses"
	cfg.Routing.Rules = []config.RoutingRule{{
		Name:        "anthropic-to-responses-route",
		FromTags:    []string{"anthropic-to-responses"},
		ToTags:      []string{"responses-primary"},
		Strategy:    "failover",
		TargetModel: "gpt-5.4",
	}}
	cfg.Outbounds = []config.OutboundSpec{{
		Name:      "cliproxy-responses",
		Protocol:  "openai_responses",
		Endpoint:  upstream.URL + "/v1",
		AuthToken: "bridge-key",
		Tag:       "responses-primary",
	}}

	app, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	body, err := json.Marshal(map[string]any{
		"model": "claude-sonnet-4-6",
		"system": []map[string]any{{
			"type": "text",
			"text": "You are Claude Code, Anthropic's official CLI for Claude.",
		}},
		"messages": []map[string]any{{
			"role": "user",
			"content": []map[string]any{{
				"type": "text",
				"text": "<system-reminder>\nremember repo rules\n</system-reminder>\n",
			}, {
				"type": "text",
				"text": "hi",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	listeners := app.Server.Listeners()
	w := httptest.NewRecorder()
	listeners[0].Handler.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/messages", "anthropic-token", body))
	if w.Code != http.StatusOK {
		t.Fatalf("route status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(upstreamBody.Instructions, "You are Claude Code, Anthropic's official CLI for Claude.") || strings.Contains(upstreamBody.Instructions, "remember repo rules") {
		t.Fatalf("upstream instructions = %q, want system without stripped reminder", upstreamBody.Instructions)
	}
	if len(upstreamBody.Input) != 1 || upstreamBody.Input[0].Type != "message" || upstreamBody.Input[0].Role != "user" || upstreamBody.Input[0].Content[0].Text != "hi" {
		t.Fatalf("upstream input = %#v, want only remaining user hi", upstreamBody.Input)
	}
	if !strings.Contains(w.Body.String(), `"text":"hello"`) {
		t.Fatalf("body = %q, want anthropic text response", w.Body.String())
	}
}

func TestNewFailsWithUnsupportedProviderProtocol(t *testing.T) {
	cfg := baseConfig()
	cfg.Outbounds = []config.OutboundSpec{{Name: "bad", Protocol: "unknown", Tag: "bad-tag"}}
	cfg.Routing.Rules[0].ToTags = []string{"bad-tag"}

	_, err := New(cfg)
	if err == nil || err.Error() != "unsupported provider protocol \"unknown\"" {
		t.Fatalf("New() error = %v, want unsupported provider protocol error", err)
	}
}
