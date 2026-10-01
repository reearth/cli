package update

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/mod/semver"

	"github.com/reearth/cli/internal/ghrelease"
	"github.com/reearth/cli/sdk/app"
	"github.com/reearth/cli/sdk/build"
	"github.com/reearth/cli/sdk/config"
	"github.com/reearth/cli/sdk/core"
	"github.com/reearth/cli/sdk/envvar"
)

const checkInterval = 24 * time.Hour

type state struct {
	CheckedAt time.Time `json:"checked_at"`
	Latest    string    `json:"latest"`
	URL       string    `json:"url,omitempty"`
}

func statePath() string { return filepath.Join(config.CacheDir(), "update.json") }

func readState() (*state, error) {
	b, err := os.ReadFile(statePath())
	if err != nil {
		return nil, err
	}
	var s state
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func writeState(s *state) error {
	if err := os.MkdirAll(filepath.Dir(statePath()), 0o700); err != nil {
		return err
	}
	b, _ := json.Marshal(s)
	return os.WriteFile(statePath(), b, 0o600)
}

// Notifier checks for a newer release in the background and prints a
// one-line notice after the command finishes.
type Notifier struct {
	done chan struct{}
}

// machineFlags are flags after which stderr is probably parsed too, as
// long and short names.
var machineFlags = [][2]string{{"json", ""}, {"jq", ""}, {"output", "o"}, {"quiet", "q"}}

// Start begins a background check if notifications are appropriate.
func Start(f *core.Factory, args []string) *Notifier {
	if !enabled(f, args) {
		return nil
	}
	n := &Notifier{done: make(chan struct{})}
	s, err := readState()
	if err == nil && time.Since(s.CheckedAt) < checkInterval {
		close(n.done)
		return n
	}
	go func() {
		defer close(n.done)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		check(ctx, newClient(build.UserAgent(f.AppName)), s)
	}()
	return n
}

// check records the latest release. The time is recorded before the request,
// keeping any previously known release, so that a failed check (no releases
// yet, no network) or one killed when the process exits is not retried on
// every run.
func check(ctx context.Context, gh *ghrelease.Client, prev *state) {
	s := &state{}
	if prev != nil {
		*s = *prev
	}
	s.CheckedAt = time.Now()
	_ = writeState(s)
	if rel, err := gh.Latest(ctx, Repo); err == nil {
		s.Latest, s.URL = rel.TagName, rel.HTMLURL
		_ = writeState(s)
	}
}

func enabled(f *core.Factory, args []string) bool {
	if build.IsDev() || !f.IO.IsInteractive() || envvar.True("REEARTH_NO_UPDATE_NOTIFIER") {
		return false
	}
	if len(args) > 0 && (args[0] == "upgrade" || args[0] == "__complete" || args[0] == "completion") {
		return false
	}
	if machineArgs(args) {
		return false
	}
	if v, _ := f.Setting("update_check"); v == "disabled" {
		return false
	}
	return true
}

// machineArgs reports whether raw args request machine output or quiet mode
// in any spelling (--json, --json=f, --output=json, -o json, -ojson, -o=json,
// -q, -yojson). Parsing may have failed, so the parsed flags cannot be relied
// on alone.
func machineArgs(args []string) bool {
	for _, m := range machineFlags {
		var short []string
		if m[1] != "" {
			short = []string{m[1]}
		}
		if _, ok := app.RawFlag(args, m[0], short...); ok {
			return true
		}
	}
	return false
}

// Notify prints the notice if a newer version is known. It waits briefly for
// an in-flight check so that the first run after the interval can report.
func (n *Notifier) Notify(f *core.Factory) {
	if n == nil {
		return
	}
	// The parsed flags also catch combined shorthands such as -yq.
	if g := f.Flags; g != nil && (g.Output.JSON != "" || g.Output.JQ != "" || g.Output.Output != "" || g.Quiet) {
		return
	}
	select {
	case <-n.done:
	case <-time.After(time.Second):
		return
	}
	s, err := readState()
	if err != nil || !Newer(s.Latest, build.Version) {
		return
	}
	exe, _ := Executable()
	cmd := f.AppName + " upgrade"
	if m := DetectMethod(exe); !m.Self {
		cmd = m.Command
	}
	cs := f.IO.ErrColor()
	_, _ = fmt.Fprintf(f.IO.ErrOut, "\n  %s %s %s %s  %s\n",
		cs.Accent("Update available"),
		cs.Dim(normalize(build.Version)), cs.Dim("→"), cs.Bold(normalize(s.Latest)),
		cs.Dim("Run `"+cmd+"`"))
}

// Newer reports whether version a is newer than b (with or without "v").
func Newer(a, b string) bool {
	va, vb := "v"+normalize(a), "v"+normalize(b)
	if !semver.IsValid(va) || !semver.IsValid(vb) {
		return false
	}
	return semver.Compare(va, vb) > 0
}

func normalize(v string) string {
	if len(v) > 0 && v[0] == 'v' {
		return v[1:]
	}
	return v
}
