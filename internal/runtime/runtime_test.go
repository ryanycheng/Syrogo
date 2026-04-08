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
		MatchedRule:    "office-route",
		Strategy:       RoutingStrategyFailover,
		ResolvedToTags: []string{"mock-tag"},
		Steps: []ExecutionStep{{
			Type:           StepTypeOutbound,
			OutboundName:   "mock",
			OutboundTarget: p,
			Model:          "gpt-4",
		}},
	}

	if plan.MatchedRule != "office-route" {
		t.Fatalf("MatchedRule = %q, want office-route", plan.MatchedRule)
	}
	if plan.Strategy != RoutingStrategyFailover {
		t.Fatalf("Strategy = %q, want failover", plan.Strategy)
	}
	if len(plan.Steps) != 1 {
		t.Fatalf("len(Steps) = %d, want 1", len(plan.Steps))
	}
	if plan.Steps[0].Type != StepTypeOutbound {
		t.Fatalf("Steps[0].Type = %q, want outbound", plan.Steps[0].Type)
	}
	if plan.Steps[0].OutboundTarget == nil {
		t.Fatal("Steps[0].OutboundTarget = nil, want provider")
	}
}

func TestRouteContextCarriesActiveTag(t *testing.T) {
	ctx := RouteContext{
		InboundName:     "openai-entry",
		InboundProtocol: "openai_chat",
		ActiveTag:       "office",
	}

	if ctx.ActiveTag != "office" {
		t.Fatalf("ActiveTag = %q, want office", ctx.ActiveTag)
	}
	if ctx.InboundProtocol != "openai_chat" {
		t.Fatalf("InboundProtocol = %q, want openai_chat", ctx.InboundProtocol)
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
}

func TestMessageCarriesToolCallingFields(t *testing.T) {
	msg := Message{
		Role: MessageRoleAssistant,
		ToolCalls: []ToolCall{{
			ID:        "call_123",
			Name:      "get_weather",
			Arguments: `{"city":"shanghai"}`,
		}},
	}

	if len(msg.ToolCalls) != 1 {
		t.Fatalf("len(ToolCalls) = %d, want 1", len(msg.ToolCalls))
	}
	if msg.ToolCalls[0].ID != "call_123" {
		t.Fatalf("ToolCalls[0].ID = %q, want call_123", msg.ToolCalls[0].ID)
	}
	if msg.ToolCalls[0].Name != "get_weather" {
		t.Fatalf("ToolCalls[0].Name = %q, want get_weather", msg.ToolCalls[0].Name)
	}
	if msg.ToolCalls[0].Arguments != `{"city":"shanghai"}` {
		t.Fatalf("ToolCalls[0].Arguments = %q, want weather args", msg.ToolCalls[0].Arguments)
	}
}

func TestToolMessageCarriesToolCallID(t *testing.T) {
	msg := Message{
		Role:       MessageRoleTool,
		ToolCallID: "call_123",
		Parts: []ContentPart{{
			Type: ContentPartTypeText,
			Text: "sunny",
		}},
	}

	if msg.Role != MessageRoleTool {
		t.Fatalf("Role = %q, want tool", msg.Role)
	}
	if msg.ToolCallID != "call_123" {
		t.Fatalf("ToolCallID = %q, want call_123", msg.ToolCallID)
	}
	if len(msg.Parts) != 1 || msg.Parts[0].Text != "sunny" {
		t.Fatalf("Parts = %#v, want single text part", msg.Parts)
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
}
