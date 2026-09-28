package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"time"

	"golang.org/x/oauth2"

	"github.com/reearth/cli/sdk/iostreams"
)

type LoginOptions struct {
	Env         *Env
	Flow        Flow
	IO          *iostreams.IOStreams
	OpenBrowser func(url string) error
	// HTTPClient is used for token requests (optional).
	HTTPClient *http.Client
	// Timeout bounds the whole interactive login (default 10 minutes).
	Timeout time.Duration
}

// Login runs the selected flow and returns a token that includes a refresh token.
func Login(ctx context.Context, o LoginOptions) (*oauth2.Token, error) {
	if o.Timeout == 0 {
		o.Timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	if o.HTTPClient != nil {
		ctx = context.WithValue(ctx, oauth2.HTTPClient, o.HTTPClient)
	}

	var tok *oauth2.Token
	var err error
	if o.Flow == FlowLoopback {
		tok, err = loopbackLogin(ctx, o)
		if errors.Is(err, errLoopbackUnavailable) {
			o.IO.Warn("Could not start a local callback server; falling back to device login")
			tok, err = deviceLogin(ctx, o)
		}
	} else {
		tok, err = deviceLogin(ctx, o)
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, errors.New("login timed out")
		}
		return nil, err
	}
	if tok.RefreshToken == "" {
		return nil, errors.New("the authorization server did not return a refresh token (is offline_access allowed for this application?)")
	}
	return tok, nil
}

func audienceOpts(env *Env) []oauth2.AuthCodeOption {
	if env.Audience == "" {
		return nil
	}
	return []oauth2.AuthCodeOption{oauth2.SetAuthURLParam("audience", env.Audience)}
}

var errLoopbackUnavailable = errors.New("loopback unavailable")

type callbackResult struct {
	code string
	err  error
}

// loopbackLogin implements the authorization code flow with PKCE (S256) and a
// loopback redirect (RFC 8252 §7.3). PKCE is always sent.
func loopbackLogin(ctx context.Context, o LoginOptions) (*oauth2.Token, error) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errLoopbackUnavailable, err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	conf := o.Env.OAuth2Config(fmt.Sprintf("http://127.0.0.1:%d/callback", port))

	verifier := oauth2.GenerateVerifier()
	state := randomString()
	opts := append([]oauth2.AuthCodeOption{oauth2.S256ChallengeOption(verifier)}, audienceOpts(o.Env)...)
	authURL := conf.AuthCodeURL(state, opts...)

	ch := make(chan callbackResult, 1)
	srv := &http.Server{
		ReadHeaderTimeout: 10 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/callback" {
				http.NotFound(w, r)
				return
			}
			q := r.URL.Query()
			var res callbackResult
			switch {
			case q.Get("error") != "":
				res.err = fmt.Errorf("authorization failed: %s %s", q.Get("error"), q.Get("error_description"))
			case q.Get("state") != state:
				res.err = errors.New("authorization failed: state mismatch")
			case q.Get("code") == "":
				res.err = errors.New("authorization failed: no code in callback")
			default:
				res.code = q.Get("code")
			}
			writeCallbackPage(w, res.err)
			select {
			case ch <- res:
			default:
			}
		}),
	}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()

	o.IO.Info("Opening your browser to sign in…")
	if err := o.OpenBrowser(authURL); err != nil {
		o.IO.Warn("Could not open a browser: %v", err)
		o.IO.Println("Open this URL to continue:")
		o.IO.Println(authURL)
	} else {
		o.IO.Hint("If nothing happened, open this URL: %s", authURL)
	}
	o.IO.StartProgress("Waiting for you to finish in the browser…")
	defer o.IO.StopProgress()

	var res callbackResult
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res = <-ch:
	}
	if res.err != nil {
		return nil, res.err
	}
	o.IO.StartProgress("Signing in…")
	return conf.Exchange(ctx, res.code, oauth2.VerifierOption(verifier))
}

// deviceLogin implements the device authorization grant (RFC 8628).
func deviceLogin(ctx context.Context, o LoginOptions) (*oauth2.Token, error) {
	conf := o.Env.OAuth2Config("")
	da, err := conf.DeviceAuth(ctx, audienceOpts(o.Env)...)
	if err != nil {
		return nil, fmt.Errorf("start device login: %w", err)
	}
	cs := o.IO.ErrColor()
	link := da.VerificationURIComplete
	if link == "" {
		link = da.VerificationURI
	}
	o.IO.Info("To sign in, open %s", cs.Bold(link))
	o.IO.Println("  " + cs.Dim("and check that it shows the code") + "  " + cs.Accent(cs.Bold(da.UserCode)))
	o.IO.Newline()
	o.IO.StartProgress("Waiting for authorization…")
	defer o.IO.StopProgress()
	return conf.DeviceAccessToken(ctx, da)
}

func randomString() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func writeCallbackPage(w http.ResponseWriter, err error) {
	title, body := "Signed in", "You're signed in to the Re:Earth CLI. You can close this tab and return to your terminal."
	status := http.StatusOK
	if err != nil {
		title, body, status = "Sign-in failed", err.Error(), http.StatusBadRequest
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8"><title>%[1]s · Re:Earth CLI</title>
<style>body{font:15px/1.6 system-ui,sans-serif;display:grid;place-items:center;min-height:100vh;margin:0;background:#fafaf9;color:#1c1917}
main{max-width:28rem;padding:2rem}h1{font-size:1.25rem;margin:0 0 .5rem}p{margin:0;color:#57534e}
@media(prefers-color-scheme:dark){body{background:#0c0a09;color:#fafaf9}p{color:#a8a29e}}</style></head>
<body><main><h1>%[1]s</h1><p>%[2]s</p></main></body></html>`, html.EscapeString(title), html.EscapeString(body))
}
