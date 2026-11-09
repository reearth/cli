package visualizer

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/core"
	"github.com/reearth/cli/sdk/output"
)

func newCmdProject(f *core.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "List, create, edit and delete Visualizer projects",
		Long: `List, create, edit and delete Visualizer projects.

A project belongs to a workspace and has one scene, where layers, widgets and
installed plugins live. Commands that take a project ID read it from the
argument, then REEARTH_VISUALIZER_PROJECT, then visualizer.project in
.reearth.yaml.`,
	}
	cmd.AddCommand(
		newCmdProjectList(f),
		newCmdProjectView(f),
		newCmdProjectCreate(f),
		newCmdProjectEdit(f),
		newCmdProjectDelete(f),
		newCmdProjectRestore(f),
		newCmdProjectPublish(f),
		newCmdProjectUnpublish(f),
		newCmdProjectExport(f),
		newCmdProjectImport(f),
	)
	return cmd
}

const workspaceUsage = "Workspace ID (default: REEARTH_VISUALIZER_WORKSPACE, .reearth.yaml, then the account's default)"

// resolveWorkspace returns the workspace from the flag, REEARTH_VISUALIZER_WORKSPACE,
// visualizer.workspace or workspace in .reearth.yaml, or the account's default.
func resolveWorkspace(f *core.Factory, flagValue string) (string, error) {
	if ws := f.ProjectValue(Product{}, "workspace", flagValue); ws != "" {
		return ws, nil
	}
	if proj := f.OptionalProject(); proj != nil && proj.Workspace != "" {
		return proj.Workspace, nil
	}
	if r, err := f.Account(); err == nil && r.Account != nil && r.Account.Workspace != "" {
		return r.Account.Workspace, nil
	}
	return "", cmdutil.NoInputError("a workspace", "--workspace or set REEARTH_VISUALIZER_WORKSPACE")
}

// resolveProject returns the project ID from args, REEARTH_VISUALIZER_PROJECT
// or visualizer.project in .reearth.yaml.
func resolveProject(f *core.Factory, args []string) (string, error) {
	arg := ""
	if len(args) > 0 {
		arg = args[0]
	}
	if id := f.ProjectValue(Product{}, "project", arg); id != "" {
		return id, nil
	}
	return "", cmdutil.NoInputError("a project ID", "<project-id> or set REEARTH_VISUALIZER_PROJECT")
}

func newCmdProjectList(f *core.Factory) *cobra.Command {
	var (
		workspace, keyword, sort string
		starred, deleted, all    bool
		limit                    int
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the projects of a workspace",
		Long: `List the projects of a workspace, most recently updated first.

Projects in the trash are left out unless --deleted is set. --keyword and
--sort cannot be combined with --starred or --deleted.`,
		Example: `  $ reearth vis project list
  $ reearth vis project list --keyword park --limit 5
  $ reearth vis project list --all --json id,name`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if limit < 1 || limit > 100 {
				return cmdutil.FlagErrorf("--limit must be between 1 and 100")
			}
			if starred && deleted {
				return cmdutil.FlagErrorf("--starred and --deleted cannot be combined")
			}
			if (starred || deleted) && (keyword != "" || sort != "") {
				return cmdutil.FlagErrorf("--keyword and --sort cannot be combined with --starred or --deleted")
			}
			ws, err := resolveWorkspace(f, workspace)
			if err != nil {
				return err
			}
			c, err := newClient(cmd.Context(), f)
			if err != nil {
				return err
			}
			q := url.Values{"limit": {strconv.Itoa(limit)}}
			if keyword != "" {
				q.Set("keyword", keyword)
			}
			if sort != "" {
				q.Set("sort", sort)
			}
			if starred {
				q.Set("starred", "true")
			}
			if deleted {
				q.Set("deleted", "true")
			}

			f.IO.StartProgress("Fetching projects…")
			var res projectList
			for {
				var page projectList
				if err := c.doJSON(cmd.Context(), http.MethodGet, "/workspaces/"+url.PathEscape(ws)+"/projects", q, nil, &page); err != nil {
					f.IO.StopProgress()
					return err
				}
				res.Items = append(res.Items, page.Items...)
				res.TotalCount, res.NextCursor = page.TotalCount, page.NextCursor
				if !all || page.NextCursor == "" {
					break
				}
				q.Set("cursor", page.NextCursor)
			}
			f.IO.StopProgress()

			p, err := f.Printer()
			if err != nil {
				return err
			}
			return p.Print(res.Items, func() error {
				if len(res.Items) == 0 {
					f.IO.Info("No projects found")
					return nil
				}
				cs := f.IO.Color()
				t := p.Table("ID", "NAME", "PUBLISHED", "UPDATED")
				for _, pr := range res.Items {
					// Full IDs: they are what view, edit and delete take.
					t.Row(pr.ID, pr.Name, pr.PublishmentStatus, output.Cell{Text: p.Time(pr.UpdatedAt), Style: cs.Dim})
				}
				if err := t.Render(); err != nil {
					return err
				}
				if p.IsHuman() && len(res.Items) < res.TotalCount {
					f.IO.Hint("showing %d of %d projects; pass --all to list every project", len(res.Items), res.TotalCount)
				}
				return nil
			})
		},
	}
	fl := cmd.Flags()
	fl.StringVarP(&workspace, "workspace", "w", "", workspaceUsage)
	fl.StringVar(&keyword, "keyword", "", "Only projects whose name contains this text")
	fl.StringVar(&sort, "sort", "", "Sort order: updatedAt, -updatedAt, name, -name")
	fl.BoolVar(&starred, "starred", false, "Only starred projects")
	fl.BoolVar(&deleted, "deleted", false, "Only projects in the trash")
	fl.IntVarP(&limit, "limit", "L", 20, "Maximum number of projects per page (1-100)")
	fl.BoolVar(&all, "all", false, "Fetch every page")
	return cmd
}

func newCmdProjectView(f *core.Factory) *cobra.Command {
	var web bool
	cmd := &cobra.Command{
		Use:   "view [project-id]",
		Short: "Show one project",
		Long: `Show one project. With --web, open it in the Visualizer editor instead
and print the editor URL.

--web needs the web app URL from REEARTH_VISUALIZER_WEB_URL or
visualizer.web_url in .reearth.yaml. When a coding agent runs the command,
the URL is printed but no browser is opened.`,
		Example: `  $ reearth vis project view 01hq...
  $ reearth vis project view 01hq... --web
  $ reearth vis project view 01hq... --json=sceneId`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := resolveProject(f, args)
			if err != nil {
				return err
			}
			webURL := ""
			if web {
				if webURL = strings.TrimRight(f.ProjectValue(Product{}, "web_url", ""), "/"); webURL == "" {
					return cmdutil.NewError(cmdutil.ExitUsage, "visualizer.no_web_url",
						"no web app URL for visualizer", "set REEARTH_VISUALIZER_WEB_URL (e.g. http://localhost:3000)")
				}
			}
			c, err := newClient(cmd.Context(), f)
			if err != nil {
				return err
			}
			var pr project
			if err := c.doJSON(cmd.Context(), http.MethodGet, "/projects/"+url.PathEscape(id), nil, nil, &pr); err != nil {
				return err
			}
			if !web {
				return printProject(f, &pr)
			}
			editorURL := webURL + "/scene/" + url.PathEscape(pr.SceneID) + "/map"
			if f.IO.Agent == "" {
				f.IO.Info("Opening %s in your browser", pr.Name)
				if err := f.OpenBrowser(editorURL); err != nil {
					f.IO.Warn("could not open a browser: %v", err)
				}
			}
			_, err = fmt.Fprintln(f.IO.Out, editorURL)
			return err
		},
	}
	cmd.Flags().BoolVar(&web, "web", false, "Open the project in the Visualizer editor")
	return cmd
}

func printProject(f *core.Factory, pr *project) error {
	p, err := f.Printer()
	if err != nil {
		return err
	}
	return p.Print(pr, func() error {
		cs := f.IO.Color()
		t := p.Table()
		row := func(k, v string) {
			if v != "" {
				t.Row(output.Cell{Text: k, Style: cs.Dim}, v)
			}
		}
		row("name", pr.Name)
		row("id", pr.ID)
		// Always shown, so an empty description does not look like a missing field.
		if pr.Description != "" {
			row("description", pr.Description)
		} else {
			t.Row(output.Cell{Text: "description", Style: cs.Dim}, output.Cell{Text: "none", Style: cs.Dim})
		}
		row("alias", pr.Alias)
		row("published alias", pr.PublishedAlias)
		row("workspace", pr.WorkspaceID)
		row("scene", pr.SceneID)
		row("visibility", pr.Visibility)
		row("published", pr.PublishmentStatus)
		if pr.ImportStatus != "" && pr.ImportStatus != "SUCCESS" {
			row("import", pr.ImportStatus)
			row("import error", pr.ImportError)
		}
		if pr.Deleted {
			row("deleted", "in the trash")
		}
		row("updated", p.Time(pr.UpdatedAt))
		return t.Render()
	})
}

func newCmdProjectCreate(f *core.Factory) *cobra.Command {
	var workspace, name, description, alias, visibility string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a project",
		Long: `Create a project with its scene.

--visibility is required: public or private. Private projects need a
workspace on a paid plan; on a free plan the server refuses them.

--alias sets the project alias, a short name for the project that is unique
in the workspace: 5 to 32 letters, digits and '-', starting and ending with a
letter or digit. Without it, the alias is "p-<project-id>". It is not the
published URL; "project publish --alias" sets that.`,
		Example: `  $ reearth vis project create --name "City parks" --visibility public
  $ reearth vis project create --name "City parks" --visibility private --alias city-parks
  $ reearth vis project create --name demo --visibility public --jq .id`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return cmdutil.NoInputError("a project name", "--name")
			}
			if visibility == "" {
				return cmdutil.NoInputError("a visibility", "--visibility public or --visibility private")
			}
			if visibility != "public" && visibility != "private" {
				return cmdutil.FlagErrorf("--visibility must be public or private")
			}
			ws, err := resolveWorkspace(f, workspace)
			if err != nil {
				return err
			}
			c, err := newClient(cmd.Context(), f)
			if err != nil {
				return err
			}
			body := map[string]any{"name": name, "visibility": visibility}
			if description != "" {
				body["description"] = description
			}
			if alias != "" {
				body["alias"] = alias
			}
			var pr project
			if err := c.doJSON(cmd.Context(), http.MethodPost, "/workspaces/"+url.PathEscape(ws)+"/projects", nil, body, &pr); err != nil {
				return err
			}
			f.IO.Success("Created project %s", pr.Name)
			return printProject(f, &pr)
		},
	}
	fl := cmd.Flags()
	fl.StringVarP(&workspace, "workspace", "w", "", workspaceUsage)
	fl.StringVar(&name, "name", "", "Project name")
	fl.StringVar(&description, "description", "", "Project description")
	fl.StringVar(&alias, "alias", "", "Project alias, unique in the workspace (default: p-<project-id>)")
	fl.StringVar(&visibility, "visibility", "", "Project visibility: public or private (private needs a paid plan)")
	return cmd
}

func newCmdProjectEdit(f *core.Factory) *cobra.Command {
	var name, description string
	var starred, archived bool
	cmd := &cobra.Command{
		Use:   "edit [project-id]",
		Short: "Change a project's name or description",
		Long: `Change a project's name, description, starred or archived state.
Only the flags you pass are changed.`,
		Example: `  $ reearth vis project edit 01hq... --name "City parks 2026"
  $ reearth vis project edit 01hq... --starred`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body := map[string]any{}
			fl := cmd.Flags()
			if fl.Changed("name") {
				if name == "" {
					return cmdutil.FlagErrorf("--name cannot be empty")
				}
				body["name"] = name
			}
			if fl.Changed("description") {
				body["description"] = description
			}
			if fl.Changed("starred") {
				body["starred"] = starred
			}
			if fl.Changed("archived") {
				body["archived"] = archived
			}
			if len(body) == 0 {
				return cmdutil.FlagErrorf("nothing to change: pass --name, --description, --starred or --archived")
			}
			id, err := resolveProject(f, args)
			if err != nil {
				return err
			}
			c, err := newClient(cmd.Context(), f)
			if err != nil {
				return err
			}
			var pr project
			if err := c.doJSON(cmd.Context(), http.MethodPatch, "/projects/"+url.PathEscape(id), nil, body, &pr); err != nil {
				return err
			}
			f.IO.Success("Updated project %s", pr.Name)
			return printProject(f, &pr)
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&name, "name", "", "New project name")
	fl.StringVar(&description, "description", "", "New project description")
	fl.BoolVar(&starred, "starred", false, "Star or unstar the project (--starred=false)")
	fl.BoolVar(&archived, "archived", false, "Archive or unarchive the project (--archived=false)")
	return cmd
}

func newCmdProjectDelete(f *core.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "delete [project-id]",
		Short: "Move a project to the trash",
		Long: `Move a project to the trash. It can be restored from the web app until
the trash is emptied. Asks for confirmation unless --yes is passed.`,
		Example: `  $ reearth vis project delete 01hq...
  $ reearth vis project delete 01hq... --yes`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := resolveProject(f, args)
			if err != nil {
				return err
			}
			if err := f.Confirm(fmt.Sprintf("Move project %s to the trash?", id)); err != nil {
				return err
			}
			c, err := newClient(cmd.Context(), f)
			if err != nil {
				return err
			}
			if err := c.doJSON(cmd.Context(), http.MethodDelete, "/projects/"+url.PathEscape(id), nil, nil, nil); err != nil {
				return err
			}
			p, err := f.Printer()
			if err != nil {
				return err
			}
			return p.Print(map[string]any{"id": id, "deleted": true}, func() error {
				f.IO.Success("Moved project %s to the trash", id)
				return nil
			})
		},
	}
}

func newCmdProjectRestore(f *core.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "restore [project-id]",
		Short: "Restore a project from the trash",
		Long: `Move a project out of the trash. Find the IDs of projects in the trash
with "project list --deleted". Restoring a project that is not in the trash
changes nothing.`,
		Example: `  $ reearth vis project list --deleted
  $ reearth vis project restore 01hq...`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := resolveProject(f, args)
			if err != nil {
				return err
			}
			c, err := newClient(cmd.Context(), f)
			if err != nil {
				return err
			}
			var pr project
			if err := c.doJSON(cmd.Context(), http.MethodPost, "/projects/"+url.PathEscape(id)+"/restore", nil, nil, &pr); err != nil {
				return err
			}
			f.IO.Success("Restored project %s", pr.Name)
			return printProject(f, &pr)
		},
	}
}

func newCmdProjectPublish(f *core.Factory) *cobra.Command {
	var alias string
	var index bool
	cmd := &cobra.Command{
		Use:   "publish [project-id]",
		Short: "Publish a project and print its URL",
		Long: `Publish a project, or update its published site with the latest changes,
and print the published URL.

Anyone with the URL can view a published project. By default it is published
as "limited": search engines are asked not to index it, as with the web app's
default. --index publishes it as "public" so that search engines may index it.

--alias sets the published alias, which names the published URL. It is kept
when omitted; a project published for the first time gets "c-<scene-id>".`,
		Example: `  $ reearth vis project publish 01hq...
  $ reearth vis project publish 01hq... --index --alias city-parks`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			status := "limited"
			if index {
				status = "public"
			}
			return publishProject(cmd, f, args, status, alias)
		},
	}
	fl := cmd.Flags()
	fl.BoolVar(&index, "index", false, "Let search engines index the published project")
	fl.StringVar(&alias, "alias", "", "Published alias, which names the published URL (default: keep the current one)")
	return cmd
}

func newCmdProjectUnpublish(f *core.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "unpublish [project-id]",
		Short: "Take a published project offline",
		Long: `Take a published project offline. Its published alias is kept, so
publishing it again brings back the same URL. Asks for confirmation unless
--yes is passed.`,
		Example: `  $ reearth vis project unpublish 01hq...`,
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := resolveProject(f, args)
			if err != nil {
				return err
			}
			if err := f.Confirm(fmt.Sprintf("Unpublish project %s? Its published URL stops working.", id)); err != nil {
				return err
			}
			return publishProject(cmd, f, []string{id}, "private", "")
		},
	}
}

func publishProject(cmd *cobra.Command, f *core.Factory, args []string, status, alias string) error {
	id, err := resolveProject(f, args)
	if err != nil {
		return err
	}
	c, err := newClient(cmd.Context(), f)
	if err != nil {
		return err
	}
	body := map[string]any{"status": status}
	if alias != "" {
		body["alias"] = alias
	}
	if status != "private" {
		f.IO.StartProgress("Publishing…")
	}
	var res publishResult
	err = c.doJSON(cmd.Context(), http.MethodPost, "/projects/"+url.PathEscape(id)+"/publish", nil, body, &res)
	f.IO.StopProgress()
	if err != nil {
		return err
	}
	p, err := f.Printer()
	if err != nil {
		return err
	}
	return p.Print(res, func() error {
		if status == "private" {
			f.IO.Success("Unpublished %s", res.Project.Name)
			return nil
		}
		f.IO.Success("Published %s (%s)", res.Project.Name, status)
		if res.URL == "" {
			f.IO.Warn("the server did not return a URL (no published host configured); the published alias is %s", res.Project.PublishedAlias)
			return nil
		}
		_, err := fmt.Fprintln(f.IO.Out, res.URL)
		return err
	})
}

func newCmdProjectExport(f *core.Factory) *cobra.Command {
	var out string
	var force bool
	cmd := &cobra.Command{
		Use:   "export [project-id]",
		Short: "Download a project as a zip",
		Long: `Download a project as a zip: the same file the web app exports, with the
project, its scene, plugins and assets. It can be imported again.

The zip is written to <project-id>.zip, or to -o; "-o -" writes it to stdout.
An existing file is not replaced unless --force is passed. Large projects
take a while to build on the server.`,
		Example: `  $ reearth vis project export 01hq...
  $ reearth vis project export 01hq... -o backups/city-parks.zip
  $ reearth vis project export 01hq... -o - > city-parks.zip`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := resolveProject(f, args)
			if err != nil {
				return err
			}
			if out == "" {
				out = id + ".zip"
			}
			c, err := newClient(cmd.Context(), f)
			if err != nil {
				return err
			}
			path := "/projects/" + url.PathEscape(id) + "/export"

			if out == "-" {
				f.IO.StartProgress("Exporting…")
				_, err := c.download(cmd.Context(), path, f.IO.Out)
				f.IO.StopProgress()
				return err
			}
			if _, err := os.Stat(out); err == nil && !force {
				return cmdutil.FlagErrorf("%s already exists (pass --force to replace it, or -o another path)", out)
			}
			if dir := filepath.Dir(out); dir != "." {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return err
				}
			}
			// Write to a temporary file first, so that a failed download never
			// leaves a partial zip behind.
			tmp, err := os.CreateTemp(filepath.Dir(out), ".export-*.zip")
			if err != nil {
				return err
			}
			defer func() { _ = os.Remove(tmp.Name()) }()

			f.IO.StartProgress("Exporting…")
			n, err := c.download(cmd.Context(), path, tmp)
			f.IO.StopProgress()
			if cerr := tmp.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
			if err := os.Rename(tmp.Name(), out); err != nil {
				return err
			}

			p, err := f.Printer()
			if err != nil {
				return err
			}
			return p.Print(map[string]any{"path": out, "bytes": n, "project": id}, func() error {
				f.IO.Success("Exported project %s (%s)", id, humanBytes(n))
				_, err := fmt.Fprintln(f.IO.Out, out)
				return err
			})
		},
	}
	fl := cmd.Flags()
	fl.StringVarP(&out, "output", "o", "", "Zip path, or - for stdout (default: <project-id>.zip)")
	fl.BoolVar(&force, "force", false, "Replace an existing file")
	return cmd
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}
