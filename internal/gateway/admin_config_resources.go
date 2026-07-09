package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"slices"

	"github.com/ryanycheng/Syrogo/internal/config"
	"github.com/ryanycheng/Syrogo/internal/protocol"

	"gopkg.in/yaml.v3"
)

type configResourceMutation func(config.Config) (config.Config, error)

type providerResourceRequest struct {
	Name         string                      `json:"name"`
	Protocol     string                      `json:"protocol"`
	Endpoint     string                      `json:"endpoint"`
	AuthToken    string                      `json:"auth_token"`
	Tag          string                      `json:"tag"`
	Enabled      *bool                       `json:"enabled"`
	Capabilities config.OutboundCapabilities `json:"capabilities"`
	Quota        config.OutboundQuotaConfig  `json:"quota"`
	Proxy        config.OutboundProxyConfig  `json:"proxy"`
}

type setProviderEnabledRequest struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

type deleteProviderRequest struct {
	Name string `json:"name"`
}

type clientResourceRequest struct {
	Inbound string                   `json:"inbound"`
	Name    string                   `json:"name"`
	Token   string                   `json:"token"`
	Tag     string                   `json:"tag"`
	Quota   config.ClientQuotaConfig `json:"quota"`
}

type deleteClientRequest struct {
	Inbound string `json:"inbound"`
	Name    string `json:"name"`
}

type routeResourceRequest struct {
	Name        string            `json:"name"`
	FromTags    []string          `json:"from_tags"`
	ToTags      []string          `json:"to_tags"`
	Strategy    string            `json:"strategy"`
	Weights     map[string]int    `json:"weights"`
	TargetModel string            `json:"target_model"`
	ModelMap    map[string]string `json:"model_map"`
}

type deleteRouteRequest struct {
	Name string `json:"name"`
}

type providerResourceResponse struct {
	Name         string                      `json:"name"`
	Protocol     string                      `json:"protocol"`
	Endpoint     string                      `json:"endpoint"`
	AuthToken    string                      `json:"auth_token"`
	Tag          string                      `json:"tag"`
	Enabled      bool                        `json:"enabled"`
	Capabilities config.OutboundCapabilities `json:"capabilities"`
	Quota        config.OutboundQuotaConfig  `json:"quota"`
	Proxy        config.OutboundProxyConfig  `json:"proxy"`
}

type clientResourceResponse struct {
	Inbound         string                   `json:"inbound"`
	InboundProtocol string                   `json:"inbound_protocol"`
	InboundPath     string                   `json:"inbound_path"`
	Name            string                   `json:"name"`
	Token           string                   `json:"token"`
	Tag             string                   `json:"tag"`
	Quota           config.ClientQuotaConfig `json:"quota"`
}

type routeResourceResponse struct {
	Name        string            `json:"name"`
	FromTags    []string          `json:"from_tags"`
	ToTags      []string          `json:"to_tags"`
	Strategy    string            `json:"strategy"`
	Weights     map[string]int    `json:"weights"`
	TargetModel string            `json:"target_model"`
	ModelMap    map[string]string `json:"model_map"`
}

func (h *Handler) handleAdminSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorizeAdmin(r) {
		writeError(w, http.StatusUnauthorized, "invalid admin token")
		return
	}
	state := h.runtimeState()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"admin":        map[string]any{"enabled": state.Admin.Enabled},
		"config_path":  state.ConfigPath,
		"config_ready": state.ConfigPath != "",
	})
}

func (h *Handler) handleConfigOptions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorizeAdmin(r) {
		writeError(w, http.StatusUnauthorized, "invalid admin token")
		return
	}
	cfg, ok := h.readAdminConfigForResource(w)
	if !ok {
		return
	}
	redacted := config.RedactedConfig(cfg)
	inbounds := make([]map[string]any, 0, len(redacted.Inbounds))
	clientTags := map[string]struct{}{}
	for _, inbound := range redacted.Inbounds {
		clients := make([]map[string]any, 0, len(inbound.Clients))
		for _, client := range inbound.Clients {
			clientTags[client.Tag] = struct{}{}
			clients = append(clients, map[string]any{"name": client.Name, "tag": client.Tag})
		}
		inbounds = append(inbounds, map[string]any{"name": inbound.Name, "protocol": inbound.Protocol, "path": inbound.Path, "clients": clients})
	}
	outbounds := make([]map[string]any, 0, len(redacted.Outbounds))
	outboundTags := map[string]struct{}{}
	for _, outbound := range redacted.Outbounds {
		outboundTags[outbound.Tag] = struct{}{}
		outbounds = append(outbounds, map[string]any{"name": outbound.Name, "protocol": outbound.Protocol, "tag": outbound.Tag})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":               h.runtimeState().ConfigPath,
		"inbounds":           inbounds,
		"outbounds":          outbounds,
		"client_tags":        sortedKeys(clientTags),
		"outbound_tags":      sortedKeys(outboundTags),
		"inbound_protocols":  []string{protocol.InboundOpenAIChat, protocol.InboundOpenAIResponses, protocol.InboundAnthropicMessages},
		"outbound_protocols": []string{protocol.OutboundMock, protocol.OutboundOpenAIChat, protocol.OutboundOpenAIResponses, protocol.OutboundAnthropicMessages},
		"routing_strategies": []string{"failover", "round_robin", "weighted_round_robin"},
	})
}

func (h *Handler) handleConfigProviders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorizeAdmin(r) {
		writeError(w, http.StatusUnauthorized, "invalid admin token")
		return
	}
	cfg, ok := h.readAdminConfigForResource(w)
	if !ok {
		return
	}
	redacted := config.RedactedConfig(cfg)
	items := make([]providerResourceResponse, 0, len(redacted.Outbounds))
	for _, outbound := range redacted.Outbounds {
		items = append(items, providerResourceResponse{
			Name:         outbound.Name,
			Protocol:     outbound.Protocol,
			Endpoint:     outbound.Endpoint,
			AuthToken:    outbound.AuthToken,
			Tag:          outbound.Tag,
			Enabled:      config.OutboundEnabled(outbound),
			Capabilities: outbound.Capabilities,
			Quota:        outbound.Quota,
			Proxy:        outbound.Proxy,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) handleConfigProviderUpsert(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorizeAdmin(r) {
		writeError(w, http.StatusUnauthorized, "invalid admin token")
		return
	}
	var req providerResourceRequest
	if !decodeJSONResourceRequest(w, r, &req) {
		return
	}
	h.writeAdminConfigResource(w, func(cfg config.Config) (config.Config, error) {
		next := config.OutboundSpec{
			Name:         req.Name,
			Protocol:     req.Protocol,
			Endpoint:     req.Endpoint,
			AuthToken:    req.AuthToken,
			Tag:          req.Tag,
			Enabled:      req.Enabled,
			Capabilities: req.Capabilities,
			Quota:        req.Quota,
			Proxy:        req.Proxy,
		}
		for _, outbound := range cfg.Outbounds {
			if outbound.Name == next.Name {
				next.AuthToken = config.PreserveSecret(next.AuthToken, outbound.AuthToken)
				break
			}
		}
		return config.UpsertOutbound(cfg, next), nil
	})
}

func (h *Handler) handleConfigProviderDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorizeAdmin(r) {
		writeError(w, http.StatusUnauthorized, "invalid admin token")
		return
	}
	var req deleteProviderRequest
	if !decodeJSONResourceRequest(w, r, &req) {
		return
	}
	h.writeAdminConfigResource(w, func(cfg config.Config) (config.Config, error) {
		return config.DeleteOutbound(cfg, req.Name), nil
	})
}

func (h *Handler) handleConfigProviderEnabled(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorizeAdmin(r) {
		writeError(w, http.StatusUnauthorized, "invalid admin token")
		return
	}
	var req setProviderEnabledRequest
	if !decodeJSONResourceRequest(w, r, &req) {
		return
	}
	h.writeAdminConfigResource(w, func(cfg config.Config) (config.Config, error) {
		return config.SetOutboundEnabled(cfg, req.Name, req.Enabled)
	})
}

func (h *Handler) handleConfigClients(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorizeAdmin(r) {
		writeError(w, http.StatusUnauthorized, "invalid admin token")
		return
	}
	cfg, ok := h.readAdminConfigForResource(w)
	if !ok {
		return
	}
	redacted := config.RedactedConfig(cfg)
	items := []clientResourceResponse{}
	for _, inbound := range redacted.Inbounds {
		for _, client := range inbound.Clients {
			items = append(items, clientResourceResponse{
				Inbound:         inbound.Name,
				InboundProtocol: inbound.Protocol,
				InboundPath:     inbound.Path,
				Name:            client.Name,
				Token:           client.Token,
				Tag:             client.Tag,
				Quota:           client.Quota,
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) handleConfigClientUpsert(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorizeAdmin(r) {
		writeError(w, http.StatusUnauthorized, "invalid admin token")
		return
	}
	var req clientResourceRequest
	if !decodeJSONResourceRequest(w, r, &req) {
		return
	}
	h.writeAdminConfigResource(w, func(cfg config.Config) (config.Config, error) {
		next := config.ClientSpec{Name: req.Name, Token: req.Token, Tag: req.Tag, Quota: req.Quota}
		for _, inbound := range cfg.Inbounds {
			if inbound.Name != req.Inbound {
				continue
			}
			for _, client := range inbound.Clients {
				if client.Name == next.Name {
					next.Token = config.PreserveSecret(next.Token, client.Token)
					break
				}
			}
			break
		}
		return config.UpsertClient(cfg, req.Inbound, next)
	})
}

func (h *Handler) handleConfigClientDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorizeAdmin(r) {
		writeError(w, http.StatusUnauthorized, "invalid admin token")
		return
	}
	var req deleteClientRequest
	if !decodeJSONResourceRequest(w, r, &req) {
		return
	}
	h.writeAdminConfigResource(w, func(cfg config.Config) (config.Config, error) {
		return config.DeleteClient(cfg, req.Inbound, req.Name)
	})
}

func (h *Handler) handleConfigRoutes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorizeAdmin(r) {
		writeError(w, http.StatusUnauthorized, "invalid admin token")
		return
	}
	cfg, ok := h.readAdminConfigForResource(w)
	if !ok {
		return
	}
	items := make([]routeResourceResponse, 0, len(cfg.Routing.Rules))
	for _, rule := range cfg.Routing.Rules {
		items = append(items, routeResourceResponse{
			Name:        rule.Name,
			FromTags:    rule.FromTags,
			ToTags:      rule.ToTags,
			Strategy:    rule.Strategy,
			Weights:     rule.Weights,
			TargetModel: rule.TargetModel,
			ModelMap:    rule.ModelMap,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) handleConfigRouteUpsert(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorizeAdmin(r) {
		writeError(w, http.StatusUnauthorized, "invalid admin token")
		return
	}
	var req routeResourceRequest
	if !decodeJSONResourceRequest(w, r, &req) {
		return
	}
	h.writeAdminConfigResource(w, func(cfg config.Config) (config.Config, error) {
		return config.UpsertRoute(cfg, config.RoutingRule(req)), nil
	})
}

func (h *Handler) handleConfigRouteDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorizeAdmin(r) {
		writeError(w, http.StatusUnauthorized, "invalid admin token")
		return
	}
	var req deleteRouteRequest
	if !decodeJSONResourceRequest(w, r, &req) {
		return
	}
	h.writeAdminConfigResource(w, func(cfg config.Config) (config.Config, error) {
		return config.DeleteRoute(cfg, req.Name), nil
	})
}

func (h *Handler) readAdminConfigForResource(w http.ResponseWriter) (config.Config, bool) {
	state := h.runtimeState()
	if state.ConfigPath == "" {
		writeError(w, http.StatusServiceUnavailable, "config path is not configured")
		return config.Config{}, false
	}
	content, err := os.ReadFile(state.ConfigPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read config: "+err.Error())
		return config.Config{}, false
	}
	cfg, err := config.ParseBytes(content)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return config.Config{}, false
	}
	return cfg, true
}

func (h *Handler) writeAdminConfigResource(w http.ResponseWriter, mutate configResourceMutation) (config.Config, bool) {
	state := h.runtimeState()
	cfg, ok := h.readAdminConfigForResource(w)
	if !ok {
		return config.Config{}, false
	}
	next, err := mutate(cfg)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return config.Config{}, false
	}
	data, err := yaml.Marshal(next)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "marshal config: "+err.Error())
		return config.Config{}, false
	}
	if _, err := config.ParseBytes(data); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return config.Config{}, false
	}
	if err := config.WriteValidatedFile(state.ConfigPath, data); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return config.Config{}, false
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": state.ConfigPath, "applied": false})
	return next, true
}

func decodeJSONResourceRequest(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "decode request: "+err.Error())
		return false
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		writeError(w, http.StatusBadRequest, "decode request: multiple JSON values")
		return false
	}
	return true
}

func sortedKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		if key != "" {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	return keys
}
