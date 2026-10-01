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
	// fromCobra marks cobra's completion command and its subcommands.
	fromCobra bool
}

// Flag is a local flag of a command. Global flags are listed on the root only.
type Flag struct {
	Name      string `json:"name"`
	Shorthand string `json:"shorthand,omitempty"`
	Type      string `json:"type"`
	Default   string `json:"default,omitempty"`
	Usage     string `json:"usage"`
	// fromCobra marks a flag that cobra adds, such as --version.
	fromCobra bool
}

// InitDefaults adds what cobra otherwise adds only when a command runs: the
// help and completion commands and the --help and --version flags of the root.
// Call it before Walk, so that a tree built in a test and the tree of a
// running command are walked alike.
func InitDefaults(root *cobra.Command) {
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	root.InitDefaultHelpFlag()
	root.InitDefaultVersionFlag()
}

// Walk lists the visible commands under root in tree order, root included.
// Hidden commands, their subcommands and cobra's help command are skipped.
// The flags listed on the root include the global flags.
func Walk(root *cobra.Command) []Command {
	var cmds []Command
	var walk func(c *cobra.Command, parents []string, fromCobra bool)
	walk = func(c *cobra.Command, parents []string, fromCobra bool) {
		if c.Hidden || c.Name() == "help" {
			return
		}
		// cobra adds "completion" unless the root has a command of that name.
		fromCobra = fromCobra || c.Parent() == root && c.Name() == "completion"
		fs := c.LocalNonPersistentFlags()
		fs.AddFlagSet(c.PersistentFlags())
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

			fromCobra: fromCobra,
		})
		if c != root {
			parents = append(parents[:len(parents):len(parents)], c.Short, c.Long)
		}
		for _, sub := range c.Commands() {
			walk(sub, parents, fromCobra)
		}
	}
	walk(root, nil, false)
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
			fromCobra: f.Annotations[cobra.FlagSetByCobraAnnotation] != nil,
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
