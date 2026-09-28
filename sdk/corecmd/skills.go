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
	"github.com/reearth/cli/sdk/output"
	"github.com/reearth/cli/sdk/skills"
)

func NewCmdSkills(f *core.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills [doc]",
		Short: "Read docs for coding agents, or install the agent skill",
		Long: `Read the docs written for coding agents. They are embedded in this binary,
so they always match the installed version.

"skills install" writes a thin SKILL.md that tells agents to read these docs.`,
		Example: `  $ reearth skills list
  $ reearth skills overview
  $ reearth skills install`,
		Args: cobra.MaximumNArgs(1),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
			return completeDocs(f, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				return showDoc(f, args[0])
			}
			return listDocs(f)
		},
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:     "list",
			Aliases: []string{"ls"},
			Short:   "List docs",
			Args:    cobra.NoArgs,
			RunE:    func(cmd *cobra.Command, args []string) error { return listDocs(f) },
		},
		&cobra.Command{
			Use:   "show <doc>",
			Short: "Print a doc as markdown",
			Args:  cobra.ExactArgs(1),
			ValidArgsFunction: func(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
				return completeDocs(f, args)
			},
			RunE: func(cmd *cobra.Command, args []string) error { return showDoc(f, args[0]) },
		},
		newCmdSkillsInstall(f),
	)
	return cmd
}

func completeDocs(f *core.Factory, args []string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var names []string
	for _, d := range f.Skills.List() {
		names = append(names, d.Name+"\t"+d.Summary)
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}

func listDocs(f *core.Factory) error {
	p, err := f.Printer()
	if err != nil {
		return err
	}
	docs := f.Skills.List()
	return p.Print(docs, func() error {
		cs := f.IO.Color()
		t := p.Table("doc", "product", "summary")
		for _, d := range docs {
			t.Row(output.Cell{Text: d.Name, Style: cs.Bold}, output.Cell{Text: orDash(d.Product), Style: cs.Dim}, d.Summary)
		}
		if err := t.Render(); err != nil {
			return err
		}
		if p.IsHuman() {
			f.IO.Newline()
			f.IO.Hint("Read one with `%s skills <doc>`", f.AppName)
		}
		return nil
	})
}

func showDoc(f *core.Factory, name string) error {
	d, ok := f.Skills.Get(name)
	if !ok {
		var names []string
		for _, d := range f.Skills.List() {
			names = append(names, d.Name)
		}
		return &cmdutil.Error{
			Exit:    cmdutil.ExitNotFound,
			Code:    "not_found",
			Message: fmt.Sprintf("no doc named %q", name),
			Hint:    "available: " + strings.Join(names, ", "),
		}
	}
	content := f.Skills.Render(d)
	p, err := f.Printer()
	if err != nil {
		return err
	}
	if p.IsMachine() {
		return p.Print(struct {
			*skills.Doc
			Content string `json:"content"`
		}{d, content}, nil)
	}
	_, err = fmt.Fprint(f.IO.Out, content)
	return err
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
		Long: `Write a thin SKILL.md that points agents to "skills <doc>".

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
