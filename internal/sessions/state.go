package sessions

import "strings"

func StatusForHookEvent(event HookEvent, current Status) Status {
	switch event.EventName {
	case "SessionStart":
		return StatusIdle
	case "UserPromptSubmit":
		return StatusRunning
	case "PreToolUse":
		if hookPayloadNeedsPermission(event.Payload) {
			return StatusWaitingPermission
		}
		return StatusToolRunning
	case "PostToolUse":
		return StatusRunning
	case "Notification":
		return statusForNotification(event.Payload, current)
	case "Stop":
		return StatusIdle
	case "SubagentStop":
		return currentOrUnknown(current)
	case "PreCompact":
		return StatusCompacting
	case "SessionEnd":
		return StatusStopped
	default:
		return currentOrUnknown(current)
	}
}

func currentOrUnknown(current Status) Status {
	if current == "" {
		return StatusUnknown
	}
	return current
}

func hookPayloadNeedsPermission(payload map[string]any) bool {
	for _, key := range []string{"permission_required", "requires_permission", "permissionRequired"} {
		if value, ok := payload[key].(bool); ok && value {
			return true
		}
	}
	return stringValueContains(payload, "permission") && stringValueContains(payload, "request")
}

func statusForNotification(payload map[string]any, current Status) Status {
	message := strings.ToLower(joinStringPayloadValues(payload))
	if strings.Contains(message, "permission") || strings.Contains(message, "approval") || strings.Contains(message, "approve") {
		return StatusWaitingPermission
	}
	if strings.Contains(message, "waiting") || strings.Contains(message, "idle") {
		return StatusIdle
	}
	return currentOrUnknown(current)
}

func stringValueContains(payload map[string]any, needle string) bool {
	return strings.Contains(strings.ToLower(joinStringPayloadValues(payload)), needle)
}

func joinStringPayloadValues(payload map[string]any) string {
	var builder strings.Builder
	for _, value := range payload {
		switch typed := value.(type) {
		case string:
			builder.WriteString(typed)
			builder.WriteByte(' ')
		case map[string]any:
			builder.WriteString(joinStringPayloadValues(typed))
		}
	}
	return builder.String()
}
