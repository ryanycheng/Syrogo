package gateway

import (
	"context"
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
	registry   *InboundRegistry
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

type inboundToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type inboundRequest struct {
	Model     string                  `json:"model"`
	System    json.RawMessage         `json:"system"`
	MaxTokens int                     `json:"max_tokens"`
	Messages  []inboundMessage        `json:"messages"`
	Tools     []inboundToolDefinition `json:"tools"`
	Stream    bool                    `json:"stream"`
}

type openAIResponsesRequest struct {
	Model  string          `json:"model"`
	Input  json.RawMessage `json:"input"`
	Stream bool            `json:"stream"`
}

type openAIResponsesInputItem struct {
	Type    string          `json:"type"`
	Role    string          `json:"role,omitempty"`
	Content json.RawMessage `json:"content,omitempty"`
	CallID  string          `json:"call_id,omitempty"`
	Name    string          `json:"name,omitempty"`
	Input   json.RawMessage `json:"input,omitempty"`
	Output  json.RawMessage `json:"output,omitempty"`
}

type openAIResponsesContentPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

func withRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, runtime.ContextKeyRequestID, requestID)
}

func New(r *router.Router, dispatcher *execution.Dispatcher, inbounds []config.InboundSpec, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		router:     r,
		dispatcher: dispatcher,
		inbounds:   append([]config.InboundSpec(nil), inbounds...),
		registry:   DefaultInboundRegistry(),
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

func (h *Handler) handleByCodec(w http.ResponseWriter, r *http.Request, inbound config.InboundSpec, client config.ClientSpec, logger *slog.Logger) bool {
	codec, ok := h.registry.Get(inbound.Protocol)
	if !ok {
		logger.Warn("request rejected", slog.String("reason", "unsupported inbound protocol"))
		writeError(w, http.StatusNotFound, "unsupported inbound protocol")
		return false
	}
	codec.Handle(h, w, r, inbound, client, logger)
	return true
}

func (h *Handler) handleRequest(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		h.handleHealthz(w, r)
		return
	}

	startedAt := time.Now()
	lw := &loggingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
	requestID := startedAt.Format("20060102-150405.000000000")
	r = r.WithContext(withRequestID(r.Context(), requestID))

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
		slog.String("request_id", requestID),
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
		slog.String("inbound", inbound.Name),
		slog.String("protocol", inbound.Protocol),
		slog.String("active_tag", client.Tag),
		slog.String("remote", r.RemoteAddr),
	)
	requestLogger.Info("request started")
	h.handleByCodec(lw, r, inbound, client, requestLogger)
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

func (h *Handler) handleOpenAIResponses(w http.ResponseWriter, r *http.Request, inbound config.InboundSpec, client config.ClientSpec, logger *slog.Logger) {
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

	var req openAIResponsesRequest
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
	if len(req.Input) == 0 {
		logger.Warn("request validation failed",
			slog.String("model", req.Model),
			slog.String("reason", "input is required"),
		)
		writeError(w, http.StatusBadRequest, "input is required")
		return
	}

	internalReq, err := buildRuntimeRequestFromResponses(req)
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
		h.handleOpenAIResponsesStreaming(w, r, internalReq, plan, logger)
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

	writeOpenAIResponsesResponse(w, resp)
}

func (h *Handler) handleAnthropicMessages(w http.ResponseWriter, r *http.Request, inbound config.InboundSpec, client config.ClientSpec, logger *slog.Logger) {
	requestID, _ := r.Context().Value(runtime.ContextKeyRequestID).(string)
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

	debugSnapshot := inboundDebugSnapshot{
		RequestID:  requestID,
		Path:       r.URL.Path,
		Inbound:    inbound.Name,
		ClientTag:  client.Tag,
		ReceivedAt: time.Now().Format(time.RFC3339Nano),
		RawBody:    append(json.RawMessage(nil), body...),
		Parsed:     debugInboundRequest(req),
	}

	internalReq, err := buildRuntimeRequest(req)
	if err != nil {
		debugSnapshot.Error = err.Error()
		if snapErr := writeInboundDebugSnapshot(debugSnapshot); snapErr != nil {
			logger.Warn("anthropic debug snapshot write failed", slog.Any("error", snapErr))
		}
		logger.Warn("request normalize failed",
			slog.String("model", req.Model),
			slog.Any("error", err),
			slog.String("body_preview", previewBody(body)),
		)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	debugSnapshot.Runtime = debugRuntimeRequest(internalReq)

	plan, err := h.planRequest(internalReq, inbound, client)
	if err != nil {
		debugSnapshot.Error = err.Error()
		if snapErr := writeInboundDebugSnapshot(debugSnapshot); snapErr != nil {
			logger.Warn("anthropic debug snapshot write failed", slog.Any("error", snapErr))
		}
		logger.Warn("request routing failed",
			slog.String("model", req.Model),
			slog.Any("error", err),
		)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	debugSnapshot.PlannedModel = plannedModel(plan)
	debugSnapshot.ResolvedTo = append([]string(nil), plan.ResolvedToTags...)
	if snapErr := writeInboundDebugSnapshot(debugSnapshot); snapErr != nil {
		logger.Warn("anthropic debug snapshot write failed", slog.Any("error", snapErr))
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
	system, err := parseInboundSystem(req.System)
	if err != nil {
		return runtime.Request{}, err
	}
	tools, err := parseInboundTools(req.Tools)
	if err != nil {
		return runtime.Request{}, err
	}

	internalReq := runtime.Request{
		Model:     req.Model,
		System:    system,
		MaxTokens: req.MaxTokens,
		Messages:  make([]runtime.Message, 0, len(req.Messages)),
		Tools:     tools,
		Stream:    req.Stream,
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

func parseInboundSystem(raw json.RawMessage) (string, error) {
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
			return "", fmt.Errorf("system content must include at least one text block")
		}
		return strings.Join(parts, "\n"), nil
	}

	return "", fmt.Errorf("unsupported system content")
}

func parseInboundTools(raw []inboundToolDefinition) ([]runtime.ToolDefinition, error) {
	tools := make([]runtime.ToolDefinition, 0, len(raw))
	for _, tool := range raw {
		if tool.Name == "" {
			return nil, fmt.Errorf("tool name is required")
		}
		inputSchema := json.RawMessage(`{}`)
		if len(tool.InputSchema) > 0 {
			var schema any
			if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
				return nil, fmt.Errorf("invalid tool input_schema for %q: %w", tool.Name, err)
			}
			encoded, err := json.Marshal(schema)
			if err != nil {
				return nil, fmt.Errorf("marshal tool input_schema for %q: %w", tool.Name, err)
			}
			inputSchema = encoded
		}
		tools = append(tools, runtime.ToolDefinition{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: inputSchema,
		})
	}
	return tools, nil
}

func buildRuntimeRequestFromResponses(req openAIResponsesRequest) (runtime.Request, error) {
	messages, err := parseOpenAIResponsesInput(req.Input)
	if err != nil {
		return runtime.Request{}, err
	}
	return runtime.Request{Model: req.Model, Messages: messages, Stream: req.Stream}, nil
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

func parseOpenAIResponsesInput(raw json.RawMessage) ([]runtime.Message, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return []runtime.Message{{
			Role:  runtime.MessageRoleUser,
			Parts: []runtime.ContentPart{{Type: runtime.ContentPartTypeText, Text: text}},
		}}, nil
	}

	var items []openAIResponsesInputItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("unsupported responses input")
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("input is required")
	}

	messages := make([]runtime.Message, 0, len(items))
	for _, item := range items {
		switch item.Type {
		case "message":
			parts, err := parseOpenAIResponsesMessageContent(item.Content)
			if err != nil {
				return nil, err
			}
			role := runtime.MessageRole(item.Role)
			if role == "" {
				role = runtime.MessageRoleUser
			}
			messages = append(messages, runtime.Message{Role: role, Parts: parts})
		case "function_call":
			arguments, err := marshalCompactJSON(item.Input)
			if err != nil {
				return nil, fmt.Errorf("invalid function_call input: %w", err)
			}
			messages = append(messages, runtime.Message{
				Role: runtime.MessageRoleAssistant,
				ToolCalls: []runtime.ToolCall{{
					ID:        item.CallID,
					Name:      item.Name,
					Arguments: arguments,
				}},
			})
		case "function_call_output":
			output, err := parseOpenAIResponsesFunctionOutput(item.Output)
			if err != nil {
				return nil, err
			}
			if item.CallID == "" {
				return nil, fmt.Errorf("function_call_output.call_id is required")
			}
			messages = append(messages, runtime.Message{
				Role:       runtime.MessageRoleTool,
				ToolCallID: item.CallID,
				Parts:      []runtime.ContentPart{{Type: runtime.ContentPartTypeText, Text: output}},
			})
		default:
			return nil, fmt.Errorf("unsupported responses input item type %q", item.Type)
		}
	}
	return messages, nil
}

func parseOpenAIResponsesMessageContent(raw json.RawMessage) ([]runtime.ContentPart, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return []runtime.ContentPart{{Type: runtime.ContentPartTypeText, Text: text}}, nil
	}

	var parts []openAIResponsesContentPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, fmt.Errorf("unsupported responses message content")
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("message content must include at least one text part")
	}

	result := make([]runtime.ContentPart, 0, len(parts))
	for _, part := range parts {
		if part.Type != "input_text" && part.Type != "output_text" && part.Type != "text" {
			return nil, fmt.Errorf("unsupported responses content part type %q", part.Type)
		}
		result = append(result, runtime.ContentPart{Type: runtime.ContentPartTypeText, Text: part.Text})
	}
	return result, nil
}

func parseOpenAIResponsesFunctionOutput(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}
	var parts []openAIResponsesContentPart
	if err := json.Unmarshal(raw, &parts); err == nil {
		texts := make([]string, 0, len(parts))
		for _, part := range parts {
			if part.Type == "output_text" || part.Type == "text" || part.Type == "input_text" {
				texts = append(texts, part.Text)
			}
		}
		if len(texts) == 0 {
			return "", fmt.Errorf("function_call_output.output must include at least one text part")
		}
		return strings.Join(texts, "\n"), nil
	}
	return "", fmt.Errorf("unsupported function_call_output.output")
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

func (h *Handler) handleOpenAIResponsesStreaming(w http.ResponseWriter, r *http.Request, req runtime.Request, plan runtime.ExecutionPlan, logger *slog.Logger) {
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

	for _, frame := range openAIResponsesStreamPrelude(plan) {
		if err := writeOpenAIResponsesSSE(w, frame.event, frame.payload); err != nil {
			logger.Error("stream write failed", slog.Any("error", err))
			return
		}
		flusher.Flush()
	}

	for _, frame := range openAIResponsesStreamFrames(events) {
		if err := writeOpenAIResponsesSSE(w, frame.event, frame.payload); err != nil {
			logger.Error("stream write failed", slog.Any("error", err))
			return
		}
		flusher.Flush()
	}
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

	allEvents := make([]runtime.StreamEvent, 0, 8)
	for event := range events {
		allEvents = append(allEvents, event)
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	for _, frame := range anthropicStreamFrames(allEvents) {
		if err := writeAnthropicSSE(w, frame.event, frame.payload); err != nil {
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

func writeOpenAIResponsesResponse(w http.ResponseWriter, resp runtime.Response) {
	output := buildOpenAIResponsesOutput(resp)
	body := map[string]any{
		"id":     resp.ID,
		"object": nonEmpty(resp.Object, "response"),
		"model":  resp.Model,
		"output": output,
	}
	if resp.Usage != nil {
		body["usage"] = map[string]any{
			"input_tokens":  resp.Usage.InputTokens,
			"output_tokens": resp.Usage.OutputTokens,
			"total_tokens":  resp.Usage.TotalTokens,
		}
	}
	writeJSON(w, http.StatusOK, body)
}

func buildOpenAIResponsesOutput(resp runtime.Response) []map[string]any {
	output := make([]map[string]any, 0, 1+len(resp.Message.ToolCalls))
	messageContent := make([]map[string]any, 0, len(resp.Message.Parts))
	for _, part := range resp.Message.Parts {
		if part.Type != runtime.ContentPartTypeText {
			continue
		}
		messageContent = append(messageContent, map[string]any{
			"type": "output_text",
			"text": part.Text,
		})
	}
	if len(messageContent) > 0 {
		output = append(output, map[string]any{
			"type":    "message",
			"role":    string(resp.Message.Role),
			"content": messageContent,
		})
	}
	for _, call := range resp.Message.ToolCalls {
		var input any
		if err := json.Unmarshal([]byte(call.Arguments), &input); err != nil {
			input = map[string]any{}
		}
		output = append(output, map[string]any{
			"type":    "function_call",
			"call_id": call.ID,
			"name":    call.Name,
			"input":   input,
		})
	}
	if len(output) == 0 {
		output = append(output, map[string]any{
			"type": "message",
			"role": string(resp.Message.Role),
			"content": []map[string]any{{
				"type": "output_text",
				"text": "",
			}},
		})
	}
	return output
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

	body := map[string]any{
		"id":          resp.ID,
		"type":        "message",
		"role":        string(resp.Message.Role),
		"model":       resp.Model,
		"content":     content,
		"stop_reason": string(resp.FinishReason),
	}
	if resp.Usage != nil {
		body["usage"] = map[string]any{
			"input_tokens":  resp.Usage.InputTokens,
			"output_tokens": resp.Usage.OutputTokens,
		}
	}
	writeJSON(w, http.StatusOK, body)
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

type openAIResponsesSSEFrame struct {
	event   string
	payload any
}

func openAIResponsesStreamPrelude(plan runtime.ExecutionPlan) []openAIResponsesSSEFrame {
	model := plannedModel(plan)
	return []openAIResponsesSSEFrame{
		{event: "response.created", payload: map[string]any{"type": "response", "response": map[string]any{"model": model, "status": "created"}}},
		{event: "response.in_progress", payload: map[string]any{"type": "response", "response": map[string]any{"model": model, "status": "in_progress"}}},
	}
}

func openAIResponsesStreamFrames(events <-chan runtime.StreamEvent) []openAIResponsesSSEFrame {
	frames := make([]openAIResponsesSSEFrame, 0, 8)
	textItemStarted := false
	toolItemsDone := make(map[string]bool)
	messageItemID := "msg_0"
	messageOutputIndex := 0
	toolOutputIndex := 1
	responseID := ""
	model := ""

	for event := range events {
		if event.ResponseID != "" {
			responseID = event.ResponseID
		}
		if event.Model != "" {
			model = event.Model
		}
		switch event.Type {
		case runtime.StreamEventMessageStart:
			frames = append(frames, openAIResponsesSSEFrame{
				event: "response.output_item.added",
				payload: map[string]any{
					"output_index": messageOutputIndex,
					"item": map[string]any{
						"id":      messageItemID,
						"type":    "message",
						"role":    string(event.MessageRole),
						"content": []map[string]any{},
					},
				},
			})
		case runtime.StreamEventContentDelta:
			if event.Delta != nil {
				if !textItemStarted {
					frames = append(frames, openAIResponsesSSEFrame{
						event: "response.content_part.added",
						payload: map[string]any{
							"output_index":  messageOutputIndex,
							"item_id":       messageItemID,
							"content_index": 0,
							"part":          map[string]any{"type": "output_text", "text": ""},
						},
					})
					textItemStarted = true
				}
				frames = append(frames, openAIResponsesSSEFrame{
					event: "response.output_text.delta",
					payload: map[string]any{
						"output_index":  messageOutputIndex,
						"item_id":       messageItemID,
						"content_index": 0,
						"delta":         event.Delta.Text,
					},
				})
			}
			if event.ToolCall != nil {
				var input any
				if err := json.Unmarshal([]byte(event.ToolCall.Arguments), &input); err != nil {
					input = map[string]any{}
				}
				frames = append(frames, openAIResponsesSSEFrame{
					event: "response.output_item.added",
					payload: map[string]any{
						"output_index": toolOutputIndex + event.ToolCallIndex,
						"item": map[string]any{
							"id":      nonEmpty(event.ToolCall.ID, fmt.Sprintf("call_%d", event.ToolCallIndex)),
							"type":    "function_call",
							"call_id": event.ToolCall.ID,
							"name":    event.ToolCall.Name,
							"input":   input,
						},
					},
				})
				toolItemsDone[event.ToolCall.ID] = false
			}
		case runtime.StreamEventUsage:
			if event.Usage != nil {
				frames = append(frames, openAIResponsesSSEFrame{event: "response.usage", payload: map[string]any{"usage": map[string]any{"input_tokens": event.Usage.InputTokens, "output_tokens": event.Usage.OutputTokens, "total_tokens": event.Usage.TotalTokens}}})
			}
		case runtime.StreamEventMessageEnd:
			if textItemStarted {
				frames = append(frames, openAIResponsesSSEFrame{
					event: "response.content_part.done",
					payload: map[string]any{
						"output_index":  messageOutputIndex,
						"item_id":       messageItemID,
						"content_index": 0,
					},
				})
			}
			frames = append(frames, openAIResponsesSSEFrame{
				event:   "response.output_item.done",
				payload: map[string]any{"output_index": messageOutputIndex, "item_id": messageItemID},
			})
			for toolCallID, done := range toolItemsDone {
				if done {
					continue
				}
				frames = append(frames, openAIResponsesSSEFrame{
					event:   "response.output_item.done",
					payload: map[string]any{"item_id": toolCallID},
				})
				toolItemsDone[toolCallID] = true
			}
			frames = append(frames, openAIResponsesSSEFrame{
				event: "response.completed",
				payload: map[string]any{
					"type": "response",
					"response": map[string]any{
						"id":     responseID,
						"model":  model,
						"status": "completed",
					},
				},
			})
		case runtime.StreamEventError:
			message := "stream error"
			if event.Err != nil {
				message = event.Err.Error()
			}
			frames = append(frames, openAIResponsesSSEFrame{
				event:   "error",
				payload: map[string]any{"message": message},
			})
		}
	}

	return frames
}

type anthropicSSEFrame struct {
	event   string
	payload any
}

func anthropicStreamFrames(events []runtime.StreamEvent) []anthropicSSEFrame {
	frames := make([]anthropicSSEFrame, 0, 8)
	responseID := ""
	model := ""
	messageRole := runtime.MessageRoleAssistant
	finishReason := runtime.FinishReasonStop
	usage := &runtime.Usage{}
	textBlockIndex := -1
	nextBlockIndex := 0
	hasToolUse := false

	for _, event := range events {
		if event.ResponseID != "" {
			responseID = event.ResponseID
		}
		if event.Model != "" {
			model = event.Model
		}
		if event.MessageRole != "" {
			messageRole = event.MessageRole
		}
		if event.Usage != nil {
			usage = event.Usage
		}
		if event.Type == runtime.StreamEventMessageEnd && event.FinishReason != "" {
			finishReason = event.FinishReason
		}
		if event.ToolCall != nil {
			hasToolUse = true
		}
	}
	if hasToolUse && finishReason == runtime.FinishReasonStop {
		finishReason = "tool_use"
	}

	frames = append(frames, anthropicSSEFrame{
		event: "message_start",
		payload: map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":            responseID,
				"type":          "message",
				"role":          string(messageRole),
				"model":         model,
				"content":       []any{},
				"stop_reason":   nil,
				"stop_sequence": nil,
				"usage": map[string]any{
					"input_tokens":  usage.InputTokens,
					"output_tokens": 0,
				},
			},
		},
	})

	for _, event := range events {
		switch event.Type {
		case runtime.StreamEventContentDelta:
			if event.Delta != nil {
				if textBlockIndex == -1 {
					textBlockIndex = nextBlockIndex
					nextBlockIndex++
					frames = append(frames, anthropicSSEFrame{
						event: "content_block_start",
						payload: map[string]any{
							"type":  "content_block_start",
							"index": textBlockIndex,
							"content_block": map[string]any{
								"type": "text",
								"text": "",
							},
						},
					})
				}
				frames = append(frames, anthropicSSEFrame{
					event: "content_block_delta",
					payload: map[string]any{
						"type":  "content_block_delta",
						"index": textBlockIndex,
						"delta": map[string]any{"type": "text_delta", "text": event.Delta.Text},
					},
				})
			}
			if event.ToolCall != nil {
				var input any
				if err := json.Unmarshal([]byte(event.ToolCall.Arguments), &input); err != nil {
					input = map[string]any{}
				}
				toolIndex := nextBlockIndex
				nextBlockIndex++
				frames = append(frames,
					anthropicSSEFrame{
						event: "content_block_start",
						payload: map[string]any{
							"type":  "content_block_start",
							"index": toolIndex,
							"content_block": map[string]any{
								"type":  "tool_use",
								"id":    event.ToolCall.ID,
								"name":  event.ToolCall.Name,
								"input": input,
							},
						},
					},
					anthropicSSEFrame{
						event:   "content_block_stop",
						payload: map[string]any{"type": "content_block_stop", "index": toolIndex},
					},
				)
			}
		case runtime.StreamEventUsage:
			if event.Usage != nil {
				usage = event.Usage
			}
		case runtime.StreamEventError:
			message := "stream error"
			if event.Err != nil {
				message = event.Err.Error()
			}
			return append(frames, anthropicSSEFrame{
				event:   "error",
				payload: map[string]any{"type": "error", "error": map[string]any{"message": message}},
			})
		}
	}

	if textBlockIndex != -1 {
		frames = append(frames, anthropicSSEFrame{
			event:   "content_block_stop",
			payload: map[string]any{"type": "content_block_stop", "index": textBlockIndex},
		})
	}

	messageDelta := map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   string(finishReason),
			"stop_sequence": nil,
		},
		"usage": map[string]any{
			"input_tokens":  usage.InputTokens,
			"output_tokens": usage.OutputTokens,
		},
	}
	frames = append(frames,
		anthropicSSEFrame{event: "message_delta", payload: messageDelta},
		anthropicSSEFrame{event: "message_stop", payload: map[string]any{"type": "message_stop"}},
	)

	return frames
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

func writeAnthropicSSE(w http.ResponseWriter, event string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		return err
	}
	return nil
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

func writeOpenAIResponsesSSE(w http.ResponseWriter, event string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
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

func nonEmpty(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
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
