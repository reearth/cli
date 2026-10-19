package visualizer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/spf13/cobra"

	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/core"
)

const manifestFile = "reearth.yml"

type initOptions struct {
	Dir         string   `json:"dir"`
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	ExtensionID string   `json:"extensionId,omitempty"`
	Type        string   `json:"type,omitempty"`
	Template    string   `json:"template"`
	Files       []string `json:"files"`

	// More are the extensions after the first, added while prompting.
	More []extensionSpec `json:"moreExtensions,omitempty"`

	refresh bool
}

func newCmdPluginInit(f *core.Factory) *cobra.Command {
	o := &initOptions{}
	cmd := &cobra.Command{
		Use:   "init [dir]",
		Short: "Create a plugin from the js or ts template",
		Long: `Create a plugin in [dir].

  --template js   reearth.yml and <extension-id>.js (default)
  --template ts   the official Vite + React + TypeScript template

On a terminal, values not given as an argument or flag are asked for, like
create-vite. With --no-input or without a terminal nothing is asked: [dir]
is required and the rest use defaults.`,
		Example: `  $ reearth visualizer plugin init
  $ reearth visualizer plugin init my-plugin
  $ reearth visualizer plugin init my-plugin --extension-id hoge --type widget
  $ reearth visualizer plugin init my-plugin --template ts --no-input`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				o.Dir = args[0]
			}
			x := newFetcher(f, o.refresh)
			if err := o.complete(f, cmd); err != nil {
				return err
			}
			f.IO.StartProgress("Creating plugin…")
			files, err := o.render(cmd.Context(), x)
			f.IO.StopProgress()
			if err != nil {
				return err
			}
			if err := checkConflicts(o.Dir, files); err != nil {
				return err
			}
			_, statErr := os.Stat(o.Dir)
			if err := writeFiles(o.Dir, files); err != nil {
				if os.IsNotExist(statErr) {
					_ = os.RemoveAll(o.Dir) // don't leave a half-written plugin behind
				}
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
				_, _ = fmt.Fprintf(f.IO.Out, "%s Created %s in %s (%d files)\n", cs.Accent("✓"), cs.Bold(o.ID), o.Dir, len(o.Files))
				if o.Template == "ts" {
					_, _ = fmt.Fprintf(f.IO.ErrOut, "\nNext:\n  cd %s\n  yarn install\n  yarn build\n", o.Dir)
				}
				return nil
			})
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&o.ID, "id", "", "Plugin ID (default: directory name)")
	fl.StringVar(&o.Name, "name", "", "Display name (default: plugin ID)")
	fl.StringVar(&o.ExtensionID, "extension-id", "", "Extension ID; for js also the .js file name (default: plugin ID)")
	fl.StringVar(&o.Type, "type", "widget", "Extension type: widget, storyBlock, infoboxBlock")
	fl.StringVar(&o.Template, "template", "js", "Template: js or ts")
	fl.BoolVar(&o.refresh, "refresh", false, "Download the ts template again instead of using the cache")
	return cmd
}

var (
	templateChoices = []string{
		"JavaScript  reearth.yml + one .js file",
		"TypeScript  Vite + React + shadcn/ui",
	}
	extensionTypeChoices = []string{
		"widget        UI placed on the map",
		"storyBlock    a block in a story",
		"infoboxBlock  a block in a layer's infobox",
	}
)

// complete fills in missing values in the order create-vite asks them:
// flag or argument > prompt (terminal, no --no-input) > default. Explicit values
// are validated as they are; prompted values are asked again until valid.
func (o *initOptions) complete(f *core.Factory, cmd *cobra.Command) error {
	canAsk := f.IO.CanPrompt() // false with --no-input, in agents and in CI
	ask := func(flag string) bool { return canAsk && !cmd.Flags().Changed(flag) }

	if o.Template != "js" && o.Template != "ts" {
		return cmdutil.FlagErrorf("--template must be js or ts")
	}
	if cmd.Flags().Changed("type") && !slices.Contains(extensionTypes, o.Type) {
		return cmdutil.FlagErrorf("--type must be one of widget, storyBlock, infoboxBlock")
	}

	// Plugin ID and directory.
	var err error
	switch {
	case o.Dir == "" && o.ID == "":
		if !canAsk {
			return cmdutil.FlagErrorf("a directory is required: init <dir>")
		}
		if o.ID, err = askValid(f, "Plugin ID", "my-plugin", func(s string) error {
			if err := checkPluginID(s); err != nil {
				return err
			}
			return checkDir(s)
		}); err != nil {
			return err
		}
		o.Dir = o.ID
	case o.Dir == "":
		o.Dir = o.ID
	case o.ID == "":
		abs, err := filepath.Abs(o.Dir)
		if err != nil {
			return err
		}
		o.ID = filepath.Base(abs)
		// A directory like "My Plugin" is fine; its name just can't be the ID.
		if checkPluginID(o.ID) != nil && canAsk {
			if o.ID, err = askValid(f, "Plugin ID", "", checkPluginID); err != nil {
				return err
			}
		}
	}
	if err := checkPluginID(o.ID); err != nil {
		return cmdutil.FlagErrorf("%s (pass --id)", err)
	}
	if err := checkDir(o.Dir); err != nil {
		return cmdutil.NewError(cmdutil.ExitUsage, "visualizer.plugin_exists", err.Error(), "choose an empty or new directory")
	}

	if o.Name == "" {
		o.Name = o.ID
		if ask("name") {
			if o.Name, err = f.Prompter.Input("Plugin name", o.ID); err != nil {
				return err
			}
		}
	}

	if ask("template") {
		i, err := f.Prompter.Select("Template", templateChoices, 0)
		if err != nil {
			return err
		}
		o.Template = []string{"js", "ts"}[i]
	}

	check, def := checkExtensionID, o.ID
	if o.Template == "ts" {
		// The template's Vite config uses the extension ID as the IIFE global name.
		check, def = checkTSExtensionID, camelCase(o.ID)
	}
	if o.ExtensionID == "" {
		o.ExtensionID = def
		if ask("extension-id") {
			if check(def) != nil {
				def = ""
			}
			if o.ExtensionID, err = askValid(f, "Extension ID", def, check); err != nil {
				return err
			}
		}
	}
	if err := check(o.ExtensionID); err != nil {
		return cmdutil.FlagErrorf("%s; pass --extension-id", err)
	}
	if ask("type") {
		if o.Type, err = askExtensionType(f); err != nil {
			return err
		}
	}

	if !canAsk {
		return nil
	}
	taken := []string{o.ExtensionID}
	for {
		more, err := f.Prompter.Confirm("Add another extension?", false)
		if err != nil || !more {
			return err
		}
		e := extensionSpec{}
		if e.ID, err = askValid(f, "Extension ID", "", checkNewExtensionID(check, taken)); err != nil {
			return err
		}
		if e.Type, err = askExtensionType(f); err != nil {
			return err
		}
		e.Name = e.ID
		o.More = append(o.More, e)
		taken = append(taken, e.ID)
	}
}

// askValid prompts until check accepts the answer.
func askValid(f *core.Factory, label, def string, check func(string) error) (string, error) {
	for {
		v, err := f.Prompter.Input(label, def)
		if err != nil {
			return "", err
		}
		err = check(v)
		if err == nil {
			return v, nil
		}
		_, _ = fmt.Fprintf(f.IO.ErrOut, "    %s %s\n", f.IO.ErrColor().FailureIcon(), err)
	}
}

// checkPluginID applies both the manifest schema's ID rule and the server's
// stricter one (see validate.go), so init never creates a plugin that
// validate and pack reject.
func checkPluginID(id string) error {
	if id == "" {
		return fmt.Errorf("plugin ID is required")
	}
	if id == "reearth" {
		return fmt.Errorf("plugin ID \"reearth\" is reserved")
	}
	if len(id) > 100 || !idPattern.MatchString(id) || !pluginNamePattern.MatchString(id) {
		return fmt.Errorf("plugin ID %q must start with a letter and use only letters, digits, _ and -, up to 100 characters", id)
	}
	return nil
}

func checkExtensionID(id string) error {
	if !idPattern.MatchString(id) {
		return fmt.Errorf("extension ID %q must match %s", id, idPattern)
	}
	return nil
}

// checkTSExtensionID follows scripts/manage.mjs in the TS template.
func checkTSExtensionID(id string) error {
	if !tsExtensionID.MatchString(id) || slices.Contains(jsReservedWords, id) {
		return fmt.Errorf("with the ts template, extension ID %q must be letters, digits and _ starting with a letter (e.g. layerList), and not a JavaScript reserved word", id)
	}
	return nil
}

// checkDir refuses a directory that already holds a plugin or a JS project.
func checkDir(dir string) error {
	for _, marker := range []string{manifestFile, "public/" + manifestFile, "package.json"} {
		if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
			return fmt.Errorf("%s already exists in %s", marker, dir)
		}
	}
	return nil
}

// checkConflicts refuses to overwrite any file that is already in dir, such
// as a README.md or .gitignore of an existing repository.
func checkConflicts(dir string, files map[string][]byte) error {
	var exist []string
	for name := range files {
		if _, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(name))); err == nil {
			exist = append(exist, name)
		}
	}
	if len(exist) == 0 {
		return nil
	}
	slices.Sort(exist)
	if len(exist) > 5 {
		exist = append(exist[:5], fmt.Sprintf("and %d more", len(exist)-5))
	}
	return cmdutil.NewError(cmdutil.ExitUsage, "visualizer.files_exist",
		fmt.Sprintf("%s already has files the plugin would overwrite: %s", dir, strings.Join(exist, ", ")),
		"choose an empty or new directory")
}

// render returns the files to write, keyed by slash-separated path.
func (o *initOptions) render(ctx context.Context, x *fetcher) (map[string][]byte, error) {
	if o.Template == "ts" {
		return renderTS(ctx, x, o)
	}
	return o.renderJS()
}

// renderJS writes reearth.yml and one <extension-id>.js per extension from
// the embedded templates. The first extension is named after the plugin.
func (o *initOptions) renderJS() (map[string][]byte, error) {
	manifest, err := execTemplate("templates/js/reearth.yml.tmpl", o)
	if err != nil {
		return nil, err
	}
	if manifest, err = plainYAML(manifest); err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	exts := append([]extensionSpec{{ID: o.ExtensionID, Type: o.Type, Name: o.Name}}, o.More...)
	for _, e := range exts {
		entry, js, err := renderExtension(e)
		if err != nil {
			return nil, err
		}
		if manifest, err = appendExtension(manifest, entry); err != nil {
			return nil, err
		}
		out[e.ID+".js"] = js
	}
	out[manifestFile] = manifest
	return out, nil
}

func writeFiles(dir string, files map[string][]byte) error {
	for name, b := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// camelCase turns "my-plugin.v2" into "myPluginV2" (a valid JS identifier
// when the input starts with a letter).
func camelCase(s string) string {
	var b strings.Builder
	upper := false
	for _, r := range s {
		switch {
		case r == '-' || r == '.' || r == ':' || r == '_':
			upper = b.Len() > 0
		case upper:
			b.WriteRune(unicode.ToUpper(r))
			upper = false
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
