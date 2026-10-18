// Package textsearch implements the lexical retrieval fallback used whenever no
// embedding provider is configured.
//
// The Agent plane and the semantic layer both need "top-k relevant context"
// from a small candidate set (columns, metrics, rules). Rather than each
// keeping its own scoring function — which drift apart and then disagree about
// what is relevant — both call this package.
//
// It is intentionally not a vector search: ASCII words plus CJK bigrams, with
// a coverage score. When a real embedding provider lands, this package is the
// single place to add it behind the same signature.
package textsearch

import (
	"sort"
	"strings"
	"unicode"
)

// stopWords are dropped before scoring: they appear in almost every question
// and would otherwise dominate the overlap.
var stopWords = map[string]bool{
	"the": true, "a": true, "an": true, "of": true, "in": true, "on": true,
	"for": true, "to": true, "and": true, "or": true, "is": true, "are": true,
	"by": true, "with": true, "as": true, "at": true, "from": true, "what": true,
	"which": true, "how": true, "many": true, "much": true, "show": true, "me": true,
	"的": true, "了": true, "是": true, "在": true, "和": true, "与": true,
	"有": true, "多少": true, "哪些": true, "如何": true, "请": true, "帮我": true,
}

// Tokenize splits text into lower-case ASCII words and CJK bigrams.
func Tokenize(s string) []string {
	var (
		out   []string
		word  strings.Builder
		runes = []rune(strings.ToLower(s))
	)
	flush := func() {
		if word.Len() > 0 {
			tok := word.String()
			word.Reset()
			if !stopWords[tok] && len(tok) > 1 {
				out = append(out, tok)
			}
		}
	}
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case unicode.IsLetter(r) && r < 0x2E80, unicode.IsDigit(r):
			// ASCII word: keep letters/digits together (count_if stays one token).
			word.WriteRune(r)
		case r >= 0x2E80:
			// CJK: unigrams are too common, bigrams carry the meaning.
			flush()
			if i+1 < len(runes) && runes[i+1] >= 0x2E80 {
				gram := string(runes[i : i+2])
				if !stopWords[gram] {
					out = append(out, gram)
				}
			}
		default:
			flush()
		}
	}
	flush()
	return out
}

// Score returns how much of the query's meaning appears in doc, in [0,1].
// It is deliberately asymmetric: a long document must not be penalised for
// containing extra words, but a query term that is absent does count against it.
func Score(query, doc string) float64 {
	q := Tokenize(query)
	if len(q) == 0 {
		return 0
	}
	seen := map[string]bool{}
	for _, tok := range Tokenize(doc) {
		seen[tok] = true
	}
	hit := 0
	for _, tok := range q {
		if seen[tok] {
			hit++
			continue
		}
		// Partial credit: "revenue" vs "revenues", "订单" vs "订单量".
		for cand := range seen {
			if strings.Contains(cand, tok) || strings.Contains(tok, cand) {
				hit++
				break
			}
		}
	}
	return float64(hit) / float64(len(q))
}

// Ranked is one scored candidate.
type Ranked[T any] struct {
	Item  T
	Score float64
}

// Rank scores candidates and returns the best k above minScore, highest first.
// Ties keep the input order, so the result is deterministic.
func Rank[T any](query string, items []T, text func(T) string, k int, minScore float64) []Ranked[T] {
	if k <= 0 {
		k = 8
	}
	ranked := make([]Ranked[T], 0, len(items))
	for _, item := range items {
		score := Score(query, text(item))
		if score < minScore {
			continue
		}
		ranked = append(ranked, Ranked[T]{Item: item, Score: score})
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].Score > ranked[j].Score })
	if len(ranked) > k {
		ranked = ranked[:k]
	}
	return ranked
}
