package cms

import (
	"strings"
	"testing"

	cmsapi "github.com/reearth/reearth-cms-api/go"
)

func TestItemTableFitsTheWidth(t *testing.T) {
	items := []cmsapi.Item{
		{Fields: []*cmsapi.Field{
			{Key: "date", Type: "date", Value: "2026-10-13T00:00:00+09:00"},
			{Key: "city", Type: "text", Value: "Tokyo"},
			{Key: "spots", Type: "group", Value: []any{"g1"}},
			{Key: "name", Type: "text", Value: "Gate", Group: "g1"},
		}},
		{Fields: []*cmsapi.Field{
			{Key: "city", Type: "text", Value: "Kuala Lumpur, the capital of Malaysia"},
			{Key: "note", Type: "textarea", Value: "東京タワーの近く"},
		}},
	}
	tb := newItemTable(items)
	if got := strings.Join(tb.keys, ","); got != "date,city,note" {
		t.Fatalf("keys = %s", got)
	}
	if tb.values[0]["date"] != "2026-10-13" {
		t.Errorf("date = %q", tb.values[0]["date"])
	}

	// date (10) and city (24 after truncation) fit; note (16) does not.
	if dropped := tb.fit(10+3+24+3+15, 0); dropped != 1 || strings.Join(tb.keys, ",") != "date,city" {
		t.Fatalf("dropped %d, keys %v", dropped, tb.keys)
	}
	if v := tb.values[1]["city"]; displayLen(v) != 24 || !strings.HasSuffix(v, "…") {
		t.Errorf("city = %q", v)
	}
}

func TestFieldValueKeepsTimes(t *testing.T) {
	for v, want := range map[string]string{
		"2026-10-13T00:00:00+09:00": "2026-10-13",
		"2026-10-13T00:00:00Z":      "2026-10-13",
		"2026-10-13T09:30:00+09:00": "2026-10-13T09:30:00+09:00",
	} {
		if got := fieldValue(&cmsapi.Field{Type: "date", Value: v}); got != want {
			t.Errorf("%s: %s", v, got)
		}
	}
	if got := fieldValue(&cmsapi.Field{Type: "text", Value: "T00:00:00"}); got != "T00:00:00" {
		t.Errorf("text: %s", got)
	}
}

func TestFieldValueSummarizesGeometry(t *testing.T) {
	for _, c := range []struct {
		v    any
		want string
	}{
		{`{ "type": "Point", "coordinates": [139.7671, 35.6812] }`, "139.7671, 35.6812"},
		{map[string]any{"type": "Point", "coordinates": []any{135.5, 34.7}}, "135.5, 34.7"},
		{`{"type":"LineString","coordinates":[[0,0],[1,1],[2,2]]}`, "LineString (3 points)"},
		{`{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,0]]]}`, "Polygon (4 points)"},
		{`{"type":"note"}`, `{"type":"note"}`},
		{"Tokyo", "Tokyo"},
	} {
		if got := fieldValue(&cmsapi.Field{Type: "geometryObject", Value: c.v}); got != c.want {
			t.Errorf("%v: %q, want %q", c.v, got, c.want)
		}
	}
}

func TestFormatValueFitsOneLine(t *testing.T) {
	for _, c := range []struct {
		v    any
		want string
	}{
		{nil, ""},
		{"line one\nline two", "line one line two"},
		{[]any{"a", 1.5, true}, "a, 1.5, true"},
		{map[string]any{"k": "v"}, `{"k":"v"}`},
		{42.0, "42"},
	} {
		if got := formatValue(c.v); got != c.want {
			t.Errorf("%v: %q, want %q", c.v, got, c.want)
		}
	}
}

func TestAssetNameAndSize(t *testing.T) {
	for _, c := range []struct{ name, url, want string }{
		{"given.png", "https://x/a/b.png", "given.png"},
		{"", "https://x/assets/a%20b.geojson?sig=1", "a b.geojson"},
		{"", "https://x/assets/", ""},
		{"", "", ""},
	} {
		if got := assetName(c.name, c.url); got != c.want {
			t.Errorf("assetName(%q, %q) = %q, want %q", c.name, c.url, got, c.want)
		}
	}
	for n, want := range map[float32]string{0: "", 512: "512 B", 1536: "1.5 KB", 3 << 20: "3.0 MB", 5 << 30: "5.0 GB"} {
		if got := formatSize(n); got != want {
			t.Errorf("formatSize(%v) = %q, want %q", n, got, want)
		}
	}
}
