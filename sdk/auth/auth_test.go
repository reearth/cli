package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/reearth/cli/sdk/build"
	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/config"
	"github.com/reearth/cli/sdk/credstore"
	"github.com/reearth/cli/sdk/iostreams"
)

// fakeAuth0 is a minimal authorization server: authorize (PKCE), device code,
// token (authorization_code, device_code, refresh_token with rotation).
type fakeAuth0 struct {
	t         *testing.T
	srv       *httptest.Server
	mu        sync.Mutex
	challenge string
	codes     map[string]bool
	refresh   map[string]bool // valid refresh tokens
	pending   int             // authorization_pending responses before device success
	tokenHits atomic.Int32
	nextRT    int
}

func newFakeAuth0(t *testing.T) *fakeAuth0 {
	f := &fakeAuth0{t: t, codes: map[string]bool{}, refresh: map[string]bool{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/device/code", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("audience") != "https://api.test" {
			t.Errorf("device code: audience = %q", r.Form.Get("audience"))
		}
		writeJSON(w, 200, map[string]any{
			"device_code": "dev-1", "user_code": "ABCD-EFGH",
			"verification_uri": f.srv.URL + "/activate", "verification_uri_complete": f.srv.URL + "/activate?user_code=ABCD-EFGH",
			"expires_in": 60, "interval": 1,
		})
	})
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		f.tokenHits.Add(1)
		_ = r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge {
				writeJSON(w, 400, map[string]any{"error": "invalid_grant", "error_description": "PKCE verification failed"})
				return
			}
			if !f.codes[r.Form.Get("code")] {
				writeJSON(w, 400, map[string]any{"error": "invalid_grant"})
				return
			}
			delete(f.codes, r.Form.Get("code"))
		case "urn:ietf:params:oauth:grant-type:device_code":
			if f.pending > 0 {
				f.pending--
				writeJSON(w, 403, map[string]any{"error": "authorization_pending"})
				return
			}
		case "refresh_token":
			rt := r.Form.Get("refresh_token")
			if !f.refresh[rt] {
				writeJSON(w, 403, map[string]any{"error": "invalid_grant"})
				return
			}
			delete(f.refresh, rt) // rotation
		default:
			writeJSON(w, 400, map[string]any{"error": "unsupported_grant_type"})
			return
		}
		f.nextRT++
		rt := "rt-" + string(rune('0'+f.nextRT))
		f.refresh[rt] = true
		writeJSON(w, 200, map[string]any{"access_token": "at-" + rt, "refresh_token": rt, "token_type": "Bearer", "expires_in": 3600})
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"sub": "auth0|1", "email": "kana@example.com"})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fakeAuth0) env() *Env {
	return &Env{Name: "test", Domain: f.srv.URL, ClientID: "cli", Audience: "https://api.test"}
}

func TestLoopbackLoginUsesPKCE(t *testing.T) {
	fa := newFakeAuth0(t)
	ios, _, _, _ := iostreams.Test()

	open := func(authURL string) error {
		u, err := url.Parse(authURL)
		if err != nil {
			return err
		}
		q := u.Query()
		if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
			t.Errorf("PKCE parameters missing: %s", u.RawQuery)
		}
		if q.Get("audience") != "https://api.test" {
			t.Errorf("audience = %q", q.Get("audience"))
		}
		if !strings.HasPrefix(q.Get("redirect_uri"), "http://127.0.0.1:") {
			t.Errorf("redirect_uri = %q", q.Get("redirect_uri"))
		}
		fa.mu.Lock()
		fa.challenge = q.Get("code_challenge")
		fa.codes["code-1"] = true
		fa.mu.Unlock()
		go func() {
			resp, err := http.Get(q.Get("redirect_uri") + "?code=code-1&state=" + url.QueryEscape(q.Get("state")))
			if err == nil {
				_ = resp.Body.Close()
			}
		}()
		return nil
	}

	tok, err := Login(context.Background(), LoginOptions{Env: fa.env(), Flow: FlowLoopback, IO: ios, OpenBrowser: open, Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if tok.RefreshToken == "" || tok.AccessToken == "" {
		t.Fatalf("token = %+v", tok)
	}
}

func TestLoopbackIgnoresStateMismatch(t *testing.T) {
	fa := newFakeAuth0(t)
	ios, _, _, _ := iostreams.Test()
	open := func(authURL string) error {
		u, _ := url.Parse(authURL)
		cb := u.Query().Get("redirect_uri")
		go func() {
			// A forged callback must neither end the login nor reach the terminal.
			resp, err := http.Get(cb + "?error=x&error_description=forged&state=forged")
			if err == nil {
				_ = resp.Body.Close()
			}
			resp, err = http.Get(cb + "?error=access_denied&error_description=" + url.QueryEscape("\x1b[2Jdenied") + "&state=" + url.QueryEscape(u.Query().Get("state")))
			if err == nil {
				_ = resp.Body.Close()
			}
		}()
		return nil
	}
	_, err := Login(context.Background(), LoginOptions{Env: fa.env(), Flow: FlowLoopback, IO: ios, OpenBrowser: open, Timeout: 10 * time.Second})
	if err == nil || !strings.Contains(err.Error(), "access_denied") || strings.Contains(err.Error(), "forged") || strings.Contains(err.Error(), "\x1b") {
		t.Fatalf("err = %q", err)
	}
}

func TestAuthEnvOverridesOnlyInDevBuilds(t *testing.T) {
	cfg, _ := config.LoadFile(t.TempDir() + "/c.yaml")
	cfg.Envs["onprem"] = &config.EnvConfig{Auth: config.EnvAuth{Domain: "auth.example.com", ClientID: "c"}}
	t.Setenv("REEARTH_AUTH_DOMAIN", "https://evil.example")
	env, err := ResolveEnv(cfg, "onprem")
	if err != nil || env.Domain != "https://evil.example" {
		t.Fatalf("dev build: %+v (%v)", env, err)
	}
	orig := build.Version
	t.Cleanup(func() { build.Version = orig })
	build.Version = "1.2.3"
	env, err = ResolveEnv(cfg, "onprem")
	if err != nil || env.Domain != "https://auth.example.com" {
		t.Fatalf("release build: %+v (%v)", env, err)
	}
}

type failingStore struct{ credstore.Store }

func (failingStore) Set(string, *credstore.Secret) error { return errors.New("disk full") }

func TestSourceSaveFailureAfterRotation(t *testing.T) {
	fa := newFakeAuth0(t)
	fa.refresh["rt-initial"] = true
	m, _ := newTestManager(t, fa)
	_ = m.Keyring.Set("work", &credstore.Secret{Kind: config.KindOAuth, RefreshToken: "rt-initial"})
	m.Keyring = failingStore{m.Keyring}
	r, _ := m.Resolve("", nil)
	ts, _ := m.TokenSource(r)
	_, err := ts.Token()
	var e *cmdutil.Error
	if !errors.As(err, &e) || e.Code != "auth.save_failed" || e.Exit != cmdutil.ExitAuth || !strings.Contains(e.Message, "rotated") {
		t.Fatalf("err = %#v", err)
	}
}

func TestDeviceLogin(t *testing.T) {
	fa := newFakeAuth0(t)
	fa.pending = 1
	ios, _, _, errOut := iostreams.Test()
	tok, err := Login(context.Background(), LoginOptions{Env: fa.env(), Flow: FlowDevice, IO: ios, Timeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if tok.RefreshToken == "" {
		t.Fatal("no refresh token")
	}
	if !strings.Contains(errOut.String(), "ABCD-EFGH") {
		t.Errorf("user code not shown: %q", errOut.String())
	}
}

func newTestManager(t *testing.T, fa *fakeAuth0) (*Manager, *config.Config) {
	t.Setenv("REEARTH_AUTH_DOMAIN", fa.srv.URL)
	t.Setenv("REEARTH_AUTH_CLIENT_ID", "cli")
	t.Setenv("REEARTH_AUTH_AUDIENCE", "https://api.test")
	cfg, err := config.LoadFile(t.TempDir() + "/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Accounts["work"] = &config.Account{Env: EnvProd, Kind: config.KindOAuth}
	cfg.Active = "work"
	m := NewManager(cfg, "reearth")
	m.Keyring = credstore.Memory()
	m.LockDir = t.TempDir()
	m.Getenv = func(string) string { return "" }
	return m, cfg
}

func TestSourceRefreshesAndPersistsRotatedToken(t *testing.T) {
	fa := newFakeAuth0(t)
	fa.refresh["rt-initial"] = true
	m, cfg := newTestManager(t, fa)
	if err := m.Keyring.Set("work", &credstore.Secret{Kind: config.KindOAuth, RefreshToken: "rt-initial"}); err != nil {
		t.Fatal(err)
	}

	r, err := m.Resolve("", nil)
	if err != nil {
		t.Fatal(err)
	}
	ts, err := m.TokenSource(r)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := ts.Token()
	if err != nil {
		t.Fatal(err)
	}
	sec, _ := m.Keyring.Get("work")
	if sec.RefreshToken != tok.RefreshToken || sec.RefreshToken == "rt-initial" {
		t.Fatalf("rotated refresh token not persisted: stored %q, got %q", sec.RefreshToken, tok.RefreshToken)
	}
	if sec.AccessTokens["https://api.test"].Token != tok.AccessToken {
		t.Fatal("access token not cached")
	}

	// A new process (new manager) reuses the cached access token.
	hits := fa.tokenHits.Load()
	m2 := NewManager(cfg, "reearth")
	m2.Keyring, m2.LockDir, m2.Getenv = m.Keyring, m.LockDir, m.Getenv
	ts2, _ := m2.TokenSource(r)
	if _, err := ts2.Token(); err != nil {
		t.Fatal(err)
	}
	if fa.tokenHits.Load() != hits {
		t.Fatal("cached access token was not reused")
	}

	// Invalidate forces a refresh.
	ts2.Invalidate()
	if _, err := ts2.Token(); err != nil {
		t.Fatal(err)
	}
	if fa.tokenHits.Load() != hits+1 {
		t.Fatal("Invalidate did not force a refresh")
	}
}

func TestSourceReauthOnInvalidGrant(t *testing.T) {
	fa := newFakeAuth0(t)
	m, _ := newTestManager(t, fa)
	_ = m.Keyring.Set("work", &credstore.Secret{Kind: config.KindOAuth, RefreshToken: "revoked"})
	r, _ := m.Resolve("", nil)
	ts, _ := m.TokenSource(r)
	_, err := ts.Token()
	var e *cmdutil.Error
	if !errors.As(err, &e) || e.Code != "auth.reauth_required" || e.Exit != cmdutil.ExitAuth {
		t.Fatalf("err = %#v", err)
	}
}

func TestResolveOrder(t *testing.T) {
	cfg, _ := config.LoadFile(t.TempDir() + "/c.yaml")
	for _, n := range []string{"a", "b", "c", "d"} {
		cfg.Accounts[n] = &config.Account{Env: EnvProd, Kind: config.KindOAuth}
	}
	cfg.Active = "a"
	env := map[string]string{}
	m := NewManager(cfg, "reearth")
	m.Getenv = func(k string) string { return env[k] }
	proj := &config.Project{Account: "c"}

	check := func(flag string, p *config.Project, want, source string) {
		t.Helper()
		r, err := m.Resolve(flag, p)
		if err != nil {
			t.Fatal(err)
		}
		if r.Name != want || r.Source != source {
			t.Fatalf("got %s/%s, want %s/%s", r.Name, r.Source, want, source)
		}
	}
	check("", nil, "a", SourceConfig)
	check("", proj, "c", SourceProject)
	env["REEARTH_ACCOUNT"] = "b"
	check("", proj, "b", SourceEnv)
	check("d", proj, "d", SourceFlag)

	env["REEARTH_TOKEN"] = "tok"
	r, _ := m.Resolve("", proj)
	if r.Source != SourceToken || r.Token != "tok" {
		t.Fatalf("REEARTH_TOKEN not honored: %+v", r)
	}
	if _, err := m.Resolve("missing", nil); err == nil {
		t.Fatal("unknown account should fail")
	}
}

func TestDetectFlow(t *testing.T) {
	mk := func(goos string, env map[string]string, files ...string) FlowEnv {
		return FlowEnv{
			GOOS:   goos,
			Getenv: func(k string) string { return env[k] },
			Exists: func(p string) bool {
				for _, f := range files {
					if f == p {
						return true
					}
				}
				return false
			},
		}
	}
	cases := []struct {
		name string
		env  FlowEnv
		web  bool
		dev  bool
		want Flow
	}{
		{"mac", mk("darwin", nil), false, false, FlowLoopback},
		{"ssh", mk("darwin", map[string]string{"SSH_CONNECTION": "x"}), false, false, FlowDevice},
		{"forced web over ssh", mk("darwin", map[string]string{"SSH_CONNECTION": "x"}), true, false, FlowLoopback},
		{"forced device", mk("darwin", nil), false, true, FlowDevice},
		{"linux desktop", mk("linux", map[string]string{"DISPLAY": ":0"}), false, false, FlowLoopback},
		{"linux headless", mk("linux", nil), false, false, FlowDevice},
		{"wsl", mk("linux", map[string]string{"WSL_DISTRO_NAME": "Ubuntu"}), false, false, FlowLoopback},
		{"container", mk("linux", map[string]string{"DISPLAY": ":0"}, "/.dockerenv"), false, false, FlowDevice},
		{"codespaces", mk("linux", map[string]string{"CODESPACES": "true", "DISPLAY": ":0"}), false, false, FlowDevice},
	}
	for _, c := range cases {
		if got := DetectFlow(c.env, c.web, c.dev); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}
