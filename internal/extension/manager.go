// Package extension manages third-party commands installed from GitHub
// releases (like `gh extension`).
//
// Unlike cargo/git, binaries named reearth-* on PATH are never executed
// implicitly. Only extensions installed through this manager run, and their
// SHA-256 is pinned in a lock file and re-verified before every run.
//
// Extensions are not sandboxed. They run with the user's own access, like any
// process the user starts, and inherit environment variables, including
// REEARTH_TOKEN. The CLI does not hand them the credentials it stores (keyring
// or file); an extension that needs the API calls `$REEARTH_BIN api ...`.
// REEARTH_EXTENSION=1 is how the CLI recognises a call from an extension. None
// of this stops a malicious extension, which could read the keyring itself:
// users are protected by installing only extensions they trust.
package extension

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/reearth/cli/internal/ghrelease"
	"github.com/reearth/cli/sdk/config"
)

// OfficialOwner is the GitHub owner whose extensions are marked official.
const OfficialOwner = "reearth"

const repoPrefix = "reearth-"

var (
	nameRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
	ownerRe = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)
)

type Entry struct {
	Name     string `json:"name"`
	Repo     string `json:"repo"`
	Version  string `json:"version"`
	Path     string `json:"path"`
	SHA256   string `json:"sha256"`
	Official bool   `json:"official"`
	// ChecksumsVerified means the release published a checksums file that matched.
	ChecksumsVerified bool      `json:"checksums_verified"`
	InstalledAt       time.Time `json:"installed_at"`
}

type lockFile struct {
	Version    int               `json:"version"`
	Extensions map[string]*Entry `json:"extensions"`
}

type Manager struct {
	Dir string
	GH  *ghrelease.Client
	// Reserved reports names that extensions may not take (built-in commands).
	Reserved func(name string) bool
}

func NewManager(userAgent string, reserved func(string) bool) *Manager {
	return &Manager{
		Dir:      filepath.Join(config.DataDir(), "extensions"),
		GH:       ghrelease.New(userAgent),
		Reserved: reserved,
	}
}

func (m *Manager) lockPath() string { return filepath.Join(m.Dir, "extensions.lock") }

func (m *Manager) load() (*lockFile, error) {
	lf := &lockFile{Version: 1, Extensions: map[string]*Entry{}}
	b, err := os.ReadFile(m.lockPath())
	if errors.Is(err, os.ErrNotExist) {
		return lf, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, lf); err != nil {
		return nil, fmt.Errorf("parse %s: %w", m.lockPath(), err)
	}
	if lf.Extensions == nil {
		lf.Extensions = map[string]*Entry{}
	}
	return lf, nil
}

func (m *Manager) save(lf *lockFile) error {
	if err := os.MkdirAll(m.Dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(lf, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.lockPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, m.lockPath())
}

func (m *Manager) List() ([]*Entry, error) {
	lf, err := m.load()
	if err != nil {
		return nil, err
	}
	out := make([]*Entry, 0, len(lf.Extensions))
	for _, e := range lf.Extensions {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *Manager) Get(name string) (*Entry, bool) {
	lf, err := m.load()
	if err != nil {
		return nil, false
	}
	e, ok := lf.Extensions[name]
	return e, ok
}

// ParseRepo validates "owner/reearth-<name>" and returns the extension name.
func ParseRepo(s string) (owner, repo, name string, err error) {
	owner, repo, ok := strings.Cut(strings.TrimSuffix(strings.TrimPrefix(s, "https://github.com/"), "/"), "/")
	if !ok || !ownerRe.MatchString(owner) || strings.Contains(repo, "/") {
		return "", "", "", fmt.Errorf("invalid repository %q (want owner/%s<name>)", s, repoPrefix)
	}
	name, ok = strings.CutPrefix(repo, repoPrefix)
	if !ok || !nameRe.MatchString(name) {
		return "", "", "", fmt.Errorf("extension repositories must be named %s<name> with a lowercase name, got %q", repoPrefix, repo)
	}
	return owner, repo, name, nil
}

// Plan describes what an install would do, for confirmation.
type Plan struct {
	Name     string
	Repo     string
	Tag      string
	Official bool
	Asset    *ghrelease.Asset
	// Checksum is the expected SHA-256 from the release's checksums files;
	// empty only when the release publishes none.
	Checksum string
	URL      string
}

// AssetName is the binary asset an extension release must provide.
func AssetName(name, goos, goarch string) string {
	s := fmt.Sprintf("%s%s-%s-%s", repoPrefix, name, goos, goarch)
	if goos == "windows" {
		s += ".exe"
	}
	return s
}

// PlanInstall resolves the release and asset for repoArg at tag (latest if empty).
func (m *Manager) PlanInstall(ctx context.Context, repoArg, tag string) (*Plan, error) {
	owner, repo, name, err := ParseRepo(repoArg)
	if err != nil {
		return nil, err
	}
	if m.reserved(name) {
		return nil, fmt.Errorf("%q is a built-in command and cannot be used as an extension name", name)
	}
	full := owner + "/" + repo
	var rel *ghrelease.Release
	if tag != "" {
		rel, err = m.GH.Tag(ctx, full, tag)
	} else {
		rel, err = m.GH.Latest(ctx, full)
	}
	if errors.Is(err, ghrelease.ErrNotFound) {
		return nil, fmt.Errorf("no release found in %s", full)
	}
	if err != nil {
		return nil, err
	}
	an := AssetName(name, runtime.GOOS, runtime.GOARCH)
	asset, ok := rel.Asset(an)
	if !ok {
		return nil, fmt.Errorf("%s %s has no binary for %s/%s (expected asset %s)", full, rel.TagName, runtime.GOOS, runtime.GOARCH, an)
	}
	p := &Plan{Name: name, Repo: full, Tag: rel.TagName, Official: strings.EqualFold(owner, OfficialOwner), Asset: asset, URL: rel.HTMLURL}
	// Once a release publishes checksums, the asset must be listed, and every
	// file that lists it must agree. Only a release without any checksums file
	// installs unverified (after confirmation).
	published := false
	for i := range rel.Assets {
		a := &rel.Assets[i]
		if a.Name != "checksums.txt" && !strings.HasSuffix(a.Name, "_checksums.txt") && !strings.HasSuffix(a.Name, "-checksums.txt") {
			continue
		}
		published = true
		sums, err := m.GH.Checksums(ctx, a)
		if err != nil {
			return nil, fmt.Errorf("read checksums: %w", err)
		}
		c, ok := sums[an]
		if !ok {
			continue
		}
		if p.Checksum != "" && c != p.Checksum {
			return nil, fmt.Errorf("%s %s publishes conflicting checksums for %s", full, rel.TagName, an)
		}
		p.Checksum = c
	}
	if published && p.Checksum == "" {
		return nil, fmt.Errorf("%s %s publishes checksums but none for %s; refusing to install an unverified binary", full, rel.TagName, an)
	}
	return p, nil
}

// cobraBuiltins are commands cobra adds lazily inside Execute, so a lookup on
// the root command does not see them yet.
var cobraBuiltins = map[string]bool{
	"help": true, "completion": true,
	cobra.ShellCompRequestCmd: true, cobra.ShellCompNoDescRequestCmd: true,
}

func (m *Manager) reserved(name string) bool {
	return cobraBuiltins[name] || (m.Reserved != nil && m.Reserved(name))
}

// Install downloads the planned binary, verifies it and records it in the lock file.
func (m *Manager) Install(ctx context.Context, p *Plan) (*Entry, error) {
	dir := filepath.Join(m.Dir, p.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(dir, ".download-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	sum, err := m.GH.Download(ctx, p.Asset, tmp)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, err
	}
	if p.Checksum != "" && sum != p.Checksum {
		return nil, fmt.Errorf("checksum mismatch for %s: got %s, want %s", p.Asset.Name, sum, p.Checksum)
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return nil, err
	}
	bin := filepath.Join(dir, p.Asset.Name)
	if runtime.GOOS == "windows" {
		_ = os.Remove(bin)
	}
	if err := os.Rename(tmp.Name(), bin); err != nil {
		return nil, err
	}

	lf, err := m.load()
	if err != nil {
		return nil, err
	}
	e := &Entry{
		Name: p.Name, Repo: p.Repo, Version: p.Tag, Path: bin, SHA256: sum,
		Official: p.Official, ChecksumsVerified: p.Checksum != "", InstalledAt: time.Now().UTC(),
	}
	lf.Extensions[p.Name] = e
	if err := m.save(lf); err != nil {
		return nil, err
	}
	return e, nil
}

func (m *Manager) Remove(name string) error {
	lf, err := m.load()
	if err != nil {
		return err
	}
	if _, ok := lf.Extensions[name]; !ok {
		return fmt.Errorf("extension %q is not installed", name)
	}
	delete(lf.Extensions, name)
	if err := m.save(lf); err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(m.Dir, name))
}

// ErrTampered means the binary on disk no longer matches the lock file.
var ErrTampered = errors.New("extension binary does not match its recorded checksum")

// Command returns an exec.Cmd for the extension after verifying its checksum.
func (m *Manager) Command(e *Entry, args []string, reearthBin string) (*exec.Cmd, error) {
	sum, err := ghrelease.FileSHA256(e.Path)
	if err != nil {
		return nil, err
	}
	if sum != e.SHA256 {
		return nil, fmt.Errorf("%w: %s (reinstall with `reearth ext install %s`)", ErrTampered, e.Path, e.Repo)
	}
	cmd := exec.Command(e.Path, args...)
	// The user's environment is inherited; REEARTH_EXTENSION marks calls back
	// into the CLI as coming from an extension. Passing the whole environment
	// on is not a read, so it bypasses envvar.
	cmd.Env = append(os.Environ(),
		"REEARTH_EXTENSION=1",
		"REEARTH_EXTENSION_NAME="+e.Name,
		"REEARTH_BIN="+reearthBin,
	)
	return cmd, nil
}
