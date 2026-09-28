// Package credstore stores account secrets in the OS keyring (macOS Keychain,
// Windows Credential Manager, Secret Service on Linux), with an explicit
// opt-in plain-file fallback for headless environments.
package credstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/zalando/go-keyring"
)

// Service is the keyring service name.
const Service = "reearth-cli"

var ErrNotFound = errors.New("credentials not found")

// Secret is everything sensitive about an account. Only RefreshToken (oauth)
// or Token (token accounts) is essential; AccessTokens is a best-effort cache.
type Secret struct {
	Kind         string                 `json:"kind"`
	RefreshToken string                 `json:"refresh_token,omitempty"`
	Token        string                 `json:"token,omitempty"`
	AccessTokens map[string]AccessToken `json:"access_tokens,omitempty"`
}

// AccessToken is a cached access token for one audience.
type AccessToken struct {
	Token  string    `json:"token"`
	Expiry time.Time `json:"expiry"`
}

type Store interface {
	Get(account string) (*Secret, error)
	Set(account string, s *Secret) error
	Delete(account string) error
}

// Keyring returns the OS keyring store.
func Keyring() Store { return keyringStore{} }

type keyringStore struct{}

func (keyringStore) Get(account string) (*Secret, error) {
	v, err := keyring.Get(Service, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var s Secret
	if err := json.Unmarshal([]byte(v), &s); err != nil {
		return nil, fmt.Errorf("corrupt keyring entry for %q: %w", account, err)
	}
	return &s, nil
}

func (keyringStore) Set(account string, s *Secret) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	err = keyring.Set(Service, account, string(b))
	if errors.Is(err, keyring.ErrSetDataTooBig) && len(s.AccessTokens) > 0 {
		// Windows Credential Manager caps entries at 2560 bytes. The access
		// token cache is optional, so drop it rather than fail.
		cp := *s
		cp.AccessTokens = nil
		return keyringStore{}.Set(account, &cp)
	}
	return err
}

func (keyringStore) Delete(account string) error {
	err := keyring.Delete(Service, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// CheckKeyring verifies that the OS keyring is usable by writing and deleting a probe entry.
func CheckKeyring() error {
	const probe = "__reearth_probe__"
	errc := make(chan error, 1)
	go func() {
		if err := keyring.Set(Service, probe, "ok"); err != nil {
			errc <- err
			return
		}
		errc <- keyring.Delete(Service, probe)
	}()
	select {
	case err := <-errc:
		return err
	case <-time.After(5 * time.Second):
		// Secret Service may hang when no unlock prompt can be shown.
		return errors.New("timed out talking to the keyring")
	}
}

// File returns a plain JSON-file store (0600). Use only when the user opted in.
func File(path string) Store { return &fileStore{path: path} }

// DefaultFilePath is where the insecure fallback store lives.
func DefaultFilePath(configDir string) string {
	return filepath.Join(configDir, "credentials.json")
}

type fileStore struct {
	path string
	mu   sync.Mutex
}

func (f *fileStore) load() (map[string]*Secret, error) {
	m := map[string]*Secret{}
	b, err := os.ReadFile(f.path)
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", f.path, err)
	}
	return m, nil
}

func (f *fileStore) save(m map[string]*Secret) error {
	if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := f.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		_ = os.Chmod(tmp, 0o600)
	}
	return os.Rename(tmp, f.path)
}

func (f *fileStore) Get(account string) (*Secret, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load()
	if err != nil {
		return nil, err
	}
	s, ok := m[account]
	if !ok {
		return nil, ErrNotFound
	}
	return s, nil
}

func (f *fileStore) Set(account string, s *Secret) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load()
	if err != nil {
		return err
	}
	m[account] = s
	return f.save(m)
}

func (f *fileStore) Delete(account string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load()
	if err != nil {
		return err
	}
	if _, ok := m[account]; !ok {
		return nil
	}
	delete(m, account)
	return f.save(m)
}

// Memory returns an in-memory store for tests.
func Memory() Store { return &memStore{m: map[string]*Secret{}} }

type memStore struct {
	mu sync.Mutex
	m  map[string]*Secret
}

func (s *memStore) Get(account string) (*Secret, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[account]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *v
	return &cp, nil
}

func (s *memStore) Set(account string, v *Secret) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *v
	s.m[account] = &cp
	return nil
}

func (s *memStore) Delete(account string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, account)
	return nil
}
