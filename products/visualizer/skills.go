package visualizer

import (
	"embed"

	"github.com/reearth/cli/sdk/core"
)

//go:embed skills
var skillFiles embed.FS

var _ core.SkillProduct = Product{}

func (Product) Skills() []core.Skill {
	return []core.Skill{
		{Name: "visualizer-plugin", Short: "Build, edit and debug Visualizer plugins", Content: skillFile("visualizer-plugin")},
	}
}

// skillFile reads skills/reearth-<name>/SKILL.md. The folder matches the name
// in the frontmatter, as agents expect, and the folder it is installed to.
func skillFile(name string) string {
	b, err := skillFiles.ReadFile("skills/reearth-" + name + "/SKILL.md")
	if err != nil {
		panic(err) // embedded; only a wrong name fails, which the tests catch
	}
	return string(b)
}
