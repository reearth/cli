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
func (Product) Short() string { return "Manage Re:Earth Visualizer projects and develop plugins" }

// baseURLs are the Visualizer REST APIs of the built-in environments. The
// release build's staging environment signs in to the dev Auth0 tenant, so it
// uses the dev API.
var baseURLs = map[string]string{
	auth.EnvProd:    "https://api.visualizer.reearth.io/api/v1",
	auth.EnvStaging: "https://api.visualizer.dev.reearth.io/api/v1",
}

// BaseURL is the API of a built-in environment. Other environments set
// REEARTH_VISUALIZER_BASE_URL or envs.<name>.products.visualizer.base_url,
// which also take precedence over this.
func (Product) BaseURL(env *auth.Env) string {
	if env == nil {
		return ""
	}
	return baseURLs[env.Name]
}

func (p Product) Command(f *core.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "visualizer",
		Aliases: []string{"vis"},
		Short:   p.Short(),
		Long: `Manage Re:Earth Visualizer projects and develop plugins.

A plugin has an ID, a version and one or more extensions. Each extension is
a widget (UI on the map), a storyBlock or an infoboxBlock, with its code in
<extension-id>.js and its settings described in reearth.yml. The plugin
commands create, serve, validate and package plugins.

"project", "story" and "plugin push/list/uninstall" call the Visualizer API
as the current account: https://api.visualizer.reearth.io/api/v1 on the prod
environment and https://api.visualizer.dev.reearth.io/api/v1 on staging. For
other environments, set REEARTH_VISUALIZER_BASE_URL or
envs.<name>.products.visualizer.base_url in the config.

The workspace comes from --workspace, REEARTH_VISUALIZER_WORKSPACE,
.reearth.yaml, then the account's default. The project comes from the
argument or --project, REEARTH_VISUALIZER_PROJECT, then visualizer.project in
.reearth.yaml.

For the plugin API itself (reearth.ui, reearth.extension, ...), search the
documentation: ` + "`reearth docs search \"プラグイン\"`" + `.`,
	}
	cmd.AddCommand(newCmdProject(f), newCmdStory(f), newCmdPlugin(f))
	return cmd
}

func newCmdPlugin(f *core.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugin",
		Short: "Create, run, package and install Visualizer plugins",
		Long: `Create a Visualizer plugin from a template, add extensions to it, serve it
to a viewer from your machine, package it, and install it in a project.
Only push, list and uninstall need a login.

  init      create a plugin from the js template (reearth.yml and one .js
            per extension) or the official ts template (Vite + React + TS)
  add       add a widget, storyBlock or infoboxBlock extension to a plugin
  dev       serve the plugin locally for a Visualizer viewer, reloading on
            every save
  validate  check reearth.yml against the manifest schema and the files it
            names, as Visualizer does on upload
  pack      validate and zip the plugin for uploading to Visualizer
  push      pack the plugin and install it in a project
  list      list the plugins installed in a project
  uninstall remove a plugin from a project

A plugin is a directory with reearth.yml (ts: public/reearth.yml) and an
<extension-id>.js for each extension (ts: built into dist/). On a terminal,
missing values are asked for; pass them as flags, or --no-input to take the
defaults. Agents and CI are never asked.`,
	}
	cmd.AddCommand(newCmdPluginInit(f), newCmdPluginAdd(f), newCmdPluginDev(f), newCmdPluginValidate(f), newCmdPluginPack(f),
		newCmdPluginPush(f), newCmdPluginList(f), newCmdPluginUninstall(f))
	return cmd
}
