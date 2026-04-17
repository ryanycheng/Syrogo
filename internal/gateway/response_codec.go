package gateway

import (
	"encoding/json"

	"syrogo/internal/runtime"
)

func decodeJSONPart(part runtime.ContentPart) any {
	if len(part.Data) == 0 {
		return map[string]any{}
	}
	var value any
	if err := json.Unmarshal(part.Data, &value); err != nil {
		return string(part.Data)
	}
	return value
}
