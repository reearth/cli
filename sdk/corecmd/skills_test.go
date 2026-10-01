package corecmd

import (
	"regexp"
	"strings"
	"testing"

	"github.com/reearth/cli/sdk/skills"
)

// TestSkillMDTopics keeps the help topics that SKILL.md names in step with the
// registered ones: an installed SKILL.md must not point at a missing topic.
func TestSkillMDTopics(t *testing.T) {
	md, err := skills.SkillMD("reearth")
	if err != nil {
		t.Fatal(err)
	}
	registered := map[string]bool{}
	for _, tp := range topics {
		registered[tp.name] = true
	}
	var named []string
	for _, l := range strings.Split(md, "\n") {
		// "`reearth help <topic>`: read `formatting`, `exit-codes` or ..."
		if _, list, ok := strings.Cut(l, "help <topic>`:"); ok {
			for _, m := range regexp.MustCompile("`([a-z-]+)`").FindAllStringSubmatch(list, -1) {
				named = append(named, m[1])
			}
		}
		for _, m := range regexp.MustCompile("`reearth help ([a-z-]+)`").FindAllStringSubmatch(l, -1) {
			named = append(named, m[1])
		}
	}
	if len(named) == 0 {
		t.Fatal("SKILL.md names no help topic; update this test")
	}
	for _, n := range named {
		if !registered[n] {
			t.Errorf("SKILL.md names help topic %q, which is not registered in topics.go", n)
		}
	}
}
