package extension

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/reearth/cli/internal/ghrelease"
)

func TestParseRepo(t *testing.T) {
	ok := map[string]string{
		"reearth/reearth-example":                   "example",
		"someone/reearth-my-tool":                   "my-tool",
		"https://github.com/someone/reearth-tool2/": "tool2",
	}
	for in, want := range ok {
		_, _, name, err := ParseRepo(in)
		if err != nil || name != want {
			t.Errorf("ParseRepo(%q) = %q, %v", in, name, err)
		}
	}
	for _, bad := range []string{"reearth-x", "a/b/c", "someone/tool", "someone/reearth-", "someone/reearth-UPPER", "someone/reearth-../x"} {
		if _, _, _, err := ParseRepo(bad); err == nil {
			t.Errorf("ParseRepo(%q) should fail", bad)
		}
	}
}

// fakeGitHub serves one release of someone/reearth-tool with a binary and checksums.
func fakeGitHub(t *testing.T, bin []byte, checksum string) *httptest.Server {
	asset := AssetName("tool", runtime.GOOS, runtime.GOARCH)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/someone/reearth-tool/releases/latest":
			_ = json.NewEncoder(w).Encode(ghrelease.Release{TagName: "v1.0.0", Assets: []ghrelease.Asset{
				{Name: asset, URL: srv.URL + "/dl/bin"},
				{Name: "checksums.txt", URL: srv.URL + "/dl/sums"},
			}})
		case "/dl/bin":
			_, _ = w.Write(bin)
		case "/dl/sums":
			_, _ = fmt.Fprintf(w, "%s  %s\n", checksum, asset)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newTestManager(t *testing.T, srv *httptest.Server) *Manager {
	gh := ghrelease.New("test")
	gh.APIBase = srv.URL
	return &Manager{Dir: t.TempDir(), GH: gh, Reserved: func(n string) bool { return n == "login" }}
}

func TestInstallVerifyAndTamperDetection(t *testing.T) {
	bin := []byte("#!/bin/sh\necho hi\n")
	sum, _ := sha256Hex(bin)
	srv := fakeGitHub(t, bin, sum)
	m := newTestManager(t, srv)

	p, err := m.PlanInstall(context.Background(), "someone/reearth-tool", "")
	if err != nil {
		t.Fatal(err)
	}
	if p.Official || p.Checksum != sum {
		t.Fatalf("plan = %+v", p)
	}
	e, err := m.Install(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Command(e, nil, "/bin/reearth"); err != nil {
		t.Fatalf("fresh install should verify: %v", err)
	}
	if err := os.WriteFile(e.Path, []byte("evil"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Command(e, nil, "/bin/reearth"); !errors.Is(err, ErrTampered) {
		t.Fatalf("tampering not detected: %v", err)
	}
}

func TestInstallRejectsChecksumMismatch(t *testing.T) {
	srv := fakeGitHub(t, []byte("binary"), strings.Repeat("0", 64))
	m := newTestManager(t, srv)
	p, err := m.PlanInstall(context.Background(), "someone/reearth-tool", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Install(context.Background(), p); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v", err)
	}
	if list, _ := m.List(); len(list) != 0 {
		t.Fatal("failed install was recorded")
	}
}

func TestReservedNames(t *testing.T) {
	m := &Manager{Dir: t.TempDir(), GH: ghrelease.New("test"), Reserved: func(n string) bool { return n == "login" }}
	if _, err := m.PlanInstall(context.Background(), "someone/reearth-login", ""); err == nil || !strings.Contains(err.Error(), "built-in") {
		t.Fatalf("err = %v", err)
	}
}

func sha256Hex(b []byte) (string, error) {
	f, err := os.CreateTemp("", "sum")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	_, _ = f.Write(b)
	_ = f.Close()
	return ghrelease.FileSHA256(f.Name())
}
