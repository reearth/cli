// Package core is the contract between the CLI and product commands: the
// Product interface and the Factory that products receive.
package core

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"github.com/reearth/cli/sdk/auth"
	"github.com/reearth/cli/sdk/build"
	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/config"
	"github.com/reearth/cli/sdk/httpx"
	"github.com/reearth/cli/sdk/iostreams"
	"github.com/reearth/cli/sdk/output"
	"github.com/reearth/cli/sdk/prompt"
)

// Product is a Re:Earth product CLI (viz, cms, flow, ...). The same Product
// runs standalone (reearth-cms) or as a subcommand (reearth cms).
type Product interface {
	// Name is the subcommand name, e.g. "cms".
	Name() string
	// Short is a one-line description.
	Short() string
	// Command builds the product's command tree.
	Command(f *Factory) *cobra.Command
}

// APIProduct is implemented by products that call an HTTP API.
type APIProduct interface {
	Product
	// BaseURL returns the API base URL for an environment ("" if unknown).
	BaseURL(env *auth.Env) string
}

// GlobalFlags are the persistent flags shared by every command.
type GlobalFlags struct {
	Account   string
	Workspace string
	Output    output.Options
	Yes       bool
	NoInput   bool
	NoColor   bool
	Quiet     bool
	Debug     bool
}

// Factory gives commands lazy access to shared services.
type Factory struct {
	// AppName is the binary name used in help and hints ("reearth", "reearth-cms").
	AppName  string
	IO       *iostreams.IOStreams
	Prompter prompt.Prompter
	Flags    *GlobalFlags
	Products []Product

	// OpenBrowser opens a URL (overridable for tests).
	OpenBrowser func(url string) error
	// DoctorChecks are extra checks for `doctor` contributed by the host binary.
	DoctorChecks []func(ctx context.Context) []Check

	once    sync.Once
	cfg     *config.Config
	cfgErr  error
	project *config.Project
	projErr error
	auth    *auth.Manager
}

// Config returns the loaded user config.
func (f *Factory) Config() (*config.Config, error) {
	f.load()
	return f.cfg, f.cfgErr
}

// Project returns the project file (.reearth.yaml) for the working directory, or nil.
func (f *Factory) Project() (*config.Project, error) {
	f.load()
	return f.project, f.projErr
}

func (f *Factory) load() {
	f.once.Do(func() {
		f.cfg, f.cfgErr = config.Load()
		if wd, err := os.Getwd(); err == nil {
			f.project, f.projErr = config.FindProject(wd)
		}
		if f.cfgErr == nil {
			f.auth = auth.NewManager(f.cfg, f.AppName)
			f.auth.HTTPClient = f.baseHTTPClient(nil)
		}
	})
}

// SetConfig injects a config (for tests).
func (f *Factory) SetConfig(cfg *config.Config, m *auth.Manager) {
	f.once.Do(func() {})
	f.cfg, f.auth = cfg, m
}

// Auth returns the account manager.
func (f *Factory) Auth() (*auth.Manager, error) {
	f.load()
	if f.cfgErr != nil {
		return nil, f.cfgErr
	}
	return f.auth, nil
}

// Account resolves the account for this invocation.
func (f *Factory) Account() (*auth.Resolved, error) {
	m, err := f.Auth()
	if err != nil {
		return nil, err
	}
	proj, err := f.Project()
	if err != nil {
		return nil, err
	}
	return m.Resolve(f.Flags.Account, proj)
}

// Printer returns the output printer configured from global flags.
func (f *Factory) Printer() (*output.Printer, error) {
	def := ""
	if cfg, err := f.Config(); err == nil {
		def, _, _ = cfg.Get("output")
	}
	p, err := output.NewPrinter(f.IO, f.Flags.Output, def)
	if err != nil {
		return nil, cmdutil.FlagErrorf("%s", err.Error())
	}
	return p, nil
}

// Confirm asks for confirmation of a destructive action. --yes skips it;
// without a TTY it fails and asks for --yes.
func (f *Factory) Confirm(question string) error {
	if f.Flags.Yes {
		return nil
	}
	if !f.IO.CanPrompt() {
		return cmdutil.NewError(cmdutil.ExitUsage, "confirmation_required", "confirmation required: "+question, "pass --yes to confirm")
	}
	ok, err := f.Prompter.Confirm(question, false)
	if err != nil {
		return err
	}
	if !ok {
		return cmdutil.ErrCancel
	}
	return nil
}

func (f *Factory) baseHTTPClient(ts httpx.TokenSource) *http.Client {
	o := httpx.Options{TokenSource: ts, UserAgent: build.UserAgent(f.AppName)}
	if f.Flags != nil && f.Flags.Debug {
		o.Debug = f.IO.ErrOut
	}
	return httpx.NewClient(o)
}

// PublicHTTPClient returns a client without credentials, for public
// resources such as the documentation site.
func (f *Factory) PublicHTTPClient() *http.Client {
	return f.baseHTTPClient(nil)
}

// HTTPClient returns an authenticated client for a product. Tokens are
// attached and refreshed transparently; products never see token strings.
// REEARTH_<PRODUCT>_TOKEN, if set, is used as a static token for that product.
func (f *Factory) HTTPClient(ctx context.Context, p Product) (*http.Client, error) {
	if tok := os.Getenv(ProductEnv(p, "TOKEN")); tok != "" {
		m, err := f.Auth()
		if err != nil {
			return nil, err
		}
		ts, _ := m.TokenSource(&auth.Resolved{Token: tok, Source: ProductEnv(p, "TOKEN")})
		return f.baseHTTPClient(ts), nil
	}
	r, err := f.Account()
	if err != nil {
		return nil, err
	}
	if r.Account.Product != "" && r.Account.Product != p.Name() {
		return nil, cmdutil.NewError(cmdutil.ExitAuth, "auth.wrong_product",
			fmt.Sprintf("account %q holds a token for %s, not %s", r.DisplayName(), r.Account.Product, p.Name()),
			fmt.Sprintf("use --account to select another account, or `%s login --with-token --product %s`", f.AppName, p.Name()))
	}
	m, _ := f.Auth()
	ts, err := m.TokenSource(r)
	if err != nil {
		return nil, err
	}
	return f.baseHTTPClient(ts), nil
}

// Env returns the auth environment of the resolved account (prod if none).
func (f *Factory) Env() (*auth.Env, error) {
	cfg, err := f.Config()
	if err != nil {
		return nil, err
	}
	name := os.Getenv("REEARTH_ENV")
	if r, err := f.Account(); err == nil {
		name = r.Account.Env
	}
	return auth.ResolveEnv(cfg, name)
}

// BaseURL resolves a product's API base URL:
// REEARTH_<PRODUCT>_BASE_URL > user env config > product default.
func (f *Factory) BaseURL(p Product) (string, error) {
	if u := os.Getenv(ProductEnv(p, "BASE_URL")); u != "" {
		return strings.TrimRight(u, "/"), nil
	}
	env, err := f.Env()
	if err != nil {
		return "", err
	}
	if u := env.ProductURLs[p.Name()]; u != "" {
		return strings.TrimRight(u, "/"), nil
	}
	if ap, ok := p.(APIProduct); ok {
		if u := ap.BaseURL(env); u != "" {
			return strings.TrimRight(u, "/"), nil
		}
	}
	return "", cmdutil.NewError(cmdutil.ExitError, "product.no_base_url",
		fmt.Sprintf("no API base URL for %s in environment %q", p.Name(), env.Name),
		"set "+ProductEnv(p, "BASE_URL"))
}

// ProjectValue returns a product-scoped value: flag value > REEARTH_<PRODUCT>_<KEY> > .reearth.yaml.
func (f *Factory) ProjectValue(p Product, key, flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if v := os.Getenv(ProductEnv(p, strings.ToUpper(key))); v != "" {
		return v
	}
	proj, _ := f.Project()
	v, _ := proj.Value(p.Name(), key)
	return v
}

// ProductEnv returns REEARTH_<PRODUCT>_<SUFFIX>.
func ProductEnv(p Product, suffix string) string {
	return "REEARTH_" + strings.ToUpper(strings.ReplaceAll(p.Name(), "-", "_")) + "_" + suffix
}

// FindProduct looks up a registered product by name.
func (f *Factory) FindProduct(name string) (Product, bool) {
	for _, p := range f.Products {
		if p.Name() == name {
			return p, true
		}
	}
	return nil, false
}
