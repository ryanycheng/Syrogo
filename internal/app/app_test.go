package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"syrogo/internal/config"
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

func TestNewSupportsAnthropicInboundProtocol(t *testing.T) {
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
	if len(upstreamBody.Tools) != 1 {
		t.Fatalf("len(upstream tools) = %d, want 1 after builtin filtering", len(upstreamBody.Tools))
	}
	if upstreamBody.Tools[0].Type != "function" || upstreamBody.Tools[0].Function.Name != "get_weather" {
		t.Fatalf("upstream tools[0] = %#v, want only custom function tool", upstreamBody.Tools[0])
	}
	if !upstreamBody.Tools[0].Function.Strict {
		t.Fatalf("upstream tools[0].Function.Strict = %v, want true", upstreamBody.Tools[0].Function.Strict)
	}
	if !strings.Contains(w.Body.String(), `"type":"tool_use"`) || !strings.Contains(w.Body.String(), `"name":"get_weather"`) {
		t.Fatalf("body = %q, want anthropic tool_use response", w.Body.String())
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

func TestNewFailsWithUnsupportedProviderProtocol(t *testing.T) {
	cfg := baseConfig()
	cfg.Outbounds = []config.OutboundSpec{{Name: "bad", Protocol: "unknown", Tag: "bad-tag"}}
	cfg.Routing.Rules[0].ToTags = []string{"bad-tag"}

	_, err := New(cfg)
	if err == nil || err.Error() != "unsupported provider protocol \"unknown\"" {
		t.Fatalf("New() error = %v, want unsupported provider protocol error", err)
	}
}
