package corecmd_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/reearth/cli/products/hello"
	"github.com/reearth/cli/sdk/app"
	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/core"
	"github.com/reearth/cli/sdk/envvar"
	"github.com/reearth/cli/sdk/envvar/envvartest"
	"github.com/reearth/cli/sdk/iostreams"
)

var opts = app.Options{Name: "reearth", Products: []core.Product{hello.Product{}}}

type result struct {
	out, errOut string
	code        int
}

// setup runs the test in a temporary directory with an environment that
// holds only the CLI's directories; the real environment is not visible.
func setup(t *testing.T) envvar.Map {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	return envvartest.Fake(t, envvar.Map{
		"REEARTH_CONFIG_DIR": filepath.Join(dir, "config"),
		"REEARTH_CACHE_DIR":  filepath.Join(dir, "cache"),
		"REEARTH_DATA_DIR":   filepath.Join(dir, "data"),
	})
}

func run(t *testing.T, args ...string) result {
	t.Helper()
	ios, _, out, errOut := iostreams.Test()
	f := app.NewFactory(opts, ios)
	f.OpenBrowser = browser
	code := app.Execute(f, opts, args)
	return result{out.String(), errOut.String(), code}
}

// fakeAuth serves token and userinfo. Setting revoked makes refreshes fail.
func fakeAuth(t *testing.T, env envvar.Map) (revoked *atomic.Bool, refreshes *atomic.Int32) {
	revoked, refreshes = &atomic.Bool{}, &atomic.Int32{}
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		if r.Form.Get("grant_type") == "refresh_token" {
			refreshes.Add(1)
			if revoked.Load() {
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid_grant"})
				return
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "refresh_token": "rt", "token_type": "Bearer", "expires_in": 3600})
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"sub": "auth0|kana", "email": "kana@example.com"})
	})
	mux.HandleFunc("/oauth/revoke", func(w http.ResponseWriter, r *http.Request) {})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	env["REEARTH_AUTH_DOMAIN"] = srv.URL
	env["REEARTH_AUTH_CLIENT_ID"] = "cli"
	env["REEARTH_AUTH_AUDIENCE"] = "https://api.test"
	return revoked, refreshes
}

func browser(authURL string) error {
	u, err := url.Parse(authURL)
	if err != nil {
		return err
	}
	q := u.Query()
	go func() {
		resp, err := http.Get(q.Get("redirect_uri") + "?code=c&state=" + url.QueryEscape(q.Get("state")))
		if err == nil {
			_ = resp.Body.Close()
		}
	}()
	return nil
}

func login(t *testing.T) {
	t.Helper()
	if r := run(t, "login", "--web", "--insecure-storage"); r.code != 0 {
		t.Fatalf("login: %+v", r)
	}
}

// errCode reads the JSON error, which is the last line of stderr: warnings
// printed before it are plain text.
func errCode(t *testing.T, r result) string {
	t.Helper()
	var e struct {
		Error cmdutil.Error `json:"error"`
	}
	lines := strings.Split(strings.TrimRight(r.errOut, "\n"), "\n")
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &e); err != nil {
		t.Fatalf("stderr is not a JSON error: %q", r.errOut)
	}
	return e.Error.Code
}

func TestAuthStatusDetectsRevokedSession(t *testing.T) {
	env := setup(t)
	revoked, refreshes := fakeAuth(t, env)
	login(t)
	if r := run(t, "auth", "status", "--jq", ".[0].valid"); r.out != "true\n" {
		t.Fatalf("auth status = %+v", r)
	}
	revoked.Store(true)
	before := refreshes.Load()
	r := run(t, "auth", "status", "--jq", ".[0].valid")
	if r.out != "false\n" || r.code != cmdutil.ExitAuth || refreshes.Load() == before || errCode(t, r) != "auth.invalid" {
		t.Fatalf("auth status after revocation = %+v (refreshes %d)", r, refreshes.Load()-before)
	}
}

func TestAuthTokenRefusedInExtension(t *testing.T) {
	env := setup(t)
	fakeAuth(t, env)
	login(t)
	env["REEARTH_EXTENSION"] = "1"
	r := run(t, "auth", "token", "--json")
	if r.code != cmdutil.ExitError || r.out != "" || errCode(t, r) != "auth.token_refused_extension" {
		t.Fatalf("%+v", r)
	}
}

func TestLogoutNeedsConfirmation(t *testing.T) {
	env := setup(t)
	fakeAuth(t, env)
	login(t)
	r := run(t, "logout", "--json")
	if r.code != cmdutil.ExitUsage || errCode(t, r) != "confirmation_required" {
		t.Fatalf("logout without --yes = %+v", r)
	}
	if r := run(t, "whoami"); r.code != 0 {
		t.Fatalf("credentials removed without confirmation: %+v", r)
	}
	if r := run(t, "logout", "--yes"); r.code != 0 {
		t.Fatalf("logout --yes = %+v", r)
	}
}

func TestWhoamiReportsProductTokenOverride(t *testing.T) {
	env := setup(t)
	fakeAuth(t, env)
	login(t)
	if r := run(t, "whoami", "--json"); strings.Contains(r.out, "token_overrides") {
		t.Fatalf("unexpected override: %s", r.out)
	}
	env["REEARTH_HELLO_TOKEN"] = "x"
	r := run(t, "whoami", "--jq", ".token_overrides[0]")
	if r.out != "REEARTH_HELLO_TOKEN\n" {
		t.Fatalf("%+v", r)
	}
	// Plain output carries the same data.
	r = run(t, "whoami")
	if cols := strings.Split(strings.TrimRight(r.out, "\n"), "\t"); len(cols) != 5 || cols[4] != "REEARTH_HELLO_TOKEN" {
		t.Fatalf("plain whoami = %q", r.out)
	}
}

func TestUseWithoutArgumentNamesIt(t *testing.T) {
	env := setup(t)
	fakeAuth(t, env)
	login(t)
	r := run(t, "use")
	if r.code != cmdutil.ExitUsage || !strings.Contains(r.errOut, "reearth use <account>") {
		t.Fatalf("%+v", r)
	}
}

func TestDoctorFailureReportsError(t *testing.T) {
	env := setup(t)
	dir := env["REEARTH_CONFIG_DIR"]
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("accounts: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := run(t, "doctor", "--json")
	if r.code != cmdutil.ExitError || !strings.Contains(r.out, `"fail"`) || errCode(t, r) != "doctor.failed" {
		t.Fatalf("%+v", r)
	}
	// The config check reports the file; it is not also "ignored".
	if strings.Contains(r.errOut, "ignoring a file") {
		t.Fatalf("doctor warned about the file it reports: %q", r.errOut)
	}
}

func TestWhoamiTokenAccountStorage(t *testing.T) {
	env := setup(t)
	env["REEARTH_TOKEN"] = "x"
	if r := run(t, "whoami", "--jq", ".storage"); r.code != 0 || r.out != "env\n" {
		t.Fatalf("%+v", r)
	}
}
