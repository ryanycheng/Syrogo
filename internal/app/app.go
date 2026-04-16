package app

import (
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
	registry := provider.DefaultFactoryRegistry()
	for _, spec := range cfg.Outbounds {
		instance, err := registry.New(spec.Protocol, spec.Name, spec.Endpoint, spec.AuthToken)
		if err != nil {
			return nil, err
		}
		providers[spec.Name] = instance
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
