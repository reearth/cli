// Package cms is the Re:Earth CMS product CLI. It works as `reearth cms` and
// as `reearth-cms`. API calls go through the CMS Go SDK
// (github.com/reearth/reearth-cms-api/go) with the SDK's authenticated client.
package cms

import (
	"github.com/spf13/cobra"

	"github.com/reearth/cli/sdk/auth"
	"github.com/reearth/cli/sdk/core"
)

const defaultBaseURL = "https://api.cms.reearth.io"

type Product struct{}

var _ core.APIProduct = Product{}

func (Product) Name() string  { return "cms" }
func (Product) Short() string { return "Manage Re:Earth CMS models, items, assets and comments" }

func (Product) BaseURL(*auth.Env) string { return defaultBaseURL }

func (p Product) Command(f *core.Factory) *cobra.Command {
	o := &globalOptions{}
	cmd := &cobra.Command{
		Use:   "cms",
		Short: p.Short(),
		Long: `Read and write Re:Earth CMS content through the integration API.

A workspace holds projects. A project (an ID or its alias) holds models and
assets. A model (an ID or its key) has a schema of fields, and its items hold
a value for each field: a key, a type such as text, number or asset, and a
value. A model can also have metadata fields, set with the --meta-* flags.
Fields of type group hold the IDs of nested field groups. Comments are
added to items and assets.

Commands need an integration token: run ` + "`reearth login --with-token --product cms`" + `
or set REEARTH_CMS_TOKEN. Most commands also need a project: pass -p, or set
REEARTH_CMS_PROJECT or cms.project in .reearth.yaml. A model given by key
needs the project too.

Safe mode refuses item update and item delete. Turn it on with
REEARTH_CMS_SAFE_MODE=true or "cms: {safe_mode: true}" in .reearth.yaml, for
example in a directory where agents work.`,
	}
	fl := cmd.PersistentFlags()
	fl.StringVarP(&o.project, "project", "p", "", "Project ID or alias (default: REEARTH_CMS_PROJECT, then .reearth.yaml)")
	fl.StringVarP(&o.workspace, "workspace", "w", "", "Workspace ID (default: REEARTH_CMS_WORKSPACE, then .reearth.yaml)")
	cmd.AddCommand(newCmdModel(f, o), newCmdItem(f, o), newCmdAsset(f, o), newCmdComment(f, o))
	return cmd
}
