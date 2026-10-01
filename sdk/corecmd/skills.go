package corecmd

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/spf13/cobra"

	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/core"
	"github.com/reearth/cli/sdk/skills"
)

func NewCmdSkills(f *core.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "Install the skill that teaches coding agents to use this CLI",
	}
	cmd.AddCommand(newCmdSkillsInstall(f))
	return cmd
}

var skillTargets = map[string]string{
	"claude": ".claude",
	"agents": ".agents",
}

func newCmdSkillsInstall(f *core.Factory) *cobra.Command {
	var (
		agents  []string
		project bool
		dir     string
		print   bool
	)
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install the agent skill (SKILL.md)",
		Long: `Write a SKILL.md that tells coding agents how to find commands and which
rules to follow. It holds no command reference: agents find commands with
"search" and read their flags with --help, which always match this version.

Targets:
  claude   ~/.claude/skills/<name>/SKILL.md  (./.claude/... with --project)
  agents   ~/.agents/skills/<name>/SKILL.md  (./.agents/... with --project)

Use --dir to write to any other skills directory.`,
		Example: `  $ reearth skills install
  $ reearth skills install --agent claude --agent agents --project
  $ reearth skills install --print > SKILL.md`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			content, err := skills.SkillMD(f.AppName)
			if err != nil {
				return err
			}
			if print {
				_, err := fmt.Fprint(f.IO.Out, content)
				return err
			}

			var roots []string
			if dir != "" {
				roots = append(roots, dir)
			} else {
				base, err := os.UserHomeDir()
				if err != nil {
					return err
				}
				if project {
					if base, err = os.Getwd(); err != nil {
						return err
					}
				}
				for _, a := range agents {
					sub, ok := skillTargets[a]
					if !ok {
						return cmdutil.FlagErrorf("unknown agent %q (known: claude, agents)", a)
					}
					roots = append(roots, filepath.Join(base, sub, "skills"))
				}
			}
			slices.Sort(roots)
			roots = slices.Compact(roots)

			var written []string
			for _, root := range roots {
				path := filepath.Join(root, f.AppName, "SKILL.md")
				old, err := os.ReadFile(path)
				if err == nil && bytes.Equal(old, []byte(content)) {
					f.IO.Success("Up to date: %s", path)
					written = append(written, path)
					continue
				}
				if err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					return err
				}
				if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
					return err
				}
				f.IO.Success("Installed %s", path)
				written = append(written, path)
			}
			p, err := f.Printer()
			if err != nil {
				return err
			}
			return p.Print(map[string]any{"paths": written}, func() error { return nil })
		},
	}
	fl := cmd.Flags()
	fl.StringArrayVar(&agents, "agent", []string{"claude"}, "Target agent: claude or agents (repeatable)")
	fl.BoolVar(&project, "project", false, "Install into the current directory instead of your home directory")
	fl.StringVar(&dir, "dir", "", "Install into this skills directory")
	fl.BoolVar(&print, "print", false, "Print SKILL.md to stdout instead of writing it")
	return cmd
}
