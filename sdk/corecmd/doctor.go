package corecmd

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	"github.com/reearth/cli/sdk/auth"
	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/config"
	"github.com/reearth/cli/sdk/core"
	"github.com/reearth/cli/sdk/credstore"
	"github.com/reearth/cli/sdk/envvar"
	"github.com/reearth/cli/sdk/output"
)

func NewCmdDoctor(f *core.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose configuration, credentials and network",
		Long: `Check the config file, the project file, the OS keyring, the connection
to the auth server and every stored account. The host binary may add checks;
reearth also checks its installation and version.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			// The config check reports a broken file, so load it before the
			// Printer would warn that it is ignored.
			_, _ = f.Config()
			p, err := f.Printer()
			if err != nil {
				return err
			}
			f.IO.StartProgress("Running checks…")
			checks := runChecks(ctx, f)
			for _, c := range f.DoctorChecks {
				checks = append(checks, c(ctx)...)
			}
			f.IO.StopProgress()

			failed := 0
			for _, c := range checks {
				if c.Status == core.CheckFail {
					failed++
				}
			}
			if err := p.Print(checks, func() error {
				cs := f.IO.Color()
				t := p.Table()
				for _, c := range checks {
					icon := cs.SuccessIcon()
					switch c.Status {
					case core.CheckWarn:
						icon = cs.WarningIcon()
					case core.CheckFail:
						icon = cs.FailureIcon()
					}
					detail := c.Detail
					if c.Hint != "" {
						detail += " — " + c.Hint
					}
					if p.IsHuman() {
						t.Row(icon, output.Cell{Text: c.Name, Style: cs.Bold}, output.Cell{Text: detail, Style: cs.Dim})
					} else {
						t.Row(string(c.Status), c.Name, detail)
					}
				}
				return t.Render()
			}); err != nil {
				return err
			}
			if failed > 0 {
				return cmdutil.NewError(cmdutil.ExitError, "doctor.failed",
					fmt.Sprintf("%d of %d checks failed", failed, len(checks)), "")
			}
			return nil
		},
	}
}

func runChecks(ctx context.Context, f *core.Factory) []core.Check {
	var checks []core.Check
	add := func(name string, status core.CheckStatus, detail, hint string) {
		checks = append(checks, core.Check{Name: name, Status: status, Detail: detail, Hint: hint})
	}

	cfg, err := f.Config()
	if err != nil {
		add("config", core.CheckFail, err.Error(), "fix or remove "+config.Path())
		return checks
	}
	add("config", core.CheckOK, config.Path(), "")
	if proj, err := f.Project(); err != nil {
		add("project file", core.CheckFail, err.Error(), "")
	} else if proj != nil {
		add("project file", core.CheckOK, proj.Path(), "")
	}

	usesKeyring := false
	for _, a := range cfg.Accounts {
		usesKeyring = usesKeyring || !a.InsecureStorage
	}
	if err := credstore.CheckKeyring(); err != nil {
		status := core.CheckWarn
		if usesKeyring {
			status = core.CheckFail
		}
		add("keyring", status, "unavailable: "+err.Error(), "use `login --insecure-storage` on machines without a keyring")
	} else {
		add("keyring", core.CheckOK, "available", "")
	}

	env, err := auth.ResolveEnv(cfg, auth.EnvProd)
	if err != nil {
		add("auth", core.CheckWarn, cmdutil.AsError(err).Message, cmdutil.AsError(err).Hint)
	} else {
		checkServer(ctx, env, add)
	}

	m, _ := f.Auth()
	for _, n := range cfg.AccountNames() {
		r := &auth.Resolved{Name: n, Account: cfg.Accounts[n], Source: auth.SourceConfig}
		name := "account " + n
		ts, err := m.TokenSource(r)
		if err == nil {
			// Refresh so that a revoked session fails even with a cached token.
			ts.Invalidate()
			_, err = ts.Token()
		}
		if err != nil {
			e := cmdutil.AsError(err)
			add(name, core.CheckFail, e.Message, e.Hint)
			continue
		}
		if ts.Static() {
			add(name, core.CheckOK, "token stored (not verified)", "")
		} else {
			add(name, core.CheckOK, "credentials work", "")
		}
	}
	if len(cfg.Accounts) == 0 && envvar.Get("REEARTH_TOKEN") == "" {
		add("accounts", core.CheckWarn, "no accounts", fmt.Sprintf("run `%s login`", f.AppName))
	}
	return checks
}

// checkServer verifies connectivity to the auth server and detects clock skew,
// which breaks token validation.
func checkServer(ctx context.Context, env *auth.Env, add func(string, core.CheckStatus, string, string)) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodHead, env.Domain+"/.well-known/openid-configuration", nil)
	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		add("network", core.CheckFail, "cannot reach "+env.Domain+": "+err.Error(), "check your connection or proxy settings (HTTPS_PROXY)")
		return
	}
	_ = resp.Body.Close()
	add("network", core.CheckOK, fmt.Sprintf("%s reachable (%s)", env.Domain, time.Since(start).Round(time.Millisecond)), "")
	if d, err := http.ParseTime(resp.Header.Get("Date")); err == nil {
		skew := time.Since(d)
		if skew < 0 {
			skew = -skew
		}
		if skew > 2*time.Minute {
			add("clock", core.CheckWarn, fmt.Sprintf("local clock is off by %s", skew.Round(time.Second)), "enable time synchronization; tokens may be rejected")
		} else {
			add("clock", core.CheckOK, "in sync", "")
		}
	}
}
