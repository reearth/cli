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

// productSkill is a skill a product ships, with the product it comes from.
type productSkill struct {
	core.Skill
	product string
}

// dir is the folder the skill is installed into, which matches the name in
// its frontmatter.
func (s productSkill) dir() string { return "reearth-" + s.Name }

// productSkills returns the skills of the registered products, by name.
func productSkills(f *core.Factory) []productSkill {
	var all []productSkill
	for _, p := range f.Products {
		if sp, ok := p.(core.SkillProduct); ok {
			for _, s := range sp.Skills() {
				all = append(all, productSkill{s, p.Name()})
			}
		}
	}
	slices.SortFunc(all, func(a, b productSkill) int { return strings.Compare(a.Name, b.Name) })
	return all
}

func skillNames(all []productSkill) []string {
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
		Short: "Install an agent skill (SKILL.md)",
		Long: `Write a SKILL.md for coding agents.

Without a name, install the skill for using this CLI: how to find commands
and which rules to follow. It holds no command reference: agents find
commands with "search" and read their flags with --help, which always match
this version. It also updates the product skills already installed in the
same place, so run it after upgrading.

With names, install product skills, such as the one for building Visualizer
plugins. "skills list" shows them.

Targets:
  claude   ~/.claude/skills/<name>/SKILL.md  (./.claude/... with --project)
  agents   ~/.agents/skills/<name>/SKILL.md  (./.agents/... with --project)

Use --dir to write to any other skills directory.`,
		Example: `  $ reearth skills install
  $ reearth skills install visualizer-plugin
  $ reearth skills install --agent claude --agent agents --project
  $ reearth skills install --print > SKILL.md`,
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			return skillNames(productSkills(f)), cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			all := productSkills(f)
			var chosen []productSkill
			for _, name := range args {
				i := slices.IndexFunc(all, func(s productSkill) bool { return s.Name == name })
				if i < 0 {
					known := "none"
					if len(all) > 0 {
						known = strings.Join(skillNames(all), ", ")
					}
					return cmdutil.FlagErrorf("unknown skill %q (available: %s)", name, known)
				}
				if !slices.ContainsFunc(chosen, func(s productSkill) bool { return s.Name == name }) {
					chosen = append(chosen, all[i])
				}
			}

			var content string
			if len(args) == 0 {
				var err error
				if content, err = skills.SkillMD(f.AppName); err != nil {
					return err
				}
			}
			if print {
				switch {
				case len(chosen) > 1:
					return cmdutil.FlagErrorf("--print takes at most one skill name")
				case len(chosen) == 1:
					content = chosen[0].Content
				}
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
				write := chosen
				if len(args) == 0 {
					path := filepath.Join(root, f.AppName, "SKILL.md")
					if err := writeSkill(f, path, content); err != nil {
						return err
					}
					written = append(written, path)
					// Update the product skills installed here, and only those.
					write = nil
					for _, s := range all {
						if _, err := os.Stat(filepath.Join(root, s.dir(), "SKILL.md")); err == nil {
							write = append(write, s)
						}
					}
				}
				for _, s := range write {
					path := filepath.Join(root, s.dir(), "SKILL.md")
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
		Short: "List the product skills you can install",
		Long: `List the product skills that "skills install <name>" can install. The skill
for using this CLI is not listed: "skills install" without a name installs it.`,
		Example: `  $ reearth skills list`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := f.Printer()
			if err != nil {
				return err
			}
			views := []skillJSON{}
			for _, s := range productSkills(f) {
				views = append(views, skillJSON{s.Name, s.product, s.Short})
			}
			return p.Print(views, func() error {
				if len(views) == 0 {
					f.IO.Info("No product skills in this build.")
					return nil
				}
				t := p.Table("name", "product", "description")
				for _, v := range views {
					t.Row(v.Name, v.Product, v.Description)
				}
				return t.Render()
			})
		},
	}
}
