package update

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/mod/semver"

	"github.com/reearth/cli/internal/ghrelease"
	"github.com/reearth/cli/sdk/build"
	"github.com/reearth/cli/sdk/config"
	"github.com/reearth/cli/sdk/core"
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

// machineArgs are flags after which stderr is probably parsed too.
var machineArgs = []string{"--json", "--jq", "-o", "--output", "--quiet", "-q"}

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
		rel, err := ghrelease.New(build.UserAgent(f.AppName)).Latest(ctx, Repo)
		if err != nil {
			return
		}
		_ = writeState(&state{CheckedAt: time.Now(), Latest: rel.TagName, URL: rel.HTMLURL})
	}()
	return n
}

func enabled(f *core.Factory, args []string) bool {
	if build.IsDev() || !f.IO.IsInteractive() || os.Getenv("REEARTH_NO_UPDATE_NOTIFIER") != "" {
		return false
	}
	if len(args) > 0 && (args[0] == "upgrade" || args[0] == "__complete" || args[0] == "completion") {
		return false
	}
	for _, a := range args {
		for _, m := range machineArgs {
			if a == m || strings.HasPrefix(a, m+"=") {
				return false
			}
		}
	}
	if cfg, err := f.Config(); err == nil {
		if v, _, _ := cfg.Get("update_check"); v == "disabled" {
			return false
		}
	}
	return true
}

// Notify prints the notice if a newer version is known. It waits briefly for
// an in-flight check so that the first run after the interval can report.
func (n *Notifier) Notify(f *core.Factory) {
	if n == nil {
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
