package config

import "fmt"

const RedactedValue = "<redacted>"

func RedactedConfig(cfg Config) Config {
	cfg.Admin.Token = redactValue(cfg.Admin.Token)
	cfg.Accounting.AdminToken = redactValue(cfg.Accounting.AdminToken)
	for inboundIndex := range cfg.Inbounds {
		for clientIndex := range cfg.Inbounds[inboundIndex].Clients {
			cfg.Inbounds[inboundIndex].Clients[clientIndex].Token = redactValue(cfg.Inbounds[inboundIndex].Clients[clientIndex].Token)
		}
	}
	for outboundIndex := range cfg.Outbounds {
		cfg.Outbounds[outboundIndex].AuthToken = redactValue(cfg.Outbounds[outboundIndex].AuthToken)
	}
	return cfg
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

func UpsertClient(cfg Config, inboundName string, next ClientSpec) (Config, error) {
	for inboundIndex := range cfg.Inbounds {
		if cfg.Inbounds[inboundIndex].Name != inboundName {
			continue
		}
		for clientIndex := range cfg.Inbounds[inboundIndex].Clients {
			if cfg.Inbounds[inboundIndex].Clients[clientIndex].Name == next.Name {
				cfg.Inbounds[inboundIndex].Clients[clientIndex] = next
				return cfg, nil
			}
		}
		cfg.Inbounds[inboundIndex].Clients = append(cfg.Inbounds[inboundIndex].Clients, next)
		return cfg, nil
	}
	return cfg, fmt.Errorf("inbound %q not found", inboundName)
}

func DeleteClient(cfg Config, inboundName, clientName string) (Config, error) {
	for inboundIndex := range cfg.Inbounds {
		if cfg.Inbounds[inboundIndex].Name != inboundName {
			continue
		}
		clients := cfg.Inbounds[inboundIndex].Clients[:0]
		for _, client := range cfg.Inbounds[inboundIndex].Clients {
			if client.Name != clientName {
				clients = append(clients, client)
			}
		}
		cfg.Inbounds[inboundIndex].Clients = clients
		return cfg, nil
	}
	return cfg, fmt.Errorf("inbound %q not found", inboundName)
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
