package visualizer_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/envvar"
	"github.com/reearth/cli/sdk/envvar/envvartest"
)

type apiRequest struct {
	Method, Path, Query string
}

type fakeAPI struct {
	srv      *httptest.Server
	mu       sync.Mutex
	requests []apiRequest
}

func newFakeAPI(t *testing.T, handlers map[string]http.HandlerFunc) *fakeAPI {
	t.Helper()
	api := &fakeAPI{}
	mux := http.NewServeMux()
	for pattern, h := range handlers {
		mux.HandleFunc(pattern, h)
	}
	api.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("%s %s: Authorization = %q", r.Method, r.URL.Path, got)
		}
		if r.Header.Get("X-Reearth-Debug-User") != "" {
			t.Errorf("%s %s: sent X-Reearth-Debug-User", r.Method, r.URL.Path)
		}
		api.mu.Lock()
		api.requests = append(api.requests, apiRequest{r.Method, r.URL.Path, r.URL.RawQuery})
		api.mu.Unlock()
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(api.srv.Close)
	return api
}

func (a *fakeAPI) reqs() []apiRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]apiRequest(nil), a.requests...)
}

func setupAPI(t *testing.T, api *fakeAPI, extra envvar.Map) {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	env := envvar.Map{
		"REEARTH_CONFIG_DIR":          filepath.Join(dir, "config"),
		"REEARTH_CACHE_DIR":           filepath.Join(dir, "cache"),
		"REEARTH_DATA_DIR":            filepath.Join(dir, "data"),
		"REEARTH_TOKEN":               "test-token",
		"REEARTH_VISUALIZER_BASE_URL": api.srv.URL,
	}
	for k, v := range extra {
		env[k] = v
	}
	envvartest.Fake(t, env)
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

func TestProjectListWorkspaceResolution(t *testing.T) {
	list := func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"items": []any{projectJSON("p1", "Parks")}, "totalCount": 1})
	}
	api := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET /workspaces/ws-env/projects":  list,
		"GET /workspaces/ws-flag/projects": list,
		"GET /workspaces/ws-file/projects": list,
	})

	setupAPI(t, api, envvar.Map{"REEARTH_VISUALIZER_WORKSPACE": "ws-env"})
	r := run(t, "vis", "project", "list")
	if r.code != 0 || !strings.HasPrefix(r.out, "p1\tParks\tprivate\t") {
		t.Fatalf("env workspace: %+v", r)
	}
	if r := run(t, "vis", "project", "list", "-w", "ws-flag"); r.code != 0 {
		t.Fatalf("flag workspace: %+v", r)
	}
	got := api.reqs()
	if got[0].Path != "/workspaces/ws-env/projects" || got[1].Path != "/workspaces/ws-flag/projects" {
		t.Fatalf("paths: %+v", got)
	}
	if got[0].Query != "limit=20" {
		t.Fatalf("query: %q", got[0].Query)
	}

	setupAPI(t, api, nil)
	if err := os.WriteFile(".reearth.yaml", []byte("visualizer:\n  workspace: ws-file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := run(t, "vis", "project", "list"); r.code != 0 {
		t.Fatalf("file workspace: %+v", r)
	}
	if p := api.reqs()[2].Path; p != "/workspaces/ws-file/projects" {
		t.Fatalf("file workspace path: %s", p)
	}

	setupAPI(t, api, nil)
	if r := run(t, "vis", "project", "list"); r.code != cmdutil.ExitUsage || !strings.Contains(r.errOut, "--workspace") {
		t.Fatalf("no workspace: %+v", r)
	}
	if n := len(api.reqs()); n != 3 {
		t.Fatalf("made %d requests, want 3", n)
	}
}

func TestProjectListAllFollowsCursor(t *testing.T) {
	api := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET /workspaces/ws1/projects": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("cursor") == "" {
				writeJSON(w, 200, map[string]any{"items": []any{projectJSON("p1", "A")}, "totalCount": 2, "nextCursor": "c1"})
				return
			}
			writeJSON(w, 200, map[string]any{"items": []any{projectJSON("p2", "B")}, "totalCount": 2})
		},
	})
	setupAPI(t, api, envvar.Map{"REEARTH_VISUALIZER_WORKSPACE": "ws1"})
	r := run(t, "vis", "project", "list", "--all", "--jq", "[.[].id]")
	if r.code != 0 || strings.TrimSpace(r.out) != `["p1","p2"]` {
		t.Fatalf("%+v", r)
	}
	if q := api.reqs()[1].Query; !strings.Contains(q, "cursor=c1") {
		t.Fatalf("second page query: %q", q)
	}
}

func TestProjectListRejectsFilterCombinations(t *testing.T) {
	api := newFakeAPI(t, nil)
	setupAPI(t, api, envvar.Map{"REEARTH_VISUALIZER_WORKSPACE": "ws1"})
	for _, args := range [][]string{
		{"--starred", "--deleted"},
		{"--starred", "--keyword", "park"},
		{"--deleted", "--sort", "name"},
		{"--limit", "0"},
	} {
		r := run(t, append([]string{"vis", "project", "list"}, args...)...)
		if r.code != cmdutil.ExitUsage {
			t.Errorf("%v: exit %d, want %d (%s)", args, r.code, cmdutil.ExitUsage, r.errOut)
		}
	}
	if n := len(api.reqs()); n != 0 {
		t.Fatalf("made %d requests, want 0", n)
	}
}

func TestProjectListAPIErrorsMapToExitCodes(t *testing.T) {
	api := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET /workspaces/missing/projects": apiError(404, "not_found", "workspace not found"),
		"GET /workspaces/bad/projects":     apiError(400, "invalid_input", `invalid workspace ID "bad"`),
		"GET /workspaces/expired/projects": apiError(401, "unauthorized", "unauthorized"),
		"GET /workspaces/broken/projects":  func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) },
	})
	setupAPI(t, api, nil)

	for ws, want := range map[string]int{
		"missing": cmdutil.ExitNotFound,
		"bad":     cmdutil.ExitUsage,
		"expired": cmdutil.ExitAuth,
		"broken":  cmdutil.ExitError,
	} {
		if r := run(t, "vis", "project", "list", "-w", ws); r.code != want {
			t.Errorf("%s: exit %d, want %d (%s)", ws, r.code, want, r.errOut)
		}
	}

	r := run(t, "vis", "project", "list", "-w", "missing", "--json")
	var e struct {
		Error struct{ Code, Message string }
	}
	if err := json.Unmarshal([]byte(r.errOut), &e); err != nil || e.Error.Code != "visualizer.not_found" || e.Error.Message != "workspace not found" {
		t.Fatalf("json error: %q", r.errOut)
	}
}
