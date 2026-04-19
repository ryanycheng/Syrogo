package gateway

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ryanycheng/Syrogo/internal/runtime"
)

type inboundDebugSnapshot struct {
	RequestID    string          `json:"request_id,omitempty"`
	Path         string          `json:"path"`
	Inbound      string          `json:"inbound"`
	ClientTag    string          `json:"client_tag"`
	ReceivedAt   string          `json:"received_at"`
	RawBody      json.RawMessage `json:"raw_body"`
	Parsed       map[string]any  `json:"parsed,omitempty"`
	Runtime      map[string]any  `json:"runtime,omitempty"`
	PlannedModel string          `json:"planned_model,omitempty"`
	ResolvedTo   []string        `json:"resolved_to,omitempty"`
	Error        string          `json:"error,omitempty"`
}

func traceInboundEnabled() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("SYROGO_TRACE")))
	return value == "1" || value == "full" || value == "inbound"
}

func traceAnthropicStreamEnabled() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("SYROGO_TRACE")))
	return value == "1" || value == "full" || value == "anthropic_stream"
}

func writeInboundDebugSnapshot(snapshot inboundDebugSnapshot) error {
	if !traceInboundEnabled() {
		return nil
	}
	if err := os.MkdirAll(filepath.Join("tmp", "trace"), 0o755); err != nil {
		return err
	}

	payload, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}

	base := snapshot.RequestID
	if base == "" {
		base = time.Now().Format("20060102-150405.000")
	}
	fileName := fmt.Sprintf("%s.inbound.json", base)
	return os.WriteFile(filepath.Join("tmp", "trace", fileName), payload, 0o644)
}

func writeAnthropicStreamTrace(requestID string, payload []byte) error {
	if !traceAnthropicStreamEnabled() {
		return nil
	}
	if err := os.MkdirAll(filepath.Join("tmp", "trace"), 0o755); err != nil {
		return err
	}
	base := requestID
	if base == "" {
		base = time.Now().Format("20060102-150405.000")
	}
	fileName := fmt.Sprintf("%s.gateway-anthropic.stream.txt", base)
	return os.WriteFile(filepath.Join("tmp", "trace", fileName), payload, 0o644)
}

func debugInboundRequest(req inboundRequest) map[string]any {
	messages := make([]map[string]any, 0, len(req.Messages))
	for _, msg := range req.Messages {
		entry := map[string]any{"role": msg.Role}
		if len(msg.Content) > 0 {
			var content any
			if err := json.Unmarshal(msg.Content, &content); err == nil {
				entry["content"] = content
			} else {
				entry["content_raw"] = string(msg.Content)
			}
		}
		if len(msg.ToolCalls) > 0 {
			entry["tool_calls"] = msg.ToolCalls
		}
		if msg.ToolCallID != "" {
			entry["tool_call_id"] = msg.ToolCallID
		}
		messages = append(messages, entry)
	}

	tools := make([]map[string]any, 0, len(req.Tools))
	for _, tool := range req.Tools {
		entry := map[string]any{
			"name":        tool.Name,
			"description": tool.Description,
		}
		if len(tool.InputSchema) > 0 {
			var schema any
			if err := json.Unmarshal(tool.InputSchema, &schema); err == nil {
				entry["input_schema"] = schema
			} else {
				entry["input_schema_raw"] = string(tool.InputSchema)
			}
		}
		tools = append(tools, entry)
	}

	parsed := map[string]any{
		"model":      req.Model,
		"max_tokens": req.MaxTokens,
		"stream":     req.Stream,
		"messages":   messages,
		"tools":      tools,
	}
	if len(req.System) > 0 {
		var system any
		if err := json.Unmarshal(req.System, &system); err == nil {
			parsed["system"] = system
		} else {
			parsed["system_raw"] = string(req.System)
		}
	}
	return parsed
}

func debugRuntimeRequest(req runtime.Request) map[string]any {
	messages := make([]map[string]any, 0, len(req.Messages))
	for _, msg := range req.Messages {
		entry := map[string]any{"role": string(msg.Role)}
		if len(msg.Parts) > 0 {
			parts := make([]map[string]any, 0, len(msg.Parts))
			for _, part := range msg.Parts {
				parts = append(parts, map[string]any{"type": string(part.Type), "text": part.Text})
			}
			entry["parts"] = parts
		}
		if len(msg.ToolCalls) > 0 {
			entry["tool_calls"] = msg.ToolCalls
		}
		if msg.ToolCallID != "" {
			entry["tool_call_id"] = msg.ToolCallID
		}
		messages = append(messages, entry)
	}

	tools := make([]map[string]any, 0, len(req.Tools))
	for _, tool := range req.Tools {
		entry := map[string]any{
			"name":        tool.Name,
			"description": tool.Description,
		}
		if len(tool.InputSchema) > 0 {
			var schema any
			if err := json.Unmarshal(tool.InputSchema, &schema); err == nil {
				entry["input_schema"] = schema
			} else {
				entry["input_schema_raw"] = string(tool.InputSchema)
			}
		}
		tools = append(tools, entry)
	}

	return map[string]any{
		"model":      req.Model,
		"system":     req.System,
		"max_tokens": req.MaxTokens,
		"stream":     req.Stream,
		"messages":   messages,
		"tools":      tools,
	}
}
