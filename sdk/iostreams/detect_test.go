package iostreams

import (
	"testing"

	"github.com/reearth/cli/sdk/envvar"
	"github.com/reearth/cli/sdk/envvar/envvartest"
)

func TestDetectAgent(t *testing.T) {
	for _, tc := range []struct{ v, want string }{
		{"1", "agent"}, {"ON", "agent"}, {"yes", "agent"},
		{"0", ""}, {"off", ""}, {"no", ""}, {"FALSE", ""},
		{"my-bot", "my-bot"},
	} {
		envvartest.Fake(t, envvar.Map{"REEARTH_AGENT": tc.v, "CLAUDECODE": "1"})
		if got := DetectAgent(); got != tc.want {
			t.Errorf("REEARTH_AGENT=%s: got %q, want %q", tc.v, got, tc.want)
		}
	}
	envvartest.Fake(t, envvar.Map{"CLAUDECODE": "1"})
	if got := DetectAgent(); got != "claude-code" {
		t.Errorf("detected %q", got)
	}
}

func TestDetectCI(t *testing.T) {
	for _, tc := range []struct {
		k, v string
		want bool
	}{
		{"CI", "true", true}, {"CI", "1", true}, {"CI", "False", false}, {"CI", "0", false}, {"CI", "off", false},
		{"JENKINS_URL", "https://ci.example", true},
	} {
		envvartest.Fake(t, envvar.Map{tc.k: tc.v})
		if got := DetectCI(); got != tc.want {
			t.Errorf("%s=%s: got %v", tc.k, tc.v, got)
		}
	}
}
