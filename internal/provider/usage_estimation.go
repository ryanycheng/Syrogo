package provider

import (
	"unicode/utf8"

	"github.com/ryanycheng/Syrogo/internal/runtime"
)

func estimateUsageHeuristically(req runtime.Request, resp runtime.Response) *runtime.Usage {
	input := estimateRequestTokens(req)
	output := estimateResponseTokens(resp)
	return &runtime.Usage{
		InputTokens:  input,
		OutputTokens: output,
		TotalTokens:  input + output,
		Source:       runtime.UsageSourceEstimated,
	}
}

func estimateRequestTokens(req runtime.Request) int {
	total := estimateTextTokens(req.System)
	for _, msg := range req.Messages {
		total += 2
		total += estimateTextTokens(string(msg.Role))
		for _, part := range msg.Parts {
			switch part.Type {
			case runtime.ContentPartTypeJSON:
				total += estimateBytesTokens(part.Data) + 2
			default:
				total += estimateTextTokens(part.Text)
			}
		}
		if msg.ToolCallID != "" {
			total += estimateTextTokens(msg.ToolCallID)
		}
		for _, call := range msg.ToolCalls {
			total += 3
			total += estimateTextTokens(call.Name)
			total += estimateTextTokens(call.Arguments)
			total += estimateTextTokens(call.Input)
		}
	}
	for _, tool := range req.Tools {
		total += 6
		total += estimateTextTokens(tool.Type)
		total += estimateTextTokens(tool.Name)
		total += estimateTextTokens(tool.Description)
		total += estimateBytesTokens(tool.InputSchema)
		total += estimateBytesTokens(tool.Format)
		total += estimateBytesTokens(tool.Raw)
	}
	total += estimateBytesTokens(req.ToolChoice)
	total += estimateBytesTokens(req.Metadata)
	total += estimateBytesTokens(req.ContextManagement)
	total += estimateTextTokens(req.ThinkingType)
	total += estimateTextTokens(req.OutputEffort)
	if total < 1 {
		return 1
	}
	return total
}

func estimateResponseTokens(resp runtime.Response) int {
	total := 2
	total += estimateTextTokens(string(resp.Message.Role))
	for _, part := range resp.Message.Parts {
		switch part.Type {
		case runtime.ContentPartTypeJSON:
			total += estimateBytesTokens(part.Data) + 2
		default:
			total += estimateTextTokens(part.Text)
		}
	}
	for _, call := range resp.Message.ToolCalls {
		total += 3
		total += estimateTextTokens(call.Name)
		total += estimateTextTokens(call.Arguments)
		total += estimateTextTokens(call.Input)
	}
	if total < 1 {
		return 1
	}
	return total
}

func estimateTextTokens(text string) int {
	if text == "" {
		return 0
	}
	return maxEstimatedTokens(utf8.RuneCountInString(text))
}

func estimateBytesTokens(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	return maxEstimatedTokens(utf8.RuneCount(data))
}

func maxEstimatedTokens(runes int) int {
	if runes <= 0 {
		return 0
	}
	tokens := (runes + 3) / 4
	if tokens < 1 {
		return 1
	}
	return tokens
}
