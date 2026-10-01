package corecmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/config"
	"github.com/reearth/cli/sdk/core"
	"github.com/reearth/cli/sdk/docs"
	"github.com/reearth/cli/sdk/output"
)

// docsMaxAge is how long a downloaded copy of the docs is used before the
// site is asked for changes. Agents search several times in a row.
const docsMaxAge = time.Hour

func NewCmdDocs(f *core.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "docs",
		Short: "Search and read the Re:Earth documentation",
		Long: `Search and read the documentation at docs.reearth.io: how to use Visualizer,
CMS and Flow, the concepts behind them, and the developer guides for plugins
and APIs.

The documentation is in Japanese; search in Japanese for the best results.
It is downloaded once and cached, and checked for changes at most once an hour.
When the site cannot be reached, the cached copy is used.

To find a command of this CLI, use "search" instead.`,
	}
	cmd.AddCommand(newCmdDocsSearch(f), newCmdDocsRead(f))
	return cmd
}

func newCmdDocsSearch(f *core.Factory) *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Find documentation pages about a topic",
		Example: `  $ reearth docs search "参照フィールド"
  $ reearth docs search "プラグイン API" --json`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pages, err := loadDocs(cmd, f)
			if err != nil {
				return err
			}
			matches := docs.Search(pages, strings.Join(args, " "), limit)
			p, err := f.Printer()
			if err != nil {
				return err
			}
			return p.Print(matches, func() error {
				cs := f.IO.Color()
				t := p.Table("id", "title", "summary")
				for _, m := range matches {
					t.Row(output.Cell{Text: m.ID, Style: cs.Dim}, output.Cell{Text: m.Title, Style: cs.Bold}, m.Summary)
				}
				if err := t.Render(); err != nil {
					return err
				}
				if len(matches) == 0 {
					f.IO.Hint("No page matched. The docs are in Japanese; try a Japanese query")
				} else if p.IsHuman() {
					f.IO.Newline()
					f.IO.Hint("Read one with `%s docs read <id>`", f.AppName)
				}
				return nil
			})
		},
	}
	cmd.Flags().IntVarP(&limit, "limit", "L", 5, "Maximum number of pages")
	return cmd
}

func newCmdDocsRead(f *core.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "read <id>",
		Short: "Print a documentation page as Markdown",
		Long: `Print a documentation page as Markdown.

<id> is an id from "docs search", a page title, or a page URL. Links to other
pages are absolute, so they can be followed.`,
		Example: `  $ reearth docs read "参照フィールドでモデルどうしをつなぐ"`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pages, err := loadDocs(cmd, f)
			if err != nil {
				return err
			}
			page, ok := docs.Find(pages, args[0])
			if !ok {
				return &cmdutil.Error{
					Exit:    cmdutil.ExitNotFound,
					Code:    "docs.not_found",
					Message: fmt.Sprintf("no documentation page %q", args[0]),
					Hint:    fmt.Sprintf("find its id with `%s docs search <query>`", f.AppName),
				}
			}
			content := page.Markdown(docsSite(f))
			p, err := f.Printer()
			if err != nil {
				return err
			}
			if p.IsMachine() {
				return p.Print(struct {
					docs.Page
					Content string `json:"content"`
				}{page, content}, nil)
			}
			_, err = fmt.Fprint(f.IO.Out, content)
			return err
		},
	}
}

func docsSite(f *core.Factory) string {
	if u := os.Getenv("REEARTH_DOCS_URL"); u != "" {
		return u
	}
	return docs.DefaultSite
}

func loadDocs(cmd *cobra.Command, f *core.Factory) ([]docs.Page, error) {
	src := &docs.Source{
		Site:   docsSite(f),
		Dir:    filepath.Join(config.CacheDir(), "docs"),
		Client: f.PublicHTTPClient(),
		MaxAge: docsMaxAge,
	}
	f.IO.StartProgress("Loading the documentation…")
	pages, stale, err := src.Load(cmd.Context())
	f.IO.StopProgress()
	if err != nil {
		return nil, &cmdutil.Error{
			Exit:    cmdutil.ExitError,
			Code:    "docs.unavailable",
			Message: "could not download the documentation: " + err.Error(),
			Hint:    "check the network, or set REEARTH_DOCS_URL",
			Err:     err,
		}
	}
	if stale {
		f.IO.Warn("Could not reach %s; using the cached documentation", src.Site)
	}
	return pages, nil
}
