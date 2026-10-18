package ai

import "superset/internal/textsearch"

// Text matching for retrieval (词法召回).
//
// The Agent plane has no embedding provider by default: AI_EMBEDDING_MODEL is
// empty in most deployments and pgvector may not even be installed. WrenAI has
// the same ladder and degrades gracefully (LanceDB when available, "grep" i.e.
// token overlap otherwise), so this file implements the same fallback honestly.
//
// The implementation now lives in internal/textsearch so the semantic layer
// (rules retrieval) and the Agent score candidates with the same function.
// These wrappers keep the call sites and tests in this package unchanged.

// tokenizeText splits text into lower-case ASCII words and CJK bigrams.
func tokenizeText(s string) []string { return textsearch.Tokenize(s) }

// textScore returns how much of the query's meaning appears in doc, in [0,1].
func textScore(query, doc string) float64 { return textsearch.Score(query, doc) }

// rankedItem is one scored candidate.
type rankedItem[T any] = textsearch.Ranked[T]

// rankByText scores candidates and returns the best k above minScore, highest
// first. Ties keep the input order, so the result is deterministic.
func rankByText[T any](query string, items []T, text func(T) string, k int, minScore float64) []rankedItem[T] {
	return textsearch.Rank(query, items, text, k, minScore)
}
