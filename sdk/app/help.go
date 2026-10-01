package app

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/reearth/cli/sdk/core"
)

const usageTemplate = `{{agentNote .}}{{heading "Usage"}}{{if .Runnable}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} <command> [flags]{{end}}{{if gt (len .Aliases) 0}}

{{heading "Aliases"}}
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

{{heading "Examples"}}
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}{{$cmds := .Commands}}{{if eq (len .Groups) 0}}

{{heading "Commands"}}{{range $cmds}}{{if .IsAvailableCommand}}
  {{rpad .Name .NamePadding}}  {{dim .Short}}{{end}}{{end}}{{else}}{{range $group := .Groups}}{{if hasGroup $cmds $group.ID}}

{{heading $group.Title}}{{range $cmds}}{{if (and (eq .GroupID $group.ID) .IsAvailableCommand)}}
  {{rpad .Name .NamePadding}}  {{dim .Short}}{{end}}{{end}}{{end}}{{end}}{{if not .AllChildCommandsHaveGroup}}

{{heading "Additional Commands"}}{{range $cmds}}{{if (and (eq .GroupID "") .IsAvailableCommand)}}
  {{rpad .Name .NamePadding}}  {{dim .Short}}{{end}}{{end}}{{end}}{{end}}{{end}}{{if .HasHelpSubCommands}}

{{heading "Help topics"}}{{range .Commands}}{{if .IsAdditionalHelpTopicCommand}}
  {{rpad .Name .NamePadding}}  {{dim .Short}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

{{heading "Flags"}}
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

{{heading "Global Flags"}}
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableSubCommands}}

{{dim (printf "Run '%s <command> --help' for more information about a command." .CommandPath)}}{{end}}{{if .HasHelpSubCommands}}
{{dim (printf "Run '%s help <topic>' to read a help topic." .Root.Name)}}{{end}}
`

func setHelp(root *cobra.Command, f *core.Factory) {
	// Read the color state when help is rendered, after --no-color is applied.
	cobra.AddTemplateFunc("heading", func(s string) string { return f.IO.Color().Bold(s) })
	cobra.AddTemplateFunc("dim", func(s string) string { return f.IO.Color().Dim(s) })
	cobra.AddTemplateFunc("hasGroup", func(cmds []*cobra.Command, id string) bool {
		for _, c := range cmds {
			if c.GroupID == id && c.IsAvailableCommand() {
				return true
			}
		}
		return false
	})
	cobra.AddTemplateFunc("agentNote", func(c *cobra.Command) string {
		return agentNote(f, c)
	})
	root.SetUsageTemplate(usageTemplate)
}

// agentNote steers coding agents to `search` on the help of command groups,
// where reading --help level by level would cost one call per level. Leaf
// commands get no note: their help is the last step of a search.
func agentNote(f *core.Factory, c *cobra.Command) string {
	if f.IO.Agent == "" || !c.HasAvailableSubCommands() {
		return ""
	}
	return fmt.Sprintf("Agents: find a command with `%s search \"<task>\"` instead of reading --help level by level.\n\n", c.Root().Name())
}
