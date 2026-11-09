package visualizer

import (
	"archive/zip"
	"crypto/rand"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/reearth/cli/sdk/app"
	"github.com/reearth/cli/sdk/core"
	"github.com/reearth/cli/sdk/envvar"
	"github.com/reearth/cli/sdk/envvar/envvartest"
	"github.com/reearth/cli/sdk/iostreams"
)

func zipEntries(t *testing.T, path string) map[string]string {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = zr.Close() }()
	out := map[string]string{}
	for _, f := range zr.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(r)
		_ = r.Close()
		out[f.Name] = string(b)
	}
	return out
}

func keys(m map[string]string) []string {
	var k []string
	for name := range m {
		k = append(k, name)
	}
	slices.Sort(k)
	return k
}

func TestPackJS(t *testing.T) {
	dir := t.TempDir()
	writePlugin(t, dir, map[string]string{
		manifestFile:              "id: p\nname: P\nversion: 1.2.3\nextensions:\n  - { id: a, name: A, type: widget }\n",
		"a.js":                    "a",
		"assets/icon.svg":         "<svg/>",
		".git/config":             "x",
		".DS_Store":               "x",
		"node_modules/x/index.js": "x",
		"old.zip":                 "x",
	})
	v, _ := validatePlugin(dir)
	out := filepath.Join(dir, packageDir, "p-1.2.3.zip")
	files, err := packFiles(v, out)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writeZip(out, files, maxPackageSize); err != nil {
		t.Fatal(err)
	}
	got := zipEntries(t, out)
	if want := []string{"a.js", "assets/icon.svg", manifestFile}; !slices.Equal(keys(got), want) {
		t.Errorf("entries = %v, want %v", keys(got), want)
	}

	// Packing again leaves the previous package out.
	files, _ = packFiles(v, out)
	if _, ok := files["package/p-1.2.3.zip"]; ok || len(files) != 3 {
		t.Errorf("repack files = %v", files)
	}
}

func TestPackTS(t *testing.T) {
	dir := t.TempDir()
	writePlugin(t, dir, map[string]string{
		"public/" + manifestFile: "id: p\nname: P\nversion: 1.0.0\nextensions:\n  - { id: demo, name: Demo, type: widget }\n",
		"dist/" + manifestFile:   "id: stale\n",
		"dist/demo.js":           "demo",
		"src/extensions/demo.ts": "src",
		"README.md":              "readme",
		"package.json":           "{}",
	})
	v, _ := validatePlugin(dir)
	files, err := packFiles(v, filepath.Join(dir, packageDir, "p-1.0.0.zip"))
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "p.zip")
	if _, err := writeZip(out, files, maxPackageSize); err != nil {
		t.Fatal(err)
	}
	got := zipEntries(t, out)
	if want := []string{"README.md", "demo.js", manifestFile}; !slices.Equal(keys(got), want) {
		t.Errorf("entries = %v, want %v", keys(got), want)
	}
	// The validated manifest, not the copy in dist/.
	if got[manifestFile] == "id: stale\n" {
		t.Error("packed dist/reearth.yml instead of public/reearth.yml")
	}
}

func TestPackTooLarge(t *testing.T) {
	dir := t.TempDir()
	writePlugin(t, dir, map[string]string{
		manifestFile: "id: p\nname: P\nversion: 1.0.0\nextensions:\n  - { id: a, name: A, type: widget }\n",
		"a.js":       "a",
	})
	big := make([]byte, maxPackageSize+1)
	_, _ = rand.Read(big) // incompressible
	if err := os.WriteFile(filepath.Join(dir, "data.bin"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	// A good package from an earlier run must survive a failed one.
	prev := filepath.Join(dir, packageDir, "p-1.0.0.zip")
	writePlugin(t, dir, map[string]string{"package/p-1.0.0.zip": "previous"})
	o := &packOptions{Dir: dir}
	err := o.run(testFactory(t))
	if err == nil {
		t.Fatal("want package_too_large")
	}
	if b, _ := os.ReadFile(prev); string(b) != "previous" {
		t.Errorf("the previous package was replaced: %q", b)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, packageDir, ".pack-*")); len(left) > 0 {
		t.Errorf("temporary files left: %v", left)
	}
}

func testFactory(t *testing.T) *core.Factory {
	t.Helper()
	dir := t.TempDir()
	envvartest.Fake(t, envvar.Map{"REEARTH_CONFIG_DIR": dir, "REEARTH_CACHE_DIR": dir})
	ios, _, _, _ := iostreams.Test()
	return app.NewFactory(app.Options{Name: "reearth"}, ios)
}
