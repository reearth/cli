package update

import (
	"runtime"
	"testing"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"v0.2.0", "0.1.0", true},
		{"0.1.0", "v0.1.0", false},
		{"v0.1.0", "0.2.0", false},
		{"v1.0.0", "1.0.0-rc.1", true},
		{"v1.0.0", "dev", false},
	}
	for _, c := range cases {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

func TestDetectMethod(t *testing.T) {
	cases := map[string]string{
		"/opt/homebrew/Caskroom/reearth/0.1.0/reearth":                "homebrew",
		"/home/linuxbrew/.linuxbrew/Cellar/reearth/0.1.0/bin/reearth": "homebrew",
		"/Users/kana/.local/bin/reearth":                              "standalone",
	}
	if runtime.GOOS == "windows" {
		cases[`C:\Users\kana\scoop\apps\reearth\current\reearth.exe`] = "scoop"
	}
	for path, want := range cases {
		if got := DetectMethod(path); got.Name != want {
			t.Errorf("DetectMethod(%q) = %q, want %q", path, got.Name, want)
		}
	}
}

func TestArchiveName(t *testing.T) {
	if got := ArchiveName("0.2.0", "darwin", "arm64"); got != "reearth_0.2.0_darwin_arm64.tar.gz" {
		t.Error(got)
	}
	if got := ArchiveName("0.2.0", "windows", "amd64"); got != "reearth_0.2.0_windows_amd64.zip" {
		t.Error(got)
	}
}
