package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/reearth/cli/internal/ghrelease"
	"github.com/reearth/cli/sdk/build"
	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/core"
)

type checkJSON struct {
	Current         string `json:"current"`
	Latest          string `json:"latest"`
	UpdateAvailable bool   `json:"update_available"`
	Method          Method `json:"method"`
	URL             string `json:"url,omitempty"`
}

func NewCmdUpgrade(f *core.Factory) *cobra.Command {
	var (
		check   bool
		version string
		force   bool
	)
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade the CLI to the latest release",
		Long: `Upgrade the CLI from GitHub Releases, verifying the SHA-256 checksum.

If the CLI was installed with a package manager (Homebrew, Scoop, winget,
apt, ...), the command prints how to upgrade with it instead.`,
		Example: `  $ reearth upgrade
  $ reearth upgrade --check
  $ reearth upgrade --version v0.3.0`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			gh := ghrelease.New(build.UserAgent(f.AppName))
			exe, err := Executable()
			if err != nil {
				return err
			}
			method := DetectMethod(exe)

			f.IO.StartProgress("Checking for updates…")
			var rel *ghrelease.Release
			if version != "" {
				rel, err = gh.Tag(ctx, Repo, "v"+normalize(version))
			} else {
				rel, err = gh.Latest(ctx, Repo)
			}
			f.IO.StopProgress()
			if errors.Is(err, ghrelease.ErrNotFound) {
				return cmdutil.NotFoundf("release not found")
			}
			if err != nil {
				return err
			}

			available := Newer(rel.TagName, build.Version)
			if check {
				p, err := f.Printer()
				if err != nil {
					return err
				}
				v := checkJSON{Current: normalize(build.Version), Latest: normalize(rel.TagName), UpdateAvailable: available, Method: method, URL: rel.HTMLURL}
				return p.Print(v, func() error {
					if available {
						f.IO.Info("Update available %s → %s", v.Current, f.IO.ErrColor().Bold(v.Latest))
						f.IO.Hint("%s", rel.HTMLURL)
					} else {
						f.IO.Success("Up to date (%s)", v.Current)
					}
					return nil
				})
			}

			if !available && version == "" && !force {
				f.IO.Success("Already up to date (%s)", normalize(build.Version))
				return nil
			}
			if !method.Self {
				f.IO.Info("%s was installed with %s. Upgrade it with:", f.AppName, method.Name)
				f.IO.Println("  " + f.IO.ErrColor().Bold(method.Command))
				return nil
			}
			if build.IsDev() && !force {
				return cmdutil.NewError(cmdutil.ExitError, "upgrade.dev_build", "this is a development build", "pass --force to replace it with a release build")
			}

			f.IO.StartProgress("Downloading " + rel.TagName + "…")
			err = install(ctx, gh, rel, exe)
			f.IO.StopProgress()
			if err != nil {
				if errors.Is(err, os.ErrPermission) {
					return &cmdutil.Error{Exit: cmdutil.ExitError, Code: "upgrade.permission", Message: err.Error(),
						Hint: "re-run with permission to write " + filepath.Dir(exe) + ", or reinstall with the install script"}
				}
				return err
			}
			f.IO.Success("Upgraded %s to %s", f.AppName, f.IO.ErrColor().Bold(normalize(rel.TagName)))
			return nil
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "Only check whether an update is available")
	cmd.Flags().StringVar(&version, "version", "", "Install a specific version")
	cmd.Flags().BoolVar(&force, "force", false, "Reinstall even if up to date (also replaces development builds)")
	return cmd
}

// ArchiveName returns the goreleaser archive name for a version (without "v").
func ArchiveName(version, goos, goarch string) string {
	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("reearth_%s_%s_%s.%s", version, goos, goarch, ext)
}

func install(ctx context.Context, gh *ghrelease.Client, rel *ghrelease.Release, exe string) error {
	ver := normalize(rel.TagName)
	name := ArchiveName(ver, runtime.GOOS, runtime.GOARCH)
	asset, ok := rel.Asset(name)
	if !ok {
		return fmt.Errorf("release %s has no asset %s", rel.TagName, name)
	}
	sumsAsset, ok := rel.Asset(fmt.Sprintf("reearth_%s_checksums.txt", ver))
	if !ok {
		return fmt.Errorf("release %s has no checksums file; refusing to install unverified binaries", rel.TagName)
	}
	sums, err := gh.Checksums(ctx, sumsAsset)
	if err != nil {
		return err
	}
	want, ok := sums[name]
	if !ok {
		return fmt.Errorf("checksums file does not list %s", name)
	}

	dir := filepath.Dir(exe)
	archive, err := os.CreateTemp("", "reearth-archive-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(archive.Name()) }()
	got, err := gh.Download(ctx, asset, archive)
	_ = archive.Close()
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s", name, got, want)
	}

	binName := "reearth"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	newBin, err := os.CreateTemp(dir, ".reearth-new-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(newBin.Name()) }()
	if strings.HasSuffix(name, ".zip") {
		err = extractZip(archive.Name(), binName, newBin)
	} else {
		err = extractTarGz(archive.Name(), binName, newBin)
	}
	if cerr := newBin.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if err := os.Chmod(newBin.Name(), 0o755); err != nil {
		return err
	}
	return replace(exe, newBin.Name())
}

// replace swaps the running binary. Windows cannot overwrite a running
// executable but can rename it, so the old one is moved aside first.
func replace(exe, newPath string) error {
	if runtime.GOOS == "windows" {
		old := exe + ".old"
		_ = os.Remove(old)
		if err := os.Rename(exe, old); err != nil {
			return err
		}
		if err := os.Rename(newPath, exe); err != nil {
			_ = os.Rename(old, exe)
			return err
		}
		return nil
	}
	return os.Rename(newPath, exe)
}

func extractTarGz(path, name string, w io.Writer) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("%s not found in archive", name)
		}
		if err != nil {
			return err
		}
		if h.Typeflag == tar.TypeReg && filepath.Base(h.Name) == name {
			_, err := io.Copy(w, io.LimitReader(tr, 512<<20))
			return err
		}
	}
}

func extractZip(path, name string, w io.Writer) error {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer func() { _ = zr.Close() }()
	for _, zf := range zr.File {
		if filepath.Base(zf.Name) != name || zf.FileInfo().IsDir() {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		defer func() { _ = rc.Close() }()
		_, err = io.Copy(w, io.LimitReader(rc, 512<<20))
		return err
	}
	return fmt.Errorf("%s not found in archive", name)
}

// DoctorCheck reports the version and install method.
func DoctorCheck(appName string) func(ctx context.Context) []core.Check {
	return func(ctx context.Context) []core.Check {
		exe, err := Executable()
		if err != nil {
			return nil
		}
		m := DetectMethod(exe)
		checks := []core.Check{{Name: "install", Status: core.CheckOK, Detail: fmt.Sprintf("%s (%s)", exe, m.Name)}}
		if s, err := readState(); err == nil && Newer(s.Latest, build.Version) {
			hint := "run `" + appName + " upgrade`"
			if !m.Self {
				hint = "run `" + m.Command + "`"
			}
			checks = append(checks, core.Check{Name: "version", Status: core.CheckWarn, Detail: fmt.Sprintf("%s (latest %s)", normalize(build.Version), normalize(s.Latest)), Hint: hint})
		} else {
			checks = append(checks, core.Check{Name: "version", Status: core.CheckOK, Detail: normalize(build.Version)})
		}
		return checks
	}
}
