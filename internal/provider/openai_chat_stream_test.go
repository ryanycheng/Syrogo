package provider

import (
	"strings"
	"testing"

	"syrogo/internal/runtime"
)

func TestDecodeOpenAIChatStreamParsesToolCallsAndUsage(t *testing.T) {
	body := strings.NewReader(strings.Join([]string{
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","model":"gpt-4o-mini","choices":[{"delta":{"role":"assistant"},"finish_reason":""}]}`,
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","model":"gpt-4o-mini","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_123","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"sh"}}]},"finish_reason":""}]}`,
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","model":"gpt-4o-mini","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"anghai\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"input_tokens":11,"output_tokens":7,"total_tokens":18}}`,
		`data: [DONE]`,
	}, "\n\n"))

	ch, err := decodeOpenAIChatStream(body)
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
	if endEvent == nil || endEvent.FinishReason != runtime.FinishReasonToolUse {
		t.Fatalf("endEvent = %#v, want finish_reason=tool_use", endEvent)
	}
}

func TestDecodeOpenAIChatStreamDoesNotEmitEmptyToolArgumentsBeforeDelta(t *testing.T) {
	body := strings.NewReader(strings.Join([]string{
		`data: {"id":"chatcmpl-2","object":"chat.completion.chunk","model":"gpt-4o-mini","choices":[{"delta":{"role":"assistant"},"finish_reason":""}]}`,
		`data: {"id":"chatcmpl-2","object":"chat.completion.chunk","model":"gpt-4o-mini","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_456","type":"function","function":{"name":"Read"}}]},"finish_reason":""}]}`,
		`data: {"id":"chatcmpl-2","object":"chat.completion.chunk","model":"gpt-4o-mini","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"file_path\":\"/tmp/a.txt\"}"}}]},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n\n"))

	ch, err := decodeOpenAIChatStream(body)
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
