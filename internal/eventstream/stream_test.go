package eventstream

import (
	"testing"

	"syrogo/internal/runtime"
)

func TestEventStreamFromRuntimeEmitsAnthropicFriendlySequence(t *testing.T) {
	input := make(chan runtime.StreamEvent, 4)
	input <- runtime.StreamEvent{Type: runtime.StreamEventMessageStart, ResponseID: "msg_123", Model: "gpt-4o-mini", MessageRole: runtime.MessageRoleAssistant}
	input <- runtime.StreamEvent{Type: runtime.StreamEventContentDelta, ResponseID: "msg_123", Model: "gpt-4o-mini", MessageRole: runtime.MessageRoleAssistant, ToolCall: &runtime.ToolCall{ID: "call_123", Name: "get_weather", Arguments: `{"city":"shanghai"}`}, ToolCallIndex: 0}
	input <- runtime.StreamEvent{Type: runtime.StreamEventUsage, ResponseID: "msg_123", Model: "gpt-4o-mini", MessageRole: runtime.MessageRoleAssistant, Usage: &runtime.Usage{InputTokens: 11, OutputTokens: 7}}
	input <- runtime.StreamEvent{Type: runtime.StreamEventMessageEnd, ResponseID: "msg_123", Model: "gpt-4o-mini", MessageRole: runtime.MessageRoleAssistant, FinishReason: runtime.FinishReasonToolUse}
	close(input)

	var events []Event
	for event := range EventStreamFromRuntime(input) {
		events = append(events, event)
	}

	if len(events) != 7 {
		t.Fatalf("len(events) = %d, want 7", len(events))
	}
	if events[0].Type != EventTypeMessageStart {
		t.Fatalf("events[0].Type = %q, want message_start", events[0].Type)
	}
	if events[1].Type != EventTypeContentBlockStart || events[1].Block == nil || events[1].Block.Type != BlockTypeToolUse {
		t.Fatalf("events[1] = %#v, want tool_use block start", events[1])
	}
	if events[2].Type != EventTypeContentBlockDelta || events[2].ToolCall == nil || events[2].ToolCall.ID != "call_123" {
		t.Fatalf("events[2] = %#v, want tool_use delta", events[2])
	}
	if events[3].Type != EventTypeUsage || events[3].Usage == nil || events[3].Usage.OutputTokens != 7 {
		t.Fatalf("events[3] = %#v, want usage event", events[3])
	}
	if events[4].Type != EventTypeContentBlockStop {
		t.Fatalf("events[4].Type = %q, want content_block_stop", events[4].Type)
	}
	if events[5].Type != EventTypeMessageDelta || events[5].FinishReason != StopReasonToolUse {
		t.Fatalf("events[5] = %#v, want message_delta tool_use", events[5])
	}
	if events[6].Type != EventTypeMessageStop {
		t.Fatalf("events[6].Type = %q, want message_stop", events[6].Type)
	}
}

func TestEventStreamFromRuntimeKeepsSingleToolBlockAcrossDeltas(t *testing.T) {
	input := make(chan runtime.StreamEvent, 5)
	input <- runtime.StreamEvent{Type: runtime.StreamEventMessageStart, ResponseID: "msg_123", Model: "gpt-4o-mini", MessageRole: runtime.MessageRoleAssistant}
	input <- runtime.StreamEvent{Type: runtime.StreamEventContentDelta, ResponseID: "msg_123", Model: "gpt-4o-mini", MessageRole: runtime.MessageRoleAssistant, Delta: &runtime.ContentPart{Type: runtime.ContentPartTypeText, Text: "先读文件。"}}
	input <- runtime.StreamEvent{Type: runtime.StreamEventContentDelta, ResponseID: "msg_123", Model: "gpt-4o-mini", MessageRole: runtime.MessageRoleAssistant, ToolCall: &runtime.ToolCall{ID: "call_123", Name: "Read", Arguments: `{"file_path":"/tmp/a"}`}, ToolCallIndex: 0}
	input <- runtime.StreamEvent{Type: runtime.StreamEventContentDelta, ResponseID: "msg_123", Model: "gpt-4o-mini", MessageRole: runtime.MessageRoleAssistant, ToolCall: &runtime.ToolCall{ID: "call_123", Name: "Read", Arguments: `{"file_path":"/tmp/a","limit":5}`}, ToolCallIndex: 0}
	input <- runtime.StreamEvent{Type: runtime.StreamEventMessageEnd, ResponseID: "msg_123", Model: "gpt-4o-mini", MessageRole: runtime.MessageRoleAssistant, FinishReason: runtime.FinishReasonToolUse}
	close(input)

	var events []Event
	for event := range EventStreamFromRuntime(input) {
		events = append(events, event)
	}

	if len(events) != 10 {
		t.Fatalf("len(events) = %d, want 10", len(events))
	}
	if events[1].Type != EventTypeContentBlockStart || events[1].BlockIndex != 0 || events[1].Block.Type != BlockTypeText {
		t.Fatalf("events[1] = %#v, want text block start", events[1])
	}
	if events[2].Type != EventTypeContentBlockDelta || events[2].BlockIndex != 0 {
		t.Fatalf("events[2] = %#v, want text delta", events[2])
	}
	if events[3].Type != EventTypeContentBlockStop || events[3].BlockIndex != 0 {
		t.Fatalf("events[3] = %#v, want text block stop before tool", events[3])
	}
	if events[4].Type != EventTypeContentBlockStart || events[4].BlockIndex != 1 || events[4].Block.Type != BlockTypeToolUse {
		t.Fatalf("events[4] = %#v, want tool block start", events[4])
	}
	if events[5].Type != EventTypeContentBlockDelta || events[5].BlockIndex != 1 || events[5].ToolCall == nil || events[5].ToolCall.Arguments != `{"file_path":"/tmp/a"}` {
		t.Fatalf("events[5] = %#v, want first tool delta on same block", events[5])
	}
	if events[6].Type != EventTypeContentBlockDelta || events[6].BlockIndex != 1 || events[6].ToolCall == nil || events[6].ToolCall.Arguments != `{"file_path":"/tmp/a","limit":5}` {
		t.Fatalf("events[6] = %#v, want second tool delta on same block", events[6])
	}
	if events[7].Type != EventTypeContentBlockStop || events[7].BlockIndex != 1 {
		t.Fatalf("events[7] = %#v, want final tool block stop", events[7])
	}
	if events[8].Type != EventTypeMessageDelta {
		t.Fatalf("events[8] = %#v, want message_delta", events[8])
	}
}
