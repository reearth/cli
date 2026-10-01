package docs

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// full mimics llms-full.txt as docs.reearth.io serves it today: a <SYSTEM>
// line, the 404 page without a description, a home page whose body repeats
// its title as a heading, and two pages with the same title.
const full = `<SYSTEM>This is the full developer documentation for Re:Earth Docs</SYSTEM>

# Page not found

ページが見つかりません。

# Re:Earth ドキュメント

> Re:Earth の各プロダクトを学び、活用するための情報をまとめています。

# Re:Earth ドキュメント

[CMS を始める](/ja/cms/)

# 参照フィールドでモデルどうしをつなぐ

> Re:Earth CMS で参照フィールドを追加する手順を説明します。

## 手順

[Section titled “手順”](#手順)

参照フィールドを追加します。

` + "```sh\n# not a page\n\n> not a description\n```" + `

# 概要

> Re:Earth CMS が提供する API の全体像。

API の説明。

# 概要

> プラグイン API の概要

プラグイン API の説明。
`

func TestParse(t *testing.T) {
	pages := Parse(full)
	var ids []string
	for _, p := range pages {
		ids = append(ids, p.ID)
	}
	if got := strings.Join(ids, "|"); got != "Re:Earth ドキュメント|参照フィールドでモデルどうしをつなぐ|概要 (1)|概要 (2)" {
		t.Fatalf("ids = %s", got)
	}
	if !strings.HasPrefix(pages[0].Body, "# Re:Earth ドキュメント") {
		t.Errorf("home body = %q", pages[0].Body)
	}
	ref := pages[1]
	if ref.Description != "Re:Earth CMS で参照フィールドを追加する手順を説明します。" {
		t.Errorf("description = %q", ref.Description)
	}
	if strings.Contains(ref.Body, "Section titled") || !strings.Contains(ref.Body, "## 手順\n\n参照フィールドを追加します。") {
		t.Errorf("anchor not removed:\n%s", ref.Body)
	}
	if !strings.Contains(ref.Body, "# not a page") {
		t.Errorf("a heading in a code block split the page:\n%s", ref.Body)
	}
}

func TestParseSourceURLs(t *testing.T) {
	pages := Parse(`# 概要

Source: https://docs.reearth.io/ja/developer/cms/overview/

> Re:Earth CMS が提供する API の全体像。

API の説明。

# 概要

Source: https://docs.reearth.io/ja/developer/plugin/api-reference/

> プラグイン API の概要
`)
	if len(pages) != 2 || pages[0].ID != "ja/developer/cms/overview" || pages[1].ID != "ja/developer/plugin/api-reference" {
		t.Fatalf("pages = %+v", pages)
	}
	if pages[0].Body != "API の説明。" {
		t.Errorf("body = %q", pages[0].Body)
	}
	for _, ref := range []string{"ja/developer/cms/overview", "/ja/developer/cms/overview/", "https://docs.reearth.io/ja/developer/cms/overview/"} {
		if p, err := Find(pages, ref); err != nil || p.ID != pages[0].ID {
			t.Errorf("Find(%q) = %v, %v", ref, p.ID, err)
		}
	}
	var ae *AmbiguousError
	if _, err := Find(pages, "概要"); !errors.As(err, &ae) || strings.Join(ae.IDs, "|") != "ja/developer/cms/overview|ja/developer/plugin/api-reference" {
		t.Errorf("an ambiguous title must not resolve: %v", err)
	}
}

func TestFindDuplicateTitles(t *testing.T) {
	pages := Parse(full)
	var ae *AmbiguousError
	if _, err := Find(pages, "概要"); !errors.As(err, &ae) || strings.Join(ae.IDs, "|") != "概要 (1)|概要 (2)" {
		t.Fatalf("a shared title resolved to one page: %v", err)
	}
	for i, id := range ae.IDs {
		if p, err := Find(pages, id); err != nil || p.ID != id || p.Description != pages[2+i].Description {
			t.Errorf("Find(%q) = %+v, %v", id, p, err)
		}
	}
	if _, err := Find(pages, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing page: %v", err)
	}
}

func TestAssignIDsAvoidsTakenNumbers(t *testing.T) {
	pages := []Page{{Title: "FAQ"}, {Title: "FAQ (1)"}, {Title: "FAQ"}}
	assignIDs(pages)
	seen := map[string]bool{}
	for _, p := range pages {
		if seen[p.ID] || p.ID == "FAQ" {
			t.Errorf("ids = %+v", pages)
		}
		seen[p.ID] = true
		if got, err := Find(pages, p.ID); err != nil || got.ID != p.ID {
			t.Errorf("Find(%q) = %+v, %v", p.ID, got, err)
		}
	}
}

func TestFindTitleIDsWithSlashes(t *testing.T) {
	pages := []Page{{Title: "/api/v1 説明"}, {Title: "https://example.com/x"}, {Title: "x"}}
	assignIDs(pages)
	for _, p := range pages {
		if got, err := Find(pages, p.ID); err != nil || got.ID != p.ID {
			t.Errorf("Find(%q) = %+v, %v", p.ID, got, err)
		}
	}
	// Normalized forms still apply when the verbatim ref matches nothing.
	if got, err := Find(pages, "/x/"); err != nil || got.ID != "x" {
		t.Errorf("Find(/x/) = %+v, %v", got, err)
	}
}

func TestFindAndMarkdown(t *testing.T) {
	pages := Parse(full)
	p, err := Find(pages, "Re:Earth ドキュメント")
	if err != nil {
		t.Fatal(err)
	}
	md := p.Markdown("https://docs.reearth.io/")
	if !strings.Contains(md, "[CMS を始める](https://docs.reearth.io/ja/cms/)") {
		t.Errorf("relative link kept:\n%s", md)
	}
}

func TestParseFences(t *testing.T) {
	page := func(title string) string { return "# " + title + "\n\n> " + title + " desc\n\n" }
	for name, body := range map[string]string{
		"info string inside":            "```md\n```js\n# a\n\n> b\n```\n",
		"longer opening":                "````md\n```\n# a\n\n> b\n```\n````\n",
		"tilde not closed by backticks": "~~~\n```\n# a\n\n> b\n~~~\n",
		"closing with text":             "```\n``` x\n# a\n\n> b\n```\n",
		"indented code":                 "    ```\n",
		"indented up to three":          "   ```\n# a\n\n> b\n   ```\n",
	} {
		if pages := Parse(page("One") + body + page("Two")); len(pages) != 2 {
			t.Errorf("%s: %d pages: %+v", name, len(pages), pages)
		}
	}
}

func TestAbsoluteLinks(t *testing.T) {
	in := strings.Join([]string{
		"[a](/ja/a/) and `[b](/ja/b/)` and ``x ` [c](/ja/c/)``",
		"<img src=\"/img.png\"> [cdn](//cdn.example.com/x.js) [ext](https://x.test/)",
		"```md",
		"[d](/ja/d/)",
		"```",
		"[e](/ja/e/)",
		"",
		"    [f](/ja/f/)",
		"",
		"\t[g](/ja/g/)",
		"[h](/ja/h/)",
		"text",
		"    [i](/ja/i/)",
		"",
		"1. item",
		"",
		"    [j](/ja/j/)",
		"    ```",
		"    [k](/ja/k/)",
		"    ```",
		"",
		"para",
		"",
		"    [l](/ja/l/)",
	}, "\n")
	want := strings.Join([]string{
		"[a](https://s.test/ja/a/) and `[b](/ja/b/)` and ``x ` [c](/ja/c/)``",
		"<img src=\"https://s.test/img.png\"> [cdn](//cdn.example.com/x.js) [ext](https://x.test/)",
		"```md",
		"[d](/ja/d/)",
		"```",
		"[e](https://s.test/ja/e/)",
		"",
		"    [f](/ja/f/)",
		"",
		"\t[g](/ja/g/)",
		"[h](https://s.test/ja/h/)",
		"text",
		"    [i](https://s.test/ja/i/)",
		"",
		"1. item",
		"",
		"    [j](https://s.test/ja/j/)",
		"    ```",
		"    [k](/ja/k/)",
		"    ```",
		"",
		"para",
		"",
		"    [l](/ja/l/)",
	}, "\n")
	if got := absoluteLinks(in, "https://s.test"); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestSearch(t *testing.T) {
	pages := Parse(full)
	for query, want := range map[string]string{
		"参照フィールド":   "参照フィールドでモデルどうしをつなぐ",
		"プラグイン API": "概要 (2)",
		"api 全体像":   "概要 (1)",
	} {
		got := Search(pages, query, 5)
		if len(got) == 0 || got[0].ID != want {
			t.Errorf("%q: got %+v, want %s first", query, got, want)
		}
	}
	if got := Search(pages, "存在しない語彙", 5); len(got) != 0 {
		t.Errorf("unrelated query matched: %+v", got)
	}
}

func TestTokenize(t *testing.T) {
	got := strings.Join(tokenize("参照フィールド GeoJSON"), " ")
	if got != "参照 照フ フィ ィー ール ルド geojson" {
		t.Errorf("tokens = %s", got)
	}
}

func TestSourceCache(t *testing.T) {
	var requests, full200 atomic.Int32
	var offline atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if offline.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		full200.Add(1)
		w.Header().Set("ETag", `"v1"`)
		_, _ = w.Write([]byte(full))
	}))
	defer srv.Close()

	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	s := &Source{Site: srv.URL, Dir: t.TempDir(), Client: &http.Client{}, MaxAge: time.Hour, Now: func() time.Time { return now }}
	load := func() ([]Page, bool) {
		t.Helper()
		pages, warn, err := s.Load(context.Background())
		if err != nil || len(pages) != 4 {
			t.Fatalf("pages %d, err %v", len(pages), err)
		}
		var stale *StaleError
		return pages, errors.As(warn, &stale)
	}

	load()
	load() // fresh: no request
	if requests.Load() != 1 {
		t.Fatalf("requests = %d", requests.Load())
	}
	now = now.Add(2 * time.Hour)
	load() // revalidated: 304
	if requests.Load() != 2 || full200.Load() != 1 {
		t.Fatalf("requests = %d, full downloads = %d", requests.Load(), full200.Load())
	}
	now = now.Add(2 * time.Hour)
	offline.Store(true)
	if _, stale := load(); !stale {
		t.Error("offline load must report a stale copy")
	}

	empty := &Source{Site: srv.URL, Dir: t.TempDir(), Client: &http.Client{}, MaxAge: time.Hour}
	var se *StatusError
	if _, _, err := empty.Load(context.Background()); !errors.As(err, &se) {
		t.Errorf("no copy and no network must fail with the status: %v", err)
	}
}

func TestSourceRejectsPagelessBody(t *testing.T) {
	var body atomic.Value
	body.Store(full)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body.Load().(string)))
	}))
	defer srv.Close()

	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	s := &Source{Site: srv.URL, Dir: t.TempDir(), Client: &http.Client{}, MaxAge: time.Hour, Now: func() time.Time { return now }}
	if pages, warn, err := s.Load(context.Background()); err != nil || warn != nil || len(pages) != 4 {
		t.Fatalf("pages %d, warn %v, err %v", len(pages), warn, err)
	}
	for _, b := range []string{"", "<!doctype html><title>Error</title>"} {
		body.Store(b)
		now = now.Add(2 * time.Hour)
		pages, warn, err := s.Load(context.Background())
		var stale *StaleError
		if err != nil || !errors.As(warn, &stale) || !errors.Is(warn, ErrNoPages) || len(pages) != 4 {
			t.Fatalf("%q: pages %d, warn %v, err %v", b, len(pages), warn, err)
		}
	}
	empty := &Source{Site: srv.URL, Dir: t.TempDir(), Client: &http.Client{}, MaxAge: time.Hour}
	if _, _, err := empty.Load(context.Background()); !errors.Is(err, ErrNoPages) {
		t.Errorf("a pageless body with no copy: %v", err)
	}
}

func TestSourceKeyedBySite(t *testing.T) {
	serve := func(title, etag string, gotETag *atomic.Value) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotETag.Store(r.Header.Get("If-None-Match"))
			w.Header().Set("ETag", etag)
			_, _ = w.Write([]byte("# " + title + "\n\n> d\n"))
		}))
	}
	var etagA, etagB atomic.Value
	a, b := serve("A", `"a"`, &etagA), serve("B", `"b"`, &etagB)
	defer a.Close()
	defer b.Close()

	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	load := func(site string) string {
		t.Helper()
		s := &Source{Site: site, Dir: dir, Client: &http.Client{}, MaxAge: time.Hour, Now: func() time.Time { return now }}
		pages, _, err := s.Load(context.Background())
		if err != nil || len(pages) != 1 {
			t.Fatalf("pages %+v, err %v", pages, err)
		}
		return pages[0].Title
	}
	if load(a.URL) != "A" || load(b.URL) != "B" {
		t.Error("a fresh copy of another site was used")
	}
	now = now.Add(2 * time.Hour)
	if load(b.URL) != "B" || etagB.Load() != `"b"` {
		t.Errorf("revalidation of B sent ETag %v", etagB.Load())
	}
	if load(a.URL) != "A" || etagA.Load() != `"a"` {
		t.Errorf("revalidation of A sent ETag %v", etagA.Load())
	}
}

func TestSourceCacheWriteFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(full))
	}))
	defer srv.Close()
	dir := t.TempDir()
	// A file where the site's cache directory belongs makes every write fail.
	if err := os.WriteFile(filepath.Join(dir, siteKey(srv.URL)), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	pages, warn, err := (&Source{Site: srv.URL, Dir: dir, Client: &http.Client{}, MaxAge: time.Hour}).Load(context.Background())
	var ce *CacheError
	if err != nil || !errors.As(warn, &ce) || len(pages) != 4 {
		t.Errorf("pages %d, warn %v, err %v", len(pages), warn, err)
	}
}
