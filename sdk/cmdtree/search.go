package cmdtree

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// Match is one search result.
type Match struct {
	Command string `json:"command"`
	Summary string `json:"summary"`
}

// Field weights: the command path and its summary say the most about what a
// command does; the long description and flags only break ties.
const (
	weightPath   = 8
	weightShort  = 5
	weightLong   = 2
	weightParent = 1
	weightFlags  = 0.5
)

// stopwords carry no meaning in a task description.
var stopwords = map[string]bool{
	"a": true, "an": true, "and": true, "the": true, "to": true, "of": true, "in": true,
	"on": true, "for": true, "with": true, "from": true, "into": true, "by": true,
	"my": true, "me": true, "i": true, "how": true, "do": true, "can": true, "want": true,
	"is": true, "it": true, "this": true, "that": true, "all": true, "or": true,
}

type document struct {
	cmd    Command
	fields [5][]string // path, short, long, parents, flags
}

var weights = [5]float64{weightPath, weightShort, weightLong, weightParent, weightFlags}

// Search ranks the runnable commands and help topics against a task
// description and returns at most limit matches, best first. It runs offline
// over the command tree. A topic is returned as the command that reads it.
func Search(cmds []Command, query string, limit int) []Match {
	terms := tokenize(query)
	if len(terms) == 0 {
		return []Match{}
	}

	var docs []document
	for _, c := range cmds {
		if !c.Runnable && !c.Topic {
			continue
		}
		var fl []string
		for _, f := range c.Flags {
			fl = append(fl, tokenize(f.Name+" "+f.Usage)...)
		}
		docs = append(docs, document{cmd: c, fields: [5][]string{
			tokenize(strings.Join(append([]string{c.Path}, c.Aliases...), " ")),
			tokenize(c.Short),
			tokenize(c.Long),
			tokenize(strings.Join(c.parents, " ")),
			fl,
		}})
	}

	// Rare terms decide more than terms every command shares.
	idf := make([]float64, len(terms))
	for i, t := range terms {
		n := 0
		for _, d := range docs {
			if d.has(t) {
				n++
			}
		}
		idf[i] = math.Log(1 + (float64(len(docs))-float64(n)+0.5)/(float64(n)+0.5))
	}

	type scored struct {
		m     Match
		score float64
	}
	var results []scored
	for _, d := range docs {
		var score float64
		for i, t := range terms {
			for f, toks := range d.fields {
				score += idf[i] * weights[f] * best(t, toks)
			}
		}
		if score > 0 {
			cmd := d.cmd.Path
			if d.cmd.Topic {
				cmd = d.cmd.Usage
			}
			results = append(results, scored{Match{cmd, d.cmd.Short}, score})
		}
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].score > results[j].score })

	out := []Match{}
	for i := 0; i < len(results) && i < limit; i++ {
		out = append(out, results[i].m)
	}
	return out
}

func (d document) has(term string) bool {
	for _, toks := range d.fields {
		if best(term, toks) > 0 {
			return true
		}
	}
	return false
}

// best scores how well term matches any token: 1 for an exact match (after
// stemming), 0.7 for a prefix match and 0.4 for a single typo.
func best(term string, toks []string) float64 {
	var s float64
	for _, tok := range toks {
		switch {
		case tok == term:
			return 1
		case len(term) >= 3 && strings.HasPrefix(tok, term), len(tok) >= 3 && strings.HasPrefix(term, tok):
			s = max(s, 0.7)
		case len(term) >= 5 && withinOneEdit(term, tok):
			s = max(s, 0.4)
		}
	}
	return s
}

// tokenize lowercases, splits on anything but letters and digits, drops
// stopwords and stems plurals, so "Lists items" matches "list item".
func tokenize(s string) []string {
	words := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	toks := words[:0]
	for _, w := range words {
		if stopwords[w] {
			continue
		}
		toks = append(toks, stem(w))
	}
	return toks
}

func stem(w string) string {
	switch {
	case len(w) > 4 && strings.HasSuffix(w, "ies"):
		return w[:len(w)-3] + "y"
	case len(w) > 3 && strings.HasSuffix(w, "es") && strings.ContainsAny(w[len(w)-3:len(w)-2], "sxz"):
		return w[:len(w)-2]
	case len(w) > 3 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss"):
		return w[:len(w)-1]
	}
	return w
}

// withinOneEdit reports whether a and b differ by at most one insertion,
// deletion, substitution or transposition.
func withinOneEdit(a, b string) bool {
	if a == b {
		return true
	}
	la, lb := len(a), len(b)
	if la-lb > 1 || lb-la > 1 {
		return false
	}
	i := 0
	for i < la && i < lb && a[i] == b[i] {
		i++
	}
	switch {
	case la == lb:
		if a[i+1:] == b[i+1:] {
			return true
		}
		return i+1 < la && a[i] == b[i+1] && a[i+1] == b[i] && a[i+2:] == b[i+2:]
	case la > lb:
		return a[i+1:] == b[i:]
	default:
		return a[i:] == b[i+1:]
	}
}
