package corecmd

import (
	"embed"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/reearth/cli/sdk/core"
)

//go:embed topics/*.txt
var topicFiles embed.FS

var topics = []struct{ name, short string }{
	{"formatting", "Output formats, JSON fields and errors on stderr"},
	{"exit-codes", "What each exit status means"},
	{"environment", "Environment variables the CLI reads"},
}

// NewHelpTopics returns commands that only carry text, read with
// `reearth help <topic>`. They are listed under "Help topics" and found by
// `search` like any other command.
func NewHelpTopics(f *core.Factory) []*cobra.Command {
	var cmds []*cobra.Command
	for _, t := range topics {
		b, err := topicFiles.ReadFile("topics/" + t.name + ".txt")
		if err != nil {
			panic(err)
		}
		c := &cobra.Command{
			Use:   t.name,
			Short: t.short,
			Long:  strings.ReplaceAll(string(b), "{{app}}", f.AppName),
		}
		c.SetHelpFunc(func(c *cobra.Command, _ []string) {
			_, _ = fmt.Fprint(f.IO.Out, c.Long)
		})
		cmds = append(cmds, c)
	}
	return cmds
}
