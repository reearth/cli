package visualizer

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writePlugin(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestValidateTemplates(t *testing.T) {
	// What init creates is valid.
	o := &initOptions{ID: "my-plugin", Name: "My plugin", ExtensionID: "my-plugin", Type: "widget",
		More: []extensionSpec{{ID: "legend", Type: "storyBlock", Name: "legend"}}}
	files, err := o.renderJS()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := writeFiles(dir, files); err != nil {
		t.Fatal(err)
	}
	v, err := validatePlugin(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Valid || v.ID != "my-plugin" || v.Version != "0.1.0" || v.Template != "js" {
		t.Errorf("validation = %+v", v)
	}
}

func TestValidateProblems(t *testing.T) {
	dir := t.TempDir()
	writePlugin(t, dir, map[string]string{
		manifestFile: `id: my.plugin
name: P
version: "1.0"
schema:
  linkable:
    url: { schemaGroupId: default, fieldId: nope }
  groups:
    - id: default
      title: Default
      fields:
        - { id: a, type: string, title: A }
        - { id: a, type: text, title: A2, ui: colour }
    - id: default
      title: Again
extensions:
  - id: w
    name: W
    type: widget
    widgetLayout:
      defaultLocation: { zone: inner, section: middle, area: top }
  - id: w
    name: W2
    type: sidebar
  - id: viz
    name: V
    type: visualizer
`,
		"w.js": "",
	})
	v, err := validatePlugin(dir)
	if err != nil {
		t.Fatal(err)
	}
	if v.Valid {
		t.Fatal("want problems")
	}
	var paths []string
	for _, i := range v.Issues {
		paths = append(paths, i.Path)
	}
	for _, want := range []string{
		"id",                              // "." is not allowed by the server
		"version",                         // not semver
		"schema.groups[0].fields[1].id",   // duplicate field
		"schema.groups[0].fields[1].type", // unknown value type
		"schema.groups[0].fields[1].ui",   // unknown ui
		"schema.groups[1].id",             // duplicate group
		"schema.linkable.url",             // points at no field
		"extensions[0].widgetLayout.defaultLocation.section",
		"extensions[1].id",         // duplicate extension
		"extensions[1].type",       // unknown type
		"extensions[2].visualizer", // required for type visualizer
		"extensions[2]",            // viz.js is missing
	} {
		if !slices.Contains(paths, want) {
			t.Errorf("no issue at %s; got %v", want, v.Issues)
		}
	}
}

func TestValidateUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	writePlugin(t, dir, map[string]string{manifestFile: "id: p\nname: P\nversion: 1.0.0\nauthors: me\n"})
	v, err := validatePlugin(dir)
	if err != nil {
		t.Fatal(err)
	}
	if v.Valid || len(v.Issues) != 1 || !strings.Contains(v.Issues[0].Message, "unknown key authors") {
		t.Errorf("issues = %v", v.Issues)
	}
}

func TestValidateTSNeedsBuild(t *testing.T) {
	dir := t.TempDir()
	writePlugin(t, dir, map[string]string{
		"public/" + manifestFile: "id: p\nname: P\nversion: 1.0.0\nextensions:\n  - { id: demo, name: Demo, type: widget }\n",
	})
	v, err := validatePlugin(dir)
	if err != nil {
		t.Fatal(err)
	}
	if v.Valid || v.Issues[0].Path != "dist" {
		t.Errorf("issues = %v", v.Issues)
	}
	writePlugin(t, dir, map[string]string{"dist/demo.js": ""})
	if v, _ := validatePlugin(dir); !v.Valid || v.Manifest != "public/reearth.yml" {
		t.Errorf("after build: %+v", v)
	}
}
