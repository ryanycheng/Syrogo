package eventstream

import "syrogo/internal/runtime"

func EventStreamFromRuntime(events <-chan runtime.StreamEvent) <-chan Event {
	ch := make(chan Event)
	go func() {
		defer close(ch)
		textBlockIndex := -1
		nextBlockIndex := 0
		toolBlockIndexes := map[int]int{}
		for event := range events {
			switch event.Type {
			case runtime.StreamEventMessageStart:
				ch <- Event{
					Type:      EventTypeMessageStart,
					MessageID: event.ResponseID,
					Model:     event.Model,
					Role:      event.MessageRole,
				}
			case runtime.StreamEventContentDelta:
				if event.Delta != nil {
					if textBlockIndex == -1 {
						textBlockIndex = nextBlockIndex
						nextBlockIndex++
						block := ContentBlock{Type: BlockTypeText, Text: ""}
						ch <- Event{Type: EventTypeContentBlockStart, MessageID: event.ResponseID, Model: event.Model, Role: event.MessageRole, BlockIndex: textBlockIndex, Block: &block}
					}
					block := ContentBlock{Type: BlockTypeText, Text: event.Delta.Text}
					ch <- Event{Type: EventTypeContentBlockDelta, MessageID: event.ResponseID, Model: event.Model, Role: event.MessageRole, BlockIndex: textBlockIndex, Block: &block, TextDelta: event.Delta.Text}
				}
				if event.ToolCall != nil {
					if textBlockIndex != -1 {
						ch <- Event{Type: EventTypeContentBlockStop, MessageID: event.ResponseID, Model: event.Model, Role: event.MessageRole, BlockIndex: textBlockIndex}
						textBlockIndex = -1
					}
					toolIndex, ok := toolBlockIndexes[event.ToolCallIndex]
					if !ok {
						toolIndex = nextBlockIndex
						nextBlockIndex++
						toolBlockIndexes[event.ToolCallIndex] = toolIndex
						block := ContentBlock{Type: BlockTypeToolUse, ToolCall: &ToolCallSnapshot{ID: event.ToolCall.ID, Name: event.ToolCall.Name, Arguments: ""}}
						ch <- Event{Type: EventTypeContentBlockStart, MessageID: event.ResponseID, Model: event.Model, Role: event.MessageRole, BlockIndex: toolIndex, Block: &block}
					}
					block := ContentBlock{Type: BlockTypeToolUse, ToolCall: &ToolCallSnapshot{ID: event.ToolCall.ID, Name: event.ToolCall.Name, Arguments: event.ToolCall.Arguments}}
					ch <- Event{Type: EventTypeContentBlockDelta, MessageID: event.ResponseID, Model: event.Model, Role: event.MessageRole, BlockIndex: toolIndex, Block: &block, ToolCall: block.ToolCall}
				}
			case runtime.StreamEventUsage:
				ch <- Event{Type: EventTypeUsage, MessageID: event.ResponseID, Model: event.Model, Role: event.MessageRole, Usage: event.Usage}
			case runtime.StreamEventMessageEnd:
				if textBlockIndex != -1 {
					ch <- Event{Type: EventTypeContentBlockStop, MessageID: event.ResponseID, Model: event.Model, Role: event.MessageRole, BlockIndex: textBlockIndex}
					textBlockIndex = -1
				}
				for _, toolIndex := range toolBlockIndexes {
					ch <- Event{Type: EventTypeContentBlockStop, MessageID: event.ResponseID, Model: event.Model, Role: event.MessageRole, BlockIndex: toolIndex}
				}
				toolBlockIndexes = map[int]int{}
				finishReason := StopReasonFromRuntime(event.FinishReason)
				ch <- Event{Type: EventTypeMessageDelta, MessageID: event.ResponseID, Model: event.Model, Role: event.MessageRole, FinishReason: finishReason, Usage: event.Usage}
				ch <- Event{Type: EventTypeMessageStop, MessageID: event.ResponseID, Model: event.Model, Role: event.MessageRole, FinishReason: finishReason}
			case runtime.StreamEventError:
				ch <- Event{Type: EventTypeError, MessageID: event.ResponseID, Model: event.Model, Role: event.MessageRole, Err: event.Err}
			}
		}
	}()
	return ch
}
