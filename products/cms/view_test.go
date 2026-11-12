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
	// Values are shown as the API returns them.
	if tb.values[0]["date"] != "2026-10-13T00:00:00+09:00" {
		t.Errorf("date = %q", tb.values[0]["date"])
	}

	// date (24 after truncation) and city (24) fit; note (16) does not.
	if dropped := tb.fit(24+3+24+3+15, 0); dropped != 1 || strings.Join(tb.keys, ",") != "date,city" {
		t.Fatalf("dropped %d, keys %v", dropped, tb.keys)
	}
	if v := tb.values[1]["city"]; displayLen(v) != 24 || !strings.HasSuffix(v, "…") {
		t.Errorf("city = %q", v)
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

func TestFormatSize(t *testing.T) {
	for n, want := range map[float32]string{0: "", 512: "512 B", 1536: "1.5 KB", 3 << 20: "3.0 MB", 5 << 30: "5.0 GB"} {
		if got := formatSize(n); got != want {
			t.Errorf("formatSize(%v) = %q, want %q", n, got, want)
		}
	}
}
