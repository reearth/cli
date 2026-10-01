package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.yaml")
	c, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	c.Accounts["work"] = &Account{Env: "prod", Kind: KindOAuth, User: &User{Sub: "s", Email: "a@b"}}
	c.Active = "work"
	if err := c.Set("output", "json"); err != nil {
		t.Fatal(err)
	}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		st, _ := os.Stat(path)
		if st.Mode().Perm() != 0o600 {
			t.Errorf("mode = %v", st.Mode().Perm())
		}
	}
	c2, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if c2.Active != "work" || c2.Accounts["work"].User.Email != "a@b" || c2.Settings["output"] != "json" {
		t.Fatalf("round trip lost data: %+v", c2)
	}
}

func TestRemoveAccountMovesActive(t *testing.T) {
	c, _ := LoadFile(filepath.Join(t.TempDir(), "c.yaml"))
	c.Accounts["a"], c.Accounts["b"] = &Account{}, &Account{}
	c.Active = "a"
	c.RemoveAccount("a")
	if c.Active != "b" {
		t.Fatalf("active = %q", c.Active)
	}
	c.RemoveAccount("b")
	if c.Active != "" {
		t.Fatalf("active = %q", c.Active)
	}
}

func TestSettings(t *testing.T) {
	c, _ := LoadFile(filepath.Join(t.TempDir(), "c.yaml"))
	if err := c.Set("output", "xml"); err == nil {
		t.Error("invalid value accepted")
	}
	if err := c.Set("nope", "x"); err == nil {
		t.Error("unknown key accepted")
	}
	v, o, _ := c.Get("prompt")
	if v != "enabled" || o != OriginDefault {
		t.Errorf("default = %s/%s", v, o)
	}
	t.Setenv("REEARTH_PROMPT", "disabled")
	_ = c.Set("prompt", "enabled")
	if v, o, _ := c.Get("prompt"); v != "disabled" || o != OriginEnv {
		t.Errorf("env override = %s/%s", v, o)
	}
}

func TestFindProject(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".reearth.yaml"), []byte("account: work\ncms:\n  project: demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := FindProject(deep)
	if err != nil || p == nil {
		t.Fatalf("project not found: %v", err)
	}
	if p.Account != "work" {
		t.Errorf("account = %q", p.Account)
	}
	if v, _ := p.Value("cms", "project"); v != "demo" {
		t.Errorf("cms.project = %q", v)
	}
}

func TestProjectRejectsSecrets(t *testing.T) {
	for file, key := range map[string]string{
		"cms:\n  token: secret_abc\n":                       "cms.token",
		"cms:\n  auth:\n    Token: abc\n":                   "cms.auth.Token",
		"cms:\n  hooks:\n    - name: a\n      API_KEY: x\n": "cms.hooks[0].API_KEY",
		"secrets:\n  a: b\n":                                "secrets",
	} {
		root := t.TempDir()
		_ = os.WriteFile(filepath.Join(root, ".reearth.yaml"), []byte(file), 0o644)
		_, err := FindProject(root)
		if err == nil || !strings.Contains(err.Error(), key+" looks like a secret") {
			t.Errorf("%q: err = %v", file, err)
		}
	}
}

func TestSaveKeepsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	_ = os.WriteFile(path, []byte("version: 1\nfuture_field: keepme\nsettings:\n  bogus: 1\n"), 0o600)
	c, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Set("output", "json"); err != nil {
		t.Fatal(err)
	}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	for _, want := range []string{"future_field: keepme\n", "bogus: 1\n", "output: json\n"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("saved config lacks %q:\n%s", want, b)
		}
	}
}

func TestSaveKeepsNestedUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	in := `version: 1
accounts:
  work:
    env: prod
    kind: oauth
    future_acct_field: a
    user:
      sub: s
      future_user_field: b
envs:
  mine:
    auth:
      domain: d
      client_id: c
      future_auth_field: c
    products:
      cms:
        base_url: u
        future_product_field: d
    future_env_field: e
`
	_ = os.WriteFile(path, []byte(in), 0o600)
	c, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Set("browser", "echo"); err != nil {
		t.Fatal(err)
	}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	for _, want := range []string{
		"future_acct_field: a\n", "future_user_field: b\n", "future_auth_field: c\n",
		"future_product_field: d\n", "future_env_field: e\n", "browser: echo\n",
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("saved config lacks %q:\n%s", want, b)
		}
	}
}

func TestInvalidVersion(t *testing.T) {
	for _, v := range []string{"0", "-5"} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		_ = os.WriteFile(path, []byte("version: "+v+"\n"), 0o600)
		if _, err := LoadFile(path); err == nil || !strings.Contains(err.Error(), "invalid config version") {
			t.Errorf("version %s: err = %v", v, err)
		}
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	_ = os.WriteFile(path, []byte("active: a\n"), 0o600)
	if c, err := LoadFile(path); err != nil || c.Version != 1 {
		t.Errorf("missing version: %v", err)
	}
}
