// Package build holds build-time metadata injected via -ldflags.
package build

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
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
	// `go install ...@vX.Y.Z` embeds the module version.
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		Version = strings.TrimPrefix(info.Main.Version, "v")
	}
}

// IsDev reports whether this is a local development build.
func IsDev() bool {
	return Version == "dev" || strings.Contains(Version, "SNAPSHOT")
}

// UserAgent returns the User-Agent header value for HTTP requests.
func UserAgent(app string) string {
	return fmt.Sprintf("%s/%s (%s/%s)", app, Version, runtime.GOOS, runtime.GOARCH)
}
