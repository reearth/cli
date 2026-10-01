package cmdtree

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Lint checks the descriptions that `search`, `--help` and the agent docs are
// built from. A command whose summary only repeats its name ("dns",
// "Operations for records") cannot be found by describing a task.
func Lint(cmds []Command) []error {
	var errs []error
	report := func(path, format string, args ...any) {
		errs = append(errs, fmt.Errorf("%s: %s", path, fmt.Sprintf(format, args...)))
	}
	for _, c := range cmds {
		name := c.Path[strings.LastIndex(c.Path, " ")+1:]
		lintText(c.Path, "short description", c.Short, report)
		if c.Short != "" && len(strings.Fields(c.Short)) < 2 {
			report(c.Path, "short description %q is a single word; describe what the command does", c.Short)
		}
		if strings.EqualFold(strings.TrimSpace(c.Short), name) {
			report(c.Path, "short description only repeats the command name")
		}
		for _, f := range c.Flags {
			lintText(c.Path, "usage of --"+f.Name, f.Usage, report)
		}
	}
	return errs
}

func lintText(path, what, s string, report func(path, format string, args ...any)) {
	switch r, _ := utf8.DecodeRuneInString(s); {
	case strings.TrimSpace(s) == "":
		report(path, "%s is empty", what)
	case strings.Contains(s, "\n"):
		report(path, "%s spans several lines; put details in Long", what)
	case unicode.IsLower(r):
		report(path, "%s %q should start with a capital letter", what, s)
	case strings.HasSuffix(s, "."):
		report(path, "%s %q should not end with a period", what, s)
	}
}
