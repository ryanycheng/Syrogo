package provider

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/ryanycheng/Syrogo/internal/runtime"
)

type openAIChatStreamChunk struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Model   string `json:"model"`
	Choices []struct {
		Delta struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		InputTokens      int `json:"input_tokens"`
		OutputTokens     int `json:"output_tokens"`
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage,omitempty"`
}

func decodeOpenAIChatStream(body io.Reader) (<-chan runtime.StreamEvent, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 4096), 1024*1024)

	type toolState struct {
		id        string
		name      string
		arguments strings.Builder
	}

	ch := make(chan runtime.StreamEvent, 16)
	go func() {
		defer close(ch)
		messageID := ""
		model := ""
		role := runtime.MessageRoleAssistant
		finishReason := runtime.FinishReasonStop
		usage := (*runtime.Usage)(nil)
		states := map[int]*toolState{}
		started := false
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, ":") {
				continue
			}
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if payload == "[DONE]" {
				break
			}
			var chunk openAIChatStreamChunk
			if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
				ch <- runtime.StreamEvent{Type: runtime.StreamEventError, ResponseID: messageID, Model: model, MessageRole: role, Err: fmt.Errorf("decode stream chunk: %w", err)}
				return
			}
			if chunk.ID != "" {
				messageID = chunk.ID
			}
			if chunk.Model != "" {
				model = chunk.Model
			}
			if len(chunk.Choices) == 0 {
				if chunk.Usage != nil {
					usage = usageFromOpenAIChatStreamChunk(chunk)
					ch <- runtime.StreamEvent{Type: runtime.StreamEventUsage, ResponseID: messageID, Model: model, MessageRole: role, Usage: usage}
				}
				continue
			}
			choice := chunk.Choices[0]
			if choice.Delta.Role != "" {
				role = runtime.MessageRole(choice.Delta.Role)
			}
			if !started {
				started = true
				ch <- runtime.StreamEvent{Type: runtime.StreamEventMessageStart, ResponseID: messageID, Model: model, MessageRole: role}
			}
			if choice.Delta.Content != "" {
				part := runtime.ContentPart{Type: runtime.ContentPartTypeText, Text: choice.Delta.Content}
				ch <- runtime.StreamEvent{Type: runtime.StreamEventContentDelta, ResponseID: messageID, Model: model, MessageRole: role, Delta: &part}
			}
			for _, call := range choice.Delta.ToolCalls {
				state, ok := states[call.Index]
				if !ok {
					state = &toolState{}
					states[call.Index] = state
				}
				if call.ID != "" {
					state.id = call.ID
				}
				if call.Function.Name != "" {
					state.name = call.Function.Name
				}
				hasArgumentsDelta := call.Function.Arguments != ""
				if hasArgumentsDelta {
					state.arguments.WriteString(call.Function.Arguments)
				}
				tool := runtime.ToolCall{ID: state.id, Name: state.name}
				if hasArgumentsDelta || state.arguments.Len() > 0 {
					tool.Arguments = compactJSONString(state.arguments.String())
				}
				ch <- runtime.StreamEvent{Type: runtime.StreamEventContentDelta, ResponseID: messageID, Model: model, MessageRole: role, ToolCall: &tool, ToolCallIndex: call.Index}
			}
			if chunk.Usage != nil {
				usage = usageFromOpenAIChatStreamChunk(chunk)
				ch <- runtime.StreamEvent{Type: runtime.StreamEventUsage, ResponseID: messageID, Model: model, MessageRole: role, Usage: usage}
			}
			switch choice.FinishReason {
			case "tool_calls", "function_call":
				finishReason = runtime.FinishReasonToolUse
			case "length":
				finishReason = runtime.FinishReasonLength
			case "stop", "":
				if len(states) == 0 {
					finishReason = runtime.FinishReasonStop
				}
			}
		}
		if err := scanner.Err(); err != nil {
			ch <- runtime.StreamEvent{Type: runtime.StreamEventError, ResponseID: messageID, Model: model, MessageRole: role, Err: fmt.Errorf("read stream chunk: %w", err)}
			return
		}
		if !started {
			ch <- runtime.StreamEvent{Type: runtime.StreamEventMessageStart, ResponseID: messageID, Model: model, MessageRole: role}
		}
		ch <- runtime.StreamEvent{Type: runtime.StreamEventMessageEnd, ResponseID: messageID, Model: model, MessageRole: role, FinishReason: finishReason, Usage: usage}
	}()
	return ch, nil
}

func usageFromOpenAIChatStreamChunk(chunk openAIChatStreamChunk) *runtime.Usage {
	if chunk.Usage == nil {
		return nil
	}
	inputTokens := chunk.Usage.InputTokens
	if inputTokens == 0 {
		inputTokens = chunk.Usage.PromptTokens
	}
	outputTokens := chunk.Usage.OutputTokens
	if outputTokens == 0 {
		outputTokens = chunk.Usage.CompletionTokens
	}
	return &runtime.Usage{
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		TotalTokens:  chunk.Usage.TotalTokens,
	}
}

func compactJSONString(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "{}"
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(trimmed)); err == nil {
		return buf.String()
	}
	return trimmed
}
