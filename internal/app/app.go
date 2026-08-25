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
	"github.com/ryanycheng/Syrogo/internal/logging"
	"github.com/ryanycheng/Syrogo/internal/oauth"
	"github.com/ryanycheng/Syrogo/internal/provider"
	"github.com/ryanycheng/Syrogo/internal/quota"
	"github.com/ryanycheng/Syrogo/internal/router"
	"github.com/ryanycheng/Syrogo/internal/server"
	"github.com/ryanycheng/Syrogo/internal/sessions"
)

type Options struct {
	ConfigPath string
	RecentLogs *logging.RecentBuffer
}

type App struct {
	Server               *server.HTTPServer
	accountingStore      accounting.Store
	dispatcher           *execution.Dispatcher
	outboundQuotaTracker *quota.Tracker
	clientQuotaTracker   *quota.Tracker
	quotaSnapshotStore   *quota.SnapshotStore
	sessionStore         *sessions.Store
	sessionSnapshotStore *sessions.SnapshotStore
	sessionReaper        *sessions.Reaper
	oauthManager         *oauth.Manager
	cfg                  config.Config
	configPath           string
	reloadManager        *ReloadManager
}

type listenerBinding struct {
	listener config.ListenerSpec
	handler  *gateway.Handler
}

type appRuntime struct {
	router               *router.Router
	dispatcher           *execution.Dispatcher
	outboundQuotaTracker *quota.Tracker
	clientQuotaTracker   *quota.Tracker
	eventRecorder        *quota.EventRecorder
	latencyStore         *latency.Store
	quotaSnapshotStore   *quota.SnapshotStore
}

func New(cfg config.Config) (*App, error) {
	return NewWithOptions(cfg, Options{})
}

func NewWithOptions(cfg config.Config, opts Options) (*App, error) {
	store, err := newAccountingStore(cfg.Accounting)
	if err != nil {
		return nil, err
	}
	oauthDir := cfg.OAuth.Dir
	if oauthDir == "" {
		oauthDir = "./data/oauth"
	}
	oauthStore, err := oauth.NewStore(oauthDir)
	if err != nil {
		_ = store.Close(context.Background())
		return nil, err
	}
	oauthManager := oauth.NewManager(oauthStore, nil)
	runtime, err := buildRuntimeWithOAuth(cfg, store, oauthManager)
	if err != nil {
		_ = store.Close(context.Background())
		return nil, err
	}
	sessionStore := sessions.NewStore()
	sessionSnapshotConfig := cfg.Sessions.Snapshot
	if sessionSnapshotConfig.Dir == "" {
		sessionSnapshotConfig.Dir = "./data/sessions"
	}
	if sessionSnapshotConfig.FlushInterval == "" {
		sessionSnapshotConfig.FlushInterval = config.DurationValue("5s")
	}
	sessionSnapshotStore, err := sessions.NewSnapshotStore(sessions.SnapshotConfig{
		Enabled:       sessionSnapshotConfig.EnabledEffective(),
		Dir:           sessionSnapshotConfig.Dir,
		FlushInterval: sessionSnapshotConfig.FlushInterval.Duration(),
	}, sessionStore)
	if err != nil {
		_ = runtime.dispatcher.Close(context.Background())
		_ = runtime.quotaSnapshotStore.Close(context.Background())
		return nil, fmt.Errorf("create session snapshot store: %w", err)
	}
	listeners, bindings := buildListeners(runtime, cfg, opts.ConfigPath, sessionStore, slog.Default())
	for _, binding := range bindings {
		binding.handler.SetRecentLogs(opts.RecentLogs)
		binding.handler.SetOAuthManager(oauthManager)
	}
	app := &App{
		Server:               server.NewListeners(listeners),
		accountingStore:      store,
		dispatcher:           runtime.dispatcher,
		outboundQuotaTracker: runtime.outboundQuotaTracker,
		clientQuotaTracker:   runtime.clientQuotaTracker,
		quotaSnapshotStore:   runtime.quotaSnapshotStore,
		sessionStore:         sessionStore,
		sessionSnapshotStore: sessionSnapshotStore,
		sessionReaper:        sessions.NewReaper(sessionStore),
		cfg:                  cfg,
		configPath:           opts.ConfigPath,
		oauthManager:         oauthManager,
	}
	app.reloadManager = NewReloadManager(app, bindings)
	for _, binding := range bindings {
		binding.handler.SetConfigReloader(app.reloadManager)
	}
	return app, nil
}

func buildRuntimeWithOAuth(cfg config.Config, store accounting.Store, oauthManager *oauth.Manager) (appRuntime, error) {
	return buildRuntimeWithTrackersAndOAuth(cfg, store, quota.NewTracker(nil), quota.NewClientTracker(nil), true, oauthManager)
}

func buildRuntimeWithTrackersAndOAuth(cfg config.Config, store accounting.Store, outboundQuotaTracker, clientQuotaTracker *quota.Tracker, loadSnapshot bool, oauthManager *oauth.Manager) (appRuntime, error) {
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
		instance, err := newProviderInstance(registry, spec, httpClient, oauthManager)
		if err != nil {
			return appRuntime{}, err
		}
		providers[spec.Name] = instance
	}

	enabledOutbounds := enabledOutbounds(cfg)
	r, err := router.New(cfg.Routing, providers, cfg.Outbounds)
	if err != nil {
		return appRuntime{}, err
	}

	if outboundQuotaTracker == nil {
		outboundQuotaTracker = quota.NewTracker(nil)
	}
	if clientQuotaTracker == nil {
		clientQuotaTracker = quota.NewClientTracker(nil)
	}
	if loadSnapshot {
		outboundQuotaTracker.ReconfigureOutbounds(enabledOutbounds)
		clientQuotaTracker.ReconfigureClientSpecs(cfg.Clients)
	}
	var quotaSnapshotStore *quota.SnapshotStore
	if hasQuotaConfig(enabledOutbounds, cfg.Clients) && loadSnapshot {
		quotaSnapshotStore, err = quota.NewSnapshotStore(cfg.Governance.Quota.Snapshot, outboundQuotaTracker, clientQuotaTracker)
	} else if hasQuotaConfig(enabledOutbounds, cfg.Clients) {
		quotaSnapshotStore, err = quota.NewSnapshotStoreWithoutLoad(cfg.Governance.Quota.Snapshot, outboundQuotaTracker, clientQuotaTracker)
	}
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
	dispatcher := execution.NewDispatcherWithStoreQuotasHealthEventsLatencyAndPricing(store, outboundQuotaTracker, clientQuotaTracker, healthTracker, eventRecorder, latencyStore, accounting.NewPriceCalculator(cfg.Accounting.Pricing))
	return appRuntime{
		router:               r,
		dispatcher:           dispatcher,
		outboundQuotaTracker: outboundQuotaTracker,
		clientQuotaTracker:   clientQuotaTracker,
		eventRecorder:        eventRecorder,
		latencyStore:         latencyStore,
		quotaSnapshotStore:   quotaSnapshotStore,
	}, nil
}

func newProviderInstance(registry *provider.FactoryRegistry, spec config.OutboundSpec, httpClient *http.Client, oauthManager *oauth.Manager) (provider.Provider, error) {
	switch spec.Auth.Type {
	case "":
		return registry.New(spec.Protocol, spec.Name, spec.Endpoint, spec.AuthToken, spec.Capabilities, httpClient)
	case "claude_consumer_oauth":
		if oauthManager == nil {
			return nil, fmt.Errorf("oauth manager is required for outbound %q", spec.Name)
		}
		source := provider.NewOAuthCredentialSource(oauthManager, spec.Auth.CredentialRef, oauth.ProviderClaude)
		return provider.NewAnthropicMessagesOAuthCompatible(spec.Name, "https://api.anthropic.com/v1", source, spec.Capabilities, httpClient), nil
	case "codex_consumer_oauth":
		if oauthManager == nil {
			return nil, fmt.Errorf("oauth manager is required for outbound %q", spec.Name)
		}
		source := provider.NewOAuthCredentialSource(oauthManager, spec.Auth.CredentialRef, oauth.ProviderCodex)
		return provider.NewCodexConsumerProvider(spec.Name, source, httpClient), nil
	default:
		return nil, fmt.Errorf("outbound %q has unsupported auth type %q", spec.Name, spec.Auth.Type)
	}
}

func enabledOutbounds(cfg config.Config) []config.OutboundSpec {
	result := make([]config.OutboundSpec, 0, len(cfg.Outbounds))
	for _, outbound := range cfg.Outbounds {
		if config.OutboundEnabled(outbound) {
			result = append(result, outbound)
		}
	}
	return result
}

func hasQuotaConfig(outbounds []config.OutboundSpec, clients []config.ClientSpec) bool {
	for _, outbound := range outbounds {
		if outbound.Quota.Enabled {
			return true
		}
	}
	for _, client := range clients {
		if client.Quota.Enabled {
			return true
		}
	}
	return false
}

func buildListeners(runtime appRuntime, cfg config.Config, configPath string, sessionStore *sessions.Store, logger *slog.Logger) ([]server.Listener, []listenerBinding) {
	listeners := normalizedListeners(cfg)
	serverListeners := make([]server.Listener, 0, len(listeners))
	bindings := make([]listenerBinding, 0, len(listeners))
	for _, listener := range listeners {
		mux := http.NewServeMux()
		handler := gateway.NewWithClientQuotaEventsLatencyConfigAdminAndSessions(runtime.router, runtime.dispatcher, cfg.Clients, cfg.ListenerInbounds(listener), runtime.clientQuotaTracker, runtime.eventRecorder, runtime.latencyStore, configPath, cfg.Accounting, cfg.Admin, sessionStore, logger)
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
	if closeErr := a.sessionReaper.Close(ctx); closeErr != nil {
		err = closeErr
	}
	if closeErr := a.sessionSnapshotStore.Close(ctx); closeErr != nil && err == nil {
		err = closeErr
	}
	if a.dispatcher != nil {
		if closeErr := a.dispatcher.Close(ctx); closeErr != nil && err == nil {
			err = closeErr
		}
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
