package app

import (
	"fmt"
	"net/http"

	"syrogo/internal/config"
	"syrogo/internal/gateway"
	"syrogo/internal/provider"
	"syrogo/internal/router"
	"syrogo/internal/server"
)

type App struct {
	Server *server.HTTPServer
}

func New(cfg config.Config) (*App, error) {
	providers := make(map[string]provider.Provider, len(cfg.Provider))
	for _, spec := range cfg.Provider {
		switch spec.Type {
		case "mock":
			providers[spec.Name] = provider.NewMock(spec.Name)
		default:
			return nil, fmt.Errorf("unsupported provider type %q", spec.Type)
		}
	}

	r, err := router.New(cfg.Routing, providers)
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	gateway.New(r).Register(mux)

	return &App{
		Server: server.New(cfg.Server.Listen, mux),
	}, nil
}
