package docs

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// Match is one search result.
type Match struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	URL     string `json:"url,omitempty"`
	Summary string `json:"summary"`
}

// Field weights for BM25F: a term in the title says the most about a page.
var fieldWeights = [3]float64{5, 3, 1} // title, description, body

const (
	bm25K1 = 1.2
	bm25B  = 0.75
)

// Search ranks pages against a query with BM25F and returns at most limit
// matches, best first.
func Search(pages []Page, query string, limit int) []Match {
	terms := tokenize(query)
	out := []Match{}
	if len(terms) == 0 || len(pages) == 0 {
		return out
	}

	type doc struct {
		tf  [3]map[string]int
		len [3]int
	}
	docs := make([]doc, len(pages))
	var avg [3]float64
	df := map[string]int{}
	for i, p := range pages {
		for f, text := range [3]string{p.Title, p.Description, p.Body} {
			toks := tokenize(text)
			tf := map[string]int{}
			for _, t := range toks {
				tf[t]++
			}
			docs[i].tf[f], docs[i].len[f] = tf, len(toks)
			avg[f] += float64(len(toks))
		}
		for _, t := range terms {
			if docs[i].tf[0][t]+docs[i].tf[1][t]+docs[i].tf[2][t] > 0 {
				df[t]++
			}
		}
	}
	for f := range avg {
		avg[f] = max(avg[f]/float64(len(pages)), 1)
	}

	n := float64(len(pages))
	type scored struct {
		i     int
		score float64
	}
	var results []scored
	for i, d := range docs {
		var score float64
		for _, t := range terms {
			var tf float64
			for f := range fieldWeights {
				norm := 1 - bm25B + bm25B*float64(d.len[f])/avg[f]
				tf += fieldWeights[f] * float64(d.tf[f][t]) / norm
			}
			if tf == 0 {
				continue
			}
			idf := math.Log(1 + (n-float64(df[t])+0.5)/(float64(df[t])+0.5))
			score += idf * tf / (bm25K1 + tf)
		}
		if score > 0 {
			results = append(results, scored{i, score})
		}
	}
	sort.SliceStable(results, func(a, b int) bool { return results[a].score > results[b].score })
	for k := 0; k < len(results) && k < limit; k++ {
		p := pages[results[k].i]
		out = append(out, Match{ID: p.ID, Title: p.Title, URL: p.URL, Summary: p.Description})
	}
	return out
}

// tokenize lowercases Latin words and splits Japanese, which has no spaces,
// into overlapping pairs of characters, so that "参照フィールド" matches a
// page that says "参照フィールドを追加する".
func tokenize(s string) []string {
	var toks []string
	var word []rune
	var cjk []rune
	flushWord := func() {
		if len(word) > 0 {
			toks = append(toks, string(word))
			word = word[:0]
		}
	}
	flushCJK := func() {
		switch {
		case len(cjk) == 1:
			toks = append(toks, string(cjk))
		case len(cjk) > 1:
			for i := 0; i+1 < len(cjk); i++ {
				toks = append(toks, string(cjk[i:i+2]))
			}
		}
		cjk = cjk[:0]
	}
	for _, r := range strings.ToLower(s) {
		switch {
		case isCJK(r):
			flushWord()
			cjk = append(cjk, r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			flushCJK()
			word = append(word, r)
		default:
			flushWord()
			flushCJK()
		}
	}
	flushWord()
	flushCJK()
	return toks
}

func isCJK(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana) || r == 'ー'
}
