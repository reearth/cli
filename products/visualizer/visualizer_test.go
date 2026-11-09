package visualizer_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/reearth/cli/products/visualizer"
	"github.com/reearth/cli/sdk/app"
	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/core"
	"github.com/reearth/cli/sdk/envvar"
	"github.com/reearth/cli/sdk/envvar/envvartest"
	"github.com/reearth/cli/sdk/iostreams"
)

var opts = app.Options{Name: "reearth", Products: []core.Product{visualizer.Product{}}}

type result struct {
	out, errOut string
	code        int
}

func run(t *testing.T, args ...string) result {
	t.Helper()
	return runTTY(t, "", args...)
}

// runTTY runs on a fake terminal when input is set, answering prompts with it.
func runTTY(t *testing.T, input string, args ...string) result {
	t.Helper()
	ios, in, out, errOut := iostreams.Test()
	if input != "" {
		ios.SetTTY(true, true, true)
		in.WriteString(input)
	}
	f := app.NewFactory(opts, ios)
	code := app.Execute(f, opts, args)
	return result{out.String(), errOut.String(), code}
}

// setup isolates config/cache and serves a fake TS template. hits counts
// downloads, to check the cache.
func setup(t *testing.T) (hits *int) {
	t.Helper()
	dir := t.TempDir()
	env := envvartest.Fake(t, envvar.Map{
		"REEARTH_CONFIG_DIR": filepath.Join(dir, "config"),
		"REEARTH_CACHE_DIR":  filepath.Join(dir, "cache"),
		"REEARTH_DATA_DIR":   filepath.Join(dir, "data"),
	})
	t.Chdir(dir)

	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/template.tar.gz" {
			http.NotFound(w, r)
			return
		}
		n++
		_, _ = w.Write(tsTarball(t))
	}))
	t.Cleanup(srv.Close)
	env["REEARTH_VISUALIZER_TS_TEMPLATE_URL"] = srv.URL + "/template.tar.gz"
	return &n
}

func tsTarball(t *testing.T) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range map[string]string{
		"tmpl-main/package.json": `{
  "name": "reearth-visualizer-plugin-shadcn-template",
  "scripts": {
    "build:demo:extension": "cross-env EXTENSION_NAME=demo vite build",
    "build:demo": "run-s build:demo:main build:demo:extension",
    "build": "run-s build:demo zip"
  },
  "dependencies": { "demo-lib": "1.0.0" }
}`,
		"tmpl-main/public/reearth.yml":               "id: reearth-visualizer-plugin-shadcn-template\nname: Template\nversion: 1.0.0\nextensions:\n  - id: demo\n    type: widget\n    name: Demo\n",
		"tmpl-main/src/extensions/demo/demo.ts":      `import html from "@distui/demo/main/index.html?raw";`,
		"tmpl-main/src/extensions/demo/main/App.tsx": "export default {}",
		"tmpl-main/.git/config":                      "x",
	} {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte(body))
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestInitJS(t *testing.T) {
	setup(t)
	r := run(t, "vis", "plugin", "init", "my-plugin", "--extension-id", "hoge", "--type", "storyBlock", "--json")
	if r.code != 0 {
		t.Fatalf("init: %+v", r)
	}
	var res struct {
		ID    string   `json:"id"`
		Files []string `json:"files"`
	}
	_ = json.Unmarshal([]byte(r.out), &res)
	if res.ID != "my-plugin" || strings.Join(res.Files, ",") != "hoge.js,reearth.yml" {
		t.Fatalf("result = %+v", res)
	}
	y := read(t, "my-plugin/reearth.yml")
	for _, want := range []string{"id: my-plugin", "- id: hoge", "type: storyBlock"} {
		if !strings.Contains(y, want) {
			t.Errorf("reearth.yml missing %q:\n%s", want, y)
		}
	}

	// A second init into the same directory is refused.
	if r := run(t, "vis", "plugin", "init", "my-plugin"); r.code != cmdutil.ExitUsage {
		t.Fatalf("second init = %d", r.code)
	}
	if r := run(t, "vis", "plugin", "init", "x", "--type", "nope"); r.code != cmdutil.ExitUsage {
		t.Fatalf("bad type = %d", r.code)
	}
	if r := run(t, "vis", "plugin", "init", "reearth"); r.code != cmdutil.ExitUsage {
		t.Fatalf("reserved id = %d", r.code)
	}
}

func TestInitTS(t *testing.T) {
	hits := setup(t)
	for _, id := range []string{"layer-list", "$x", "class"} {
		if r := run(t, "vis", "plugin", "init", "bad", "--template", "ts", "--extension-id", id); r.code != cmdutil.ExitUsage {
			t.Fatalf("extension id %q = %d", id, r.code)
		}
	}
	r := run(t, "vis", "plugin", "init", "my-layers", "--template", "ts", "--name", "My Layers")
	if r.code != 0 {
		t.Fatalf("init ts: %+v", r)
	}
	// Default extension ID for ts is the camel-cased plugin ID.
	y := read(t, "my-layers/public/reearth.yml")
	for _, want := range []string{"id: my-layers", "name: My Layers", "- id: myLayers"} {
		if !strings.Contains(y, want) {
			t.Errorf("reearth.yml missing %q:\n%s", want, y)
		}
	}
	pkg := read(t, "my-layers/package.json")
	for _, want := range []string{`"name": "my-layers"`, "EXTENSION_NAME=myLayers", `"build:myLayers":`, "run-s build:myLayers zip", `"demo-lib"`} {
		if !strings.Contains(pkg, want) {
			t.Errorf("package.json missing %q:\n%s", want, pkg)
		}
	}
	if ts := read(t, "my-layers/src/extensions/myLayers/myLayers.ts"); !strings.Contains(ts, "@distui/myLayers/main") {
		t.Errorf("entry = %s", ts)
	}
	if _, err := os.Stat("my-layers/src/extensions/myLayers/main/App.tsx"); err != nil {
		t.Error(err)
	}
	if _, err := os.Stat("my-layers/.git"); err == nil {
		t.Error(".git should be skipped")
	}

	// The template is cached: a second init downloads nothing.
	before := *hits
	if r := run(t, "vis", "plugin", "init", "again", "--template", "ts"); r.code != 0 {
		t.Fatalf("second init: %+v", r)
	}
	if *hits != before {
		t.Errorf("expected the cached template, got %d new downloads", *hits-before)
	}
}

func TestInitInteractive(t *testing.T) {
	setup(t)

	// Everything asked: ID (an invalid one first), name, template, extension ID,
	// type, then one more extension (a duplicate ID first).
	r := runTTY(t, "reearth\nmy-plugin\nMy Plugin\n\nhoge\n3\ny\nhoge\nlegend\n2\n\n", "vis", "plugin", "init")
	if r.code != 0 {
		t.Fatalf("init: %+v", r)
	}
	if strings.Count(r.errOut, "Plugin ID") != 2 {
		t.Errorf("invalid ID should be asked again:\n%s", r.errOut)
	}
	y := read(t, "my-plugin/reearth.yml")
	for _, want := range []string{"id: my-plugin", "name: My Plugin", "- id: hoge", "type: infoboxBlock", "- id: legend", "type: storyBlock"} {
		if !strings.Contains(y, want) {
			t.Errorf("reearth.yml missing %q:\n%s", want, y)
		}
	}
	if !strings.Contains(r.errOut, `"hoge" is already used`) {
		t.Errorf("duplicate extension ID should be asked again:\n%s", r.errOut)
	}
	for _, f := range []string{"hoge.js", "legend.js"} {
		if _, err := os.Stat("my-plugin/" + f); err != nil {
			t.Error(err)
		}
	}

	// Flags are not asked again; only the name is left.
	r = runTTY(t, "\nn\n", "vis", "plugin", "init", "ts-one", "--template", "ts", "--extension-id", "layers", "--type", "widget")
	if r.code != 0 || strings.Contains(r.errOut, "Template") || strings.Contains(r.errOut, "Extension") {
		t.Fatalf("init ts: %+v", r)
	}
	if !strings.Contains(read(t, "ts-one/public/reearth.yml"), "- id: layers") {
		t.Error("extension not renamed")
	}

	// --no-input asks nothing, so a directory is required.
	if r := runTTY(t, "x\n", "vis", "plugin", "init", "--no-input"); r.code != cmdutil.ExitUsage {
		t.Fatalf("--no-input without dir = %+v", r)
	}
	// --yes only confirms destructive actions; it does not skip questions.
	r = runTTY(t, "yes-dir\n\n\n\n\n\nn\n", "vis", "plugin", "init", "--yes")
	if r.code != 0 || !strings.Contains(r.errOut, "Plugin ID") {
		t.Fatalf("--yes should still ask: %+v", r)
	}
	if r := run(t, "vis", "plugin", "init"); r.code != cmdutil.ExitUsage {
		t.Fatalf("no terminal, no dir = %+v", r)
	}
}

func TestAdd(t *testing.T) {
	setup(t)
	if r := run(t, "vis", "plugin", "init", "p", "--extension-id", "first"); r.code != 0 {
		t.Fatalf("init: %+v", r)
	}
	// A comment the user wrote survives the rewrite.
	y := read(t, "p/reearth.yml")
	if err := os.WriteFile("p/reearth.yml", []byte("# mine\n"+y), 0o644); err != nil {
		t.Fatal(err)
	}

	r := run(t, "vis", "plugin", "add", "legend", "--type", "storyBlock", "--name", "Legend", "--dir", "p", "--json")
	if r.code != 0 {
		t.Fatalf("add: %+v", r)
	}
	var res struct {
		PluginID  string `json:"pluginId"`
		Extension struct{ ID, Type string }
	}
	_ = json.Unmarshal([]byte(r.out), &res)
	if res.PluginID != "p" || res.Extension.ID != "legend" || res.Extension.Type != "storyBlock" {
		t.Fatalf("result = %s", r.out)
	}
	y = read(t, "p/reearth.yml")
	for _, want := range []string{"# mine", "- id: first", "- id: legend", "name: Legend", "type: storyBlock"} {
		if !strings.Contains(y, want) {
			t.Errorf("reearth.yml missing %q:\n%s", want, y)
		}
	}
	if !strings.Contains(read(t, "p/legend.js"), "Hello from Legend") {
		t.Error("legend.js not rendered")
	}

	// Asked on a terminal; the default type is widget.
	t.Chdir("p")
	if r := runTTY(t, "first\nchart\n\n", "vis", "plugin", "add"); r.code != 0 || !strings.Contains(read(t, "reearth.yml"), "- id: chart") {
		t.Fatalf("interactive add: %+v", r)
	}
	t.Chdir("..")

	for name, args := range map[string][]string{
		"duplicate": {"legend", "--dir", "p"},
		"no id":     {"--dir", "p"},
		"bad type":  {"x", "--type", "nope", "--dir", "p"},
		"bad id":    {"1x", "--dir", "p"},
		"no plugin": {"x", "--dir", "nowhere"},
	} {
		want := cmdutil.ExitUsage
		if name == "no plugin" {
			want = cmdutil.ExitNotFound
		}
		if r := run(t, append([]string{"vis", "plugin", "add"}, args...)...); r.code != want {
			t.Errorf("%s: code = %d, want %d (%s)", name, r.code, want, r.errOut)
		}
	}
}

func TestAddTS(t *testing.T) {
	setup(t)

	// init: a second extension while prompting (an ID with "-" first).
	r := runTTY(t, "\ny\nmy-legend\nlegend\n2\n\n", "vis", "plugin", "init", "ts", "--template", "ts", "--extension-id", "layers", "--type", "widget")
	if r.code != 0 {
		t.Fatalf("init: %+v", r)
	}
	// add: a third one afterwards.
	if r := run(t, "vis", "plugin", "add", "chart", "--type", "infoboxBlock", "--name", "Chart", "--dir", "ts"); r.code != 0 {
		t.Fatalf("add: %+v", r)
	}

	y := read(t, "ts/public/reearth.yml")
	for _, want := range []string{"- id: layers", "- id: legend", "type: storyBlock", "- id: chart", "name: Chart", "type: infoboxBlock"} {
		if !strings.Contains(y, want) {
			t.Errorf("reearth.yml missing %q:\n%s", want, y)
		}
	}
	var pkg struct{ Scripts map[string]string }
	if err := json.Unmarshal([]byte(read(t, "ts/package.json")), &pkg); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{
		"build":                 "run-s build:layers build:legend build:chart zip",
		"build:chart":           "run-s build:chart:main build:chart:extension",
		"build:chart:main":      "cross-env EXTENSION_NAME=chart UI_NAME=main vite build -c configs/ui.ts",
		"build:chart:extension": "cross-env EXTENSION_NAME=chart vite build -c configs/extension.ts",
		"dev:legend:main":       "cross-env EXTENSION_NAME=legend UI_NAME=main vite -c configs/ui.ts",
	} {
		if pkg.Scripts[k] != want {
			t.Errorf("scripts[%q] = %q, want %q", k, pkg.Scripts[k], want)
		}
	}
	if ts := read(t, "ts/src/extensions/chart/chart.ts"); !strings.Contains(ts, `"@distui/chart/main/index.html?raw"`) {
		t.Errorf("chart.ts = %s", ts)
	}
	if app := read(t, "ts/src/extensions/chart/main/App.tsx"); !strings.Contains(app, `<CardTitle>{"Chart"}</CardTitle>`) {
		t.Errorf("App.tsx = %s", app)
	}
	for _, f := range []string{"main/hooks.ts", "main/main.tsx", "main/index.html", "main/app.css"} {
		if _, err := os.Stat("ts/src/extensions/legend/" + f); err != nil {
			t.Error(err)
		}
	}

	for name, args := range map[string][]string{
		"duplicate":      {"legend", "--dir", "ts"},
		"not identifier": {"my-chart", "--dir", "ts"},
		"reserved word":  {"class", "--dir", "ts"},
	} {
		if r := run(t, append([]string{"vis", "plugin", "add"}, args...)...); r.code != cmdutil.ExitUsage {
			t.Errorf("%s: code = %d (%s)", name, r.code, r.errOut)
		}
	}
}
