package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/ryanycheng/Syrogo/internal/accounting"
	"github.com/ryanycheng/Syrogo/internal/config"
	"github.com/ryanycheng/Syrogo/internal/execution"
	"github.com/ryanycheng/Syrogo/internal/gateway"
	"github.com/ryanycheng/Syrogo/internal/provider"
	"github.com/ryanycheng/Syrogo/internal/quota"
	"github.com/ryanycheng/Syrogo/internal/router"
	"github.com/ryanycheng/Syrogo/internal/server"
)

type App struct {
	Server          *server.HTTPServer
	accountingStore accounting.Store
	dispatcher      *execution.Dispatcher
}

func New(cfg config.Config) (*App, error) {
	providers := make(map[string]provider.Provider, len(cfg.Outbounds))
	registry := provider.DefaultFactoryRegistry()
	for _, spec := range cfg.Outbounds {
		instance, err := registry.New(spec.Protocol, spec.Name, spec.Endpoint, spec.AuthToken, spec.Capabilities)
		if err != nil {
			return nil, err
		}
		providers[spec.Name] = instance
	}

	r, err := router.New(cfg.Routing, providers, cfg.Outbounds)
	if err != nil {
		return nil, err
	}

	store, err := newAccountingStore(cfg.Accounting)
	if err != nil {
		return nil, err
	}
	outboundQuotaTracker := quota.NewTrackerFromOutbounds(cfg.Outbounds)
	clientQuotaTracker := quota.NewClientTrackerFromInbounds(cfg.Inbounds)
	dispatcher := execution.NewDispatcherWithStoreAndQuota(store, outboundQuotaTracker)
	listeners := buildListeners(r, dispatcher, cfg, clientQuotaTracker, slog.Default())

	return &App{
		Server:          server.NewListeners(listeners),
		accountingStore: store,
		dispatcher:      dispatcher,
	}, nil
}

func buildListeners(r *router.Router, dispatcher *execution.Dispatcher, cfg config.Config, clientQuotaTracker *quota.Tracker, logger *slog.Logger) []server.Listener {
	listeners := make([]server.Listener, 0, len(cfg.Listeners))
	for _, listener := range cfg.Listeners {
		mux := http.NewServeMux()
		gateway.NewWithClientQuota(r, dispatcher, cfg.ListenerInbounds(listener), clientQuotaTracker, cfg.Accounting, logger).Register(mux)
		listeners = append(listeners, server.Listener{
			Addr:    listener.Listen,
			Handler: mux,
		})
	}
	return listeners
}

func (a *App) Close(ctx context.Context) error {
	if a == nil || a.dispatcher == nil {
		return nil
	}
	return a.dispatcher.Close(ctx)
}

func newAccountingStore(cfg config.AccountingConfig) (accounting.Store, error) {
	backend := cfg.Backend
	if backend == "" {
		backend = "memory"
	}
	switch backend {
	case "memory":
		return accounting.NewMemoryStore(), nil
	case "local_file":
		store, err := accounting.NewLocalFileStore(cfg.LocalFile)
		if err != nil {
			return nil, fmt.Errorf("create accounting local_file store: %w", err)
		}
		return store, nil
	default:
		return nil, fmt.Errorf("unsupported accounting backend %q", backend)
	}
}
