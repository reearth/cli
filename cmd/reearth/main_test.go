package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"

	"github.com/reearth/cli/sdk/app"
	"github.com/reearth/cli/sdk/cmdtree"
	"github.com/reearth/cli/sdk/iostreams"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/commands.json from the current command tree")

func newRoot(t *testing.T) *cobra.Command {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("REEARTH_CONFIG_DIR", filepath.Join(dir, "config"))
	t.Setenv("REEARTH_CACHE_DIR", filepath.Join(dir, "cache"))
	t.Setenv("REEARTH_DATA_DIR", filepath.Join(dir, "data"))
	o := options()
	ios, _, _, _ := iostreams.Test()
	return app.NewRoot(app.NewFactory(o, ios), o)
}

// TestCommandSurface pins every command, argument and flag of the distributed
// binary. A diff in testdata/commands.json shows reviewers what a change adds,
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
