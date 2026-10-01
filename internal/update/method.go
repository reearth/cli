// Package update implements update notifications and `reearth upgrade`
// (self-update from GitHub Releases with checksum verification).
package update

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/reearth/cli/sdk/envvar"
)

// Repo is the GitHub repository that publishes releases.
const Repo = "reearth/cli"

// Method is how the running binary was installed.
type Method struct {
	Name string `json:"name"`
	// Command upgrades the CLI when Self is false.
	Command string `json:"command,omitempty"`
	// Self means `reearth upgrade` may replace the binary in place.
	Self bool `json:"self"`
}

// Executable returns the resolved path of the running binary.
func Executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// DetectMethod infers the install method from the executable path.
func DetectMethod(exe string) Method {
	p := strings.ToLower(filepath.ToSlash(exe))
	switch {
	case strings.Contains(p, "/caskroom/"), strings.Contains(p, "/cellar/"), strings.Contains(p, "/homebrew/"), strings.Contains(p, "/linuxbrew/"):
		return Method{Name: "homebrew", Command: "brew upgrade reearth"}
	case strings.Contains(p, "/scoop/"):
		return Method{Name: "scoop", Command: "scoop update reearth"}
	case strings.Contains(p, "/winget/"), strings.Contains(p, "/windowsapps/"):
		return Method{Name: "winget", Command: "winget upgrade Reearth.Reearth"}
	case strings.HasPrefix(p, "/nix/store/"):
		return Method{Name: "nix", Command: "upgrade reearth with Nix (nix profile upgrade, or update your Nix configuration)"}
	case runtime.GOOS == "linux" && strings.HasPrefix(p, "/usr/bin/"):
		return Method{Name: "system package", Command: "upgrade the reearth package with your package manager (apt, dnf, apk)"}
	}
	if gobin := goBin(); gobin != "" && strings.HasPrefix(p, strings.ToLower(filepath.ToSlash(gobin))+"/") {
		return Method{Name: "go install", Command: "go install github.com/reearth/cli/cmd/reearth@latest"}
	}
	return Method{Name: "standalone", Self: true}
}

func goBin() string {
	if v := envvar.Get("GOBIN"); v != "" {
		return v
	}
	gopath := envvar.Get("GOPATH")
	if gopath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		gopath = filepath.Join(home, "go")
	}
	return filepath.Join(strings.Split(gopath, string(os.PathListSeparator))[0], "bin")
}
