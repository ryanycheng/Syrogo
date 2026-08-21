package oauth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const credentialVersion = 1

var credentialIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type Provider string

const (
	ProviderClaude Provider = "claude_consumer_oauth"
	ProviderCodex  Provider = "codex_consumer_oauth"
)

type Credential struct {
	ID           string    `json:"id"`
	Provider     Provider  `json:"provider"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	AccountLabel string    `json:"account_label,omitempty"`
	Scope        string    `json:"scope,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Metadata struct {
	ID           string    `json:"id"`
	Provider     Provider  `json:"provider"`
	ExpiresAt    time.Time `json:"expires_at"`
	AccountLabel string    `json:"account_label,omitempty"`
	Scope        string    `json:"scope,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Store struct {
	dir string
	mu  sync.Mutex
}

type persistedCredential struct {
	Version int `json:"version"`
	Credential
}

func NewStore(dir string) (*Store, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("oauth credential directory is required")
	}
	return &Store{dir: dir}, nil
}

func (s *Store) Save(credential Credential) error {
	if err := validateCredential(credential); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("create oauth credential directory: %w", err)
	}
	if err := os.Chmod(s.dir, 0o700); err != nil {
		return fmt.Errorf("chmod oauth credential directory: %w", err)
	}
	now := time.Now().UTC()
	if credential.CreatedAt.IsZero() {
		credential.CreatedAt = now
	}
	credential.UpdatedAt = now
	return s.writeLocked(credential)
}

func (s *Store) Load(id string) (Credential, error) {
	if err := validateCredentialID(id); err != nil {
		return Credential{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked(id)
}

func (s *Store) List() ([]Metadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read oauth credential directory: %w", err)
	}
	items := make([]Metadata, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		credential, err := s.loadLocked(id)
		if err != nil {
			return nil, err
		}
		items = append(items, credential.Metadata())
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, nil
}

func (s *Store) Delete(id string) error {
	if err := validateCredentialID(id); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.path(id)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove oauth credential: %w", err)
	}
	return nil
}

func (s *Store) loadLocked(id string) (Credential, error) {
	path := s.path(id)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Credential{}, fmt.Errorf("oauth credential %q not found", id)
		}
		return Credential{}, fmt.Errorf("stat oauth credential: %w", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return Credential{}, fmt.Errorf("oauth credential %q has insecure permissions", id)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Credential{}, fmt.Errorf("read oauth credential: %w", err)
	}
	var persisted persistedCredential
	if err := json.Unmarshal(data, &persisted); err != nil {
		return Credential{}, fmt.Errorf("decode oauth credential %q: %w", id, err)
	}
	if persisted.Version != credentialVersion {
		return Credential{}, fmt.Errorf("oauth credential %q has unsupported version %d", id, persisted.Version)
	}
	if persisted.ID != id {
		return Credential{}, fmt.Errorf("oauth credential %q has mismatched id", id)
	}
	if err := validateCredential(persisted.Credential); err != nil {
		return Credential{}, err
	}
	return persisted.Credential, nil
}

func (s *Store) writeLocked(credential Credential) error {
	data, err := json.Marshal(persistedCredential{Version: credentialVersion, Credential: credential})
	if err != nil {
		return fmt.Errorf("encode oauth credential: %w", err)
	}
	tmp, err := os.CreateTemp(s.dir, ".credential-*.tmp")
	if err != nil {
		return fmt.Errorf("create oauth credential temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod oauth credential temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write oauth credential: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync oauth credential: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close oauth credential: %w", err)
	}
	if err := os.Rename(tmpPath, s.path(credential.ID)); err != nil {
		return fmt.Errorf("replace oauth credential: %w", err)
	}
	dir, err := os.Open(s.dir)
	if err != nil {
		return fmt.Errorf("open oauth credential directory: %w", err)
	}
	defer func() { _ = dir.Close() }()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sync oauth credential directory: %w", err)
	}
	return nil
}

func (s *Store) path(id string) string {
	return filepath.Join(s.dir, id+".json")
}

func (c Credential) Metadata() Metadata {
	return Metadata{
		ID:           c.ID,
		Provider:     c.Provider,
		ExpiresAt:    c.ExpiresAt,
		AccountLabel: c.AccountLabel,
		Scope:        c.Scope,
		CreatedAt:    c.CreatedAt,
		UpdatedAt:    c.UpdatedAt,
	}
}

func validateCredential(credential Credential) error {
	if err := validateCredentialID(credential.ID); err != nil {
		return err
	}
	if credential.Provider != ProviderClaude && credential.Provider != ProviderCodex {
		return fmt.Errorf("oauth credential %q has unsupported provider %q", credential.ID, credential.Provider)
	}
	if strings.TrimSpace(credential.AccessToken) == "" {
		return fmt.Errorf("oauth credential %q access token is required", credential.ID)
	}
	if credential.ExpiresAt.IsZero() {
		return fmt.Errorf("oauth credential %q expiry is required", credential.ID)
	}
	return nil
}

func validateCredentialID(id string) error {
	if !credentialIDPattern.MatchString(id) {
		return fmt.Errorf("oauth credential id %q is invalid", id)
	}
	return nil
}
