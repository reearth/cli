// Package docs searches and reads the Re:Earth documentation through the
// site's llms-full.txt: every page of docs.reearth.io in one Markdown file.
package docs

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Page is one page of the documentation.
type Page struct {
	// ID names the page for `docs read`: the URL path when the file carries
	// page URLs, otherwise the title, numbered when several pages share it.
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
	fence := ""
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		if fence == "" {
			if f := fenceOf(l); f != "" {
				fence = f
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
		} else if strings.HasPrefix(strings.TrimSpace(l), fence) {
			fence = ""
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

func fenceOf(l string) string {
	t := strings.TrimSpace(l)
	for _, f := range []string{"```", "~~~"} {
		if strings.HasPrefix(t, f) {
			return f
		}
	}
	return ""
}

func assignIDs(pages []Page) {
	seen := map[string]int{}
	for i := range pages {
		p := &pages[i]
		if u, err := url.Parse(p.URL); err == nil && p.URL != "" {
			p.ID = strings.Trim(u.Path, "/")
		}
		if p.ID == "" {
			p.ID = p.Title
		}
		seen[p.ID]++
		if n := seen[p.ID]; n > 1 {
			p.ID = fmt.Sprintf("%s (%d)", p.ID, n)
		}
	}
}

// Find returns the page named by ref: an ID, a unique title, or a page URL.
func Find(pages []Page, ref string) (Page, bool) {
	ref = strings.TrimSpace(ref)
	if u, err := url.Parse(ref); err == nil && u.Host != "" {
		ref = strings.Trim(u.Path, "/")
	}
	ref = strings.Trim(ref, "/")
	var byTitle []Page
	for _, p := range pages {
		if p.ID == ref {
			return p, true
		}
		if p.Title == ref {
			byTitle = append(byTitle, p)
		}
	}
	if len(byTitle) == 1 {
		return byTitle[0], true
	}
	return Page{}, false
}

// Markdown renders a page with its header. Site-relative links are made
// absolute against site so that they can be followed.
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
	return strings.NewReplacer("](/", "]("+site+"/", `src="/`, `src="`+site+"/").Replace(s)
}
