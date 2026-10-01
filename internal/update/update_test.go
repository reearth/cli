package update

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"github.com/reearth/cli/internal/ghrelease"
	"github.com/reearth/cli/sdk/build"
	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/core"
	"github.com/reearth/cli/sdk/iostreams"
	"github.com/reearth/cli/sdk/output"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"v0.2.0", "0.1.0", true},
		{"0.1.0", "v0.1.0", false},
		{"v0.1.0", "0.2.0", false},
		{"v1.0.0", "1.0.0-rc.1", true},
		{"v1.0.0", "dev", false},
	}
	for _, c := range cases {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

func TestDetectMethod(t *testing.T) {
	cases := map[string]string{
		"/opt/homebrew/Caskroom/reearth/0.1.0/reearth":                "homebrew",
		"/home/linuxbrew/.linuxbrew/Cellar/reearth/0.1.0/bin/reearth": "homebrew",
		"/Users/kana/.local/bin/reearth":                              "standalone",
		"/nix/store/abc123-reearth-0.1.0/bin/reearth":                 "nix",
	}
	if runtime.GOOS == "windows" {
		cases[`C:\Users\kana\scoop\apps\reearth\current\reearth.exe`] = "scoop"
	}
	for path, want := range cases {
		if got := DetectMethod(path); got.Name != want {
			t.Errorf("DetectMethod(%q) = %q, want %q", path, got.Name, want)
		}
	}
}

func TestArchiveName(t *testing.T) {
	if got := ArchiveName("0.2.0", "darwin", "arm64"); got != "reearth_0.2.0_darwin_arm64.tar.gz" {
		t.Error(got)
	}
	if got := ArchiveName("0.2.0", "windows", "amd64"); got != "reearth_0.2.0_windows_amd64.zip" {
		t.Error(got)
	}
}

// fakeReleases points newClient at a server whose latest release is latest
// ("" serves no releases, i.e. 404).
func fakeReleases(t *testing.T, latest string) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if latest == "" || r.URL.Path != "/repos/"+Repo+"/releases/latest" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(ghrelease.Release{TagName: latest})
	}))
	t.Cleanup(srv.Close)
	orig := newClient
	newClient = func(ua string) *ghrelease.Client {
		c := orig(ua)
		c.APIBase = srv.URL
		return c
	}
	t.Cleanup(func() { newClient = orig })
}

func setVersion(t *testing.T, v string) {
	orig := build.Version
	build.Version = v
	t.Cleanup(func() { build.Version = orig })
}

func TestUpgradeRefusesDevBuild(t *testing.T) {
	fakeReleases(t, "v0.2.0")
	setVersion(t, "dev")
	ios, _, _, _ := iostreams.Test()
	f := &core.Factory{AppName: "reearth", IO: ios, Flags: &core.GlobalFlags{}}
	cmd := NewCmdUpgrade(f)
	cmd.SetArgs([]string{})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	var e *cmdutil.Error
	if err := cmd.Execute(); !errors.As(err, &e) || e.Code != "upgrade.dev_build" {
		t.Fatalf("err = %v", err)
	}
}

func TestCheckRecordsFailure(t *testing.T) {
	t.Setenv("REEARTH_CACHE_DIR", t.TempDir())
	fakeReleases(t, "")
	check(context.Background(), newClient("test"), nil)
	s, err := readState()
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(s.CheckedAt) > time.Minute || s.Latest != "" {
		t.Fatalf("state = %+v", s)
	}

	// A failure keeps the release found by an earlier check.
	check(context.Background(), newClient("test"), &state{Latest: "v0.1.0"})
	if s, _ := readState(); s.Latest != "v0.1.0" {
		t.Fatalf("state = %+v", s)
	}

	fakeReleases(t, "v0.2.0")
	check(context.Background(), newClient("test"), nil)
	if s, _ := readState(); s.Latest != "v0.2.0" {
		t.Fatalf("state = %+v", s)
	}
}

func TestCheckRecordsTimeBeforeRequest(t *testing.T) {
	t.Setenv("REEARTH_CACHE_DIR", t.TempDir())
	started, release := make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	gh := newClient("test")
	gh.APIBase = srv.URL
	go check(context.Background(), gh, &state{Latest: "v0.1.0"})
	<-started
	// The process may exit now; the attempt must already be recorded.
	s, err := readState()
	if err != nil || time.Since(s.CheckedAt) > time.Minute || s.Latest != "v0.1.0" {
		t.Fatalf("state = %+v, %v", s, err)
	}
}

func TestMachineArgs(t *testing.T) {
	for _, a := range []string{"--json", "--json=name", "--jq=.x", "--jq", "-o", "-ojson", "-oJSON", "-o=json", "--output=json", "--output", "-q", "--quiet", "-yojson", "-qy", "-yq", "-yo=json"} {
		if !machineArgs([]string{"account", "list", a}) {
			t.Errorf("machineArgs(%q) = false", a)
		}
	}
	for _, a := range []string{"--yes", "--jsonx", "--outputs", "-y", "--debug", "-xo", "-yx"} {
		if machineArgs([]string{"account", "list", a}) {
			t.Errorf("machineArgs(%q) = true", a)
		}
	}
}

func TestNotifySkipsParsedMachineOutput(t *testing.T) {
	t.Setenv("REEARTH_CACHE_DIR", t.TempDir())
	setVersion(t, "0.1.0")
	if err := writeState(&state{CheckedAt: time.Now(), Latest: "v0.2.0"}); err != nil {
		t.Fatal(err)
	}
	for _, flags := range []core.GlobalFlags{{}, {Output: output.Options{Output: "json"}}, {Quiet: true}} {
		ios, _, _, stderr := iostreams.Test()
		n := &Notifier{done: make(chan struct{})}
		close(n.done)
		n.Notify(&core.Factory{AppName: "reearth", IO: ios, Flags: &flags})
		if got, want := stderr.Len() > 0, flags == (core.GlobalFlags{}); got != want {
			t.Errorf("flags %+v: notice printed = %v", flags, got)
		}
	}
}
