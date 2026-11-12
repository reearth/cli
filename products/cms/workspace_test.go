package cms_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/envvar"
)

// The commands on the generated client call the workspace-scoped API.
var workspaceEnv = envvar.Map{"REEARTH_CMS_WORKSPACE": "ws1", "REEARTH_CMS_PROJECT": "proj"}

func query(t *testing.T, r request) url.Values {
	t.Helper()
	q, err := url.ParseQuery(r.Query)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func TestProjectList(t *testing.T) {
	api := newFakeServer(t, map[string]http.HandlerFunc{
		"GET /api/ws1/projects": reply(map[string]any{
			"projects": []any{map[string]any{"id": "p1", "alias": "parks-project", "name": "Parks"}},
			"page":     1, "perPage": 1, "totalCount": 2,
		}),
	})
	setup(t, api, nil)

	if r := run(t, "cms", "project", "list"); r.code != cmdutil.ExitUsage || !strings.Contains(r.errOut, "workspace") {
		t.Fatalf("no workspace: %+v", r)
	}
	r := runTTY(t, "cms", "project", "list", "-w", "ws1", "--per-page", "1", "--keyword", "park", "--sort", "updatedAt", "--dir", "desc")
	if r.code != 0 || !strings.Contains(r.out, "parks-project") || !strings.Contains(r.errOut, "--page 2") {
		t.Fatalf("table: %+v", r)
	}
	q := query(t, api.requests()[0])
	if q.Get("keyword") != "park" || q.Get("sort") != "updatedAt" || q.Get("dir") != "desc" || q.Get("perPage") != "1" {
		t.Fatalf("query: %v", q)
	}
	if got := api.requests()[0].Auth; got != "Bearer test-token" {
		t.Fatalf("auth: %q", got)
	}
	if r := run(t, "cms", "project", "list", "-w", "ws1", "--json=alias"); r.code != 0 || !strings.Contains(r.out, `"alias": "parks-project"`) {
		t.Fatalf("json: %+v", r)
	}
}

func TestListFlagsAreChecked(t *testing.T) {
	api := newFakeServer(t, nil)
	setup(t, api, workspaceEnv)
	for _, args := range [][]string{
		{"project", "list", "--per-page", "101"},
		{"project", "list", "--sort", "name"},
		{"project", "list", "--dir", "asc"},
		{"asset", "list", "--sort", "createdAt", "--dir", "up"},
		{"item", "list", "-m", "parks", "--ref", "draft"},
		{"item", "list", "-m", "parks", "--asset=some"},
	} {
		if r := run(t, append([]string{"cms"}, args...)...); r.code != cmdutil.ExitUsage {
			t.Errorf("%v: %+v", args, r)
		}
	}
	if n := len(api.requests()); n != 0 {
		t.Fatalf("made %d requests, want 0", n)
	}
}

func TestAssetList(t *testing.T) {
	api := newFakeServer(t, map[string]http.HandlerFunc{
		"GET /api/ws1/projects/proj/assets": reply(map[string]any{
			// As the API lists assets: no name, content type or update time.
			"items": []any{map[string]any{"id": "a1", "url": "https://files.example.com/assets/ab/c%20d/park%20map.geojson",
				"totalSize": 1536, "public": true}},
			"page": 1, "perPage": 50, "totalCount": 1,
		}),
	})
	setup(t, api, envvar.Map{"REEARTH_CMS_WORKSPACE": "ws1"})

	if r := run(t, "cms", "asset", "list"); r.code != cmdutil.ExitUsage || !strings.Contains(r.errOut, "project") {
		t.Fatalf("no project: %+v", r)
	}
	r := runTTY(t, "cms", "asset", "list", "-p", "proj")
	for _, want := range []string{"a1", "park map.geojson", "1.5 KB", "true"} {
		if r.code != 0 || !strings.Contains(r.out, want) {
			t.Fatalf("missing %q: %+v", want, r)
		}
	}
}

var schemaDoc = map[string]any{
	"$schema": "https://json-schema.org/draft/2020-12/schema",
	"type":    "object",
	"properties": map[string]any{
		"title":  map[string]any{"type": "string", "title": "Title", "x-fieldType": "text", "x-required": true},
		"status": map[string]any{"type": "string", "x-fieldType": "select", "x-options": []any{"draft", "published"}},
		"tags":   map[string]any{"type": "array", "x-fieldType": "text", "x-multiple": true, "items": map[string]any{"type": "string"}},
		"spots": map[string]any{"type": "array", "x-fieldType": "group", "x-multiple": true, "items": map[string]any{
			"type":       "object",
			"properties": map[string]any{"name": map[string]any{"type": "string", "x-fieldType": "text"}},
		}},
	},
}

func TestModelSchema(t *testing.T) {
	api := newFakeServer(t, map[string]http.HandlerFunc{
		"GET /api/ws1/projects/proj/models/parks/schema.json":          reply(schemaDoc),
		"GET /api/ws1/projects/proj/models/parks/metadata_schema.json": reply(map[string]any{"type": "object"}),
	})
	setup(t, api, workspaceEnv)

	r := runTTY(t, "cms", "model", "schema", "parks")
	if r.code != 0 {
		t.Fatalf("schema: %+v", r)
	}
	for _, want := range []string{"title", "text", "true", "Title", "draft, published", "tags", "text[]", "spots", "group[]", "spots[].name"} {
		if !strings.Contains(r.out, want) {
			t.Errorf("table missing %q:\n%s", want, r.out)
		}
	}
	// Machines get the JSON Schema as the API returns it.
	if r := run(t, "cms", "model", "schema", "parks", "--json"); r.code != 0 || !strings.Contains(r.out, `"x-fieldType": "select"`) {
		t.Fatalf("json: %+v", r)
	}
	if r := runTTY(t, "cms", "model", "schema", "parks", "--metadata"); r.code != 0 || !strings.Contains(r.errOut, "No fields") {
		t.Fatalf("metadata: %+v", r)
	}
}

func TestItemListOnTheWorkspaceAPI(t *testing.T) {
	api := newFakeServer(t, map[string]http.HandlerFunc{
		"GET /api/ws1/projects/proj/models/parks/items": reply(map[string]any{
			"items": []any{item}, "page": 1, "perPage": 50, "totalCount": 1,
		}),
	})
	setup(t, api, nil)

	// Search and order need the workspace-scoped API.
	for _, args := range [][]string{{"--keyword", "x"}, {"--sort", "createdAt"}, {"--ref", "public"}, {"--asset=all"}} {
		if r := run(t, append([]string{"cms", "item", "list", "-m", "parks", "-p", "proj"}, args...)...); r.code != cmdutil.ExitUsage {
			t.Errorf("%v without a workspace: %+v", args, r)
		}
	}

	r := run(t, "cms", "item", "list", "-m", "parks", "-w", "ws1", "-p", "proj",
		"--keyword", "tokyo", "--sort", "updatedAt", "--dir", "asc", "--ref", "public", "--asset=all")
	if r.code != 0 || !strings.Contains(r.out, "Hello") {
		t.Fatalf("list: %+v", r)
	}
	q := query(t, api.requests()[0])
	for k, want := range map[string]string{"keyword": "tokyo", "sort": "updatedAt", "dir": "asc", "ref": "public", "asset": "all"} {
		if q.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, q.Get(k), want)
		}
	}
	// --asset alone still means true.
	if r := run(t, "cms", "item", "list", "-m", "parks", "-w", "ws1", "-p", "proj", "--asset"); r.code != 0 {
		t.Fatalf("--asset: %+v", r)
	}
	if got := query(t, api.requests()[1]).Get("asset"); got != "true" {
		t.Fatalf("asset = %q, want true", got)
	}
}

func TestItemPublish(t *testing.T) {
	api := newFakeServer(t, map[string]http.HandlerFunc{
		"POST /api/ws1/projects/proj/models/parks/items/it1/publish":  reply(item),
		"POST /api/ws1/projects/proj/models/parks/items/gone/publish": status(http.StatusNotFound),
	})
	setup(t, api, workspaceEnv)

	if r := run(t, "cms", "item", "publish", "it1", "--yes"); r.code == 0 || !strings.Contains(r.errOut, `"model"`) {
		t.Fatalf("no model: %+v", r)
	}
	if r := run(t, "cms", "item", "publish", "it1", "-m", "parks"); r.code == 0 {
		t.Fatalf("published without confirmation: %+v", r)
	}
	if r := run(t, "cms", "item", "publish", "it1", "-m", "parks", "--yes", "--json=id"); r.code != 0 || !strings.Contains(r.out, `"id": "it1"`) {
		t.Fatalf("publish: %+v", r)
	}
	if r := run(t, "cms", "item", "publish", "gone", "-m", "parks", "--yes"); r.code != cmdutil.ExitNotFound {
		t.Fatalf("not found: %+v", r)
	}
	if n := len(api.requests()); n != 2 {
		t.Fatalf("made %d requests, want 2", n)
	}

	setup(t, api, envvar.Map{"REEARTH_CMS_WORKSPACE": "ws1", "REEARTH_CMS_PROJECT": "proj", "REEARTH_CMS_SAFE_MODE": "true"})
	if r := run(t, "cms", "item", "publish", "it1", "-m", "parks", "--yes"); r.code == 0 || !strings.Contains(r.errOut, "safe mode") {
		t.Fatalf("safe mode: %+v", r)
	}
}

func TestWorkspaceAPIErrors(t *testing.T) {
	api := newFakeServer(t, map[string]http.HandlerFunc{
		"GET /api/ws1/projects": func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
		},
		"GET /api/ws1/projects/proj/assets": func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, `{"error":"bad perPage"}`, http.StatusBadRequest)
		},
	})
	setup(t, api, workspaceEnv)
	if r := run(t, "cms", "project", "list"); r.code != cmdutil.ExitAuth {
		t.Errorf("401: %+v", r)
	}
	if r := run(t, "cms", "asset", "list"); r.code != cmdutil.ExitUsage || !strings.Contains(r.errOut, "bad perPage") {
		t.Errorf("400: %+v", r)
	}
}
