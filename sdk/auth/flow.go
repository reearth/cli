package auth

import (
	"os"
	"runtime"
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

// FlowEnv abstracts the environment for DetectFlow (for tests).
type FlowEnv struct {
	Getenv func(string) string
	GOOS   string
	Exists func(path string) bool
}

// SystemFlowEnv returns the real process environment.
func SystemFlowEnv() FlowEnv {
	return FlowEnv{
		Getenv: os.Getenv,
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
		if e.Getenv(k) != "" {
			return FlowDevice
		}
	}
	// Remote dev environments: the browser runs elsewhere.
	if e.Getenv("CODESPACES") == "true" || e.Getenv("GITPOD_WORKSPACE_ID") != "" || e.Getenv("REMOTE_CONTAINERS") == "true" {
		return FlowDevice
	}
	if e.Getenv("BROWSER") == "none" {
		return FlowDevice
	}
	if e.GOOS == "linux" {
		// WSL can open the Windows browser, and localhost is forwarded.
		if e.Getenv("WSL_DISTRO_NAME") != "" {
			return FlowLoopback
		}
		if e.Exists != nil && (e.Exists("/.dockerenv") || e.Exists("/run/.containerenv")) {
			return FlowDevice
		}
		if e.Getenv("DISPLAY") == "" && e.Getenv("WAYLAND_DISPLAY") == "" {
			return FlowDevice
		}
	}
	return FlowLoopback
}
