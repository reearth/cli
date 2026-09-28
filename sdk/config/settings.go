package config

import (
	"fmt"
	"os"
	"slices"
	"strings"
)

// Setting is a user-tunable key for `reearth config get/set`.
type Setting struct {
	Key         string
	Description string
	Default     string
	Allowed     []string // empty means free-form
}

// Settings lists every supported setting key.
var Settings = []Setting{
	{Key: "output", Description: "Default output format on a TTY", Default: "table", Allowed: []string{"table", "plain", "json", "yaml", "ndjson"}},
	{Key: "prompt", Description: "Allow interactive prompts", Default: "enabled", Allowed: []string{"enabled", "disabled"}},
	{Key: "update_check", Description: "Check for new CLI versions", Default: "enabled", Allowed: []string{"enabled", "disabled"}},
	{Key: "browser", Description: "Command used to open URLs (empty: system default)"},
}

func LookupSetting(key string) (Setting, bool) {
	for _, s := range Settings {
		if s.Key == key {
			return s, true
		}
	}
	return Setting{}, false
}

// SettingEnv returns the environment variable that overrides a setting.
func SettingEnv(key string) string {
	return "REEARTH_" + strings.ToUpper(key)
}

// Origin tells where a resolved value came from.
type Origin string

const (
	OriginDefault Origin = "default"
	OriginConfig  Origin = "config"
	OriginEnv     Origin = "env"
	OriginProject Origin = "project"
	OriginFlag    Origin = "flag"
)

// Get resolves a setting: environment > config file > default.
func (c *Config) Get(key string) (string, Origin, error) {
	s, ok := LookupSetting(key)
	if !ok {
		return "", "", unknownKey(key)
	}
	if v := os.Getenv(SettingEnv(key)); v != "" {
		return v, OriginEnv, nil
	}
	if v, ok := c.Settings[key]; ok {
		return v, OriginConfig, nil
	}
	return s.Default, OriginDefault, nil
}

func (c *Config) Set(key, value string) error {
	s, ok := LookupSetting(key)
	if !ok {
		return unknownKey(key)
	}
	if len(s.Allowed) > 0 && !slices.Contains(s.Allowed, value) {
		return fmt.Errorf("invalid value %q for %s (allowed: %s)", value, key, strings.Join(s.Allowed, ", "))
	}
	c.Settings[key] = value
	return nil
}

func (c *Config) Unset(key string) error {
	if _, ok := LookupSetting(key); !ok {
		return unknownKey(key)
	}
	delete(c.Settings, key)
	return nil
}

func unknownKey(key string) error {
	keys := make([]string, len(Settings))
	for i, s := range Settings {
		keys[i] = s.Key
	}
	return fmt.Errorf("unknown config key %q (known keys: %s)", key, strings.Join(keys, ", "))
}
