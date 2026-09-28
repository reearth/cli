package app

import (
	"github.com/spf13/cobra"

	"github.com/reearth/cli/sdk/core"
)

const usageTemplate = `{{heading "Usage"}}{{if .Runnable}}
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
  {{rpad .Name .NamePadding}}  {{dim .Short}}{{end}}{{end}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

{{heading "Flags"}}
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

{{heading "Global Flags"}}
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableSubCommands}}

{{dim (printf "Run '%s <command> --help' for more information about a command." .CommandPath)}}{{end}}
`

func setHelp(root *cobra.Command, f *core.Factory) {
	cs := f.IO.Color()
	cobra.AddTemplateFunc("heading", cs.Bold)
	cobra.AddTemplateFunc("dim", cs.Dim)
	cobra.AddTemplateFunc("hasGroup", func(cmds []*cobra.Command, id string) bool {
		for _, c := range cmds {
			if c.GroupID == id && c.IsAvailableCommand() {
				return true
			}
		}
		return false
	})
	root.SetUsageTemplate(usageTemplate)
}
