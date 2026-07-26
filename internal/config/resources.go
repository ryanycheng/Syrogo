package config

import "fmt"

const RedactedValue = "<redacted>"

type ResolvedClientBinding struct {
	Client  ClientSpec
	Binding ClientBindingSpec
}

type InboundClientBinding struct {
	Inbound InboundSpec
	Binding ClientBindingSpec
}

func RedactedConfig(cfg Config) Config {
	cfg.Admin.Token = redactValue(cfg.Admin.Token)
	cfg.Accounting.AdminToken = redactValue(cfg.Accounting.AdminToken)
	cfg.Clients = append([]ClientSpec(nil), cfg.Clients...)
	for index := range cfg.Clients {
		cfg.Clients[index].Token = redactValue(cfg.Clients[index].Token)
	}
	cfg.Outbounds = append([]OutboundSpec(nil), cfg.Outbounds...)
	for index := range cfg.Outbounds {
		cfg.Outbounds[index].AuthToken = redactValue(cfg.Outbounds[index].AuthToken)
	}
	return cfg
}

func FindClient(cfg Config, name string) (ClientSpec, bool) {
	for _, client := range cfg.Clients {
		if client.Name == name {
			return client, true
		}
	}
	return ClientSpec{}, false
}

func UpsertClient(cfg Config, next ClientSpec) Config {
	for index := range cfg.Clients {
		if cfg.Clients[index].Name == next.Name {
			cfg.Clients[index] = next
			return cfg
		}
	}
	cfg.Clients = append(cfg.Clients, next)
	return cfg
}

func DeleteClient(cfg Config, name string) Config {
	clients := cfg.Clients[:0]
	for _, client := range cfg.Clients {
		if client.Name != name {
			clients = append(clients, client)
		}
	}
	cfg.Clients = clients
	return cfg
}

func FindBinding(cfg Config, inboundName, ref string) (ClientBindingSpec, bool, error) {
	for _, inbound := range cfg.Inbounds {
		if inbound.Name != inboundName {
			continue
		}
		for _, binding := range inbound.Clients {
			if binding.Ref == ref {
				return binding, true, nil
			}
		}
		return ClientBindingSpec{}, false, nil
	}
	return ClientBindingSpec{}, false, fmt.Errorf("inbound %q not found", inboundName)
}

func UpsertBinding(cfg Config, inboundName string, next ClientBindingSpec) (Config, error) {
	for inboundIndex := range cfg.Inbounds {
		if cfg.Inbounds[inboundIndex].Name != inboundName {
			continue
		}
		for bindingIndex := range cfg.Inbounds[inboundIndex].Clients {
			if cfg.Inbounds[inboundIndex].Clients[bindingIndex].Ref == next.Ref {
				cfg.Inbounds[inboundIndex].Clients[bindingIndex] = next
				return cfg, nil
			}
		}
		cfg.Inbounds[inboundIndex].Clients = append(cfg.Inbounds[inboundIndex].Clients, next)
		return cfg, nil
	}
	return cfg, fmt.Errorf("inbound %q not found", inboundName)
}

func DeleteBinding(cfg Config, inboundName, ref string) (Config, error) {
	for inboundIndex := range cfg.Inbounds {
		if cfg.Inbounds[inboundIndex].Name != inboundName {
			continue
		}
		bindings := cfg.Inbounds[inboundIndex].Clients[:0]
		for _, binding := range cfg.Inbounds[inboundIndex].Clients {
			if binding.Ref != ref {
				bindings = append(bindings, binding)
			}
		}
		cfg.Inbounds[inboundIndex].Clients = bindings
		return cfg, nil
	}
	return cfg, fmt.Errorf("inbound %q not found", inboundName)
}

func ClientBindings(cfg Config, clientName string) []InboundClientBinding {
	bindings := make([]InboundClientBinding, 0)
	for _, inbound := range cfg.Inbounds {
		for _, binding := range inbound.Clients {
			if binding.Ref == clientName {
				bindings = append(bindings, InboundClientBinding{Inbound: inbound, Binding: binding})
			}
		}
	}
	return bindings
}

func ResolveClientBinding(cfg Config, binding ClientBindingSpec) (ResolvedClientBinding, bool) {
	client, ok := FindClient(cfg, binding.Ref)
	if !ok {
		return ResolvedClientBinding{}, false
	}
	return ResolvedClientBinding{Client: client, Binding: binding}, true
}

func ResolveInboundClients(cfg Config, inbound InboundSpec) []ResolvedClientBinding {
	resolved := make([]ResolvedClientBinding, 0, len(inbound.Clients))
	for _, binding := range inbound.Clients {
		if item, ok := ResolveClientBinding(cfg, binding); ok {
			resolved = append(resolved, item)
		}
	}
	return resolved
}

func UpsertOutbound(cfg Config, next OutboundSpec) Config {
	for index := range cfg.Outbounds {
		if cfg.Outbounds[index].Name == next.Name {
			cfg.Outbounds[index] = next
			return cfg
		}
	}
	cfg.Outbounds = append(cfg.Outbounds, next)
	return cfg
}

func DeleteOutbound(cfg Config, name string) Config {
	outbounds := cfg.Outbounds[:0]
	for _, outbound := range cfg.Outbounds {
		if outbound.Name != name {
			outbounds = append(outbounds, outbound)
		}
	}
	cfg.Outbounds = outbounds
	return cfg
}

func UpsertRoute(cfg Config, next RoutingRule) Config {
	for index := range cfg.Routing.Rules {
		if cfg.Routing.Rules[index].Name == next.Name {
			cfg.Routing.Rules[index] = next
			return cfg
		}
	}
	cfg.Routing.Rules = append(cfg.Routing.Rules, next)
	return cfg
}

func DeleteRoute(cfg Config, name string) Config {
	rules := cfg.Routing.Rules[:0]
	for _, rule := range cfg.Routing.Rules {
		if rule.Name != name {
			rules = append(rules, rule)
		}
	}
	cfg.Routing.Rules = rules
	return cfg
}

func OutboundEnabled(outbound OutboundSpec) bool {
	return outbound.Enabled == nil || *outbound.Enabled
}

func SetOutboundEnabled(cfg Config, name string, enabled bool) (Config, error) {
	for index := range cfg.Outbounds {
		if cfg.Outbounds[index].Name == name {
			cfg.Outbounds[index].Enabled = &enabled
			return cfg, nil
		}
	}
	return cfg, fmt.Errorf("outbound %q not found", name)
}

func PreserveSecret(next, current string) string {
	if next == "" || next == RedactedValue {
		return current
	}
	return next
}

func redactValue(value string) string {
	if value == "" {
		return ""
	}
	return RedactedValue
}
