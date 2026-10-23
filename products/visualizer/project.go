package visualizer

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/core"
	"github.com/reearth/cli/sdk/output"
)

func newCmdProject(f *core.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "List Visualizer projects",
		Long: `List Visualizer projects through the Visualizer API, as the current account.

A project belongs to a workspace and has one scene, where layers, widgets and
installed plugins live.`,
	}
	cmd.AddCommand(newCmdProjectList(f))
	return cmd
}

const workspaceUsage = "Workspace ID (default: REEARTH_VISUALIZER_WORKSPACE, .reearth.yaml, then the account's default)"

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
