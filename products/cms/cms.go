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

func (Product) Name() string { return "cms" }
func (Product) Short() string {
	return "Manage Re:Earth CMS projects, models, items, assets and comments"
}

func (Product) BaseURL(*auth.Env) string { return defaultBaseURL }

func (p Product) Command(f *core.Factory) *cobra.Command {
	o := &globalOptions{}
	cmd := &cobra.Command{
		Use:   "cms",
		Short: p.Short(),
		Long: `Read and write Re:Earth CMS content through the integration API.

A workspace holds projects. A project (an ID or its alias) holds models and
assets. A model (an ID or its key) defines fields, and each of its items
holds a value for every field; a model can also define metadata fields.
Items and assets take comments. For the field types and what they hold,
search the documentation: ` + "`reearth docs search \"CMS フィールド\"`" + `.

Commands run as the account you signed in with (` + "`reearth login`" + `). An
integration token also works: ` + "`reearth login --with-token --product cms`" + `, or
REEARTH_CMS_TOKEN. Every command needs a workspace (an ID or its alias), and
all but project list a project. Pass -w and -p, or set REEARTH_CMS_WORKSPACE
and REEARTH_CMS_PROJECT, or cms.workspace and cms.project in .reearth.yaml.
Without them, the workspace is the account's default.

Safe mode refuses item update, item delete and item publish. Turn it on with
REEARTH_CMS_SAFE_MODE=true or "cms: {safe_mode: true}" in .reearth.yaml, for
example in a directory where agents work.`,
	}
	fl := cmd.PersistentFlags()
	fl.StringVarP(&o.project, "project", "p", "", "Project ID or alias (default: REEARTH_CMS_PROJECT, then .reearth.yaml)")
	fl.StringVarP(&o.workspace, "workspace", "w", "", "Workspace ID or alias (default: REEARTH_CMS_WORKSPACE, .reearth.yaml, then the account's default)")
	cmd.AddCommand(newCmdProject(f, o), newCmdModel(f, o), newCmdItem(f, o), newCmdAsset(f, o), newCmdComment(f, o))
	return cmd
}
