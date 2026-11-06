package cms

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	cmsapi "github.com/reearth/reearth-cms-api/go"

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
	if ws := f.ProjectValue(Product{}, "workspace", o.workspace); ws != "" {
		project, err := o.requireProject(f)
		if err != nil {
			return nil, err
		}
		c = c.WithWorkspace(ws).WithProject(project)
	}
	return c, nil
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
		return cmdutil.NotFoundf("%s not found", what)
	}
	var status string
	if m := reStatus.FindStringSubmatch(err.Error()); m != nil {
		status = m[1]
	}
	switch status {
	case "401", "403":
		return &cmdutil.Error{Exit: cmdutil.ExitAuth, Code: "auth.rejected", Err: err,
			Message: fmt.Sprintf("the CMS API rejected the token for %s", what),
			Hint:    "check the integration token: `reearth login --with-token --product cms` or REEARTH_CMS_TOKEN"}
	case "400", "422":
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
