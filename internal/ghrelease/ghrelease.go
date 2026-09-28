// Package ghrelease reads GitHub releases and downloads their assets with
// checksum verification. It is shared by self-update and extensions.
package ghrelease

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

var ErrNotFound = errors.New("release not found")

type Release struct {
	TagName    string  `json:"tag_name"`
	HTMLURL    string  `json:"html_url"`
	Prerelease bool    `json:"prerelease"`
	Assets     []Asset `json:"assets"`
}

type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

func (r *Release) Asset(name string) (*Asset, bool) {
	for i := range r.Assets {
		if r.Assets[i].Name == name {
			return &r.Assets[i], true
		}
	}
	return nil, false
}

type Client struct {
	HTTP      *http.Client
	APIBase   string
	UserAgent string
	// Token is an optional GitHub token (GH_TOKEN / GITHUB_TOKEN) to raise rate limits.
	Token string
}

func New(userAgent string) *Client {
	tok := os.Getenv("GH_TOKEN")
	if tok == "" {
		tok = os.Getenv("GITHUB_TOKEN")
	}
	return &Client{
		HTTP:      &http.Client{Timeout: 5 * time.Minute},
		APIBase:   "https://api.github.com",
		UserAgent: userAgent,
		Token:     tok,
	}
}

// Latest returns the latest non-prerelease release.
func (c *Client) Latest(ctx context.Context, repo string) (*Release, error) {
	return c.get(ctx, fmt.Sprintf("/repos/%s/releases/latest", repo))
}

// Tag returns the release for a tag.
func (c *Client) Tag(ctx context.Context, repo, tag string) (*Release, error) {
	return c.get(ctx, fmt.Sprintf("/repos/%s/releases/tags/%s", repo, tag))
}

func (c *Client) get(ctx context.Context, path string) (*Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.APIBase+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", c.UserAgent)
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API %s: %s", path, resp.Status)
	}
	var r Release
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	return &r, nil
}

// Download streams an asset into w and returns its SHA-256 (hex).
// Tokens are never sent to download hosts.
func (c *Client) Download(ctx context.Context, a *Asset, w io.Writer) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: %s", a.Name, resp.Status)
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(w, h), resp.Body); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Checksums downloads and parses a checksums file ("<sha256>  <name>" lines).
func (c *Client) Checksums(ctx context.Context, a *Asset) (map[string]string, error) {
	var buf bytes.Buffer
	if _, err := c.Download(ctx, a, &buf); err != nil {
		return nil, err
	}
	return ParseChecksums(buf.Bytes()), nil
}

func ParseChecksums(b []byte) map[string]string {
	m := map[string]string{}
	s := bufio.NewScanner(bytes.NewReader(b))
	for s.Scan() {
		fields := strings.Fields(s.Text())
		if len(fields) == 2 {
			m[strings.TrimPrefix(fields[1], "*")] = strings.ToLower(fields[0])
		}
	}
	return m
}

// FileSHA256 hashes a file on disk.
func FileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
