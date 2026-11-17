package cms

import (
	"context"

	cmsapi "github.com/reearth/reearth-cms-api/go"
	"github.com/spf13/cobra"

	"github.com/reearth/cli/sdk/core"
)

func newCmdComment(f *core.Factory, o *globalOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "comment",
		Short: "Comment on items and assets",
	}
	item := newCmdCommentOn(f, o, "item", func(ctx context.Context, c *cmsapi.CMS, model, id, content string) error {
		return c.CommentToItemByModel(ctx, model, id, content)
	})
	asset := newCmdCommentOn(f, o, "asset", func(ctx context.Context, c *cmsapi.CMS, _, id, content string) error {
		return c.CommentToAsset(ctx, id, content)
	})
	cmd.AddCommand(item, asset)
	return cmd
}

// newCmdCommentOn makes the command that comments on an item or an asset.
// An item also takes its model (-m), as the API reaches it through the model.
func newCmdCommentOn(f *core.Factory, o *globalOptions, target string, send func(ctx context.Context, c *cmsapi.CMS, model, id, content string) error) *cobra.Command {
	var content, model string
	example := "  $ reearth cms comment " + target + " 01j... "
	if target == "item" {
		example += "-m parks "
	}
	cmd := &cobra.Command{
		Use:     target + " <" + target + "-id>",
		Short:   "Add a comment to an " + target,
		Example: example + `-c "Checked"`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			c, err := o.client(cmd.Context(), f)
			if err != nil {
				return err
			}
			f.IO.StartProgress("Adding the comment…")
			err = send(cmd.Context(), c, model, id, content)
			f.IO.StopProgress()
			if err != nil {
				return apiError(err, target+" "+id)
			}
			p, err := f.Printer()
			if err != nil {
				return err
			}
			return p.Print(struct {
				Target  string `json:"target"`
				ID      string `json:"id"`
				Content string `json:"content"`
			}{target, id, content}, func() error {
				f.IO.Success("Commented on %s %s", target, id)
				return nil
			})
		},
	}
	cmd.Flags().StringVarP(&content, "content", "c", "", "Comment text (required)")
	_ = cmd.MarkFlagRequired("content")
	if target == "item" {
		addModelFlag(cmd, &model)
	}
	return cmd
}
