// Package app assembles a runnable CLI from products: the unified `reearth`
// binary (Run with several products) or a standalone product binary (Main).
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/cli/browser"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/reearth/cli/sdk/build"
	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/core"
	"github.com/reearth/cli/sdk/corecmd"
	"github.com/reearth/cli/sdk/iostreams"
	"github.com/reearth/cli/sdk/prompt"
)

type (
	Product = core.Product
	Factory = core.Factory
)

type Options struct {
	// Name is the binary name ("reearth", "reearth-cms").
	Name string
	// Short describes the root command.
	Short string
	// Products are mounted as subcommands, or as the root when Standalone.
	Products []core.Product
	// Standalone makes the single product the root command.
	Standalone bool
	// Extra adds host-specific commands (upgrade, extension, ...).
	Extra func(f *core.Factory) []*cobra.Command
	// Dispatch may handle args before cobra (e.g. running an extension).
	Dispatch func(f *core.Factory, root *cobra.Command, args []string) (handled bool, exitCode int)
	// BeforeRun runs before the command; AfterRun after it, even on error.
	BeforeRun func(f *core.Factory, args []string)
	AfterRun  func(f *core.Factory, cmd *cobra.Command, err error)
	// DoctorChecks contributes extra checks to `doctor`.
	DoctorChecks []func(ctx context.Context) []core.Check
}

// Main runs a single product as a standalone CLI and exits.
func Main(p core.Product) {
	os.Exit(Run(Options{Name: "reearth-" + p.Name(), Products: []core.Product{p}, Standalone: true}, os.Args[1:]))
}

// Run executes the CLI with args and returns the exit code.
func Run(o Options, args []string) int {
	f := NewFactory(o, iostreams.System())

	// busybox-style dispatch: invoking the unified binary as "reearth-cms"
	// behaves like "reearth cms".
	if !o.Standalone {
		exe := strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe")
		if name, ok := strings.CutPrefix(exe, o.Name+"-"); ok {
			if _, found := f.FindProduct(name); found {
				args = append([]string{name}, args...)
			}
		}
	}
	return Execute(f, o, args)
}

// Execute runs args against a Factory and returns the exit code.
func Execute(f *core.Factory, o Options, args []string) int {
	root := NewRoot(f, o)
	if o.Dispatch != nil {
		if handled, code := o.Dispatch(f, root, args); handled {
			return code
		}
	}
	if o.BeforeRun != nil {
		o.BeforeRun(f, args)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	root.SetArgs(args)
	root.SetOut(f.IO.Out)
	root.SetErr(f.IO.ErrOut)
	cmd, err := root.ExecuteContextC(ctx)
	f.IO.StopProgress()
	if err != nil {
		err = classify(err)
		reportError(f, cmd, err)
	}
	if o.AfterRun != nil {
		o.AfterRun(f, cmd, err)
	}
	return cmdutil.ExitCode(err)
}

// NewFactory creates the Factory shared by all commands.
func NewFactory(o Options, io *iostreams.IOStreams) *core.Factory {
	f := &core.Factory{
		AppName:      o.Name,
		IO:           io,
		Prompter:     prompt.New(io),
		Flags:        &core.GlobalFlags{},
		Products:     o.Products,
		DoctorChecks: o.DoctorChecks,
	}
	f.OpenBrowser = func(url string) error { return openBrowser(f, url) }
	return f
}

// openBrowser honors the "browser" setting (REEARTH_BROWSER), else the system default.
func openBrowser(f *core.Factory, url string) error {
	if cfg, err := f.Config(); err == nil {
		if v, _, _ := cfg.Get("browser"); v != "" {
			argv := append(strings.Fields(v), url)
			c := exec.Command(argv[0], argv[1:]...)
			c.Stdout, c.Stderr = io.Discard, io.Discard
			return c.Start()
		}
	}
	// Keep stdout clean for data.
	browser.Stdout, browser.Stderr = io.Discard, io.Discard
	return browser.OpenURL(url)
}

const (
	groupProducts = "products"
	groupAccounts = "accounts"
	groupMore     = "more"
)

// NewRoot builds the root command with global flags, core commands and products.
func NewRoot(f *core.Factory, o Options) *cobra.Command {
	cobra.EnableTraverseRunHooks = true
	cobra.EnableCommandSorting = false

	short := o.Short
	if short == "" {
		short = "Work with Re:Earth from the command line"
	}
	root := &cobra.Command{
		Use:           o.Name,
		Short:         short,
		SilenceErrors: true,
		SilenceUsage:  true,
		Version:       build.Version,
		CompletionOptions: cobra.CompletionOptions{
			HiddenDefaultCmd: false,
		},
	}
	root.SetVersionTemplate(corecmd.VersionLine(o.Name, f.IO.Color().Dim) + "\n")
	addGlobalFlags(f, root.PersistentFlags())
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		return applyGlobalFlags(f)
	}
	root.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		return &cmdutil.Error{Exit: cmdutil.ExitUsage, Code: "usage", Message: err.Error()}
	})

	root.AddGroup(
		&cobra.Group{ID: groupProducts, Title: "Products"},
		&cobra.Group{ID: groupAccounts, Title: "Accounts"},
		&cobra.Group{ID: groupMore, Title: "More"},
	)

	if o.Standalone && len(o.Products) == 1 {
		mountStandalone(f, root, o.Products[0])
	} else {
		for _, p := range o.Products {
			pc := p.Command(f)
			pc.Use = p.Name()
			if pc.Short == "" {
				pc.Short = p.Short()
			}
			pc.GroupID = groupProducts
			root.AddCommand(pc)
		}
	}

	add := func(group string, cmds ...*cobra.Command) {
		for _, c := range cmds {
			c.GroupID = group
			root.AddCommand(c)
		}
	}
	add(groupAccounts,
		corecmd.NewCmdLogin(f),
		corecmd.NewCmdLogout(f),
		corecmd.NewCmdUse(f),
		corecmd.NewCmdAccount(f),
		corecmd.NewCmdWhoami(f),
		corecmd.NewCmdAuth(f),
	)
	add(groupMore,
		corecmd.NewCmdSearch(f),
		corecmd.NewCmdDocs(f),
		corecmd.NewCmdAPI(f),
		corecmd.NewCmdConfig(f),
		corecmd.NewCmdSkills(f),
		corecmd.NewCmdDoctor(f),
		corecmd.NewCmdVersion(f),
	)
	if o.Extra != nil {
		add(groupMore, o.Extra(f)...)
	}
	root.AddCommand(corecmd.NewHelpTopics(f)...)
	root.SetHelpCommandGroupID(groupMore)
	root.SetCompletionCommandGroupID(groupMore)

	standalone := ""
	if o.Standalone && len(o.Products) == 1 {
		standalone = o.Products[0].Name()
	}
	finalize(root, o.Name, standalone, f)
	return root
}

func mountStandalone(f *core.Factory, root *cobra.Command, p core.Product) {
	pc := p.Command(f)
	root.Short = pc.Short
	if root.Short == "" {
		root.Short = p.Short()
	}
	root.Long, root.Example = pc.Long, pc.Example
	if pc.RunE != nil || pc.Run != nil {
		root.Args, root.Run, root.RunE = pc.Args, pc.Run, pc.RunE
	}
	root.PersistentFlags().AddFlagSet(pc.PersistentFlags())
	root.Flags().AddFlagSet(pc.LocalNonPersistentFlags())
	for _, g := range pc.Groups() {
		root.AddGroup(g)
	}
	if pre := pc.PersistentPreRunE; pre != nil {
		ours := root.PersistentPreRunE
		root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
			if err := ours(cmd, args); err != nil {
				return err
			}
			return pre(cmd, args)
		}
	}
	for _, c := range pc.Commands() {
		pc.RemoveCommand(c)
		if c.GroupID == "" {
			c.GroupID = groupProducts
		}
		root.AddCommand(c)
	}
}

func addGlobalFlags(f *core.Factory, fs *pflag.FlagSet) {
	g := f.Flags
	fs.StringVar(&g.Account, "account", "", "Account to use for this command")
	fs.StringVarP(&g.Workspace, "workspace", "w", "", "Workspace ID")
	g.Output.AddFlags(fs)
	fs.BoolVarP(&g.Yes, "yes", "y", false, "Skip confirmation prompts")
	fs.BoolVar(&g.NoInput, "no-input", false, "Never prompt; fail if input is required")
	fs.BoolVar(&g.NoColor, "no-color", false, "Disable colors")
	fs.BoolVarP(&g.Quiet, "quiet", "q", false, "Only print data and errors")
	fs.BoolVar(&g.Debug, "debug", false, "Print HTTP traces to stderr (secrets masked)")
}

func applyGlobalFlags(f *core.Factory) error {
	g := f.Flags
	if g.NoColor {
		f.IO.SetColorEnabled(false)
	}
	if g.NoInput {
		f.IO.NoInput = true
	}
	if g.Quiet {
		f.IO.Quiet = true
	}
	if v := f.IO.Getenv("REEARTH_DEBUG"); v != "" && v != "0" && v != "false" {
		g.Debug = true
	}
	if cfg, err := f.Config(); err == nil {
		if v, _, _ := cfg.Get("prompt"); v == "disabled" {
			f.IO.NoInput = true
		}
	}
	return nil
}

// finalize walks the tree: it rewrites examples for the binary name, turns
// argument errors into usage errors (exit 2) and wires account completion.
func finalize(root *cobra.Command, name, standalone string, f *core.Factory) {
	_ = root.RegisterFlagCompletionFunc("account", func(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
		cfg, err := f.Config()
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		return cfg.AccountNames(), cobra.ShellCompDirectiveNoFileComp
	})
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if name != "reearth" && c.Example != "" {
			if standalone != "" {
				c.Example = strings.ReplaceAll(c.Example, "$ reearth "+standalone+" ", "$ "+name+" ")
			}
			c.Example = strings.ReplaceAll(c.Example, "$ reearth ", "$ "+name+" ")
		}
		if args := c.Args; args != nil {
			c.Args = func(cmd *cobra.Command, a []string) error {
				if err := args(cmd, a); err != nil {
					return &cmdutil.Error{Exit: cmdutil.ExitUsage, Code: "usage", Message: err.Error()}
				}
				return nil
			}
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
	setHelp(root, f)
}

func wantsJSON(f *core.Factory) bool {
	o := f.Flags.Output
	return o.JSON != "" || o.JQ != "" || o.Output == "json" || o.Output == "ndjson"
}

// classify turns cobra's plain usage errors into usage errors (exit code 2).
func classify(err error) error {
	if isCobraUsageError(err) {
		return &cmdutil.Error{Exit: cmdutil.ExitUsage, Code: "usage", Message: err.Error()}
	}
	return err
}

func reportError(f *core.Factory, cmd *cobra.Command, err error) {
	e := cmdutil.AsError(err)
	if e.Silent {
		return
	}
	if e.Exit == cmdutil.ExitUsage && e.Hint == "" && cmd != nil {
		e.Hint = fmt.Sprintf("run `%s --help` for usage", cmd.CommandPath())
	}
	w := f.IO.ErrOut
	if wantsJSON(f) {
		b, _ := json.Marshal(map[string]any{"error": e})
		_, _ = fmt.Fprintln(w, string(b))
		return
	}
	cs := f.IO.ErrColor()
	if f.IO.IsStderrTTY() {
		_, _ = fmt.Fprintln(w, "  "+cs.FailureIcon()+" "+e.Message)
		if e.Hint != "" {
			_, _ = fmt.Fprintln(w, "    "+cs.Dim(e.Hint))
		}
		return
	}
	_, _ = fmt.Fprintln(w, "error: "+e.Message)
	if e.Hint != "" {
		_, _ = fmt.Fprintln(w, "hint: "+e.Hint)
	}
}

func isCobraUsageError(err error) bool {
	var e *cmdutil.Error
	if errors.As(err, &e) {
		return false
	}
	msg := err.Error()
	for _, p := range []string{"unknown command", "unknown flag", "unknown shorthand flag", "flag needs an argument", "invalid argument"} {
		if strings.HasPrefix(msg, p) {
			return true
		}
	}
	return false
}
