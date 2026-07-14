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
	"github.com/ryanycheng/Syrogo/internal/sessions"
)

type Options struct {
	ConfigPath string
}

type App struct {
	Server             *server.HTTPServer
	accountingStore    accounting.Store
	dispatcher         *execution.Dispatcher
	quotaSnapshotStore *quota.SnapshotStore
	cfg                config.Config
	configPath         string
	reloadManager      *ReloadManager
}

type listenerBinding struct {
	listener config.ListenerSpec
	handler  *gateway.Handler
}

type appRuntime struct {
	router             *router.Router
	dispatcher         *execution.Dispatcher
	clientQuotaTracker *quota.Tracker
	eventRecorder      *quota.EventRecorder
	latencyStore       *latency.Store
	quotaSnapshotStore *quota.SnapshotStore
}

func New(cfg config.Config) (*App, error) {
	return NewWithOptions(cfg, Options{})
}

func NewWithOptions(cfg config.Config, opts Options) (*App, error) {
	store, err := newAccountingStore(cfg.Accounting)
	if err != nil {
		return nil, err
	}
	runtime, err := buildRuntime(cfg, store)
	if err != nil {
		return nil, err
	}
	sessionStore := sessions.NewStore()
	listeners, bindings := buildListeners(runtime, cfg, opts.ConfigPath, sessionStore, slog.Default())
	app := &App{
		Server:             server.NewListeners(listeners),
		accountingStore:    store,
		dispatcher:         runtime.dispatcher,
		quotaSnapshotStore: runtime.quotaSnapshotStore,
		cfg:                cfg,
		configPath:         opts.ConfigPath,
	}
	app.reloadManager = NewReloadManager(app, bindings)
	for _, binding := range bindings {
		binding.handler.SetConfigReloader(app.reloadManager)
	}
	return app, nil
}

func buildRuntime(cfg config.Config, store accounting.Store) (appRuntime, error) {
	providers := make(map[string]provider.Provider, len(cfg.Outbounds))
	registry := provider.DefaultFactoryRegistry()
	for _, spec := range cfg.Outbounds {
		if !config.OutboundEnabled(spec) {
			continue
		}
		httpClient, err := provider.NewHTTPClient(spec.Proxy)
		if err != nil {
			return appRuntime{}, fmt.Errorf("create outbound %q http client: %w", spec.Name, err)
		}
		instance, err := registry.New(spec.Protocol, spec.Name, spec.Endpoint, spec.AuthToken, spec.Capabilities, httpClient)
		if err != nil {
			return appRuntime{}, err
		}
		providers[spec.Name] = instance
	}

	enabledOutbounds := make([]config.OutboundSpec, 0, len(cfg.Outbounds))
	for _, outbound := range cfg.Outbounds {
		if config.OutboundEnabled(outbound) {
			enabledOutbounds = append(enabledOutbounds, outbound)
		}
	}
	r, err := router.New(cfg.Routing, providers, enabledOutbounds)
	if err != nil {
		return appRuntime{}, err
	}

	outboundQuotaTracker := quota.NewTrackerFromOutbounds(enabledOutbounds)
	clientQuotaTracker := quota.NewClientTrackerFromInbounds(cfg.Inbounds)
	quotaSnapshotStore, err := quota.NewSnapshotStore(cfg.Governance.Quota.Snapshot, outboundQuotaTracker, clientQuotaTracker)
	if err != nil {
		return appRuntime{}, err
	}
	outboundNames := make([]string, 0, len(enabledOutbounds))
	for _, outbound := range enabledOutbounds {
		outboundNames = append(outboundNames, outbound.Name)
	}
	healthTracker := provider.NewHealthTracker(outboundNames)
	eventRecorder := quota.NewEventRecorder(cfg.Governance.Quota.Events)
	latencyStore := latency.NewStore(200)
	dispatcher := execution.NewDispatcherWithStoreQuotaHealthEventsAndLatency(store, outboundQuotaTracker, healthTracker, eventRecorder, latencyStore)
	return appRuntime{
		router:             r,
		dispatcher:         dispatcher,
		clientQuotaTracker: clientQuotaTracker,
		eventRecorder:      eventRecorder,
		latencyStore:       latencyStore,
		quotaSnapshotStore: quotaSnapshotStore,
	}, nil
}

func buildListeners(runtime appRuntime, cfg config.Config, configPath string, sessionStore *sessions.Store, logger *slog.Logger) ([]server.Listener, []listenerBinding) {
	listeners := normalizedListeners(cfg)
	serverListeners := make([]server.Listener, 0, len(listeners))
	bindings := make([]listenerBinding, 0, len(listeners))
	for _, listener := range listeners {
		mux := http.NewServeMux()
		handler := gateway.NewWithClientQuotaEventsLatencyConfigAdminAndSessions(runtime.router, runtime.dispatcher, cfg.ListenerInbounds(listener), runtime.clientQuotaTracker, runtime.eventRecorder, runtime.latencyStore, configPath, cfg.Accounting, cfg.Admin, sessionStore, logger)
		handler.Register(mux)
		serverListeners = append(serverListeners, server.Listener{
			Addr:    listener.Listen,
			Handler: mux,
		})
		bindings = append(bindings, listenerBinding{listener: listener, handler: handler})
	}
	return serverListeners, bindings
}

func normalizedListeners(cfg config.Config) []config.ListenerSpec {
	if len(cfg.Listeners) > 0 {
		return append([]config.ListenerSpec(nil), cfg.Listeners...)
	}
	inbounds := make([]string, 0, len(cfg.Inbounds))
	for _, inbound := range cfg.Inbounds {
		inbounds = append(inbounds, inbound.Name)
	}
	return []config.ListenerSpec{{Name: "default", Listen: cfg.Server.Listen, Inbounds: inbounds}}
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
