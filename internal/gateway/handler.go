package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"syrogo/internal/config"
	"syrogo/internal/execution"
	"syrogo/internal/router"
	"syrogo/internal/runtime"
)

type Handler struct {
	router     *router.Router
	dispatcher *execution.Dispatcher
	inbounds   []config.InboundSpec
}

func New(r *router.Router, dispatcher *execution.Dispatcher, inbounds []config.InboundSpec) *Handler {
	return &Handler{
		router:     r,
		dispatcher: dispatcher,
		inbounds:   append([]config.InboundSpec(nil), inbounds...),
	}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/healthz", h.handleHealthz)
	mux.HandleFunc("/", h.handleRequest)
}

func (h *Handler) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleRequest(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		h.handleHealthz(w, r)
		return
	}

	inbound, client, ok := h.matchInbound(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid token or inbound")
		return
	}

	switch inbound.Protocol {
	case "openai_chat":
		h.handleOpenAIChatCompletions(w, r, inbound, client)
	case "anthropic_messages":
		h.handleAnthropicMessages(w, r, inbound, client)
	default:
		writeError(w, http.StatusNotFound, "unsupported inbound protocol")
	}
}

func (h *Handler) matchInbound(r *http.Request) (config.InboundSpec, config.ClientSpec, bool) {
	token := bearerToken(r.Header.Get("Authorization"))
	if token == "" {
		return config.InboundSpec{}, config.ClientSpec{}, false
	}

	for _, inbound := range h.inbounds {
		if inbound.Path != r.URL.Path {
			continue
		}
		for _, client := range inbound.Clients {
			if client.Token == token {
				return inbound, client, true
			}
		}
	}
	return config.InboundSpec{}, config.ClientSpec{}, false
}

func bearerToken(header string) string {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(header, prefix))
}

func (h *Handler) handleOpenAIChatCompletions(w http.ResponseWriter, r *http.Request, inbound config.InboundSpec, client config.ClientSpec) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Stream bool `json:"stream"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if req.Model == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	if len(req.Messages) == 0 {
		writeError(w, http.StatusBadRequest, "messages is required")
		return
	}

	internalReq := runtime.Request{
		Model:    req.Model,
		Messages: make([]runtime.Message, 0, len(req.Messages)),
		Stream:   req.Stream,
	}
	for _, msg := range req.Messages {
		internalReq.Messages = append(internalReq.Messages, runtime.Message{
			Role: runtime.MessageRole(msg.Role),
			Parts: []runtime.ContentPart{{
				Type: runtime.ContentPartTypeText,
				Text: msg.Content,
			}},
		})
	}

	plan, err := h.planRequest(internalReq, inbound, client)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if req.Stream {
		h.handleOpenAIStreaming(w, r, internalReq, plan)
		return
	}

	resp, err := h.dispatcher.Dispatch(r.Context(), internalReq, plan)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeOpenAIChatResponse(w, resp)
}

func (h *Handler) handleAnthropicMessages(w http.ResponseWriter, r *http.Request, inbound config.InboundSpec, client config.ClientSpec) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Stream bool `json:"stream"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if req.Model == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	if len(req.Messages) == 0 {
		writeError(w, http.StatusBadRequest, "messages is required")
		return
	}

	internalReq := runtime.Request{
		Model:    req.Model,
		Messages: make([]runtime.Message, 0, len(req.Messages)),
		Stream:   req.Stream,
	}
	for _, msg := range req.Messages {
		internalReq.Messages = append(internalReq.Messages, runtime.Message{
			Role: runtime.MessageRole(msg.Role),
			Parts: []runtime.ContentPart{{
				Type: runtime.ContentPartTypeText,
				Text: msg.Content,
			}},
		})
	}

	plan, err := h.planRequest(internalReq, inbound, client)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if req.Stream {
		h.handleAnthropicStreaming(w, r, internalReq, plan)
		return
	}

	resp, err := h.dispatcher.Dispatch(r.Context(), internalReq, plan)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeAnthropicMessageResponse(w, resp)
}

func (h *Handler) planRequest(req runtime.Request, inbound config.InboundSpec, client config.ClientSpec) (runtime.ExecutionPlan, error) {
	return h.router.Plan(runtime.RouteContext{
		Request:         req,
		InboundName:     inbound.Name,
		InboundProtocol: inbound.Protocol,
		ActiveTag:       client.Tag,
	})
}

func (h *Handler) handleOpenAIStreaming(w http.ResponseWriter, r *http.Request, req runtime.Request, plan runtime.ExecutionPlan) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming is not supported")
		return
	}

	events, err := h.dispatcher.DispatchStream(r.Context(), req, plan)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	for event := range events {
		if err := writeSSE(w, event.Type, openAIStreamChunk(event)); err != nil {
			return
		}
		flusher.Flush()
	}

	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func (h *Handler) handleAnthropicStreaming(w http.ResponseWriter, r *http.Request, req runtime.Request, plan runtime.ExecutionPlan) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming is not supported")
		return
	}

	events, err := h.dispatcher.DispatchStream(r.Context(), req, plan)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	for event := range events {
		if err := writeSSE(w, event.Type, anthropicStreamChunk(event)); err != nil {
			return
		}
		flusher.Flush()
	}

	_, _ = fmt.Fprint(w, "event: done\ndata: {}\n\n")
	flusher.Flush()
}

func writeOpenAIChatResponse(w http.ResponseWriter, resp runtime.Response) {
	writeJSON(w, http.StatusOK, map[string]any{
		"id":     resp.ID,
		"object": resp.Object,
		"model":  resp.Model,
		"choices": []map[string]any{{
			"index": 0,
			"message": map[string]string{
				"role":    string(resp.Message.Role),
				"content": firstTextPart(resp.Message),
			},
		}},
	})
}

func writeAnthropicMessageResponse(w http.ResponseWriter, resp runtime.Response) {
	writeJSON(w, http.StatusOK, map[string]any{
		"id":    resp.ID,
		"type":  "message",
		"role":  string(resp.Message.Role),
		"model": resp.Model,
		"content": []map[string]any{{
			"type": "text",
			"text": firstTextPart(resp.Message),
		}},
		"stop_reason": string(resp.FinishReason),
	})
}

func openAIStreamChunk(event runtime.StreamEvent) any {
	chunk := map[string]any{
		"id":     event.ResponseID,
		"object": "chat.completion.chunk",
		"model":  event.Model,
	}

	switch event.Type {
	case runtime.StreamEventMessageStart:
		chunk["choices"] = []map[string]any{{
			"index": 0,
			"delta": map[string]any{"role": string(event.MessageRole)},
		}}
	case runtime.StreamEventContentDelta:
		content := ""
		if event.Delta != nil {
			content = event.Delta.Text
		}
		chunk["choices"] = []map[string]any{{
			"index": 0,
			"delta": map[string]any{"content": content},
		}}
	case runtime.StreamEventUsage:
		chunk["usage"] = event.Usage
	case runtime.StreamEventMessageEnd:
		chunk["choices"] = []map[string]any{{
			"index":         0,
			"delta":         map[string]any{},
			"finish_reason": string(event.FinishReason),
		}}
	case runtime.StreamEventError:
		message := "stream error"
		if event.Err != nil {
			message = event.Err.Error()
		}
		chunk = map[string]any{"error": message}
	}

	return chunk
}

func anthropicStreamChunk(event runtime.StreamEvent) any {
	switch event.Type {
	case runtime.StreamEventMessageStart:
		return map[string]any{"type": "message_start", "message": map[string]any{"id": event.ResponseID, "role": string(event.MessageRole), "model": event.Model}}
	case runtime.StreamEventContentDelta:
		text := ""
		if event.Delta != nil {
			text = event.Delta.Text
		}
		return map[string]any{"type": "content_block_delta", "delta": map[string]any{"type": "text_delta", "text": text}}
	case runtime.StreamEventMessageEnd:
		return map[string]any{"type": "message_stop", "stop_reason": string(event.FinishReason)}
	case runtime.StreamEventUsage:
		return map[string]any{"type": "message_delta", "usage": event.Usage}
	case runtime.StreamEventError:
		message := "stream error"
		if event.Err != nil {
			message = event.Err.Error()
		}
		return map[string]any{"type": "error", "error": map[string]any{"message": message}}
	default:
		return map[string]any{"type": string(event.Type)}
	}
}

func writeSSE(w http.ResponseWriter, eventType runtime.StreamEventType, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\n", eventType); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		return err
	}
	return nil
}

func firstTextPart(msg runtime.Message) string {
	for _, part := range msg.Parts {
		if part.Type == runtime.ContentPartTypeText {
			return part.Text
		}
	}
	return ""
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
