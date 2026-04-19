package provider

import (
	"encoding/json"
	"strings"

	"github.com/ryanycheng/Syrogo/internal/runtime"
)

func normalizedToolSchema(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return append(json.RawMessage(nil), raw...)
	}
	encoded, err := json.Marshal(v)
	if err != nil {
		return append(json.RawMessage(nil), raw...)
	}
	return encoded
}

func compactJSONOrEmpty(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	encoded, err := json.Marshal(v)
	if err != nil {
		return string(raw)
	}
	return string(encoded)
}

func joinedTextParts(msg runtime.Message) string {
	parts := make([]string, 0, len(msg.Parts))
	for _, part := range msg.Parts {
		if part.Type == runtime.ContentPartTypeText && part.Text != "" {
			parts = append(parts, part.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func joinedToolResultParts(msg runtime.Message) string {
	parts := make([]string, 0, len(msg.Parts))
	for _, part := range msg.Parts {
		switch part.Type {
		case runtime.ContentPartTypeText:
			if part.Text != "" {
				parts = append(parts, part.Text)
			}
		case runtime.ContentPartTypeJSON:
			if len(part.Data) > 0 {
				parts = append(parts, compactJSONOrEmpty(part.Data))
			}
		}
	}
	return strings.Join(parts, "\n")
}

func firstTextPart(msg runtime.Message) string {
	for _, part := range msg.Parts {
		if part.Type == runtime.ContentPartTypeText {
			return part.Text
		}
	}
	return ""
}
