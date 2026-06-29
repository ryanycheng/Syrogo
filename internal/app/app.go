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
	"github.com/ryanycheng/Syrogo/internal/latency"
	"github.com/ryanycheng/Syrogo/internal/provider"
	"github.com/ryanycheng/Syrogo/internal/quota"
	"github.com/ryanycheng/Syrogo/internal/router"
	"github.com/ryanycheng/Syrogo/internal/server"
)

type Options struct {
	ConfigPath string
}

type App struct {
	Server             *server.HTTPServer
	accountingStore    accounting.Store
	dispatcher         *execution.Dispatcher
	quotaSnapshotStore *quota.SnapshotStore
}

func New(cfg config.Config) (*App, error) {
	return NewWithOptions(cfg, Options{})
}

func NewWithOptions(cfg config.Config, opts Options) (*App, error) {
	providers := make(map[string]provider.Provider, len(cfg.Outbounds))
	registry := provider.DefaultFactoryRegistry()
	for _, spec := range cfg.Outbounds {
		httpClient, err := provider.NewHTTPClient(spec.Proxy)
		if err != nil {
			return nil, fmt.Errorf("create outbound %q http client: %w", spec.Name, err)
		}
		instance, err := registry.New(spec.Protocol, spec.Name, spec.Endpoint, spec.AuthToken, spec.Capabilities, httpClient)
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
	quotaSnapshotStore, err := quota.NewSnapshotStore(cfg.Governance.Quota.Snapshot, outboundQuotaTracker, clientQuotaTracker)
	if err != nil {
		return nil, err
	}
	outboundNames := make([]string, 0, len(cfg.Outbounds))
	for _, outbound := range cfg.Outbounds {
		outboundNames = append(outboundNames, outbound.Name)
	}
	healthTracker := provider.NewHealthTracker(outboundNames)
	eventRecorder := quota.NewEventRecorder(cfg.Governance.Quota.Events)
	latencyStore := latency.NewStore(200)
	dispatcher := execution.NewDispatcherWithStoreQuotaHealthEventsAndLatency(store, outboundQuotaTracker, healthTracker, eventRecorder, latencyStore)
	listeners := buildListeners(r, dispatcher, cfg, clientQuotaTracker, eventRecorder, latencyStore, opts.ConfigPath, slog.Default())

	return &App{
		Server:             server.NewListeners(listeners),
		accountingStore:    store,
		dispatcher:         dispatcher,
		quotaSnapshotStore: quotaSnapshotStore,
	}, nil
}

func buildListeners(r *router.Router, dispatcher *execution.Dispatcher, cfg config.Config, clientQuotaTracker *quota.Tracker, eventRecorder *quota.EventRecorder, latencyStore *latency.Store, configPath string, logger *slog.Logger) []server.Listener {
	listeners := make([]server.Listener, 0, len(cfg.Listeners))
	for _, listener := range cfg.Listeners {
		mux := http.NewServeMux()
		gateway.NewWithClientQuotaEventsLatencyConfigAndAdmin(r, dispatcher, cfg.ListenerInbounds(listener), clientQuotaTracker, eventRecorder, latencyStore, configPath, cfg.Accounting, cfg.Admin, logger).Register(mux)
		listeners = append(listeners, server.Listener{
			Addr:    listener.Listen,
			Handler: mux,
		})
	}
	return listeners
}

func (a *App) Close(ctx context.Context) error {
	if a == nil {
		return nil
	}
	var err error
	if a.dispatcher != nil {
		err = a.dispatcher.Close(ctx)
	}
	if closeErr := a.quotaSnapshotStore.Close(ctx); closeErr != nil && err == nil {
		err = closeErr
	}
	return err
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
