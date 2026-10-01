// Package skills renders the SKILL.md that `skills install` writes into agent
// skill directories. It holds only rules that outlive a release; commands and
// flags are left to `search` and `--help`, which always match the installed CLI.
package skills

import (
	"embed"
	"strings"
	"text/template"
)

//go:embed SKILL.md.tmpl
var embedded embed.FS

// SkillMD returns the SKILL.md for the binary named app.
func SkillMD(app string) (string, error) {
	t, err := template.ParseFS(embedded, "SKILL.md.tmpl")
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := t.Execute(&b, map[string]string{"App": app}); err != nil {
		return "", err
	}
	return b.String(), nil
}
