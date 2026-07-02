package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ryanycheng/Syrogo/internal/config"
	"github.com/ryanycheng/Syrogo/internal/gateway"
	"gopkg.in/yaml.v3"
)

const configHistoryLimit = 20

type ReloadManager struct {
	mu       sync.Mutex
	app      *App
	bindings []listenerBinding
	history  *configHistory
}

type ReloadResult struct {
	OK              bool   `json:"ok"`
	Applied         bool   `json:"applied"`
	RestartRequired bool   `json:"restart_required"`
	Reason          string `json:"reason,omitempty"`
	HistoryID       string `json:"history_id,omitempty"`
	QuotaStateReset bool   `json:"quota_state_reset"`
}

type HistoryItem struct {
	ID        string `json:"id"`
	CreatedAt string `json:"created_at"`
	Reason    string `json:"reason"`
	Path      string `json:"path"`
	Checksum  string `json:"checksum"`
}

type configHistory struct {
	dir string
}

func NewReloadManager(app *App, bindings []listenerBinding) *ReloadManager {
	return &ReloadManager{app: app, bindings: bindings, history: newConfigHistory(app.configPath)}
}

func (m *ReloadManager) ApplyConfig(ctx context.Context) (gateway.ReloadResult, error) {
	result, err := m.applyConfig(ctx, "apply")
	return gateway.ReloadResult(result), err
}

func (m *ReloadManager) History() []gateway.HistoryItem {
	items, _ := m.history.List()
	converted := make([]gateway.HistoryItem, 0, len(items))
	for _, item := range items {
		converted = append(converted, gateway.HistoryItem(item))
	}
	return converted
}

func (m *ReloadManager) Rollback(ctx context.Context, id string) (gateway.ReloadResult, error) {
	result, err := m.rollback(ctx, id)
	return gateway.ReloadResult(result), err
}

func (m *ReloadManager) applyConfig(ctx context.Context, reason string) (ReloadResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.applyConfigLocked(ctx, reason)
}

func (m *ReloadManager) applyConfigLocked(_ context.Context, reason string) (ReloadResult, error) {
	if m.app.configPath == "" {
		return ReloadResult{}, errors.New("config path is not configured")
	}
	data, err := os.ReadFile(m.app.configPath)
	if err != nil {
		return ReloadResult{}, fmt.Errorf("read config: %w", err)
	}
	next, err := config.ParseBytes(data)
	if err != nil {
		return ReloadResult{}, err
	}
	if restartReason := restartRequiredReason(m.app.cfg, next); restartReason != "" {
		return ReloadResult{OK: true, Applied: false, RestartRequired: true, Reason: restartReason}, nil
	}
	historyID, err := m.history.SaveConfig(m.app.configPath, m.app.cfg, reason)
	if err != nil {
		return ReloadResult{}, err
	}
	runtime, err := buildRuntime(next, m.app.accountingStore)
	if err != nil {
		return ReloadResult{}, err
	}
	listeners := normalizedListeners(next)
	for i, binding := range m.bindings {
		binding.handler.ApplyRuntime(gateway.RuntimeState{
			Router:             runtime.router,
			Dispatcher:         runtime.dispatcher,
			Inbounds:           next.ListenerInbounds(listeners[i]),
			ClientQuotaTracker: runtime.clientQuotaTracker,
			EventRecorder:      runtime.eventRecorder,
			LatencyStore:       runtime.latencyStore,
			Accounting:         next.Accounting,
			Admin:              next.Admin,
			ConfigPath:         m.app.configPath,
		})
	}
	oldSnapshotStore := m.app.quotaSnapshotStore
	m.app.cfg = next
	m.app.dispatcher = runtime.dispatcher
	m.app.quotaSnapshotStore = runtime.quotaSnapshotStore
	if oldSnapshotStore != nil {
		_ = oldSnapshotStore.Close(context.Background())
	}
	return ReloadResult{OK: true, Applied: true, HistoryID: historyID, QuotaStateReset: true}, nil
}

func (m *ReloadManager) rollback(ctx context.Context, id string) (ReloadResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.app.configPath == "" {
		return ReloadResult{}, errors.New("config path is not configured")
	}
	data, item, err := m.history.Read(id)
	if err != nil {
		return ReloadResult{}, err
	}
	if _, err := config.ParseBytes(data); err != nil {
		return ReloadResult{}, err
	}
	current, err := os.ReadFile(m.app.configPath)
	if err != nil {
		return ReloadResult{}, fmt.Errorf("read current config: %w", err)
	}
	if _, err := m.history.SaveBytes(m.app.configPath, current, "rollback_before_"+item.ID); err != nil {
		return ReloadResult{}, err
	}
	if err := config.WriteValidatedFile(m.app.configPath, data); err != nil {
		return ReloadResult{}, err
	}
	result, err := m.applyConfigLocked(ctx, "rollback_"+item.ID)
	if err != nil {
		_ = config.WriteValidatedFile(m.app.configPath, current)
		return result, err
	}
	return result, nil
}

func restartRequiredReason(current, next config.Config) string {
	currentListeners := normalizedListeners(current)
	nextListeners := normalizedListeners(next)
	if len(currentListeners) != len(nextListeners) {
		return "listener count changed"
	}
	for i := range currentListeners {
		if currentListeners[i].Name != nextListeners[i].Name {
			return "listener name changed"
		}
		if currentListeners[i].Listen != nextListeners[i].Listen {
			return "listener listen address changed"
		}
		if strings.Join(currentListeners[i].Inbounds, "\x00") != strings.Join(nextListeners[i].Inbounds, "\x00") {
			return "listener inbound binding changed"
		}
	}
	return ""
}

func newConfigHistory(configPath string) *configHistory {
	dir := ".syrogo-history"
	if configPath != "" {
		dir = filepath.Join(filepath.Dir(configPath), ".syrogo-history")
	}
	return &configHistory{dir: dir}
}

func (h *configHistory) Save(sourcePath string, reason string) (string, error) {
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return "", fmt.Errorf("read config for history: %w", err)
	}
	return h.SaveBytes(sourcePath, data, reason)
}

func (h *configHistory) SaveConfig(sourcePath string, cfg config.Config, reason string) (string, error) {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return "", err
	}
	return h.SaveBytes(sourcePath, data, reason)
}

func (h *configHistory) SaveBytes(sourcePath string, data []byte, reason string) (string, error) {
	if err := os.MkdirAll(h.dir, 0o700); err != nil {
		return "", fmt.Errorf("create config history dir: %w", err)
	}
	now := time.Now().UTC()
	checksum := checksumHex(data)
	id := now.Format("20060102-150405") + "-" + checksum[:8]
	yamlPath := filepath.Join(h.dir, id+".yaml")
	metaPath := filepath.Join(h.dir, id+".json")
	if err := os.WriteFile(yamlPath, data, 0o600); err != nil {
		return "", fmt.Errorf("write config history: %w", err)
	}
	item := HistoryItem{ID: id, CreatedAt: now.Format(time.RFC3339Nano), Reason: reason, Path: yamlPath, Checksum: checksum}
	meta, err := json.MarshalIndent(item, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(metaPath, meta, 0o600); err != nil {
		return "", fmt.Errorf("write config history metadata: %w", err)
	}
	if err := h.Prune(configHistoryLimit); err != nil {
		return "", err
	}
	_ = sourcePath
	return id, nil
}

func (h *configHistory) List() ([]HistoryItem, error) {
	entries, err := os.ReadDir(h.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	items := make([]HistoryItem, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(h.dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		var item HistoryItem
		if err := json.Unmarshal(data, &item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt > items[j].CreatedAt })
	return items, nil
}

func (h *configHistory) Read(id string) ([]byte, HistoryItem, error) {
	items, err := h.List()
	if err != nil {
		return nil, HistoryItem{}, err
	}
	if id == "" && len(items) > 0 {
		id = items[0].ID
	}
	for _, item := range items {
		if item.ID == id {
			data, err := os.ReadFile(item.Path)
			if err != nil {
				return nil, HistoryItem{}, err
			}
			return data, item, nil
		}
	}
	return nil, HistoryItem{}, fmt.Errorf("history item %q not found", id)
}

func (h *configHistory) Prune(limit int) error {
	items, err := h.List()
	if err != nil || len(items) <= limit {
		return err
	}
	for _, item := range items[limit:] {
		_ = os.Remove(item.Path)
		_ = os.Remove(filepath.Join(h.dir, item.ID+".json"))
	}
	return nil
}

func checksumHex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
