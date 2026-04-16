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
		for _, part := range resp.Message.Parts {
			partCopy := part
			ch <- runtime.StreamEvent{Type: runtime.StreamEventContentDelta, ResponseID: resp.ID, Model: resp.Model, Delta: &partCopy}
		}
		for i, call := range resp.Message.ToolCalls {
			callCopy := call
			ch <- runtime.StreamEvent{Type: runtime.StreamEventContentDelta, ResponseID: resp.ID, Model: resp.Model, ToolCall: &callCopy, ToolCallIndex: i}
		}
		if resp.Usage != nil {
			ch <- runtime.StreamEvent{Type: runtime.StreamEventUsage, ResponseID: resp.ID, Model: resp.Model, Usage: resp.Usage}
		}
		ch <- runtime.StreamEvent{Type: runtime.StreamEventMessageEnd, ResponseID: resp.ID, Model: resp.Model, FinishReason: resp.FinishReason}
	}()
	return ch
}
