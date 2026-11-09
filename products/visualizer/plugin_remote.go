package visualizer

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/core"
	"github.com/reearth/cli/sdk/output"
)

const projectUsage = "Project ID (default: REEARTH_VISUALIZER_PROJECT, then .reearth.yaml)"

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
	var projectID, dir, fromURL string
	cmd := &cobra.Command{
		Use:   "push",
		Short: "Upload a plugin and install it in a project",
		Long: `Validate and pack the plugin in --dir as ` + "`reearth visualizer plugin pack`" + ` does,
upload it and install it in a project. With --url, the server downloads the
plugin from a GitHub repository instead: https://github.com/<owner>/<repo>,
optionally followed by /tree/<branch>.

Pushing the same plugin ID and version again replaces it. A new version
upgrades the installed one: the project switches to it and the old version
is removed.`,
		Example: `  $ reearth vis plugin push --project 01hq...
  $ reearth vis plugin push --dir my-widget --project 01hq...
  $ reearth vis plugin push --url https://github.com/owner/my-widget --project 01hq...`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if fromURL != "" && cmd.Flags().Changed("dir") {
				return cmdutil.FlagErrorf("pass either --dir or --url, not both")
			}
			if fromURL != "" && !isGitHubRepoURL(fromURL) {
				return cmdutil.FlagErrorf("--url must be a GitHub repository URL, such as https://github.com/owner/repo")
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
				b, v, err := packForPush(f, dir)
				if err != nil {
					return err
				}
				body, contentType, label = bytes.NewReader(b), "application/zip", v.ID+" "+v.Version
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
	fl.StringVar(&dir, "dir", ".", "Plugin directory")
	fl.StringVar(&fromURL, "url", "", "Have the server download the plugin from this GitHub repository URL")
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

func packForPush(f *core.Factory, dir string) ([]byte, *validation, error) {
	v, err := validatePlugin(dir)
	if err != nil {
		return nil, nil, err
	}
	if !v.Valid {
		for _, i := range v.Issues {
			f.IO.Warn("%s: %s", i.Path, i.Message)
		}
		return nil, nil, invalidError(v)
	}
	tmp, err := os.MkdirTemp("", "reearth-plugin-push-*")
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	out := filepath.Join(tmp, v.ID+"-"+v.Version+".zip")
	files, err := packFiles(v, out)
	if err != nil {
		return nil, nil, err
	}
	if size, err := writeZip(out, files, maxPackageSize); errors.Is(err, errTooLarge) {
		return nil, nil, cmdutil.NewError(cmdutil.ExitError, "visualizer.package_too_large",
			fmt.Sprintf("the package would be %.1f MB; Visualizer accepts up to 10 MB", float64(size)/(1<<20)),
			"remove large files from the plugin, or load large data from a URL at run time")
	} else if err != nil {
		return nil, nil, err
	}
	b, err := os.ReadFile(out)
	return b, v, err
}

func isGitHubRepoURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || !strings.EqualFold(u.Host, "github.com") {
		return false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	return len(parts) >= 2 && parts[0] != "" && parts[1] != ""
}
