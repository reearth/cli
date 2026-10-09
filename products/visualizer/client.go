package visualizer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/core"
)

// project and plugin mirror the Visualizer API (/api/v1) responses. Their
// JSON field names are what --json prints, so they follow the API's names.
type project struct {
	ID                string     `json:"id"`
	WorkspaceID       string     `json:"workspaceId"`
	SceneID           string     `json:"sceneId"`
	Name              string     `json:"name"`
	Description       string     `json:"description"`
	Alias             string     `json:"alias"`
	PublishedAlias    string     `json:"publishedAlias,omitempty"`
	ImportStatus      string     `json:"importStatus,omitempty"`
	ImportError       string     `json:"importError,omitempty"`
	Visibility        string     `json:"visibility"`
	Starred           bool       `json:"starred"`
	Archived          bool       `json:"archived"`
	Deleted           bool       `json:"deleted"`
	ImageURL          string     `json:"imageUrl,omitempty"`
	PublishmentStatus string     `json:"publishmentStatus"`
	PublishedAt       *time.Time `json:"publishedAt,omitempty"`
	CreatedAt         time.Time  `json:"createdAt"`
	UpdatedAt         time.Time  `json:"updatedAt"`
}

type publishResult struct {
	Project project `json:"project"`
	URL     string  `json:"url,omitempty"`
}

type projectList struct {
	Items      []project `json:"items"`
	TotalCount int       `json:"totalCount"`
	NextCursor string    `json:"nextCursor,omitempty"`
}

type plugin struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Version     string            `json:"version"`
	Description string            `json:"description,omitempty"`
	Author      string            `json:"author,omitempty"`
	System      bool              `json:"system"`
	Extensions  []pluginExtension `json:"extensions"`
}

type pluginExtension struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Name string `json:"name"`
}

type pluginList struct {
	Items []plugin `json:"items"`
}

// client calls the Visualizer API as the current account.
type client struct {
	f    *core.Factory
	http *http.Client
	base string
}

func newClient(ctx context.Context, f *core.Factory) (*client, error) {
	p := Product{}
	base, err := f.BaseURL(p)
	if err != nil {
		return nil, err
	}
	hc, err := f.HTTPClient(ctx, p)
	if err != nil {
		return nil, err
	}
	return &client{f: f, http: hc, base: base}, nil
}

// doJSON sends in (if not nil) as JSON and decodes the response into out (if not nil).
func (c *client) doJSON(ctx context.Context, method, path string, query url.Values, in, out any) error {
	var body io.Reader
	contentType := ""
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body, contentType = bytes.NewReader(b), "application/json"
	}
	return c.do(ctx, method, path, query, body, contentType, out)
}

// download sends a GET request and copies a successful response body to w.
// It returns the number of bytes copied.
func (c *client) download(ctx context.Context, path string, w io.Writer) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return 0, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return 0, c.apiError(resp)
	}
	return io.Copy(w, resp.Body)
}

func (c *client) do(ctx context.Context, method, path string, query url.Values, body io.Reader, contentType string, out any) error {
	u := c.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return c.apiError(resp)
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// apiError turns an API error response into a CLI error with a stable code
// ("visualizer.<api code>") and the matching exit code.
func (c *client) apiError(resp *http.Response) error {
	var e struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if json.Unmarshal(b, &e) != nil || e.Error.Code == "" {
		e.Error.Code, e.Error.Message = "http_error", fmt.Sprintf("the Visualizer API returned %s", resp.Status)
	}
	exit, hint := cmdutil.ExitError, ""
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		exit, hint = cmdutil.ExitAuth, fmt.Sprintf("run `%s login`", c.f.AppName)
	case http.StatusNotFound:
		exit = cmdutil.ExitNotFound
	case http.StatusBadRequest:
		exit = cmdutil.ExitUsage
	}
	return cmdutil.NewError(exit, "visualizer."+e.Error.Code, e.Error.Message, hint)
}
