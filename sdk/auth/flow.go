package auth

import (
	"os"
	"runtime"

	"github.com/reearth/cli/sdk/envvar"
)

type Flow int

const (
	// FlowLoopback opens a browser and receives the code on 127.0.0.1 (authorization code + PKCE).
	FlowLoopback Flow = iota
	// FlowDevice shows a code to enter on another device (RFC 8628).
	FlowDevice
)

func (f Flow) String() string {
	if f == FlowDevice {
		return "device"
	}
	return "loopback"
}

// FlowEnv abstracts the platform for DetectFlow (for tests). Environment
// variables are read through envvar.
type FlowEnv struct {
	GOOS   string
	Exists func(path string) bool
}

// SystemFlowEnv returns the real platform.
func SystemFlowEnv() FlowEnv {
	return FlowEnv{
		GOOS:   runtime.GOOS,
		Exists: func(p string) bool { _, err := os.Stat(p); return err == nil },
	}
}

// DetectFlow picks the login flow. The loopback flow needs a browser on the
// same machine that can reach 127.0.0.1; otherwise the device flow is used.
func DetectFlow(e FlowEnv, forceWeb, forceDevice bool) Flow {
	switch {
	case forceDevice:
		return FlowDevice
	case forceWeb:
		return FlowLoopback
	}
	for _, k := range []string{"SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY"} {
		if envvar.Get(k) != "" {
			return FlowDevice
		}
	}
	// Remote dev environments: the browser runs elsewhere.
	if envvar.Get("CODESPACES") == "true" || envvar.Get("GITPOD_WORKSPACE_ID") != "" || envvar.Get("REMOTE_CONTAINERS") == "true" {
		return FlowDevice
	}
	if envvar.Get("BROWSER") == "none" {
		return FlowDevice
	}
	if e.GOOS == "linux" {
		// WSL can open the Windows browser, and localhost is forwarded.
		if envvar.Get("WSL_DISTRO_NAME") != "" {
			return FlowLoopback
		}
		if e.Exists != nil && (e.Exists("/.dockerenv") || e.Exists("/run/.containerenv")) {
			return FlowDevice
		}
		if envvar.Get("DISPLAY") == "" && envvar.Get("WAYLAND_DISPLAY") == "" {
			return FlowDevice
		}
	}
	return FlowLoopback
}
