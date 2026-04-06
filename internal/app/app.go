package app

import (
	"fmt"
	"net/http"

	"syrogo/internal/config"
	"syrogo/internal/execution"
	"syrogo/internal/gateway"
	"syrogo/internal/provider"
	"syrogo/internal/router"
	"syrogo/internal/server"
)

type App struct {
	Server *server.HTTPServer
}

func New(cfg config.Config) (*App, error) {
	outbounds := cfg.OutboundSpecs()
	providers := make(map[string]provider.Provider, len(outbounds))
	for _, spec := range outbounds {
		switch spec.Type {
		case "mock":
			providers[spec.Name] = provider.NewMock(spec.Name)
		case "openai_compatible":
			providers[spec.Name] = provider.NewOpenAICompatible(spec.Name, spec.BaseURL, appendAPIKeys(spec), nil)
		default:
			return nil, fmt.Errorf("unsupported provider type %q", spec.Type)
		}
	}

	r, err := router.New(cfg.Routing, providers)
	if err != nil {
		return nil, err
	}

	dispatcher := execution.NewDispatcher()
	listeners := buildListeners(r, dispatcher, cfg)

	return &App{
		Server: server.NewListeners(listeners),
	}, nil
}

func buildListeners(r *router.Router, dispatcher *execution.Dispatcher, cfg config.Config) []server.Listener {
	if len(cfg.Listeners) == 0 {
		mux := http.NewServeMux()
		gateway.New(r, dispatcher, cfg.PrimaryInbound()).Register(mux)
		return []server.Listener{{Addr: cfg.ListenAddress(), Handler: mux}}
	}

	listeners := make([]server.Listener, 0, len(cfg.Listeners))
	for _, listener := range cfg.Listeners {
		mux := http.NewServeMux()
		gateway.New(r, dispatcher, cfg.InboundByName(listener.Inbound)).Register(mux)
		listeners = append(listeners, server.Listener{
			Addr:    listener.Listen,
			Handler: mux,
		})
	}
	return listeners
}

func appendAPIKeys(spec config.ProviderSpec) []string {
	keys := make([]string, 0, len(spec.APIKeys)+1)
	if spec.APIKey != "" {
		keys = append(keys, spec.APIKey)
	}
	keys = append(keys, spec.APIKeys...)
	return keys
}
