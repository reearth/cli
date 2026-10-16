package visualizer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/core"
)

// playgroundPage is the page of a Plugin Playground build
// (reearth-visualizer/web `yarn build:playground`).
const playgroundPage = "playground.html"

// defaultViewerURL is where `yarn playground` in reearth-visualizer/web
// serves the Plugin Playground. dev opens it when it is running and no
// viewer is given.
const defaultViewerURL = "http://localhost:3000/" + playgroundPage

// playgroundTitle tells the Plugin Playground apart from another app on the
// same port; a vite server answers any .html path with its index page.
const playgroundTitle = "<title>Re:Earth Visualizer Workbench</title>"

type devOptions struct {
	Dir       string `json:"dir"`
	Template  string `json:"template"`
	URL       string `json:"url,omitempty"`
	PluginURL string `json:"pluginUrl"`

	port      int
	noOpen    bool
	viewer    string
	viewerURL string
}

func newCmdPluginDev(f *core.Factory) *cobra.Command {
	o := &devOptions{}
	cmd := &cobra.Command{
		Use:   "dev",
		Short: "Serve a plugin locally, reloading on every save",
		Long: `Serve the plugin in --dir on a local server and notify viewers when its
files change. Stop it with Ctrl+C.

The plugin is served at <url>/plugin. Open a Visualizer viewer with
?dev-plugin=<url>/plugin to load it, such as a Plugin Playground build of
reearth-visualizer (web: ` + "`yarn build:playground`" + `). With --viewer, that build is
served from the same server and opened in a browser. With --viewer-url, a
viewer already running elsewhere, such as ` + "`yarn playground`" + ` in
reearth-visualizer/web, is opened with ?dev-plugin= set; a URL without an
.html page opens its playground.html. With neither, a Plugin Playground
running at ` + defaultViewerURL + ` is opened if there is one.

  <url>/plugin/reearth.yml        the manifest
  <url>/plugin/<extension-id>.js  each extension
  <url>/plugin/events             server-sent "change" events

Only reearth.yml and .js files are served, as Visualizer keeps only those
from an uploaded plugin; hidden files and node_modules never are. The server
listens on 127.0.0.1 and answers only requests addressed to it by a
loopback name. With --json, the URLs are printed as JSON once it is ready.

  js plugin     serves reearth.yml and <extension-id>.js from --dir
  ts template   serves dist/; keep ` + "`yarn dev-build`" + ` (or run ` + "`yarn build`" + `) running`,
		Example: `  # with ` + "`yarn playground`" + ` running in reearth-visualizer/web, opens it
  $ reearth visualizer plugin dev
  $ reearth visualizer plugin dev --dir my-plugin --port 8080
  $ reearth visualizer plugin dev --viewer ../reearth-visualizer/web/dist-playground
  $ reearth visualizer plugin dev --viewer-url http://localhost:3000`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return o.run(cmd.Context(), f)
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&o.Dir, "dir", ".", "Plugin directory")
	fl.IntVar(&o.port, "port", 5180, "Port to listen on (0 picks a free one)")
	fl.BoolVar(&o.noOpen, "no-open", false, "Don't open the viewer in a browser")
	fl.StringVar(&o.viewer, "viewer", "", "Serve this Plugin Playground build and open it")
	fl.StringVar(&o.viewerURL, "viewer-url", "", "Open the Plugin Playground running at this URL, such as `yarn playground`")
	return cmd
}

// devPaths locates the manifest and the directory the extension .js files are
// served from.
func devPaths(dir string) (template, manifest, root string, err error) {
	if _, err := os.Stat(filepath.Join(dir, manifestFile)); err == nil {
		return "js", filepath.Join(dir, manifestFile), dir, nil
	}
	if _, err := os.Stat(filepath.Join(dir, "public", manifestFile)); err == nil {
		return "ts", filepath.Join(dir, "public", manifestFile), filepath.Join(dir, "dist"), nil
	}
	return "", "", "", cmdutil.NewError(cmdutil.ExitNotFound, "visualizer.manifest_not_found",
		fmt.Sprintf("%s not found in %s", manifestFile, dir), "pass --dir, or create a plugin with `reearth visualizer plugin init`")
}

func (o *devOptions) run(ctx context.Context, f *core.Factory) error {
	template, manifest, root, err := devPaths(o.Dir)
	if err != nil {
		return err
	}
	o.Template = template
	if o.viewer != "" && o.viewerURL != "" {
		return cmdutil.FlagErrorf("--viewer and --viewer-url cannot be used together")
	}
	if o.viewer != "" {
		if _, err := os.Stat(filepath.Join(o.viewer, playgroundPage)); err != nil {
			return cmdutil.NewError(cmdutil.ExitNotFound, "visualizer.viewer_not_found",
				fmt.Sprintf("%s not found in %s", playgroundPage, o.viewer), "build it in reearth-visualizer/web with `yarn build:playground`")
		}
	}
	var page *url.URL
	switch {
	case o.viewerURL != "":
		if page, err = viewerPage(o.viewerURL); err != nil {
			return err
		}
	case o.viewer == "":
		page = detectViewer(ctx, defaultViewerURL)
	}

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", o.port))
	if err != nil {
		return cmdutil.NewError(cmdutil.ExitUsage, "visualizer.port_in_use", err.Error(), "pass another --port, or --port 0 for a free one")
	}
	base := fmt.Sprintf("http://%s/", ln.Addr())
	o.PluginURL = base + "plugin"
	if o.viewer != "" {
		o.URL = playgroundURL(base, o.PluginURL)
	} else if page != nil {
		q := page.Query()
		q.Set("dev-plugin", o.PluginURL)
		page.RawQuery = q.Encode()
		o.URL = page.String()
	}

	w := newWatcher(manifest, root)
	srv := &http.Server{
		Handler:           devHandler(o.viewer, manifest, root, w),
		ReadHeaderTimeout: 10 * time.Second,
		// Requests (the event streams) end with the command.
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	go w.run(ctx)
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
	}()

	// The URLs are the command's data: agents read them from --json.
	pr, err := f.Printer()
	if err != nil {
		return err
	}
	if err := pr.Print(o, func() error { return nil }); err != nil {
		return err
	}
	switch {
	case o.viewer != "":
		f.IO.Success("Plugin Playground running at %s", f.IO.ErrColor().Bold(o.URL))
		f.IO.Hint("Plugin served at %s (for ?dev-plugin=)", o.PluginURL)
	case o.URL != "":
		f.IO.Success("Plugin served at %s", f.IO.ErrColor().Bold(o.PluginURL))
		f.IO.Hint("Open the Plugin Playground at %s", o.URL)
	default:
		f.IO.Success("Plugin served at %s", f.IO.ErrColor().Bold(o.PluginURL))
		f.IO.Hint("Open a Visualizer viewer with ?dev-plugin=%s to load it", url.QueryEscape(o.PluginURL))
		f.IO.Hint("Or run `yarn playground` in reearth-visualizer/web and rerun this to open it")
	}
	if template == "ts" {
		f.IO.Hint("Serving the build in %s. Keep `yarn dev-build` running, or rerun `yarn build`, to update it", root)
		if _, err := os.Stat(root); err != nil {
			f.IO.Warn("dist/ does not exist yet")
		}
	}
	f.IO.Hint("Press Ctrl+C to stop")
	if o.URL != "" && !o.noOpen && f.IO.IsInteractive() && f.OpenBrowser != nil {
		_ = f.OpenBrowser(o.URL)
	}

	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func playgroundURL(base, pluginURL string) string {
	return base + playgroundPage + "?" + url.Values{"dev-plugin": {pluginURL}}.Encode()
}

// detectViewer returns pageURL if a Plugin Playground answers there, or nil.
func detectViewer(ctx context.Context, pageURL string) *url.URL {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil || resp.StatusCode != http.StatusOK || !strings.Contains(string(b), playgroundTitle) {
		return nil
	}
	u, err := url.Parse(pageURL)
	if err != nil {
		return nil
	}
	return u
}

// viewerPage parses --viewer-url. A URL that names no .html page, such as
// the root of `yarn playground`, gets the Plugin Playground page.
func viewerPage(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, cmdutil.FlagErrorf("--viewer-url must be an http or https URL, such as http://localhost:3000")
	}
	if path.Ext(u.Path) != ".html" {
		u.Path = strings.TrimSuffix(u.Path, "/") + "/" + playgroundPage
	}
	return u, nil
}

// devHandler serves the plugin at /plugin/ and, when viewer is set, that
// viewer build at /. The plugin routes allow any origin, so a viewer hosted
// elsewhere can load them too; that is why they serve only what a published
// plugin exposes anyway. Every route requires a loopback Host, so a web page
// cannot reach the server through DNS rebinding.
func devHandler(viewer, manifest, root string, w *watcher) http.Handler {
	mux := http.NewServeMux()
	if viewer != "" {
		mux.HandleFunc("GET /{$}", func(rw http.ResponseWriter, r *http.Request) {
			http.Redirect(rw, r, playgroundURL("/", "http://"+r.Host+"/plugin"), http.StatusFound)
		})
		// The viewer reads it if present; the defaults are enough.
		mux.HandleFunc("GET /reearth_config.json", func(rw http.ResponseWriter, r *http.Request) {
			rw.Header().Set("Content-Type", "application/json")
			_, _ = rw.Write([]byte("{}"))
		})
		mux.Handle("GET /", http.FileServer(http.Dir(viewer)))
	}

	mux.HandleFunc("GET /plugin/events", cors(w.serveEvents))
	mux.HandleFunc("GET /plugin/"+manifestFile, cors(func(rw http.ResponseWriter, r *http.Request) {
		// A ts build copies public/reearth.yml into dist/; before the first
		// build, serve the source.
		p := filepath.Join(root, manifestFile)
		if _, err := os.Stat(p); err != nil {
			p = manifest
		}
		rw.Header().Set("Content-Type", "text/yaml; charset=utf-8")
		http.ServeFile(rw, r, p)
	}))
	mux.HandleFunc("GET /plugin/", cors(func(rw http.ResponseWriter, r *http.Request) {
		rel, ok := servablePath(strings.TrimPrefix(r.URL.Path, "/plugin/"))
		if !ok {
			http.NotFound(rw, r)
			return
		}
		http.ServeFile(rw, r, filepath.Join(root, filepath.FromSlash(rel)))
	}))
	return loopbackOnly(mux)
}

// servablePath reports whether a plugin file may be served: a .js file, not
// hidden and not in node_modules. reearth.yml has a route of its own.
func servablePath(p string) (string, bool) {
	p = path.Clean("/" + p)[1:]
	if p == "" || !strings.EqualFold(path.Ext(p), ".js") {
		return "", false
	}
	for _, seg := range strings.Split(p, "/") {
		if strings.HasPrefix(seg, ".") || seg == "node_modules" {
			return "", false
		}
	}
	return p, true
}

// loopbackOnly rejects requests whose Host is not a loopback name.
func loopbackOnly(h http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		switch strings.Trim(host, "[]") {
		case "127.0.0.1", "localhost", "::1":
			h.ServeHTTP(rw, r)
		default:
			http.Error(rw, "forbidden host", http.StatusForbidden)
		}
	})
}

func cors(h http.HandlerFunc) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Access-Control-Allow-Origin", "*")
		rw.Header().Set("Cache-Control", "no-store")
		h(rw, r)
	}
}

// watcher polls the manifest and served files and tells viewers when they
// change. Polling keeps it dependency-free and works the same everywhere.
type watcher struct {
	manifest, root string

	mu   sync.Mutex
	subs map[chan string]struct{}
}

func newWatcher(manifest, root string) *watcher {
	return &watcher{manifest: manifest, root: root, subs: map[chan string]struct{}{}}
}

type fileStamp struct {
	mod  time.Time
	size int64
}

func (w *watcher) scan() map[string]fileStamp {
	out := map[string]fileStamp{}
	add := func(p string, info fs.FileInfo) {
		out[p] = fileStamp{info.ModTime(), info.Size()}
	}
	if info, err := os.Stat(w.manifest); err == nil {
		add(w.manifest, info)
	}
	_ = filepath.WalkDir(w.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != w.root && (strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(p, ".js") || filepath.Base(p) == manifestFile {
			if info, err := d.Info(); err == nil {
				add(p, info)
			}
		}
		return nil
	})
	return out
}

// maxSettle bounds how long the watcher waits for files to stop changing,
// so a build that keeps writing still gets its changes reported.
const maxSettle = 2 * time.Second

func (w *watcher) run(ctx context.Context) {
	t := time.NewTicker(400 * time.Millisecond)
	defer t.Stop()
	last := w.scan()
	var pending []string
	var since time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		cur := w.scan()
		changed := diffStamps(last, cur)
		last = cur
		if len(changed) > 0 {
			if len(pending) == 0 {
				since = time.Now()
			}
			pending = append(pending, changed...)
			if time.Since(since) < maxSettle {
				continue // wait until a build finishes writing
			}
		}
		if len(pending) > 0 {
			slices.Sort(pending)
			w.broadcast(strings.Join(slices.Compact(pending), ", "))
			pending = nil
		}
	}
}

func diffStamps(a, b map[string]fileStamp) []string {
	var out []string
	for p, s := range b {
		if a[p] != s {
			out = append(out, filepath.Base(p))
		}
	}
	for p := range a {
		if _, ok := b[p]; !ok {
			out = append(out, filepath.Base(p))
		}
	}
	return out
}

func (w *watcher) broadcast(msg string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for ch := range w.subs {
		select {
		case ch <- msg:
		default:
		}
	}
}

func (w *watcher) serveEvents(rw http.ResponseWriter, r *http.Request) {
	flusher, ok := rw.(http.Flusher)
	if !ok {
		http.Error(rw, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	rw.Header().Set("Content-Type", "text/event-stream")
	rw.Header().Set("Cache-Control", "no-store")
	ch := make(chan string, 4)
	w.mu.Lock()
	w.subs[ch] = struct{}{}
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		delete(w.subs, ch)
		w.mu.Unlock()
	}()

	_, _ = fmt.Fprint(rw, ": connected\n\n")
	flusher.Flush()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case msg := <-ch:
			_, _ = fmt.Fprintf(rw, "event: change\ndata: %s\n\n", msg)
		case <-ping.C:
			_, _ = fmt.Fprint(rw, ": ping\n\n")
		}
		flusher.Flush()
	}
}
