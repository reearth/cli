package visualizer

import (
	"bytes"
	"fmt"
	"regexp"

	"go.yaml.in/yaml/v3"
)

// idPattern is the plugin and extension ID rule from reearth-visualizer
// server/schemas/plugin_manifest.json.
var idPattern = regexp.MustCompile(`^[A-Za-z][\w\-:.]*$`)

// tsExtensionID is the TS template's extension ID rule (scripts/manage.mjs).
var tsExtensionID = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

// jsReservedWords are rejected as TS extension IDs, as in scripts/manage.mjs.
var jsReservedWords = []string{
	"break", "case", "catch", "class", "const", "continue", "debugger", "default",
	"delete", "do", "else", "export", "extends", "finally", "for", "function",
	"if", "import", "in", "instanceof", "let", "new", "return", "super", "switch",
	"this", "throw", "try", "typeof", "var", "void", "while", "with", "yield",
	"enum", "implements", "interface", "package", "private", "protected", "public", "static",
}

var extensionTypes = []string{"widget", "storyBlock", "infoboxBlock"}

// manifestEdit describes changes to reearth.yml. Empty fields are left as is.
type manifestEdit struct {
	ID, Name                                  string
	ExtensionID, ExtensionType, ExtensionName string
}

// editManifest rewrites reearth.yml in place, keeping key order and comments.
// Extension fields apply to the first extension only.
func editManifest(src []byte, e manifestEdit) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, fmt.Errorf("reearth.yml: %w", err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("reearth.yml: top level is not a mapping")
	}
	root := doc.Content[0]
	setScalar(root, "id", e.ID)
	setScalar(root, "name", e.Name)
	if exts := mapValue(root, "extensions"); exts != nil && exts.Kind == yaml.SequenceNode && len(exts.Content) > 0 {
		first := exts.Content[0]
		setScalar(first, "id", e.ExtensionID)
		setScalar(first, "type", e.ExtensionType)
		setScalar(first, "name", e.ExtensionName)
	}
	return encodeYAML(&doc)
}

// extensionIDs lists extension IDs in reearth.yml.
func extensionIDs(src []byte) ([]string, error) {
	var m struct {
		Extensions []struct {
			ID string `yaml:"id"`
		} `yaml:"extensions"`
	}
	if err := yaml.Unmarshal(src, &m); err != nil {
		return nil, fmt.Errorf("reearth.yml: %w", err)
	}
	ids := make([]string, 0, len(m.Extensions))
	for _, e := range m.Extensions {
		ids = append(ids, e.ID)
	}
	return ids, nil
}

func mapValue(m *yaml.Node, key string) *yaml.Node {
	if m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func setScalar(m *yaml.Node, key, value string) {
	if value == "" || m.Kind != yaml.MappingNode {
		return
	}
	if v := mapValue(m, key); v != nil {
		v.Kind, v.Tag, v.Value, v.Style = yaml.ScalarNode, "!!str", value, 0
		return
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Value: value})
}

// appendExtension adds entry (one extension as YAML) to the extensions list
// in reearth.yml, keeping the rest of the file as is.
func appendExtension(src, entry []byte) ([]byte, error) {
	var doc, e yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, fmt.Errorf("reearth.yml: %w", err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("reearth.yml: top level is not a mapping")
	}
	if err := yaml.Unmarshal(entry, &e); err != nil {
		return nil, err
	}
	plainScalars(&e)
	root := doc.Content[0]
	exts := mapValue(root, "extensions")
	if exts == nil {
		exts = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "extensions"}, exts)
	}
	if exts.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("reearth.yml: extensions is not a list")
	}
	exts.Style = 0 // "extensions: []" becomes a block list
	exts.Content = append(exts.Content, e.Content[0])
	return encodeYAML(&doc)
}

// plainScalars drops the quoting of string scalars, so the encoder quotes
// only the values that need it. Templates quote every user-supplied value;
// this keeps a plain name plain in the written file.
func plainScalars(n *yaml.Node) {
	if n.Kind == yaml.ScalarNode && n.Tag == "!!str" {
		n.Style &^= yaml.DoubleQuotedStyle | yaml.SingleQuotedStyle
	}
	for _, c := range n.Content {
		plainScalars(c)
	}
}

// plainYAML rewrites a rendered template with plainScalars.
func plainYAML(src []byte) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, err
	}
	plainScalars(&doc)
	return encodeYAML(&doc)
}

func encodeYAML(doc *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
