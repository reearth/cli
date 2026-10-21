package visualizer

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
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

// viewerHTMLPage is the HTML page served by the viewer (workbench) build.
const viewerHTMLPage = "workbench.html"

// defaultViewerURL is where the viewer dev server serves the workbench.
// dev opens it when it is running and no viewer is given.
const defaultViewerURL = "http://localhost:3000/" + viewerHTMLPage

// viewerTitle identifies the viewer in a page response.
const viewerTitle = "<title>Re:Earth Visualizer Workbench</title>"

// viewerCacheDir is where downloaded viewer (workbench) builds are cached.
const viewerCacheDir = "reearth-viz-workbench"

// viewerManifestFile is the manifest file inside a viewer (workbench) build.
const viewerManifestFile = "workbench-manifest.json"

// maxExtractFileSize limits the size of individual files during extraction
// to prevent decompression bombs.
const maxExtractFileSize = 100 << 20 // 100 MB

// viewerManifest describes a downloaded viewer build.
type viewerManifest struct {
	Version          string `json:"version"`
	PluginAPIVersion string `json:"pluginApiVersion"`
	Commit           string `json:"commit"`
}

type devOptions struct {
	Dir       string `json:"dir"`
	Template  string `json:"template"`
	URL       string `json:"url,omitempty"`
	PluginURL string `json:"pluginUrl"`

	port          int
	noOpen        bool
	viewer        bool
	viewerDir     string
	viewerURL     string
	viewerVersion string
}

func newCmdPluginDev(f *core.Factory) *cobra.Command {
	o := &devOptions{}
	cmd := &cobra.Command{
		Use:   "dev",
		Short: "Serve a plugin locally, reloading on every save",
		Long: `Serve the plugin in --dir on a local server and notify viewers when its
files change. Stop it with Ctrl+C.

The plugin is served at <url>/plugin. Open a Visualizer viewer with
?dev-plugin=<url>/plugin to load it.

With --viewer, the viewer is automatically downloaded from GitHub releases
and served locally. The viewer is cached in ~/.reearth-cli/reearth-viz-workbench/
and reused across runs. Use --viewer-version to specify a version (latest,
nightly, or v1.0.0).

With --viewer-dir, a local viewer build is served instead.
With --viewer-url, a viewer already running elsewhere is opened with
?dev-plugin= set. With neither, a viewer running at ` + defaultViewerURL + `
is opened if there is one.

  <url>/plugin/reearth.yml        the manifest
  <url>/plugin/<extension-id>.js  each extension
  <url>/plugin/events             server-sent "change" events

Only reearth.yml and .js files are served, as Visualizer keeps only those
from an uploaded plugin; hidden files and node_modules never are. The server
listens on 127.0.0.1 and answers only requests addressed to it by a
loopback name. With --json, the URLs are printed as JSON once it is ready.

  js plugin     serves reearth.yml and <extension-id>.js from --dir
  ts template   serves dist/; keep ` + "`yarn dev-build`" + ` (or run ` + "`yarn build`" + `) running`,
		Example: `  # with a viewer running at localhost:3000, opens it
  $ reearth visualizer plugin dev
  $ reearth visualizer plugin dev --dir my-plugin --port 8080

  # auto-download and serve the viewer
  $ reearth visualizer plugin dev --viewer
  $ reearth visualizer plugin dev --viewer --viewer-version nightly

  # serve a local viewer build
  $ reearth visualizer plugin dev --viewer-dir ../reearth-visualizer/web/dist-playground
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
	fl.BoolVar(&o.viewer, "viewer", false, "Download and serve the Visualizer viewer")
	fl.StringVar(&o.viewerDir, "viewer-dir", "", "Serve this local viewer build")
	fl.StringVar(&o.viewerURL, "viewer-url", "", "Open the viewer running at this URL")
	fl.StringVar(&o.viewerVersion, "viewer-version", "latest", "Viewer version to download: latest, nightly, or a specific version like v1.0.0")
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

	// Validate mutually exclusive flags
	flagCount := 0
	if o.viewer {
		flagCount++
	}
	if o.viewerDir != "" {
		flagCount++
	}
	if o.viewerURL != "" {
		flagCount++
	}
	if flagCount > 1 {
		return cmdutil.FlagErrorf("--viewer, --viewer-dir and --viewer-url cannot be used together")
	}

	// Resolve the viewer directory
	var viewerPath string
	if o.viewer {
		// Auto-download viewer
		var vManifest *viewerManifest
		viewerPath, vManifest, err = ensureViewer(ctx, f, o.viewerVersion)
		if err != nil {
			return err
		}
		f.IO.Success("Viewer ready (version: %s, plugin API: %s)", vManifest.Version, vManifest.PluginAPIVersion)
	} else if o.viewerDir != "" {
		// Use local viewer directory
		if _, err := os.Stat(filepath.Join(o.viewerDir, viewerHTMLPage)); err != nil {
			return cmdutil.NewError(cmdutil.ExitNotFound, "visualizer.viewer_not_found",
				fmt.Sprintf("%s not found in %s", viewerHTMLPage, o.viewerDir), "build the viewer in reearth-visualizer/web with `yarn build:playground`")
		}
		viewerPath = o.viewerDir
	}

	var page *url.URL
	switch {
	case o.viewerURL != "":
		if page, err = viewerPageURL(o.viewerURL); err != nil {
			return err
		}
	case viewerPath == "":
		page = detectViewer(ctx, defaultViewerURL)
	}

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", o.port))
	if err != nil {
		return cmdutil.NewError(cmdutil.ExitUsage, "visualizer.port_in_use", err.Error(), "pass another --port, or --port 0 for a free one")
	}
	base := fmt.Sprintf("http://%s/", ln.Addr())
	o.PluginURL = base + "plugin"
	if viewerPath != "" {
		o.URL = viewerOpenURL(base, o.PluginURL)
	} else if page != nil {
		q := page.Query()
		q.Set("dev-plugin", o.PluginURL)
		page.RawQuery = q.Encode()
		o.URL = page.String()
	}

	w := newWatcher(manifest, root)
	srv := &http.Server{
		Handler:           devHandler(viewerPath, manifest, root, w),
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
	case viewerPath != "":
		f.IO.Success("Viewer running at %s", f.IO.ErrColor().Bold(o.URL))
		f.IO.Hint("Plugin served at %s (for ?dev-plugin=)", o.PluginURL)
	case o.URL != "":
		f.IO.Success("Plugin served at %s", f.IO.ErrColor().Bold(o.PluginURL))
		f.IO.Hint("Open the viewer at %s", o.URL)
	default:
		f.IO.Success("Plugin served at %s", f.IO.ErrColor().Bold(o.PluginURL))
		f.IO.Hint("Open a viewer with ?dev-plugin=%s to load it", url.QueryEscape(o.PluginURL))
		f.IO.Hint("Or run `reearth visualizer plugin dev --viewer` to auto-download the viewer")
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

// viewerOpenURL constructs the URL to open the viewer with the plugin.
func viewerOpenURL(base, pluginURL string) string {
	return base + viewerHTMLPage + "?" + url.Values{"dev-plugin": {pluginURL}}.Encode()
}

// detectViewer returns pageURL if a viewer answers there, or nil.
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
	if err != nil || resp.StatusCode != http.StatusOK || !strings.Contains(string(b), viewerTitle) {
		return nil
	}
	u, err := url.Parse(pageURL)
	if err != nil {
		return nil
	}
	return u
}

// viewerPageURL parses --viewer-url. A URL that names no .html page gets
// the default viewer page.
func viewerPageURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, cmdutil.FlagErrorf("--viewer-url must be an http or https URL, such as http://localhost:3000")
	}
	if path.Ext(u.Path) != ".html" {
		u.Path = strings.TrimSuffix(u.Path, "/") + "/" + viewerHTMLPage
	}
	return u, nil
}

// devHandler serves the plugin at /plugin/ and, when viewerDir is set, that
// viewer build at /. The plugin routes allow any origin, so a viewer hosted
// elsewhere can load them too; that is why they serve only what a published
// plugin exposes anyway. Every route requires a loopback Host, so a web page
// cannot reach the server through DNS rebinding.
func devHandler(viewerDir, manifest, root string, w *watcher) http.Handler {
	mux := http.NewServeMux()
	if viewerDir != "" {
		mux.HandleFunc("GET /{$}", func(rw http.ResponseWriter, r *http.Request) {
			http.Redirect(rw, r, viewerOpenURL("/", "http://"+r.Host+"/plugin"), http.StatusFound)
		})
		// The viewer reads it if present; the defaults are enough.
		mux.HandleFunc("GET /reearth_config.json", func(rw http.ResponseWriter, r *http.Request) {
			rw.Header().Set("Content-Type", "application/json")
			_, _ = rw.Write([]byte("{}"))
		})
		mux.Handle("GET /", http.FileServer(http.Dir(viewerDir)))
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

// viewerCachePath returns the cache directory for the given viewer version.
func viewerCachePath(version string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	return filepath.Join(home, ".reearth-cli", viewerCacheDir, version)
}

// ensureViewer downloads the viewer if not cached, and returns its path and manifest.
// For latest/nightly versions, it automatically checks if a newer version is available.
func ensureViewer(ctx context.Context, f *core.Factory, version string) (string, *viewerManifest, error) {
	versionDir := viewerCachePath(version)
	manifestPath := filepath.Join(versionDir, viewerManifestFile)

	// Check if already cached
	var cachedManifest *viewerManifest
	if data, err := os.ReadFile(manifestPath); err == nil {
		var manifest viewerManifest
		if json.Unmarshal(data, &manifest) == nil {
			cachedManifest = &manifest
		}
	}

	// For nightly/latest, check if remote has a newer version
	var release *releaseInfo
	if version == "nightly" || version == "latest" {
		f.IO.StartProgress(fmt.Sprintf("Checking for %s updates...", version))
		var err error
		release, err = fetchReleaseInfo(ctx, version)
		f.IO.StopProgress()
		if err != nil && cachedManifest == nil {
			return "", nil, err
		}
		if err == nil && cachedManifest != nil {
			needsUpdate := false
			if version == "nightly" && release.Commit != "" && release.Commit != cachedManifest.Commit {
				f.IO.Info("New nightly available (local: %s, remote: %s)", shortCommit(cachedManifest.Commit), shortCommit(release.Commit))
				needsUpdate = true
			} else if version == "latest" && release.TagName != "" && release.TagName != cachedManifest.Version {
				f.IO.Info("New version available (local: %s, remote: %s)", cachedManifest.Version, release.TagName)
				needsUpdate = true
			}
			if needsUpdate {
				_ = os.RemoveAll(versionDir)
				cachedManifest = nil
			}
		}
	}

	// Use cache if available
	if cachedManifest != nil {
		return versionDir, cachedManifest, nil
	}

	// Fetch release info if not already fetched
	if release == nil {
		f.IO.StartProgress(fmt.Sprintf("Fetching release info for viewer %s from GitHub...", version))
		var err error
		release, err = fetchReleaseInfo(ctx, version)
		f.IO.StopProgress()
		if err != nil {
			return "", nil, err
		}
	}

	f.IO.StartProgress("Downloading and extracting viewer...")

	if err := os.MkdirAll(versionDir, 0755); err != nil {
		f.IO.StopProgress()
		return "", nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	if err := downloadAndExtractViewer(ctx, release.DownloadURL, versionDir); err != nil {
		f.IO.StopProgress()
		// Clean up partial download
		_ = os.RemoveAll(versionDir)
		return "", nil, err
	}

	f.IO.StopProgress()

	// Read manifest
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return "", nil, fmt.Errorf("failed to read viewer manifest: %w", err)
	}

	var manifest viewerManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return "", nil, fmt.Errorf("failed to parse viewer manifest: %w", err)
	}

	return versionDir, &manifest, nil
}

// releaseInfo holds information about a GitHub release.
type releaseInfo struct {
	TagName     string
	Commit      string
	DownloadURL string
}

// fetchReleaseInfo fetches release information from GitHub for the given version.
// It returns the tag name, commit, and download URL for the viewer asset.
func fetchReleaseInfo(ctx context.Context, version string) (*releaseInfo, error) {
	var apiURL string
	switch version {
	case "latest":
		apiURL = "https://api.github.com/repos/reearth/reearth-visualizer/releases/latest"
	case "nightly":
		apiURL = "https://api.github.com/repos/reearth/reearth-visualizer/releases/tags/workbench-nightly"
	default:
		// Specific version - construct URL directly without API call
		tag := version
		if !strings.HasPrefix(tag, "v") {
			tag = "v" + tag
		}
		return &releaseInfo{
			TagName:     tag,
			DownloadURL: fmt.Sprintf("https://github.com/reearth/reearth-visualizer/releases/download/%s/reearth-viz-workbench_%s.tar.gz", tag, tag),
		}, nil
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch release info from GitHub API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("failed to fetch release info: %s (URL: %s, Response: %s)", resp.Status, apiURL, string(bodyBytes))
	}

	var release struct {
		TagName         string `json:"tag_name"`
		TargetCommitish string `json:"target_commitish"`
		Assets          []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("failed to parse release info: %w", err)
	}

	// Find viewer asset - workbench assets are platform-independent
	var downloadURL string
	for _, asset := range release.Assets {
		if !strings.HasSuffix(asset.Name, ".tar.gz") {
			continue
		}
		if !strings.HasPrefix(asset.Name, "reearth-viz-workbench_") {
			continue
		}
		// Skip nightly when looking for latest
		if version == "latest" && strings.Contains(asset.Name, "nightly") {
			continue
		}
		// Skip non-nightly when looking for nightly
		if version == "nightly" && !strings.Contains(asset.Name, "nightly") {
			continue
		}
		downloadURL = asset.BrowserDownloadURL
		break
	}

	if downloadURL == "" {
		return nil, cmdutil.NewError(cmdutil.ExitNotFound, "visualizer.viewer_not_found",
			fmt.Sprintf("viewer asset not found for version %s", version),
			"check the available releases at https://github.com/reearth/reearth-visualizer/releases")
	}

	return &releaseInfo{
		TagName:     release.TagName,
		Commit:      release.TargetCommitish,
		DownloadURL: downloadURL,
	}, nil
}

// shortCommit returns the first 8 characters of a commit hash.
func shortCommit(commit string) string {
	if len(commit) > 8 {
		return commit[:8]
	}
	return commit
}

// downloadAndExtractViewer downloads and extracts the viewer tarball.
func downloadAndExtractViewer(ctx context.Context, downloadURL, destDir string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to download viewer: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("failed to download viewer: %s (Response: %s)", resp.Status, string(bodyBytes))
	}

	// Extract tar.gz
	gzr, err := gzip.NewReader(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to decompress viewer: %w", err)
	}
	defer func() { _ = gzr.Close() }()

	tr := tar.NewReader(gzr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read tar: %w", err)
		}

		// Clean the path to prevent directory traversal
		name := filepath.Clean(header.Name)
		if strings.HasPrefix(name, "..") {
			continue
		}
		target := filepath.Join(destDir, name)

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return err
			}
			// Limit file size to prevent decompression bombs
			if _, err := io.Copy(f, io.LimitReader(tr, maxExtractFileSize)); err != nil {
				_ = f.Close()
				return err
			}
			_ = f.Close()
		}
	}

	return nil
}
