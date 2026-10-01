package corecmd

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/reearth/cli/sdk/cmdtree"
	"github.com/reearth/cli/sdk/core"
	"github.com/reearth/cli/sdk/output"
)

func NewCmdSearch(f *core.Factory) *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "search <task>",
		Short: "Find the command for a task",
		Long: `Search every command by describing what you want to do, in English.

The search runs offline over the installed command tree and returns the best
matches with a one-line summary. Run "<command> --help" on a match for its flags.`,
		Example: `  $ reearth search "greet someone"
  $ reearth search "check whether my login still works" --json`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			matches := cmdtree.Search(cmdtree.Walk(cmd.Root()), strings.Join(args, " "), limit)
			p, err := f.Printer()
			if err != nil {
				return err
			}
			return p.Print(matches, func() error {
				cs := f.IO.Color()
				t := p.Table("command", "summary")
				for _, m := range matches {
					t.Row(output.Cell{Text: m.Command, Style: cs.Bold}, m.Summary)
				}
				if err := t.Render(); err != nil {
					return err
				}
				if len(matches) == 0 {
					f.IO.Hint("No command matched. Describe the action and the resource, such as \"list projects\". To learn about a product, use `%s docs search`", f.AppName)
				}
				return nil
			})
		},
	}
	cmd.Flags().IntVarP(&limit, "limit", "L", 5, "Maximum number of matches")
	return cmd
}
