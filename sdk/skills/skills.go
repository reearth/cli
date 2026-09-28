// Package skills serves agent-facing documentation embedded in the binary,
// so docs always match the installed CLI version.
//
// A doc is a markdown file with a small front matter. In the body, {{app}} is
// replaced by the binary name and {{cmd}} by the product's command prefix.
//
//	---
//	name: cms
//	summary: Manage CMS models, items and assets
//	---
//	# body...
//
// If a doc is attached to a command, a command reference generated from the
// cobra tree is appended when rendering.
package skills

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"text/template"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"go.yaml.in/yaml/v3"
)

//go:embed SKILL.md.tmpl docs/*.md
var embedded embed.FS

type Doc struct {
	Name    string `json:"name"`
	Product string `json:"product,omitempty"`
	Summary string `json:"summary"`

	body   string
	cmds   []*cobra.Command
	prefix string
}

type Registry struct {
	app  string
	docs map[string]*Doc
}

// NewRegistry returns a registry preloaded with the core docs.
func NewRegistry(app string) *Registry {
	r := &Registry{app: app, docs: map[string]*Doc{}}
	sub, _ := fs.Sub(embedded, "docs")
	if err := r.AddFS("", sub, nil, app); err != nil {
		panic(err)
	}
	return r
}

// AddFS registers every *.md file in fsys. cmds are attached to the doc named
// after the product so that its command reference is generated. prefix is how
// the product is invoked ("reearth cms" or "reearth-cms") and replaces {{cmd}}.
func (r *Registry) AddFS(product string, fsys fs.FS, cmds []*cobra.Command, prefix string) error {
	return fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path.Ext(p) != ".md" {
			return err
		}
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		doc, err := parse(b)
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		if doc.Name == "" {
			doc.Name = strings.TrimSuffix(path.Base(p), ".md")
		}
		doc.Product = product
		doc.prefix = prefix
		if product != "" && doc.Name == product {
			doc.cmds = cmds
		}
		if _, dup := r.docs[doc.Name]; dup {
			return fmt.Errorf("duplicate skill doc %q", doc.Name)
		}
		r.docs[doc.Name] = doc
		return nil
	})
}

func parse(b []byte) (*Doc, error) {
	s := string(bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")))
	doc := &Doc{}
	if rest, ok := strings.CutPrefix(s, "---\n"); ok {
		front, body, ok := strings.Cut(rest, "\n---\n")
		if !ok {
			return nil, fmt.Errorf("unterminated front matter")
		}
		if err := yaml.Unmarshal([]byte(front), doc); err != nil {
			return nil, err
		}
		s = body
	}
	doc.body = strings.TrimSpace(s) + "\n"
	return doc, nil
}

// List returns docs sorted: core docs first, then by product and name.
func (r *Registry) List() []*Doc {
	docs := make([]*Doc, 0, len(r.docs))
	for _, d := range r.docs {
		docs = append(docs, d)
	}
	sort.Slice(docs, func(i, j int) bool {
		if docs[i].Product != docs[j].Product {
			return docs[i].Product < docs[j].Product
		}
		return docs[i].Name < docs[j].Name
	})
	return docs
}

func (r *Registry) Get(name string) (*Doc, bool) {
	d, ok := r.docs[name]
	return d, ok
}

// Render returns the doc body with {{app}} (binary) and {{cmd}} (product
// command) replaced, and the command reference appended.
func (r *Registry) Render(d *Doc) string {
	body := strings.ReplaceAll(d.body, "{{cmd}}", d.prefix)
	body = strings.ReplaceAll(body, "{{app}}", r.app)
	if len(d.cmds) == 0 {
		return body
	}
	var b strings.Builder
	b.WriteString(body)
	b.WriteString("\n## Command reference\n\n")
	b.WriteString("_Generated from the installed CLI._\n")
	for _, c := range d.cmds {
		writeCommands(&b, c)
	}
	return b.String()
}

func writeCommands(b *strings.Builder, c *cobra.Command) {
	if c.Hidden || c.Name() == "help" {
		return
	}
	if c.Runnable() {
		fmt.Fprintf(b, "\n### `%s`\n\n%s\n", c.UseLine(), c.Short)
		if c.Example != "" {
			fmt.Fprintf(b, "\n```sh\n%s\n```\n", dedent(c.Example))
		}
		if fl := localFlagUsages(c); fl != "" {
			fmt.Fprintf(b, "\nFlags:\n\n```\n%s```\n", fl)
		}
	}
	for _, sub := range c.Commands() {
		writeCommands(b, sub)
	}
}

func localFlagUsages(c *cobra.Command) string {
	fs := pflag.NewFlagSet(c.Name(), pflag.ContinueOnError)
	c.LocalNonPersistentFlags().VisitAll(func(f *pflag.Flag) {
		if !f.Hidden && f.Name != "help" {
			fs.AddFlag(f)
		}
	})
	return fs.FlagUsages()
}

// SkillMD returns the thin SKILL.md installed into agent skill directories.
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

// dedent removes the two-space indentation cobra examples conventionally use.
func dedent(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimPrefix(l, "  ")
	}
	return strings.Join(lines, "\n")
}
