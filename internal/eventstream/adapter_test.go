package eventstream

import (
	"encoding/json"
	"testing"

	"github.com/ryanycheng/Syrogo/internal/runtime"
)

func TestEventsFromRuntimeResponsePreservesOrderAndFinishReason(t *testing.T) {
	resp := runtime.Response{
		ID:           "msg_123",
		Model:        "gpt-4o-mini",
		FinishReason: runtime.FinishReasonToolUse,
		Usage:        &runtime.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
		Message: runtime.Message{
			Role: runtime.MessageRoleAssistant,
			Parts: []runtime.ContentPart{{
				Type: runtime.ContentPartTypeText,
				Text: "hello",
			}, {
				Type: runtime.ContentPartTypeJSON,
				Data: json.RawMessage(`{"x":1}`),
			}},
			ToolCalls: []runtime.ToolCall{{ID: "call_123", Name: "get_weather", Arguments: `{"city":"shanghai"}`}},
		},
	}

	events := EventsFromRuntimeResponse(resp)
	if len(events) != 13 {
		t.Fatalf("len(events) = %d, want 13", len(events))
	}
	if events[0].Type != EventTypeMessageStart {
		t.Fatalf("events[0].Type = %q, want message_start", events[0].Type)
	}
	if events[1].Type != EventTypeContentBlockStart || events[1].BlockIndex != 0 || events[1].Block.Type != BlockTypeText {
		t.Fatalf("events[1] = %#v, want first text block start", events[1])
	}
	if events[4].Type != EventTypeContentBlockStart || events[4].BlockIndex != 1 || events[4].Block.Type != BlockTypeJSON {
		t.Fatalf("events[4] = %#v, want second json block start", events[4])
	}
	if events[7].Type != EventTypeContentBlockStart || events[7].BlockIndex != 2 || events[7].Block.Type != BlockTypeToolUse {
		t.Fatalf("events[7] = %#v, want tool_use block start", events[7])
	}
	if events[11].Type != EventTypeMessageDelta || events[11].FinishReason != StopReasonToolUse {
		t.Fatalf("events[11] = %#v, want message_delta with tool_use", events[11])
	}
	if events[12].Type != EventTypeMessageStop {
		t.Fatalf("events[12].Type = %q, want message_stop", events[12].Type)
	}
}

func TestSnapshotFromEventsReconstructsBlocksAndUsage(t *testing.T) {
	events := []Event{
		{Type: EventTypeMessageStart, MessageID: "msg_123", Model: "gpt-4o-mini", Role: runtime.MessageRoleAssistant},
		{Type: EventTypeContentBlockStart, MessageID: "msg_123", Model: "gpt-4o-mini", Role: runtime.MessageRoleAssistant, BlockIndex: 0, Block: &ContentBlock{Type: BlockTypeText, Text: "hello"}},
		{Type: EventTypeContentBlockDelta, MessageID: "msg_123", Model: "gpt-4o-mini", Role: runtime.MessageRoleAssistant, BlockIndex: 0, Block: &ContentBlock{Type: BlockTypeText, Text: "hello"}, TextDelta: "hello"},
		{Type: EventTypeContentBlockStop, MessageID: "msg_123", Model: "gpt-4o-mini", Role: runtime.MessageRoleAssistant, BlockIndex: 0},
		{Type: EventTypeContentBlockStart, MessageID: "msg_123", Model: "gpt-4o-mini", Role: runtime.MessageRoleAssistant, BlockIndex: 1, Block: &ContentBlock{Type: BlockTypeToolUse, ToolCall: &ToolCallSnapshot{ID: "call_123", Name: "get_weather", Arguments: `{"city":"shanghai"}`}}},
		{Type: EventTypeContentBlockDelta, MessageID: "msg_123", Model: "gpt-4o-mini", Role: runtime.MessageRoleAssistant, BlockIndex: 1, Block: &ContentBlock{Type: BlockTypeToolUse, ToolCall: &ToolCallSnapshot{ID: "call_123", Name: "get_weather", Arguments: `{"city":"shanghai"}`}}, ToolCall: &ToolCallSnapshot{ID: "call_123", Name: "get_weather", Arguments: `{"city":"shanghai"}`}},
		{Type: EventTypeContentBlockStop, MessageID: "msg_123", Model: "gpt-4o-mini", Role: runtime.MessageRoleAssistant, BlockIndex: 1},
		{Type: EventTypeUsage, MessageID: "msg_123", Model: "gpt-4o-mini", Role: runtime.MessageRoleAssistant, Usage: &runtime.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}},
		{Type: EventTypeMessageDelta, MessageID: "msg_123", Model: "gpt-4o-mini", Role: runtime.MessageRoleAssistant, FinishReason: StopReasonToolUse, Usage: &runtime.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}},
		{Type: EventTypeMessageStop, MessageID: "msg_123", Model: "gpt-4o-mini", Role: runtime.MessageRoleAssistant, FinishReason: StopReasonToolUse},
	}

	snapshot := SnapshotFromEvents(events)
	if snapshot.ID != "msg_123" || snapshot.Model != "gpt-4o-mini" {
		t.Fatalf("snapshot identity = %#v, want msg_123/gpt-4o-mini", snapshot)
	}
	if snapshot.Role != runtime.MessageRoleAssistant {
		t.Fatalf("snapshot.Role = %q, want assistant", snapshot.Role)
	}
	if snapshot.FinishReason != StopReasonToolUse {
		t.Fatalf("snapshot.FinishReason = %q, want tool_use", snapshot.FinishReason)
	}
	if snapshot.Usage == nil || snapshot.Usage.TotalTokens != 15 {
		t.Fatalf("snapshot.Usage = %#v, want total_tokens=15", snapshot.Usage)
	}
	if len(snapshot.Blocks) != 2 {
		t.Fatalf("len(snapshot.Blocks) = %d, want 2", len(snapshot.Blocks))
	}
	if snapshot.Blocks[0].Type != BlockTypeText || snapshot.Blocks[0].Text != "hello" {
		t.Fatalf("snapshot.Blocks[0] = %#v, want text hello", snapshot.Blocks[0])
	}
	if snapshot.Blocks[1].Type != BlockTypeToolUse || snapshot.Blocks[1].ToolCall == nil || snapshot.Blocks[1].ToolCall.ID != "call_123" {
		t.Fatalf("snapshot.Blocks[1] = %#v, want tool_use call_123", snapshot.Blocks[1])
	}
}
