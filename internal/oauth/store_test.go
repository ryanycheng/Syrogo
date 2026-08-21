package oauth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStoreRoundTripAndPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "oauth")
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	credential := Credential{
		ID:           "claude-main",
		Provider:     ProviderClaude,
		AccessToken:  "access-secret",
		RefreshToken: "refresh-secret",
		ExpiresAt:    time.Now().Add(time.Hour).UTC(),
		AccountLabel: "work",
	}
	if err := store.Save(credential); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	loaded, err := store.Load(credential.ID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.AccessToken != credential.AccessToken || loaded.RefreshToken != credential.RefreshToken || loaded.Provider != ProviderClaude {
		t.Fatalf("Load() = %#v", loaded)
	}
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("directory permissions = %v, %v", info.Mode().Perm(), err)
	}
	if info, err := os.Stat(filepath.Join(dir, "claude-main.json")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file permissions = %v, %v", info.Mode().Perm(), err)
	}
	items, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 1 || items[0].ID != credential.ID || items[0].Provider != ProviderClaude {
		t.Fatalf("List() = %#v", items)
	}
}

func TestStoreRejectsUnsafeCredentialID(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	err = store.Save(Credential{ID: "../outside", Provider: ProviderClaude, AccessToken: "token", ExpiresAt: time.Now().Add(time.Hour)})
	if err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("Save() error = %v, want invalid id", err)
	}
}

func TestStoreRejectsInsecureCredentialFile(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	credential := Credential{ID: "codex-main", Provider: ProviderCodex, AccessToken: "token", ExpiresAt: time.Now().Add(time.Hour)}
	if err := store.Save(credential); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "codex-main.json")
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(credential.ID); err == nil || !strings.Contains(err.Error(), "insecure permissions") {
		t.Fatalf("Load() error = %v, want insecure permissions", err)
	}
}
