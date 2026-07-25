package execution

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ryanycheng/Syrogo/internal/accounting"
	"github.com/ryanycheng/Syrogo/internal/latency"
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

type controlledStreamProvider struct {
	name   string
	events chan runtime.StreamEvent
}

func (p *controlledStreamProvider) Name() string { return p.name }
func (p *controlledStreamProvider) ChatCompletion(context.Context, runtime.Request) (runtime.Response, error) {
	return runtime.Response{}, nil
}
func (p *controlledStreamProvider) StreamCompletion(context.Context, runtime.Request) (<-chan runtime.StreamEvent, error) {
	return p.events, nil
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

func TestDispatchSelectsPreferredError(t *testing.T) {
	tests := []struct {
		name   string
		errors []error
	}{
		{name: "429 then retryable", errors: []error{
			&provider.ProviderError{Kind: provider.ErrorKindQuotaExceeded, Err: errors.New("upstream 429"), StatusCode: 429, RequestID: "req-429"},
			provider.NewRetryableError(errors.New("temporary failure")),
		}},
		{name: "retryable then 429", errors: []error{
			provider.NewRetryableError(errors.New("temporary failure")),
			&provider.ProviderError{Kind: provider.ErrorKindQuotaExceeded, Err: errors.New("upstream 429"), StatusCode: 429, RequestID: "req-429"},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := accounting.NewMemoryStore()
			dispatcher := NewDispatcherWithStore(store)
			steps := make([]runtime.ExecutionStep, 0, len(tt.errors))
			for _, err := range tt.errors {
				p := &stubProvider{name: "outbound", err: err}
				steps = append(steps, runtime.ExecutionStep{Type: runtime.StepTypeOutbound, OutboundName: p.name, OutboundTarget: p, Model: "gpt-4", OnError: runtime.FallbackOnRetryable})
			}

			_, err := dispatcher.Dispatch(context.Background(), runtime.Request{Model: "gpt-4"}, runtime.ExecutionPlan{Steps: steps})
			var providerErr *provider.ProviderError
			if !provider.AsProviderError(err, &providerErr) || providerErr.StatusCode != 429 || providerErr.RequestID != "req-429" {
				t.Fatalf("Dispatch() error = %#v, want upstream 429 metadata", err)
			}
			records, queryErr := store.RecentRecords(accounting.RecentRecordsQuery{Limit: 10})
			if queryErr != nil {
				t.Fatalf("RecentRecords() error = %v", queryErr)
			}
			if len(records) != 1 || records[0].ErrorKind != string(provider.ErrorKindQuotaExceeded) {
				t.Fatalf("accounting records = %#v, want preferred quota_exceeded", records)
			}
		})
	}
}

func TestDispatchPreferredErrorDoesNotAffectFallbackSuccess(t *testing.T) {
	dispatcher := NewDispatcher()
	primary := &stubProvider{name: "primary", err: &provider.ProviderError{Kind: provider.ErrorKindQuotaExceeded, Err: errors.New("upstream 429"), StatusCode: 429}}
	fallback := &stubProvider{name: "fallback", resp: runtime.Response{Model: "gpt-4"}}

	_, err := dispatcher.Dispatch(context.Background(), runtime.Request{Model: "gpt-4"}, runtime.ExecutionPlan{Steps: []runtime.ExecutionStep{
		{Type: runtime.StepTypeOutbound, OutboundName: primary.name, OutboundTarget: primary, Model: "gpt-4", OnError: runtime.FallbackOnRetryable},
		{Type: runtime.StepTypeOutbound, OutboundName: fallback.name, OutboundTarget: fallback, Model: "gpt-4", OnError: runtime.FallbackOnRetryable},
	}})
	if err != nil {
		t.Fatalf("Dispatch() error = %v, want fallback success", err)
	}
	if fallback.req.Model != "gpt-4" {
		t.Fatalf("fallback req = %#v, want fallback called", fallback.req)
	}
}

func TestDispatchSkipsUnavailableModelAndFallsBack(t *testing.T) {
	dispatcher := NewDispatcher()
	primary := &stubProvider{name: "primary"}
	fallback := &stubProvider{name: "fallback", resp: runtime.Response{Model: "fallback-model"}}
	_, err := dispatcher.Dispatch(context.Background(), runtime.Request{Model: "client-model"}, runtime.ExecutionPlan{
		RequestedModel: "client-model",
		Steps: []runtime.ExecutionStep{
			{Type: runtime.StepTypeOutbound, OutboundName: "primary", OutboundTarget: primary, Model: "client-model", ModelUnavailable: true, OnError: runtime.FallbackOnRetryable},
			{Type: runtime.StepTypeOutbound, OutboundName: "fallback", OutboundTarget: fallback, Model: "fallback-model", OnError: runtime.FallbackOnRetryable},
		},
	})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if primary.req.Model != "" || fallback.req.Model != "fallback-model" {
		t.Fatalf("primary req = %#v fallback req = %#v, want unavailable skipped and fallback called", primary.req, fallback.req)
	}
}

func TestDispatchAllModelsUnavailable(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "non-stream", true: "stream"}[stream], func(t *testing.T) {
			dispatcher := NewDispatcher()
			p := &stubProvider{name: "primary"}
			plan := runtime.ExecutionPlan{RequestedModel: "secret-model", Steps: []runtime.ExecutionStep{{Type: runtime.StepTypeOutbound, OutboundName: "primary", OutboundTarget: p, Model: "secret-model", ModelUnavailable: true}}}
			var err error
			if stream {
				_, err = dispatcher.DispatchStream(context.Background(), runtime.Request{Model: "secret-model"}, plan)
			} else {
				_, err = dispatcher.Dispatch(context.Background(), runtime.Request{Model: "secret-model"}, plan)
			}
			if provider.NormalizeError(err) != provider.ErrorKindModelUnavailable || p.req.Model != "" {
				t.Fatalf("error = %#v req = %#v, want model unavailable without provider call", err, p.req)
			}
		})
	}
}

func TestDispatchModelUnavailableDoesNotOverridePreferred429(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "non-stream", true: "stream"}[stream], func(t *testing.T) {
			dispatcher := NewDispatcher()
			primary := &stubProvider{name: "primary", err: &provider.ProviderError{Kind: provider.ErrorKindQuotaExceeded, Err: errors.New("upstream 429"), StatusCode: 429}}
			plan := runtime.ExecutionPlan{RequestedModel: "client-model", Steps: []runtime.ExecutionStep{
				{Type: runtime.StepTypeOutbound, OutboundName: "primary", OutboundTarget: primary, Model: "model-a", OnError: runtime.FallbackOnRetryable},
				{Type: runtime.StepTypeOutbound, OutboundName: "fallback", OutboundTarget: &stubProvider{name: "fallback"}, Model: "client-model", ModelUnavailable: true},
			}}
			var err error
			if stream {
				_, err = dispatcher.DispatchStream(context.Background(), runtime.Request{Model: "client-model"}, plan)
			} else {
				_, err = dispatcher.Dispatch(context.Background(), runtime.Request{Model: "client-model"}, plan)
			}
			var providerErr *provider.ProviderError
			if !provider.AsProviderError(err, &providerErr) || providerErr.Kind != provider.ErrorKindQuotaExceeded || providerErr.StatusCode != 429 {
				t.Fatalf("error = %#v, want preferred real 429", err)
			}
		})
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

func TestDispatchRecordsProviderHealth(t *testing.T) {
	now := time.Date(2026, 6, 13, 9, 0, 0, 0, time.UTC)
	health := provider.NewTestHealthTracker([]string{"primary"}, func() time.Time { return now })
	dispatcher := NewDispatcherWithStoreQuotaHealthAndEvents(accounting.NewMemoryStore(), nil, health, nil)
	primary := &stubProvider{name: "primary", err: provider.NewTimeoutError(errors.New("timeout"))}
	plan := runtime.ExecutionPlan{Steps: []runtime.ExecutionStep{{Type: runtime.StepTypeOutbound, OutboundName: "primary", OutboundTarget: primary, Model: "gpt-4", OnError: runtime.FallbackAlways}}}

	_, err := dispatcher.Dispatch(context.Background(), runtime.Request{Model: "gpt-4"}, plan)
	if err == nil {
		t.Fatal("Dispatch() error = nil, want error")
	}
	items := dispatcher.QueryProviderHealth()
	if len(items) != 1 || items[0].State != provider.HealthDegraded || items[0].LastErrorKind != string(provider.ErrorKindTimeout) || items[0].ConsecutiveFailures != 1 {
		t.Fatalf("health = %#v, want degraded timeout", items)
	}

	now = now.Add(time.Minute)
	primary.err = nil
	primary.resp = runtime.Response{Model: "gpt-4"}
	if _, err := dispatcher.Dispatch(context.Background(), runtime.Request{Model: "gpt-4"}, plan); err != nil {
		t.Fatalf("Dispatch() after recovery error = %v", err)
	}
	items = dispatcher.QueryProviderHealth()
	if len(items) != 1 || items[0].State != provider.HealthAvailable || items[0].ConsecutiveFailures != 0 || items[0].LastSuccessAt == "" {
		t.Fatalf("health after recovery = %#v, want available", items)
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

func TestDispatchSkipsHealthDegradedOutbound(t *testing.T) {
	health := provider.NewTestHealthTracker([]string{"primary", "fallback"}, func() time.Time { return time.Date(2026, 6, 13, 9, 0, 0, 0, time.UTC) })
	for range provider.DefaultHealthFailureThreshold {
		health.RecordFailure("primary", provider.ErrorKindTimeout)
	}
	dispatcher := NewDispatcherWithStoreQuotaHealthAndEvents(accounting.NewMemoryStore(), nil, health, nil)
	primary := &stubProvider{name: "primary"}
	fallback := &stubProvider{name: "fallback", resp: runtime.Response{Model: "gpt-4", Message: runtime.Message{Parts: []runtime.ContentPart{{Type: runtime.ContentPartTypeText, Text: "fallback ok"}}}}}

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

func TestDispatchHealthProbeRecoversDegradedOutbound(t *testing.T) {
	now := time.Date(2026, 6, 13, 9, 0, 0, 0, time.UTC)
	health := provider.NewTestHealthTrackerWithProbeInterval([]string{"primary", "fallback"}, func() time.Time { return now }, time.Minute)
	for range provider.DefaultHealthFailureThreshold {
		health.RecordFailure("primary", provider.ErrorKindTimeout)
	}
	recorder := quota.NewTestEventRecorder(10, func() time.Time { return now })
	dispatcher := NewDispatcherWithStoreQuotaHealthAndEvents(accounting.NewMemoryStore(), nil, health, recorder)
	primary := &stubProvider{name: "primary"}
	fallback := &stubProvider{name: "fallback", resp: runtime.Response{Model: "gpt-4", Message: runtime.Message{Parts: []runtime.ContentPart{{Type: runtime.ContentPartTypeText, Text: "fallback ok"}}}}}
	plan := runtime.ExecutionPlan{Steps: []runtime.ExecutionStep{
		{Type: runtime.StepTypeOutbound, OutboundName: "primary", OutboundTarget: primary, Model: "gpt-4", OnError: runtime.FallbackOnRetryable},
		{Type: runtime.StepTypeOutbound, OutboundName: "fallback", OutboundTarget: fallback, Model: "gpt-4", OnError: runtime.FallbackOnRetryable},
	}}

	resp, err := dispatcher.Dispatch(context.Background(), runtime.Request{Model: "gpt-4"}, plan)
	if err != nil {
		t.Fatalf("Dispatch() before probe error = %v", err)
	}
	if primary.req.Model != "" || resp.Message.Parts[0].Text != "fallback ok" {
		t.Fatalf("primary req = %#v resp = %#v, want degraded skip to fallback", primary.req, resp)
	}

	now = now.Add(time.Minute)
	primary.resp = runtime.Response{Model: "gpt-4", Message: runtime.Message{Parts: []runtime.ContentPart{{Type: runtime.ContentPartTypeText, Text: "primary ok"}}}}
	primary.req = runtime.Request{}
	fallback.req = runtime.Request{}
	resp, err = dispatcher.Dispatch(context.Background(), runtime.Request{Model: "gpt-4"}, plan)
	if err != nil {
		t.Fatalf("Dispatch() probe error = %v", err)
	}
	if primary.req.Model != "gpt-4" || fallback.req.Model != "" || resp.Message.Parts[0].Text != "primary ok" {
		t.Fatalf("primary req = %#v fallback req = %#v resp = %#v, want successful primary probe", primary.req, fallback.req, resp)
	}
	items := dispatcher.QueryProviderHealth()
	if len(items) != 2 || items[1].State != provider.HealthAvailable || items[1].ConsecutiveFailures != 0 {
		t.Fatalf("health = %#v, want recovered primary", items)
	}
	events := recorder.Snapshot()
	if len(events) != 2 || events[0].Type != quota.EventProviderHealthLimited || events[1].Type != quota.EventProviderProbeSucceeded {
		t.Fatalf("events = %#v, want health limited and probe succeeded", events)
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
	recorder := quota.NewTestEventRecorder(10, func() time.Time { return now })
	dispatcher := NewDispatcherWithStoreQuotaAndEvents(accounting.NewMemoryStore(), tracker, recorder)
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
	if events := recorder.Snapshot(); len(events) != 1 || events[0].Type != quota.EventOutboundLimited || events[0].Outbound != "primary" {
		t.Fatalf("events = %#v, want outbound limited event", events)
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
	recorder := quota.NewTestEventRecorder(10, func() time.Time { return now })
	dispatcher := NewDispatcherWithStoreQuotaAndEvents(accounting.NewMemoryStore(), tracker, recorder)
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
	if got := items[0].Windows[0]; got.UsedRequests != 0 || got.UsedTokens != 0 {
		t.Fatalf("quota-exceeded usage = %#v, want no successful request or tokens", got)
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
	events := recorder.Snapshot()
	if len(events) != 3 {
		t.Fatalf("events = %#v, want 3 quota events", events)
	}
	if events[0].Type != quota.EventOutboundQuotaExceeded || events[1].Type != quota.EventOutboundLimited || events[2].Type != quota.EventOutboundProbeSucceeded {
		t.Fatalf("events = %#v, want quota_exceeded, outbound_limited, probe_succeeded", events)
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
	items := tracker.Snapshot()
	if len(items) != 1 || items[0].State != quota.StateCooldown {
		t.Fatalf("Snapshot() = %#v, want cooldown", items)
	}
	if got := items[0].Windows[0]; got.UsedRequests != 0 || got.UsedTokens != 0 {
		t.Fatalf("stream quota-exceeded usage = %#v, want no successful request or tokens", got)
	}
}
func TestDispatchStreamRecordsTTFTMilestones(t *testing.T) {
	latencyStore := latency.NewStore(10)
	dispatcher := NewDispatcherWithStoreQuotaHealthEventsAndLatency(accounting.NewMemoryStore(), nil, nil, nil, latencyStore)
	providerEvents := make(chan runtime.StreamEvent)
	p := &controlledStreamProvider{name: "primary", events: providerEvents}
	ctx, recorder := latency.Start(context.Background(), latencyStore, "req-stream", "POST", "/v1/messages", time.Now().Add(-20*time.Millisecond))

	events, err := dispatcher.DispatchStream(ctx, runtime.Request{Model: "claude"}, runtime.ExecutionPlan{
		Steps: []runtime.ExecutionStep{{Type: runtime.StepTypeOutbound, OutboundName: "primary", OutboundProtocol: "anthropic_messages", OutboundTarget: p, Model: "claude", OnError: runtime.FallbackAlways}},
	})
	if err != nil {
		t.Fatalf("DispatchStream() error = %v", err)
	}
	active := latencyStore.ActiveSnapshot()
	if len(active.Items) != 1 || active.Items[0].StreamState != latency.StreamStateWaitingFirstToken || active.Items[0].OutboundName != "primary" {
		t.Fatalf("active before token = %#v", active.Items)
	}

	providerEvents <- runtime.StreamEvent{Type: runtime.StreamEventMessageStart}
	if event := <-events; event.Type != runtime.StreamEventMessageStart {
		t.Fatalf("first event = %#v", event)
	}
	active = latencyStore.ActiveSnapshot()
	if active.Items[0].FirstTokenAt != "" {
		t.Fatalf("message_start recorded first token: %#v", active.Items[0])
	}

	providerEvents <- runtime.StreamEvent{Type: runtime.StreamEventContentDelta, Delta: &runtime.ContentPart{Type: runtime.ContentPartTypeText, Text: "hello"}}
	if event := <-events; event.Type != runtime.StreamEventContentDelta {
		t.Fatalf("content event = %#v", event)
	}
	active = latencyStore.ActiveSnapshot()
	if active.Items[0].StreamState != latency.StreamStateStreaming || active.Items[0].FirstTokenAt == "" || active.Items[0].StreamEventCount != 2 {
		t.Fatalf("active after token = %#v", active.Items[0])
	}

	close(providerEvents)
	for range events {
	}
	recorder.Finish(200, time.Now())
	completed := latencyStore.Snapshot()
	if len(completed.Items) != 1 || completed.Items[0].StreamState != latency.StreamStateCompleted || completed.Items[0].TTFTMs <= 0 {
		t.Fatalf("completed trace = %#v", completed.Items)
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

func TestDispatchStreamSelectsPreferredError(t *testing.T) {
	tests := []struct {
		name   string
		errors []error
	}{
		{name: "429 then retryable", errors: []error{
			&provider.ProviderError{Kind: provider.ErrorKindQuotaExceeded, Err: errors.New("upstream 429"), StatusCode: 429, RetryAfter: "10"},
			provider.NewRetryableError(errors.New("temporary failure")),
		}},
		{name: "retryable then 429", errors: []error{
			provider.NewRetryableError(errors.New("temporary failure")),
			&provider.ProviderError{Kind: provider.ErrorKindQuotaExceeded, Err: errors.New("upstream 429"), StatusCode: 429, RetryAfter: "10"},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := accounting.NewMemoryStore()
			dispatcher := NewDispatcherWithStore(store)
			steps := make([]runtime.ExecutionStep, 0, len(tt.errors))
			for _, err := range tt.errors {
				p := &stubProvider{name: "outbound", err: err}
				steps = append(steps, runtime.ExecutionStep{Type: runtime.StepTypeOutbound, OutboundName: p.name, OutboundTarget: p, Model: "gpt-4", OnError: runtime.FallbackOnRetryable})
			}

			_, err := dispatcher.DispatchStream(context.Background(), runtime.Request{Model: "gpt-4"}, runtime.ExecutionPlan{Steps: steps})
			var providerErr *provider.ProviderError
			if !provider.AsProviderError(err, &providerErr) || providerErr.StatusCode != 429 || providerErr.RetryAfter != "10" {
				t.Fatalf("DispatchStream() error = %#v, want upstream 429 metadata", err)
			}
			records, queryErr := store.RecentRecords(accounting.RecentRecordsQuery{Limit: 10})
			if queryErr != nil {
				t.Fatalf("RecentRecords() error = %v", queryErr)
			}
			if len(records) != 1 || records[0].ErrorKind != string(provider.ErrorKindQuotaExceeded) {
				t.Fatalf("accounting records = %#v, want preferred quota_exceeded", records)
			}
		})
	}
}

func TestDispatchStreamPreferredErrorDoesNotAffectFallbackSuccess(t *testing.T) {
	dispatcher := NewDispatcher()
	primary := &stubProvider{name: "primary", err: &provider.ProviderError{Kind: provider.ErrorKindQuotaExceeded, Err: errors.New("upstream 429"), StatusCode: 429}}
	fallback := &stubProvider{name: "fallback", streamEvents: []runtime.StreamEvent{{Type: runtime.StreamEventMessageEnd}}}

	events, err := dispatcher.DispatchStream(context.Background(), runtime.Request{Model: "gpt-4"}, runtime.ExecutionPlan{Steps: []runtime.ExecutionStep{
		{Type: runtime.StepTypeOutbound, OutboundName: primary.name, OutboundTarget: primary, Model: "gpt-4", OnError: runtime.FallbackOnRetryable},
		{Type: runtime.StepTypeOutbound, OutboundName: fallback.name, OutboundTarget: fallback, Model: "gpt-4", OnError: runtime.FallbackOnRetryable},
	}})
	if err != nil {
		t.Fatalf("DispatchStream() error = %v, want fallback success", err)
	}
	for range events {
	}
	if fallback.req.Model != "gpt-4" || !fallback.req.Stream {
		t.Fatalf("fallback req = %#v, want stream fallback called", fallback.req)
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

func newTokenTracker(names ...string) *quota.Tracker {
	configs := make([]quota.OutboundConfig, 0, len(names))
	for _, name := range names {
		configs = append(configs, quota.OutboundConfig{
			Name: name,
			Windows: []quota.WindowConfig{{
				Name:        "usage",
				Duration:    time.Hour,
				MaxRequests: 100,
				MaxTokens:   10000,
			}},
		})
	}
	return quota.NewTestTracker(configs, time.Now)
}

func quotaUsage(t *testing.T, tracker *quota.Tracker, outbound string) (int, int) {
	t.Helper()
	for _, item := range tracker.Snapshot() {
		if item.Outbound == outbound {
			if len(item.Windows) != 1 {
				t.Fatalf("quota windows for %q = %#v, want one", outbound, item.Windows)
			}
			return item.Windows[0].UsedRequests, item.Windows[0].UsedTokens
		}
	}
	t.Fatalf("quota snapshot has no outbound %q", outbound)
	return 0, 0
}

func TestDispatchRecordsNormalizedQuotaTokens(t *testing.T) {
	tests := []struct {
		name  string
		usage *runtime.Usage
		want  int
	}{
		{name: "total tokens preferred", usage: &runtime.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 21}, want: 21},
		{name: "input output fallback", usage: &runtime.Usage{InputTokens: 10, OutputTokens: 5}, want: 15},
		{name: "nil usage", usage: nil, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracker := newTokenTracker("primary")
			dispatcher := NewDispatcherWithStoreAndQuota(accounting.NewMemoryStore(), tracker)
			p := &stubProvider{name: "primary", resp: runtime.Response{Model: "gpt-4", Usage: tt.usage}}
			plan := runtime.ExecutionPlan{Steps: []runtime.ExecutionStep{{Type: runtime.StepTypeOutbound, OutboundName: "primary", OutboundTarget: p, Model: "gpt-4", OnError: runtime.FallbackAlways}}}

			if _, err := dispatcher.Dispatch(context.Background(), runtime.Request{Model: "gpt-4"}, plan); err != nil {
				t.Fatalf("Dispatch() error = %v", err)
			}
			requests, tokens := quotaUsage(t, tracker, "primary")
			if requests != 1 || tokens != tt.want {
				t.Fatalf("quota usage = requests %d tokens %d, want 1/%d", requests, tokens, tt.want)
			}
		})
	}
}

func TestDispatchFallbackRecordsOnlySuccessfulOutboundQuota(t *testing.T) {
	tracker := newTokenTracker("primary", "fallback")
	dispatcher := NewDispatcherWithStoreAndQuota(accounting.NewMemoryStore(), tracker)
	primary := &stubProvider{name: "primary", err: provider.NewRetryableError(errors.New("temporary"))}
	fallback := &stubProvider{name: "fallback", resp: runtime.Response{Model: "gpt-4", Usage: &runtime.Usage{TotalTokens: 17}}}
	plan := runtime.ExecutionPlan{Steps: []runtime.ExecutionStep{
		{Type: runtime.StepTypeOutbound, OutboundName: "primary", OutboundTarget: primary, Model: "gpt-4", OnError: runtime.FallbackOnRetryable},
		{Type: runtime.StepTypeOutbound, OutboundName: "fallback", OutboundTarget: fallback, Model: "gpt-4", OnError: runtime.FallbackAlways},
	}}

	if _, err := dispatcher.Dispatch(context.Background(), runtime.Request{Model: "gpt-4"}, plan); err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if requests, tokens := quotaUsage(t, tracker, "primary"); requests != 0 || tokens != 0 {
		t.Fatalf("primary quota usage = %d/%d, want 0/0", requests, tokens)
	}
	if requests, tokens := quotaUsage(t, tracker, "fallback"); requests != 1 || tokens != 17 {
		t.Fatalf("fallback quota usage = %d/%d, want 1/17", requests, tokens)
	}
}

func TestDispatchStreamRecordsLatestUsageOnce(t *testing.T) {
	tracker := newTokenTracker("primary")
	store := accounting.NewMemoryStore()
	dispatcher := NewDispatcherWithStoreAndQuota(store, tracker)
	p := &stubProvider{name: "primary", streamEvents: []runtime.StreamEvent{
		{Type: runtime.StreamEventUsage, Usage: &runtime.Usage{InputTokens: 10, OutputTokens: 2, TotalTokens: 12}},
		{Type: runtime.StreamEventUsage, Usage: &runtime.Usage{InputTokens: 10, OutputTokens: 8, TotalTokens: 18}},
		{Type: runtime.StreamEventMessageEnd, Usage: &runtime.Usage{InputTokens: 10, OutputTokens: 11}},
	}}
	plan := runtime.ExecutionPlan{Steps: []runtime.ExecutionStep{{Type: runtime.StepTypeOutbound, OutboundName: "primary", OutboundTarget: p, Model: "gpt-4", OnError: runtime.FallbackAlways}}}

	events, err := dispatcher.DispatchStream(context.Background(), runtime.Request{Model: "gpt-4"}, plan)
	if err != nil {
		t.Fatalf("DispatchStream() error = %v", err)
	}
	for range events {
	}
	if requests, tokens := quotaUsage(t, tracker, "primary"); requests != 1 || tokens != 21 {
		t.Fatalf("quota usage = %d/%d, want 1/21", requests, tokens)
	}
	records, err := store.RecentRecords(accounting.RecentRecordsQuery{Limit: 10})
	if err != nil {
		t.Fatalf("RecentRecords() error = %v", err)
	}
	if len(records) != 1 || records[0].Breakdown.TotalTokens != 21 {
		t.Fatalf("accounting records = %#v, want one record with 21 tokens", records)
	}
}

func TestDispatchStreamFailuresDoNotRecordSuccessfulQuota(t *testing.T) {
	t.Run("stream error", func(t *testing.T) {
		tracker := newTokenTracker("primary")
		dispatcher := NewDispatcherWithStoreAndQuota(accounting.NewMemoryStore(), tracker)
		p := &stubProvider{name: "primary", streamEvents: []runtime.StreamEvent{
			{Type: runtime.StreamEventUsage, Usage: &runtime.Usage{TotalTokens: 13}},
			{Type: runtime.StreamEventError, Err: provider.NewUpstreamServerError(errors.New("bad gateway"))},
		}}
		plan := runtime.ExecutionPlan{Steps: []runtime.ExecutionStep{{Type: runtime.StepTypeOutbound, OutboundName: "primary", OutboundTarget: p, Model: "gpt-4", OnError: runtime.FallbackAlways}}}
		events, err := dispatcher.DispatchStream(context.Background(), runtime.Request{Model: "gpt-4"}, plan)
		if err != nil {
			t.Fatalf("DispatchStream() error = %v", err)
		}
		for range events {
		}
		if requests, tokens := quotaUsage(t, tracker, "primary"); requests != 0 || tokens != 0 {
			t.Fatalf("quota usage = %d/%d, want 0/0", requests, tokens)
		}
	})

	t.Run("context cancellation", func(t *testing.T) {
		tracker := newTokenTracker("primary")
		dispatcher := NewDispatcherWithStoreAndQuota(accounting.NewMemoryStore(), tracker)
		providerEvents := make(chan runtime.StreamEvent)
		p := &controlledStreamProvider{name: "primary", events: providerEvents}
		ctx, cancel := context.WithCancel(context.Background())
		plan := runtime.ExecutionPlan{Steps: []runtime.ExecutionStep{{Type: runtime.StepTypeOutbound, OutboundName: "primary", OutboundTarget: p, Model: "gpt-4", OnError: runtime.FallbackAlways}}}
		events, err := dispatcher.DispatchStream(ctx, runtime.Request{Model: "gpt-4"}, plan)
		if err != nil {
			t.Fatalf("DispatchStream() error = %v", err)
		}
		providerEvents <- runtime.StreamEvent{Type: runtime.StreamEventUsage, Usage: &runtime.Usage{TotalTokens: 13}}
		if event := <-events; event.Type != runtime.StreamEventUsage {
			t.Fatalf("event = %#v, want usage", event)
		}
		cancel()
		for range events {
		}
		if requests, tokens := quotaUsage(t, tracker, "primary"); requests != 0 || tokens != 0 {
			t.Fatalf("quota usage = %d/%d, want 0/0", requests, tokens)
		}
	})
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
