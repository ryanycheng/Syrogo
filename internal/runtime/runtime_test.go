package runtime

import (
	"context"
	"testing"
)

type testProvider struct{}

func (p *testProvider) Name() string {
	return "mock"
}

func (p *testProvider) ChatCompletion(_ context.Context, _ Request) (Response, error) {
	return Response{}, nil
}

func (p *testProvider) StreamCompletion(_ context.Context, _ Request) (<-chan StreamEvent, error) {
	ch := make(chan StreamEvent)
	close(ch)
	return ch, nil
}

func TestExecutionPlanCarriesOutboundStep(t *testing.T) {
	p := &testProvider{}
	plan := ExecutionPlan{
		MatchedRoute: "mock",
		Steps: []ExecutionStep{{
			Type:           StepTypeOutbound,
			ProviderName:   "mock",
			ProviderTarget: p,
			Model:          "gpt-4",
		}},
	}

	if plan.MatchedRoute != "mock" {
		t.Fatalf("MatchedRoute = %q, want mock", plan.MatchedRoute)
	}
	if len(plan.Steps) != 1 {
		t.Fatalf("len(Steps) = %d, want 1", len(plan.Steps))
	}
	if plan.Steps[0].Type != StepTypeOutbound {
		t.Fatalf("Steps[0].Type = %q, want outbound", plan.Steps[0].Type)
	}
	if plan.Steps[0].ProviderTarget == nil {
		t.Fatal("Steps[0].ProviderTarget = nil, want provider")
	}
}

func TestRequestCarriesStandardMessageParts(t *testing.T) {
	req := Request{
		Model: "gpt-4",
		Messages: []Message{{
			Role: MessageRoleUser,
			Parts: []ContentPart{{
				Type: ContentPartTypeText,
				Text: "hello",
			}},
		}},
	}

	if req.Model != "gpt-4" {
		t.Fatalf("Model = %q, want gpt-4", req.Model)
	}
	if len(req.Messages) != 1 || req.Messages[0].Role != MessageRoleUser {
		t.Fatalf("Messages = %#v, want single user message", req.Messages)
	}
	if len(req.Messages[0].Parts) != 1 || req.Messages[0].Parts[0].Text != "hello" {
		t.Fatalf("Parts = %#v, want single text part hello", req.Messages[0].Parts)
	}
}

func TestStreamEventCarriesStandardFields(t *testing.T) {
	delta := ContentPart{Type: ContentPartTypeText, Text: "chunk"}
	event := StreamEvent{
		Type:         StreamEventContentDelta,
		ResponseID:   "chatcmpl-1",
		Model:        "gpt-4o-mini",
		MessageRole:  MessageRoleAssistant,
		Delta:        &delta,
		FinishReason: FinishReasonStop,
		Usage: &Usage{
			InputTokens:  10,
			OutputTokens: 3,
			TotalTokens:  13,
		},
	}

	if event.Type != StreamEventContentDelta {
		t.Fatalf("Type = %q, want content_delta", event.Type)
	}
	if event.Delta == nil || event.Delta.Text != "chunk" {
		t.Fatalf("Delta = %#v, want text chunk", event.Delta)
	}
	if event.Usage == nil || event.Usage.TotalTokens != 13 {
		t.Fatalf("Usage = %#v, want total tokens 13", event.Usage)
	}
	if event.FinishReason != FinishReasonStop {
		t.Fatalf("FinishReason = %q, want stop", event.FinishReason)
	}
}
