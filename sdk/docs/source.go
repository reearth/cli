package docs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultSite is the documentation site. REEARTH_DOCS_URL overrides it.
const DefaultSite = "https://docs.reearth.io"

// Source downloads llms-full.txt and keeps a copy in Dir.
type Source struct {
	Site   string
	Dir    string
	Client *http.Client
	// MaxAge is how long a copy is used without asking the site for changes.
	MaxAge time.Duration
	Now    func() time.Time
}

type cacheMeta struct {
	ETag         string    `json:"etag,omitempty"`
	LastModified string    `json:"last_modified,omitempty"`
	CheckedAt    time.Time `json:"checked_at"`
}

// Load returns the pages. A fresh copy is used as is. An older one is
// revalidated with a conditional request; when the site cannot be reached,
// it is used anyway and stale is true.
func (s *Source) Load(ctx context.Context) (pages []Page, stale bool, err error) {
	textPath, metaPath := filepath.Join(s.Dir, "llms-full.txt"), filepath.Join(s.Dir, "llms-full.json")
	cached, readErr := os.ReadFile(textPath)
	var meta cacheMeta
	if readErr == nil {
		if b, err := os.ReadFile(metaPath); err == nil {
			_ = json.Unmarshal(b, &meta)
		}
		if s.now().Sub(meta.CheckedAt) < s.MaxAge {
			return Parse(string(cached)), false, nil
		}
	}

	body, notModified, err := s.fetch(ctx, readErr == nil, &meta)
	if err != nil {
		if readErr == nil {
			return Parse(string(cached)), true, nil
		}
		return nil, false, err
	}
	meta.CheckedAt = s.now()
	if notModified {
		body = cached
	} else if err := writeFile(textPath, body); err != nil {
		return nil, false, err
	}
	if b, err := json.Marshal(meta); err == nil {
		_ = writeFile(metaPath, b)
	}
	return Parse(string(body)), false, nil
}

func (s *Source) fetch(ctx context.Context, conditional bool, meta *cacheMeta) ([]byte, bool, error) {
	u := strings.TrimRight(s.Site, "/") + "/llms-full.txt"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, false, err
	}
	if conditional {
		if meta.ETag != "" {
			req.Header.Set("If-None-Match", meta.ETag)
		}
		if meta.LastModified != "" {
			req.Header.Set("If-Modified-Since", meta.LastModified)
		}
	}
	resp, err := s.Client.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusNotModified && conditional:
		return nil, true, nil
	case resp.StatusCode != http.StatusOK:
		return nil, false, fmt.Errorf("GET %s: %s", u, resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, false, err
	}
	meta.ETag, meta.LastModified = resp.Header.Get("ETag"), resp.Header.Get("Last-Modified")
	return body, false, nil
}

func (s *Source) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// writeFile replaces path atomically, so that a concurrent reader never sees
// a half-written copy.
func writeFile(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
