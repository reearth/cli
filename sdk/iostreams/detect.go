package iostreams

import "strings"

// agentEnvs maps environment variables set by coding agents to a display name.
// When one is present, the CLI never prompts and suppresses decorative output.
var agentEnvs = []struct{ env, name string }{
	{"CLAUDECODE", "claude-code"},
	{"CURSOR_AGENT", "cursor"},
	{"CODEX_SANDBOX", "codex"},
	{"CODEX_CI", "codex"},
	{"GEMINI_CLI", "gemini-cli"},
	{"OPENCODE", "opencode"},
	{"AMP_AGENT", "amp"},
}

// ParseBool reads a boolean environment value: 1/true/yes/on or 0/false/no/off,
// case-insensitively. ok is false for any other value, including "".
func ParseBool(v string) (value, ok bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	}
	return false, false
}

// EnvTrue reports whether v is a true boolean value (see ParseBool).
func EnvTrue(v string) bool {
	b, ok := ParseBool(v)
	return ok && b
}

// DetectAgent returns the name of the coding agent driving this process, or "".
// REEARTH_AGENT overrides detection: a false value disables it, a true value
// enables it, and any other value is used as the agent's name.
func DetectAgent(getenv func(string) string) string {
	if v := getenv("REEARTH_AGENT"); v != "" {
		if b, ok := ParseBool(v); ok {
			if b {
				return "agent"
			}
			return ""
		}
		return v
	}
	for _, a := range agentEnvs {
		if getenv(a.env) != "" {
			return a.name
		}
	}
	return ""
}

// ciEnvs are set by CI services. A false value (CI=false) does not count.
var ciEnvs = []string{"CI", "GITHUB_ACTIONS", "BUILDKITE", "CIRCLECI", "GITLAB_CI", "JENKINS_URL", "TF_BUILD"}

// DetectCI reports whether the process runs on a CI service.
func DetectCI(getenv func(string) string) bool {
	for _, k := range ciEnvs {
		if v := getenv(k); v != "" {
			if b, ok := ParseBool(v); !ok || b {
				return true
			}
		}
	}
	return false
}
