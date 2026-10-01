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
	"slices"
	"strconv"
	"strings"

	"github.com/cli/browser"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/reearth/cli/sdk/build"
	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/core"
	"github.com/reearth/cli/sdk/corecmd"
	"github.com/reearth/cli/sdk/envvar"
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
	o = symlinkOptions(o, os.Args[0])
	return Execute(NewFactory(o, iostreams.System()), o, args)
}

// symlinkOptions implements busybox-style dispatch: the unified binary
// invoked as "reearth-cms" runs exactly like the standalone binary that
// Main builds. Host-only extras (Extra, Dispatch, BeforeRun, AfterRun and
// DoctorChecks, such as upgrade, extensions and update notices) are not part
// of the standalone experience and are dropped.
func symlinkOptions(o Options, argv0 string) Options {
	if o.Standalone {
		return o
	}
	exe := strings.TrimSuffix(filepath.Base(argv0), ".exe")
	name, ok := strings.CutPrefix(exe, o.Name+"-")
	if !ok {
		return o
	}
	for _, p := range o.Products {
		if p.Name() == name {
			return Options{Name: exe, Products: []core.Product{p}, Standalone: true}
		}
	}
	return o
}

// Execute runs args against a Factory and returns the exit code.
func Execute(f *core.Factory, o Options, args []string) int {
	// --no-color must also hold for help and for errors that occur before
	// the flags are parsed.
	for _, a := range args {
		if a == "--" {
			break
		}
		if v, ok := strings.CutPrefix(a, "--no-color"); ok && (v == "" || strings.HasPrefix(v, "=")) {
			if b, err := strconv.ParseBool(strings.TrimPrefix(v, "=")); v == "" || (err == nil && b) {
				f.IO.SetColorEnabled(false)
			}
		}
	}
	root := NewRoot(f, o)
	// Cobra prints the help of a command group, with exit code 0, when it is
	// given an unknown subcommand. Report a usage error instead.
	var unknown error
	help := root.HelpFunc()
	root.SetHelpFunc(func(c *cobra.Command, a []string) {
		if h := c.Flags().Lookup("help"); !c.Runnable() && c.HasAvailableSubCommands() && (h == nil || !h.Changed) {
			if rest := c.Flags().Args(); len(rest) > 0 {
				unknown = &cmdutil.Error{Exit: cmdutil.ExitUsage, Code: "usage", Message: fmt.Sprintf("unknown command %q for %q", rest[0], c.CommandPath())}
				return
			}
		}
		help(c, a)
	})
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
	if err == nil {
		err = unknown
	}
	if err != nil {
		err = classify(err)
		reportError(f, cmd, err, args)
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
	if v, _ := f.Setting("browser"); strings.TrimSpace(v) != "" {
		argv := append(strings.Fields(v), url)
		c := exec.Command(argv[0], argv[1:]...)
		c.Stdout, c.Stderr = io.Discard, io.Discard
		return c.Start()
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
	root.InitDefaultHelpCmd()
	for _, c := range root.Commands() {
		if c.Name() == "help" {
			c.Run, c.RunE = nil, runHelp
		}
	}

	standalone := ""
	if o.Standalone && len(o.Products) == 1 {
		standalone = o.Products[0].Name()
	}
	finalize(root, o.Name, standalone, f)
	return root
}

// runHelp replaces the Run of cobra's help command, which prints "Unknown
// help topic" to stdout with exit code 0.
func runHelp(c *cobra.Command, args []string) error {
	cmd, rest, err := c.Root().Find(args)
	if err != nil || cmd == nil || len(rest) > 0 {
		return &cmdutil.Error{Exit: cmdutil.ExitUsage, Code: "usage",
			Message: fmt.Sprintf("unknown help topic %q", strings.Join(args, " ")),
			Hint:    fmt.Sprintf("run `%s --help` to list commands and help topics", c.Root().Name())}
	}
	if cmd.Context() == nil {
		cmd.SetContext(c.Context())
	}
	cmd.InitDefaultHelpFlag()
	cmd.InitDefaultVersionFlag()
	return cmd.Help()
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
	g.Output.AddFlags(fs)
	fs.BoolVarP(&g.Yes, "yes", "y", false, "Skip confirmation prompts")
	fs.BoolVar(&g.NoInput, "no-input", false, "Never prompt; fail if input is required")
	fs.BoolVar(&g.NoColor, "no-color", false, "Disable colors")
	fs.BoolVarP(&g.Quiet, "quiet", "q", false, "Only print data, warnings and errors")
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
	if envvar.True("REEARTH_DEBUG") {
		g.Debug = true
	}
	if v, _ := f.Setting("prompt"); v == "disabled" {
		f.IO.NoInput = true
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

// wantsJSON reports whether errors should be printed as JSON. It also reads
// the raw args, because a flag error stops parsing before --json is seen.
func wantsJSON(f *core.Factory, args []string) bool {
	o := f.Flags.Output
	if o.JSON != "" || o.JQ != "" || isJSONFormat(o.Output) {
		return true
	}
	_, json := RawFlag(args, "json")
	_, jq := RawFlag(args, "jq")
	v, _ := RawFlag(args, "output", "o")
	return json || jq || isJSONFormat(v)
}

func isJSONFormat(v string) bool {
	v = strings.ToLower(v)
	return v == "json" || v == "ndjson"
}

// RawFlag finds a global flag in unparsed args (--name, --name=v, --name v,
// -s v, -sv, -s=v) before "--". Without "=", the value is the next arg. As in
// pflag, the shorthand may follow the boolean global shorthands (-yojson). It
// is for reading flags when parsing may have failed.
func RawFlag(args []string, name string, short ...string) (value string, ok bool) {
	next := func(i int) string {
		if i+1 < len(args) {
			return args[i+1]
		}
		return ""
	}
	for i, a := range args {
		if a == "--" {
			break
		}
		if rest, found := strings.CutPrefix(a, "--"+name); found {
			if rest == "" {
				return next(i), true
			}
			if v, found := strings.CutPrefix(rest, "="); found {
				return v, true
			}
		}
		if len(short) == 0 || !strings.HasPrefix(a, "-") || strings.HasPrefix(a, "--") {
			continue
		}
		cluster := a[1:]
		for cluster != "" {
			if slices.Contains(short, cluster[:1]) {
				if rest := cluster[1:]; rest != "" {
					return strings.TrimPrefix(rest, "="), true
				}
				return next(i), true
			}
			if !strings.Contains(boolShorthands, cluster[:1]) {
				break
			}
			cluster = cluster[1:]
		}
	}
	return "", false
}

// boolShorthands are the global boolean shorthands (-y, -q), which pflag lets
// other shorthands follow in one arg.
const boolShorthands = "yq"

// classify turns cobra's plain usage errors into usage errors (exit code 2).
func classify(err error) error {
	if isCobraUsageError(err) {
		return &cmdutil.Error{Exit: cmdutil.ExitUsage, Code: "usage", Message: err.Error()}
	}
	return err
}

func reportError(f *core.Factory, cmd *cobra.Command, err error, args []string) {
	e := cmdutil.AsError(err)
	if e.Silent {
		return
	}
	if e.Exit == cmdutil.ExitUsage && e.Hint == "" && cmd != nil {
		e.Hint = fmt.Sprintf("run `%s --help` for usage", cmd.CommandPath())
	}
	w := f.IO.ErrOut
	if wantsJSON(f, args) {
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
