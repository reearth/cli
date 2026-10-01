package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/gofrs/flock"
	"golang.org/x/oauth2"

	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/config"
	"github.com/reearth/cli/sdk/credstore"
	"github.com/reearth/cli/sdk/envvar"
)

// Where the resolved account came from.
const (
	SourceFlag    = "flag"
	SourceEnv     = "env"
	SourceToken   = "REEARTH_TOKEN"
	SourceProject = "project"
	SourceConfig  = "config"
)

// Manager owns accounts: resolution, credential storage and token sources.
type Manager struct {
	Config *config.Config
	// AppName is used in hints ("run: <AppName> login").
	AppName string
	// Keyring is the secure store (default: OS keyring).
	Keyring credstore.Store
	// FileStore is the opt-in insecure store.
	FileStore credstore.Store
	// LockDir holds refresh locks.
	LockDir string
	// HTTPClient is used for token endpoint calls.
	HTTPClient *http.Client

	mu      sync.Mutex
	sources map[string]*Source
}

func NewManager(cfg *config.Config, appName string) *Manager {
	return &Manager{
		Config:    cfg,
		AppName:   appName,
		Keyring:   credstore.Keyring(),
		FileStore: credstore.File(credstore.DefaultFilePath(config.Dir())),
		LockDir:   filepath.Join(config.CacheDir(), "locks"),
	}
}

// Store returns the credential store for an account.
func (m *Manager) Store(acc *config.Account) credstore.Store {
	if acc != nil && acc.InsecureStorage {
		return m.FileStore
	}
	return m.Keyring
}

var accountNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@-]{0,63}$`)

// ValidateAccountName checks that an account name is safe to use as a keyring key and file name.
func ValidateAccountName(name string) error {
	if !accountNameRe.MatchString(name) {
		return cmdutil.FlagErrorf("invalid account name %q (use letters, digits, '.', '_', '@' or '-')", name)
	}
	return nil
}

// Resolved is the account selected for the current command.
type Resolved struct {
	Name    string
	Account *config.Account
	Source  string
	// Token is set for anonymous token accounts from REEARTH_TOKEN.
	Token string
}

// DisplayName is the account name, or a description for anonymous accounts.
func (r *Resolved) DisplayName() string {
	if r.Name == "" {
		return "(" + r.Source + ")"
	}
	return r.Name
}

// Resolve picks the account: --account > REEARTH_TOKEN > REEARTH_ACCOUNT > project file > active.
func (m *Manager) Resolve(flagAccount string, project *config.Project) (*Resolved, error) {
	pick := func(name, source string) (*Resolved, error) {
		acc, ok := m.Config.Accounts[name]
		if !ok {
			return nil, cmdutil.NewError(cmdutil.ExitAuth, "auth.unknown_account",
				fmt.Sprintf("account %q not found (selected by %s)", name, source),
				fmt.Sprintf("run `%s account list` to see accounts, or `%s login %s` to add it", m.AppName, m.AppName, name))
		}
		return &Resolved{Name: name, Account: acc, Source: source}, nil
	}
	if flagAccount != "" {
		return pick(flagAccount, SourceFlag)
	}
	if tok := envvar.Get("REEARTH_TOKEN"); tok != "" {
		env := envvar.Get("REEARTH_ENV")
		if env == "" {
			env = EnvProd
		}
		return &Resolved{Account: &config.Account{Env: env, Kind: config.KindToken}, Source: SourceToken, Token: tok}, nil
	}
	if name := envvar.Get("REEARTH_ACCOUNT"); name != "" {
		return pick(name, SourceEnv)
	}
	if project != nil && project.Account != "" {
		return pick(project.Account, SourceProject)
	}
	if m.Config.Active != "" {
		return pick(m.Config.Active, SourceConfig)
	}
	return nil, cmdutil.NewError(cmdutil.ExitAuth, "auth.not_logged_in", "not logged in",
		fmt.Sprintf("run `%s login` to sign in", m.AppName))
}

// SaveLogin stores the credentials of a freshly logged-in oauth account.
func (m *Manager) SaveLogin(name string, acc *config.Account, env *Env, tok *oauth2.Token) error {
	s := &credstore.Secret{
		Kind:         config.KindOAuth,
		RefreshToken: tok.RefreshToken,
		AccessTokens: map[string]credstore.AccessToken{env.Audience: {Token: tok.AccessToken, Expiry: tok.Expiry}},
	}
	if err := m.Store(acc).Set(name, s); err != nil {
		return keyringError(err)
	}
	m.forget(name)
	return nil
}

// SaveToken stores a static token account.
func (m *Manager) SaveToken(name string, acc *config.Account, token string) error {
	if err := m.Store(acc).Set(name, &credstore.Secret{Kind: config.KindToken, Token: token}); err != nil {
		return keyringError(err)
	}
	m.forget(name)
	return nil
}

func keyringError(err error) error {
	return &cmdutil.Error{
		Exit:    cmdutil.ExitError,
		Code:    "auth.keyring_unavailable",
		Message: "could not store credentials in the OS keyring: " + err.Error(),
		Hint:    "pass --insecure-storage to store them in a plain file instead",
		Err:     err,
	}
}

func (m *Manager) forget(name string) {
	m.mu.Lock()
	delete(m.sources, name)
	m.mu.Unlock()
}

// TokenSource returns a token source for the resolved account. Access tokens
// are refreshed transparently and rotated refresh tokens are persisted.
func (m *Manager) TokenSource(r *Resolved) (*Source, error) {
	if r.Token != "" {
		return staticSource(r.Token), nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sources[r.Name]; ok {
		return s, nil
	}
	store := m.Store(r.Account)
	if r.Account.Kind == config.KindToken {
		sec, err := store.Get(r.Name)
		if err != nil {
			return nil, m.credentialsError(r.Name, err)
		}
		return staticSource(sec.Token), nil
	}
	env, err := ResolveEnv(m.Config, r.Account.Env)
	if err != nil {
		return nil, err
	}
	s := &Source{
		m:        m,
		name:     r.Name,
		env:      env,
		store:    store,
		lockPath: filepath.Join(m.LockDir, r.Name+".lock"),
	}
	if m.sources == nil {
		m.sources = map[string]*Source{}
	}
	m.sources[r.Name] = s
	return s, nil
}

func (m *Manager) credentialsError(name string, err error) error {
	if errors.Is(err, credstore.ErrNotFound) {
		return cmdutil.NewError(cmdutil.ExitAuth, "auth.reauth_required",
			fmt.Sprintf("no stored credentials for account %q", name),
			fmt.Sprintf("run `%s login %s`", m.AppName, name))
	}
	return err
}

// Source is an oauth2.TokenSource that refreshes and persists tokens.
type Source struct {
	m        *Manager
	name     string
	env      *Env
	store    credstore.Store
	lockPath string

	mu     sync.Mutex
	cur    *oauth2.Token
	static bool
	force  bool
}

func staticSource(token string) *Source {
	return &Source{static: true, cur: &oauth2.Token{AccessToken: token, TokenType: "Bearer"}}
}

// Static reports whether the source holds a non-refreshable token.
func (s *Source) Static() bool { return s.static }

func (s *Source) Token() (*oauth2.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.static {
		return s.cur, nil
	}
	if !s.force && s.cur != nil && s.cur.Valid() {
		return s.cur, nil
	}
	tok, err := s.refresh()
	if err != nil {
		return nil, err
	}
	s.cur, s.force = tok, false
	return tok, nil
}

// Invalidate forces the next Token call to refresh (used after a 401, and to
// verify a session with the server). It reports false for static tokens,
// which cannot be refreshed.
func (s *Source) Invalidate() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.static {
		return false
	}
	s.cur, s.force = nil, true
	return true
}

// refresh obtains a new access token. A file lock serializes refreshes across
// processes: with refresh token rotation, two concurrent refreshes would
// invalidate one of the rotated tokens.
func (s *Source) refresh() (*oauth2.Token, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if s.m.HTTPClient != nil {
		ctx = context.WithValue(ctx, oauth2.HTTPClient, s.m.HTTPClient)
	}

	if err := os.MkdirAll(filepath.Dir(s.lockPath), 0o700); err != nil {
		return nil, err
	}
	lock := flock.New(s.lockPath)
	if ok, err := lock.TryLockContext(ctx, 100*time.Millisecond); err != nil {
		return nil, fmt.Errorf("could not acquire token lock %s: %w", s.lockPath, err)
	} else if !ok {
		return nil, fmt.Errorf("could not acquire token lock %s", s.lockPath)
	}
	defer func() { _ = lock.Unlock() }()

	// Re-read under the lock: another process may have refreshed already.
	sec, err := s.store.Get(s.name)
	if err != nil {
		return nil, s.m.credentialsError(s.name, err)
	}
	aud := s.env.Audience
	if c, ok := sec.AccessTokens[aud]; ok && !s.force && time.Until(c.Expiry) > time.Minute {
		return &oauth2.Token{AccessToken: c.Token, TokenType: "Bearer", Expiry: c.Expiry}, nil
	}
	if sec.RefreshToken == "" {
		return nil, s.m.credentialsError(s.name, credstore.ErrNotFound)
	}

	tok, err := s.env.OAuth2Config("").TokenSource(ctx, &oauth2.Token{RefreshToken: sec.RefreshToken}).Token()
	if err != nil {
		var re *oauth2.RetrieveError
		if errors.As(err, &re) && (re.ErrorCode == "invalid_grant" || re.ErrorCode == "unauthorized_client") {
			return nil, &cmdutil.Error{
				Exit:    cmdutil.ExitAuth,
				Code:    "auth.reauth_required",
				Message: fmt.Sprintf("the session of account %q has expired or was revoked", s.name),
				Hint:    fmt.Sprintf("run `%s login %s`", s.m.AppName, s.name),
				Err:     err,
			}
		}
		return nil, fmt.Errorf("refresh token: %w", err)
	}
	if tok.RefreshToken == "" {
		tok.RefreshToken = sec.RefreshToken
	}
	rotated := tok.RefreshToken != sec.RefreshToken
	sec.RefreshToken = tok.RefreshToken
	sec.AccessTokens = map[string]credstore.AccessToken{aud: {Token: tok.AccessToken, Expiry: tok.Expiry}}
	if err := s.store.Set(s.name, sec); err != nil {
		if !rotated {
			return nil, fmt.Errorf("save refreshed credentials: %w", err)
		}
		// The server already invalidated the stored refresh token.
		return nil, &cmdutil.Error{
			Exit: cmdutil.ExitAuth,
			Code: "auth.save_failed",
			Message: fmt.Sprintf("could not store the refreshed credentials of account %q: %v; "+
				"the server has already rotated the refresh token, so the session is lost", s.name, err),
			Hint: fmt.Sprintf("run `%s login %s`", s.m.AppName, s.name),
			Err:  err,
		}
	}
	return tok, nil
}
