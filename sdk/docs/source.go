package docs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultSite is the documentation site. REEARTH_DOCS_URL overrides it.
const DefaultSite = "https://docs.reearth.io"

// Source downloads llms-full.txt and keeps a copy in a directory under Dir
// that is named after Site, so that switching sites never mixes their copies.
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

// ErrNoPages means that the site served an llms-full.txt with no pages in it,
// such as an empty body or an HTML error page.
var ErrNoPages = errors.New("llms-full.txt contains no pages")

// StatusError is a response other than 200 or 304.
type StatusError struct {
	URL    string
	Status string
}

func (e *StatusError) Error() string { return fmt.Sprintf("GET %s: %s", e.URL, e.Status) }

// StaleError is the warning Load returns when it falls back to the cached
// copy because the site could not be used.
type StaleError struct{ Err error }

func (e *StaleError) Error() string { return e.Err.Error() }
func (e *StaleError) Unwrap() error { return e.Err }

// CacheError is the warning Load returns when it could not save a fresh copy.
// The fresh pages are returned anyway.
type CacheError struct{ Err error }

func (e *CacheError) Error() string { return e.Err.Error() }
func (e *CacheError) Unwrap() error { return e.Err }

// Load returns at least one page, or an error. A fresh copy is used as is.
// An older one is revalidated with a conditional request; when the site cannot
// be reached, answers with an error status or serves no pages, the copy is
// used anyway and warn is a *StaleError. When a fresh download cannot be
// saved, warn is a *CacheError.
func (s *Source) Load(ctx context.Context) (pages []Page, warn, err error) {
	dir := filepath.Join(s.Dir, siteKey(s.Site))
	textPath, metaPath := filepath.Join(dir, "llms-full.txt"), filepath.Join(dir, "llms-full.json")
	var meta cacheMeta
	var cached []Page
	cachedText, readErr := os.ReadFile(textPath)
	if readErr == nil {
		cached = Parse(string(cachedText))
	}
	if len(cached) > 0 {
		if b, err := os.ReadFile(metaPath); err == nil {
			_ = json.Unmarshal(b, &meta)
		}
		if s.now().Sub(meta.CheckedAt) < s.MaxAge {
			return cached, nil, nil
		}
	}

	body, notModified, err := s.fetch(ctx, len(cached) > 0, &meta)
	var fresh []Page
	if err == nil && !notModified {
		if fresh = Parse(string(body)); len(fresh) == 0 {
			err = ErrNoPages
		}
	}
	if err != nil {
		if len(cached) > 0 {
			return cached, &StaleError{err}, nil
		}
		return nil, nil, err
	}
	meta.CheckedAt = s.now()
	if notModified {
		fresh = cached
	} else if err := writeFile(textPath, body); err != nil {
		// Leave the metadata alone: it must keep describing the old copy.
		return fresh, &CacheError{err}, nil
	}
	if b, err := json.Marshal(meta); err == nil {
		_ = writeFile(metaPath, b)
	}
	return fresh, nil, nil
}

// siteKey names the cache directory of a site: its host for people reading
// the cache directory, and a hash of the whole URL to keep sites apart.
func siteKey(site string) string {
	site = strings.TrimRight(site, "/")
	host := site
	if u, err := url.Parse(site); err == nil && u.Host != "" {
		host = u.Host
	}
	host = strings.Map(func(r rune) rune {
		if r == '.' || r == '-' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			return r
		}
		return '_'
	}, host)
	sum := sha256.Sum256([]byte(site))
	return host + "-" + hex.EncodeToString(sum[:4])
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
		return nil, false, &StatusError{URL: u, Status: resp.Status}
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
