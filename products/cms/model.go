package cms

import (
	"errors"

	cmsapi "github.com/reearth/reearth-cms-api/go"
	"github.com/spf13/cobra"

	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/core"
)

func newCmdModel(f *core.Factory, o *globalOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "model",
		Short: "List and show models",
	}
	cmd.AddCommand(newCmdModelList(f, o), newCmdModelGet(f, o))
	return cmd
}

func newCmdModelList(f *core.Factory, o *globalOptions) *cobra.Command {
	var page, perPage int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the models of a project",
		Example: `  $ reearth cms model list -p my-project
  $ reearth cms model list --page 2 --per-page 20
  $ reearth cms model list --json id,key,name`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkPage(page, perPage); err != nil {
				return err
			}
			project, err := o.requireProject(f)
			if err != nil {
				return err
			}
			c, err := o.client(cmd.Context(), f)
			if err != nil {
				return err
			}
			f.IO.StartProgress("Fetching models…")
			res, err := c.GetModelsPartially(cmd.Context(), project, page, perPage)
			f.IO.StopProgress()
			if err != nil {
				return apiError(err, "project "+project)
			}

			p, err := f.Printer()
			if err != nil {
				return err
			}
			return p.Print(res.Models, func() error {
				if len(res.Models) == 0 {
					f.IO.Info("No models found")
					return nil
				}
				t := p.Table("ID", "KEY", "NAME", "PUBLIC", "UPDATED")
				for _, m := range res.Models {
					t.Row(m.ID, m.Key, m.Name, m.Public, p.Time(m.UpdatedAt))
				}
				if err := t.Render(); err != nil {
					return err
				}
				pageHint(f, p.IsHuman(), res.HasNext(), page, res.TotalCount, "models")
				return nil
			})
		},
	}
	addPageFlags(cmd, &page, &perPage)
	return cmd
}

func newCmdModelGet(f *core.Factory, o *globalOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "get <model-id-or-key>",
		Short: "Show a model by ID, or by key within a project",
		Example: `  $ reearth cms model get 01j...
  $ reearth cms model get parks -p my-project`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := o.client(cmd.Context(), f)
			if err != nil {
				return err
			}
			f.IO.StartProgress("Fetching the model…")
			m, err := c.GetModel(cmd.Context(), args[0])
			// Not an ID: try it as a key in the project.
			if project := o.projectOr(f); errors.Is(err, cmsapi.ErrNotFound) && project != "" {
				m, err = c.GetModelByKey(cmd.Context(), project, args[0])
			}
			f.IO.StopProgress()
			if err != nil {
				return apiError(err, "model "+args[0])
			}
			p, err := f.Printer()
			if err != nil {
				return err
			}
			return p.Print(m, nil)
		},
	}
}

func addPageFlags(cmd *cobra.Command, page, perPage *int) {
	cmd.Flags().IntVar(page, "page", 1, "Page number")
	cmd.Flags().IntVar(perPage, "per-page", 50, "Results per page")
}

func checkPage(page, perPage int) error {
	if page < 1 || perPage < 1 {
		return cmdutil.FlagErrorf("--page and --per-page must be 1 or more")
	}
	return nil
}

// pageHint tells a person when there are more pages.
func pageHint(f *core.Factory, human, hasNext bool, page, total int, what string) {
	if human && hasNext {
		f.IO.Hint("page %d of %d %s; pass --page %d for the next", page, total, what, page+1)
	}
}
