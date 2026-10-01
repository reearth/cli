package corecmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/reearth/cli/sdk/auth"
	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/config"
	"github.com/reearth/cli/sdk/core"
	"github.com/reearth/cli/sdk/output"
)

type accountJSON struct {
	Name      string       `json:"name"`
	Active    bool         `json:"active"`
	Env       string       `json:"env"`
	Kind      string       `json:"kind"`
	Product   string       `json:"product,omitempty"`
	User      *config.User `json:"user,omitempty"`
	Workspace string       `json:"workspace,omitempty"`
	Storage   string       `json:"storage"`
}

func accountView(name string, acc *config.Account, active bool) accountJSON {
	storage := "keyring"
	switch {
	case name == "":
		// The REEARTH_TOKEN account is the only one without a name; its
		// token lives in the environment.
		storage = "env"
	case acc.InsecureStorage:
		storage = "file"
	}
	return accountJSON{
		Name: name, Active: active, Env: acc.Env, Kind: acc.Kind, Product: acc.Product,
		User: acc.User, Workspace: acc.Workspace, Storage: storage,
	}
}

func NewCmdAccount(f *core.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "account",
		Aliases: []string{"accounts"},
		Short:   "List, inspect and rename signed-in accounts",
		Long: fmt.Sprintf(`Manage the accounts stored by "%[1]s login".

Each command chooses its account in this order:

  1. the --account flag
  2. REEARTH_TOKEN, an anonymous token account meant for CI
  3. REEARTH_ACCOUNT
  4. account: in .reearth.yaml, searched upward from the current directory
  5. the active account, set with "%[1]s use"

REEARTH_<PRODUCT>_TOKEN, such as REEARTH_CMS_TOKEN, overrides the token for
that product only. "%[1]s whoami" shows which account applies here and why.`, f.AppName),
	}
	cmd.AddCommand(newCmdAccountList(f), newCmdAccountCurrent(f, "current"), newCmdAccountRename(f))
	return cmd
}

func NewCmdWhoami(f *core.Factory) *cobra.Command {
	return newCmdAccountCurrent(f, "whoami")
}

func newCmdAccountList(f *core.Factory) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List accounts",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := f.Config()
			if err != nil {
				return err
			}
			p, err := f.Printer()
			if err != nil {
				return err
			}
			names := cfg.AccountNames()
			views := make([]accountJSON, 0, len(names))
			for _, n := range names {
				views = append(views, accountView(n, cfg.Accounts[n], n == cfg.Active))
			}
			return p.Print(views, func() error {
				if len(views) == 0 {
					f.IO.Info("No accounts yet. Run `%s login` to add one.", f.AppName)
					return nil
				}
				cs := f.IO.Color()
				t := p.Table("", "name", "user", "env", "kind")
				if !p.IsHuman() {
					t = p.Table()
				}
				for _, v := range views {
					user := userLabel(v.User)
					kind := v.Kind
					if v.Product != "" {
						kind += " · " + v.Product
					}
					if v.Storage == "file" {
						kind += " · file"
					}
					if !p.IsHuman() {
						t.Row(v.Name, fmt.Sprint(v.Active), user, v.Env, v.Kind)
						continue
					}
					name := output.Cell{Text: v.Name}
					if v.Active {
						name.Style = cs.Bold
					}
					if user == "" {
						user = "—"
					}
					t.Row(cs.ActiveMark(v.Active), name, output.Cell{Text: user}, output.Cell{Text: v.Env, Style: cs.Dim}, output.Cell{Text: kind, Style: cs.Dim})
				}
				return t.Render()
			})
		},
	}
}

func sourceLabel(r *auth.Resolved, f *core.Factory) string {
	switch r.Source {
	case auth.SourceFlag:
		return "--account flag"
	case auth.SourceEnv:
		return "REEARTH_ACCOUNT"
	case auth.SourceToken:
		return "REEARTH_TOKEN"
	case auth.SourceProject:
		if p, _ := f.Project(); p != nil {
			return p.Path()
		}
		return ".reearth.yaml"
	default:
		return "active account"
	}
}

func newCmdAccountCurrent(f *core.Factory, use string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: "Show the account used by commands here, and why",
		Long: `Show the account used by commands here, and why it was selected.

REEARTH_<PRODUCT>_TOKEN overrides the account for that product; the variables
that are set are listed as well.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := f.Account()
			if err != nil {
				return err
			}
			p, err := f.Printer()
			if err != nil {
				return err
			}
			var overrides []string
			for _, pr := range f.Products {
				if k := core.ProductEnv(pr, "TOKEN"); os.Getenv(k) != "" {
					overrides = append(overrides, k)
				}
			}
			v := struct {
				accountJSON
				Source         string   `json:"source"`
				TokenOverrides []string `json:"token_overrides,omitempty"`
			}{accountView(r.Name, r.Account, true), r.Source, overrides}
			return p.Print(v, func() error {
				cs := f.IO.Color()
				w := f.IO.Out
				if !p.IsHuman() {
					_, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", r.DisplayName(), userLabel(r.Account.User), r.Account.Env, r.Source, strings.Join(overrides, ","))
					return err
				}
				line := "  " + cs.ActiveMark(true) + " " + cs.Bold(r.DisplayName())
				if r.Account.User != nil {
					line += "  " + userLabel(r.Account.User)
				}
				_, _ = fmt.Fprintln(w, line)
				_, err := fmt.Fprintln(w, "    "+cs.Dim(fmt.Sprintf("%s · %s · selected by %s", r.Account.Env, r.Account.Kind, sourceLabel(r, f))))
				if err == nil && len(overrides) > 0 {
					_, err = fmt.Fprintln(w, "    "+cs.Dim(strings.Join(overrides, ", ")+" overrides this account for its product"))
				}
				return err
			})
		},
	}
}

func newCmdAccountRename(f *core.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "rename <old> <new>",
		Short: "Rename an account",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			oldName, newName := args[0], args[1]
			if err := auth.ValidateAccountName(newName); err != nil {
				return err
			}
			cfg, err := f.Config()
			if err != nil {
				return err
			}
			m, err := f.Auth()
			if err != nil {
				return err
			}
			acc, ok := cfg.Accounts[oldName]
			if !ok {
				return cmdutil.NotFoundf("account %q not found", oldName)
			}
			if _, taken := cfg.Accounts[newName]; taken {
				return cmdutil.FlagErrorf("account %q already exists", newName)
			}
			store := m.Store(acc)
			sec, err := store.Get(oldName)
			if err != nil {
				return fmt.Errorf("read credentials of %s: %w", oldName, err)
			}
			if err := store.Set(newName, sec); err != nil {
				return err
			}
			cfg.Accounts[newName] = acc
			delete(cfg.Accounts, oldName)
			if cfg.Active == oldName {
				cfg.Active = newName
			}
			if err := cfg.Save(); err != nil {
				return err
			}
			if err := store.Delete(oldName); err != nil {
				f.IO.Warn("Could not remove the old credentials entry %q: %v", oldName, err)
			}
			f.IO.Success("Renamed %s to %s", oldName, f.IO.ErrColor().Bold(newName))
			return nil
		},
	}
}
