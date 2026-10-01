package cmdtree

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func tree() *cobra.Command {
	run := func(*cobra.Command, []string) {}
	root := &cobra.Command{Use: "reearth", Short: "Work with Re:Earth"}
	cms := &cobra.Command{Use: "cms", Short: "Manage Re:Earth CMS"}
	items := &cobra.Command{Use: "items", Short: "Work with the items of a model"}
	list := &cobra.Command{Use: "list", Aliases: []string{"ls"}, Short: "List items", Run: run}
	list.Flags().String("model", "", "Model ID")
	create := &cobra.Command{Use: "create", Short: "Create an item", Run: run}
	create.Flags().StringArray("field", nil, "Field value as key=value")
	items.AddCommand(list, create)
	models := &cobra.Command{Use: "models", Short: "Define the fields that items have"}
	models.AddCommand(&cobra.Command{Use: "list", Short: "List models", Run: run})
	cms.AddCommand(items, models)
	root.AddCommand(cms,
		&cobra.Command{Use: "login", Short: "Sign in and add an account", Run: run},
		&cobra.Command{Use: "secret", Short: "Hidden", Hidden: true, Run: run},
	)
	return root
}

func TestWalk(t *testing.T) {
	var paths []string
	for _, c := range Walk(tree()) {
		paths = append(paths, c.Path)
	}
	want := "reearth|reearth cms|reearth cms items|reearth cms items create|reearth cms items list|reearth cms models|reearth cms models list|reearth login"
	if got := strings.Join(paths, "|"); got != want {
		t.Fatalf("paths = %s", got)
	}
}

func TestSearch(t *testing.T) {
	cmds := Walk(tree())
	for query, want := range map[string]string{
		"list the items in a model":     "reearth cms items list",
		"create a new item":             "reearth cms items create",
		"show all models":               "reearth cms models list",
		"sign in":                       "reearth login",
		"set a field value":             "reearth cms items create",
		"creat itme":                    "reearth cms items create",
		"ls":                            "reearth cms items list",
		"list models of the cms please": "reearth cms models list",
	} {
		got := Search(cmds, query, 5)
		if len(got) == 0 || got[0].Command != want {
			t.Errorf("%q: got %v, want %s first", query, got, want)
		}
	}
	if got := Search(cmds, "hidden secret", 5); len(got) != 0 {
		t.Errorf("hidden command found: %v", got)
	}
	if got := Search(cmds, "the", 5); len(got) != 0 {
		t.Errorf("stopword-only query matched: %v", got)
	}
	if got := Search(cmds, "list", 1); len(got) != 1 {
		t.Errorf("limit ignored: %v", got)
	}
}

func TestLint(t *testing.T) {
	root := &cobra.Command{Use: "reearth", Short: "Work with Re:Earth"}
	dns := &cobra.Command{Use: "dns", Short: "dns"}
	records := &cobra.Command{Use: "records", Short: "Records", Run: func(*cobra.Command, []string) {}}
	records.Flags().String("type", "", "record type.")
	dns.AddCommand(records, &cobra.Command{Use: "empty", Run: func(*cobra.Command, []string) {}})
	root.AddCommand(dns)

	var got []string
	for _, err := range Lint(Walk(root)) {
		got = append(got, err.Error())
	}
	for _, want := range []string{
		`reearth dns: short description "dns" should start with a capital letter`,
		`reearth dns: short description only repeats the command name`,
		`reearth dns records: short description "Records" is a single word`,
		`reearth dns records: usage of --type "record type." should start with a capital letter`,
		`reearth dns empty: short description is empty`,
	} {
		found := false
		for _, g := range got {
			found = found || strings.HasPrefix(g, want)
		}
		if !found {
			t.Errorf("missing %q in\n%s", want, strings.Join(got, "\n"))
		}
	}
}

func TestLintNamesOnly(t *testing.T) {
	run := func(*cobra.Command, []string) {}
	for _, c := range []struct {
		group, short string
		runnable     bool
		reject       bool
	}{
		{"records", "Operations for records", true, true},
		{"records", "Records commands", false, true},
		{"records", "Manage dns", false, true},
		{"records", "Manage DNS records", false, true},
		{"records", "Work with the record", true, true},
		{"entries", "Manage entries", false, true},
		{"rec", "Manage records", false, true}, // an alias is a name too
		{"records", "Add, list and delete the records of a zone", false, false},
		{"list", "List dns", true, false},
		{"list", "List records", true, false},
	} {
		root := &cobra.Command{Use: "reearth", Short: "Work with Re:Earth"}
		dns := &cobra.Command{Use: "dns", Short: "Point domain names at hosts"}
		use, aliases := c.group, []string(nil)
		if use == "rec" {
			use, aliases = "records", []string{"rec"}
		}
		cmd := &cobra.Command{Use: use, Aliases: aliases, Short: c.short}
		if c.runnable {
			cmd.Run = run
		} else {
			cmd.AddCommand(&cobra.Command{Use: "add", Short: "Add a record to a zone", Run: run})
		}
		dns.AddCommand(cmd)
		root.AddCommand(dns)
		errs := Lint(Walk(root))
		if got := len(errs) > 0; got != c.reject {
			t.Errorf("%s %q: rejected = %v, want %v (%v)", use, c.short, got, c.reject, errs)
		}
	}
}

func TestInitDefaults(t *testing.T) {
	root := tree()
	root.Version = "1.0.0"
	InitDefaults(root)
	cmds := Walk(root)
	var paths []string
	for _, c := range cmds {
		paths = append(paths, c.Path)
	}
	if got := strings.Join(paths, "|"); !strings.Contains(got, "|reearth completion|reearth completion bash|") || strings.Contains(got, "help") {
		t.Errorf("paths = %s", got)
	}
	found := false
	for _, f := range cmds[0].Flags {
		found = found || f.Name == "version"
	}
	if !found {
		t.Errorf("root flags = %+v", cmds[0].Flags)
	}
	// cobra's text, such as "version for reearth", is not ours to lint.
	if errs := Lint(cmds); len(errs) != 0 {
		t.Errorf("lint of cobra's commands: %v", errs)
	}
}

func TestWithinOneEdit(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"item", "item", true},
		{"itme", "item", true},
		{"creat", "create", true},
		{"lgin", "login", true},
		{"logot", "logout", true},
		{"model", "modal", true},
		{"model", "mdoal", false},
		{"list", "lost1", false},
	} {
		if got := withinOneEdit(c.a, c.b); got != c.want {
			t.Errorf("withinOneEdit(%q, %q) = %v", c.a, c.b, got)
		}
	}
}
