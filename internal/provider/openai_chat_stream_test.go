package provider

import (
	"strings"
	"testing"

	"github.com/ryanycheng/Syrogo/internal/runtime"
)

func TestDecodeOpenAIChatStreamParsesToolCallsAndUsage(t *testing.T) {
	body := strings.NewReader(strings.Join([]string{
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","model":"gpt-4o-mini","choices":[{"delta":{"role":"assistant"},"finish_reason":""}]}`,
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","model":"gpt-4o-mini","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_123","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"sh"}}]},"finish_reason":""}]}`,
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","model":"gpt-4o-mini","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"anghai\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"input_tokens":11,"output_tokens":7,"total_tokens":18}}`,
		`data: [DONE]`,
	}, "\n\n"))

	ch, err := decodeOpenAIChatStream(body, runtime.Request{}, false)
	if err != nil {
		t.Fatalf("decodeOpenAIChatStream() error = %v", err)
	}

	var toolEvent *runtime.StreamEvent
	var usageEvent *runtime.StreamEvent
	var endEvent *runtime.StreamEvent
	for event := range ch {
		e := event
		if e.ToolCall != nil {
			toolEvent = &e
		}
		if e.Type == runtime.StreamEventUsage {
			usageEvent = &e
		}
		if e.Type == runtime.StreamEventMessageEnd {
			endEvent = &e
		}
	}
	if toolEvent == nil || toolEvent.ToolCall == nil {
		t.Fatal("toolEvent = nil, want decoded tool call")
	}
	if toolEvent.ToolCall.ID != "call_123" || toolEvent.ToolCall.Name != "get_weather" || toolEvent.ToolCall.Arguments != `{"city":"shanghai"}` {
		t.Fatalf("toolEvent.ToolCall = %#v, want merged tool call", toolEvent.ToolCall)
	}
	if usageEvent == nil || usageEvent.Usage == nil || usageEvent.Usage.TotalTokens != 18 {
		t.Fatalf("usageEvent = %#v, want total_tokens=18", usageEvent)
	}
	if usageEvent.Usage.Source != runtime.UsageSourceProvider {
		t.Fatalf("usageEvent.Usage.Source = %q, want provider", usageEvent.Usage.Source)
	}
	if endEvent == nil || endEvent.FinishReason != runtime.FinishReasonToolUse {
		t.Fatalf("endEvent = %#v, want finish_reason=tool_use", endEvent)
	}
}

func TestDecodeOpenAIChatStreamAcceptsOpenAIUsageFieldNames(t *testing.T) {
	body := strings.NewReader(strings.Join([]string{
		`data: {"id":"chatcmpl-usage-1","object":"chat.completion.chunk","model":"gpt-4o-mini","choices":[{"delta":{"role":"assistant"},"finish_reason":""}]}`,
		`data: {"id":"chatcmpl-usage-1","object":"chat.completion.chunk","model":"gpt-4o-mini","choices":[{"delta":{"content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":18,"completion_tokens":13,"total_tokens":31}}`,
		`data: [DONE]`,
	}, "\n\n"))

	ch, err := decodeOpenAIChatStream(body, runtime.Request{}, false)
	if err != nil {
		t.Fatalf("decodeOpenAIChatStream() error = %v", err)
	}

	var usageEvent *runtime.StreamEvent
	var endEvent *runtime.StreamEvent
	for event := range ch {
		e := event
		if e.Type == runtime.StreamEventUsage {
			usageEvent = &e
		}
		if e.Type == runtime.StreamEventMessageEnd {
			endEvent = &e
		}
	}
	if usageEvent == nil || usageEvent.Usage == nil {
		t.Fatalf("usageEvent = %#v, want usage event", usageEvent)
	}
	if usageEvent.Usage.InputTokens != 18 || usageEvent.Usage.OutputTokens != 13 || usageEvent.Usage.TotalTokens != 31 {
		t.Fatalf("usageEvent.Usage = %#v, want prompt=18 completion=13 total=31", usageEvent.Usage)
	}
	if usageEvent.Usage.Source != runtime.UsageSourceProvider {
		t.Fatalf("usageEvent.Usage.Source = %q, want provider", usageEvent.Usage.Source)
	}
	if endEvent == nil || endEvent.Usage == nil || endEvent.Usage.InputTokens != 18 || endEvent.Usage.OutputTokens != 13 || endEvent.Usage.TotalTokens != 31 {
		t.Fatalf("endEvent = %#v, want final usage carried through", endEvent)
	}
}

func TestDecodeOpenAIChatStreamEstimatesUsageWhenMissing(t *testing.T) {
	body := strings.NewReader(strings.Join([]string{
		`data: {"id":"chatcmpl-estimate-1","object":"chat.completion.chunk","model":"gpt-4o-mini","choices":[{"delta":{"role":"assistant"},"finish_reason":""}]}`,
		`data: {"id":"chatcmpl-estimate-1","object":"chat.completion.chunk","model":"gpt-4o-mini","choices":[{"delta":{"content":"pong"},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
	}, "\n\n"))

	ch, err := decodeOpenAIChatStream(body, runtime.Request{
		Model: "gpt-4o-mini",
		Messages: []runtime.Message{{
			Role: runtime.MessageRoleUser,
			Parts: []runtime.ContentPart{{Type: runtime.ContentPartTypeText, Text: "hello"}},
		}},
	}, true)
	if err != nil {
		t.Fatalf("decodeOpenAIChatStream() error = %v", err)
	}

	var usageEvent *runtime.StreamEvent
	var endEvent *runtime.StreamEvent
	for event := range ch {
		e := event
		if e.Type == runtime.StreamEventUsage {
			usageEvent = &e
		}
		if e.Type == runtime.StreamEventMessageEnd {
			endEvent = &e
		}
	}
	if usageEvent == nil || usageEvent.Usage == nil {
		t.Fatalf("usageEvent = %#v, want estimated usage event", usageEvent)
	}
	if usageEvent.Usage.Source != runtime.UsageSourceEstimated {
		t.Fatalf("usageEvent.Usage.Source = %q, want estimated", usageEvent.Usage.Source)
	}
	if usageEvent.Usage.InputTokens <= 0 || usageEvent.Usage.OutputTokens <= 0 || usageEvent.Usage.TotalTokens != usageEvent.Usage.InputTokens+usageEvent.Usage.OutputTokens {
		t.Fatalf("usageEvent.Usage = %#v, want positive heuristic usage", usageEvent.Usage)
	}
	if endEvent == nil || endEvent.Usage == nil || endEvent.Usage.Source != runtime.UsageSourceEstimated {
		t.Fatalf("endEvent = %#v, want final estimated usage", endEvent)
	}
}

func TestDecodeOpenAIChatStreamDoesNotEmitEmptyToolArgumentsBeforeDelta(t *testing.T) {
	body := strings.NewReader(strings.Join([]string{
		`data: {"id":"chatcmpl-2","object":"chat.completion.chunk","model":"gpt-4o-mini","choices":[{"delta":{"role":"assistant"},"finish_reason":""}]}`,
		`data: {"id":"chatcmpl-2","object":"chat.completion.chunk","model":"gpt-4o-mini","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_456","type":"function","function":{"name":"Read"}}]},"finish_reason":""}]}`,
		`data: {"id":"chatcmpl-2","object":"chat.completion.chunk","model":"gpt-4o-mini","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"file_path\":\"/tmp/a.txt\"}"}}]},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n\n"))

	ch, err := decodeOpenAIChatStream(body, runtime.Request{}, false)
	if err != nil {
		t.Fatalf("decodeOpenAIChatStream() error = %v", err)
	}

	var toolEvents []runtime.StreamEvent
	for event := range ch {
		if event.ToolCall != nil {
			toolEvents = append(toolEvents, event)
		}
	}
	if len(toolEvents) != 2 {
		t.Fatalf("len(toolEvents) = %d, want 2", len(toolEvents))
	}
	if toolEvents[0].ToolCall == nil {
		t.Fatal("toolEvents[0].ToolCall = nil")
	}
	if toolEvents[0].ToolCall.ID != "call_456" || toolEvents[0].ToolCall.Name != "Read" {
		t.Fatalf("toolEvents[0].ToolCall = %#v, want id/name only", toolEvents[0].ToolCall)
	}
	if toolEvents[0].ToolCall.Arguments != "" {
		t.Fatalf("toolEvents[0].ToolCall.Arguments = %q, want empty string", toolEvents[0].ToolCall.Arguments)
	}
	if toolEvents[1].ToolCall == nil || toolEvents[1].ToolCall.Arguments != `{"file_path":"/tmp/a.txt"}` {
		t.Fatalf("toolEvents[1].ToolCall = %#v, want populated arguments", toolEvents[1].ToolCall)
	}
}
