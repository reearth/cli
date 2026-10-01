package cmdtree

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Lint checks the descriptions that `search`, `--help` and the agent docs are
// built from. A command whose summary only repeats its name ("dns",
// "Operations for records", "Manage accounts") cannot be found by describing
// a task.
//
// cobra's completion commands and the flags cobra adds are skipped: cobra
// writes their text, which changes with cobra and is not ours to fix.
func Lint(cmds []Command) []error {
	var errs []error
	report := func(path, format string, args ...any) {
		errs = append(errs, fmt.Errorf("%s: %s", path, fmt.Sprintf(format, args...)))
	}
	for _, c := range cmds {
		if c.fromCobra {
			continue
		}
		name := c.Path[strings.LastIndex(c.Path, " ")+1:]
		lintText(c.Path, "short description", c.Short, report)
		if c.Short != "" && len(strings.Fields(c.Short)) < 2 {
			report(c.Path, "short description %q is a single word; describe what the command does", c.Short)
		}
		if strings.EqualFold(strings.TrimSpace(c.Short), name) {
			report(c.Path, "short description only repeats the command name")
		} else if len(strings.Fields(c.Short)) > 1 && onlyNames(c) {
			report(c.Path, "short description %q says nothing beyond the command's name; describe what it does", c.Short)
		}
		for _, f := range c.Flags {
			if !f.fromCobra {
				lintText(c.Path, "usage of --"+f.Name, f.Usage, report)
			}
		}
	}
	return errs
}

// genericWords say nothing about what a command does.
var genericWords = map[string]bool{
	"operations": true, "operation": true, "commands": true, "command": true, "manage": true,
	"for": true, "the": true, "a": true, "an": true, "of": true, "with": true, "work": true,
}

// onlyNames reports whether the Short, without generic words, names only the
// command (or one of its aliases) or only its parent: "Manage accounts" on
// `account`, "Records commands" on `dns records`, "Manage dns" on
// `dns records`. A runnable command may name both, as "List accounts" on
// `account list` does: its name is the action. A group may not, as in
// "Dns records" on `dns records`.
func onlyNames(c Command) bool {
	path := strings.Fields(c.Path)
	own := map[string]bool{singular(path[len(path)-1]): true}
	for _, a := range c.Aliases {
		own[singular(a)] = true
	}
	parent := map[string]bool{}
	if len(path) > 1 {
		parent[singular(path[len(path)-2])] = true
	}
	ownOnly, parentOnly, both := true, true, true
	for _, w := range strings.Fields(strings.ToLower(c.Short)) {
		w = singular(strings.TrimFunc(w, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }))
		if w == "" || genericWords[w] {
			continue
		}
		ownOnly = ownOnly && own[w]
		parentOnly = parentOnly && parent[w]
		both = both && (own[w] || parent[w])
	}
	return ownOnly || parentOnly || both && !c.Runnable
}

// singular drops a plural ending, so that "accounts" matches `account`.
func singular(w string) string {
	w = strings.ToLower(w)
	switch {
	case strings.HasSuffix(w, "ies") && len(w) > 3:
		return w[:len(w)-3] + "y"
	case strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") && len(w) > 1:
		return w[:len(w)-1]
	}
	return w
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
