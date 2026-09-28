package output

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/mattn/go-runewidth"
)

// Cell is a table cell with an optional style applied only on a TTY.
type Cell struct {
	Text  string
	Style func(string) string
}

// Table renders rows as an aligned, borderless table on a TTY and as
// tab-separated values without a header otherwise.
type Table struct {
	p       *Printer
	headers []string
	rows    [][]Cell
}

func (p *Printer) Table(headers ...string) *Table {
	return &Table{p: p, headers: headers}
}

// Row adds a row. Each value may be a string, a Cell, or anything fmt can print.
func (t *Table) Row(values ...any) {
	row := make([]Cell, len(values))
	for i, v := range values {
		switch c := v.(type) {
		case Cell:
			row[i] = c
		case string:
			row[i] = Cell{Text: c}
		default:
			row[i] = Cell{Text: fmt.Sprint(c)}
		}
	}
	t.rows = append(t.rows, row)
}

func (t *Table) Len() int { return len(t.rows) }

func (t *Table) Render() error {
	w := t.p.io.Out
	if t.p.format != FormatTable {
		for _, row := range t.rows {
			texts := make([]string, len(row))
			for i, c := range row {
				texts[i] = strings.ReplaceAll(c.Text, "\t", " ")
			}
			if _, err := fmt.Fprintln(w, strings.Join(texts, "\t")); err != nil {
				return err
			}
		}
		return nil
	}

	cs := t.p.io.Color()
	cols := len(t.headers)
	for _, r := range t.rows {
		cols = max(cols, len(r))
	}
	widths := make([]int, cols)
	for i, h := range t.headers {
		widths[i] = displayWidth(h)
	}
	for _, r := range t.rows {
		for i, c := range r {
			widths[i] = max(widths[i], displayWidth(c.Text))
		}
	}

	const indent = "  "
	// A leading one-character column without a header (status icon, active
	// mark) sits right next to its row.
	gaps := make([]string, cols)
	for i := range gaps {
		gaps[i] = "   "
	}
	if cols > 1 && widths[0] <= 1 && (len(t.headers) == 0 || t.headers[0] == "") {
		gaps[0] = " "
	}
	line := func(cells []Cell, header bool) string {
		var b strings.Builder
		b.WriteString(indent)
		for i := 0; i < cols; i++ {
			var c Cell
			if i < len(cells) {
				c = cells[i]
			}
			text := c.Text
			if header {
				text = cs.Dim(text)
			} else if c.Style != nil && text != "" {
				text = c.Style(text)
			}
			b.WriteString(text)
			if i < cols-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-displayWidth(c.Text)))
				b.WriteString(gaps[i])
			}
		}
		return strings.TrimRight(b.String(), " ")
	}

	if len(t.headers) > 0 {
		hs := make([]Cell, len(t.headers))
		for i, h := range t.headers {
			hs[i] = Cell{Text: strings.ToUpper(h)}
		}
		if _, err := fmt.Fprintln(w, line(hs, true)); err != nil {
			return err
		}
	}
	for _, r := range t.rows {
		if _, err := fmt.Fprintln(w, line(r, false)); err != nil {
			return err
		}
	}
	return nil
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// widthCond treats East Asian ambiguous characters (—, ●, ✓) as narrow, as
// terminals render them, regardless of a CJK locale.
var widthCond = func() *runewidth.Condition {
	c := runewidth.NewCondition()
	c.EastAsianWidth = false
	return c
}()

func displayWidth(s string) int {
	return widthCond.StringWidth(ansiRe.ReplaceAllString(s, ""))
}
