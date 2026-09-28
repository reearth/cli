// Package corecmd implements the commands shared by every Re:Earth CLI
// binary: login, accounts, config, api, skills, version and doctor.
package corecmd

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/spf13/cobra"

	"github.com/reearth/cli/sdk/auth"
	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/config"
	"github.com/reearth/cli/sdk/core"
	"github.com/reearth/cli/sdk/credstore"
)

func NewCmdLogin(f *core.Factory) *cobra.Command {
	var (
		web, device, withToken, insecure bool
		envName, product                 string
	)
	cmd := &cobra.Command{
		Use:   "login [account]",
		Short: "Sign in and add or refresh an account",
		Long: `Sign in to Re:Earth and store the credentials in the OS keyring.

The browser flow (authorization code with PKCE on a loopback address) is used
when a local browser is available. The device-code flow is used otherwise,
for example over SSH or in a container. Force one with --web or --device.`,
		Example: `  $ reearth login
  $ reearth login work --env prod
  $ reearth login --device
  $ reearth login ci --with-token --product cms < token.txt`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if web && device {
				return cmdutil.FlagErrorf("--web and --device cannot be used together")
			}
			if product != "" && !withToken {
				return cmdutil.FlagErrorf("--product can only be used with --with-token")
			}
			cfg, err := f.Config()
			if err != nil {
				return err
			}
			m, err := f.Auth()
			if err != nil {
				return err
			}

			name := ""
			if len(args) > 0 {
				name = args[0]
				if err := auth.ValidateAccountName(name); err != nil {
					return err
				}
			}
			existing := cfg.Accounts[name]
			if envName == "" && existing != nil {
				envName = existing.Env
			}
			if envName == "" {
				envName = auth.EnvProd
			}
			if !slices.Contains(auth.EnvNames(cfg), envName) {
				return cmdutil.FlagErrorf("unknown environment %q (known: %s)", envName, strings.Join(auth.EnvNames(cfg), ", "))
			}

			if !insecure {
				if existing != nil && existing.InsecureStorage {
					insecure = true
				} else if err := credstore.CheckKeyring(); err != nil {
					return &cmdutil.Error{
						Exit:    cmdutil.ExitError,
						Code:    "auth.keyring_unavailable",
						Message: "the OS keyring is not available: " + err.Error(),
						Hint:    "pass --insecure-storage to store credentials in " + credstore.DefaultFilePath(config.Dir()) + " instead",
					}
				}
			}

			var acc *config.Account
			if withToken {
				name, acc, err = loginWithToken(f, cfg, m, name, envName, product, insecure)
			} else {
				name, acc, err = loginOAuth(cmd.Context(), f, cfg, m, name, envName, auth.DetectFlow(auth.SystemFlowEnv(), web, device), insecure)
			}
			if err != nil {
				return err
			}

			cfg.Accounts[name] = acc
			cfg.Active = name
			if err := cfg.Save(); err != nil {
				return err
			}

			who := ""
			if acc.User != nil {
				who = " as " + f.IO.ErrColor().Bold(userLabel(acc.User))
			}
			f.IO.Success("Signed in%s %s", who, f.IO.ErrColor().Dim("(account: "+name+", env: "+acc.Env+")"))
			if len(cfg.Accounts) > 1 {
				f.IO.Hint("Switch accounts with `%s use <account>`", f.AppName)
			}

			p, err := f.Printer()
			if err != nil {
				return err
			}
			return p.Print(accountView(name, acc, true), func() error { return nil })
		},
	}
	fl := cmd.Flags()
	fl.BoolVar(&web, "web", false, "Use the browser flow")
	fl.BoolVar(&device, "device", false, "Use the device-code flow")
	fl.StringVar(&envName, "env", "", "Environment to sign in to (default: prod)")
	fl.BoolVar(&withToken, "with-token", false, "Read an API token from stdin instead of signing in")
	fl.StringVar(&product, "product", "", "Restrict a --with-token account to one product (e.g. cms)")
	fl.BoolVar(&insecure, "insecure-storage", false, "Store credentials in a plain file instead of the OS keyring")
	return cmd
}

func loginWithToken(f *core.Factory, cfg *config.Config, m *auth.Manager, name, envName, product string, insecure bool) (string, *config.Account, error) {
	var token string
	if !f.IO.IsStdinTTY() {
		b, err := io.ReadAll(f.IO.In)
		if err != nil {
			return "", nil, err
		}
		token = strings.TrimSpace(string(b))
	} else {
		var err error
		if token, err = f.Prompter.Password("Paste your token"); err != nil {
			return "", nil, err
		}
	}
	if token == "" {
		return "", nil, cmdutil.FlagErrorf("no token given (pipe it to stdin: `%s login --with-token < token.txt`)", f.AppName)
	}
	if name == "" {
		def := "default"
		if product != "" {
			def = product
		}
		var err error
		if name, err = askAccountName(f, cfg, def); err != nil {
			return "", nil, err
		}
	}
	acc := &config.Account{Env: envName, Kind: config.KindToken, Product: product, InsecureStorage: insecure}
	if err := m.SaveToken(name, acc, token); err != nil {
		return "", nil, err
	}
	return name, acc, nil
}

func loginOAuth(ctx context.Context, f *core.Factory, cfg *config.Config, m *auth.Manager, name, envName string, flow auth.Flow, insecure bool) (string, *config.Account, error) {
	env, err := auth.ResolveEnv(cfg, envName)
	if err != nil {
		return "", nil, err
	}
	cs := f.IO.ErrColor()
	f.IO.Newline()
	f.IO.Println(cs.Accent("◉") + " " + cs.Bold("Re:Earth") + " " + cs.Dim("· "+env.Name))
	f.IO.Newline()

	tok, err := auth.Login(ctx, auth.LoginOptions{
		Env:         env,
		Flow:        flow,
		IO:          f.IO,
		OpenBrowser: f.OpenBrowser,
		HTTPClient:  m.HTTPClient,
	})
	if err != nil {
		return "", nil, err
	}

	uctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	user, err := auth.FetchUser(uctx, m.HTTPClient, env, tok)
	if err != nil {
		f.IO.Warn("Could not fetch your profile: %v", err)
	}

	if name == "" {
		name = accountForUser(cfg, env.Name, user)
		if name == "" {
			if name, err = askAccountName(f, cfg, suggestName(cfg, user)); err != nil {
				return "", nil, err
			}
		}
	}
	acc := cfg.Accounts[name]
	if acc == nil {
		acc = &config.Account{}
	} else if acc.User != nil && user != nil && acc.User.Sub != user.Sub {
		f.IO.Warn("Account %q previously belonged to %s; it now signs in as %s", name, userLabel(acc.User), userLabel(user))
	}
	acc.Env, acc.Kind, acc.Product, acc.InsecureStorage = env.Name, config.KindOAuth, "", insecure
	if user != nil {
		acc.User = user
	}
	if err := m.SaveLogin(name, acc, env, tok); err != nil {
		return "", nil, err
	}
	return name, acc, nil
}

// accountForUser finds an existing account for the same user and env, so re-login keeps its name.
func accountForUser(cfg *config.Config, env string, user *config.User) string {
	if user == nil {
		return ""
	}
	for _, n := range cfg.AccountNames() {
		a := cfg.Accounts[n]
		if a.Kind == config.KindOAuth && a.Env == env && a.User != nil && a.User.Sub == user.Sub {
			return n
		}
	}
	return ""
}

func suggestName(cfg *config.Config, user *config.User) string {
	base := "default"
	if user != nil && user.Email != "" {
		local, _, _ := strings.Cut(user.Email, "@")
		local = strings.Map(func(r rune) rune {
			if r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '-') {
				return r
			}
			return -1
		}, local)
		if local != "" {
			base = local
		}
	}
	name := base
	for i := 2; cfg.Accounts[name] != nil; i++ {
		name = fmt.Sprintf("%s-%d", base, i)
	}
	return name
}

func askAccountName(f *core.Factory, cfg *config.Config, def string) (string, error) {
	if !f.IO.CanPrompt() {
		return def, nil
	}
	for {
		name, err := f.Prompter.Input("Account name", def)
		if err != nil {
			return "", err
		}
		if err := auth.ValidateAccountName(name); err != nil {
			f.IO.Warn("%s", err.Error())
			continue
		}
		return name, nil
	}
}

func userLabel(u *config.User) string {
	if u == nil {
		return ""
	}
	if u.Email != "" {
		return u.Email
	}
	if u.Name != "" {
		return u.Name
	}
	return u.Sub
}

func NewCmdLogout(f *core.Factory) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "logout [account]",
		Short: "Sign out and remove an account's credentials",
		Example: `  $ reearth logout
  $ reearth logout work
  $ reearth logout --all`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := f.Config()
			if err != nil {
				return err
			}
			m, err := f.Auth()
			if err != nil {
				return err
			}
			var names []string
			switch {
			case all:
				names = cfg.AccountNames()
			case len(args) > 0:
				names = args
			case f.Flags.Account != "":
				names = []string{f.Flags.Account}
			case cfg.Active != "":
				names = []string{cfg.Active}
			}
			if len(names) == 0 {
				return cmdutil.NewError(cmdutil.ExitAuth, "auth.not_logged_in", "not logged in", "")
			}
			for _, n := range names {
				if _, ok := cfg.Accounts[n]; !ok {
					return cmdutil.NotFoundf("account %q not found", n)
				}
			}
			if all {
				if err := f.Confirm(fmt.Sprintf("Log out of all %d accounts?", len(names))); err != nil {
					return err
				}
			}

			prevActive := cfg.Active
			for _, n := range names {
				acc := cfg.Accounts[n]
				store := m.Store(acc)
				if sec, err := store.Get(n); err == nil && sec.RefreshToken != "" {
					if env, err := auth.ResolveEnv(cfg, acc.Env); err == nil {
						rctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
						if err := auth.Revoke(rctx, m.HTTPClient, env, sec.RefreshToken); err != nil {
							f.IO.Warn("Could not revoke the session of %s on the server: %v", n, err)
						}
						cancel()
					}
				}
				if err := store.Delete(n); err != nil {
					return fmt.Errorf("remove credentials of %s: %w", n, err)
				}
				cfg.RemoveAccount(n)
				f.IO.Success("Logged out of %s", f.IO.ErrColor().Bold(n))
			}
			if err := cfg.Save(); err != nil {
				return err
			}
			if cfg.Active != prevActive && cfg.Active != "" {
				f.IO.Info("Active account is now %s", f.IO.ErrColor().Bold(cfg.Active))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Log out of every account")
	return cmd
}

func NewCmdUse(f *core.Factory) *cobra.Command {
	return &cobra.Command{
		Use:     "use [account]",
		Short:   "Switch the active account",
		Example: "  $ reearth use personal",
		Args:    cobra.MaximumNArgs(1),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
			return completeAccounts(f, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := f.Config()
			if err != nil {
				return err
			}
			names := cfg.AccountNames()
			if len(names) == 0 {
				return cmdutil.NewError(cmdutil.ExitAuth, "auth.not_logged_in", "no accounts", fmt.Sprintf("run `%s login`", f.AppName))
			}
			var name string
			if len(args) > 0 {
				name = args[0]
			} else {
				if !f.IO.CanPrompt() {
					return cmdutil.NoInputError("account name", "an account name")
				}
				labels := make([]string, len(names))
				def := 0
				for i, n := range names {
					labels[i] = n
					if u := cfg.Accounts[n].User; u != nil {
						labels[i] += "  " + f.IO.ErrColor().Dim(userLabel(u))
					}
					if n == cfg.Active {
						def = i
					}
				}
				i, err := f.Prompter.Select("Switch to account", labels, def)
				if err != nil {
					return err
				}
				name = names[i]
			}
			acc, ok := cfg.Accounts[name]
			if !ok {
				return cmdutil.NotFoundf("account %q not found (known: %s)", name, strings.Join(names, ", "))
			}
			cfg.Active = name
			if err := cfg.Save(); err != nil {
				return err
			}
			detail := acc.Env
			if acc.User != nil {
				detail = userLabel(acc.User) + ", " + acc.Env
			}
			f.IO.Success("Now using %s %s", f.IO.ErrColor().Bold(name), f.IO.ErrColor().Dim("("+detail+")"))
			return nil
		},
	}
}

func completeAccounts(f *core.Factory, args []string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	cfg, err := f.Config()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	return cfg.AccountNames(), cobra.ShellCompDirectiveNoFileComp
}
