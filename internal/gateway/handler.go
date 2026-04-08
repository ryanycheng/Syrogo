package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"syrogo/internal/config"
	"syrogo/internal/execution"
	"syrogo/internal/provider"
	"syrogo/internal/router"
	"syrogo/internal/runtime"
)

type Handler struct {
	router     *router.Router
	dispatcher *execution.Dispatcher
	inbounds   []config.InboundSpec
	logger     *slog.Logger
}

type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (w *loggingResponseWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *loggingResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

type inboundMessage struct {
	Role       string            `json:"role"`
	Content    json.RawMessage   `json:"content"`
	ToolCalls  []inboundToolCall `json:"tool_calls"`
	ToolCallID string            `json:"tool_call_id"`
}

type inboundToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type inboundRequest struct {
	Model    string           `json:"model"`
	Messages []inboundMessage `json:"messages"`
	Stream   bool             `json:"stream"`
}

func New(r *router.Router, dispatcher *execution.Dispatcher, inbounds []config.InboundSpec, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		router:     r,
		dispatcher: dispatcher,
		inbounds:   append([]config.InboundSpec(nil), inbounds...),
		logger:     logger,
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

	startedAt := time.Now()
	lw := &loggingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}

	inbound, client, ok := h.matchInbound(r)
	if !ok {
		h.logger.Warn("request rejected",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.String("remote", r.RemoteAddr),
			slog.String("reason", "invalid token or inbound"),
		)
		writeError(lw, http.StatusUnauthorized, "invalid token or inbound")
		h.logger.Info("request completed",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", lw.statusCode),
			slog.Duration("duration", time.Since(startedAt)),
		)
		return
	}

	requestLogger := h.logger.With(
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
		slog.String("inbound", inbound.Name),
		slog.String("protocol", inbound.Protocol),
		slog.String("active_tag", client.Tag),
		slog.String("remote", r.RemoteAddr),
	)
	requestLogger.Info("request started")

	switch inbound.Protocol {
	case "openai_chat":
		h.handleOpenAIChatCompletions(lw, r, inbound, client, requestLogger)
	case "anthropic_messages":
		h.handleAnthropicMessages(lw, r, inbound, client, requestLogger)
	default:
		requestLogger.Warn("request rejected", slog.String("reason", "unsupported inbound protocol"))
		writeError(lw, http.StatusNotFound, "unsupported inbound protocol")
	}

	requestLogger.Info("request completed",
		slog.Int("status", lw.statusCode),
		slog.Duration("duration", time.Since(startedAt)),
	)
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

func (h *Handler) handleOpenAIChatCompletions(w http.ResponseWriter, r *http.Request, inbound config.InboundSpec, client config.ClientSpec, logger *slog.Logger) {
	if r.Method != http.MethodPost {
		logger.Warn("request rejected", slog.String("reason", "method not allowed"))
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		logger.Warn("request body read failed", slog.Any("error", err))
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}

	var req inboundRequest
	if err := json.Unmarshal(body, &req); err != nil {
		logger.Warn("request decode failed",
			slog.Any("error", err),
			slog.String("body_preview", previewBody(body)),
		)
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if req.Model == "" {
		logger.Warn("request validation failed", slog.String("reason", "model is required"))
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	if len(req.Messages) == 0 {
		logger.Warn("request validation failed",
			slog.String("model", req.Model),
			slog.String("reason", "messages is required"),
		)
		writeError(w, http.StatusBadRequest, "messages is required")
		return
	}

	internalReq, err := buildRuntimeRequest(req)
	if err != nil {
		logger.Warn("request normalize failed",
			slog.String("model", req.Model),
			slog.Any("error", err),
			slog.String("body_preview", previewBody(body)),
		)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	plan, err := h.planRequest(internalReq, inbound, client)
	if err != nil {
		logger.Warn("request routing failed",
			slog.String("model", req.Model),
			slog.Any("error", err),
		)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	logger.Info("request routed",
		slog.String("requested_model", req.Model),
		slog.String("planned_model", plannedModel(plan)),
		slog.String("matched_rule", plan.MatchedRule),
		slog.String("resolved_to", strings.Join(plan.ResolvedToTags, ",")),
		slog.Bool("stream", req.Stream),
	)

	if req.Stream {
		h.handleOpenAIStreaming(w, r, internalReq, plan, logger)
		return
	}

	resp, err := h.dispatcher.Dispatch(r.Context(), internalReq, plan)
	if err != nil {
		status, message := gatewayError(err)
		logger.Error("request dispatch failed",
			slog.String("model", plannedModel(plan)),
			slog.Int("status", status),
			slog.Any("error", err),
		)
		writeError(w, status, message)
		return
	}

	writeOpenAIChatResponse(w, resp)
}

func (h *Handler) handleAnthropicMessages(w http.ResponseWriter, r *http.Request, inbound config.InboundSpec, client config.ClientSpec, logger *slog.Logger) {
	if r.Method != http.MethodPost {
		logger.Warn("request rejected", slog.String("reason", "method not allowed"))
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		logger.Warn("request body read failed", slog.Any("error", err))
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}

	var req inboundRequest
	if err := json.Unmarshal(body, &req); err != nil {
		logger.Warn("request decode failed",
			slog.Any("error", err),
			slog.String("body_preview", previewBody(body)),
		)
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if req.Model == "" {
		logger.Warn("request validation failed", slog.String("reason", "model is required"))
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	if len(req.Messages) == 0 {
		logger.Warn("request validation failed",
			slog.String("model", req.Model),
			slog.String("reason", "messages is required"),
		)
		writeError(w, http.StatusBadRequest, "messages is required")
		return
	}

	internalReq, err := buildRuntimeRequest(req)
	if err != nil {
		logger.Warn("request normalize failed",
			slog.String("model", req.Model),
			slog.Any("error", err),
			slog.String("body_preview", previewBody(body)),
		)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	plan, err := h.planRequest(internalReq, inbound, client)
	if err != nil {
		logger.Warn("request routing failed",
			slog.String("model", req.Model),
			slog.Any("error", err),
		)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	logger.Info("request routed",
		slog.String("requested_model", req.Model),
		slog.String("planned_model", plannedModel(plan)),
		slog.String("matched_rule", plan.MatchedRule),
		slog.String("resolved_to", strings.Join(plan.ResolvedToTags, ",")),
		slog.Bool("stream", req.Stream),
	)

	if req.Stream {
		h.handleAnthropicStreaming(w, r, internalReq, plan, logger)
		return
	}

	resp, err := h.dispatcher.Dispatch(r.Context(), internalReq, plan)
	if err != nil {
		status, message := gatewayError(err)
		logger.Error("request dispatch failed",
			slog.String("model", plannedModel(plan)),
			slog.Int("status", status),
			slog.Any("error", err),
		)
		writeError(w, status, message)
		return
	}

	writeAnthropicMessageResponse(w, resp)
}

func buildRuntimeRequest(req inboundRequest) (runtime.Request, error) {
	internalReq := runtime.Request{
		Model:    req.Model,
		Messages: make([]runtime.Message, 0, len(req.Messages)),
		Stream:   req.Stream,
	}

	for _, msg := range req.Messages {
		parts, toolCalls, toolCallID, err := parseInboundMessage(msg)
		if err != nil {
			return runtime.Request{}, err
		}
		internalReq.Messages = append(internalReq.Messages, runtime.Message{
			Role:       runtime.MessageRole(msg.Role),
			Parts:      parts,
			ToolCalls:  toolCalls,
			ToolCallID: toolCallID,
		})
	}

	return internalReq, nil
}

func parseInboundMessage(msg inboundMessage) ([]runtime.ContentPart, []runtime.ToolCall, string, error) {
	parts, toolCalls, toolCallID, err := parseInboundContent(msg.Role, msg.Content)
	if err != nil {
		return nil, nil, "", err
	}
	if len(msg.ToolCalls) > 0 {
		toolCalls = make([]runtime.ToolCall, 0, len(msg.ToolCalls))
		for _, call := range msg.ToolCalls {
			if call.Type != "" && call.Type != "function" {
				return nil, nil, "", fmt.Errorf("unsupported tool call type %q", call.Type)
			}
			toolCalls = append(toolCalls, runtime.ToolCall{
				ID:        call.ID,
				Name:      call.Function.Name,
				Arguments: call.Function.Arguments,
			})
		}
	}
	if msg.ToolCallID != "" {
		toolCallID = msg.ToolCallID
	}
	return parts, toolCalls, toolCallID, nil
}

func parseInboundContent(role string, raw json.RawMessage) ([]runtime.ContentPart, []runtime.ToolCall, string, error) {
	if len(raw) == 0 {
		return []runtime.ContentPart{{Type: runtime.ContentPartTypeText, Text: ""}}, nil, "", nil
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return []runtime.ContentPart{{Type: runtime.ContentPartTypeText, Text: text}}, nil, "", nil
	}

	var blocks []struct {
		Type      string          `json:"type"`
		Text      string          `json:"text"`
		ID        string          `json:"id"`
		ToolUseID string          `json:"tool_use_id"`
		Name      string          `json:"name"`
		Input     json.RawMessage `json:"input"`
		Content   json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		parts := make([]runtime.ContentPart, 0, len(blocks))
		toolCalls := make([]runtime.ToolCall, 0, len(blocks))
		toolCallID := ""
		for _, block := range blocks {
			switch block.Type {
			case "text":
				parts = append(parts, runtime.ContentPart{Type: runtime.ContentPartTypeText, Text: block.Text})
			case "tool_use":
				arguments, err := marshalCompactJSON(block.Input)
				if err != nil {
					return nil, nil, "", fmt.Errorf("invalid tool_use input: %w", err)
				}
				toolCalls = append(toolCalls, runtime.ToolCall{ID: block.ID, Name: block.Name, Arguments: arguments})
			case "tool_result":
				toolCallID = block.ToolUseID
				resultText, err := parseToolResultContent(block.Content)
				if err != nil {
					return nil, nil, "", err
				}
				parts = append(parts, runtime.ContentPart{Type: runtime.ContentPartTypeText, Text: resultText})
			}
		}
		if len(parts) == 0 && len(toolCalls) == 0 {
			return nil, nil, "", fmt.Errorf("message content must include at least one text or tool block")
		}
		if role == string(runtime.MessageRoleTool) && toolCallID == "" {
			return nil, nil, "", fmt.Errorf("tool message must include tool_call_id")
		}
		return parts, toolCalls, toolCallID, nil
	}

	return nil, nil, "", fmt.Errorf("unsupported message content")
}

func parseToolResultContent(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		parts := make([]string, 0, len(blocks))
		for _, block := range blocks {
			if block.Type == "text" {
				parts = append(parts, block.Text)
			}
		}
		if len(parts) == 0 {
			return "", fmt.Errorf("tool_result content must include at least one text block")
		}
		return strings.Join(parts, "\n"), nil
	}
	return "", fmt.Errorf("unsupported tool_result content")
}

func marshalCompactJSON(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "{}", nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func (h *Handler) planRequest(req runtime.Request, inbound config.InboundSpec, client config.ClientSpec) (runtime.ExecutionPlan, error) {
	return h.router.Plan(runtime.RouteContext{
		Request:         req,
		InboundName:     inbound.Name,
		InboundProtocol: inbound.Protocol,
		ActiveTag:       client.Tag,
	})
}

func (h *Handler) handleOpenAIStreaming(w http.ResponseWriter, r *http.Request, req runtime.Request, plan runtime.ExecutionPlan, logger *slog.Logger) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		logger.Error("streaming not supported by response writer")
		writeError(w, http.StatusInternalServerError, "streaming is not supported")
		return
	}

	events, err := h.dispatcher.DispatchStream(r.Context(), req, plan)
	if err != nil {
		status, message := gatewayError(err)
		logger.Error("stream dispatch failed",
			slog.String("model", plannedModel(plan)),
			slog.Int("status", status),
			slog.Any("error", err),
		)
		writeError(w, status, message)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	for event := range events {
		if err := writeOpenAISSE(w, openAIStreamChunk(event)); err != nil {
			logger.Error("stream write failed", slog.Any("error", err))
			return
		}
		flusher.Flush()
	}

	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func (h *Handler) handleAnthropicStreaming(w http.ResponseWriter, r *http.Request, req runtime.Request, plan runtime.ExecutionPlan, logger *slog.Logger) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		logger.Error("streaming not supported by response writer")
		writeError(w, http.StatusInternalServerError, "streaming is not supported")
		return
	}

	events, err := h.dispatcher.DispatchStream(r.Context(), req, plan)
	if err != nil {
		status, message := gatewayError(err)
		logger.Error("stream dispatch failed",
			slog.String("model", plannedModel(plan)),
			slog.Int("status", status),
			slog.Any("error", err),
		)
		writeError(w, status, message)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	for event := range events {
		if err := writeSSE(w, event.Type, anthropicStreamChunk(event)); err != nil {
			logger.Error("stream write failed", slog.Any("error", err))
			return
		}
		flusher.Flush()
	}

	_, _ = fmt.Fprint(w, "event: done\ndata: {}\n\n")
	flusher.Flush()
}

func writeOpenAIChatResponse(w http.ResponseWriter, resp runtime.Response) {
	message := map[string]any{
		"role":    string(resp.Message.Role),
		"content": firstTextPart(resp.Message),
	}
	if len(resp.Message.ToolCalls) > 0 {
		toolCalls := make([]map[string]any, 0, len(resp.Message.ToolCalls))
		for _, call := range resp.Message.ToolCalls {
			toolCalls = append(toolCalls, map[string]any{
				"id":   call.ID,
				"type": "function",
				"function": map[string]any{
					"name":      call.Name,
					"arguments": call.Arguments,
				},
			})
		}
		message["tool_calls"] = toolCalls
	}
	if resp.Message.ToolCallID != "" {
		message["tool_call_id"] = resp.Message.ToolCallID
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":     resp.ID,
		"object": resp.Object,
		"model":  resp.Model,
		"choices": []map[string]any{{
			"index":   0,
			"message": message,
		}},
	})
}

func writeAnthropicMessageResponse(w http.ResponseWriter, resp runtime.Response) {
	content := make([]map[string]any, 0, len(resp.Message.Parts)+len(resp.Message.ToolCalls))
	for _, part := range resp.Message.Parts {
		if part.Type != runtime.ContentPartTypeText {
			continue
		}
		content = append(content, map[string]any{
			"type": "text",
			"text": part.Text,
		})
	}
	for _, call := range resp.Message.ToolCalls {
		var input any
		if err := json.Unmarshal([]byte(call.Arguments), &input); err != nil {
			input = map[string]any{}
		}
		content = append(content, map[string]any{
			"type":  "tool_use",
			"id":    call.ID,
			"name":  call.Name,
			"input": input,
		})
	}
	if len(content) == 0 {
		content = append(content, map[string]any{"type": "text", "text": ""})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":          resp.ID,
		"type":        "message",
		"role":        string(resp.Message.Role),
		"model":       resp.Model,
		"content":     content,
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
		delta := map[string]any{}
		if event.Delta != nil {
			delta["content"] = event.Delta.Text
		}
		if event.ToolCall != nil {
			delta["tool_calls"] = []map[string]any{{
				"index": event.ToolCallIndex,
				"id":    event.ToolCall.ID,
				"type":  "function",
				"function": map[string]any{
					"name":      event.ToolCall.Name,
					"arguments": event.ToolCall.Arguments,
				},
			}}
		}
		chunk["choices"] = []map[string]any{{
			"index": 0,
			"delta": delta,
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
		if event.ToolCall != nil {
			var input any
			if err := json.Unmarshal([]byte(event.ToolCall.Arguments), &input); err != nil {
				input = map[string]any{}
			}
			return map[string]any{
				"type":  "content_block_start",
				"index": event.ToolCallIndex,
				"content_block": map[string]any{
					"type":  "tool_use",
					"id":    event.ToolCall.ID,
					"name":  event.ToolCall.Name,
					"input": input,
				},
			}
		}
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

func gatewayError(err error) (int, string) {
	switch provider.NormalizeError(err) {
	case provider.ErrorKindQuotaExceeded:
		return http.StatusBadGateway, "upstream quota exceeded"
	case provider.ErrorKindRetryable:
		return http.StatusBadGateway, "upstream temporarily unavailable"
	case provider.ErrorKindFatal:
		return http.StatusBadGateway, err.Error()
	default:
		return http.StatusInternalServerError, err.Error()
	}
}

func writeOpenAISSE(w http.ResponseWriter, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		return err
	}
	return nil
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

func previewBody(body []byte) string {
	const max = 512
	trimmed := strings.TrimSpace(string(body))
	if len(trimmed) <= max {
		return trimmed
	}
	return trimmed[:max] + "..."
}

func plannedModel(plan runtime.ExecutionPlan) string {
	if len(plan.Steps) == 0 {
		return ""
	}
	return plan.Steps[0].Model
}
