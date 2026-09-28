package httpx

import (
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
}

func (s *fakeSource) Token() (*oauth2.Token, error) {
	return &oauth2.Token{AccessToken: s.tokens[s.i], TokenType: "Bearer"}, nil
}

func (s *fakeSource) Invalidate() {
	s.invalidated++
	s.i++
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

func TestDebugMasksAuthorization(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	var buf strings.Builder
	c := NewClient(Options{TokenSource: &fakeSource{tokens: []string{"secret-token"}}, Debug: &buf})
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if strings.Contains(buf.String(), "secret-token") || !strings.Contains(buf.String(), "Authorization: Bearer ****") {
		t.Fatalf("trace leaks or lacks auth: %s", buf.String())
	}
}
