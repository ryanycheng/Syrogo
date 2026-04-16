package protocol

const (
	InboundOpenAIChat        = "openai_chat"
	InboundOpenAIResponses   = "openai_responses"
	InboundAnthropicMessages = "anthropic_messages"

	OutboundMock              = "mock"
	OutboundOpenAIChat        = "openai_chat"
	OutboundOpenAIResponses   = "openai_responses"
	OutboundAnthropicMessages = "anthropic_messages"
)

func IsSupportedInbound(name string) bool {
	switch name {
	case InboundOpenAIChat, InboundOpenAIResponses, InboundAnthropicMessages:
		return true
	default:
		return false
	}
}

func IsSupportedOutbound(name string) bool {
	switch name {
	case OutboundMock, OutboundOpenAIChat, OutboundOpenAIResponses, OutboundAnthropicMessages:
		return true
	default:
		return false
	}
}
