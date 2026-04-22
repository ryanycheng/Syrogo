package eventstream

import "github.com/ryanycheng/Syrogo/internal/runtime"

func EventsFromRuntimeResponse(resp runtime.Response) []Event {
	events := make([]Event, 0, 2+len(resp.Message.Parts)+len(resp.Message.ToolCalls)+1)
	events = append(events, Event{
		Type:      EventTypeMessageStart,
		MessageID: resp.ID,
		Model:     resp.Model,
		Role:      resp.Message.Role,
	})

	blockIndex := 0
	for _, part := range resp.Message.Parts {
		block := ContentBlock{}
		switch part.Type {
		case runtime.ContentPartTypeJSON:
			block.Type = BlockTypeJSON
			block.Data = append(block.Data, part.Data...)
		default:
			block.Type = BlockTypeText
			block.Text = part.Text
		}
		delta := Event{Type: EventTypeContentBlockDelta, MessageID: resp.ID, Model: resp.Model, Role: resp.Message.Role, BlockIndex: blockIndex, Block: &block}
		if block.Type == BlockTypeText {
			delta.TextDelta = block.Text
		}
		events = append(events,
			Event{Type: EventTypeContentBlockStart, MessageID: resp.ID, Model: resp.Model, Role: resp.Message.Role, BlockIndex: blockIndex, Block: &block},
			delta,
			Event{Type: EventTypeContentBlockStop, MessageID: resp.ID, Model: resp.Model, Role: resp.Message.Role, BlockIndex: blockIndex},
		)
		blockIndex++
	}

	for _, call := range resp.Message.ToolCalls {
		block := ContentBlock{Type: BlockTypeToolUse, ToolCall: &ToolCallSnapshot{ID: call.ID, Name: call.Name, Arguments: call.Arguments}}
		events = append(events,
			Event{Type: EventTypeContentBlockStart, MessageID: resp.ID, Model: resp.Model, Role: resp.Message.Role, BlockIndex: blockIndex, Block: &block},
			Event{Type: EventTypeContentBlockDelta, MessageID: resp.ID, Model: resp.Model, Role: resp.Message.Role, BlockIndex: blockIndex, Block: &block, ToolCall: block.ToolCall},
			Event{Type: EventTypeContentBlockStop, MessageID: resp.ID, Model: resp.Model, Role: resp.Message.Role, BlockIndex: blockIndex},
		)
		blockIndex++
	}

	if resp.Usage != nil {
		events = append(events, Event{Type: EventTypeUsage, MessageID: resp.ID, Model: resp.Model, Role: resp.Message.Role, Usage: resp.Usage})
	}

	finishReason := StopReasonFromRuntime(resp.FinishReason)
	events = append(events,
		Event{Type: EventTypeMessageDelta, MessageID: resp.ID, Model: resp.Model, Role: resp.Message.Role, FinishReason: finishReason, Usage: resp.Usage},
		Event{Type: EventTypeMessageStop, MessageID: resp.ID, Model: resp.Model, Role: resp.Message.Role, FinishReason: finishReason},
	)
	return events
}

func SnapshotFromEvents(events []Event) ResponseSnapshot {
	snapshot := ResponseSnapshot{}
	blocks := make(map[int]ContentBlock)
	order := make([]int, 0, len(events))
	seen := make(map[int]bool)

	for _, event := range events {
		if snapshot.ID == "" && event.MessageID != "" {
			snapshot.ID = event.MessageID
		}
		if snapshot.Model == "" && event.Model != "" {
			snapshot.Model = event.Model
		}
		if snapshot.Role == "" && event.Role != "" {
			snapshot.Role = event.Role
		}
		if event.Block != nil {
			if !seen[event.BlockIndex] {
				order = append(order, event.BlockIndex)
				seen[event.BlockIndex] = true
			}
			blocks[event.BlockIndex] = cloneBlock(*event.Block)
		}
		if event.Type == EventTypeUsage && event.Usage != nil {
			usage := *event.Usage
			snapshot.Usage = &usage
		}
		if event.Type == EventTypeMessageDelta || event.Type == EventTypeMessageStop {
			if event.FinishReason != "" {
				snapshot.FinishReason = event.FinishReason
			}
			if event.Usage != nil {
				usage := *event.Usage
				snapshot.Usage = &usage
			}
		}
	}

	snapshot.Blocks = make([]ContentBlock, 0, len(order))
	for _, idx := range order {
		snapshot.Blocks = append(snapshot.Blocks, blocks[idx])
	}
	return snapshot
}

func cloneBlock(block ContentBlock) ContentBlock {
	cloned := block
	if block.Data != nil {
		cloned.Data = append([]byte(nil), block.Data...)
	}
	if block.ToolCall != nil {
		call := *block.ToolCall
		cloned.ToolCall = &call
	}
	if block.ToolResult != nil {
		result := *block.ToolResult
		if len(block.ToolResult.Blocks) > 0 {
			result.Blocks = make([]ContentBlock, 0, len(block.ToolResult.Blocks))
			for _, nested := range block.ToolResult.Blocks {
				result.Blocks = append(result.Blocks, cloneBlock(nested))
			}
		}
		cloned.ToolResult = &result
	}
	return cloned
}
