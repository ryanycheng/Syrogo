package semantic

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestRoleAndSegmentKindConstantsRemainStable(t *testing.T) {
	if RoleSystem != "system" || RoleUser != "user" || RoleAssistant != "assistant" || RoleTool != "tool" {
		t.Fatalf("roles changed: system=%q user=%q assistant=%q tool=%q", RoleSystem, RoleUser, RoleAssistant, RoleTool)
	}
	if SegmentText != "text" || SegmentToolCall != "tool_call" || SegmentToolResult != "tool_result" || SegmentData != "data" {
		t.Fatalf("segment kinds changed: text=%q tool_call=%q tool_result=%q data=%q", SegmentText, SegmentToolCall, SegmentToolResult, SegmentData)
	}
}

func TestRequestCarriesNestedToolResultAndDataPayload(t *testing.T) {
	toolSchema := json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`)
	jsonPayload := json.RawMessage(`{"city":"shanghai","forecast":"sunny"}`)
	arguments := json.RawMessage(`{"city":"shanghai"}`)

	req := Request{
		Model:        "claude-sonnet-4-5",
		Instructions: []Segment{{Kind: SegmentText, Text: "You are a careful assistant."}},
		Turns: []Turn{{
			Role: RoleAssistant,
			Segments: []Segment{{
				Kind: SegmentToolCall,
				ToolCall: &ToolCall{
					ID:        "toolu_123",
					Name:      "get_weather",
					Arguments: arguments,
				},
			}, {
				Kind: SegmentToolResult,
				ToolResult: &ToolResult{
					ToolCallID: "toolu_123",
					IsError:    true,
					Content: []Segment{{
						Kind: SegmentText,
						Text: "weather lookup failed",
					}, {
						Kind: SegmentData,
						Data: &DataPart{
							Format: "json",
							Value:  jsonPayload,
						},
					}},
				},
			}},
		}},
		Tools: []ToolDefinition{{
			Name:        "get_weather",
			Description: "Query weather by city",
			InputSchema: toolSchema,
		}},
		Options: GenerateOptions{MaxTokens: 256, Stream: true},
	}

	if req.Model != "claude-sonnet-4-5" {
		t.Fatalf("req.Model = %q, want claude-sonnet-4-5", req.Model)
	}
	if len(req.Instructions) != 1 || req.Instructions[0].Kind != SegmentText || req.Instructions[0].Text != "You are a careful assistant." {
		t.Fatalf("req.Instructions = %#v, want single text instruction", req.Instructions)
	}
	if len(req.Tools) != 1 || req.Tools[0].Name != "get_weather" || !bytes.Equal(req.Tools[0].InputSchema, toolSchema) {
		t.Fatalf("req.Tools = %#v, want preserved tool schema", req.Tools)
	}
	if req.Options.MaxTokens != 256 || !req.Options.Stream {
		t.Fatalf("req.Options = %#v, want max_tokens and stream preserved", req.Options)
	}

	if len(req.Turns) != 1 || req.Turns[0].Role != RoleAssistant {
		t.Fatalf("req.Turns = %#v, want single assistant turn", req.Turns)
	}
	segments := req.Turns[0].Segments
	if len(segments) != 2 {
		t.Fatalf("len(segments) = %d, want 2", len(segments))
	}
	if segments[0].ToolCall == nil || segments[0].ToolCall.ID != "toolu_123" || !bytes.Equal(segments[0].ToolCall.Arguments, arguments) {
		t.Fatalf("segments[0].ToolCall = %#v, want preserved tool call", segments[0].ToolCall)
	}
	if segments[1].ToolResult == nil {
		t.Fatalf("segments[1].ToolResult is nil")
	}
	if !segments[1].ToolResult.IsError || segments[1].ToolResult.ToolCallID != "toolu_123" {
		t.Fatalf("segments[1].ToolResult = %#v, want preserved tool result metadata", segments[1].ToolResult)
	}
	if len(segments[1].ToolResult.Content) != 2 {
		t.Fatalf("len(tool result content) = %d, want 2", len(segments[1].ToolResult.Content))
	}
	if segments[1].ToolResult.Content[0].Kind != SegmentText || segments[1].ToolResult.Content[0].Text != "weather lookup failed" {
		t.Fatalf("tool result text = %#v, want preserved text segment", segments[1].ToolResult.Content[0])
	}
	if segments[1].ToolResult.Content[1].Data == nil || segments[1].ToolResult.Content[1].Data.Format != "json" || !bytes.Equal(segments[1].ToolResult.Content[1].Data.Value, jsonPayload) {
		t.Fatalf("tool result data = %#v, want preserved json payload", segments[1].ToolResult.Content[1].Data)
	}
}
