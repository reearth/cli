package visualizer

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/reearth/cli/sdk/app"
	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/core"
	"github.com/reearth/cli/sdk/envvar"
	"github.com/reearth/cli/sdk/envvar/envvartest"
	"github.com/reearth/cli/sdk/iostreams"
)

var options = app.Options{Name: "reearth", Products: []core.Product{Product{}}}

// request is a request the fake API received.
type request struct {
	Method, Path, Query, ContentType string
	Body                             []byte
}

// fakeAPI is a Visualizer API that answers from handlers keyed by
// "METHOD /path" and records every request.
type fakeAPI struct {
	t        *testing.T
	srv      *httptest.Server
	mu       sync.Mutex
	requests []request
}

func newFakeAPI(t *testing.T, handlers map[string]http.HandlerFunc) *fakeAPI {
	t.Helper()
	api := &fakeAPI{t: t}
	mux := http.NewServeMux()
	for pattern, h := range handlers {
		mux.HandleFunc(pattern, h)
	}
	api.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("%s %s: Authorization = %q", r.Method, r.URL.Path, got)
		}
		// Debug headers only work against a mock-auth server; the CLI must never send them.
		if r.Header.Get("X-Reearth-Debug-User") != "" {
			t.Errorf("%s %s: sent X-Reearth-Debug-User", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		api.mu.Lock()
		api.requests = append(api.requests, request{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Content-Type"), body})
		api.mu.Unlock()
		r.Body = io.NopCloser(bytes.NewReader(body))
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(api.srv.Close)
	return api
}

func (a *fakeAPI) reqs() []request {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]request(nil), a.requests...)
}

type result struct {
	out, errOut string
	code        int
}

// setup runs the test in a temporary directory with an environment holding
// only the CLI's directories, a token and the fake API's URL.
func setup(t *testing.T, api *fakeAPI, extra envvar.Map) {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	env := envvar.Map{
		"REEARTH_CONFIG_DIR": filepath.Join(dir, "config"),
		"REEARTH_CACHE_DIR":  filepath.Join(dir, "cache"),
		"REEARTH_DATA_DIR":   filepath.Join(dir, "data"),
		"REEARTH_TOKEN":      "test-token",
	}
	if api != nil {
		env["REEARTH_VISUALIZER_BASE_URL"] = api.srv.URL
	}
	for k, v := range extra {
		env[k] = v
	}
	envvartest.Fake(t, env)
}

func run(t *testing.T, args ...string) result {
	t.Helper()
	ios, _, out, errOut := iostreams.Test()
	f := app.NewFactory(options, ios)
	f.OpenBrowser = func(string) error { return nil }
	code := app.Execute(f, options, args)
	return result{out.String(), errOut.String(), code}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func apiError(status int, code, message string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, status, map[string]any{"error": map[string]any{"code": code, "message": message}})
	}
}

func projectJSON(id, name string) map[string]any {
	return map[string]any{
		"id": id, "name": name, "workspaceId": "ws1", "sceneId": "sc1",
		"publishmentStatus": "private", "visibility": "private",
		"createdAt": "2026-10-01T00:00:00Z", "updatedAt": "2026-10-01T00:00:00Z",
	}
}

func jsonBody(t *testing.T, r request) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.Body, &m); err != nil {
		t.Fatalf("%s %s: body is not JSON: %q", r.Method, r.Path, r.Body)
	}
	return m
}

func TestProjectEditSendsOnlyChangedFields(t *testing.T) {
	api := newFakeAPI(t, map[string]http.HandlerFunc{
		"PATCH /projects/p1": func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, projectJSON("p1", "New")) },
	})
	setup(t, api, nil)

	if r := run(t, "vis", "project", "edit", "p1", "--name", "New", "--starred=false"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	body := jsonBody(t, api.reqs()[0])
	if len(body) != 2 || body["name"] != "New" || body["starred"] != false {
		t.Fatalf("body: %v", body)
	}

	// Nothing to change is a usage error, without a request.
	if r := run(t, "vis", "project", "edit", "p1"); r.code != cmdutil.ExitUsage {
		t.Fatalf("%+v", r)
	}
	if n := len(api.reqs()); n != 1 {
		t.Fatalf("made %d requests, want 1", n)
	}
}

func TestProjectCreateAndProjectFromEnv(t *testing.T) {
	api := newFakeAPI(t, map[string]http.HandlerFunc{
		"POST /workspaces/ws1/projects": func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 201, projectJSON("p9", "Parks")) },
		"GET /projects/p9":              func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, projectJSON("p9", "Parks")) },
	})
	setup(t, api, envvar.Map{"REEARTH_VISUALIZER_WORKSPACE": "ws1", "REEARTH_VISUALIZER_PROJECT": "p9"})

	r := run(t, "vis", "project", "create", "--name", "Parks", "--visibility", "private", "--alias", "city-parks", "--jq", ".id")
	if r.code != 0 || strings.TrimSpace(r.out) != "p9" {
		t.Fatalf("%+v", r)
	}
	if body := jsonBody(t, api.reqs()[0]); body["name"] != "Parks" || body["visibility"] != "private" || body["alias"] != "city-parks" || body["description"] != nil {
		t.Fatalf("body: %v", body)
	}
	for _, args := range [][]string{
		{"--visibility", "public"},
		{"--name", "Parks"},
		{"--name", "Parks", "--visibility", "secret"},
	} {
		if r := run(t, append([]string{"vis", "project", "create"}, args...)...); r.code != cmdutil.ExitUsage {
			t.Fatalf("create %v: %+v", args, r)
		}
	}
	if n := len(api.reqs()); n != 1 {
		t.Fatalf("rejected creates reached the API: %d requests", n)
	}

	// The project ID comes from REEARTH_VISUALIZER_PROJECT.
	if r := run(t, "vis", "project", "view"); r.code != 0 || !strings.Contains(r.out, "Parks") {
		t.Fatalf("view: %+v", r)
	}
}

func TestDestructiveCommandsNeedYes(t *testing.T) {
	api := newFakeAPI(t, map[string]http.HandlerFunc{
		"DELETE /projects/p1": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) },
		"POST /projects/p1/publish": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, 200, map[string]any{"project": projectJSON("p1", "A")})
		},
		"POST /projects/p1/story/publish": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, 200, map[string]any{"story": map[string]any{"id": "s1", "title": "Default", "updatedAt": "2026-10-01T00:00:00Z"}})
		},
		"DELETE /projects/p1/plugins/pl1": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) },
	})
	setup(t, api, nil)

	for _, args := range [][]string{
		{"vis", "project", "delete", "p1"},
		{"vis", "project", "unpublish", "p1"},
		{"vis", "story", "unpublish", "p1"},
		{"vis", "plugin", "uninstall", "pl1", "-p", "p1"},
	} {
		// Without a terminal to ask on, the command needs --yes.
		r := run(t, args...)
		if r.code != cmdutil.ExitUsage || !strings.Contains(r.errOut, "--yes") {
			t.Fatalf("%v without --yes: %+v", args, r)
		}
		if n := len(api.reqs()); n != 0 {
			t.Fatalf("%v without --yes made %d requests", args, n)
		}
	}
	for _, args := range [][]string{
		{"vis", "project", "delete", "p1", "--yes"},
		{"vis", "project", "unpublish", "p1", "--yes"},
		{"vis", "story", "unpublish", "p1", "--yes"},
		{"vis", "plugin", "uninstall", "pl1", "-p", "p1", "--yes"},
	} {
		if r := run(t, args...); r.code != 0 {
			t.Fatalf("%v: %+v", args, r)
		}
	}
	got := api.reqs()
	if len(got) != 4 || jsonBody(t, got[1])["status"] != "private" || jsonBody(t, got[2])["status"] != "private" {
		t.Fatalf("requests: %+v", got)
	}
}

func TestDestructiveCommandsNeedProjectArg(t *testing.T) {
	api := newFakeAPI(t, nil)
	setup(t, api, envvar.Map{"REEARTH_VISUALIZER_PROJECT": "p9"})

	for _, args := range [][]string{
		{"vis", "project", "delete", "--yes"},
		{"vis", "project", "restore"},
		{"vis", "project", "unpublish", "--yes"},
		{"vis", "story", "unpublish", "--yes"},
	} {
		if r := run(t, args...); r.code == 0 {
			t.Fatalf("%v without a project ID: %+v", args, r)
		}
	}
	if n := len(api.reqs()); n != 0 {
		t.Fatalf("made %d requests", n)
	}
}

func TestAPIErrorsMapToExitCodes(t *testing.T) {
	api := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET /projects/missing":   apiError(404, "not_found", "project not found"),
		"GET /projects/bad":       apiError(400, "invalid_input", `invalid project ID "bad"`),
		"GET /projects/expired":   apiError(401, "unauthorized", "unauthorized"),
		"GET /projects/forbidden": apiError(403, "permission_denied", "permission denied"),
		"GET /projects/broken":    func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) },
	})
	setup(t, api, nil)

	for id, want := range map[string]int{
		"missing":   cmdutil.ExitNotFound,
		"bad":       cmdutil.ExitUsage,
		"expired":   cmdutil.ExitAuth,
		"forbidden": cmdutil.ExitError,
		"broken":    cmdutil.ExitError,
	} {
		if r := run(t, "vis", "project", "view", id); r.code != want {
			t.Errorf("%s: exit %d, want %d (%s)", id, r.code, want, r.errOut)
		}
	}

	// With --json, the error is JSON with the API's code under "visualizer.".
	r := run(t, "vis", "project", "view", "missing", "--json")
	var e struct {
		Error struct{ Code, Message string }
	}
	if err := json.Unmarshal([]byte(r.errOut), &e); err != nil || e.Error.Code != "visualizer.not_found" || e.Error.Message != "project not found" {
		t.Fatalf("json error: %q", r.errOut)
	}
}

func TestPublishPrintsURL(t *testing.T) {
	publish := func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"project": projectJSON("p1", "A"), "url": "https://a.example.com"})
	}
	api := newFakeAPI(t, map[string]http.HandlerFunc{
		"POST /projects/p1/publish": publish,
		"POST /projects/p1/story/publish": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, 200, map[string]any{
				"story": map[string]any{"id": "s1", "title": "Default", "updatedAt": "2026-10-01T00:00:00Z"},
				"url":   "https://s.example.com",
			})
		},
	})
	setup(t, api, nil)

	// Only the URL goes to stdout, so it can be captured.
	if r := run(t, "vis", "project", "publish", "p1"); r.code != 0 || r.out != "https://a.example.com\n" {
		t.Fatalf("%+v", r)
	}
	if r := run(t, "vis", "project", "publish", "p1", "--index", "--alias", "parks"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if r := run(t, "vis", "story", "publish", "p1"); r.code != 0 || r.out != "https://s.example.com\n" {
		t.Fatalf("%+v", r)
	}
	got := api.reqs()
	if b := jsonBody(t, got[0]); b["status"] != "limited" || b["alias"] != nil {
		t.Fatalf("default publish body: %v", b)
	}
	if b := jsonBody(t, got[1]); b["status"] != "public" || b["alias"] != "parks" {
		t.Fatalf("--index publish body: %v", b)
	}
	if b := jsonBody(t, got[2]); b["status"] != "limited" {
		t.Fatalf("story publish body: %v", b)
	}
}

func TestProjectExport(t *testing.T) {
	zipData := []byte("PK\x03\x04 a zip")
	api := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET /projects/p1/export": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write(zipData)
		},
		"GET /projects/gone/export": apiError(404, "not_found", "project not found"),
	})
	setup(t, api, nil)

	if r := run(t, "vis", "project", "export", "p1"); r.code != 0 || r.out != "p1.zip\n" {
		t.Fatalf("%+v", r)
	}
	if b, _ := os.ReadFile("p1.zip"); !bytes.Equal(b, zipData) {
		t.Fatalf("p1.zip = %q", b)
	}

	// An existing file is kept, without a request.
	if r := run(t, "vis", "project", "export", "p1"); r.code != cmdutil.ExitUsage || !strings.Contains(r.errOut, "--force") {
		t.Fatalf("%+v", r)
	}
	if n := len(api.reqs()); n != 1 {
		t.Fatalf("made %d requests, want 1", n)
	}
	if r := run(t, "vis", "project", "export", "p1", "-o", "out/x.zip", "--force"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if r := run(t, "vis", "project", "export", "p1", "-o", "-"); r.code != 0 || r.out != string(zipData) {
		t.Fatalf("stdout: %+v", r)
	}

	// A failed export leaves no file behind.
	if r := run(t, "vis", "project", "export", "gone"); r.code != cmdutil.ExitNotFound {
		t.Fatalf("%+v", r)
	}
	entries, _ := os.ReadDir(".")
	for _, e := range entries {
		if e.Name() == "gone.zip" || strings.HasPrefix(e.Name(), ".export-") {
			t.Fatalf("left %s behind", e.Name())
		}
	}
}

// importChunk is a chunk the fake API received.
type importChunk struct {
	fileID, chunkNum, totalChunks string
	size                          int
}

func parseImportChunk(t *testing.T, r request) importChunk {
	t.Helper()
	_, params, err := mime.ParseMediaType(r.ContentType)
	if err != nil {
		t.Fatalf("content type %q: %v", r.ContentType, err)
	}
	mr := multipart.NewReader(bytes.NewReader(r.Body), params["boundary"])
	var c importChunk
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(p)
		switch p.FormName() {
		case "file_id":
			c.fileID = string(b)
		case "chunk_num":
			c.chunkNum = string(b)
		case "total_chunks":
			c.totalChunks = string(b)
		case "file":
			c.size = len(b)
		}
	}
	return c
}

func TestProjectImport(t *testing.T) {
	importPollInterval = time.Millisecond
	t.Cleanup(func() { importPollInterval = 2 * time.Second })

	var mu sync.Mutex
	polls := map[string]int{}
	statuses := map[string][]string{
		"p-ok":   {"PROCESSING", "SUCCESS"},
		"p-fail": {"PROCESSING", "FAILED"},
	}
	next := "p-ok"
	api := newFakeAPI(t, map[string]http.HandlerFunc{
		// This server imports through chunks, as a self-hosted one does.
		"POST /workspaces/ws1/projects/import-url": apiError(http.StatusNotImplemented, "unsupported", "use chunked upload"),
		"POST /workspaces/ws1/projects/import": func(w http.ResponseWriter, r *http.Request) {
			_ = r.ParseMultipartForm(32 << 20)
			done := r.FormValue("chunk_num") == "1" || r.FormValue("total_chunks") == "1"
			writeJSON(w, 200, map[string]any{"projectId": next, "received": 1, "total": 2, "completed": done})
		},
		"GET /projects/{id}": func(w http.ResponseWriter, r *http.Request) {
			id := r.PathValue("id")
			if id == "p-gone" {
				apiError(404, "not_found", "project not found")(w, r)
				return
			}
			mu.Lock()
			s := statuses[id][min(polls[id], len(statuses[id])-1)]
			polls[id]++
			mu.Unlock()
			p := projectJSON(id, "Imported")
			p["importStatus"] = s
			if s == "FAILED" {
				p["importError"] = "zip: not a valid zip file"
			}
			writeJSON(w, 200, p)
		},
	})
	setup(t, api, envvar.Map{"REEARTH_VISUALIZER_WORKSPACE": "ws1"})

	// A zip one chunk and 100 bytes long goes up in two chunks.
	data := append([]byte("PK\x03\x04"), bytes.Repeat([]byte("x"), importChunkSize+96)...)
	if err := os.WriteFile("big.zip", data, 0o644); err != nil {
		t.Fatal(err)
	}
	r := run(t, "vis", "project", "import", "big.zip", "--jq", ".id")
	if r.code != 0 || strings.TrimSpace(r.out) != "p-ok" {
		t.Fatalf("%+v", r)
	}
	var chunks []importChunk
	for _, req := range api.reqs() {
		if req.Path == "/workspaces/ws1/projects/import" {
			chunks = append(chunks, parseImportChunk(t, req))
		}
	}
	if len(chunks) != 2 ||
		chunks[0].size != importChunkSize || chunks[1].size != 100 ||
		chunks[0].chunkNum != "0" || chunks[1].chunkNum != "1" ||
		chunks[0].totalChunks != "2" || chunks[1].totalChunks != "2" ||
		chunks[0].fileID != chunks[1].fileID || !strings.HasPrefix(chunks[0].fileID, "cli-") {
		t.Fatalf("chunks: %+v", chunks)
	}

	// A failed import reports the server's reason.
	next = "p-fail"
	if err := os.WriteFile("small.zip", []byte("PK\x03\x04 small"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := run(t, "vis", "project", "import", "small.zip"); r.code != cmdutil.ExitError || !strings.Contains(r.errOut, "not a valid zip file") {
		t.Fatalf("failed import: %+v", r)
	}

	// A project that disappears while importing counts as failed.
	next = "p-gone"
	if r := run(t, "vis", "project", "import", "small.zip"); r.code != cmdutil.ExitError || !strings.Contains(r.errOut, "removed") {
		t.Fatalf("removed project: %+v", r)
	}

	// --no-wait returns the ID without polling.
	next = "p-ok"
	before := len(api.reqs())
	if r := run(t, "vis", "project", "import", "small.zip", "--no-wait"); r.code != 0 || r.out != "p-ok\n" {
		t.Fatalf("--no-wait: %+v", r)
	}
	for _, got := range api.reqs()[before:] {
		if got.Method != http.MethodPost {
			t.Fatalf("--no-wait polled: %+v", got)
		}
	}

	// A file that is not a zip is refused before any upload.
	before = len(api.reqs())
	if err := os.WriteFile("notes.txt", []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := run(t, "vis", "project", "import", "notes.txt"); r.code != cmdutil.ExitUsage {
		t.Fatalf("not a zip: %+v", r)
	}
	if len(api.reqs()) != before {
		t.Fatal("uploaded a file that is not a zip")
	}
}

func TestProjectImportSignedURL(t *testing.T) {
	importPollInterval = time.Millisecond
	t.Cleanup(func() { importPollInterval = 2 * time.Second })

	// A fake storage bucket behind the signed URL. The first upload attempt
	// gets a temporary error, which the CLI retries.
	var mu sync.Mutex
	var uploads [][]byte
	attempts := 0
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/bucket/import/x.zip" || r.URL.Query().Get("sig") != "abc" {
			t.Errorf("unexpected upload %s %s", r.Method, r.URL)
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("the Re:Earth token was sent to storage")
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/octet-stream" {
			t.Errorf("Content-Type = %q", ct)
		}
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		uploads = append(uploads, b)
	}))
	t.Cleanup(storage.Close)

	polls := 0
	api := newFakeAPI(t, map[string]http.HandlerFunc{
		"POST /workspaces/ws1/projects/import-url": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusCreated, map[string]any{
				"projectId": "p-new", "uploadUrl": storage.URL + "/bucket/import/x.zip?sig=abc",
				"contentType": "application/octet-stream", "expiresAt": "2026-10-13T00:15:00Z",
			})
		},
		"POST /workspaces/ws1/projects/import": func(w http.ResponseWriter, _ *http.Request) {
			t.Error("used chunked upload on a server with upload URLs")
		},
		"GET /projects/p-new": func(w http.ResponseWriter, _ *http.Request) {
			p := projectJSON("p-new", "Imported")
			p["importStatus"] = map[bool]string{true: "UPLOADING", false: "SUCCESS"}[polls == 0]
			polls++
			writeJSON(w, http.StatusOK, p)
		},
	})
	setup(t, api, envvar.Map{"REEARTH_VISUALIZER_WORKSPACE": "ws1"})

	data := []byte("PK\x03\x04 the project zip")
	if err := os.WriteFile("p.zip", data, 0o644); err != nil {
		t.Fatal(err)
	}
	r := run(t, "vis", "project", "import", "p.zip", "--jq", ".id")
	if r.code != 0 || strings.TrimSpace(r.out) != "p-new" {
		t.Fatalf("%+v", r)
	}
	if len(uploads) != 1 || !bytes.Equal(uploads[0], data) {
		t.Fatalf("uploaded %d times: %q", len(uploads), uploads)
	}

	// A rejected upload fails the command.
	storage.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("SignatureDoesNotMatch"))
	})
	if r := run(t, "vis", "project", "import", "p.zip"); r.code != cmdutil.ExitError || !strings.Contains(r.errOut, "SignatureDoesNotMatch") {
		t.Fatalf("rejected upload: %+v", r)
	}
}

func TestPluginPushAndList(t *testing.T) {
	api := newFakeAPI(t, map[string]http.HandlerFunc{
		"POST /projects/p1/plugins": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, 200, map[string]any{"id": "sc1~hello~1.0.0", "name": "hello", "version": "1.0.0", "system": false, "extensions": []any{}})
		},
		"GET /projects/p1/plugins": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, 200, map[string]any{"items": []any{
				map[string]any{"id": "reearth", "name": "Re:Earth", "version": "0.0.0", "system": true, "extensions": []any{}},
				map[string]any{"id": "sc1~hello~1.0.0", "name": "hello", "version": "1.0.0", "system": false,
					"extensions": []any{map[string]any{"id": "hello", "type": "widget", "name": "hello"}}},
			}})
		},
	})
	setup(t, api, nil)

	if r := run(t, "vis", "plugin", "init", "hello"); r.code != 0 {
		t.Fatalf("init: %+v", r)
	}
	// Packed as plugin pack does: every file except hidden ones and package/.
	if err := os.WriteFile(filepath.Join("hello", "style.css"), []byte("body{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("hello", ".env"), []byte("SECRET=1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := run(t, "vis", "plugin", "push", "--dir", "hello", "-p", "p1"); r.code != 0 || r.out != "sc1~hello~1.0.0\n" {
		t.Fatalf("push: %+v", r)
	}
	req := api.reqs()[0]
	if req.ContentType != "application/zip" {
		t.Fatalf("content type %q", req.ContentType)
	}
	zr, err := zip.NewReader(bytes.NewReader(req.Body), int64(len(req.Body)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	if strings.Join(names, ",") != "hello.js,reearth.yml,style.css" {
		t.Fatalf("zip entries: %v", names)
	}

	if r := run(t, "vis", "plugin", "push", "--url", "https://github.com/owner/hello", "-p", "p1"); r.code != 0 {
		t.Fatalf("push --url: %+v", r)
	}
	if b := jsonBody(t, api.reqs()[1]); b["url"] != "https://github.com/owner/hello" {
		t.Fatalf("push --url body: %v", b)
	}
	for _, u := range []string{"https://example.com/p.zip", "https://github.com/owner", "ftp://github.com/owner/hello"} {
		if r := run(t, "vis", "plugin", "push", "--url", u, "-p", "p1"); r.code != cmdutil.ExitUsage {
			t.Fatalf("push --url %s: %+v", u, r)
		}
	}
	if r := run(t, "vis", "plugin", "push", "--dir", "hello", "--url", "https://github.com/owner/hello", "-p", "p1"); r.code != cmdutil.ExitUsage {
		t.Fatalf("push --dir --url: %+v", r)
	}
	if n := len(api.reqs()); n != 2 {
		t.Fatalf("rejected pushes reached the API: %d requests", n)
	}

	// The built-in plugin is hidden unless --all is passed.
	if r := run(t, "vis", "plugin", "list", "-p", "p1", "--jq", "[.[].id]"); strings.TrimSpace(r.out) != `["sc1~hello~1.0.0"]` {
		t.Fatalf("list: %+v", r)
	}
	if r := run(t, "vis", "plugin", "list", "-p", "p1", "--all", "--jq", "length"); strings.TrimSpace(r.out) != "2" {
		t.Fatalf("list --all: %+v", r)
	}
}

func TestProjectViewWeb(t *testing.T) {
	api := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET /projects/p1": func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, projectJSON("p1", "A")) },
	})
	setup(t, api, envvar.Map{"REEARTH_VISUALIZER_WEB_URL": "http://localhost:3000/"})
	if r := run(t, "vis", "project", "view", "p1", "--web"); r.code != 0 || r.out != "http://localhost:3000/scene/sc1/map\n" {
		t.Fatalf("%+v", r)
	}

	setup(t, api, nil)
	if r := run(t, "vis", "project", "view", "p1", "--web"); r.code != cmdutil.ExitUsage || !strings.Contains(r.errOut, "REEARTH_VISUALIZER_WEB_URL") {
		t.Fatalf("no web URL: %+v", r)
	}
}

func TestHumanBytes(t *testing.T) {
	for n, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 KB", 1536: "1.5 KB", 16 << 20: "16.0 MB"} {
		if got := humanBytes(n); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", n, got, want)
		}
	}
}
