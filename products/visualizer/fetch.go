package visualizer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/reearth/cli/sdk/build"
	"github.com/reearth/cli/sdk/config"
	"github.com/reearth/cli/sdk/core"
	"github.com/reearth/cli/sdk/envvar"
	"github.com/reearth/cli/sdk/httpx"
)

// Source of the ts template. It can be overridden (for mirrors, pinned refs
// and tests) with an environment variable.
const (
	defaultTSTemplate = "https://codeload.github.com/reearth-plugins/reearth-visualizer-plugin-shadcn-template/tar.gz/refs/heads/main"

	envTSTemplate = "REEARTH_VISUALIZER_TS_TEMPLATE_URL"

	cacheTTL = 24 * time.Hour

	maxDownloadSize = 64 << 20
)

func tsTemplateURL() string { return envOr(envTSTemplate, defaultTSTemplate) }

func envOr(k, def string) string {
	if v := envvar.Get(k); v != "" {
		return v
	}
	return def
}

// fetcher downloads public files (no credentials) and caches them under
// <cache dir>/visualizer for cacheTTL.
type fetcher struct {
	http    *http.Client
	dir     string
	refresh bool
}

func newFetcher(f *core.Factory, refresh bool) *fetcher {
	o := httpx.Options{UserAgent: build.UserAgent(f.AppName)}
	if f.Flags != nil && f.Flags.Debug {
		o.Debug = f.IO.ErrOut
	}
	return &fetcher{
		http:    httpx.NewClient(o),
		dir:     filepath.Join(config.CacheDir(), "visualizer"),
		refresh: refresh,
	}
}

func (x *fetcher) cachePath(url string) string {
	sum := sha256.Sum256([]byte(url))
	return filepath.Join(x.dir, hex.EncodeToString(sum[:16]))
}

// get returns the body of url, and whether it came from the cache.
func (x *fetcher) get(ctx context.Context, url string) (body []byte, fromCache bool, err error) {
	cached := x.cachePath(url)
	if !x.refresh {
		if st, err := os.Stat(cached); err == nil && time.Since(st.ModTime()) < cacheTTL {
			if b, err := os.ReadFile(cached); err == nil {
				return b, true, nil
			}
		}
	}
	b, err := x.download(ctx, url)
	if err != nil {
		return nil, false, err
	}
	x.store(cached, b)
	return b, false, nil
}

// drop removes the cached copy of url, for a body that turned out unusable.
func (x *fetcher) drop(url string) { _ = os.Remove(x.cachePath(url)) }

func (x *fetcher) download(ctx context.Context, url string) ([]byte, error) {

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := x.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxDownloadSize+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxDownloadSize {
		return nil, fmt.Errorf("GET %s: larger than %d MB", url, maxDownloadSize>>20)
	}
	return b, nil
}

// store writes the cache through a temporary file, so a concurrent reader
// never sees a half-written copy. The cache is best-effort.
func (x *fetcher) store(path string, b []byte) {
	if err := os.MkdirAll(x.dir, 0o755); err != nil {
		return
	}
	tmp, err := os.CreateTemp(x.dir, ".tmp-*")
	if err != nil {
		return
	}
	_, werr := tmp.Write(b)
	if cerr := tmp.Close(); werr != nil || cerr != nil || os.Rename(tmp.Name(), path) != nil {
		_ = os.Remove(tmp.Name())
	}
}
