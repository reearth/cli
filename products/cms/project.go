package cms

import (
	"github.com/reearth/reearth-cms-api/go/cmsoapi"
	"github.com/spf13/cobra"

	"github.com/reearth/cli/sdk/core"
	"github.com/reearth/cli/sdk/output"
)

func newCmdProject(f *core.Factory, o *globalOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "List projects",
		Long: `List the projects of a workspace (-w), to find the project to pass to -p.
A project is given by its ID or its alias.`,
	}
	cmd.AddCommand(newCmdProjectList(f, o))
	return cmd
}

func newCmdProjectList(f *core.Factory, o *globalOptions) *cobra.Command {
	var lf listFlags
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the projects of a workspace",
		Example: `  $ reearth cms project list -w my-workspace
  $ reearth cms project list --keyword park --json=id,alias,name`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := lf.check(); err != nil {
				return err
			}
			c, err := o.api(cmd.Context(), f, false)
			if err != nil {
				return err
			}
			f.IO.StartProgress("Fetching projects…")
			res, err := c.ProjectFilterWithResponse(cmd.Context(), c.ws, &cmsoapi.ProjectFilterParams{
				Page:    &lf.page,
				PerPage: &lf.perPage,
				Keyword: optional(lf.keyword),
				Sort:    optional(cmsoapi.SortParam(lf.sort)),
				Dir:     optional(cmsoapi.SortDirParam(lf.dir)),
			})
			f.IO.StopProgress()
			what := "workspace " + c.ws
			if err != nil {
				return apiError(err, what)
			}
			if err := checkStatus(res.StatusCode(), res.Body, what); err != nil {
				return err
			}
			if res.JSON200 == nil {
				return unexpectedResponse(what)
			}
			projects := deref(res.JSON200.Projects)

			p, err := f.Printer()
			if err != nil {
				return err
			}
			return p.Print(projects, func() error {
				if len(projects) == 0 {
					f.IO.Info("No projects found")
					return nil
				}
				cs := f.IO.Color()
				t := p.Table("ID", "ALIAS", "NAME", "UPDATED")
				for _, pr := range projects {
					t.Row(pr.Id, pr.Alias, pr.Name, output.Cell{Text: p.Time(pr.UpdatedAt), Style: cs.Dim})
				}
				if err := t.Render(); err != nil {
					return err
				}
				pageHint(f, p.IsHuman(), hasNext(res.JSON200.Page, res.JSON200.PerPage, res.JSON200.TotalCount),
					lf.page, deref(res.JSON200.TotalCount), "projects")
				return nil
			})
		},
	}
	lf.add(cmd)
	return cmd
}
