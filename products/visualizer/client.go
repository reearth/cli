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

type project struct {
	ID                string     `json:"id"`
	WorkspaceID       string     `json:"workspaceId"`
	SceneID           string     `json:"sceneId"`
	Name              string     `json:"name"`
	Description       string     `json:"description"`
	Alias             string     `json:"alias"`
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

type projectList struct {
	Items      []project `json:"items"`
	TotalCount int       `json:"totalCount"`
	NextCursor string    `json:"nextCursor,omitempty"`
}

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
