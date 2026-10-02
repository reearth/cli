package visualizer

import (
	"archive/zip"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"

	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/core"
)

const manifestFile = "reearth.yml"

// The rules the Visualizer server applies (pkg/id/plugin.go and
// schemas/plugin_manifest.json), checked here so that mistakes show up
// before an upload. Extension IDs are also file names, so they are kept to
// characters that are safe in one.
var (
	pluginIDRe    = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)
	extensionIDRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)
	versionRe     = regexp.MustCompile(`^\d+\.\d+\.\d+([-+][0-9A-Za-z.-]+)?$`)
)

var extensionTypes = []string{"widget", "storyBlock", "infoboxBlock"}

type manifest struct {
	ID         string              `yaml:"id" json:"id"`
	Name       string              `yaml:"name" json:"name"`
	Version    string              `yaml:"version" json:"version"`
	Extensions []manifestExtension `yaml:"extensions" json:"extensions"`
}

type manifestExtension struct {
	ID   string `yaml:"id" json:"id"`
	Type string `yaml:"type" json:"type"`
	Name string `yaml:"name" json:"name"`
}

func newCmdPlugin(f *core.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugin",
		Short: "Create, check and package Visualizer plugins",
		Long: `Create, check and package Visualizer plugins.

A plugin is a directory with reearth.yml and one <extension-id>.js per
extension. The server keeps only reearth.yml and .js files, so HTML and CSS
must live inside the JS.`,
	}
	cmd.AddCommand(newCmdPluginInit(f), newCmdPluginValidate(f), newCmdPluginPack(f))
	return cmd
}

func newCmdPluginInit(f *core.Factory) *cobra.Command {
	var id, name, extID, extType string
	cmd := &cobra.Command{
		Use:   "init <dir>",
		Short: "Create a plugin from a template",
		Long: `Create a plugin with reearth.yml and one extension in <dir>.

The plugin ID defaults to the directory name and the extension ID to the
plugin ID. It never asks questions, so agents and CI can use it.`,
		Example: `  $ reearth vis plugin init hello-widget
  $ reearth vis plugin init my-block --type storyBlock --name "My Block"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := args[0]
			if id == "" {
				id = filepath.Base(filepath.Clean(dir))
			}
			if extID == "" {
				extID = id
			}
			if name == "" {
				name = id
			}
			m := manifest{
				ID:         id,
				Name:       name,
				Version:    "1.0.0",
				Extensions: []manifestExtension{{ID: extID, Type: extType, Name: name}},
			}
			if err := m.validate(); err != nil {
				return cmdutil.FlagErrorf("%v", err)
			}
			if _, err := os.Stat(filepath.Join(dir, manifestFile)); err == nil {
				return cmdutil.FlagErrorf("%s already has a %s", dir, manifestFile)
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			files := map[string]string{
				manifestFile:  manifestTemplate(m),
				extID + ".js": jsTemplate(extType, name),
			}
			for n, c := range files {
				if err := os.WriteFile(filepath.Join(dir, n), []byte(c), 0o644); err != nil {
					return err
				}
			}
			p, err := f.Printer()
			if err != nil {
				return err
			}
			return p.Print(map[string]any{"dir": dir, "manifest": m}, func() error {
				f.IO.Success("Created plugin %s in %s", id, dir)
				f.IO.Hint("check it with: reearth vis plugin validate %s", dir)
				return nil
			})
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&id, "id", "", "Plugin ID (default: the directory name)")
	fl.StringVar(&name, "name", "", "Display name (default: the plugin ID)")
	fl.StringVar(&extID, "extension-id", "", "Extension ID, also its file name (default: the plugin ID)")
	fl.StringVar(&extType, "type", "widget", "Extension type: "+strings.Join(extensionTypes, ", "))
	return cmd
}

func newCmdPluginValidate(f *core.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "validate [dir]",
		Short: "Check a plugin before packing it",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := dirArg(args)
			m, err := loadPlugin(dir)
			if err != nil {
				return err
			}
			p, err := f.Printer()
			if err != nil {
				return err
			}
			return p.Print(m, func() error {
				f.IO.Success("%s %s is valid (%d extension(s))", m.ID, m.Version, len(m.Extensions))
				return nil
			})
		},
	}
}

func newCmdPluginPack(f *core.Factory) *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "pack [dir]",
		Short: "Validate a plugin and zip it for upload",
		Long: `Validate a plugin and write a zip with reearth.yml at its root and the
.js files, the only files the server keeps. Prints the zip path.`,
		Example: `  $ reearth vis plugin pack hello-widget
  $ reearth vis plugin pack . -o dist/plugin.zip`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := dirArg(args)
			m, err := loadPlugin(dir)
			if err != nil {
				return err
			}
			if out == "" {
				out = fmt.Sprintf("%s-%s.zip", m.ID, m.Version)
			}
			n, err := writeZip(dir, out)
			if err != nil {
				return err
			}
			p, err := f.Printer()
			if err != nil {
				return err
			}
			return p.Print(map[string]any{"path": out, "files": n, "id": m.ID, "version": m.Version}, func() error {
				_, err := fmt.Fprintln(f.IO.Out, out)
				return err
			})
		},
	}
	cmd.Flags().StringVarP(&out, "output", "o", "", "Zip path (default: <id>-<version>.zip)")
	return cmd
}

func dirArg(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	return "."
}

// loadPlugin reads and validates the plugin in dir.
func loadPlugin(dir string) (*manifest, error) {
	b, err := os.ReadFile(filepath.Join(dir, manifestFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, cmdutil.FlagErrorf("no %s in %s (create one with: reearth vis plugin init)", manifestFile, dir)
	} else if err != nil {
		return nil, err
	}
	var m manifest
	if err := yaml.Unmarshal(b, &m); err != nil {
		return nil, cmdutil.FlagErrorf("%s: %v", manifestFile, err)
	}
	if err := m.validate(); err != nil {
		return nil, cmdutil.FlagErrorf("%s: %v", manifestFile, err)
	}
	for _, e := range m.Extensions {
		if _, err := os.Stat(filepath.Join(dir, e.ID+".js")); err != nil {
			return nil, cmdutil.FlagErrorf("extension %q needs %s.js in %s", e.ID, e.ID, dir)
		}
	}
	return &m, nil
}

func (m manifest) validate() error {
	if !pluginIDRe.MatchString(m.ID) || m.ID == "reearth" {
		return fmt.Errorf("invalid plugin id %q (letters, digits, '_' and '-', not \"reearth\")", m.ID)
	}
	if !versionRe.MatchString(m.Version) {
		return fmt.Errorf("invalid version %q (want semver, e.g. 1.0.0)", m.Version)
	}
	if len(m.Extensions) == 0 {
		return errors.New("a plugin needs at least one extension")
	}
	seen := map[string]bool{}
	for _, e := range m.Extensions {
		if !extensionIDRe.MatchString(e.ID) {
			return fmt.Errorf("invalid extension id %q (start with a letter; letters, digits, '_' and '-')", e.ID)
		}
		if seen[e.ID] {
			return fmt.Errorf("duplicate extension id %q", e.ID)
		}
		seen[e.ID] = true
		if e.Type == "" {
			return fmt.Errorf("extension %q has no type", e.ID)
		}
	}
	return nil
}

// writeZip zips reearth.yml and every .js file under dir, with paths relative
// to dir. It returns the number of files written.
func writeZip(dir, out string) (n int, err error) {
	absOut, _ := filepath.Abs(out)
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return 0, err
	}
	file, err := os.Create(out)
	if err != nil {
		return 0, err
	}
	defer func() {
		if cerr := file.Close(); err == nil {
			err = cerr
		}
	}()
	zw := zip.NewWriter(file)
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		if d.IsDir() {
			if path != dir && (strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if abs, _ := filepath.Abs(path); abs == absOut {
			return nil
		}
		if rel != manifestFile && filepath.Ext(path) != ".js" {
			return nil
		}
		w, err := zw.Create(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if _, err := w.Write(b); err != nil {
			return err
		}
		n++
		return nil
	})
	if err != nil {
		return 0, err
	}
	return n, zw.Close()
}

func manifestTemplate(m manifest) string {
	e := m.Extensions[0]
	s := fmt.Sprintf(`id: %s
name: %s
version: %s
extensions:
  - id: %s
    type: %s
    name: %s
`, m.ID, yamlString(m.Name), m.Version, e.ID, e.Type, yamlString(e.Name))
	if e.Type == "widget" {
		s += `    widgetLayout:
      defaultLocation:
        zone: inner
        section: left
        area: top
`
	}
	return s
}

func yamlString(s string) string {
	b, _ := yaml.Marshal(s)
	return strings.TrimSpace(string(b))
}

func jsTemplate(extType, name string) string {
	return fmt.Sprintf(`// %[1]s: a %[2]s extension. The HTML lives in the JS because the server
// keeps only reearth.yml and .js files.
reearth.ui.show(`+"`"+`
  <style>
    body { margin: 0; font-family: sans-serif; }
    .card { padding: 12px 16px; background: #fff; border-radius: 8px; }
  </style>
  <div class="card">
    <strong>%[1]s</strong>
    <p>Edit this file, then run: reearth vis plugin pack</p>
  </div>
`+"`"+`, { visible: true });
`, name, extType)
}
