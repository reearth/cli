package visualizer

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/core"
	"github.com/reearth/cli/sdk/output"
)

const projectUsage = "Project ID (default: REEARTH_VISUALIZER_PROJECT, then .reearth.yaml)"

// maxPluginSize is the API's upload limit.
const maxPluginSize = 10 << 20

func projectFromFlag(f *core.Factory, flagValue string) (string, error) {
	if flagValue == "" {
		return resolveProject(f, nil)
	}
	return resolveProject(f, []string{flagValue})
}

func pluginsPath(projectID string) string {
	return "/projects/" + url.PathEscape(projectID) + "/plugins"
}

func newCmdPluginPush(f *core.Factory) *cobra.Command {
	var projectID, fromURL string
	cmd := &cobra.Command{
		Use:   "push [dir]",
		Short: "Upload a plugin and install it in a project",
		Long: `Validate and pack the plugin in [dir] (default: the current directory),
upload it and install it in a project. With --url, the server downloads the
plugin zip from that URL instead.

Pushing the same plugin ID and version again replaces it. A new version
upgrades the installed one: the project switches to it and the old version
is removed.`,
		Example: `  $ reearth vis plugin push --project 01hq...
  $ reearth vis plugin push ./my-widget --project 01hq...
  $ reearth vis plugin push --url https://example.com/my-widget.zip --project 01hq...`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if fromURL != "" && len(args) > 0 {
				return cmdutil.FlagErrorf("pass either [dir] or --url, not both")
			}
			pid, err := projectFromFlag(f, projectID)
			if err != nil {
				return err
			}

			var (
				body        *bytes.Reader
				contentType string
				label       string
			)
			if fromURL != "" {
				b := fmt.Appendf(nil, `{"url":%q}`, fromURL)
				body, contentType, label = bytes.NewReader(b), "application/json", fromURL
			} else {
				dir := dirArg(args)
				m, err := loadPlugin(dir)
				if err != nil {
					return err
				}
				var buf bytes.Buffer
				if _, err := zipPlugin(dir, &buf, ""); err != nil {
					return err
				}
				if buf.Len() > maxPluginSize {
					return cmdutil.FlagErrorf("the plugin zip is %d bytes; the limit is 10 MB", buf.Len())
				}
				body, contentType, label = bytes.NewReader(buf.Bytes()), "application/zip", m.ID+" "+m.Version
			}

			c, err := newClient(cmd.Context(), f)
			if err != nil {
				return err
			}
			f.IO.StartProgress("Uploading " + label + "…")
			var pl plugin
			err = c.do(cmd.Context(), http.MethodPost, pluginsPath(pid), nil, body, contentType, &pl)
			f.IO.StopProgress()
			if err != nil {
				return err
			}
			p, err := f.Printer()
			if err != nil {
				return err
			}
			return p.Print(pl, func() error {
				f.IO.Success("Installed %s %s in project %s", pl.Name, pl.Version, pid)
				_, err := fmt.Fprintln(f.IO.Out, pl.ID)
				return err
			})
		},
	}
	fl := cmd.Flags()
	fl.StringVarP(&projectID, "project", "p", "", projectUsage)
	fl.StringVar(&fromURL, "url", "", "Have the server download the plugin zip from this URL")
	return cmd
}

func newCmdPluginList(f *core.Factory) *cobra.Command {
	var projectID string
	var all bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the plugins installed in a project",
		Long: `List the plugins installed in a project. The built-in "reearth" plugin
is left out unless --all is passed.`,
		Example: `  $ reearth vis plugin list --project 01hq...
  $ reearth vis plugin list --json id,version`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			pid, err := projectFromFlag(f, projectID)
			if err != nil {
				return err
			}
			c, err := newClient(cmd.Context(), f)
			if err != nil {
				return err
			}
			var res pluginList
			if err := c.doJSON(cmd.Context(), http.MethodGet, pluginsPath(pid), nil, nil, &res); err != nil {
				return err
			}
			items := make([]plugin, 0, len(res.Items))
			for _, pl := range res.Items {
				if all || !pl.System {
					items = append(items, pl)
				}
			}
			p, err := f.Printer()
			if err != nil {
				return err
			}
			return p.Print(items, func() error {
				if len(items) == 0 {
					f.IO.Info("No plugins installed")
					return nil
				}
				cs := f.IO.Color()
				t := p.Table("ID", "NAME", "VERSION", "EXTENSIONS")
				for _, pl := range items {
					exts := make([]string, len(pl.Extensions))
					for i, e := range pl.Extensions {
						exts[i] = e.ID
					}
					t.Row(pl.ID, pl.Name, pl.Version, output.Cell{Text: strings.Join(exts, ", "), Style: cs.Dim})
				}
				return t.Render()
			})
		},
	}
	fl := cmd.Flags()
	fl.StringVarP(&projectID, "project", "p", "", projectUsage)
	fl.BoolVar(&all, "all", false, "Include the built-in plugin")
	return cmd
}

func newCmdPluginUninstall(f *core.Factory) *cobra.Command {
	var projectID string
	cmd := &cobra.Command{
		Use:   "uninstall <plugin-id>",
		Short: "Remove a plugin from a project",
		Long: `Remove a plugin from a project. <plugin-id> is the full ID that
"plugin list" prints. Asks for confirmation unless --yes is passed.`,
		Example: `  $ reearth vis plugin uninstall 01hq...~my-widget~1.0.0 --project 01hq...`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pid, err := projectFromFlag(f, projectID)
			if err != nil {
				return err
			}
			if err := f.Confirm(fmt.Sprintf("Uninstall plugin %s from project %s?", args[0], pid)); err != nil {
				return err
			}
			c, err := newClient(cmd.Context(), f)
			if err != nil {
				return err
			}
			if err := c.doJSON(cmd.Context(), http.MethodDelete, pluginsPath(pid)+"/"+url.PathEscape(args[0]), nil, nil, nil); err != nil {
				return err
			}
			p, err := f.Printer()
			if err != nil {
				return err
			}
			return p.Print(map[string]any{"id": args[0], "project": pid, "uninstalled": true}, func() error {
				f.IO.Success("Uninstalled %s", args[0])
				return nil
			})
		},
	}
	cmd.Flags().StringVarP(&projectID, "project", "p", "", projectUsage)
	return cmd
}
