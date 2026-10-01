package app_test

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

	"github.com/spf13/cobra"

	"github.com/reearth/cli/products/hello"
	"github.com/reearth/cli/sdk/app"
	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/core"
	"github.com/reearth/cli/sdk/envvar"
	"github.com/reearth/cli/sdk/envvar/envvartest"
	"github.com/reearth/cli/sdk/iostreams"
)

var unified = app.Options{Name: "reearth", Products: []core.Product{hello.Product{}}}

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

func run(t *testing.T, o app.Options, stdin string, open func(string) error, args ...string) result {
	t.Helper()
	ios, in, out, errOut := iostreams.Test()
	in.WriteString(stdin)
	f := app.NewFactory(o, ios)
	if open != nil {
		f.OpenBrowser = open
	}
	code := app.Execute(f, o, args)
	return result{out.String(), errOut.String(), code}
}

// fakeAuth serves the endpoints the CLI uses: token (code + refresh), userinfo, revoke.
func fakeAuth(t *testing.T, env envvar.Map) (*httptest.Server, *atomic.Int32) {
	var revoked atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("grant_type") == "authorization_code" && r.Form.Get("code_verifier") == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "refresh_token": "rt", "token_type": "Bearer", "expires_in": 3600})
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer at" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"sub": "auth0|kana", "email": "kana@example.com", "name": "Kana"})
	})
	mux.HandleFunc("/oauth/revoke", func(w http.ResponseWriter, r *http.Request) { revoked.Add(1) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	env["REEARTH_AUTH_DOMAIN"] = srv.URL
	env["REEARTH_AUTH_CLIENT_ID"] = "cli"
	env["REEARTH_AUTH_AUDIENCE"] = "https://api.test"
	return srv, &revoked
}

// browser simulates the user approving the login in a browser.
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

func TestHelloWorld(t *testing.T) {
	setup(t)
	r := run(t, unified, "", nil, "hello", "world", "Kana", "--json")
	if r.code != 0 || !strings.Contains(r.out, `"message": "Hello, Kana!"`) {
		t.Fatalf("%+v", r)
	}
	r = run(t, unified, "", nil, "hello", "world", "--jq", ".message")
	if r.out != "Hello, world!\n" {
		t.Fatalf("jq output = %q", r.out)
	}
}

func TestErrorsAndExitCodes(t *testing.T) {
	setup(t)
	r := run(t, unified, "", nil, "whoami", "--json")
	if r.code != cmdutil.ExitAuth {
		t.Fatalf("code = %d", r.code)
	}
	var e struct {
		Error cmdutil.Error `json:"error"`
	}
	if err := json.Unmarshal([]byte(r.errOut), &e); err != nil || e.Error.Code != "auth.not_logged_in" {
		t.Fatalf("stderr = %q (%v)", r.errOut, err)
	}
	if r.out != "" {
		t.Fatalf("stdout must stay clean: %q", r.out)
	}

	if r := run(t, unified, "", nil, "nope"); r.code != cmdutil.ExitUsage {
		t.Fatalf("unknown command code = %d", r.code)
	}
	if r := run(t, unified, "", nil, "use", "a", "b"); r.code != cmdutil.ExitUsage {
		t.Fatalf("bad args code = %d", r.code)
	}
	if r := run(t, unified, "", nil, "logout", "--all"); r.code != cmdutil.ExitAuth {
		t.Fatalf("logout without accounts = %d", r.code)
	}
}

func TestOAuthLoginAndAuthenticatedCalls(t *testing.T) {
	env := setup(t)
	_, revoked := fakeAuth(t, env)

	r := run(t, unified, "", browser, "login", "--web", "--insecure-storage", "--json")
	if r.code != 0 {
		t.Fatalf("login failed: %+v", r)
	}
	var acc struct {
		Name string `json:"name"`
		User struct {
			Email string `json:"email"`
		} `json:"user"`
	}
	if err := json.Unmarshal([]byte(r.out), &acc); err != nil || acc.Name != "kana" || acc.User.Email != "kana@example.com" {
		t.Fatalf("login output = %q", r.out)
	}

	// Re-login finds the same account by subject instead of creating a new one.
	if r := run(t, unified, "", browser, "login", "--web", "--insecure-storage"); r.code != 0 {
		t.Fatalf("re-login failed: %+v", r)
	}
	if r := run(t, unified, "", nil, "account", "list", "--jq", "length"); r.out != "1\n" {
		t.Fatalf("accounts = %q", r.out)
	}

	if r := run(t, unified, "", nil, "hello", "me", "--jq", ".email"); r.out != "kana@example.com\n" {
		t.Fatalf("hello me = %+v", r)
	}
	if r := run(t, unified, "", nil, "api", "hello", "/userinfo", "--jq", ".sub"); r.out != "auth0|kana\n" {
		t.Fatalf("api = %+v", r)
	}
	if r := run(t, unified, "", nil, "api", "hello", "https://evil.example/x"); r.code != cmdutil.ExitUsage {
		t.Fatalf("absolute URL accepted: %+v", r)
	}
	if r := run(t, unified, "", nil, "auth", "status", "--jq", ".[0].valid"); r.out != "true\n" {
		t.Fatalf("auth status = %+v", r)
	}

	if r := run(t, unified, "", nil, "logout", "--yes"); r.code != 0 || revoked.Load() != 1 {
		t.Fatalf("logout = %+v, revoked %d", r, revoked.Load())
	}
	if r := run(t, unified, "", nil, "hello", "me"); r.code != cmdutil.ExitAuth {
		t.Fatalf("after logout = %+v", r)
	}
}

func TestTokenAccountAndProductScope(t *testing.T) {
	env := setup(t)
	r := run(t, unified, "secret-token\n", nil, "login", "ci", "--with-token", "--product", "cms", "--insecure-storage")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	creds, _ := os.ReadFile(filepath.Join(env["REEARTH_CONFIG_DIR"], "credentials.json"))
	if !strings.Contains(string(creds), "secret-token") {
		t.Fatal("token not stored")
	}
	cfg, _ := os.ReadFile(filepath.Join(env["REEARTH_CONFIG_DIR"], "config.yaml"))
	if strings.Contains(string(cfg), "secret-token") {
		t.Fatal("secret leaked into config.yaml")
	}
	// A cms-only token must not be sent to another product.
	r = run(t, unified, "", nil, "hello", "me")
	if r.code != cmdutil.ExitAuth || !strings.Contains(r.errOut, "cms") {
		t.Fatalf("%+v", r)
	}
}

func TestSkillsInstall(t *testing.T) {
	setup(t)
	dir := t.TempDir()
	if r := run(t, unified, "", nil, "skills", "install", "--dir", dir); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	b, err := os.ReadFile(filepath.Join(dir, "reearth", "SKILL.md"))
	if err != nil || !strings.Contains(string(b), "`reearth search \"<task>\"`") {
		t.Fatalf("SKILL.md = %q (%v)", b, err)
	}
}

func TestHelpTopics(t *testing.T) {
	setup(t)
	r := run(t, unified, "", nil, "help", "exit-codes")
	if r.code != 0 || !strings.Contains(r.out, "Run `reearth login`") {
		t.Fatalf("%+v", r)
	}
	if r := run(t, unified, "", nil, "--help"); !strings.Contains(r.out, "Help topics") || !strings.Contains(r.out, "environment") {
		t.Fatalf("root help lacks topics:\n%s", r.out)
	}
	if r := run(t, unified, "", nil, "search", "what", "exit", "code", "4", "means", "--jq", ".[0].command"); r.out != "reearth help exit-codes\n" {
		t.Fatalf("search = %+v", r)
	}
}

func TestStandalone(t *testing.T) {
	setup(t)
	o := app.Options{Name: "reearth-hello", Products: []core.Product{hello.Product{}}, Standalone: true}
	if r := run(t, o, "", nil, "world"); r.out != "Hello, world!\n" {
		t.Fatalf("%+v", r)
	}
	r := run(t, o, "", nil, "search", "greeting")
	if !strings.HasPrefix(r.out, "reearth-hello world\t") {
		t.Fatalf("standalone search = %+v", r)
	}
	if r := run(t, o, "", nil, "help", "exit-codes"); !strings.Contains(r.out, "Run `reearth-hello login`") {
		t.Fatalf("standalone topic = %+v", r)
	}
	if r := run(t, o, "", nil, "login", "--help"); r.code != 0 || !strings.Contains(r.out, "reearth-hello login") {
		t.Fatalf("%+v", r)
	}
}

func TestSearch(t *testing.T) {
	setup(t)
	r := run(t, unified, "", nil, "search", "print", "a", "greeting", "--jq", ".[0].command")
	if r.code != 0 || r.out != "reearth hello world\n" {
		t.Fatalf("%+v", r)
	}
	if r := run(t, unified, "", nil, "search", "zzzz", "--json"); r.code != 0 || strings.TrimSpace(r.out) != "[]" {
		t.Fatalf("no match = %+v", r)
	}
}

func TestAgentHelpNote(t *testing.T) {
	setup(t)
	help := func(agent string, args ...string) string {
		ios, _, out, _ := iostreams.Test()
		ios.Agent = agent
		if code := app.Execute(app.NewFactory(unified, ios), unified, args); code != 0 {
			t.Fatalf("%v: exit %d", args, code)
		}
		return out.String()
	}
	const note = "`reearth search \"<task>\"`"
	if !strings.Contains(help("claude-code", "hello", "--help"), note) {
		t.Error("group help lacks the search note for agents")
	}
	if strings.Contains(help("claude-code", "hello", "world", "--help"), note) {
		t.Error("leaf help must not carry the search note")
	}
	if strings.Contains(help("", "hello", "--help"), note) {
		t.Error("humans must not see the agent note")
	}
}

// TestFakeEnvHidesRealEnv: under envvartest.Fake, the process environment
// does not reach the CLI, so a developer's shell cannot change test results.
func TestFakeEnvHidesRealEnv(t *testing.T) {
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("REEARTH_TOKEN", "x")
	setup(t)
	run := func(args ...string) (int, string) {
		ios := iostreams.System()
		if ios.Agent != "" {
			t.Fatalf("agent detected: %q", ios.Agent)
		}
		var out strings.Builder
		ios.In, ios.Out, ios.ErrOut = strings.NewReader(""), &out, &out
		ios.SetTTY(false, false, false)
		return app.Execute(app.NewFactory(unified, ios), unified, args), out.String()
	}
	if _, out := run("hello", "--help"); strings.Contains(out, "`reearth search") {
		t.Error("agent help note shown")
	}
	if code, out := run("whoami", "--json"); code == 0 || strings.Contains(out, `"source": "REEARTH_TOKEN"`) {
		t.Errorf("REEARTH_TOKEN seen: exit %d, %s", code, out)
	}
}

func TestDocs(t *testing.T) {
	env := setup(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/llms-full.txt" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("# 参照フィールドでモデルどうしをつなぐ\n\nSource: https://docs.reearth.io/ja/cms/reference-field/\n\n> 参照フィールドの手順。\n\n[モデル](/ja/cms/model/)\n"))
	}))
	t.Cleanup(srv.Close)
	env["REEARTH_DOCS_URL"] = srv.URL

	r := run(t, unified, "", nil, "docs", "search", "参照フィールド", "--jq", ".[0].id")
	if r.code != 0 || r.out != "ja/cms/reference-field\n" {
		t.Fatalf("search = %+v", r)
	}
	r = run(t, unified, "", nil, "docs", "read", "ja/cms/reference-field")
	if r.code != 0 || !strings.Contains(r.out, "[モデル]("+srv.URL+"/ja/cms/model/)") {
		t.Fatalf("read = %+v", r)
	}
	if r := run(t, unified, "", nil, "docs", "read", "nope"); r.code != cmdutil.ExitNotFound {
		t.Fatalf("missing page = %+v", r)
	}
}

func TestUsageErrors(t *testing.T) {
	setup(t)
	for _, tc := range []struct {
		args []string
		msg  string
	}{
		{[]string{"hello", "nope"}, `unknown command "nope" for "reearth hello"`},
		{[]string{"config", "nope"}, `unknown command "nope" for "reearth config"`},
		{[]string{"help", "nope"}, `unknown help topic "nope"`},
		{[]string{"help", "hello", "nope"}, `unknown help topic "hello nope"`},
	} {
		r := run(t, unified, "", nil, tc.args...)
		if r.code != cmdutil.ExitUsage || r.out != "" || !strings.Contains(r.errOut, tc.msg) {
			t.Errorf("%v: %+v", tc.args, r)
		}
	}
	if r := run(t, unified, "", nil, "hello"); r.code != 0 || !strings.Contains(r.out, "Usage") {
		t.Errorf("group without args: %+v", r)
	}
	if r := run(t, unified, "", nil, "help", "hello"); r.code != 0 || !strings.Contains(r.out, "reearth hello <command>") {
		t.Errorf("help hello: %+v", r)
	}
}

func TestJSONErrors(t *testing.T) {
	setup(t)
	for _, args := range [][]string{
		{"whoami", "-o", "JSON"},
		{"hello", "world", "--bogus", "--json"},
		{"hello", "world", "--bogus", "--jq", ".x"},
		{"hello", "world", "--bogus", "--output=ndjson"},
		{"hello", "world", "--bogus", "-ojson"},
		{"hello", "world", "--bogus", "-yojson"},
		{"hello", "world", "--bogus", "-qo", "json"},
	} {
		r := run(t, unified, "", nil, args...)
		var e struct {
			Error cmdutil.Error `json:"error"`
		}
		if err := json.Unmarshal([]byte(r.errOut), &e); err != nil || e.Error.Code == "" {
			t.Errorf("%v: stderr = %q", args, r.errOut)
		}
	}
	if r := run(t, unified, "", nil, "hello", "world", "--bogus", "--", "--json"); strings.HasPrefix(r.errOut, "{") {
		t.Errorf("--json after -- is an argument: %q", r.errOut)
	}
}

func TestNoColor(t *testing.T) {
	setup(t)
	exec := func(args ...string) result {
		ios, _, out, errOut := iostreams.Test()
		ios.SetColorEnabled(true)
		code := app.Execute(app.NewFactory(unified, ios), unified, args)
		return result{out.String(), errOut.String(), code}
	}
	if r := exec("hello", "--help"); !strings.Contains(r.out, "\x1b[") {
		t.Fatal("help is not colored without --no-color")
	}
	if r := exec("hello", "--help", "--no-color"); strings.Contains(r.out, "\x1b[") {
		t.Errorf("help colored with --no-color: %q", r.out)
	}
	if r := exec("hello", "world", "--bogus", "--no-color"); r.code != cmdutil.ExitUsage || strings.Contains(r.errOut, "\x1b[") {
		t.Errorf("flag error colored with --no-color: %+v", r)
	}
}

func TestSymlinkDispatch(t *testing.T) {
	setup(t)
	host := unified
	host.Extra = func(f *core.Factory) []*cobra.Command {
		return []*cobra.Command{{Use: "upgrade", Run: func(*cobra.Command, []string) {}}}
	}
	if o := app.SymlinkOptions(host, "/usr/local/bin/reearth"); o.Standalone {
		t.Fatal("plain name dispatched")
	}
	if o := app.SymlinkOptions(host, "/usr/local/bin/reearth-nope"); o.Standalone {
		t.Fatal("unknown product dispatched")
	}
	o := app.SymlinkOptions(host, "/usr/local/bin/reearth-hello.exe")
	if o.Name != "reearth-hello" || !o.Standalone || len(o.Products) != 1 || o.Extra != nil {
		t.Fatalf("options = %+v", o)
	}
	if r := run(t, o, "", nil, "search", "greeting"); !strings.HasPrefix(r.out, "reearth-hello world\t") {
		t.Errorf("search = %+v", r)
	}
	if r := run(t, o, "", nil, "version"); !strings.HasPrefix(r.out, "reearth-hello ") {
		t.Errorf("version = %+v", r)
	}
	if r := run(t, o, "", nil, "skills", "install", "--print"); r.code != 0 || !strings.Contains(r.out, "name: reearth-hello") {
		t.Errorf("skills install --print = %+v", r)
	}
	if r := run(t, o, "", nil, "upgrade"); r.code != cmdutil.ExitUsage {
		t.Errorf("host-only command available: %+v", r)
	}
}

func TestBrokenFilesWarn(t *testing.T) {
	env := setup(t)
	dir := env["REEARTH_CONFIG_DIR"]
	_ = os.MkdirAll(dir, 0o700)
	_ = os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("settings: [\n"), 0o600)
	r := run(t, unified, "", nil, "hello", "world")
	if r.code != 0 || r.out != "Hello, world!\n" || strings.Count(r.errOut, "config.yaml") != 1 {
		t.Fatalf("broken config.yaml: %+v", r)
	}
	_ = os.Remove(filepath.Join(dir, "config.yaml"))

	_ = os.WriteFile(".reearth.yaml", []byte("cms:\n  auth:\n    token: abc\n"), 0o644)
	if r := run(t, unified, "", nil, "whoami"); r.code == 0 || !strings.Contains(r.errOut, "cms.auth.token looks like a secret") {
		t.Fatalf("a broken project file must stop account resolution: %+v", r)
	}
	env["REEARTH_TOKEN"] = "x"
	r = run(t, unified, "", nil, "whoami", "--json")
	if r.code != 0 || !strings.Contains(r.out, `"source": "REEARTH_TOKEN"`) || !strings.Contains(r.errOut, ".reearth.yaml") {
		t.Fatalf("REEARTH_TOKEN with a broken project file: %+v", r)
	}
	if r := run(t, unified, "", nil, "config", "list", "--json"); strings.Contains(r.out, "abc") || !strings.Contains(r.errOut, ".reearth.yaml") {
		t.Fatalf("config list: %+v", r)
	}
}

func TestBrokenConfig(t *testing.T) {
	for name, content := range map[string]string{"bad yaml": "settings: [\n", "bad version": "version: -5\n"} {
		t.Run(name, func(t *testing.T) {
			env := setup(t)
			dir := env["REEARTH_CONFIG_DIR"]
			_ = os.MkdirAll(dir, 0o700)
			_ = os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0o600)
			// Commands that need the file fail with one error that says how to fix it.
			for _, args := range [][]string{{"config", "list"}, {"whoami"}, {"account", "list"}, {"config", "set", "browser", "echo"}} {
				r := run(t, unified, "", nil, args...)
				if r.code != cmdutil.ExitError || strings.Contains(r.errOut, "ignoring") || strings.Count(r.errOut, "config.yaml") != 1 || !strings.Contains(r.errOut, "fix the file") {
					t.Errorf("%v: %+v", args, r)
				}
			}
			// Commands that do not need it run with one warning.
			for _, args := range [][]string{{"config", "path"}, {"version"}, {"search", "login"}} {
				r := run(t, unified, "", nil, args...)
				if r.code != 0 || r.out == "" || strings.Count(r.errOut, "ignoring a file that cannot be loaded") != 1 {
					t.Errorf("%v: %+v", args, r)
				}
			}
			env["REEARTH_TOKEN"] = "x"
			r := run(t, unified, "", nil, "whoami", "--json")
			if r.code != 0 || !strings.Contains(r.out, `"source": "REEARTH_TOKEN"`) || strings.Count(r.errOut, "ignoring a file that cannot be loaded") != 1 {
				t.Errorf("REEARTH_TOKEN whoami: %+v", r)
			}
			if r := run(t, unified, "", nil, "whoami", "--account", "a"); r.code != cmdutil.ExitError || strings.Contains(r.errOut, "ignoring") {
				t.Errorf("--account needs the file: %+v", r)
			}
		})
	}
}

func TestInvalidOutputSetting(t *testing.T) {
	env := setup(t)
	env["REEARTH_OUTPUT"] = "xml"
	ios, _, _, errOut := iostreams.Test()
	ios.SetTTY(false, true, false)
	code := app.Execute(app.NewFactory(unified, ios), unified, []string{"version"})
	if code != cmdutil.ExitUsage || !strings.Contains(errOut.String(), "REEARTH_OUTPUT") || strings.Contains(errOut.String(), "--help") {
		t.Fatalf("code %d, stderr %q", code, errOut.String())
	}
}
