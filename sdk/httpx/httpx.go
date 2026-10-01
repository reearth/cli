// Package httpx builds HTTP clients with authentication, retries, a
// User-Agent and optional debug tracing.
package httpx

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

// TokenSource is an oauth2.TokenSource that can be told its token was rejected.
type TokenSource interface {
	oauth2.TokenSource
	// Invalidate discards the current token and reports whether the next
	// Token call can return a different one (false for static tokens).
	Invalidate() bool
}

type Options struct {
	// TokenSource authenticates requests (optional).
	TokenSource TokenSource
	UserAgent   string
	// Debug receives a request/response trace when non-nil.
	Debug io.Writer
	// MaxRetries for idempotent requests on 429/5xx (default 2).
	MaxRetries int
	Base       http.RoundTripper
}

func NewClient(o Options) *http.Client {
	base := o.Base
	if base == nil {
		base = http.DefaultTransport
	}
	rt := base
	if o.Debug != nil {
		rt = &debugTransport{next: rt, w: o.Debug}
	}
	if o.TokenSource != nil {
		rt = &authTransport{next: rt, ts: o.TokenSource}
	}
	retries := o.MaxRetries
	if retries == 0 {
		retries = 2
	}
	rt = &retryTransport{next: rt, max: retries}
	if o.UserAgent != "" {
		rt = &uaTransport{next: rt, ua: o.UserAgent}
	}
	c := &http.Client{Transport: rt, Timeout: 5 * time.Minute}
	if o.TokenSource != nil {
		c.CheckRedirect = sameOriginRedirect
	}
	return c
}

// sameOriginRedirect refuses redirects to another origin. The auth transport
// sets the token on every hop, so following one would leak the token.
func sameOriginRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	orig := via[0].URL
	if req.URL.Scheme != orig.Scheme || !strings.EqualFold(req.URL.Host, orig.Host) {
		return fmt.Errorf("refusing to follow a redirect to %s://%s: credentials are only sent to %s://%s", req.URL.Scheme, req.URL.Host, orig.Scheme, orig.Host)
	}
	return nil
}

type uaTransport struct {
	next http.RoundTripper
	ua   string
}

func (t *uaTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get("User-Agent") == "" {
		req = req.Clone(req.Context())
		req.Header.Set("User-Agent", t.ua)
	}
	return t.next.RoundTrip(req)
}

// authTransport sets the bearer token and, on 401, refreshes once and retries
// if the token source can refresh and the request body can be replayed.
type authTransport struct {
	next http.RoundTripper
	ts   TokenSource
}

// tokenError is a failure to obtain a token. It is never retried.
type tokenError struct{ err error }

func (e *tokenError) Error() string { return e.err.Error() }
func (e *tokenError) Unwrap() error { return e.err }

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.do(req)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		return resp, err
	}
	if req.Body != nil && req.GetBody == nil {
		return resp, nil
	}
	if !t.ts.Invalidate() {
		return resp, nil
	}
	_ = resp.Body.Close()
	retry := req.Clone(req.Context())
	if req.GetBody != nil {
		if retry.Body, err = req.GetBody(); err != nil {
			return nil, err
		}
	}
	return t.do(retry)
}

func (t *authTransport) do(req *http.Request) (*http.Response, error) {
	tok, err := t.ts.Token()
	if err != nil {
		return nil, &tokenError{err}
	}
	r := req.Clone(req.Context())
	tok.SetAuthHeader(r)
	return t.next.RoundTrip(r)
}

type retryTransport struct {
	next http.RoundTripper
	max  int
}

func idempotent(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPut, http.MethodDelete:
		return true
	}
	return false
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		resp, err := t.next.RoundTrip(req)
		var te *tokenError
		if attempt >= t.max || !idempotent(req.Method) || (req.Body != nil && req.GetBody == nil) || errors.As(err, &te) {
			return resp, err
		}
		if err == nil && resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
			return resp, nil
		}
		if err != nil && req.Context().Err() != nil {
			return nil, err
		}
		wait := time.Duration(1<<attempt) * 500 * time.Millisecond
		if err == nil {
			if s, perr := strconv.Atoi(resp.Header.Get("Retry-After")); perr == nil && s > 0 && s <= 30 {
				wait = time.Duration(s) * time.Second
			}
			_ = resp.Body.Close()
		}
		select {
		case <-req.Context().Done():
			return nil, req.Context().Err()
		case <-time.After(wait):
		}
		if req.GetBody != nil {
			req = req.Clone(req.Context())
			if req.Body, err = req.GetBody(); err != nil {
				return nil, err
			}
		}
	}
}

type debugTransport struct {
	next http.RoundTripper
	w    io.Writer
}

// sensitive reports whether a header or query parameter name looks like it carries a secret.
func sensitive(name string) bool {
	name = strings.ToLower(name)
	for _, s := range []string{"token", "secret", "key", "auth", "password", "cookie"} {
		if strings.Contains(name, s) {
			return true
		}
	}
	return false
}

func redactURL(u *url.URL) string {
	q := u.Query()
	masked := false
	for k := range q {
		if sensitive(k) {
			q[k] = []string{"****"}
			masked = true
		}
	}
	if masked {
		cp := *u
		cp.RawQuery = q.Encode()
		u = &cp
	}
	return u.Redacted()
}

func (t *debugTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	_, _ = fmt.Fprintf(t.w, "> %s %s\n", req.Method, redactURL(req.URL))
	for k, vs := range req.Header {
		for _, v := range vs {
			if sensitive(k) {
				v = maskValue(v)
			}
			_, _ = fmt.Fprintf(t.w, "> %s: %s\n", k, v)
		}
	}
	resp, err := t.next.RoundTrip(req)
	if err != nil {
		_, _ = fmt.Fprintf(t.w, "< error: %v (%s)\n", err, time.Since(start).Round(time.Millisecond))
		return nil, err
	}
	_, _ = fmt.Fprintf(t.w, "< %s (%s)\n", resp.Status, time.Since(start).Round(time.Millisecond))
	for k, vs := range resp.Header {
		for _, v := range vs {
			if sensitive(k) {
				v = maskValue(v)
			}
			_, _ = fmt.Fprintf(t.w, "< %s: %s\n", k, v)
		}
	}
	return resp, nil
}

func maskValue(v string) string {
	if scheme, _, ok := strings.Cut(v, " "); ok {
		return scheme + " ****"
	}
	return "****"
}
