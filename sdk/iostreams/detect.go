package iostreams

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

// DetectAgent returns the name of the coding agent driving this process, or "".
// REEARTH_AGENT overrides detection: "0"/"false" disables it, any other value is used as the name.
func DetectAgent(getenv func(string) string) string {
	if v := getenv("REEARTH_AGENT"); v != "" {
		if v == "0" || v == "false" {
			return ""
		}
		if truthy(v) {
			return "agent"
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

// DetectCI reports whether the process runs on a CI service.
func DetectCI(getenv func(string) string) bool {
	for _, k := range []string{"CI", "GITHUB_ACTIONS", "BUILDKITE", "CIRCLECI", "GITLAB_CI", "JENKINS_URL", "TF_BUILD"} {
		if v := getenv(k); v != "" && v != "false" && v != "0" {
			return true
		}
	}
	return false
}
