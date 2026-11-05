package cms

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/reearth/cli/sdk/core"
)

func newCmdComment(f *core.Factory, o *globalOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "comment",
		Short: "Comment on items and assets",
	}
	cmd.AddCommand(
		newCmdCommentOn(f, o, "item", func(ctx context.Context, c commenter, id, content string) error {
			return c.CommentToItem(ctx, id, content)
		}),
		newCmdCommentOn(f, o, "asset", func(ctx context.Context, c commenter, id, content string) error {
			return c.CommentToAsset(ctx, id, content)
		}),
	)
	return cmd
}

type commenter interface {
	CommentToItem(ctx context.Context, itemID, content string) error
	CommentToAsset(ctx context.Context, assetID, content string) error
}

func newCmdCommentOn(f *core.Factory, o *globalOptions, target string, send func(context.Context, commenter, string, string) error) *cobra.Command {
	var content string
	cmd := &cobra.Command{
		Use:     target + " <" + target + "-id>",
		Short:   "Add a comment to an " + target,
		Example: "  $ reearth cms comment " + target + ` 01j... -c "Checked"`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			c, err := o.client(cmd.Context(), f)
			if err != nil {
				return err
			}
			f.IO.StartProgress("Adding the comment…")
			err = send(cmd.Context(), c, id, content)
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
	return cmd
}
