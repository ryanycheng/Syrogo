package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"

	"github.com/ryanycheng/Syrogo/internal/config"
	"github.com/ryanycheng/Syrogo/internal/protocol"
)

type providerResourceRequest struct {
	Name         string                      `json:"name"`
	Protocol     string                      `json:"protocol"`
	Endpoint     string                      `json:"endpoint"`
	AuthToken    string                      `json:"auth_token"`
	Tag          string                      `json:"tag"`
	Enabled      *bool                       `json:"enabled"`
	Models       []config.OutboundModelSpec  `json:"models"`
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
	Name  string                   `json:"name"`
	Token string                   `json:"token"`
	Quota config.ClientQuotaConfig `json:"quota"`
}

type deleteClientRequest struct {
	Name string `json:"name"`
}

type clientBindingRequest struct {
	Inbound string `json:"inbound"`
	Ref     string `json:"ref"`
	Tag     string `json:"tag"`
}

type deleteClientBindingRequest struct {
	Inbound string `json:"inbound"`
	Ref     string `json:"ref"`
}

type bindingTagLastSourceError struct {
	Operation  string
	Client     string
	Inbound    string
	Tag        string
	RouteNames []string
}

func (e *bindingTagLastSourceError) Error() string {
	return fmt.Sprintf("cannot %s tag %q from client binding %q in inbound %q: routing rule(s) %s reference tag %q", e.Operation, e.Tag, e.Client, e.Inbound, strings.Join(e.RouteNames, ", "), e.Tag)
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
	Models       []config.OutboundModelSpec  `json:"models"`
	Capabilities config.OutboundCapabilities `json:"capabilities"`
	Quota        config.OutboundQuotaConfig  `json:"quota"`
	Proxy        config.OutboundProxyConfig  `json:"proxy"`
}

type clientBindingResourceResponse struct {
	Inbound         string `json:"inbound"`
	InboundProtocol string `json:"inbound_protocol"`
	InboundPath     string `json:"inbound_path"`
	Ref             string `json:"ref"`
	Tag             string `json:"tag"`
}

type clientResourceResponse struct {
	Name     string                          `json:"name"`
	Token    string                          `json:"token"`
	Quota    config.ClientQuotaConfig        `json:"quota"`
	Bindings []clientBindingResourceResponse `json:"bindings"`
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
		for _, binding := range inbound.Clients {
			clientTags[binding.Tag] = struct{}{}
			clients = append(clients, map[string]any{"ref": binding.Ref, "tag": binding.Tag})
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
			Models:       outbound.Models,
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
	h.writeAdminConfigMutation(w, r, "provider_upsert_"+req.Name, func(cfg config.Config) (config.Config, error) {
		next := config.OutboundSpec{
			Name:         req.Name,
			Protocol:     req.Protocol,
			Endpoint:     req.Endpoint,
			AuthToken:    req.AuthToken,
			Tag:          req.Tag,
			Enabled:      req.Enabled,
			Models:       req.Models,
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
	h.writeAdminConfigMutation(w, r, "provider_delete_"+req.Name, func(cfg config.Config) (config.Config, error) {
		if err := validateProviderDelete(cfg, req.Name); err != nil {
			return cfg, err
		}
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
	h.writeAdminConfigMutation(w, r, fmt.Sprintf("provider_enabled_%s_%t", req.Name, req.Enabled), func(cfg config.Config) (config.Config, error) {
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
	items := make([]clientResourceResponse, 0, len(redacted.Clients))
	for _, client := range redacted.Clients {
		items = append(items, clientResource(redacted, client))
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
	h.writeAdminConfigMutation(w, r, "client_upsert_"+req.Name, func(cfg config.Config) (config.Config, error) {
		next := config.ClientSpec{Name: req.Name, Token: req.Token, Quota: req.Quota}
		current, found := config.FindClient(cfg, req.Name)
		if found {
			next.Token = config.PreserveSecret(next.Token, current.Token)
		} else if next.Token == "" || next.Token == config.RedactedValue {
			return cfg, fmt.Errorf("cannot create client %q: token is required", req.Name)
		}
		return config.UpsertClient(cfg, next), nil
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
	h.writeAdminConfigMutation(w, r, "client_delete_"+req.Name, func(cfg config.Config) (config.Config, error) {
		if _, found := config.FindClient(cfg, req.Name); !found {
			return cfg, fmt.Errorf("client %q not found", req.Name)
		}
		if bindings := config.ClientBindings(cfg, req.Name); len(bindings) > 0 {
			return cfg, fmt.Errorf("cannot delete client %q: %d binding(s) still reference it", req.Name, len(bindings))
		}
		return config.DeleteClient(cfg, req.Name), nil
	})
}

func (h *Handler) handleConfigClientBindingUpsert(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorizeAdmin(r) {
		writeError(w, http.StatusUnauthorized, "invalid admin token")
		return
	}
	var req clientBindingRequest
	if !decodeJSONResourceRequest(w, r, &req) {
		return
	}
	h.writeAdminConfigMutation(w, r, "client_binding_upsert_"+req.Inbound+"_"+req.Ref, func(cfg config.Config) (config.Config, error) {
		current, found, err := config.FindBinding(cfg, req.Inbound, req.Ref)
		if err != nil {
			return cfg, err
		}
		if found && current.Tag != req.Tag {
			if err := validateClientTagRemoval(cfg, "update", req.Inbound, req.Ref, current.Tag); err != nil {
				return cfg, err
			}
		}
		return config.UpsertBinding(cfg, req.Inbound, config.ClientBindingSpec{Ref: req.Ref, Tag: req.Tag})
	})
}

func (h *Handler) handleConfigClientBindingDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorizeAdmin(r) {
		writeError(w, http.StatusUnauthorized, "invalid admin token")
		return
	}
	var req deleteClientBindingRequest
	if !decodeJSONResourceRequest(w, r, &req) {
		return
	}
	h.writeAdminConfigMutation(w, r, "client_binding_delete_"+req.Inbound+"_"+req.Ref, func(cfg config.Config) (config.Config, error) {
		current, found, err := config.FindBinding(cfg, req.Inbound, req.Ref)
		if err != nil {
			return cfg, err
		}
		if !found {
			return cfg, fmt.Errorf("client binding for %q not found in inbound %q", req.Ref, req.Inbound)
		}
		if err := validateClientTagRemoval(cfg, "delete", req.Inbound, req.Ref, current.Tag); err != nil {
			return cfg, err
		}
		return config.DeleteBinding(cfg, req.Inbound, req.Ref)
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
	h.writeAdminConfigMutation(w, r, "route_upsert_"+req.Name, func(cfg config.Config) (config.Config, error) {
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
	h.writeAdminConfigMutation(w, r, "route_delete_"+req.Name, func(cfg config.Config) (config.Config, error) {
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

func (h *Handler) writeAdminConfigMutation(w http.ResponseWriter, r *http.Request, reason string, mutate ConfigMutation) {
	if h.configReloader == nil {
		writeError(w, http.StatusServiceUnavailable, "config reload is not configured")
		return
	}
	result, err := h.configReloader.MutateConfig(r.Context(), reason, mutate)
	if err != nil {
		response := map[string]any{
			"ok":                false,
			"saved":             result.Saved,
			"applied":           result.Applied,
			"restart_required":  result.RestartRequired,
			"reason":            result.Reason,
			"history_id":        result.HistoryID,
			"quota_state_reset": result.QuotaStateReset,
			"error":             err.Error(),
		}
		var bindingErr *bindingTagLastSourceError
		if errors.As(err, &bindingErr) {
			response["error_code"] = "binding_tag_last_source"
			response["details"] = map[string]any{
				"operation":   bindingErr.Operation,
				"client":      bindingErr.Client,
				"inbound":     bindingErr.Inbound,
				"tag":         bindingErr.Tag,
				"route_names": bindingErr.RouteNames,
			}
		}
		writeJSON(w, http.StatusBadRequest, response)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func validateClientTagRemoval(cfg config.Config, operation, inboundName, ref, tag string) error {
	for _, inbound := range cfg.Inbounds {
		for _, binding := range inbound.Clients {
			if inbound.Name == inboundName && binding.Ref == ref {
				continue
			}
			if binding.Tag == tag {
				return nil
			}
		}
	}
	routeNames := make([]string, 0)
	for _, rule := range cfg.Routing.Rules {
		if slices.Contains(rule.FromTags, tag) {
			routeNames = append(routeNames, rule.Name)
		}
	}
	if len(routeNames) == 0 {
		return nil
	}
	slices.Sort(routeNames)
	return &bindingTagLastSourceError{Operation: operation, Client: ref, Inbound: inboundName, Tag: tag, RouteNames: routeNames}
}

func validateProviderDelete(cfg config.Config, name string) error {
	var tag string
	found := false
	for _, outbound := range cfg.Outbounds {
		if outbound.Name == name {
			tag = outbound.Tag
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("outbound %q not found", name)
	}
	for _, rule := range cfg.Routing.Rules {
		if slices.Contains(rule.ToTags, tag) {
			return fmt.Errorf("cannot delete outbound %q: routing rule %q references tag %q", name, rule.Name, tag)
		}
		if _, ok := rule.Weights[tag]; ok {
			return fmt.Errorf("cannot delete outbound %q: routing rule %q weights reference tag %q", name, rule.Name, tag)
		}
	}
	for _, price := range cfg.Accounting.Pricing {
		if price.Provider == name {
			return fmt.Errorf("cannot delete outbound %q: accounting pricing references provider %q", name, price.Provider)
		}
	}
	return nil
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
