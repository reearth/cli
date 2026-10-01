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

	"github.com/reearth/cli/products/hello"
	"github.com/reearth/cli/sdk/app"
	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/core"
	"github.com/reearth/cli/sdk/iostreams"
)

var unified = app.Options{Name: "reearth", Products: []core.Product{hello.Product{}}}

type result struct {
	out, errOut string
	code        int
}

func setup(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("REEARTH_CONFIG_DIR", filepath.Join(dir, "config"))
	t.Setenv("REEARTH_CACHE_DIR", filepath.Join(dir, "cache"))
	t.Setenv("REEARTH_DATA_DIR", filepath.Join(dir, "data"))
	for _, k := range []string{"REEARTH_TOKEN", "REEARTH_ACCOUNT", "REEARTH_HELLO_TOKEN", "REEARTH_ENV"} {
		t.Setenv(k, "")
	}
	t.Chdir(dir)
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
func fakeAuth(t *testing.T) (*httptest.Server, *atomic.Int32) {
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
	t.Setenv("REEARTH_AUTH_DOMAIN", srv.URL)
	t.Setenv("REEARTH_AUTH_CLIENT_ID", "cli")
	t.Setenv("REEARTH_AUTH_AUDIENCE", "https://api.test")
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
	setup(t)
	_, revoked := fakeAuth(t)

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

	if r := run(t, unified, "", nil, "logout"); r.code != 0 || revoked.Load() != 1 {
		t.Fatalf("logout = %+v, revoked %d", r, revoked.Load())
	}
	if r := run(t, unified, "", nil, "hello", "me"); r.code != cmdutil.ExitAuth {
		t.Fatalf("after logout = %+v", r)
	}
}

func TestTokenAccountAndProductScope(t *testing.T) {
	setup(t)
	r := run(t, unified, "secret-token\n", nil, "login", "ci", "--with-token", "--product", "cms", "--insecure-storage")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	creds, _ := os.ReadFile(filepath.Join(os.Getenv("REEARTH_CONFIG_DIR"), "credentials.json"))
	if !strings.Contains(string(creds), "secret-token") {
		t.Fatal("token not stored")
	}
	cfg, _ := os.ReadFile(filepath.Join(os.Getenv("REEARTH_CONFIG_DIR"), "config.yaml"))
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

func TestDocs(t *testing.T) {
	setup(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/llms-full.txt" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("# 参照フィールドでモデルどうしをつなぐ\n\nSource: https://docs.reearth.io/ja/cms/reference-field/\n\n> 参照フィールドの手順。\n\n[モデル](/ja/cms/model/)\n"))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("REEARTH_DOCS_URL", srv.URL)

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
