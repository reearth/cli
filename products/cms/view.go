package cms

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mattn/go-runewidth"
	cmsapi "github.com/reearth/reearth-cms-api/go"

	"github.com/reearth/cli/sdk/core"
	"github.com/reearth/cli/sdk/output"
)

// detail renders one object as label and value rows, skipping empty values.
func detail(f *core.Factory, p *output.Printer, rows ...[2]string) error {
	cs := f.IO.Color()
	t := p.Table()
	for _, r := range rows {
		if r[1] != "" {
			t.Row(output.Cell{Text: r[0], Style: cs.Dim}, r[1])
		}
	}
	return t.Render()
}

// printItem prints an item: as the API returns it for machines, and for
// people as its details followed by one row per field.
func printItem(f *core.Factory, it *cmsapi.Item) error {
	p, err := f.Printer()
	if err != nil {
		return err
	}
	return p.Print(it, func() error {
		if err := detail(f, p,
			[2]string{"id", it.ID},
			[2]string{"model", it.ModelID},
			[2]string{"created", p.Time(it.CreatedAt)},
			[2]string{"updated", p.Time(it.UpdatedAt)},
		); err != nil {
			return err
		}
		for _, sec := range []struct {
			title  string
			fields []*cmsapi.Field
		}{{"FIELDS", it.Fields}, {"METADATA", it.MetadataFields}} {
			rows := fieldRows(sec.fields)
			if len(rows) == 0 {
				continue
			}
			f.IO.Newline()
			t := p.Table("KEY", "TYPE", "VALUE")
			for _, r := range rows {
				t.Row(r[0], r[1], r[2])
			}
			if err := t.Render(); err != nil {
				return err
			}
		}
		return nil
	})
}

// fieldRows flattens fields into key, type and value rows. The fields of a
// group are listed under the group's key, as key[i].field.
func fieldRows(fields []*cmsapi.Field) [][3]string {
	groups := map[string][]*cmsapi.Field{}
	var top []*cmsapi.Field
	for _, fd := range fields {
		if fd.Group != "" {
			groups[fd.Group] = append(groups[fd.Group], fd)
		} else {
			top = append(top, fd)
		}
	}
	var rows [][3]string
	var walk func(prefix string, fields []*cmsapi.Field)
	walk = func(prefix string, fields []*cmsapi.Field) {
		for _, fd := range fields {
			key := prefix + fd.Key
			if fd.Type != "group" {
				rows = append(rows, [3]string{key, fd.Type, formatValue(fd.Value)})
				continue
			}
			for i, gid := range stringsOf(fd.Value) {
				walk(fmt.Sprintf("%s[%d].", key, i), groups[gid])
			}
		}
	}
	walk("", top)
	return rows
}

// stringsOf reads a group field's value: one group ID or a list of them.
func stringsOf(v any) []string {
	switch v := v.(type) {
	case string:
		return []string{v}
	case []string:
		return v
	case []any:
		var out []string
		for _, e := range v {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// formatValue shows a field value on one line.
func formatValue(v any) string {
	var s string
	switch v := v.(type) {
	case nil:
		return ""
	case string:
		s = v
	case []any:
		parts := make([]string, len(v))
		for i, e := range v {
			parts[i] = formatValue(e)
		}
		s = strings.Join(parts, ", ")
	case map[string]any:
		b, _ := json.Marshal(v)
		s = string(b)
	default:
		s = fmt.Sprint(v)
	}
	return strings.Join(strings.Fields(s), " ")
}

// itemTable lays items out with a column per top-level field, in the order
// the fields first appear. Groups are left out; `item get` shows them.
type itemTable struct {
	keys   []string
	values []map[string]string
}

func newItemTable(items []cmsapi.Item) itemTable {
	var t itemTable
	seen := map[string]bool{}
	for _, it := range items {
		row := map[string]string{}
		for _, fd := range it.Fields {
			if fd.Group != "" || fd.Type == "group" {
				continue
			}
			if !seen[fd.Key] {
				seen[fd.Key] = true
				t.keys = append(t.keys, fd.Key)
			}
			row[fd.Key] = fieldValue(fd)
		}
		t.values = append(t.values, row)
	}
	return t
}

// fit keeps the field columns that fit in width beside the fixed columns,
// shortening long values, and returns how many fields it left out.
func (t *itemTable) fit(width, fixed int) int {
	const maxCell, gap = 24, 3
	used := fixed
	for i, k := range t.keys {
		w := displayLen(k)
		for _, row := range t.values {
			row[k] = truncate(row[k], maxCell)
			w = max(w, displayLen(row[k]))
		}
		if used+gap+w > width {
			dropped := len(t.keys) - i
			t.keys = t.keys[:i]
			return dropped
		}
		used += gap + w
	}
	return 0
}

// fieldValue is formatValue, with a date at midnight shown as the date only
// and a GeoJSON geometry summarized.
func fieldValue(fd *cmsapi.Field) string {
	if g, ok := geometryOf(fd.Value); ok {
		return g
	}
	s := formatValue(fd.Value)
	if fd.Type == "date" {
		if d, rest, ok := strings.Cut(s, "T00:00:00"); ok && (rest == "" || rest == "Z" || rest[0] == '+' || rest[0] == '-') {
			return d
		}
	}
	return s
}

func displayLen(s string) int { return runewidth.StringWidth(s) }

func truncate(s string, n int) string { return runewidth.Truncate(s, n, "…") }

// geometryOf summarizes a GeoJSON geometry, given as an object or as JSON
// text: a point as "lon, lat", anything else as its type and point count.
func geometryOf(v any) (string, bool) {
	var g struct {
		Type        string          `json:"type"`
		Coordinates json.RawMessage `json:"coordinates"`
	}
	var b []byte
	switch v := v.(type) {
	case string:
		b = []byte(v)
	case map[string]any:
		b, _ = json.Marshal(v)
	default:
		return "", false
	}
	if json.Unmarshal(b, &g) != nil || g.Type == "" || len(g.Coordinates) == 0 {
		return "", false
	}
	if g.Type == "Point" {
		var c []float64
		if json.Unmarshal(g.Coordinates, &c) == nil && len(c) >= 2 {
			return fmt.Sprintf("%g, %g", c[0], c[1]), true
		}
	}
	return fmt.Sprintf("%s (%d points)", g.Type, countPoints(g.Coordinates)), true
}

// countPoints counts the positions in nested GeoJSON coordinates.
func countPoints(raw json.RawMessage) int {
	var nums []float64
	if json.Unmarshal(raw, &nums) == nil {
		return 1
	}
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) != nil {
		return 0
	}
	n := 0
	for _, e := range list {
		n += countPoints(e)
	}
	return n
}
