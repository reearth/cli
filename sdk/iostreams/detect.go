package iostreams

import "github.com/reearth/cli/sdk/envvar"

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
// REEARTH_AGENT overrides detection: a false value disables it, a true value
// enables it, and any other value is used as the agent's name.
func DetectAgent() string {
	if v := envvar.Get("REEARTH_AGENT"); v != "" {
		if b, ok := envvar.Bool("REEARTH_AGENT"); ok {
			if b {
				return "agent"
			}
			return ""
		}
		return v
	}
	for _, a := range agentEnvs {
		if envvar.Get(a.env) != "" {
			return a.name
		}
	}
	return ""
}

// ciEnvs are set by CI services. A false value (CI=false) does not count.
var ciEnvs = []string{"CI", "GITHUB_ACTIONS", "BUILDKITE", "CIRCLECI", "GITLAB_CI", "JENKINS_URL", "TF_BUILD"}

// DetectCI reports whether the process runs on a CI service.
func DetectCI() bool {
	for _, k := range ciEnvs {
		if envvar.Get(k) != "" {
			if b, ok := envvar.Bool(k); !ok || b {
				return true
			}
		}
	}
	return false
}
