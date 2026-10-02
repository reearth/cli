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

Commands call the Visualizer API as the current account. Until they are
added, use "reearth api visualizer <path>" to call the API directly, e.g.
"reearth api visualizer /workspaces/<workspace-id>/projects".`,
	}
	cmd.AddCommand(newCmdPlugin(f))
	return cmd
}
