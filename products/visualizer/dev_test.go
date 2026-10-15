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
	write(viewer, playgroundPage, "<html>playground</html>")

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
	get := func(path string) (*http.Response, string) {
		t.Helper()
		resp, err := client.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp, string(b)
	}

	// / opens the viewer pointed at this server's plugin.
	resp, _ := get("/")
	if loc := resp.Header.Get("Location"); resp.StatusCode != http.StatusFound || loc != "/playground.html?dev-plugin="+url.QueryEscape(srv.URL+"/plugin") {
		t.Errorf("/ = %d %s", resp.StatusCode, loc)
	}
	if resp, body := get("/" + playgroundPage); resp.StatusCode != 200 || body != "<html>playground</html>" {
		t.Errorf("viewer = %d %q", resp.StatusCode, body)
	}
	if resp, body := get("/reearth_config.json"); resp.StatusCode != 200 || body != "{}" {
		t.Errorf("config = %d %q", resp.StatusCode, body)
	}
	for path, wantBody := range map[string]string{
		"/plugin/a.js":        "reearth.ui.show('a');",
		"/plugin/reearth.yml": "id: p\n",
	} {
		resp, body := get(path)
		if resp.StatusCode != 200 || !strings.HasPrefix(body, wantBody) || resp.Header.Get("Access-Control-Allow-Origin") != "*" {
			t.Errorf("%s = %d %q (CORS %q)", path, resp.StatusCode, body, resp.Header.Get("Access-Control-Allow-Origin"))
		}
	}
	if resp, _ := get("/plugin/missing.js"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("missing = %d", resp.StatusCode)
	}

	// Only what a published plugin exposes: no secrets, sources or dependencies.
	write(dir, ".env", "SECRET=1")
	write(dir, "notes.txt", "x")
	_ = os.MkdirAll(filepath.Join(dir, "node_modules", "x"), 0o755)
	write(filepath.Join(dir, "node_modules", "x"), "index.js", "x")
	_ = os.MkdirAll(filepath.Join(dir, ".git"), 0o755)
	write(filepath.Join(dir, ".git"), "hook.js", "x")
	for _, p := range []string{"/plugin/.env", "/plugin/notes.txt", "/plugin/node_modules/x/index.js", "/plugin/.git/hook.js", "/plugin/../a.js", "/plugin/%2e%2e/a.js"} {
		if resp, body := get(p); resp.StatusCode == http.StatusOK && body != "reearth.ui.show('a');" {
			t.Errorf("%s = %d %q", p, resp.StatusCode, body)
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
		"/" + playgroundPage:  http.StatusNotFound,
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
