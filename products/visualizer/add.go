package visualizer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"text/template"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"

	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/core"
)

// extensionSpec is one extension of a js plugin.
type extensionSpec struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Name string `json:"name"`
}

// renderExtension returns the reearth.yml entry and the .js file for e.
func renderExtension(e extensionSpec) (entry, js []byte, err error) {
	if entry, err = execTemplate("templates/js/extension.yml.tmpl", e); err != nil {
		return nil, nil, err
	}
	if js, err = execTemplate("templates/js/extension.js.tmpl", e); err != nil {
		return nil, nil, err
	}
	return entry, js, nil
}

// templateFuncs are available in templates. quote writes a string as a
// double-quoted literal, which is valid in both YAML and JavaScript, so
// names such as "Demo: test" or `Bob's "map"` cannot break the output.
var templateFuncs = template.FuncMap{
	"quote": func(s string) string {
		b, _ := json.Marshal(s)
		return string(b)
	},
}

func execTemplate(name string, data any) ([]byte, error) {
	t, err := template.New(path.Base(name)).Funcs(templateFuncs).ParseFS(templates, name)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// checkNewExtensionID applies check and rejects IDs already in the plugin.
func checkNewExtensionID(check func(string) error, taken []string) func(string) error {
	return func(id string) error {
		if err := check(id); err != nil {
			return err
		}
		if slices.Contains(taken, id) {
			return fmt.Errorf("extension ID %q is already used", id)
		}
		return nil
	}
}

// askExtensionType asks for an extension type, widget first.
func askExtensionType(f *core.Factory) (string, error) {
	i, err := f.Prompter.Select("Extension type", extensionTypeChoices, 0)
	if err != nil {
		return "", err
	}
	return extensionTypes[i], nil
}

type addOptions struct {
	Dir       string        `json:"dir"`
	PluginID  string        `json:"pluginId"`
	Template  string        `json:"template"`
	Extension extensionSpec `json:"extension"`
	Files     []string      `json:"files"`
}

func newCmdPluginAdd(f *core.Factory) *cobra.Command {
	o := &addOptions{}
	cmd := &cobra.Command{
		Use:   "add [extension-id]",
		Short: "Add an extension to a plugin",
		Long: `Add an extension to the plugin in --dir.

  js plugin (reearth.yml)          adds it to reearth.yml and creates <extension-id>.js
  ts template (public/reearth.yml) adds it to public/reearth.yml, creates
                                   src/extensions/<extension-id>/ and adds its
                                   scripts to package.json, including "build"

On a terminal, the extension ID and type are asked for when not given.`,
		Example: `  $ reearth visualizer plugin add
  $ reearth visualizer plugin add legend --type widget
  $ reearth visualizer plugin add chart --type storyBlock --dir my-plugin`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				o.Extension.ID = args[0]
			}
			return o.run(f, cmd)
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&o.Dir, "dir", ".", "Plugin directory")
	fl.StringVar(&o.Extension.Type, "type", "widget", "Extension type: widget, storyBlock, infoboxBlock")
	fl.StringVar(&o.Extension.Name, "name", "", "Display name (default: extension ID)")
	return cmd
}

func (o *addOptions) run(f *core.Factory, cmd *cobra.Command) error {
	canAsk := f.IO.CanPrompt()
	e := &o.Extension

	if cmd.Flags().Changed("type") && !slices.Contains(extensionTypes, e.Type) {
		return cmdutil.FlagErrorf("--type must be one of widget, storyBlock, infoboxBlock")
	}

	// reearth.yml at the top is a js plugin; public/reearth.yml is the TS template.
	o.Template = "js"
	manifestPath := manifestFile
	src, err := os.ReadFile(filepath.Join(o.Dir, manifestPath))
	if errors.Is(err, fs.ErrNotExist) {
		o.Template, manifestPath = "ts", tsManifest
		src, err = os.ReadFile(filepath.Join(o.Dir, filepath.FromSlash(manifestPath)))
		if errors.Is(err, fs.ErrNotExist) {
			return cmdutil.NewError(cmdutil.ExitNotFound, "visualizer.manifest_not_found",
				fmt.Sprintf("%s not found in %s", manifestFile, o.Dir), "pass --dir, or create a plugin with `reearth visualizer plugin init`")
		}
	}
	if err != nil {
		return err // e.g. permission denied: report it as it is
	}
	taken, err := extensionIDs(src)
	if err != nil {
		return err
	}
	var meta struct {
		ID string `yaml:"id"`
	}
	_ = yaml.Unmarshal(src, &meta)
	o.PluginID = meta.ID

	// Files of the same name would be overwritten, so treat those IDs as taken too.
	check, existing := checkExtensionID, func(id string) string { return id + ".js" }
	if o.Template == "ts" {
		check, existing = checkTSExtensionID, func(id string) string { return "src/extensions/" + id }
	}
	checkNew := checkNewExtensionID(check, taken)
	checkAll := func(id string) error {
		if err := checkNew(id); err != nil {
			return err
		}
		if _, err := os.Stat(filepath.Join(o.Dir, filepath.FromSlash(existing(id)))); err == nil {
			return fmt.Errorf("%s already exists", existing(id))
		}
		return nil
	}
	if e.ID == "" {
		if !canAsk {
			return cmdutil.FlagErrorf("an extension ID is required: add <extension-id>")
		}
		if e.ID, err = askValid(f, "Extension ID", "", checkAll); err != nil {
			return err
		}
	}
	if err := checkAll(e.ID); err != nil {
		return cmdutil.FlagErrorf("%s", err)
	}
	if canAsk && !cmd.Flags().Changed("type") {
		if e.Type, err = askExtensionType(f); err != nil {
			return err
		}
	}
	if e.Name == "" {
		e.Name = e.ID
	}

	files := map[string][]byte{}
	if o.Template == "ts" {
		pkg, err := os.ReadFile(filepath.Join(o.Dir, "package.json"))
		if err != nil {
			return err
		}
		files[tsManifest], files["package.json"] = src, pkg
		if err := addTSExtension(files, *e); err != nil {
			return err
		}
	} else {
		entry, js, err := renderExtension(*e)
		if err != nil {
			return err
		}
		if files[manifestFile], err = appendExtension(src, entry); err != nil {
			return err
		}
		files[e.ID+".js"] = js
	}
	if err := writeFiles(o.Dir, files); err != nil {
		return err
	}
	for name := range files {
		o.Files = append(o.Files, name)
	}
	slices.Sort(o.Files)

	pr, err := f.Printer()
	if err != nil {
		return err
	}
	return pr.Print(o, func() error {
		cs := f.IO.Color()
		_, _ = fmt.Fprintf(f.IO.Out, "%s Added %s (%s) to %s (%d files)\n", cs.Accent("✓"), cs.Bold(e.ID), e.Type, o.PluginID, len(o.Files))
		if o.Template == "ts" {
			_, _ = fmt.Fprintf(f.IO.ErrOut, "\nNext:\n  yarn build:%s   # or yarn build for the zip\n", e.ID)
		}
		return nil
	})
}
