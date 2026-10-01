// Package config manages the non-secret CLI configuration
// (~/.config/reearth/config.yaml) and project files (.reearth.yaml).
// Secrets never live here; see package credstore.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"

	"go.yaml.in/yaml/v3"
)

const currentVersion = 1

// ErrNewerVersion is wrapped by the error LoadFile returns for a file written
// by a newer version of the CLI.
var ErrNewerVersion = errors.New("written by a newer version of the CLI")

// Account kinds.
const (
	KindOAuth = "oauth"
	KindToken = "token"
)

type Config struct {
	Version  int                   `yaml:"version"`
	Active   string                `yaml:"active,omitempty"`
	Accounts map[string]*Account   `yaml:"accounts,omitempty"`
	Envs     map[string]*EnvConfig `yaml:"envs,omitempty"`
	// Settings keeps values of keys this version does not know as written.
	Settings map[string]any `yaml:"settings,omitempty"`
	// Extra keeps top-level fields this version does not know, so that saving
	// does not erase what a newer version wrote. The nested structs below
	// keep theirs the same way.
	Extra map[string]any `yaml:",inline" json:"-"`

	path string
}

type Account struct {
	Env     string `yaml:"env" json:"env"`
	Kind    string `yaml:"kind" json:"kind"`
	Product string `yaml:"product,omitempty" json:"product,omitempty"`
	User    *User  `yaml:"user,omitempty" json:"user,omitempty"`
	// Workspace is the default workspace ID for this account.
	Workspace string `yaml:"workspace,omitempty" json:"workspace,omitempty"`
	// InsecureStorage stores credentials in a plain file instead of the OS keyring.
	InsecureStorage bool `yaml:"insecure_storage,omitempty" json:"insecure_storage,omitempty"`

	Extra map[string]any `yaml:",inline" json:"-"`
}

type User struct {
	Sub   string `yaml:"sub" json:"sub"`
	Email string `yaml:"email,omitempty" json:"email,omitempty"`
	Name  string `yaml:"name,omitempty" json:"name,omitempty"`

	Extra map[string]any `yaml:",inline" json:"-"`
}

// EnvConfig is a user-defined environment (on-premise, self-hosted Auth0, ...).
type EnvConfig struct {
	Auth     EnvAuth               `yaml:"auth"`
	Products map[string]EnvProduct `yaml:"products,omitempty"`

	Extra map[string]any `yaml:",inline" json:"-"`
}

type EnvAuth struct {
	Domain   string `yaml:"domain"`
	ClientID string `yaml:"client_id"`
	Audience string `yaml:"audience,omitempty"`

	Extra map[string]any `yaml:",inline" json:"-"`
}

type EnvProduct struct {
	BaseURL string `yaml:"base_url"`

	Extra map[string]any `yaml:",inline" json:"-"`
}

// Dir returns the configuration directory.
// REEARTH_CONFIG_DIR > XDG_CONFIG_HOME/reearth > %AppData%/reearth (Windows) > ~/.config/reearth
func Dir() string {
	if d := os.Getenv("REEARTH_CONFIG_DIR"); d != "" {
		return d
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "reearth")
	}
	if runtime.GOOS == "windows" {
		if d := os.Getenv("AppData"); d != "" {
			return filepath.Join(d, "reearth")
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "reearth")
}

// CacheDir returns the cache directory (tokens are never stored here).
func CacheDir() string {
	if d := os.Getenv("REEARTH_CACHE_DIR"); d != "" {
		return d
	}
	if d := os.Getenv("XDG_CACHE_HOME"); d != "" {
		return filepath.Join(d, "reearth")
	}
	if d, err := os.UserCacheDir(); err == nil {
		return filepath.Join(d, "reearth")
	}
	return filepath.Join(os.TempDir(), "reearth-cache")
}

// DataDir returns the data directory (extensions live here).
func DataDir() string {
	if d := os.Getenv("REEARTH_DATA_DIR"); d != "" {
		return d
	}
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "reearth")
	}
	if runtime.GOOS == "windows" {
		if d := os.Getenv("LocalAppData"); d != "" {
			return filepath.Join(d, "reearth")
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "reearth")
}

// Path returns the path of the config file.
func Path() string { return filepath.Join(Dir(), "config.yaml") }

// Load reads the config file. A missing file yields an empty config.
func Load() (*Config, error) {
	return LoadFile(Path())
}

func LoadFile(path string) (*Config, error) {
	c := &Config{Version: currentVersion, path: path}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		c.init()
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if c.Version < 1 {
		return nil, fmt.Errorf("%s: invalid config version %d", path, c.Version)
	}
	if c.Version > currentVersion {
		return nil, fmt.Errorf("%s was %w (config version %d); please upgrade", path, ErrNewerVersion, c.Version)
	}
	c.init()
	return c, nil
}

func (c *Config) init() {
	if c.Accounts == nil {
		c.Accounts = map[string]*Account{}
	}
	if c.Envs == nil {
		c.Envs = map[string]*EnvConfig{}
	}
	if c.Settings == nil {
		c.Settings = map[string]any{}
	}
	c.Version = currentVersion
}

func (c *Config) Path() string { return c.path }

// Save writes the config atomically with 0600 permissions.
func (c *Config) Save() error {
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return err
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(c); err != nil {
		return err
	}
	_ = enc.Close()
	b := buf.Bytes()
	tmp, err := os.CreateTemp(filepath.Dir(c.path), ".config-*.yaml")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil && runtime.GOOS != "windows" {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), c.path)
}

// AccountNames returns account names sorted alphabetically.
func (c *Config) AccountNames() []string {
	names := make([]string, 0, len(c.Accounts))
	for n := range c.Accounts {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// RemoveAccount deletes an account and fixes up the active account.
func (c *Config) RemoveAccount(name string) {
	delete(c.Accounts, name)
	if c.Active == name {
		c.Active = ""
		if names := c.AccountNames(); len(names) > 0 {
			c.Active = names[0]
		}
	}
}
