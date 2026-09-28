package corecmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/reearth/cli/sdk/auth"
	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/core"
	"github.com/reearth/cli/sdk/output"
)

func NewCmdAuth(f *core.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Inspect credentials",
	}
	cmd.AddCommand(newCmdAuthStatus(f), newCmdAuthToken(f))
	return cmd
}

type statusJSON struct {
	accountJSON
	Valid     bool           `json:"valid"`
	Verified  bool           `json:"verified"`
	ExpiresAt *time.Time     `json:"expires_at,omitempty"`
	Error     *cmdutil.Error `json:"error,omitempty"`
}

func newCmdAuthStatus(f *core.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Check that each account's credentials work",
		Long: `Check each account by refreshing its access token.

Pass --account to check one account only. The command exits with code 4
when any checked account needs to log in again.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := f.Config()
			if err != nil {
				return err
			}
			m, err := f.Auth()
			if err != nil {
				return err
			}
			p, err := f.Printer()
			if err != nil {
				return err
			}

			var targets []*auth.Resolved
			if f.Flags.Account != "" || f.IO.Getenv("REEARTH_TOKEN") != "" {
				r, err := f.Account()
				if err != nil {
					return err
				}
				targets = append(targets, r)
			} else {
				for _, n := range cfg.AccountNames() {
					targets = append(targets, &auth.Resolved{Name: n, Account: cfg.Accounts[n], Source: auth.SourceConfig})
				}
			}
			if len(targets) == 0 {
				return cmdutil.NewError(cmdutil.ExitAuth, "auth.not_logged_in", "no accounts", fmt.Sprintf("run `%s login`", f.AppName))
			}

			f.IO.StartProgress("Checking credentials…")
			results := make([]statusJSON, 0, len(targets))
			failed := 0
			for _, r := range targets {
				s := statusJSON{accountJSON: accountView(r.Name, r.Account, r.Name != "" && r.Name == cfg.Active)}
				if r.Name == "" {
					s.Name = r.DisplayName()
				}
				ts, err := m.TokenSource(r)
				if err == nil {
					var tokExp time.Time
					if tok, terr := ts.Token(); terr != nil {
						err = terr
					} else {
						tokExp = tok.Expiry
					}
					if err == nil {
						s.Valid = true
						s.Verified = !ts.Static()
						if !tokExp.IsZero() {
							s.ExpiresAt = &tokExp
						}
					}
				}
				if err != nil {
					s.Error = cmdutil.AsError(err)
					failed++
				}
				results = append(results, s)
			}
			f.IO.StopProgress()

			if err := p.Print(results, func() error { return renderStatus(f, p, results) }); err != nil {
				return err
			}
			if failed > 0 {
				return cmdutil.SilentExit(cmdutil.ExitAuth, "auth.invalid")
			}
			return nil
		},
	}
}

func renderStatus(f *core.Factory, p *output.Printer, results []statusJSON) error {
	cs := f.IO.Color()
	t := p.Table()
	for _, s := range results {
		mark, state := cs.SuccessIcon(), "signed in"
		switch {
		case s.Error != nil:
			mark, state = cs.FailureIcon(), s.Error.Message
			if s.Error.Hint != "" {
				state += " — " + s.Error.Hint
			}
		case !s.Verified:
			state = "token stored (not verified)"
		case s.ExpiresAt != nil:
			state += " · token expires " + output.RelativeTime(time.Since(*s.ExpiresAt))
		}
		if !p.IsHuman() {
			t.Row(s.Name, fmt.Sprint(s.Valid), userLabel(s.User), s.Env, state)
			continue
		}
		t.Row(mark, output.Cell{Text: s.Name, Style: cs.Bold}, orDash(userLabel(s.User)), output.Cell{Text: s.Env, Style: cs.Dim}, output.Cell{Text: state, Style: cs.Dim})
	}
	return t.Render()
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func newCmdAuthToken(f *core.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "token",
		Short: "Print an access token for the current account",
		Long: `Print a valid access token for the current account to stdout.

Use this only to hand a token to a tool that cannot call the CLI itself.
It is refused when a coding agent is detected. Set REEARTH_AGENT=0 to override.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if f.IO.Agent != "" {
				return cmdutil.NewError(cmdutil.ExitError, "auth.token_refused",
					"refusing to print an access token to a coding agent ("+f.IO.Agent+")",
					"let the CLI make authenticated requests (e.g. `"+f.AppName+" api`), or set REEARTH_AGENT=0 if a human runs this")
			}
			r, err := f.Account()
			if err != nil {
				return err
			}
			m, err := f.Auth()
			if err != nil {
				return err
			}
			ts, err := m.TokenSource(r)
			if err != nil {
				return err
			}
			tok, err := ts.Token()
			if err != nil {
				return err
			}
			if f.IO.IsStdoutTTY() {
				f.IO.Warn("This token grants access to account %s. Do not share it.", r.DisplayName())
			}
			_, err = fmt.Fprintln(f.IO.Out, tok.AccessToken)
			return err
		},
	}
}
