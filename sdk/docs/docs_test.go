package docs

import (
	"context"
	"net/http"
	"net/http/httptest"
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
	if got := strings.Join(ids, "|"); got != "Re:Earth ドキュメント|参照フィールドでモデルどうしをつなぐ|概要|概要 (2)" {
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
		if p, ok := Find(pages, ref); !ok || p.ID != pages[0].ID {
			t.Errorf("Find(%q) = %v, %v", ref, p.ID, ok)
		}
	}
	if _, ok := Find(pages, "概要"); ok {
		t.Error("an ambiguous title must not resolve")
	}
}

func TestFindAndMarkdown(t *testing.T) {
	pages := Parse(full)
	p, ok := Find(pages, "Re:Earth ドキュメント")
	if !ok {
		t.Fatal("not found by title")
	}
	md := p.Markdown("https://docs.reearth.io/")
	if !strings.Contains(md, "[CMS を始める](https://docs.reearth.io/ja/cms/)") {
		t.Errorf("relative link kept:\n%s", md)
	}
	if p, ok := Find(pages, "概要 (2)"); !ok || p.Description != "プラグイン API の概要" {
		t.Errorf("numbered id = %+v, %v", p, ok)
	}
}

func TestSearch(t *testing.T) {
	pages := Parse(full)
	for query, want := range map[string]string{
		"参照フィールド":   "参照フィールドでモデルどうしをつなぐ",
		"プラグイン API": "概要 (2)",
		"api 全体像":   "概要",
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
		pages, stale, err := s.Load(context.Background())
		if err != nil || len(pages) != 4 {
			t.Fatalf("pages %d, err %v", len(pages), err)
		}
		return pages, stale
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
	if _, _, err := empty.Load(context.Background()); err == nil {
		t.Error("no copy and no network must fail")
	}
}
