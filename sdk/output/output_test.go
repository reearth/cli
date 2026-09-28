package output

import (
	"testing"
	"time"

	"github.com/reearth/cli/sdk/iostreams"
)

type item struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Count   int       `json:"count"`
	Updated time.Time `json:"updated_at"`
}

var items = []item{
	{ID: "01hqxxxxxxxxxxa1", Name: "Tokyo", Count: 3, Updated: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)},
	{ID: "01hqxxxxxxxxxxa2", Name: "Osaka", Count: 1, Updated: time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)},
}

func run(t *testing.T, o Options, tty bool, data any, human func(p *Printer) error) string {
	t.Helper()
	ios, _, out, _ := iostreams.Test()
	ios.SetTTY(false, tty, tty)
	p, err := NewPrinter(ios, o, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Print(data, func() error { return human(p) }); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func table(p *Printer) error {
	t := p.Table("id", "name")
	for _, it := range items {
		t.Row(p.ShortID(it.ID), it.Name)
	}
	return t.Render()
}

func TestFormats(t *testing.T) {
	if got := run(t, Options{}, false, items, table); got != "01hqxxxxxxxxxxa1\tTokyo\n01hqxxxxxxxxxxa2\tOsaka\n" {
		t.Errorf("plain = %q", got)
	}
	if got := run(t, Options{}, true, items, table); got != "  ID        NAME\n  01hq…a1   Tokyo\n  01hq…a2   Osaka\n" {
		t.Errorf("table = %q", got)
	}
	got := run(t, Options{JSON: "id,count"}, true, items, table)
	want := "[\n  {\n    \"count\": 3,\n    \"id\": \"01hqxxxxxxxxxxa1\"\n  },\n  {\n    \"count\": 1,\n    \"id\": \"01hqxxxxxxxxxxa2\"\n  }\n]\n"
	if got != want {
		t.Errorf("json fields = %q", got)
	}
	if got := run(t, Options{JQ: ".[].name"}, false, items, table); got != "Tokyo\nOsaka\n" {
		t.Errorf("jq = %q", got)
	}
	if got := run(t, Options{JQ: "map(.count) | add"}, false, items, table); got != "4\n" {
		t.Errorf("jq number = %q", got)
	}
	if got := run(t, Options{Output: "ndjson", JSON: "name"}, false, items, table); got != "{\"name\":\"Tokyo\"}\n{\"name\":\"Osaka\"}\n" {
		t.Errorf("ndjson = %q", got)
	}
	if got := run(t, Options{Output: "yaml", JSON: "updated_at"}, false, items[:1], table); got != "- updated_at: \"2026-09-01T00:00:00Z\"\n" {
		t.Errorf("yaml = %q", got)
	}
}

func TestInvalidOptions(t *testing.T) {
	ios, _, _, _ := iostreams.Test()
	if _, err := NewPrinter(ios, Options{Output: "xml"}, ""); err == nil {
		t.Error("unknown format should fail")
	}
	if _, err := NewPrinter(ios, Options{JQ: ".[", Output: ""}, ""); err == nil {
		t.Error("bad jq should fail")
	}
	if _, err := NewPrinter(ios, Options{JQ: ".", Output: "yaml"}, ""); err == nil {
		t.Error("jq with yaml should fail")
	}
}

func TestRelativeTime(t *testing.T) {
	cases := map[time.Duration]string{
		10 * time.Second:     "just now",
		-10 * time.Second:    "in a few seconds",
		time.Minute:          "1 minute ago",
		2 * time.Hour:        "2 hours ago",
		-12 * time.Minute:    "in 12 minutes",
		30 * time.Hour:       "yesterday",
		3 * 24 * time.Hour:   "3 days ago",
		400 * 24 * time.Hour: "1 year ago",
	}
	for d, want := range cases {
		if got := RelativeTime(d); got != want {
			t.Errorf("RelativeTime(%s) = %q, want %q", d, got, want)
		}
	}
}

func TestDisplayWidthIgnoresANSIAndAmbiguous(t *testing.T) {
	if w := displayWidth("\x1b[1m●\x1b[0m —"); w != 3 {
		t.Errorf("width = %d", w)
	}
	if w := displayWidth("東京"); w != 4 {
		t.Errorf("CJK width = %d", w)
	}
}
