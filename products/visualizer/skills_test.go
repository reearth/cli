package visualizer

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestSkills(t *testing.T) {
	skills := Product{}.Skills()
	if len(skills) == 0 {
		t.Fatal("no skills")
	}
	// Agents expect the name in the frontmatter to match the skill's folder,
	// both here and where `skills install` writes it (reearth-<name>/).
	entries, err := skillFiles.ReadDir("skills")
	if err != nil {
		t.Fatal(err)
	}
	var folders []string
	for _, e := range entries {
		b, err := skillFiles.ReadFile("skills/" + e.Name() + "/SKILL.md")
		if err != nil {
			t.Errorf("skills/%s: %v", e.Name(), err)
			continue
		}
		m := regexp.MustCompile(`\A---\nname: (\S+)\n`).FindStringSubmatch(string(b))
		if m == nil || m[1] != e.Name() {
			t.Errorf("skills/%s/SKILL.md: frontmatter name = %v, want %s", e.Name(), m, e.Name())
		}
		folders = append(folders, e.Name())
	}
	for _, s := range skills {
		if !slices.Contains(folders, "reearth-"+s.Name) {
			t.Errorf("%s: no folder skills/reearth-%s", s.Name, s.Name)
		}
		if s.Short == "" {
			t.Errorf("%s: no Short", s.Name)
		}
	}
	if len(folders) != len(skills) {
		t.Errorf("%d skill folders but %d skills in Skills()", len(folders), len(skills))
	}
}

// TestPluginSkillQuotes keeps the CLI messages that the plugin skill quotes in
// step with what validate and pack print, so an installed skill never tells
// an agent to look for a message the CLI no longer prints.
func TestPluginSkillQuotes(t *testing.T) {
	md := skillFile("visualizer-plugin")
	for _, q := range []string{"`… .title is required`", "`\"reearth\" is reserved`", "`the package would be <size>; Visualizer accepts up to 10.0 MB`"} {
		if !strings.Contains(md, q) {
			t.Errorf("the skill no longer quotes %s; update this test", q)
		}
	}

	// A field without a title, and the reserved id.
	dir := t.TempDir()
	writePlugin(t, dir, map[string]string{
		manifestFile: `id: reearth
name: P
version: 1.0.0
extensions:
  - id: a
    name: A
    type: widget
    schema:
      groups:
        - id: default
          title: Default
          fields:
            - { id: f, type: string }
`,
		"a.js": "",
	})
	v, err := validatePlugin(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, i := range v.Issues {
		got = append(got, i.Path+" "+i.Message)
	}
	if !slices.ContainsFunc(got, func(s string) bool { return strings.HasSuffix(s, ".title is required") }) {
		t.Errorf("no `.title is required` issue; got %q", got)
	}
	if !slices.Contains(got, `id "reearth" is reserved`) {
		t.Errorf("no `\"reearth\" is reserved` issue; got %q", got)
	}

	// Over the size limit.
	dir = t.TempDir()
	writePlugin(t, dir, map[string]string{
		manifestFile: "id: p\nname: P\nversion: 1.0.0\nextensions:\n  - { id: a, name: A, type: widget }\n",
		"a.js":       "a",
	})
	big := make([]byte, maxPackageSize+1)
	_, _ = rand.Read(big) // incompressible
	if err := os.WriteFile(filepath.Join(dir, "data.bin"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	err = (&packOptions{Dir: dir}).run(testFactory(t))
	if err == nil || !regexp.MustCompile(`^the package would be \d+\.\d [KMG]B; Visualizer accepts up to 10\.0 MB$`).MatchString(err.Error()) {
		t.Errorf("pack error = %v", err)
	}
}
