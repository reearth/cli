// Package docs searches and reads the Re:Earth documentation through the
// site's llms-full.txt: every page of docs.reearth.io in one Markdown file.
package docs

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Page is one page of the documentation.
type Page struct {
	// ID names the page for `docs read`: the URL path when the file carries
	// page URLs, otherwise the title. When several pages would share an ID,
	// each of them is numbered. Numbers follow file order, so they can change
	// when pages are added to the site.
	ID          string `json:"id"`
	Title       string `json:"title"`
	URL         string `json:"url,omitempty"`
	Description string `json:"description"`
	Body        string `json:"-"`
}

// Parse splits llms-full.txt into pages. The generator writes each page as
//
//	# Title
//
//	Source: https://docs.reearth.io/ja/cms/   (only on newer builds)
//
//	> Description
//
//	Body...
//
// A heading followed by anything other than a description is a heading inside
// a page, not a new page. Text before the first page, such as the <SYSTEM>
// line, is dropped.
func Parse(text string) []Page {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var pages []Page
	var body []string
	flush := func() {
		if len(pages) > 0 {
			pages[len(pages)-1].Body = strings.TrimSpace(strings.Join(body, "\n"))
		}
		body = nil
	}
	var open fence
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		if open.n == 0 {
			if f, ok := openFence(l); ok {
				open = f
			} else if p, next, ok := pageHead(lines, i); ok {
				flush()
				pages = append(pages, p)
				i = next - 1
				continue
			} else if sectionAnchor.MatchString(l) {
				if i+1 < len(lines) && strings.TrimSpace(lines[i+1]) == "" {
					i++
				}
				continue
			}
		} else if open.closedBy(l) {
			open = fence{}
		}
		body = append(body, l)
	}
	flush()
	assignIDs(pages)
	return pages
}

// pageHead reads a page header at lines[i] and returns the index of the first
// body line.
func pageHead(lines []string, i int) (Page, int, bool) {
	title, ok := strings.CutPrefix(lines[i], "# ")
	if !ok {
		return Page{}, 0, false
	}
	p := Page{Title: strings.TrimSpace(title)}
	j := skipBlank(lines, i+1)
	if j < len(lines) {
		if u, ok := strings.CutPrefix(lines[j], "Source: "); ok {
			p.URL = strings.TrimSpace(u)
			j = skipBlank(lines, j+1)
		}
	}
	var desc []string
	for ; j < len(lines); j++ {
		d, ok := strings.CutPrefix(lines[j], "> ")
		if !ok {
			break
		}
		desc = append(desc, strings.TrimSpace(d))
	}
	if len(desc) == 0 {
		return Page{}, 0, false
	}
	p.Description = strings.Join(desc, " ")
	return p, j, true
}

// sectionAnchor matches the anchor link Starlight writes under every heading,
// which carries nothing a reader needs.
var sectionAnchor = regexp.MustCompile(`^\[Section titled “.*”\]\(#[^)]*\)$`)

func skipBlank(lines []string, j int) int {
	for j < len(lines) && strings.TrimSpace(lines[j]) == "" {
		j++
	}
	return j
}

// fence is an open code fence: its character and length. The zero value
// means no fence is open.
type fence struct {
	char byte
	n    int
}

// openFence reports whether l opens a code fence, following CommonMark: three
// or more backticks or tildes indented by at most three spaces. A line
// indented further is indented code, not a fence.
func openFence(l string) (fence, bool) {
	t, ok := fenceIndent(l)
	if !ok || t == "" || (t[0] != '`' && t[0] != '~') {
		return fence{}, false
	}
	f := fence{char: t[0], n: runLen(t, t[0])}
	if f.n < 3 || (f.char == '`' && strings.Contains(t[f.n:], "`")) {
		return fence{}, false
	}
	return f, true
}

// closedBy reports whether l closes f: the same character, at least as many
// of it, and nothing but whitespace after.
func (f fence) closedBy(l string) bool {
	t, ok := fenceIndent(l)
	if !ok {
		return false
	}
	n := runLen(t, f.char)
	return n >= f.n && strings.TrimSpace(t[n:]) == ""
}

// fenceIndent strips up to three leading spaces and reports false when the
// line is indented further.
func fenceIndent(l string) (string, bool) {
	t := strings.TrimLeft(l, " ")
	return t, len(l)-len(t) <= 3 && !strings.HasPrefix(t, "\t")
}

func runLen(s string, c byte) int {
	n := 0
	for n < len(s) && s[n] == c {
		n++
	}
	return n
}

// assignIDs names each page by its URL path, or by its title when the file
// carries no URLs. Pages that would share an ID are all numbered ("FAQ (1)",
// "FAQ (2)"), so no ID is a shared title and every ID names one page.
func assignIDs(pages []Page) {
	count := map[string]int{}
	for i := range pages {
		p := &pages[i]
		if u, err := url.Parse(p.URL); err == nil && p.URL != "" {
			p.ID = strings.Trim(u.Path, "/")
		}
		if p.ID == "" {
			p.ID = p.Title
		}
		count[p.ID]++
	}
	taken := map[string]bool{}
	for id, n := range count {
		if n == 1 {
			taken[id] = true
		}
	}
	next := map[string]int{}
	for i := range pages {
		p := &pages[i]
		if count[p.ID] == 1 {
			continue
		}
		base := p.ID
		for {
			next[base]++
			p.ID = fmt.Sprintf("%s (%d)", base, next[base])
			if !taken[p.ID] {
				break
			}
		}
		taken[p.ID] = true
	}
}

// ErrNotFound is returned by Find when no page matches.
var ErrNotFound = errors.New("no such page")

// AmbiguousError is returned by Find when several pages share the title.
type AmbiguousError struct {
	Ref string
	IDs []string
}

func (e *AmbiguousError) Error() string {
	return fmt.Sprintf("%d pages are titled %q: %s", len(e.IDs), e.Ref, strings.Join(e.IDs, ", "))
}

// Find returns the page named by ref: an ID, a title, or a page URL or URL
// path. IDs are unique and never a shared title, so an ID always resolves; a
// title that several pages share is an *AmbiguousError listing their IDs. The
// ref is tried verbatim first, so titles with slashes or that look like URLs
// resolve too.
func Find(pages []Page, ref string) (Page, error) {
	ref = strings.TrimSpace(ref)
	path := ref
	if u, err := url.Parse(ref); err == nil && u.Host != "" {
		path = u.Path
	}
	path = strings.Trim(path, "/")
	p, err := find(pages, ref)
	if errors.Is(err, ErrNotFound) && path != ref {
		return find(pages, path)
	}
	return p, err
}

func find(pages []Page, ref string) (Page, error) {
	var byTitle []Page
	for _, p := range pages {
		if p.ID == ref {
			return p, nil
		}
		if p.Title == ref {
			byTitle = append(byTitle, p)
		}
	}
	switch len(byTitle) {
	case 0:
		return Page{}, ErrNotFound
	case 1:
		return byTitle[0], nil
	}
	e := &AmbiguousError{Ref: ref}
	for _, p := range byTitle {
		e.IDs = append(e.IDs, p.ID)
	}
	return Page{}, e
}

// Markdown renders a page with its header. Site-relative links outside code
// (fenced, indented or inline) are made absolute against site so that they
// can be followed.
func (p Page) Markdown(site string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", p.Title)
	if p.URL != "" {
		fmt.Fprintf(&b, "Source: %s\n\n", p.URL)
	}
	fmt.Fprintf(&b, "> %s\n\n", p.Description)
	b.WriteString(absoluteLinks(p.Body, strings.TrimRight(site, "/")))
	b.WriteString("\n")
	return b.String()
}

func absoluteLinks(s, site string) string {
	lines := strings.Split(s, "\n")
	var open fence
	indented := false // inside indented code
	blank := true     // the previous line is blank; indented code cannot interrupt a paragraph
	inList := false   // inside a list item, where indented lines continue the item
	for i, l := range lines {
		isBlank := strings.TrimSpace(l) == ""
		line := l
		if inList && codeIndent(l) {
			// Fences in a list item are indented with it.
			line = strings.TrimLeft(l, " \t")
		}
		switch {
		case open.n > 0:
			if open.closedBy(line) {
				open = fence{}
			}
		case indented && (isBlank || codeIndent(l)), blank && !isBlank && !inList && codeIndent(l):
			indented = true
		default:
			indented = false
			f, isFence := openFence(line)
			switch {
			case listItem.MatchString(l):
				inList = true
			case !isBlank && !codeIndent(l) && (blank || isFence):
				// A lazy continuation line stays in the item; anything else ends the list.
				inList = false
			}
			if isFence {
				open = f
			} else {
				lines[i] = absoluteLinksInLine(l, site)
			}
		}
		blank = isBlank
	}
	return strings.Join(lines, "\n")
}

// listItem matches the start of a list item.
var listItem = regexp.MustCompile(`^ {0,3}([-*+]|[0-9]{1,9}[.)])([ \t]|$)`)

// codeIndent reports whether l is indented enough to be indented code: four
// spaces or a tab. Inside a list item, such lines continue the item instead.
func codeIndent(l string) bool {
	t := strings.TrimLeft(l, " ")
	return len(l)-len(t) >= 4 || strings.HasPrefix(t, "\t")
}

// siteRelative matches the start of a site-relative link target. A target
// that starts with // is protocol-relative and left alone.
var siteRelative = regexp.MustCompile(`(\]\(|src=")/([^/]|$)`)

// absoluteLinksInLine rewrites site-relative links outside inline code spans.
func absoluteLinksInLine(l, site string) string {
	var b strings.Builder
	text := 0 // start of the text not yet written
	for i := 0; i < len(l); {
		if l[i] != '`' {
			i++
			continue
		}
		n := runLen(l[i:], '`')
		end := closingTicks(l, i+n, n)
		if end < 0 {
			i += n
			continue
		}
		b.WriteString(rewriteLinks(l[text:i], site))
		b.WriteString(l[i:end])
		text, i = end, end
	}
	b.WriteString(rewriteLinks(l[text:], site))
	return b.String()
}

func rewriteLinks(s, site string) string {
	return siteRelative.ReplaceAllStringFunc(s, func(m string) string {
		i := strings.IndexByte(m, '/')
		return m[:i] + site + m[i:]
	})
}

// closingTicks returns the index just past the next run of exactly n
// backticks at or after from, or -1 when the code span is not closed.
func closingTicks(l string, from, n int) int {
	for i := from; i < len(l); {
		if l[i] != '`' {
			i++
			continue
		}
		m := runLen(l[i:], '`')
		if m == n {
			return i + m
		}
		i += m
	}
	return -1
}
