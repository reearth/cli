package cms_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/reearth/cli/products/cms"
	"github.com/reearth/cli/sdk/app"
	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/core"
	"github.com/reearth/cli/sdk/envvar"
	"github.com/reearth/cli/sdk/envvar/envvartest"
	"github.com/reearth/cli/sdk/iostreams"
)

var opts = app.Options{Name: "reearth", Products: []core.Product{cms.Product{}}}

type result struct {
	out, errOut string
	code        int
}

func run(t *testing.T, args ...string) result {
	t.Helper()
	ios, _, out, errOut := iostreams.Test()
	f := app.NewFactory(opts, ios)
	code := app.Execute(f, opts, args)
	return result{out.String(), errOut.String(), code}
}

// runTTY runs with stdout and stderr on a terminal, as a person sees it.
func runTTY(t *testing.T, args ...string) result {
	t.Helper()
	ios, _, out, errOut := iostreams.Test()
	ios.SetTTY(false, true, true)
	f := app.NewFactory(opts, ios)
	code := app.Execute(f, opts, args)
	return result{out.String(), errOut.String(), code}
}

type request struct {
	Method, Path, Query, Auth string
	Body                      map[string]any
}

// fakeServer records requests and serves handlers keyed by "METHOD /path".
type fakeServer struct {
	srv *httptest.Server
	mu  sync.Mutex
	got []request
}

func newFakeServer(t *testing.T, handlers map[string]http.HandlerFunc) *fakeServer {
	t.Helper()
	s := &fakeServer{}
	mux := http.NewServeMux()
	for pattern, h := range handlers {
		mux.HandleFunc(pattern, h)
	}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := request{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Auth: r.Header.Get("Authorization")}
		if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			_ = json.NewDecoder(r.Body).Decode(&req.Body)
		}
		s.mu.Lock()
		s.got = append(s.got, req)
		s.mu.Unlock()
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *fakeServer) requests() []request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]request(nil), s.got...)
}

func setup(t *testing.T, api *fakeServer, extra envvar.Map) {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	env := envvar.Map{
		"REEARTH_CONFIG_DIR":   filepath.Join(dir, "config"),
		"REEARTH_CACHE_DIR":    filepath.Join(dir, "cache"),
		"REEARTH_DATA_DIR":     filepath.Join(dir, "data"),
		"REEARTH_CMS_TOKEN":    "test-token",
		"REEARTH_CMS_BASE_URL": api.srv.URL,
	}
	for k, v := range extra {
		env[k] = v
	}
	envvartest.Fake(t, env)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func reply(v any) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, v) }
}

func status(code int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }
}

var item = map[string]any{
	"id": "it1", "modelId": "m1",
	"fields": []any{
		map[string]any{"key": "title", "type": "text", "value": "Hello"},
		map[string]any{"key": "spots", "type": "group", "value": []any{"g1"}},
		map[string]any{"key": "name", "type": "text", "value": "Gate", "group": "g1"},
	},
	"metadataFields": []any{
		map[string]any{"key": "status", "type": "select", "value": "draft"},
	},
}

// The SDK is given no token; the authenticated client sets the real one.
func TestModelListSendsTheTokenAndPrints(t *testing.T) {
	api := newFakeServer(t, map[string]http.HandlerFunc{
		"GET /api/projects/proj/models": reply(map[string]any{
			"models": []any{map[string]any{"id": "m1", "key": "parks", "name": "Parks"}},
			"page":   1, "perPage": 50, "totalCount": 1,
		}),
	})
	setup(t, api, envvar.Map{"REEARTH_CMS_PROJECT": "proj"})

	r := run(t, "cms", "model", "list")
	if r.code != 0 || !strings.HasPrefix(r.out, "m1\tparks\tParks\tfalse\t") {
		t.Fatalf("%+v", r)
	}
	got := api.requests()[0]
	if got.Auth != "Bearer test-token" || got.Query != "page=1&perPage=50" {
		t.Fatalf("request: %+v", got)
	}

	r = run(t, "cms", "model", "list", "--jq", "[.[].key]")
	if r.code != 0 || strings.TrimSpace(r.out) != `["parks"]` {
		t.Fatalf("jq: %+v", r)
	}
}

func TestProjectComesFromFlagEnvOrFile(t *testing.T) {
	api := newFakeServer(t, map[string]http.HandlerFunc{
		"GET /api/projects/{p}/models": reply(map[string]any{"models": []any{}}),
	})
	setup(t, api, nil)
	if r := run(t, "cms", "model", "list"); r.code != cmdutil.ExitUsage || !strings.Contains(r.errOut, "-p") {
		t.Fatalf("no project: %+v", r)
	}
	if err := os.WriteFile(".reearth.yaml", []byte("cms:\n  project: from-file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := run(t, "cms", "model", "list"); r.code != 0 {
		t.Fatalf("file: %+v", r)
	}
	if r := run(t, "cms", "model", "list", "-p", "from-flag"); r.code != 0 {
		t.Fatalf("flag: %+v", r)
	}
	got := api.requests()
	if len(got) != 2 || got[0].Path != "/api/projects/from-file/models" || got[1].Path != "/api/projects/from-flag/models" {
		t.Fatalf("requests: %+v", got)
	}
}

func TestModelGetFallsBackToKey(t *testing.T) {
	api := newFakeServer(t, map[string]http.HandlerFunc{
		"GET /api/models/parks":               status(http.StatusNotFound),
		"GET /api/projects/proj/models/parks": reply(map[string]any{"id": "m1", "key": "parks"}),
	})
	setup(t, api, nil)
	r := run(t, "cms", "model", "get", "parks", "-p", "proj", "--json=id")
	if r.code != 0 || !strings.Contains(r.out, `"id": "m1"`) {
		t.Fatalf("%+v", r)
	}
	if r := run(t, "cms", "model", "get", "parks"); r.code != cmdutil.ExitNotFound {
		t.Fatalf("without a project: %+v", r)
	}
}

// People get one row per field, with the fields of a group under its key.
func TestItemGetListsFields(t *testing.T) {
	api := newFakeServer(t, map[string]http.HandlerFunc{"GET /api/items/it1": reply(item)})
	setup(t, api, nil)
	r := run(t, "cms", "item", "get", "it1")
	for _, want := range []string{"id\tit1\n", "title\ttext\tHello\n", "spots[0].name\ttext\tGate\n", "status\tselect\tdraft\n"} {
		if r.code != 0 || !strings.Contains(r.out, want) {
			t.Fatalf("missing %q: %+v", want, r)
		}
	}
	if q := api.requests()[0].Query; q != "asset=true" {
		t.Fatalf("query: %q", q)
	}
	// Machines get the item as the API returns it.
	r = run(t, "cms", "item", "get", "it1", "--jq", ".fields[2].group")
	if r.code != 0 || strings.TrimSpace(r.out) != "g1" {
		t.Fatalf("json: %+v", r)
	}
}

// Piped, every field is a column and no value is cut.
func TestItemListPlainHasEveryField(t *testing.T) {
	api := newFakeServer(t, map[string]http.HandlerFunc{
		"GET /api/models/m1/items": reply(map[string]any{"items": []any{item}, "page": 1, "perPage": 50, "totalCount": 1}),
	})
	setup(t, api, nil)
	r := run(t, "cms", "item", "list", "-m", "m1")
	if r.code != 0 || !strings.HasPrefix(r.out, "it1\tHello\t") {
		t.Fatalf("%+v", r)
	}
	// On a terminal, the field keys are the column headers.
	r = runTTY(t, "cms", "item", "list", "-m", "m1")
	if r.code != 0 || !strings.Contains(r.out, "TITLE") || !strings.Contains(r.out, "Hello") {
		t.Fatalf("tty: %+v", r)
	}
}

func TestItemCreateSendsFields(t *testing.T) {
	api := newFakeServer(t, map[string]http.HandlerFunc{
		"POST /api/projects/proj/models/parks/items": reply(item),
	})
	setup(t, api, nil)
	r := run(t, "cms", "item", "create", "-p", "proj", "-m", "parks",
		"-k", "title", "-t", "text", "-v", "Hello", "-K", "status", "-T", "select", "-V", "draft", "--json=id")
	if r.code != 0 || !strings.Contains(r.out, `"it1"`) {
		t.Fatalf("%+v", r)
	}
	body, _ := json.Marshal(api.requests()[0].Body)
	if s := string(body); !strings.Contains(s, `"key":"title"`) || !strings.Contains(s, `"metadataFields":[{"key":"status","type":"select","value":"draft"}]`) {
		t.Fatalf("body: %s", s)
	}

	for _, args := range [][]string{
		{"-k", "title", "-t", "text"},
		{"-K", "status", "-V", "draft"},
		{},
	} {
		r := run(t, append([]string{"cms", "item", "create", "-m", "m1"}, args...)...)
		if r.code != cmdutil.ExitUsage {
			t.Errorf("%v: exit %d (%s)", args, r.code, r.errOut)
		}
	}
	if n := len(api.requests()); n != 1 {
		t.Fatalf("made %d requests, want 1", n)
	}
}

func TestSafeModeRefusesUpdateAndDelete(t *testing.T) {
	api := newFakeServer(t, map[string]http.HandlerFunc{
		"PATCH /api/items/it1":  reply(item),
		"DELETE /api/items/it1": reply(map[string]any{"id": "it1"}),
		"GET /api/items/it1":    reply(item),
	})
	update := []string{"cms", "item", "update", "it1", "-k", "title", "-t", "text", "-v", "x", "--yes"}
	del := []string{"cms", "item", "delete", "it1", "--yes"}

	for _, value := range []string{"true", "1", "YES"} {
		setup(t, api, envvar.Map{"REEARTH_CMS_SAFE_MODE": value})
		for _, args := range [][]string{update, del} {
			if r := run(t, args...); r.code != cmdutil.ExitError || !strings.Contains(r.errOut, "safe mode") {
				t.Errorf("%s %v: %+v", value, args[2], r)
			}
		}
	}
	setup(t, api, nil)
	if err := os.WriteFile(".reearth.yaml", []byte("cms:\n  safe_mode: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := run(t, del...); r.code != cmdutil.ExitError {
		t.Errorf(".reearth.yaml: %+v", r)
	}
	// An unknown value fails closed.
	setup(t, api, envvar.Map{"REEARTH_CMS_SAFE_MODE": "enabled"})
	if r := run(t, del...); r.code != cmdutil.ExitUsage {
		t.Errorf("invalid value: %+v", r)
	}
	// Reads still work in safe mode.
	setup(t, api, envvar.Map{"REEARTH_CMS_SAFE_MODE": "true"})
	if r := run(t, "cms", "item", "get", "it1"); r.code != 0 {
		t.Errorf("get: %+v", r)
	}
	if got := api.requests(); len(got) != 1 || got[0].Method != http.MethodGet {
		t.Fatalf("requests: %+v", got)
	}

	setup(t, api, envvar.Map{"REEARTH_CMS_SAFE_MODE": "false"})
	if r := run(t, del[:4]...); r.code != cmdutil.ExitUsage || !strings.Contains(r.errOut, "--yes") {
		t.Errorf("without --yes: %+v", r)
	}
	if r := run(t, update...); r.code != 0 {
		t.Errorf("update: %+v", r)
	}
	if r := run(t, append(del, "--json")...); r.code != 0 || !strings.Contains(r.out, `"deleted": true`) {
		t.Errorf("delete: %+v", r)
	}
}

// With a workspace, the SDK calls the workspace-scoped API, which needs the
// project even for an item given by ID.
func TestWorkspaceNeedsProject(t *testing.T) {
	api := newFakeServer(t, map[string]http.HandlerFunc{
		"GET /api/ws1/projects/proj/items/it1": reply(item),
	})
	setup(t, api, envvar.Map{"REEARTH_CMS_WORKSPACE": "ws1"})
	if r := run(t, "cms", "item", "get", "it1"); r.code != cmdutil.ExitUsage {
		t.Fatalf("no project: %+v", r)
	}
	if r := run(t, "cms", "item", "get", "it1", "-p", "proj"); r.code != 0 {
		t.Fatalf("with project: %+v", r)
	}
	if n := len(api.requests()); n != 1 {
		t.Fatalf("made %d requests, want 1", n)
	}
}

func TestAPIErrorsMapToExitCodes(t *testing.T) {
	api := newFakeServer(t, map[string]http.HandlerFunc{
		"GET /api/assets/missing": status(http.StatusNotFound),
		"GET /api/assets/denied":  status(http.StatusUnauthorized),
		"GET /api/assets/invalid": status(http.StatusBadRequest),
		"GET /api/assets/broken":  status(http.StatusConflict),
	})
	setup(t, api, nil)
	for id, want := range map[string]struct {
		exit int
		code string
	}{
		"missing": {cmdutil.ExitNotFound, "not_found"},
		"denied":  {cmdutil.ExitAuth, "auth.rejected"},
		"invalid": {cmdutil.ExitUsage, "cms.invalid_input"},
		"broken":  {cmdutil.ExitError, "cms.request_failed"},
	} {
		r := run(t, "cms", "asset", "get", id, "--json")
		if r.code != want.exit || !strings.Contains(r.errOut, `"code":"`+want.code+`"`) {
			t.Errorf("%s: exit %d, want %d %s (%s)", id, r.code, want.exit, want.code, r.errOut)
		}
	}
}

// Files go to and come from the storage host without the CMS token.
func TestAssetFilesSkipTheToken(t *testing.T) {
	storage := newFakeServer(t, map[string]http.HandlerFunc{
		"PUT /upload": func(w http.ResponseWriter, r *http.Request) {
			if b, _ := io.ReadAll(r.Body); string(b) != "{}" {
				t.Errorf("uploaded %q", b)
			}
		},
		"GET /files/a.geojson": func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "content") },
	})
	api := newFakeServer(t, map[string]http.HandlerFunc{
		"POST /api/projects/proj/assets/uploads": reply(map[string]any{"url": storage.srv.URL + "/upload", "token": "up1", "contentType": "application/geo+json"}),
		"POST /api/projects/proj/assets":         reply(map[string]any{"id": "a1", "url": storage.srv.URL + "/files/a.geojson"}),
		"GET /api/assets/a1":                     reply(map[string]any{"id": "a1", "url": storage.srv.URL + "/files/a.geojson"}),
	})
	setup(t, api, envvar.Map{"REEARTH_CMS_PROJECT": "proj"})
	if err := os.WriteFile("a.geojson", []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	if r := run(t, "cms", "asset", "create", "-f", "a.geojson", "--json=id"); r.code != 0 || !strings.Contains(r.out, `"a1"`) {
		t.Fatalf("create: %+v", r)
	}
	got := api.requests()
	if got[0].Body["name"] != "a.geojson" || got[1].Body["token"] != "up1" {
		t.Fatalf("api requests: %+v", got)
	}

	if r := run(t, "cms", "asset", "cat", "a1"); r.code != 0 || r.out != "content" {
		t.Fatalf("cat: %+v", r)
	}
	if r := run(t, "cms", "asset", "cp", "a1", "out.geojson"); r.code != 0 {
		t.Fatalf("cp: %+v", r)
	}
	if b, _ := os.ReadFile("out.geojson"); string(b) != "content" {
		t.Fatalf("copied %q", b)
	}

	for _, req := range storage.requests() {
		if req.Auth != "" {
			t.Errorf("%s %s sent Authorization %q to storage", req.Method, req.Path, req.Auth)
		}
	}
	for _, req := range api.requests() {
		if req.Auth != "Bearer test-token" {
			t.Errorf("%s %s: Authorization %q", req.Method, req.Path, req.Auth)
		}
	}

	for _, args := range [][]string{{}, {"-f", "a", "-u", "http://x"}, {"-u", "http://x", "--direct"}} {
		if r := run(t, append([]string{"cms", "asset", "create"}, args...)...); r.code != cmdutil.ExitUsage {
			t.Errorf("%v: %+v", args, r)
		}
	}
}

func TestComment(t *testing.T) {
	api := newFakeServer(t, map[string]http.HandlerFunc{
		"POST /api/items/it1/comments": reply(map[string]any{}),
		"POST /api/assets/a1/comments": reply(map[string]any{}),
	})
	setup(t, api, nil)
	if r := run(t, "cms", "comment", "item", "it1", "-c", "Checked"); r.code != 0 {
		t.Fatalf("item: %+v", r)
	}
	if r := run(t, "cms", "comment", "asset", "a1", "-c", "Fine"); r.code != 0 {
		t.Fatalf("asset: %+v", r)
	}
	got := api.requests()
	if got[0].Body["content"] != "Checked" || got[1].Body["content"] != "Fine" {
		t.Fatalf("requests: %+v", got)
	}
}

func TestAssetCreateFromURLAndDirect(t *testing.T) {
	api := newFakeServer(t, map[string]http.HandlerFunc{
		"POST /api/projects/proj/assets": func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
				writeJSON(w, map[string]any{"id": "from-url"})
				return
			}
			fh, hdr, err := r.FormFile("file")
			if err != nil {
				t.Errorf("multipart: %v", err)
				return
			}
			b, _ := io.ReadAll(fh)
			if hdr.Filename != "park.geojson" || string(b) != "{}" {
				t.Errorf("uploaded %s %q", hdr.Filename, b)
			}
			writeJSON(w, map[string]any{"id": "direct"})
		},
		"GET /api/assets/from-url": reply(map[string]any{"id": "from-url", "name": "image.png"}),
		// The asset is not readable yet: create still reports its ID.
		"GET /api/assets/direct": status(http.StatusNotFound),
	})
	setup(t, api, envvar.Map{"REEARTH_CMS_PROJECT": "proj"})
	if err := os.MkdirAll("data", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("data", "park.geojson"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := run(t, "cms", "asset", "create", "-u", "https://example.com/image.png", "--json=id,name")
	if r.code != 0 || !strings.Contains(r.out, `"name": "image.png"`) {
		t.Fatalf("url: %+v", r)
	}
	if u := api.requests()[0].Body["url"]; u != "https://example.com/image.png" {
		t.Fatalf("url body: %v", u)
	}

	r = run(t, "cms", "asset", "create", "-f", filepath.Join("data", "park.geojson"), "--direct", "--json=id")
	if r.code != 0 || !strings.Contains(r.out, `"direct"`) {
		t.Fatalf("direct: %+v", r)
	}
}

func TestAssetFailures(t *testing.T) {
	storage := newFakeServer(t, map[string]http.HandlerFunc{
		"PUT /upload":     status(http.StatusForbidden),
		"GET /files/gone": status(http.StatusNotFound),
	})
	api := newFakeServer(t, map[string]http.HandlerFunc{
		"POST /api/projects/proj/assets/uploads": reply(map[string]any{"url": storage.srv.URL + "/upload", "token": "up1"}),
		"GET /api/assets/gone":                   reply(map[string]any{"id": "gone", "url": storage.srv.URL + "/files/gone"}),
		"GET /api/assets/nourl":                  reply(map[string]any{"id": "nourl"}),
	})
	setup(t, api, envvar.Map{"REEARTH_CMS_PROJECT": "proj"})
	if err := os.WriteFile("a.geojson", []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		args []string
		exit int
		code string
	}{
		{[]string{"asset", "create", "-f", "a.geojson"}, cmdutil.ExitError, "cms.upload_failed"},
		{[]string{"asset", "create", "-f", "missing.geojson"}, cmdutil.ExitUsage, "cms.file_unreadable"},
		{[]string{"asset", "cat", "gone"}, cmdutil.ExitError, "cms.download_failed"},
		{[]string{"asset", "cp", "gone", "out.geojson"}, cmdutil.ExitError, "cms.download_failed"},
		{[]string{"asset", "cat", "nourl"}, cmdutil.ExitError, "cms.asset_no_url"},
	} {
		r := run(t, append(append([]string{"cms"}, c.args...), "--json")...)
		if r.code != c.exit || !strings.Contains(r.errOut, `"code":"`+c.code+`"`) {
			t.Errorf("%v: exit %d, want %d %s (%s)", c.args, r.code, c.exit, c.code, r.errOut)
		}
	}
	// A failed copy leaves no partial file behind.
	if _, err := os.Stat("out.geojson"); !os.IsNotExist(err) {
		t.Errorf("out.geojson: %v", err)
	}
}

func TestPaging(t *testing.T) {
	api := newFakeServer(t, map[string]http.HandlerFunc{
		"GET /api/projects/proj/models": reply(map[string]any{
			"models": []any{map[string]any{"id": "m1", "key": "parks"}},
			"page":   1, "perPage": 1, "totalCount": 3,
		}),
	})
	setup(t, api, envvar.Map{"REEARTH_CMS_PROJECT": "proj"})

	r := runTTY(t, "cms", "model", "list", "--per-page", "1")
	if r.code != 0 || !strings.Contains(r.errOut, "page 1 of 3 models; pass --page 2 for the next") {
		t.Fatalf("hint: %+v", r)
	}
	// Machines get no hint.
	if r := run(t, "cms", "model", "list", "--per-page", "1"); strings.Contains(r.errOut, "--page 2") {
		t.Fatalf("plain hint: %+v", r)
	}

	for _, args := range [][]string{{"--page", "0"}, {"--per-page", "0"}} {
		if r := run(t, append([]string{"cms", "model", "list"}, args...)...); r.code != cmdutil.ExitUsage {
			t.Errorf("%v: %+v", args, r)
		}
	}
	if n := len(api.requests()); n != 2 {
		t.Fatalf("made %d requests, want 2", n)
	}
}

func TestHumanViews(t *testing.T) {
	api := newFakeServer(t, map[string]http.HandlerFunc{
		"GET /api/assets/a1":    reply(map[string]any{"id": "a1", "name": "park.geojson", "url": "https://files.example.com/park.geojson"}),
		"DELETE /api/items/it1": reply(map[string]any{}),
	})
	setup(t, api, nil)

	r := runTTY(t, "cms", "asset", "get", "a1")
	for _, want := range []string{"id", "a1", "park.geojson", "https://files.example.com/park.geojson"} {
		if r.code != 0 || !strings.Contains(r.out, want) {
			t.Fatalf("asset get missing %q: %+v", want, r)
		}
	}
	// Empty values are left out.
	if strings.Contains(r.out, "content type") {
		t.Fatalf("asset get shows an empty row: %q", r.out)
	}

	r = runTTY(t, "cms", "item", "delete", "it1", "--yes")
	if r.code != 0 || r.out != "" || !strings.Contains(r.errOut, "Deleted item it1") {
		t.Fatalf("delete: %+v", r)
	}
}
