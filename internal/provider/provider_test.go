package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestDecodeOpenAIChatResponseMapsAssistantMessage(t *testing.T) {
	resp, err := decodeOpenAIChatResponse(openAIChatResponseEnvelope{
		ID:     "chatcmpl-1",
		Object: "chat.completion",
		Model:  "gpt-4o-mini",
		Choices: []struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
		}{
			{Message: struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			}{Role: "assistant", Content: "hello from upstream"}},
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

func TestMockProviderStreamCompletionEmitsLifecycleEvents(t *testing.T) {
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
