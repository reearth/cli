// Package hello is a sample product showing how to plug a product CLI into
// the Re:Earth CLI. It works as `reearth hello` and as `reearth-hello`.
package hello

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/spf13/cobra"

	"github.com/reearth/cli/sdk/auth"
	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/core"
	"github.com/reearth/cli/sdk/output"
)

type Product struct{}

var _ core.APIProduct = Product{}

func (Product) Name() string  { return "hello" }
func (Product) Short() string { return "Sample product: greet, and call an API as yourself" }

// BaseURL points at the auth server so that `hello me` can call /userinfo
// without any product backend.
func (Product) BaseURL(env *auth.Env) string { return env.Domain }

func (p Product) Command(f *core.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hello",
		Short: p.Short(),
		// A product's Long explains its concepts. --help shows it, and search
		// matches it against the product's commands, so write it for agents too.
		Long: `A sample product that shows how product commands behave.

"world" prints a greeting and needs no login. "me" calls an API as the
current account and prints your profile; it exits with code 4 when you are
not logged in. Both support --json.`,
	}
	cmd.AddCommand(newCmdWorld(f), newCmdMe(f, p))
	return cmd
}

type greeting struct {
	Message string `json:"message"`
	Name    string `json:"name"`
}

func newCmdWorld(f *core.Factory) *cobra.Command {
	var shout bool
	cmd := &cobra.Command{
		Use:   "world [name]",
		Short: "Print a greeting",
		Example: `  $ reearth hello world
  $ reearth hello world Kana --shout
  $ reearth hello world --json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := "world"
			if len(args) > 0 {
				name = args[0]
			}
			g := greeting{Message: fmt.Sprintf("Hello, %s!", name), Name: name}
			if shout {
				g.Message = strings.ToUpper(g.Message)
			}
			p, err := f.Printer()
			if err != nil {
				return err
			}
			return p.Print(g, func() error {
				msg := g.Message
				if p.IsHuman() {
					msg = "  " + f.IO.Color().Accent("◉") + " " + f.IO.Color().Bold(msg)
				}
				_, err := fmt.Fprintln(f.IO.Out, msg)
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&shout, "shout", false, "Greet loudly")
	return cmd
}

type me struct {
	Sub           string `json:"sub"`
	Email         string `json:"email,omitempty"`
	EmailVerified bool   `json:"email_verified,omitempty"`
	Name          string `json:"name,omitempty"`
	Nickname      string `json:"nickname,omitempty"`
	UpdatedAt     string `json:"updated_at,omitempty"`
}

func newCmdMe(f *core.Factory, p Product) *cobra.Command {
	return &cobra.Command{
		Use:   "me",
		Short: "Show your profile via an authenticated API call",
		Long: `Call the OIDC userinfo endpoint with the current account.

This shows the pattern every product follows: ask the Factory for an
authenticated *http.Client. Token refresh and retries happen transparently.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.HTTPClient(cmd.Context(), p)
			if err != nil {
				return err
			}
			base, err := f.BaseURL(p)
			if err != nil {
				return err
			}
			req, err := http.NewRequestWithContext(cmd.Context(), http.MethodGet, base+"/userinfo", nil)
			if err != nil {
				return err
			}
			f.IO.StartProgress("Fetching your profile…")
			resp, err := client.Do(req)
			f.IO.StopProgress()
			if err != nil {
				return err
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode == http.StatusUnauthorized {
				return cmdutil.NewError(cmdutil.ExitAuth, "auth.unauthorized", "the server rejected the credentials", fmt.Sprintf("run `%s login`", f.AppName))
			}
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("userinfo: %s", resp.Status)
			}
			var u me
			if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
				return err
			}

			pr, err := f.Printer()
			if err != nil {
				return err
			}
			return pr.Print(u, func() error {
				cs := f.IO.Color()
				t := pr.Table()
				row := func(k, v string) {
					if v != "" {
						t.Row(output.Cell{Text: k, Style: cs.Dim}, v)
					}
				}
				row("name", u.Name)
				row("email", u.Email)
				row("sub", u.Sub)
				return t.Render()
			})
		},
	}
}
