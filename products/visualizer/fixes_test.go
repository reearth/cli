package visualizer_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/reearth/cli/sdk/cmdutil"
)

// Display names are written as quoted strings, so YAML and JS stay valid.
func TestInitNamesAreQuoted(t *testing.T) {
	setup(t)
	name := `Demo: #1 "map" Bob's`
	if r := run(t, "vis", "plugin", "init", "p", "--name", name); r.code != 0 {
		t.Fatalf("init: %+v", r)
	}
	if r := run(t, "vis", "plugin", "validate", "--dir", "p", "--json"); r.code != 0 || !strings.Contains(r.out, `"valid": true`) {
		t.Fatalf("validate: %+v", r)
	}
	if r := run(t, "vis", "plugin", "add", "legend", "--dir", "p", "--name", "Legend: all"); r.code != 0 {
		t.Fatalf("add: %+v", r)
	}
	// Plain where possible, quoted only where YAML needs it.
	y := read(t, "p/reearth.yml")
	for _, want := range []string{"id: p\n", "- id: legend\n", `name: 'Legend: all'`} {
		if !strings.Contains(y, want) {
			t.Errorf("reearth.yml missing %q:\n%s", want, y)
		}
	}
	if js := read(t, "p/p.js"); !strings.Contains(js, `?? "Hello from Demo: #1 \"map\" Bob's";`) {
		t.Errorf("p.js does not quote the name:\n%s", js)
	}
}

// init accepts only IDs the Visualizer server accepts.
func TestInitServerPluginID(t *testing.T) {
	setup(t)
	for _, id := range []string{"my.plugin", "a:b"} {
		if r := run(t, "vis", "plugin", "init", "x", "--id", id); r.code != cmdutil.ExitUsage {
			t.Errorf("init --id %s = %d", id, r.code)
		}
	}
	// A directory name that is no valid ID needs --id.
	if r := run(t, "vis", "plugin", "init", "plugin.v2"); r.code != cmdutil.ExitUsage || !strings.Contains(r.errOut, "--id") {
		t.Errorf("init plugin.v2 = %+v", r)
	}
}

// init never overwrites files that are already there.
func TestInitKeepsExistingFiles(t *testing.T) {
	setup(t)
	if err := os.MkdirAll("repo", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("repo/repo.js", []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := run(t, "vis", "plugin", "init", "repo")
	if r.code != cmdutil.ExitUsage || !strings.Contains(r.errOut, "repo.js") {
		t.Fatalf("init = %+v", r)
	}
	if read(t, "repo/repo.js") != "mine" {
		t.Error("repo.js was overwritten")
	}
	if _, err := os.Stat("repo/reearth.yml"); err == nil {
		t.Error("files were written despite the conflict")
	}
}

// A corrupt cached template is downloaded again instead of failing for a day.
func TestInitTSCorruptCache(t *testing.T) {
	hits := setup(t)
	if r := run(t, "vis", "plugin", "init", "a", "--template", "ts"); r.code != 0 {
		t.Fatalf("first init: %+v", r)
	}
	cached, _ := filepath.Glob(filepath.Join("cache", "visualizer", "*"))
	if len(cached) == 0 {
		t.Fatal("nothing cached")
	}
	for _, p := range cached {
		_ = os.WriteFile(p, []byte("not a tarball"), 0o644)
	}
	before := *hits
	if r := run(t, "vis", "plugin", "init", "b", "--template", "ts"); r.code != 0 {
		t.Fatalf("init with a corrupt cache: %+v", r)
	}
	if *hits != before+1 {
		t.Errorf("downloads = %d, want 1", *hits-before)
	}
}

// add reports why reearth.yml cannot be read instead of "not found".
func TestAddUnreadableManifest(t *testing.T) {
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("permissions are not enforced")
	}
	setup(t)
	if r := run(t, "vis", "plugin", "init", "p"); r.code != 0 {
		t.Fatalf("init: %+v", r)
	}
	if err := os.Chmod("p/reearth.yml", 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod("p/reearth.yml", 0o644) })
	r := run(t, "vis", "plugin", "add", "x", "--dir", "p")
	if r.code == 0 || r.code == cmdutil.ExitNotFound || !strings.Contains(r.errOut, "permission denied") {
		t.Errorf("add = %+v", r)
	}
}
