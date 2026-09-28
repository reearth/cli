// Package auth implements login flows (loopback + PKCE, device code), multi-account
// token management and transparent refresh for Re:Earth's Auth0 tenant.
package auth

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"golang.org/x/oauth2"

	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/config"
)

// Built-in environments. Values are public (native app client IDs are not
// secrets) but are injected at release time so that they live in one place:
//
//	-ldflags "-X github.com/reearth/cli/sdk/auth.prodDomain=..."
var (
	prodDomain      string
	prodClientID    string
	prodAudience    string
	stagingDomain   string
	stagingClientID string
	stagingAudience string
)

const (
	EnvProd    = "prod"
	EnvStaging = "staging"
)

// Env is a resolved authentication environment.
type Env struct {
	Name     string
	Domain   string // e.g. https://auth.reearth.io
	ClientID string
	Audience string
	// ProductURLs overrides product base URLs (from user-defined envs).
	ProductURLs map[string]string
}

// Scopes requested at login. offline_access yields a refresh token.
var Scopes = []string{"openid", "profile", "email", "offline_access"}

func builtinEnvs() map[string]*Env {
	return map[string]*Env{
		EnvProd:    {Name: EnvProd, Domain: prodDomain, ClientID: prodClientID, Audience: prodAudience},
		EnvStaging: {Name: EnvStaging, Domain: stagingDomain, ClientID: stagingClientID, Audience: stagingAudience},
	}
}

// EnvNames lists the built-in and user-defined environment names.
func EnvNames(cfg *config.Config) []string {
	names := []string{EnvProd, EnvStaging}
	var custom []string
	for n := range cfg.Envs {
		if n != EnvProd && n != EnvStaging {
			custom = append(custom, n)
		}
	}
	sort.Strings(custom)
	return append(names, custom...)
}

// ResolveEnv returns the environment by name. User-defined envs in the config
// override built-ins; REEARTH_AUTH_DOMAIN / REEARTH_AUTH_CLIENT_ID /
// REEARTH_AUTH_AUDIENCE override any env (useful for development builds).
func ResolveEnv(cfg *config.Config, name string) (*Env, error) {
	if name == "" {
		name = EnvProd
	}
	var env *Env
	if c, ok := cfg.Envs[name]; ok {
		env = &Env{Name: name, Domain: c.Auth.Domain, ClientID: c.Auth.ClientID, Audience: c.Auth.Audience, ProductURLs: map[string]string{}}
		for p, pc := range c.Products {
			env.ProductURLs[p] = pc.BaseURL
		}
	} else if b, ok := builtinEnvs()[name]; ok {
		env = b
	} else {
		return nil, cmdutil.NewError(cmdutil.ExitUsage, "env.unknown",
			fmt.Sprintf("unknown environment %q", name),
			"known environments: "+strings.Join(EnvNames(cfg), ", "))
	}
	if v := os.Getenv("REEARTH_AUTH_DOMAIN"); v != "" {
		env.Domain = v
	}
	if v := os.Getenv("REEARTH_AUTH_CLIENT_ID"); v != "" {
		env.ClientID = v
	}
	if v := os.Getenv("REEARTH_AUTH_AUDIENCE"); v != "" {
		env.Audience = v
	}
	if env.Domain == "" || env.ClientID == "" {
		return nil, cmdutil.NewError(cmdutil.ExitError, "env.not_configured",
			fmt.Sprintf("environment %q is not configured in this build", name),
			"set REEARTH_AUTH_DOMAIN and REEARTH_AUTH_CLIENT_ID, or define envs."+name+" in "+config.Path())
	}
	env.Domain = normalizeDomain(env.Domain)
	return env, nil
}

func normalizeDomain(d string) string {
	d = strings.TrimRight(d, "/")
	if !strings.HasPrefix(d, "https://") && !strings.HasPrefix(d, "http://") {
		d = "https://" + d
	}
	return d
}

// OAuth2Config returns the oauth2 configuration for this environment.
func (e *Env) OAuth2Config(redirectURL string) *oauth2.Config {
	return &oauth2.Config{
		ClientID: e.ClientID,
		Endpoint: oauth2.Endpoint{
			AuthURL:       e.Domain + "/authorize",
			TokenURL:      e.Domain + "/oauth/token",
			DeviceAuthURL: e.Domain + "/oauth/device/code",
			AuthStyle:     oauth2.AuthStyleInParams,
		},
		RedirectURL: redirectURL,
		Scopes:      Scopes,
	}
}
