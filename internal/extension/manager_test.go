package extension

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/reearth/cli/internal/ghrelease"
	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/core"
	"github.com/reearth/cli/sdk/iostreams"
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

var toolAsset = AssetName("tool", runtime.GOOS, runtime.GOARCH)

// fakeGitHub serves one release of someone/reearth-tool with a binary and checksums.
func fakeGitHub(t *testing.T, bin []byte, checksum string) *httptest.Server {
	return fakeGitHubFiles(t, bin, []sumsFile{{"checksums.txt", fmt.Sprintf("%s  %s\n", checksum, toolAsset)}})
}

type sumsFile struct{ name, content string }

// fakeGitHubFiles serves one release of someone/reearth-tool with a binary and
// the given checksums files, listed as assets in this order.
func fakeGitHubFiles(t *testing.T, bin []byte, sums []sumsFile) *httptest.Server {
	byName := map[string]string{}
	for _, f := range sums {
		byName[f.name] = f.content
	}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/someone/reearth-tool/releases/latest":
			assets := []ghrelease.Asset{{Name: toolAsset, URL: srv.URL + "/dl/bin"}}
			for _, f := range sums {
				assets = append(assets, ghrelease.Asset{Name: f.name, URL: srv.URL + "/sums/" + f.name})
			}
			_ = json.NewEncoder(w).Encode(ghrelease.Release{TagName: "v1.0.0", Assets: assets})
		case r.URL.Path == "/dl/bin":
			_, _ = w.Write(bin)
		case strings.HasPrefix(r.URL.Path, "/sums/"):
			_, _ = w.Write([]byte(byName[strings.TrimPrefix(r.URL.Path, "/sums/")]))
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

func TestPlanChecksums(t *testing.T) {
	bin := []byte("binary")
	sum, _ := sha256Hex(bin)
	wrong := strings.Repeat("0", 64)
	line := func(s string) string { return fmt.Sprintf("%s  %s\n", s, toolAsset) }
	cases := []struct {
		name    string
		sums    []sumsFile
		wantErr string
	}{
		{"no checksums file", nil, ""},
		// The file without the binary comes first, so checking only the first file fails.
		{"listed in one of several", []sumsFile{{"other_checksums.txt", wrong + "  other\n"}, {"checksums.txt", line(sum)}}, ""},
		{"not listed", []sumsFile{{"checksums.txt", wrong + "  other\n"}}, "none for"},
		{"conflicting files", []sumsFile{{"checksums.txt", line(sum)}, {"x-checksums.txt", line(wrong)}}, "conflicting"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := newTestManager(t, fakeGitHubFiles(t, bin, c.sums))
			p, err := m.PlanInstall(context.Background(), "someone/reearth-tool", "")
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err = %v, want %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := sum
			if c.sums == nil {
				want = ""
			}
			if p.Checksum != want {
				t.Fatalf("checksum = %q, want %q", p.Checksum, want)
			}
		})
	}
}

func TestReservedNames(t *testing.T) {
	m := &Manager{Dir: t.TempDir(), GH: ghrelease.New("test"), Reserved: func(n string) bool { return n == "login" }}
	for _, n := range []string{"login", "help", "completion"} {
		if _, err := m.PlanInstall(context.Background(), "someone/reearth-"+n, ""); err == nil || !strings.Contains(err.Error(), "built-in") {
			t.Fatalf("%s: err = %v", n, err)
		}
	}
}

func testFactory() *core.Factory {
	ios, _, _, _ := iostreams.Test()
	return &core.Factory{AppName: "reearth", IO: ios, Flags: &core.GlobalFlags{}}
}

func TestDispatchSkipsCobraBuiltins(t *testing.T) {
	m := &Manager{Dir: t.TempDir()}
	lf := &lockFile{Version: 1, Extensions: map[string]*Entry{}}
	for n := range cobraBuiltins {
		lf.Extensions[n] = &Entry{Name: n, Path: "/nonexistent"}
	}
	if err := m.save(lf); err != nil {
		t.Fatal(err)
	}
	root := &cobra.Command{Use: "reearth"}
	for n := range cobraBuiltins {
		if handled, _ := Dispatch(m)(testFactory(), root, []string{n}); handled {
			t.Errorf("%s was dispatched to an extension", n)
		}
	}
}

func TestRemoveRequiresConfirmation(t *testing.T) {
	m := &Manager{Dir: t.TempDir()}
	if err := m.save(&lockFile{Version: 1, Extensions: map[string]*Entry{"tool": {Name: "tool"}}}); err != nil {
		t.Fatal(err)
	}
	run := func(f *core.Factory) error {
		cmd := newCmdRemove(f, m)
		cmd.SetArgs([]string{"tool"})
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		return cmd.Execute()
	}
	var e *cmdutil.Error
	if err := run(testFactory()); !errors.As(err, &e) || e.Code != "confirmation_required" {
		t.Fatalf("err = %v", err)
	}
	if _, ok := m.Get("tool"); !ok {
		t.Fatal("removed without confirmation")
	}
	f := testFactory()
	f.Flags.Yes = true
	if err := run(f); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Get("tool"); ok {
		t.Fatal("not removed with --yes")
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
