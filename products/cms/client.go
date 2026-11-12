package cms

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	cmsapi "github.com/reearth/reearth-cms-api/go"
	"github.com/reearth/reearth-cms-api/go/cmsoapi"

	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/core"
)

// globalOptions holds the flags every cms command shares.
type globalOptions struct {
	project, workspace string
}

// projectOr returns the project from -p, REEARTH_CMS_PROJECT or
// .reearth.yaml, or "" when none is set.
func (o *globalOptions) projectOr(f *core.Factory) string {
	return f.ProjectValue(Product{}, "project", o.project)
}

// requireProject is projectOr for commands that cannot run without one.
func (o *globalOptions) requireProject(f *core.Factory) (string, error) {
	if p := o.projectOr(f); p != "" {
		return p, nil
	}
	return "", cmdutil.NoInputError("a project", "-p or set REEARTH_CMS_PROJECT")
}

// workspaceOr returns the workspace from -w, REEARTH_CMS_WORKSPACE or
// .reearth.yaml, or "" when none is set.
func (o *globalOptions) workspaceOr(f *core.Factory) string {
	return f.ProjectValue(Product{}, "workspace", o.workspace)
}

// requireWorkspace is workspaceOr for commands that cannot run without one.
func (o *globalOptions) requireWorkspace(f *core.Factory) (string, error) {
	if ws := o.workspaceOr(f); ws != "" {
		return ws, nil
	}
	return "", cmdutil.NoInputError("a workspace", "-w or set REEARTH_CMS_WORKSPACE")
}

// checkItemModel requires the model of an item given by ID when a workspace
// is set: the workspace-scoped API reaches items only through their model.
func (o *globalOptions) checkItemModel(f *core.Factory, model string) error {
	if model == "" && o.workspaceOr(f) != "" {
		return cmdutil.FlagErrorf("-m is required with a workspace: the item's model ID or key")
	}
	return nil
}

// client returns a CMS SDK client that calls the API through the
// authenticated HTTP client. The SDK is given no token: it sets an empty
// bearer header, which the authenticated client replaces.
func (o *globalOptions) client(ctx context.Context, f *core.Factory) (*cmsapi.CMS, error) {
	hc, err := f.HTTPClient(ctx, Product{})
	if err != nil {
		return nil, err
	}
	base, err := f.BaseURL(Product{})
	if err != nil {
		return nil, err
	}
	c, err := cmsapi.New(base, "")
	if err != nil {
		return nil, err
	}
	c = c.WithHTTPClient(hc)
	// With a workspace, the SDK calls the workspace-scoped API, whose paths
	// name the project even for an item or asset given by ID.
	if ws := o.workspaceOr(f); ws != "" {
		project, err := o.requireProject(f)
		if err != nil {
			return nil, err
		}
		c = c.WithWorkspace(ws).WithProject(project)
	}
	return c, nil
}

// api is the client generated from the integration API's OpenAPI document,
// with the workspace and, when the command needs it, the project to call it
// with. Commands the hand-written SDK client does not cover use it.
type api struct {
	*cmsoapi.ClientWithResponses
	ws, project string
}

// api returns the generated client. It calls the workspace-scoped API, so it
// needs a workspace, and a project when withProject is set.
func (o *globalOptions) api(ctx context.Context, f *core.Factory, withProject bool) (*api, error) {
	ws, err := o.requireWorkspace(f)
	if err != nil {
		return nil, err
	}
	var project string
	if withProject {
		if project, err = o.requireProject(f); err != nil {
			return nil, err
		}
	}
	hc, err := f.HTTPClient(ctx, Product{})
	if err != nil {
		return nil, err
	}
	base, err := f.BaseURL(Product{})
	if err != nil {
		return nil, err
	}
	// The document's paths start at the workspace; the API serves them
	// under /api, as the hand-written client does.
	c, err := cmsoapi.NewClientWithResponses(strings.TrimRight(base, "/")+"/api", cmsoapi.WithHTTPClient(hc))
	if err != nil {
		return nil, err
	}
	return &api{ClientWithResponses: c, ws: ws, project: project}, nil
}

// checkStatus turns a failed response of the generated client into the
// CLI's errors, by its status code and body.
func checkStatus(code int, body []byte, what string) error {
	if code >= 200 && code < 300 {
		return nil
	}
	msg := strings.Join(strings.Fields(string(body)), " ")
	if len(msg) > 200 {
		msg = msg[:200] + "…"
	}
	if msg == "" {
		msg = http.StatusText(code)
	}
	return statusError(code, fmt.Errorf("code=%d, %s", code, msg), what)
}

// unexpectedResponse reports a successful response the generated client
// could not read.
func unexpectedResponse(what string) error {
	return cmdutil.NewError(cmdutil.ExitError, "cms.unexpected_response",
		fmt.Sprintf("%s: the CMS API returned a response that is not the expected JSON", what), "")
}

// storageClient is for asset files on storage hosts: signed upload URLs and
// asset URLs. It carries no credentials, so the CMS token never leaves the
// API, and no overall timeout, so large files are not cut off.
func storageClient(f *core.Factory) *http.Client {
	hc := *f.PublicHTTPClient()
	hc.Timeout = 0
	return &hc
}

// reStatus finds the status code in an SDK error. The SDK reports a status
// other than 404 only in its message ("failed to request: code=401, ...").
var reStatus = regexp.MustCompile(`code=(\d{3})`)

// apiError gives SDK errors the CLI's error codes and exit codes.
func apiError(err error, what string) error {
	if errors.Is(err, cmsapi.ErrNotFound) {
		return statusError(http.StatusNotFound, err, what)
	}
	var code int
	if m := reStatus.FindStringSubmatch(err.Error()); m != nil {
		code, _ = strconv.Atoi(m[1])
	}
	return statusError(code, err, what)
}

// statusError gives a failed request, by its HTTP status (0 when there was
// none), the CLI's error codes and exit codes.
func statusError(code int, err error, what string) error {
	switch code {
	case http.StatusNotFound:
		return cmdutil.NotFoundf("%s not found", what)
	case http.StatusUnauthorized, http.StatusForbidden:
		return &cmdutil.Error{Exit: cmdutil.ExitAuth, Code: "auth.rejected", Err: err,
			Message: fmt.Sprintf("the CMS API rejected the token for %s", what),
			Hint:    "check the integration token: `reearth login --with-token --product cms` or REEARTH_CMS_TOKEN"}
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return &cmdutil.Error{Exit: cmdutil.ExitUsage, Code: "cms.invalid_input", Err: err,
			Message: fmt.Sprintf("the CMS API refused the request for %s: %v", what, err),
			Hint:    "check the field keys, types and values against the model's schema"}
	}
	return &cmdutil.Error{Exit: cmdutil.ExitError, Code: "cms.request_failed", Err: err,
		Message: fmt.Sprintf("%s: %v", what, err)}
}

// checkSafeMode refuses action when safe mode is on.
func checkSafeMode(f *core.Factory, action string) error {
	v := strings.ToLower(strings.TrimSpace(f.ProjectValue(Product{}, "safe_mode", "")))
	switch v {
	case "", "0", "false", "no", "off":
		return nil
	case "1", "true", "yes", "on":
		return cmdutil.NewError(cmdutil.ExitError, "cms.safe_mode",
			action+" is disabled in safe mode",
			"safe mode is on through REEARTH_CMS_SAFE_MODE or cms.safe_mode in .reearth.yaml")
	}
	return cmdutil.NewError(cmdutil.ExitUsage, "cms.safe_mode_invalid",
		fmt.Sprintf("safe mode must be true or false, not %q", v),
		"fix REEARTH_CMS_SAFE_MODE or cms.safe_mode in .reearth.yaml")
}
