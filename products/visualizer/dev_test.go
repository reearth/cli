package visualizer

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDevServer(t *testing.T) {
	dir, viewer := t.TempDir(), t.TempDir()
	write := func(base, name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(base, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(dir, manifestFile, "id: p\nname: P\nextensions:\n  - id: a\n    type: widget\n")
	write(dir, "a.js", "reearth.ui.show('a');")
	write(viewer, viewerHTMLPage, "<html>playground</html>")

	template, manifest, root, err := devPaths(dir)
	if err != nil || template != "js" || root != dir {
		t.Fatalf("devPaths = %q %q %v", template, root, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := newWatcher(manifest, root)
	go w.run(ctx)
	srv := httptest.NewServer(devHandler(viewer, manifest, root, w))
	defer srv.Close()

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(path string) (int, http.Header, string) {
		t.Helper()
		resp, err := client.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header, string(b)
	}

	// / opens the viewer pointed at this server's plugin.
	status, header, _ := get("/")
	if loc := header.Get("Location"); status != http.StatusFound || loc != "/workbench.html?dev-plugin="+url.QueryEscape(srv.URL+"/plugin") {
		t.Errorf("/ = %d %s", status, loc)
	}
	if status, _, body := get("/" + viewerHTMLPage); status != http.StatusOK || body != "<html>playground</html>" {
		t.Errorf("viewer = %d %q", status, body)
	}
	if status, _, body := get("/reearth_config.json"); status != http.StatusOK || body != "{}" {
		t.Errorf("config = %d %q", status, body)
	}
	for path, wantBody := range map[string]string{
		"/plugin/a.js":        "reearth.ui.show('a');",
		"/plugin/reearth.yml": "id: p\n",
	} {
		status, header, body := get(path)
		if cors := header.Get("Access-Control-Allow-Origin"); status != http.StatusOK || !strings.HasPrefix(body, wantBody) || cors != "*" {
			t.Errorf("%s = %d %q (CORS %q)", path, status, body, cors)
		}
	}
	if status, _, _ := get("/plugin/missing.js"); status != http.StatusNotFound {
		t.Errorf("missing = %d", status)
	}

	// Only what a published plugin exposes: no secrets, sources or dependencies.
	write(dir, ".env", "SECRET=1")
	write(dir, "notes.txt", "x")
	_ = os.MkdirAll(filepath.Join(dir, "node_modules", "x"), 0o755)
	write(filepath.Join(dir, "node_modules", "x"), "index.js", "x")
	_ = os.MkdirAll(filepath.Join(dir, ".git"), 0o755)
	write(filepath.Join(dir, ".git"), "hook.js", "x")
	for _, p := range []string{"/plugin/.env", "/plugin/notes.txt", "/plugin/node_modules/x/index.js", "/plugin/.git/hook.js", "/plugin/../a.js", "/plugin/%2e%2e/a.js"} {
		if status, _, body := get(p); status == http.StatusOK && body != "reearth.ui.show('a');" {
			t.Errorf("%s = %d %q", p, status, body)
		}
	}

	// A page that reaches the server through DNS rebinding is refused.
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/plugin/a.js", nil)
	req.Host = "evil.example:5180"
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusForbidden {
		t.Errorf("foreign Host: %v %v", resp, err)
	} else {
		_ = resp.Body.Close()
	}

	// A saved file is reported on the event stream.
	es, err := http.Get(srv.URL + "/plugin/events")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = es.Body.Close() }()
	if es.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Error("events: no CORS header")
	}
	lines := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(es.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
	}()
	time.Sleep(500 * time.Millisecond)
	future := time.Now().Add(time.Second)
	write(dir, "a.js", "reearth.ui.show('b');")
	_ = os.Chtimes(filepath.Join(dir, "a.js"), future, future)
	if !waitLine(lines, "data: a.js", 5*time.Second) {
		t.Fatal("no change event")
	}
}

func TestDevPaths(t *testing.T) {
	dir := t.TempDir()
	if _, _, _, err := devPaths(dir); err == nil {
		t.Error("empty dir should fail")
	}
	_ = os.MkdirAll(filepath.Join(dir, "public"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "public", manifestFile), []byte("id: t\n"), 0o644)
	template, _, root, err := devPaths(dir)
	if err != nil || template != "ts" || root != filepath.Join(dir, "dist") {
		t.Errorf("ts devPaths = %q %q %v", template, root, err)
	}
}

func TestViewerPageURL(t *testing.T) {
	for raw, want := range map[string]string{
		"http://localhost:3000":                 "http://localhost:3000/workbench.html",
		"http://localhost:3000/":                "http://localhost:3000/workbench.html",
		"http://localhost:3000/web":             "http://localhost:3000/web/workbench.html",
		"http://localhost:3000/workbench.html":  "http://localhost:3000/workbench.html",
		"https://example.com/p.html?lang=ja":    "https://example.com/p.html?lang=ja",
		"http://localhost:3000/?dev-plugin=old": "http://localhost:3000/workbench.html?dev-plugin=old",
	} {
		u, err := viewerPageURL(raw)
		if err != nil || u.String() != want {
			t.Errorf("viewerPageURL(%q) = %v %v, want %s", raw, u, err, want)
		}
	}
	for _, raw := range []string{"localhost:3000", "/workbench.html", "file:///tmp/workbench.html", "http://"} {
		if _, err := viewerPageURL(raw); err == nil {
			t.Errorf("viewerPageURL(%q) should fail", raw)
		}
	}
}

// dev opens a viewer it finds, and not another app on the port.
func TestDetectViewer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/" + viewerHTMLPage:
			_, _ = io.WriteString(rw, "<html><head>"+viewerTitle+"</head></html>")
		case "/other.html":
			_, _ = io.WriteString(rw, "<html><head><title>Another app</title></head></html>")
		default:
			http.NotFound(rw, r)
		}
	}))
	defer srv.Close()
	if u := detectViewer(context.Background(), srv.URL+"/"+viewerHTMLPage); u == nil || u.String() != srv.URL+"/"+viewerHTMLPage {
		t.Errorf("viewer = %v", u)
	}
	for _, p := range []string{"/other.html", "/missing.html"} {
		if u := detectViewer(context.Background(), srv.URL+p); u != nil {
			t.Errorf("%s = %v, want nil", p, u)
		}
	}
	addr := srv.Listener.Addr().String()
	srv.Close()
	if u := detectViewer(context.Background(), "http://"+addr+"/"+viewerHTMLPage); u != nil {
		t.Errorf("closed server = %v, want nil", u)
	}
}

// Without --viewer, only the plugin is served.
func TestDevServerWithoutViewer(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, manifestFile), []byte("id: p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(devHandler("", filepath.Join(dir, manifestFile), dir, newWatcher(filepath.Join(dir, manifestFile), dir)))
	defer srv.Close()
	for path, want := range map[string]int{
		"/plugin/reearth.yml": http.StatusOK,
		"/":                   http.StatusNotFound,
		"/" + viewerHTMLPage:  http.StatusNotFound,
	} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("%s = %d, want %d", path, resp.StatusCode, want)
		}
	}
}

// A build that keeps writing still gets a change event within maxSettle.
func TestWatcherContinuousWrites(t *testing.T) {
	dir := t.TempDir()
	manifest := filepath.Join(dir, manifestFile)
	_ = os.WriteFile(manifest, []byte("id: p\n"), 0o644)
	w := newWatcher(manifest, dir)
	ch := make(chan string, 4)
	w.subs[ch] = struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.run(ctx)

	stop := time.After(maxSettle + 2*time.Second)
	for i := 0; ; i++ {
		select {
		case <-ch:
			return
		case <-stop:
			t.Fatal("no change event while files kept changing")
		case <-time.After(100 * time.Millisecond):
			_ = os.WriteFile(filepath.Join(dir, "a.js"), []byte(strings.Repeat("x", i)), 0o644)
		}
	}
}

func waitLine(lines <-chan string, want string, timeout time.Duration) bool {
	deadline := time.After(timeout)
	for {
		select {
		case l := <-lines:
			if l == want {
				return true
			}
		case <-deadline:
			return false
		}
	}
}
