package execution

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ryanycheng/Syrogo/internal/accounting"
	"github.com/ryanycheng/Syrogo/internal/provider"
	"github.com/ryanycheng/Syrogo/internal/quota"
	"github.com/ryanycheng/Syrogo/internal/runtime"
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

func TestDispatchAlwaysDoesNotFallbackWhenErrorIsFatal(t *testing.T) {
	dispatcher := NewDispatcher()
	primary := &stubProvider{name: "primary", err: provider.NewFatalError(errors.New("bad request"))}
	fallback := &stubProvider{name: "fallback"}

	_, err := dispatcher.Dispatch(context.Background(), runtime.Request{Model: "gpt-4"}, runtime.ExecutionPlan{
		Steps: []runtime.ExecutionStep{
			{Type: runtime.StepTypeOutbound, OutboundName: "primary", OutboundTarget: primary, Model: "gpt-4", OnError: runtime.FallbackAlways},
			{Type: runtime.StepTypeOutbound, OutboundName: "fallback", OutboundTarget: fallback, Model: "gpt-4", OnError: runtime.FallbackAlways},
		},
	})
	if err == nil || err.Error() != "bad request" {
		t.Fatalf("Dispatch() error = %v, want bad request", err)
	}
	if fallback.req.Model != "" {
		t.Fatalf("fallback should not be called, got req.Model = %q", fallback.req.Model)
	}
}

func TestDispatchDoesNotFallbackWhenErrorIsAuthFailed(t *testing.T) {
	dispatcher := NewDispatcher()
	primary := &stubProvider{name: "primary", err: provider.NewAuthFailedError(errors.New("unauthorized"))}
	fallback := &stubProvider{name: "fallback"}

	_, err := dispatcher.Dispatch(context.Background(), runtime.Request{Model: "gpt-4"}, runtime.ExecutionPlan{
		Steps: []runtime.ExecutionStep{
			{Type: runtime.StepTypeOutbound, OutboundName: "primary", OutboundTarget: primary, Model: "gpt-4", OnError: runtime.FallbackAlways},
			{Type: runtime.StepTypeOutbound, OutboundName: "fallback", OutboundTarget: fallback, Model: "gpt-4", OnError: runtime.FallbackAlways},
		},
	})
	if err == nil || err.Error() != "unauthorized" {
		t.Fatalf("Dispatch() error = %v, want unauthorized", err)
	}
	if fallback.req.Model != "" {
		t.Fatalf("fallback should not be called, got req.Model = %q", fallback.req.Model)
	}
}

func TestDispatchDoesNotFallbackWhenCapabilityUnsupported(t *testing.T) {
	dispatcher := NewDispatcher()
	primary := &stubProvider{name: "primary", err: provider.NewCapabilityUnsupportedError(errors.New("unsupported builtin tools"))}
	fallback := &stubProvider{name: "fallback"}

	_, err := dispatcher.Dispatch(context.Background(), runtime.Request{Model: "gpt-4"}, runtime.ExecutionPlan{
		Steps: []runtime.ExecutionStep{
			{Type: runtime.StepTypeOutbound, OutboundName: "primary", OutboundTarget: primary, Model: "gpt-4", OnError: runtime.FallbackAlways},
			{Type: runtime.StepTypeOutbound, OutboundName: "fallback", OutboundTarget: fallback, Model: "gpt-4", OnError: runtime.FallbackAlways},
		},
	})
	if err == nil || err.Error() != "unsupported builtin tools" {
		t.Fatalf("Dispatch() error = %v, want unsupported builtin tools", err)
	}
	if fallback.req.Model != "" {
		t.Fatalf("fallback should not be called, got req.Model = %q", fallback.req.Model)
	}
}

func TestDispatchRecordsErrorKind(t *testing.T) {
	store := accounting.NewMemoryStore()
	dispatcher := NewDispatcherWithStore(store)
	primary := &stubProvider{name: "primary", err: provider.NewAuthFailedError(errors.New("unauthorized"))}

	_, err := dispatcher.Dispatch(context.Background(), runtime.Request{Model: "gpt-4"}, runtime.ExecutionPlan{
		Steps: []runtime.ExecutionStep{{
			Type:           runtime.StepTypeOutbound,
			OutboundName:   "primary",
			OutboundTarget: primary,
			Model:          "gpt-4",
			OnError:        runtime.FallbackAlways,
		}},
	})
	if err == nil {
		t.Fatal("Dispatch() error = nil, want error")
	}

	items, err := store.Query(accounting.Query{GroupBy: "error_kind", Window: accounting.WindowTotal})
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if len(items) != 1 || items[0].Value != "auth_failed" || items[0].ErrorCount != 1 {
		t.Fatalf("items = %#v, want auth_failed error_count=1", items)
	}
}

func TestDispatchSkipsQuotaLimitedOutbound(t *testing.T) {
	now := time.Date(2026, 6, 12, 9, 0, 0, 0, time.UTC)
	tracker := quota.NewTestTracker([]quota.OutboundConfig{{
		Name:          "primary",
		Cooldown:      10 * time.Minute,
		ProbeInterval: time.Minute,
		Windows:       []quota.WindowConfig{{Name: "short", Duration: time.Hour, MaxRequests: 1}},
	}}, func() time.Time { return now })
	tracker.RecordSuccess("primary")
	dispatcher := NewDispatcherWithStoreAndQuota(accounting.NewMemoryStore(), tracker)
	primary := &stubProvider{name: "primary"}
	fallback := &stubProvider{
		name: "fallback",
		resp: runtime.Response{Model: "gpt-4", Message: runtime.Message{Parts: []runtime.ContentPart{{Type: runtime.ContentPartTypeText, Text: "fallback ok"}}}},
	}

	resp, err := dispatcher.Dispatch(context.Background(), runtime.Request{Model: "gpt-4"}, runtime.ExecutionPlan{Steps: []runtime.ExecutionStep{
		{Type: runtime.StepTypeOutbound, OutboundName: "primary", OutboundTarget: primary, Model: "gpt-4", OnError: runtime.FallbackOnRetryable},
		{Type: runtime.StepTypeOutbound, OutboundName: "fallback", OutboundTarget: fallback, Model: "gpt-4", OnError: runtime.FallbackOnRetryable},
	}})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if primary.req.Model != "" {
		t.Fatalf("primary should be skipped, got req = %#v", primary.req)
	}
	if got := resp.Message.Parts[0].Text; got != "fallback ok" {
		t.Fatalf("response text = %q, want fallback ok", got)
	}
}

func TestDispatchMarksQuotaExceededCooldownAndProbeRecovery(t *testing.T) {
	now := time.Date(2026, 6, 12, 9, 0, 0, 0, time.UTC)
	tracker := quota.NewTestTracker([]quota.OutboundConfig{{
		Name:          "primary",
		Cooldown:      10 * time.Minute,
		ProbeInterval: time.Minute,
		Windows:       []quota.WindowConfig{{Name: "daily", Duration: 24 * time.Hour, MaxRequests: 100}},
	}}, func() time.Time { return now })
	dispatcher := NewDispatcherWithStoreAndQuota(accounting.NewMemoryStore(), tracker)
	primary := &stubProvider{name: "primary", err: provider.NewQuotaExceededError(errors.New("quota"))}
	fallback := &stubProvider{name: "fallback", resp: runtime.Response{Model: "gpt-4"}}
	plan := runtime.ExecutionPlan{Steps: []runtime.ExecutionStep{
		{Type: runtime.StepTypeOutbound, OutboundName: "primary", OutboundTarget: primary, Model: "gpt-4", OnError: runtime.FallbackOnRetryable},
		{Type: runtime.StepTypeOutbound, OutboundName: "fallback", OutboundTarget: fallback, Model: "gpt-4", OnError: runtime.FallbackOnRetryable},
	}}

	if _, err := dispatcher.Dispatch(context.Background(), runtime.Request{Model: "gpt-4"}, plan); err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	items := tracker.Snapshot()
	if len(items) != 1 || items[0].State != quota.StateCooldown {
		t.Fatalf("Snapshot() = %#v, want primary cooldown", items)
	}

	primary.req = runtime.Request{}
	fallback.req = runtime.Request{}
	if _, err := dispatcher.Dispatch(context.Background(), runtime.Request{Model: "gpt-4"}, plan); err != nil {
		t.Fatalf("Dispatch() during cooldown error = %v", err)
	}
	if primary.req.Model != "" || fallback.req.Model != "gpt-4" {
		t.Fatalf("primary req = %#v fallback req = %#v, want cooldown skip", primary.req, fallback.req)
	}

	now = now.Add(time.Minute)
	primary.err = nil
	primary.resp = runtime.Response{Model: "gpt-4", Message: runtime.Message{Parts: []runtime.ContentPart{{Type: runtime.ContentPartTypeText, Text: "primary ok"}}}}
	resp, err := dispatcher.Dispatch(context.Background(), runtime.Request{Model: "gpt-4"}, plan)
	if err != nil {
		t.Fatalf("Dispatch() probe error = %v", err)
	}
	if got := resp.Message.Parts[0].Text; got != "primary ok" {
		t.Fatalf("response text = %q, want primary ok", got)
	}
	if items := tracker.Snapshot(); len(items) != 1 || items[0].State != quota.StateAvailable {
		t.Fatalf("Snapshot() after probe success = %#v, want available", items)
	}
}

func TestDispatchStreamMarksQuotaExceededCooldown(t *testing.T) {
	now := time.Date(2026, 6, 12, 9, 0, 0, 0, time.UTC)
	tracker := quota.NewTestTracker([]quota.OutboundConfig{{
		Name:          "primary",
		Cooldown:      10 * time.Minute,
		ProbeInterval: time.Minute,
		Windows:       []quota.WindowConfig{{Name: "daily", Duration: 24 * time.Hour, MaxRequests: 100}},
	}}, func() time.Time { return now })
	dispatcher := NewDispatcherWithStoreAndQuota(accounting.NewMemoryStore(), tracker)
	p := &stubProvider{name: "primary", err: provider.NewQuotaExceededError(errors.New("quota"))}

	_, err := dispatcher.DispatchStream(context.Background(), runtime.Request{Model: "gpt-4"}, runtime.ExecutionPlan{Steps: []runtime.ExecutionStep{{Type: runtime.StepTypeOutbound, OutboundName: "primary", OutboundTarget: p, Model: "gpt-4", OnError: runtime.FallbackAlways}}})
	if err == nil {
		t.Fatal("DispatchStream() error = nil, want quota error")
	}
	if items := tracker.Snapshot(); len(items) != 1 || items[0].State != quota.StateCooldown {
		t.Fatalf("Snapshot() = %#v, want cooldown", items)
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

func TestDispatchStreamRecordsEventErrorKind(t *testing.T) {
	store := accounting.NewMemoryStore()
	dispatcher := NewDispatcherWithStore(store)
	p := &stubProvider{name: "primary", streamEvents: []runtime.StreamEvent{{Type: runtime.StreamEventError, Err: provider.NewUpstreamServerError(errors.New("bad gateway"))}}}

	events, err := dispatcher.DispatchStream(context.Background(), runtime.Request{Model: "gpt-4"}, runtime.ExecutionPlan{
		Steps: []runtime.ExecutionStep{{Type: runtime.StepTypeOutbound, OutboundName: "primary", OutboundTarget: p, Model: "gpt-4", OnError: runtime.FallbackAlways}},
	})
	if err != nil {
		t.Fatalf("DispatchStream() error = %v", err)
	}
	for range events {
	}

	items, err := store.Query(accounting.Query{GroupBy: "error_kind", Window: accounting.WindowTotal})
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if len(items) != 1 || items[0].Value != "upstream_server_error" || items[0].ErrorCount != 1 {
		t.Fatalf("items = %#v, want upstream_server_error error_count=1", items)
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
