package provider

import "syrogo/internal/runtime"

func streamResponse(resp runtime.Response) <-chan runtime.StreamEvent {
	toolCallCount := len(resp.Message.ToolCalls)
	eventCount := 2 + len(resp.Message.Parts) + toolCallCount
	if resp.Usage != nil {
		eventCount++
	}
	ch := make(chan runtime.StreamEvent, eventCount)
	go func() {
		defer close(ch)
		ch <- runtime.StreamEvent{Type: runtime.StreamEventMessageStart, ResponseID: resp.ID, Model: resp.Model, MessageRole: resp.Message.Role}
		if resp.Usage != nil {
			ch <- runtime.StreamEvent{Type: runtime.StreamEventUsage, ResponseID: resp.ID, Model: resp.Model, MessageRole: resp.Message.Role, Usage: resp.Usage}
		}
		for _, part := range resp.Message.Parts {
			partCopy := part
			ch <- runtime.StreamEvent{Type: runtime.StreamEventContentDelta, ResponseID: resp.ID, Model: resp.Model, MessageRole: resp.Message.Role, Delta: &partCopy}
		}
		for i, call := range resp.Message.ToolCalls {
			callCopy := call
			ch <- runtime.StreamEvent{Type: runtime.StreamEventContentDelta, ResponseID: resp.ID, Model: resp.Model, MessageRole: resp.Message.Role, ToolCall: &callCopy, ToolCallIndex: i}
		}
		ch <- runtime.StreamEvent{Type: runtime.StreamEventMessageEnd, ResponseID: resp.ID, Model: resp.Model, MessageRole: resp.Message.Role, FinishReason: resp.FinishReason, Usage: resp.Usage}
	}()
	return ch
}
