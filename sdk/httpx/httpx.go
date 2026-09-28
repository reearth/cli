// Package httpx builds HTTP clients with authentication, retries, a
// User-Agent and optional debug tracing.
package httpx

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

// TokenSource is an oauth2.TokenSource that can be told its token was rejected.
type TokenSource interface {
	oauth2.TokenSource
	Invalidate()
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
	return &http.Client{Transport: rt, Timeout: 5 * time.Minute}
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
// if the request body can be replayed.
type authTransport struct {
	next http.RoundTripper
	ts   TokenSource
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.do(req)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		return resp, err
	}
	if req.Body != nil && req.GetBody == nil {
		return resp, nil
	}
	_ = resp.Body.Close()
	t.ts.Invalidate()
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
		return nil, err
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
		if attempt >= t.max || !idempotent(req.Method) || (req.Body != nil && req.GetBody == nil) {
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

var sensitiveHeaders = map[string]bool{"Authorization": true, "Cookie": true, "Set-Cookie": true, "X-Api-Key": true}

func (t *debugTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	_, _ = fmt.Fprintf(t.w, "> %s %s\n", req.Method, req.URL.Redacted())
	for k, vs := range req.Header {
		for _, v := range vs {
			if sensitiveHeaders[k] {
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
			if sensitiveHeaders[k] {
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
