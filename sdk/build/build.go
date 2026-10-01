// Package build holds build-time metadata injected via -ldflags.
package build

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"

	"golang.org/x/mod/module"
)

// Set at build time via -ldflags "-X github.com/reearth/cli/sdk/build.Version=...".
var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

func init() {
	if Version != "dev" {
		return
	}
	// `go install ...@vX.Y.Z` embeds the module version; `go build` in a git
	// checkout embeds a pseudo-version, which IsDev still treats as development.
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		Version = strings.TrimPrefix(info.Main.Version, "v")
	}
}

// IsDev reports whether this is a development build: anything but a release
// version, i.e. "dev", a GoReleaser snapshot, a Go pseudo-version or a build
// from a modified checkout (+dirty).
func IsDev() bool { return isDev(Version) }

func isDev(v string) bool {
	switch v {
	case "", "dev", "(devel)":
		return true
	}
	if strings.Contains(v, "SNAPSHOT") || strings.Contains(v, "+") {
		return true
	}
	return module.IsPseudoVersion("v" + strings.TrimPrefix(v, "v"))
}

// UserAgent returns the User-Agent header value for HTTP requests.
func UserAgent(app string) string {
	return fmt.Sprintf("%s/%s (%s/%s)", app, Version, runtime.GOOS, runtime.GOARCH)
}
