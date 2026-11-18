package corecmd

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/core"
	"github.com/reearth/cli/sdk/skills"
)

func NewCmdSkills(f *core.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "Install agent skills for this CLI and its products",
	}
	cmd.AddCommand(newCmdSkillsInstall(f), newCmdSkillsList(f))
	return cmd
}

var skillTargets = map[string]string{
	"claude": ".claude",
	"agents": ".agents",
}

// skill is a skill that "skills install" can write: the one for using this
// CLI, or one a product ships.
type skill struct {
	core.Skill
	product string // empty for the CLI skill
	dir     string // the folder it is installed into, the name in its frontmatter
}

// availableSkills returns the CLI skill, then the product skills by name.
func availableSkills(f *core.Factory) ([]skill, error) {
	md, err := skills.SkillMD(f.AppName)
	if err != nil {
		return nil, err
	}
	all := []skill{{Skill: core.Skill{Name: f.AppName, Short: "Use the " + f.AppName + " CLI", Content: md}, dir: f.AppName}}
	var ps []skill
	for _, p := range f.Products {
		if sp, ok := p.(core.SkillProduct); ok {
			for _, s := range sp.Skills() {
				ps = append(ps, skill{Skill: s, product: p.Name(), dir: "reearth-" + s.Name})
			}
		}
	}
	slices.SortFunc(ps, func(a, b skill) int { return strings.Compare(a.Name, b.Name) })
	return append(all, ps...), nil
}

func skillNames(all []skill) []string {
	names := make([]string, len(all))
	for i, s := range all {
		names[i] = s.Name
	}
	return names
}

func newCmdSkillsInstall(f *core.Factory) *cobra.Command {
	var (
		agents  []string
		project bool
		dir     string
		print   bool
	)
	cmd := &cobra.Command{
		Use:   "install [name...]",
		Short: "Install agent skills (SKILL.md)",
		Long: `Write SKILL.md files for coding agents. "skills list" shows the skills.

The skill named after this CLI teaches agents to use it: how to find
commands and which rules to follow. It holds no command reference: agents
find commands with "search" and read their flags with --help, which always
match this version. Product skills teach agents to build on a product, such
as writing Visualizer plugins.

Without a name, install the skill for using this CLI and update the product
skills already installed in the same place, so run it after upgrading.

Targets:
  claude   ~/.claude/skills/<name>/SKILL.md  (./.claude/... with --project)
  agents   ~/.agents/skills/<name>/SKILL.md  (./.agents/... with --project)

Use --dir to write to any other skills directory.`,
		Example: `  $ reearth skills install
  $ reearth skills install visualizer-plugin
  $ reearth skills install reearth visualizer-plugin
  $ reearth skills install --agent claude --agent agents --project
  $ reearth skills install --print > SKILL.md`,
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			all, err := availableSkills(f)
			if err != nil {
				return nil, cobra.ShellCompDirectiveError
			}
			return skillNames(all), cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			all, err := availableSkills(f)
			if err != nil {
				return err
			}
			var chosen []skill
			for _, name := range args {
				i := slices.IndexFunc(all, func(s skill) bool { return s.Name == name })
				if i < 0 {
					return cmdutil.FlagErrorf("unknown skill %q (available: %s)", name, strings.Join(skillNames(all), ", "))
				}
				if !slices.ContainsFunc(chosen, func(s skill) bool { return s.Name == name }) {
					chosen = append(chosen, all[i])
				}
			}
			if len(args) == 0 {
				chosen = all[:1] // the CLI skill
			}

			if print {
				if len(chosen) > 1 {
					return cmdutil.FlagErrorf("--print takes at most one skill name")
				}
				_, err := fmt.Fprint(f.IO.Out, chosen[0].Content)
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
				write := chosen
				if len(args) == 0 {
					// Also update the product skills installed here, and only those.
					write = slices.Clone(chosen)
					for _, s := range all[1:] {
						if _, err := os.Stat(filepath.Join(root, s.dir, "SKILL.md")); err == nil {
							write = append(write, s)
						}
					}
				}
				for _, s := range write {
					path := filepath.Join(root, s.dir, "SKILL.md")
					if err := writeSkill(f, path, s.Content); err != nil {
						return err
					}
					written = append(written, path)
				}
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

// writeSkill writes a SKILL.md unless it already has this content.
func writeSkill(f *core.Factory, path, content string) error {
	old, err := os.ReadFile(path)
	if err == nil && bytes.Equal(old, []byte(content)) {
		f.IO.Success("Up to date: %s", path)
		return nil
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
	return nil
}

type skillJSON struct {
	Name        string `json:"name"`
	Product     string `json:"product"`
	Description string `json:"description"`
}

func newCmdSkillsList(f *core.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the agent skills you can install",
		Long: `List the skills that "skills install <name>" can install: the one for using
this CLI, which has no product, and the product skills.`,
		Example: `  $ reearth skills list`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			all, err := availableSkills(f)
			if err != nil {
				return err
			}
			p, err := f.Printer()
			if err != nil {
				return err
			}
			views := make([]skillJSON, len(all))
			for i, s := range all {
				views[i] = skillJSON{s.Name, s.product, s.Short}
			}
			return p.Print(views, func() error {
				t := p.Table("name", "product", "description")
				for _, v := range views {
					product := v.Product
					if p.IsHuman() {
						product = orDash(product)
					}
					t.Row(v.Name, product, v.Description)
				}
				return t.Render()
			})
		},
	}
}
