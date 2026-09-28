// Package output renders command results either for humans (tables, relative
// times, colors) or for programs (json, yaml, ndjson, jq), from the same data.
package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/itchyny/gojq"
	"github.com/spf13/pflag"
	"go.yaml.in/yaml/v3"

	"github.com/reearth/cli/sdk/iostreams"
)

type Format string

const (
	FormatTable  Format = "table"
	FormatPlain  Format = "plain"
	FormatJSON   Format = "json"
	FormatYAML   Format = "yaml"
	FormatNDJSON Format = "ndjson"
)

// allFields is the NoOptDefVal of --json: "--json" without a value selects every field.
const allFields = "*"

// Options are the output-related global flags.
type Options struct {
	JSON   string
	JQ     string
	Output string
}

func (o *Options) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&o.JSON, "json", "", "Output JSON; optionally select fields: --json=id,name")
	fs.Lookup("json").NoOptDefVal = allFields
	fs.StringVar(&o.JQ, "jq", "", "Filter JSON output with a jq expression (implies --json)")
	fs.StringVarP(&o.Output, "output", "o", "", "Output format: table|plain|json|yaml|ndjson")
}

// Printer writes command results to stdout in the resolved format.
type Printer struct {
	io     *iostreams.IOStreams
	format Format
	fields []string
	jq     *gojq.Code
}

// NewPrinter resolves the output format from flags, the default setting and the terminal.
func NewPrinter(io *iostreams.IOStreams, o Options, defaultFormat string) (*Printer, error) {
	p := &Printer{io: io}

	format := Format(strings.ToLower(o.Output))
	if format == "" && (o.JSON != "" || o.JQ != "") {
		format = FormatJSON
	}
	if format == "" && defaultFormat != "" && io.IsStdoutTTY() {
		format = Format(defaultFormat)
	}
	if format == "" {
		if io.IsStdoutTTY() {
			format = FormatTable
		} else {
			format = FormatPlain
		}
	}
	switch format {
	case FormatTable, FormatPlain, FormatJSON, FormatYAML, FormatNDJSON:
	default:
		return nil, fmt.Errorf("unknown output format %q (want table|plain|json|yaml|ndjson)", format)
	}
	if o.JQ != "" && format != FormatJSON && format != FormatNDJSON {
		return nil, fmt.Errorf("--jq cannot be combined with --output %s", format)
	}
	p.format = format

	if o.JSON != "" && o.JSON != allFields && o.JSON != "true" && o.JSON != "1" {
		for _, f := range strings.Split(o.JSON, ",") {
			if f = strings.TrimSpace(f); f != "" {
				p.fields = append(p.fields, f)
			}
		}
	}

	if o.JQ != "" {
		q, err := gojq.Parse(o.JQ)
		if err != nil {
			return nil, fmt.Errorf("invalid --jq expression: %w", err)
		}
		code, err := gojq.Compile(q)
		if err != nil {
			return nil, fmt.Errorf("invalid --jq expression: %w", err)
		}
		p.jq = code
	}
	return p, nil
}

func (p *Printer) Format() Format { return p.format }

// IsMachine reports whether output is structured data (json/yaml/ndjson).
func (p *Printer) IsMachine() bool {
	return p.format == FormatJSON || p.format == FormatYAML || p.format == FormatNDJSON
}

// IsHuman reports whether output is the decorated TTY table format.
func (p *Printer) IsHuman() bool { return p.format == FormatTable }

// Print writes data in a machine format, or calls human() for table/plain output.
// data should be JSON-serializable and use stable lower_snake/camel keys.
func (p *Printer) Print(data any, human func() error) error {
	if !p.IsMachine() {
		if human == nil {
			return p.write(FormatYAML, data)
		}
		return human()
	}
	return p.write(p.format, data)
}

// write encodes data in the given format, applying --json fields and --jq.
func (p *Printer) write(format Format, data any) error {
	v, err := normalize(data)
	if err != nil {
		return err
	}
	if len(p.fields) > 0 {
		v = filterFields(v, p.fields)
	}
	if p.jq != nil {
		return p.runJQ(v, format)
	}
	w := p.io.Out
	switch format {
	case FormatYAML:
		enc := yaml.NewEncoder(w)
		enc.SetIndent(2)
		defer func() { _ = enc.Close() }()
		return enc.Encode(v)
	case FormatNDJSON:
		if arr, ok := v.([]any); ok {
			for _, e := range arr {
				if err := writeJSON(w, e, false); err != nil {
					return err
				}
			}
			return nil
		}
		return writeJSON(w, v, false)
	default:
		return writeJSON(w, v, true)
	}
}

func (p *Printer) runJQ(v any, format Format) error {
	iter := p.jq.Run(v)
	for {
		r, ok := iter.Next()
		if !ok {
			return nil
		}
		if err, ok := r.(error); ok {
			var halt *gojq.HaltError
			if errors.As(err, &halt) {
				return nil
			}
			return fmt.Errorf("jq: %w", err)
		}
		// Like `jq -r`: strings are printed raw so they compose with shell pipelines.
		if s, ok := r.(string); ok {
			if _, err := fmt.Fprintln(p.io.Out, s); err != nil {
				return err
			}
			continue
		}
		if err := writeJSON(p.io.Out, r, format == FormatJSON && p.io.IsStdoutTTY()); err != nil {
			return err
		}
	}
}

func writeJSON(w io.Writer, v any, indent bool) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if indent {
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(v); err != nil {
		return err
	}
	_, err := w.Write(buf.Bytes())
	return err
}

// normalize round-trips data through JSON so field filtering, jq and yaml all
// see the same shape (json tags are honored, times become RFC3339 strings).
func normalize(data any) (any, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	var v any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err := d.Decode(&v); err != nil {
		return nil, err
	}
	return fixNumbers(v), nil
}

// fixNumbers converts json.Number into int or float64 for gojq/yaml.
func fixNumbers(v any) any {
	switch t := v.(type) {
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return int(i)
		}
		f, _ := t.Float64()
		return f
	case []any:
		for i := range t {
			t[i] = fixNumbers(t[i])
		}
	case map[string]any:
		for k := range t {
			t[k] = fixNumbers(t[k])
		}
	}
	return v
}

func filterFields(v any, fields []string) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(fields))
		for _, f := range fields {
			if val, ok := t[f]; ok {
				out[f] = val
			}
		}
		return out
	case []any:
		for i := range t {
			t[i] = filterFields(t[i], fields)
		}
		return t
	}
	return v
}

// Time formats a timestamp: relative on a TTY ("2 hours ago"), RFC3339 otherwise.
func (p *Printer) Time(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	if p.format == FormatTable {
		return RelativeTime(time.Since(t))
	}
	return t.UTC().Format(time.RFC3339)
}

// RelativeTime renders a duration since an event in a human-friendly way.
func RelativeTime(d time.Duration) string {
	future := d < 0
	if future {
		d = -d
	}
	var s string
	switch {
	case d < time.Minute:
		if future {
			return "in a few seconds"
		}
		return "just now"
	case d < time.Hour:
		s = plural(int(d.Minutes()), "minute")
	case d < 24*time.Hour:
		s = plural(int(d.Hours()), "hour")
	case d < 48*time.Hour:
		if future {
			return "tomorrow"
		}
		return "yesterday"
	case d < 30*24*time.Hour:
		s = plural(int(d.Hours()/24), "day")
	case d < 365*24*time.Hour:
		s = plural(int(d.Hours()/24/30), "month")
	default:
		s = plural(int(d.Hours()/24/365), "year")
	}
	if future {
		return "in " + s
	}
	return s + " ago"
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

// ShortID abbreviates long IDs on a TTY ("01hq…a1"); full IDs otherwise.
func (p *Printer) ShortID(id string) string {
	if p.format != FormatTable || len(id) <= 12 {
		return id
	}
	return id[:4] + "…" + id[len(id)-2:]
}
