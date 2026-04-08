package app

import (
	"fmt"
	"log/slog"
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
	providers := make(map[string]provider.Provider, len(cfg.Outbounds))
	for _, spec := range cfg.Outbounds {
		switch spec.Protocol {
		case "mock":
			providers[spec.Name] = provider.NewMock(spec.Name)
		case "openai_chat":
			providers[spec.Name] = provider.NewOpenAICompatible(spec.Name, spec.Endpoint, []string{spec.AuthToken}, nil)
		case "openai_responses":
			providers[spec.Name] = provider.NewOpenAIResponsesCompatible(spec.Name, spec.Endpoint, []string{spec.AuthToken}, nil)
		default:
			return nil, fmt.Errorf("unsupported provider protocol %q", spec.Protocol)
		}
	}

	r, err := router.New(cfg.Routing, providers, cfg.Outbounds)
	if err != nil {
		return nil, err
	}

	dispatcher := execution.NewDispatcher()
	listeners := buildListeners(r, dispatcher, cfg, slog.Default())

	return &App{
		Server: server.NewListeners(listeners),
	}, nil
}

func buildListeners(r *router.Router, dispatcher *execution.Dispatcher, cfg config.Config, logger *slog.Logger) []server.Listener {
	listeners := make([]server.Listener, 0, len(cfg.Listeners))
	for _, listener := range cfg.Listeners {
		mux := http.NewServeMux()
		gateway.New(r, dispatcher, cfg.ListenerInbounds(listener), logger).Register(mux)
		listeners = append(listeners, server.Listener{
			Addr:    listener.Listen,
			Handler: mux,
		})
	}
	return listeners
}
