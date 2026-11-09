package visualizer

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"

	"github.com/reearth/cli/sdk/cmdutil"
	"github.com/reearth/cli/sdk/core"
)

// The manifest types follow reearth-visualizer server/schemas/plugin_manifest.json.
// Every object there has additionalProperties: false, so unknown keys are
// rejected (yaml KnownFields).
type mRoot struct {
	ID          string       `yaml:"id"`
	Name        string       `yaml:"name"`
	System      bool         `yaml:"system"`
	Version     string       `yaml:"version"`
	Description *string      `yaml:"description"`
	Repository  *string      `yaml:"repository"`
	Author      *string      `yaml:"author"`
	Main        *string      `yaml:"main"`
	Extensions  []mExtension `yaml:"extensions"`
	Schema      *mSchema     `yaml:"schema"`
}

type mExtension struct {
	ID           string         `yaml:"id"`
	Name         string         `yaml:"name"`
	Description  *string        `yaml:"description"`
	Icon         *string        `yaml:"icon"`
	Visualizer   *string        `yaml:"visualizer"`
	Type         string         `yaml:"type"`
	SingleOnly   *bool          `yaml:"singleOnly"`
	WidgetLayout *mWidgetLayout `yaml:"widgetLayout"`
	Schema       *mSchema       `yaml:"schema"`
}

type mWidgetLayout struct {
	Extendable *struct {
		Vertically   *bool `yaml:"vertically"`
		Horizontally *bool `yaml:"horizontally"`
	} `yaml:"extendable"`
	Extended        *bool `yaml:"extended"`
	Floating        bool  `yaml:"floating"`
	DefaultLocation *struct {
		Zone    string `yaml:"zone"`
		Section string `yaml:"section"`
		Area    string `yaml:"area"`
	} `yaml:"defaultLocation"`
}

type mSchema struct {
	Version  float64 `yaml:"version"`
	Linkable *struct {
		LatLng *mPointer `yaml:"latlng"`
		URL    *mPointer `yaml:"url"`
	} `yaml:"linkable"`
	Groups []mGroup `yaml:"groups"`
}

type mPointer struct {
	SchemaGroupID string `yaml:"schemaGroupId"`
	FieldID       string `yaml:"fieldId"`
}

type mGroup struct {
	ID                  string      `yaml:"id"`
	Collection          *string     `yaml:"collection"`
	Title               string      `yaml:"title"`
	Description         *string     `yaml:"description"`
	List                bool        `yaml:"list"`
	AvailableIf         *mCondition `yaml:"availableIf"`
	RepresentativeField *string     `yaml:"representativeField"`
	Fields              []mField    `yaml:"fields"`
}

type mField struct {
	ID           string      `yaml:"id"`
	Title        string      `yaml:"title"`
	Description  *string     `yaml:"description"`
	Placeholder  *string     `yaml:"placeholder"`
	Type         string      `yaml:"type"`
	Prefix       *string     `yaml:"prefix"`
	Suffix       *string     `yaml:"suffix"`
	DefaultValue any         `yaml:"defaultValue"`
	UI           *string     `yaml:"ui"`
	Min          *float64    `yaml:"min"`
	Max          *float64    `yaml:"max"`
	Choices      []mChoice   `yaml:"choices"`
	AvailableIf  *mCondition `yaml:"availableIf"`
}

type mChoice struct {
	Key   string `yaml:"key"`
	Label string `yaml:"label"`
	Icon  string `yaml:"icon"`
}

type mCondition struct {
	Field string `yaml:"field"`
	Type  string `yaml:"type"`
	Value any    `yaml:"value"`
}

var (
	allExtensionTypes = []string{"primitive", "widget", "block", "visualizer", "infobox", "cluster", "story", "storyPage", "storyBlock", "infoboxBlock", "photoOverlay"}
	valueTypes        = []string{"bool", "number", "string", "url", "latlng", "latlngheight", "camera", "typography", "coordinates", "polygon", "rect", "ref", "spacing", "array", "timeline"}
	fieldUIs          = []string{"layer", "color", "multiline", "selection", "buttons", "range", "slider", "image", "video", "file", "camera_pose", "datetime", "padding", "margin", "property_selector", "zoomLevel"}

	// pluginNamePattern is stricter than the schema's ID rule: the server
	// (pkg/id/plugin.go) accepts only these characters, up to 100 of them.
	pluginNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	// semverPattern is the regular expression from semver.org; the server
	// parses version with github.com/blang/semver.
	semverPattern  = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-((?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?(?:\+([0-9a-zA-Z-]+(?:\.[0-9a-zA-Z-]+)*))?$`)
	reUnknownField = regexp.MustCompile(`field (\S+) not found in type \S+`)
)

type issue struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

type validation struct {
	Dir      string  `json:"dir"`
	Template string  `json:"template"`
	Manifest string  `json:"manifest"`
	ID       string  `json:"id"`
	Version  string  `json:"version"`
	Valid    bool    `json:"valid"`
	Issues   []issue `json:"issues"`

	root     string // where the extension .js files are
	manifest *mRoot
	src      []byte
}

func (v *validation) add(path, format string, args ...any) {
	v.Issues = append(v.Issues, issue{Path: path, Message: fmt.Sprintf(format, args...)})
}

// validatePlugin checks the plugin in dir the way Visualizer reads an
// uploaded package: the manifest against the schema and the server's rules,
// and an <extension-id>.js for each extension (ts: in dist/).
func validatePlugin(dir string) (*validation, error) {
	template, manifest, root, err := devPaths(dir)
	if err != nil {
		return nil, err
	}
	src, err := os.ReadFile(manifest)
	if err != nil {
		return nil, err
	}
	rel, _ := filepath.Rel(dir, manifest)
	v := &validation{Dir: dir, Template: template, Manifest: filepath.ToSlash(rel), Issues: []issue{}, root: root, src: src}

	var m mRoot
	dec := yaml.NewDecoder(bytes.NewReader(src))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		var te *yaml.TypeError
		if errors.As(err, &te) {
			for _, e := range te.Errors {
				v.add("", "%s", reUnknownField.ReplaceAllString(e, "unknown key $1"))
			}
		} else {
			v.add("", "not valid YAML: %v", err)
		}
		return v, nil
	}
	v.manifest, v.ID, v.Version = &m, m.ID, m.Version
	v.checkRoot(&m)
	v.checkFiles(&m)
	v.Valid = len(v.Issues) == 0
	return v, nil
}

func (v *validation) checkRoot(m *mRoot) {
	switch {
	case m.ID == "":
		v.add("id", "is required")
	case m.ID == "reearth":
		v.add("id", `"reearth" is reserved`)
	case len(m.ID) > 100 || !pluginNamePattern.MatchString(m.ID):
		v.add("id", "%q must be 1 to 100 letters, digits, _ or -", m.ID)
	}
	if m.Name == "" {
		v.add("name", "is required")
	}
	switch {
	case m.Version == "":
		v.add("version", "is required, such as 1.0.0")
	case !semverPattern.MatchString(m.Version):
		v.add("version", "%q is not a semantic version, such as 1.0.0", m.Version)
	}
	v.checkSchema("schema", m.Schema)

	var seen []string
	for i, e := range m.Extensions {
		p := fmt.Sprintf("extensions[%d]", i)
		switch {
		case e.ID == "":
			v.add(p+".id", "is required")
		case !idPattern.MatchString(e.ID):
			v.add(p+".id", "%q must match %s", e.ID, idPattern)
		case slices.Contains(seen, e.ID):
			v.add(p+".id", "%q is used by another extension", e.ID)
		}
		seen = append(seen, e.ID)
		if e.Name == "" {
			v.add(p+".name", "is required")
		}
		switch {
		case e.Type == "":
			v.add(p+".type", "is required")
		case !slices.Contains(allExtensionTypes, e.Type):
			v.add(p+".type", "%q is not one of %s", e.Type, strings.Join(allExtensionTypes, ", "))
		}
		if e.Visualizer != nil && *e.Visualizer != "cesium" {
			v.add(p+".visualizer", "%q is not cesium", *e.Visualizer)
		} else if e.Visualizer == nil && e.Type == "visualizer" {
			v.add(p+".visualizer", "is required for a visualizer extension")
		}
		if l := e.WidgetLayout; l != nil && l.DefaultLocation != nil {
			d := l.DefaultLocation
			v.checkEnum(p+".widgetLayout.defaultLocation.zone", d.Zone, "inner", "outer")
			v.checkEnum(p+".widgetLayout.defaultLocation.section", d.Section, "left", "center", "right")
			v.checkEnum(p+".widgetLayout.defaultLocation.area", d.Area, "top", "middle", "bottom")
		}
		v.checkSchema(p+".schema", e.Schema)
	}
}

func (v *validation) checkEnum(path, value string, allowed ...string) {
	if value != "" && !slices.Contains(allowed, value) {
		v.add(path, "%q is not one of %s", value, strings.Join(allowed, ", "))
	}
}

func (v *validation) checkSchema(path string, s *mSchema) {
	if s == nil {
		return
	}
	fields := map[string][]string{} // group ID -> field IDs
	for i, g := range s.Groups {
		p := fmt.Sprintf("%s.groups[%d]", path, i)
		switch {
		case g.ID == "":
			v.add(p+".id", "is required")
		case !idPattern.MatchString(g.ID):
			v.add(p+".id", "%q must match %s", g.ID, idPattern)
		default:
			if _, dup := fields[g.ID]; dup {
				v.add(p+".id", "%q is used by another group", g.ID)
			}
		}
		if g.Title == "" {
			v.add(p+".title", "is required")
		}
		v.checkCondition(p+".availableIf", g.AvailableIf)
		var ids []string
		for j, f := range g.Fields {
			fp := fmt.Sprintf("%s.fields[%d]", p, j)
			switch {
			case f.ID == "":
				v.add(fp+".id", "is required")
			case !idPattern.MatchString(f.ID):
				v.add(fp+".id", "%q must match %s", f.ID, idPattern)
			case slices.Contains(ids, f.ID):
				v.add(fp+".id", "%q is used by another field in this group", f.ID)
			}
			ids = append(ids, f.ID)
			if f.Title == "" {
				v.add(fp+".title", "is required")
			}
			switch {
			case f.Type == "":
				v.add(fp+".type", "is required")
			case !slices.Contains(valueTypes, f.Type):
				v.add(fp+".type", "%q is not one of %s", f.Type, strings.Join(valueTypes, ", "))
			}
			if f.UI != nil {
				v.checkEnum(fp+".ui", *f.UI, fieldUIs...)
			}
			for k, c := range f.Choices {
				if c.Key == "" {
					v.add(fmt.Sprintf("%s.choices[%d].key", fp, k), "is required")
				}
			}
			v.checkCondition(fp+".availableIf", f.AvailableIf)
		}
		fields[g.ID] = ids
	}
	// The server refuses linkable fields that point at no field.
	if l := s.Linkable; l != nil {
		for name, ptr := range map[string]*mPointer{"latlng": l.LatLng, "url": l.URL} {
			if ptr == nil {
				continue
			}
			if !slices.Contains(fields[ptr.SchemaGroupID], ptr.FieldID) {
				v.add(path+".linkable."+name, "points at %s/%s, which is not a field of this schema", ptr.SchemaGroupID, ptr.FieldID)
			}
		}
	}
}

func (v *validation) checkCondition(path string, c *mCondition) {
	if c == nil {
		return
	}
	if c.Field == "" {
		v.add(path+".field", "is required")
	}
	if !slices.Contains(valueTypes, c.Type) {
		v.add(path+".type", "%q is not one of %s", c.Type, strings.Join(valueTypes, ", "))
	}
	if c.Value == nil {
		v.add(path+".value", "is required")
	}
}

// checkFiles requires the built .js of each extension.
func (v *validation) checkFiles(m *mRoot) {
	if v.Template == "ts" {
		if _, err := os.Stat(v.root); err != nil {
			v.add("dist", "not found; build the plugin with `yarn build`")
			return
		}
	}
	for i, e := range m.Extensions {
		if e.ID == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(v.root, e.ID+".js")); err != nil {
			rel, _ := filepath.Rel(v.Dir, filepath.Join(v.root, e.ID+".js"))
			v.add(fmt.Sprintf("extensions[%d]", i), "%s not found", filepath.ToSlash(rel))
		}
	}
}

// invalidError is returned after the issues are printed.
func invalidError(v *validation) error {
	n := len(v.Issues)
	s := "s"
	if n == 1 {
		s = ""
	}
	return cmdutil.NewError(cmdutil.ExitError, "visualizer.invalid_plugin",
		fmt.Sprintf("the plugin in %s has %d problem%s", v.Dir, n, s), "fix them and run `reearth visualizer plugin validate` again")
}

func newCmdPluginValidate(f *core.Factory) *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Check a plugin's manifest and files before uploading",
		Long: `Check the plugin in --dir the way Visualizer reads an uploaded plugin:

  - reearth.yml (ts: public/reearth.yml) against the plugin manifest schema
    (plugin_manifest.json): required keys, allowed values, no unknown keys
  - the server's own rules: the plugin ID uses only letters, digits, _ and -,
    version is a semantic version, and group and field IDs are unique
  - an <extension-id>.js for every extension (ts: in dist/, after yarn build)

Each problem is printed with its path in the manifest. Exits with 1 when
there are any.`,
		Example: `  $ reearth visualizer plugin validate
  $ reearth visualizer plugin validate --dir my-plugin --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			v, err := validatePlugin(dir)
			if err != nil {
				return err
			}
			pr, err := f.Printer()
			if err != nil {
				return err
			}
			if err := pr.Print(v, func() error {
				if v.Valid {
					return nil
				}
				t := pr.Table("PATH", "PROBLEM")
				for _, i := range v.Issues {
					t.Row(i.Path, i.Message)
				}
				return t.Render()
			}); err != nil {
				return err
			}
			if !v.Valid {
				return invalidError(v)
			}
			f.IO.Success("%s %s is valid", v.ID, v.Version)
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "Plugin directory")
	return cmd
}
