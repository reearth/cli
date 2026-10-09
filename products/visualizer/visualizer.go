// Package visualizer is the Re:Earth Visualizer product: `reearth visualizer`
// (alias `vis`). It talks to the Visualizer REST API (/api/v1), which runs as
// its own service next to the main Visualizer server.
package visualizer

import (
	"github.com/spf13/cobra"

	"github.com/reearth/cli/sdk/auth"
	"github.com/reearth/cli/sdk/core"
)

type Product struct{}

var _ core.APIProduct = Product{}

func (Product) Name() string  { return "visualizer" }
func (Product) Short() string { return "Manage Re:Earth Visualizer projects and plugins" }

// BaseURL is empty until the API's prod and staging URLs are known. Until
// then, set REEARTH_VISUALIZER_BASE_URL (for example to a local server).
func (Product) BaseURL(*auth.Env) string { return "" }

func (p Product) Command(f *core.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "visualizer",
		Aliases: []string{"vis"},
		Short:   p.Short(),
		Long: `Manage Re:Earth Visualizer projects and plugins.

A workspace holds projects. Each project has one scene, where layers, widgets
and installed plugins live. "project" and "plugin push/list/uninstall" call
the Visualizer API as the current account; "plugin init/validate/pack" work
locally.

The workspace comes from --workspace, REEARTH_VISUALIZER_WORKSPACE,
.reearth.yaml, then the account's default. The project comes from the
argument or --project, REEARTH_VISUALIZER_PROJECT, then visualizer.project in
.reearth.yaml. For anything else, call the API directly with
"reearth api visualizer <path>".`,
	}
	cmd.AddCommand(newCmdProject(f), newCmdStory(f), newCmdPlugin(f))
	return cmd
}
