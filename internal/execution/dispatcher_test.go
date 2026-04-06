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
	resp runtime.Response
	err  error
	req  runtime.Request
}

func (p *stubProvider) Name() string {
	return p.name
}

func (p *stubProvider) ChatCompletion(_ context.Context, req runtime.Request) (runtime.Response, error) {
	p.req = req
	if p.err != nil {
		return runtime.Response{}, p.err
	}
	return p.resp, nil
}

func (p *stubProvider) StreamCompletion(_ context.Context, req runtime.Request) (<-chan runtime.StreamEvent, error) {
	p.req = req
	if p.err != nil {
		return nil, p.err
	}
	ch := make(chan runtime.StreamEvent)
	close(ch)
	return ch, nil
}

func TestDispatchExecutesFirstOutboundStep(t *testing.T) {
	dispatcher := NewDispatcher()
	p := &stubProvider{
		name: "primary",
		resp: runtime.Response{
			ID:     "1",
			Object: "chat.completion",
			Model:  "gpt-4",
			Message: runtime.Message{
				Role:  runtime.MessageRoleAssistant,
				Parts: []runtime.ContentPart{{Type: runtime.ContentPartTypeText, Text: "ok"}},
			},
		},
	}

	resp, err := dispatcher.Dispatch(context.Background(), runtime.Request{
		Model: "gpt-4",
		Messages: []runtime.Message{{
			Role:  runtime.MessageRoleUser,
			Parts: []runtime.ContentPart{{Type: runtime.ContentPartTypeText, Text: "hello"}},
		}},
	}, runtime.ExecutionPlan{
		MatchedRoute: "primary",
		Steps: []runtime.ExecutionStep{{
			Type:           runtime.StepTypeOutbound,
			ProviderName:   "primary",
			ProviderTarget: p,
			Model:          "gpt-4",
			OnError:        runtime.FallbackAlways,
		}},
	})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if got := resp.Message.Parts[0].Text; got != "ok" {
		t.Fatalf("Dispatch() content = %q, want ok", got)
	}
	if p.req.Model != "gpt-4" {
		t.Fatalf("provider req.Model = %q, want gpt-4", p.req.Model)
	}
	if len(p.req.Messages) != 1 || p.req.Messages[0].Parts[0].Text != "hello" {
		t.Fatalf("provider req.Messages = %#v, want single hello message", p.req.Messages)
	}
}

func TestDispatchUsesFallbackStepWhenErrorIsRetryable(t *testing.T) {
	dispatcher := NewDispatcher()
	primary := &stubProvider{name: "primary", err: provider.NewRetryableError(errors.New("temporary upstream failure"))}
	fallback := &stubProvider{
		name: "fallback",
		resp: runtime.Response{
			ID:     "2",
			Object: "chat.completion",
			Model:  "gpt-4",
			Message: runtime.Message{
				Role:  runtime.MessageRoleAssistant,
				Parts: []runtime.ContentPart{{Type: runtime.ContentPartTypeText, Text: "fallback ok"}},
			},
		},
	}

	resp, err := dispatcher.Dispatch(context.Background(), runtime.Request{
		Model: "gpt-4",
		Messages: []runtime.Message{{
			Role:  runtime.MessageRoleUser,
			Parts: []runtime.ContentPart{{Type: runtime.ContentPartTypeText, Text: "hello"}},
		}},
	}, runtime.ExecutionPlan{
		MatchedRoute: "primary",
		Steps: []runtime.ExecutionStep{
			{
				Type:           runtime.StepTypeOutbound,
				ProviderName:   "primary",
				ProviderTarget: primary,
				Model:          "gpt-4",
				OnError:        runtime.FallbackOnRetryable,
			},
			{
				Type:           runtime.StepTypeOutbound,
				ProviderName:   "fallback",
				ProviderTarget: fallback,
				Model:          "gpt-4",
				OnError:        runtime.FallbackAlways,
			},
		},
	})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if got := resp.Message.Parts[0].Text; got != "fallback ok" {
		t.Fatalf("Dispatch() content = %q, want fallback ok", got)
	}
}

func TestDispatchDoesNotFallbackWhenErrorIsFatal(t *testing.T) {
	dispatcher := NewDispatcher()
	primary := &stubProvider{name: "primary", err: provider.NewFatalError(errors.New("auth failed"))}
	fallback := &stubProvider{
		name: "fallback",
		resp: runtime.Response{
			ID:     "2",
			Object: "chat.completion",
			Model:  "gpt-4",
			Message: runtime.Message{
				Role:  runtime.MessageRoleAssistant,
				Parts: []runtime.ContentPart{{Type: runtime.ContentPartTypeText, Text: "fallback ok"}},
			},
		},
	}

	_, err := dispatcher.Dispatch(context.Background(), runtime.Request{Model: "gpt-4"}, runtime.ExecutionPlan{
		Steps: []runtime.ExecutionStep{
			{
				Type:           runtime.StepTypeOutbound,
				ProviderName:   "primary",
				ProviderTarget: primary,
				Model:          "gpt-4",
				OnError:        runtime.FallbackOnRetryable,
			},
			{
				Type:           runtime.StepTypeOutbound,
				ProviderName:   "fallback",
				ProviderTarget: fallback,
				Model:          "gpt-4",
				OnError:        runtime.FallbackAlways,
			},
		},
	})
	if err == nil || err.Error() != "auth failed" {
		t.Fatalf("Dispatch() error = %v, want auth failed", err)
	}
	if fallback.req.Model != "" {
		t.Fatalf("fallback should not be called, got req.Model = %q", fallback.req.Model)
	}
}

func TestDispatchUsesFallbackStepWhenQuotaExceeded(t *testing.T) {
	dispatcher := NewDispatcher()
	primary := &stubProvider{name: "primary", err: provider.NewQuotaExceededError(errors.New("quota exceeded"))}
	fallback := &stubProvider{
		name: "fallback",
		resp: runtime.Response{
			ID:     "2",
			Object: "chat.completion",
			Model:  "gpt-4",
			Message: runtime.Message{
				Role:  runtime.MessageRoleAssistant,
				Parts: []runtime.ContentPart{{Type: runtime.ContentPartTypeText, Text: "fallback ok"}},
			},
		},
	}

	resp, err := dispatcher.Dispatch(context.Background(), runtime.Request{Model: "gpt-4"}, runtime.ExecutionPlan{
		Steps: []runtime.ExecutionStep{
			{
				Type:           runtime.StepTypeOutbound,
				ProviderName:   "primary",
				ProviderTarget: primary,
				Model:          "gpt-4",
				OnError:        runtime.FallbackOnQuotaExceeded,
			},
			{
				Type:           runtime.StepTypeOutbound,
				ProviderName:   "fallback",
				ProviderTarget: fallback,
				Model:          "gpt-4",
				OnError:        runtime.FallbackAlways,
			},
		},
	})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if got := resp.Message.Parts[0].Text; got != "fallback ok" {
		t.Fatalf("Dispatch() content = %q, want fallback ok", got)
	}
}

func TestDispatchFailsWhenPlanHasNoSteps(t *testing.T) {
	dispatcher := NewDispatcher()

	_, err := dispatcher.Dispatch(context.Background(), runtime.Request{}, runtime.ExecutionPlan{})
	if err == nil || err.Error() != "execution plan has no steps" {
		t.Fatalf("Dispatch() error = %v, want no steps error", err)
	}
}

func TestDispatchFailsWhenAllStepsFail(t *testing.T) {
	dispatcher := NewDispatcher()
	primary := &stubProvider{name: "primary", err: provider.NewRetryableError(errors.New("primary failed"))}
	fallback := &stubProvider{name: "fallback", err: provider.NewRetryableError(errors.New("fallback failed"))}

	_, err := dispatcher.Dispatch(context.Background(), runtime.Request{Model: "gpt-4"}, runtime.ExecutionPlan{
		Steps: []runtime.ExecutionStep{
			{
				Type:           runtime.StepTypeOutbound,
				ProviderName:   "primary",
				ProviderTarget: primary,
				Model:          "gpt-4",
				OnError:        runtime.FallbackOnRetryable,
			},
			{
				Type:           runtime.StepTypeOutbound,
				ProviderName:   "fallback",
				ProviderTarget: fallback,
				Model:          "gpt-4",
				OnError:        runtime.FallbackAlways,
			},
		},
	})
	if err == nil || err.Error() != "fallback failed" {
		t.Fatalf("Dispatch() error = %v, want fallback failed", err)
	}
}
