package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/reearth/cli/products/visualizer"
	"github.com/reearth/cli/sdk/app"
	"github.com/reearth/cli/sdk/cmdtree"
	"github.com/reearth/cli/sdk/envvar"
	"github.com/reearth/cli/sdk/envvar/envvartest"
	"github.com/reearth/cli/sdk/iostreams"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/commands.json from the current command tree")

func newRoot(t *testing.T) *cobra.Command {
	t.Helper()
	dir := t.TempDir()
	envvartest.Fake(t, envvar.Map{
		"REEARTH_CONFIG_DIR": filepath.Join(dir, "config"),
		"REEARTH_CACHE_DIR":  filepath.Join(dir, "cache"),
		"REEARTH_DATA_DIR":   filepath.Join(dir, "data"),
	})
	o := options()
	ios, _, _, _ := iostreams.Test()
	root := app.NewRoot(app.NewFactory(o, ios), o)
	cmdtree.InitDefaults(root)
	return root
}

// TestCommandSurface pins every command, alias, argument and flag of the
// distributed binary, including the completion command and --version that
// cobra adds. A diff in testdata/commands.json shows reviewers what a change adds,
// renames or removes; renaming or removing breaks users' scripts.
// Run `make golden` after an intended change.
func TestCommandSurface(t *testing.T) {
	b, err := json.MarshalIndent(cmdtree.Walk(newRoot(t)), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, '\n')
	golden := filepath.Join("testdata", "commands.json")
	if *updateGolden {
		if err := os.WriteFile(golden, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run `make golden`)", err)
	}
	if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), b) {
		t.Errorf("the command surface changed; review the diff of %s after running `make golden`", golden)
	}
}

func TestCommandDescriptions(t *testing.T) {
	for _, err := range cmdtree.Lint(cmdtree.Walk(newRoot(t))) {
		t.Error(err)
	}
}

// TestSkillSearches checks that the searches product skills tell agents to
// run find the commands they mean.
func TestSkillSearches(t *testing.T) {
	dir := t.TempDir()
	envvartest.Fake(t, envvar.Map{
		"REEARTH_CONFIG_DIR": filepath.Join(dir, "config"),
		"REEARTH_CACHE_DIR":  filepath.Join(dir, "cache"),
		"REEARTH_DATA_DIR":   filepath.Join(dir, "data"),
	})
	want := map[string]string{
		"validate a plugin":       "reearth visualizer plugin validate",
		"zip a plugin for upload": "reearth visualizer plugin pack",
	}
	var found int
	for _, s := range (visualizer.Product{}).Skills() {
		for _, m := range regexp.MustCompile("`reearth search \"([^\"]+)\"`").FindAllStringSubmatch(s.Content, -1) {
			found++
			cmd, ok := want[m[1]]
			if !ok {
				t.Errorf("%s: no expected command for the search %q; add it to this test", s.Name, m[1])
				continue
			}
			ios, _, out, _ := iostreams.Test()
			o := options()
			code := app.Execute(app.NewFactory(o, ios), o, []string{"search", m[1], "--jq", ".[0].command"})
			if got := strings.TrimSpace(out.String()); code != 0 || got != cmd {
				t.Errorf("%s: search %q finds %q first (exit %d), want %q", s.Name, m[1], got, code, cmd)
			}
		}
	}
	if found != len(want) {
		t.Errorf("found %d searches in the skills, want %d", found, len(want))
	}
}
