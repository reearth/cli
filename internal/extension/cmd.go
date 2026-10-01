package extension

import (
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/core"
	"github.com/reearth/cli/sdk/output"
)

func NewCmd(f *core.Factory, m *Manager) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "extension",
		Aliases: []string{"ext"},
		Short:   "Install and run extensions",
		Long: `Extensions are extra commands published as GitHub releases in
repositories named <owner>/reearth-<name>. A release must contain a binary
asset named reearth-<name>-<os>-<arch> (with .exe on Windows).

Security model:
  - Only extensions installed here are run. reearth-* binaries on PATH are never run.
  - The SHA-256 of the binary is recorded at install time and checked before every run.
  - Extensions are not sandboxed: they run with your user's access, like any
    program you run. Install only extensions you trust.
  - The CLI does not hand extensions the credentials it stores. They inherit your
    environment variables, including REEARTH_TOKEN. To call an API, they run
    "$REEARTH_BIN api". REEARTH_EXTENSION=1 marks calls coming from an extension.
  - Extensions from owners other than "reearth" are marked third-party.
  - Built-in command names cannot be taken by extensions.
  - When a coding agent is detected, extensions run only through "ext exec".`,
	}
	cmd.AddCommand(newCmdInstall(f, m), newCmdList(f, m), newCmdUpgrade(f, m), newCmdRemove(f, m), newCmdExec(f, m))
	return cmd
}

func confirmPlan(f *core.Factory, p *Plan, verb string) error {
	cs := f.IO.ErrColor()
	f.IO.Println(cs.Bold(p.Repo) + " " + cs.Dim(p.Tag))
	if p.Official {
		f.IO.Println("  " + cs.SuccessIcon() + " official Re:Earth extension")
	} else {
		f.IO.Println("  " + cs.WarningIcon() + " " + cs.Yellow("third-party extension") + cs.Dim(" — it runs with your user's permissions"))
	}
	if p.Checksum != "" {
		f.IO.Println("  " + cs.SuccessIcon() + " checksum published: " + cs.Dim(p.Checksum))
	} else {
		f.IO.Println("  " + cs.WarningIcon() + " the release publishes no checksums file" + cs.Dim(" — the download cannot be verified"))
	}
	f.IO.Println("  " + cs.Dim(p.URL))
	f.IO.Newline()
	return f.Confirm(fmt.Sprintf("%s %s?", verb, p.Name))
}

func newCmdInstall(f *core.Factory, m *Manager) *cobra.Command {
	var pin string
	cmd := &cobra.Command{
		Use:     "install <owner/reearth-name>",
		Short:   "Install an extension from a GitHub release",
		Example: "  $ reearth ext install reearth/reearth-example\n  $ reearth ext install someone/reearth-tool --pin v1.2.0",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f.IO.StartProgress("Resolving release…")
			p, err := m.PlanInstall(cmd.Context(), args[0], pin)
			f.IO.StopProgress()
			if err != nil {
				return err
			}
			if e, ok := m.Get(p.Name); ok && e.Repo != p.Repo {
				return cmdutil.FlagErrorf("an extension named %q is already installed from %s", p.Name, e.Repo)
			}
			if err := confirmPlan(f, p, "Install"); err != nil {
				return err
			}
			f.IO.StartProgress("Downloading…")
			e, err := m.Install(cmd.Context(), p)
			f.IO.StopProgress()
			if err != nil {
				return err
			}
			f.IO.Success("Installed %s %s", f.IO.ErrColor().Bold(e.Name), e.Version)
			f.IO.Hint("Run it with `%s %s`", f.AppName, e.Name)
			return nil
		},
	}
	cmd.Flags().StringVar(&pin, "pin", "", "Install this release tag instead of the latest")
	return cmd
}

func newCmdList(f *core.Factory, m *Manager) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List installed extensions",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			list, err := m.List()
			if err != nil {
				return err
			}
			p, err := f.Printer()
			if err != nil {
				return err
			}
			return p.Print(list, func() error {
				if len(list) == 0 {
					f.IO.Info("No extensions installed")
					return nil
				}
				cs := f.IO.Color()
				t := p.Table("name", "repo", "version", "")
				for _, e := range list {
					trust := "third-party"
					if e.Official {
						trust = "official"
					}
					t.Row(output.Cell{Text: e.Name, Style: cs.Bold}, e.Repo, e.Version, output.Cell{Text: trust, Style: cs.Dim})
				}
				return t.Render()
			})
		},
	}
}

func newCmdUpgrade(f *core.Factory, m *Manager) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "upgrade [name]",
		Short: "Upgrade extensions to their latest release",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if (len(args) == 0) == !all {
				return cmdutil.FlagErrorf("specify an extension name or --all")
			}
			var targets []*Entry
			if all {
				list, err := m.List()
				if err != nil {
					return err
				}
				targets = list
			} else {
				e, ok := m.Get(args[0])
				if !ok {
					return cmdutil.NotFoundf("extension %q is not installed", args[0])
				}
				targets = []*Entry{e}
			}
			for _, e := range targets {
				p, err := m.PlanInstall(cmd.Context(), e.Repo, "")
				if err != nil {
					return fmt.Errorf("%s: %w", e.Name, err)
				}
				if p.Tag == e.Version {
					f.IO.Success("%s is up to date (%s)", e.Name, e.Version)
					continue
				}
				if err := confirmPlan(f, p, "Upgrade"); err != nil {
					return err
				}
				if _, err := m.Install(cmd.Context(), p); err != nil {
					return fmt.Errorf("%s: %w", e.Name, err)
				}
				f.IO.Success("Upgraded %s %s → %s", e.Name, e.Version, p.Tag)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Upgrade every extension")
	return cmd
}

func newCmdRemove(f *core.Factory, m *Manager) *cobra.Command {
	return &cobra.Command{
		Use:     "remove <name>",
		Aliases: []string{"rm"},
		Short:   "Remove an extension",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, ok := m.Get(args[0]); !ok {
				return cmdutil.NotFoundf("extension %q is not installed", args[0])
			}
			if err := f.Confirm(fmt.Sprintf("Remove %s?", args[0])); err != nil {
				return err
			}
			if err := m.Remove(args[0]); err != nil {
				return err
			}
			f.IO.Success("Removed %s", args[0])
			return nil
		},
	}
}

func newCmdExec(f *core.Factory, m *Manager) *cobra.Command {
	return &cobra.Command{
		Use:                "exec <name> [args...]",
		Short:              "Run an installed extension",
		DisableFlagParsing: true,
		Args:               cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if args[0] == "--help" || args[0] == "-h" {
				return cmd.Help()
			}
			e, ok := m.Get(args[0])
			if !ok {
				return cmdutil.NotFoundf("extension %q is not installed", args[0])
			}
			code, err := Run(f, m, e, args[1:])
			if err != nil {
				return err
			}
			if code != 0 {
				return cmdutil.SilentExit(code, "extension.failed")
			}
			return nil
		},
	}
}

// Run executes an extension with stdio attached and returns its exit code.
func Run(f *core.Factory, m *Manager, e *Entry, args []string) (int, error) {
	self, err := os.Executable()
	if err != nil {
		return 0, err
	}
	c, err := m.Command(e, args, self)
	if err != nil {
		return 0, err
	}
	c.Stdin, c.Stdout, c.Stderr = f.IO.In, f.IO.Out, f.IO.ErrOut
	if err := c.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode(), nil
		}
		return 0, err
	}
	return 0, nil
}

// Dispatch runs `reearth <name> ...` for an installed extension when <name>
// is not a built-in command. Agents must use `ext exec` explicitly.
func Dispatch(m *Manager) func(f *core.Factory, root *cobra.Command, args []string) (bool, int) {
	return func(f *core.Factory, root *cobra.Command, args []string) (bool, int) {
		if len(args) == 0 || len(args[0]) == 0 || args[0][0] == '-' || cobraBuiltins[args[0]] {
			return false, 0
		}
		if c, _, err := root.Find(args[:1]); err == nil && c != root {
			return false, 0
		}
		e, ok := m.Get(args[0])
		if !ok {
			return false, 0
		}
		if f.IO.Agent != "" {
			_, _ = fmt.Fprintf(f.IO.ErrOut, "error: %q is an extension; coding agents must run it explicitly with `%s ext exec %s`\n", e.Name, f.AppName, e.Name)
			return true, cmdutil.ExitUsage
		}
		code, err := Run(f, m, e, args[1:])
		if err != nil {
			_, _ = fmt.Fprintf(f.IO.ErrOut, "error: %v\n", err)
			return true, cmdutil.ExitError
		}
		return true, code
	}
}
