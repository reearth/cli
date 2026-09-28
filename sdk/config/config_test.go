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
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, ".reearth.yaml"), []byte("cms:\n  token: secret_abc\n"), 0o644)
	_, err := FindProject(root)
	if err == nil || !strings.Contains(err.Error(), "looks like a secret") {
		t.Fatalf("err = %v", err)
	}
}
