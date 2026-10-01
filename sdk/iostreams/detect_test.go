package iostreams

import "testing"

func env(kv ...string) func(string) string {
	m := map[string]string{}
	for i := 0; i < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return func(k string) string { return m[k] }
}

func TestDetectAgent(t *testing.T) {
	for _, tc := range []struct{ v, want string }{
		{"1", "agent"}, {"ON", "agent"}, {"yes", "agent"},
		{"0", ""}, {"off", ""}, {"no", ""}, {"FALSE", ""},
		{"my-bot", "my-bot"},
	} {
		if got := DetectAgent(env("REEARTH_AGENT", tc.v, "CLAUDECODE", "1")); got != tc.want {
			t.Errorf("REEARTH_AGENT=%s: got %q, want %q", tc.v, got, tc.want)
		}
	}
	if got := DetectAgent(env("CLAUDECODE", "1")); got != "claude-code" {
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
		if got := DetectCI(env(tc.k, tc.v)); got != tc.want {
			t.Errorf("%s=%s: got %v", tc.k, tc.v, got)
		}
	}
}
