package gateway

import (
	"encoding/json"
	"net/http"

	"syrogo/internal/config"
	"syrogo/internal/execution"
	"syrogo/internal/router"
	"syrogo/internal/runtime"
)

type Handler struct {
	router      *router.Router
	dispatcher  *execution.Dispatcher
	inboundName string
	inboundType string
	labels      map[string]string
}

func New(r *router.Router, dispatcher *execution.Dispatcher, inbound config.InboundSpec) *Handler {
	return &Handler{
		router:      r,
		dispatcher:  dispatcher,
		inboundName: inbound.Name,
		inboundType: inbound.Type,
		labels:      inbound.Labels,
	}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/healthz", h.handleHealthz)
	mux.HandleFunc("/v1/chat/completions", h.handleChatCompletions)
}

func (h *Handler) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
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

	plan, err := h.router.Plan(runtime.RouteContext{
		Request:       internalReq,
		InboundName:   h.inboundName,
		InboundType:   h.inboundType,
		InboundLabels: h.labels,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	resp, err := h.dispatcher.Dispatch(r.Context(), internalReq, plan)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

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
