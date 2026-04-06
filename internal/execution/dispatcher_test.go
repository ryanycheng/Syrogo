package execution

import (
	"context"
	"errors"
	"testing"

	"syrogo/internal/provider"
	"syrogo/internal/runtime"
)

type stubProvider struct {
	name         string
	resp         runtime.Response
	streamEvents []runtime.StreamEvent
	err          error
	req          runtime.Request
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
	ch := make(chan runtime.StreamEvent, len(p.streamEvents))
	for _, event := range p.streamEvents {
		ch <- event
	}
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
		Steps: []runtime.ExecutionStep{{
			Type:           runtime.StepTypeOutbound,
			OutboundName:   "primary",
			OutboundTarget: p,
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
		t.Fatalf("outbound req.Model = %q, want gpt-4", p.req.Model)
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

	resp, err := dispatcher.Dispatch(context.Background(), runtime.Request{Model: "gpt-4"}, runtime.ExecutionPlan{
		Steps: []runtime.ExecutionStep{
			{Type: runtime.StepTypeOutbound, OutboundName: "primary", OutboundTarget: primary, Model: "gpt-4", OnError: runtime.FallbackOnRetryable},
			{Type: runtime.StepTypeOutbound, OutboundName: "fallback", OutboundTarget: fallback, Model: "gpt-4", OnError: runtime.FallbackAlways},
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
	fallback := &stubProvider{name: "fallback"}

	_, err := dispatcher.Dispatch(context.Background(), runtime.Request{Model: "gpt-4"}, runtime.ExecutionPlan{
		Steps: []runtime.ExecutionStep{
			{Type: runtime.StepTypeOutbound, OutboundName: "primary", OutboundTarget: primary, Model: "gpt-4", OnError: runtime.FallbackOnRetryable},
			{Type: runtime.StepTypeOutbound, OutboundName: "fallback", OutboundTarget: fallback, Model: "gpt-4", OnError: runtime.FallbackAlways},
		},
	})
	if err == nil || err.Error() != "auth failed" {
		t.Fatalf("Dispatch() error = %v, want auth failed", err)
	}
	if fallback.req.Model != "" {
		t.Fatalf("fallback should not be called, got req.Model = %q", fallback.req.Model)
	}
}

func TestDispatchStreamReturnsFirstOutboundEvents(t *testing.T) {
	dispatcher := NewDispatcher()
	p := &stubProvider{name: "primary", streamEvents: []runtime.StreamEvent{{Type: runtime.StreamEventMessageStart, ResponseID: "chatcmpl-1", Model: "gpt-4"}}}

	events, err := dispatcher.DispatchStream(context.Background(), runtime.Request{Model: "gpt-4"}, runtime.ExecutionPlan{
		Steps: []runtime.ExecutionStep{{Type: runtime.StepTypeOutbound, OutboundName: "primary", OutboundTarget: p, Model: "gpt-4", OnError: runtime.FallbackAlways}},
	})
	if err != nil {
		t.Fatalf("DispatchStream() error = %v", err)
	}
	if !p.req.Stream {
		t.Fatal("outbound req.Stream = false, want true")
	}

	var got []runtime.StreamEvent
	for event := range events {
		got = append(got, event)
	}
	if len(got) != 1 || got[0].Type != runtime.StreamEventMessageStart {
		t.Fatalf("events = %#v, want single message_start", got)
	}
}

func TestDispatchStreamUsesFallbackWhenErrorIsRetryable(t *testing.T) {
	dispatcher := NewDispatcher()
	primary := &stubProvider{name: "primary", err: provider.NewRetryableError(errors.New("temporary upstream failure"))}
	fallback := &stubProvider{name: "fallback", streamEvents: []runtime.StreamEvent{{Type: runtime.StreamEventMessageEnd, ResponseID: "chatcmpl-2", Model: "gpt-4"}}}

	events, err := dispatcher.DispatchStream(context.Background(), runtime.Request{Model: "gpt-4"}, runtime.ExecutionPlan{
		Steps: []runtime.ExecutionStep{
			{Type: runtime.StepTypeOutbound, OutboundName: "primary", OutboundTarget: primary, Model: "gpt-4", OnError: runtime.FallbackOnRetryable},
			{Type: runtime.StepTypeOutbound, OutboundName: "fallback", OutboundTarget: fallback, Model: "gpt-4", OnError: runtime.FallbackAlways},
		},
	})
	if err != nil {
		t.Fatalf("DispatchStream() error = %v", err)
	}
	if fallback.req.Model != "gpt-4" || !fallback.req.Stream {
		t.Fatalf("fallback req = %#v, want stream gpt-4", fallback.req)
	}

	var got []runtime.StreamEvent
	for event := range events {
		got = append(got, event)
	}
	if len(got) != 1 || got[0].ResponseID != "chatcmpl-2" {
		t.Fatalf("events = %#v, want fallback stream", got)
	}
}

func TestDispatchFailsWhenPlanHasNoSteps(t *testing.T) {
	dispatcher := NewDispatcher()
	_, err := dispatcher.Dispatch(context.Background(), runtime.Request{}, runtime.ExecutionPlan{})
	if err == nil || err.Error() != "execution plan has no steps" {
		t.Fatalf("Dispatch() error = %v, want no steps error", err)
	}
}

func TestDispatchStreamFailsWhenPlanHasNoSteps(t *testing.T) {
	dispatcher := NewDispatcher()
	_, err := dispatcher.DispatchStream(context.Background(), runtime.Request{}, runtime.ExecutionPlan{})
	if err == nil || err.Error() != "execution plan has no steps" {
		t.Fatalf("DispatchStream() error = %v, want no steps error", err)
	}
}
