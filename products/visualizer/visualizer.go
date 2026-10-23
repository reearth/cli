// Package visualizer is the Re:Earth Visualizer product CLI. It works as
// `reearth visualizer` (alias `reearth vis`) and as `reearth-visualizer`.
package visualizer

import (
	"embed"

	"github.com/spf13/cobra"

	"github.com/reearth/cli/sdk/auth"
	"github.com/reearth/cli/sdk/core"
)

//go:embed templates
var templates embed.FS

type Product struct{}

var _ core.APIProduct = Product{}

func (Product) Name() string  { return "visualizer" }
func (Product) Short() string { return "Develop Re:Earth Visualizer plugins and list projects" }

func (Product) BaseURL(*auth.Env) string { return "" }

func (p Product) Command(f *core.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "visualizer",
		Aliases: []string{"vis"},
		Short:   p.Short(),
		Long: `Develop Re:Earth Visualizer plugins on your machine, without logging in,
and list Visualizer projects.

A plugin has an ID, a version and one or more extensions. Each extension is
a widget (UI on the map), a storyBlock or an infoboxBlock, with its code in
<extension-id>.js and its settings described in reearth.yml. The plugin
commands create, serve, validate and package plugins.

"project list" calls the Visualizer API as the current account. Until the
API's address is built in, set REEARTH_VISUALIZER_BASE_URL to it.

For the plugin API itself (reearth.ui, reearth.extension, ...), search the
documentation: ` + "`reearth docs search \"プラグイン\"`" + `.`,
	}
	cmd.AddCommand(newCmdProject(f), newCmdPlugin(f))
	return cmd
}

func newCmdPlugin(f *core.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugin",
		Short: "Create, run and package Visualizer plugins",
		Long: `Create a Visualizer plugin from a template, add extensions to it, serve it
to a viewer from your machine, and package it for uploading. None of these
need a login.

  init      create a plugin from the js template (reearth.yml and one .js
            per extension) or the official ts template (Vite + React + TS)
  add       add a widget, storyBlock or infoboxBlock extension to a plugin
  dev       serve the plugin locally for a Visualizer viewer, reloading on
            every save
  validate  check reearth.yml against the manifest schema and the files it
            names, as Visualizer does on upload
  pack      validate and zip the plugin for uploading to Visualizer

A plugin is a directory with reearth.yml (ts: public/reearth.yml) and an
<extension-id>.js for each extension (ts: built into dist/). On a terminal,
missing values are asked for; pass them as flags, or --no-input to take the
defaults. Agents and CI are never asked.`,
	}
	cmd.AddCommand(newCmdPluginInit(f), newCmdPluginAdd(f), newCmdPluginDev(f), newCmdPluginValidate(f), newCmdPluginPack(f))
	return cmd
}
