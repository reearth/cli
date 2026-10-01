// Package cmdtree describes the command tree as data. The same description
// backs `search`, the command surface snapshot and the description lint, so
// generated and hand-written commands are treated alike.
package cmdtree

import (
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Command is one visible command.
type Command struct {
	Path  string `json:"path"`
	Usage string `json:"usage"`
	Short string `json:"short"`
	// Long is searched but left out of the surface snapshot: it is prose and
	// may embed machine-specific values such as the config path.
	Long    string   `json:"-"`
	Aliases []string `json:"aliases,omitempty"`
	// Runnable is false for groups that only hold subcommands, and for topics.
	Runnable bool `json:"runnable"`
	// Topic marks a help topic: text read with `<app> help <topic>`.
	Topic bool   `json:"topic,omitempty"`
	Flags []Flag `json:"flags,omitempty"`

	// parents holds the Short and Long of each ancestor below the root, so that
	// a group's description of its concepts leads search to its commands.
	parents []string
}

// Flag is a local flag of a command. Global flags are listed on the root only.
type Flag struct {
	Name      string `json:"name"`
	Shorthand string `json:"shorthand,omitempty"`
	Type      string `json:"type"`
	Default   string `json:"default,omitempty"`
	Usage     string `json:"usage"`
}

// Walk lists the visible commands under root in tree order, root included.
// Hidden commands, their subcommands and cobra's help command are skipped.
func Walk(root *cobra.Command) []Command {
	var cmds []Command
	var walk func(c *cobra.Command, parents []string)
	walk = func(c *cobra.Command, parents []string) {
		if c.Hidden || c.Name() == "help" {
			return
		}
		fs := c.LocalNonPersistentFlags()
		if c == root {
			fs = c.PersistentFlags()
		} else {
			fs.AddFlagSet(c.PersistentFlags())
		}
		usage := c.UseLine()
		topic := c.IsAdditionalHelpTopicCommand()
		if topic {
			usage = root.Name() + " help " + c.Name()
		}
		cmds = append(cmds, Command{
			Path:     c.CommandPath(),
			Usage:    usage,
			Short:    c.Short,
			Long:     c.Long,
			Aliases:  c.Aliases,
			Runnable: c.Runnable(),
			Topic:    topic,
			Flags:    flags(fs),
			parents:  parents,
		})
		if c != root {
			parents = append(parents[:len(parents):len(parents)], c.Short, c.Long)
		}
		for _, sub := range c.Commands() {
			walk(sub, parents)
		}
	}
	walk(root, nil)
	return cmds
}

func flags(fs *pflag.FlagSet) []Flag {
	var out []Flag
	fs.VisitAll(func(f *pflag.Flag) {
		if f.Hidden || f.Name == "help" {
			return
		}
		out = append(out, Flag{
			Name:      f.Name,
			Shorthand: f.Shorthand,
			Type:      f.Value.Type(),
			Default:   defaultValue(f),
			Usage:     f.Usage,
		})
	})
	return out
}

func defaultValue(f *pflag.Flag) string {
	switch d := f.DefValue; {
	case d == "[]", d == "false" && f.Value.Type() == "bool":
		return ""
	default:
		return strings.TrimSpace(d)
	}
}
