package execution

import (
	"context"
	"errors"
	"testing"

	"syrogo/internal/provider"
	"syrogo/internal/runtime"
)

type stubProvider struct {
	name string
	resp provider.ChatResponse
	err  error
	req  provider.ChatRequest
}

func (p *stubProvider) Name() string {
	return p.name
}

func (p *stubProvider) ChatCompletion(_ context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	p.req = req
	if p.err != nil {
		return provider.ChatResponse{}, p.err
	}
	return p.resp, nil
}

func TestDispatchExecutesFirstOutboundStep(t *testing.T) {
	dispatcher := NewDispatcher()
	p := &stubProvider{
		name: "primary",
		resp: provider.ChatResponse{ID: "1", Object: "chat.completion", Model: "gpt-4", Content: "ok"},
	}

	resp, err := dispatcher.Dispatch(context.Background(), runtime.InternalRequest{
		Model:    "gpt-4",
		Messages: []provider.ChatMessage{{Role: "user", Content: "hello"}},
	}, runtime.ExecutionPlan{
		MatchedRoute: "primary",
		Steps: []runtime.ExecutionStep{{
			Type:           runtime.StepTypeOutbound,
			ProviderName:   "primary",
			ProviderTarget: p,
			Model:          "gpt-4",
		}},
	})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if resp.Content != "ok" {
		t.Fatalf("Dispatch() content = %q, want ok", resp.Content)
	}
	if p.req.Model != "gpt-4" {
		t.Fatalf("provider req.Model = %q, want gpt-4", p.req.Model)
	}
	if len(p.req.Messages) != 1 || p.req.Messages[0].Content != "hello" {
		t.Fatalf("provider req.Messages = %#v, want single hello message", p.req.Messages)
	}
}

func TestDispatchUsesFallbackStepWhenPrimaryFails(t *testing.T) {
	dispatcher := NewDispatcher()
	primary := &stubProvider{name: "primary", err: errors.New("primary failed")}
	fallback := &stubProvider{
		name: "fallback",
		resp: provider.ChatResponse{ID: "2", Object: "chat.completion", Model: "gpt-4", Content: "fallback ok"},
	}

	resp, err := dispatcher.Dispatch(context.Background(), runtime.InternalRequest{
		Model:    "gpt-4",
		Messages: []provider.ChatMessage{{Role: "user", Content: "hello"}},
	}, runtime.ExecutionPlan{
		MatchedRoute: "primary",
		Steps: []runtime.ExecutionStep{
			{
				Type:           runtime.StepTypeOutbound,
				ProviderName:   "primary",
				ProviderTarget: primary,
				Model:          "gpt-4",
			},
			{
				Type:           runtime.StepTypeOutbound,
				ProviderName:   "fallback",
				ProviderTarget: fallback,
				Model:          "gpt-4",
			},
		},
	})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if resp.Content != "fallback ok" {
		t.Fatalf("Dispatch() content = %q, want fallback ok", resp.Content)
	}
	if fallback.req.Model != "gpt-4" {
		t.Fatalf("fallback req.Model = %q, want gpt-4", fallback.req.Model)
	}
}

func TestDispatchFailsWhenPlanHasNoSteps(t *testing.T) {
	dispatcher := NewDispatcher()

	_, err := dispatcher.Dispatch(context.Background(), runtime.InternalRequest{}, runtime.ExecutionPlan{})
	if err == nil || err.Error() != "execution plan has no steps" {
		t.Fatalf("Dispatch() error = %v, want no steps error", err)
	}
}

func TestDispatchFailsWhenAllStepsFail(t *testing.T) {
	dispatcher := NewDispatcher()
	primary := &stubProvider{name: "primary", err: errors.New("primary failed")}
	fallback := &stubProvider{name: "fallback", err: errors.New("fallback failed")}

	_, err := dispatcher.Dispatch(context.Background(), runtime.InternalRequest{Model: "gpt-4"}, runtime.ExecutionPlan{
		Steps: []runtime.ExecutionStep{
			{
				Type:           runtime.StepTypeOutbound,
				ProviderName:   "primary",
				ProviderTarget: primary,
				Model:          "gpt-4",
			},
			{
				Type:           runtime.StepTypeOutbound,
				ProviderName:   "fallback",
				ProviderTarget: fallback,
				Model:          "gpt-4",
			},
		},
	})
	if err == nil || err.Error() != "fallback failed" {
		t.Fatalf("Dispatch() error = %v, want fallback failed", err)
	}
}
