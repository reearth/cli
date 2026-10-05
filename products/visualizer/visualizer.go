// Package visualizer provides the Re:Earth Visualizer CLI.
//
// NOTE: This is a demo implementation by the Visualizer team.
// For production use, this should be reviewed and integrated by the SE team.
package visualizer

import (
	"github.com/spf13/cobra"

	"github.com/reearth/cli/sdk/core"
)

type Product struct{}

func (Product) Name() string  { return "vis" }
func (Product) Short() string { return "Re:Earth Visualizer tools (demo)" }

func (p Product) Command(f *core.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "vis",
		Short: p.Short(),
		Long: `Tools for Re:Earth Visualizer plugin development.

This is a demo implementation by the Visualizer team. The plugin development
workflow helps you test plugins locally before deploying them.`,
	}

	// Add plugin command group
	cmd.AddCommand(newPluginCmd(f))

	return cmd
}

func newPluginCmd(f *core.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugin",
		Short: "Plugin development tools",
		Long: `Commands for developing and testing Re:Earth Visualizer plugins.

Plugins extend Re:Earth Visualizer with custom widgets, blocks, and interactions.
Use these commands to develop and test plugins locally before publishing.`,
	}

	cmd.AddCommand(newInitCmd(f))
	cmd.AddCommand(newDevCmd(f))

	return cmd
}
