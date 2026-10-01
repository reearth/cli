package httpx

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"golang.org/x/oauth2"
)

type fakeSource struct {
	tokens      []string
	i           int
	invalidated int
	static      bool
	err         error
	calls       int
}

func (s *fakeSource) Token() (*oauth2.Token, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return &oauth2.Token{AccessToken: s.tokens[s.i], TokenType: "Bearer"}, nil
}

func (s *fakeSource) Invalidate() bool {
	if s.static {
		return false
	}
	s.invalidated++
	s.i++
	return true
}

func TestRetriesOnceWith401AfterInvalidate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fresh" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if !strings.HasPrefix(r.Header.Get("User-Agent"), "reearth/") {
			t.Errorf("UA = %q", r.Header.Get("User-Agent"))
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	ts := &fakeSource{tokens: []string{"stale", "fresh"}}
	c := NewClient(Options{TokenSource: ts, UserAgent: "reearth/test"})
	resp, err := c.Post(srv.URL, "application/json", strings.NewReader(`{"a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || ts.invalidated != 1 {
		t.Fatalf("status %d, invalidated %d", resp.StatusCode, ts.invalidated)
	}
}

func TestRetriesIdempotentOn5xx(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) < 2 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	resp, err := NewClient(Options{}).Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || n.Load() != 2 {
		t.Fatalf("status %d after %d attempts", resp.StatusCode, n.Load())
	}
}

func TestStaticTokenIsNotResentOn401(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	c := NewClient(Options{TokenSource: &fakeSource{tokens: []string{"static"}, static: true}})
	resp, err := c.Post(srv.URL, "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || n.Load() != 1 {
		t.Fatalf("status %d after %d requests", resp.StatusCode, n.Load())
	}
}

func TestTokenErrorsAreNotRetried(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	want := errors.New("reauth required")
	ts := &fakeSource{err: want}
	resp, err := NewClient(Options{TokenSource: ts}).Get(srv.URL)
	if err == nil {
		_ = resp.Body.Close()
	}
	if !errors.Is(err, want) || ts.calls != 1 {
		t.Fatalf("err = %v after %d token calls", err, ts.calls)
	}
}

func TestRefusesCrossOriginRedirectWithToken(t *testing.T) {
	var leaked atomic.Value
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked.Store(r.Header.Get("Authorization"))
	}))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/same" {
			http.Redirect(w, r, "/ok", http.StatusFound)
			return
		}
		if r.URL.Path == "/ok" {
			return
		}
		http.Redirect(w, r, other.URL+"/x", http.StatusFound)
	}))
	defer srv.Close()

	c := NewClient(Options{TokenSource: &fakeSource{tokens: []string{"secret"}}})
	resp, err := c.Get(srv.URL + "/same")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	resp, err = c.Get(srv.URL + "/away")
	if err == nil {
		_ = resp.Body.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "refusing to follow a redirect") {
		t.Fatalf("err = %v", err)
	}
	if v := leaked.Load(); v != nil {
		t.Fatalf("other host received %q", v)
	}
}

func TestDebugMasksSecrets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Session-Token", "resp-secret")
	}))
	defer srv.Close()
	var buf strings.Builder
	c := NewClient(Options{TokenSource: &fakeSource{tokens: []string{"secret-token"}}, Debug: &buf})
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"?access_token=q-secret&page=2", nil)
	req.Header.Set("Proxy-Authorization", "Basic proxy-secret")
	req.Header.Set("X-Client-Secret", "custom-secret")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	out := buf.String()
	for _, leak := range []string{"secret-token", "q-secret", "proxy-secret", "custom-secret", "resp-secret"} {
		if strings.Contains(out, leak) {
			t.Errorf("trace leaks %s: %s", leak, out)
		}
	}
	if !strings.Contains(out, "Authorization: Bearer ****") || !strings.Contains(out, "page=2") {
		t.Fatalf("trace lacks auth or query: %s", out)
	}
}
