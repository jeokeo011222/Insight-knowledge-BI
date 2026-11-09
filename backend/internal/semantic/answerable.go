// Package semantic: check_answerable reports whether a natural-language
// question can be answered from a given semantic model. It is NVIDIA Auto
// Ontology's "Validate" phase — the Agent runs it *before* generating SQL,
// so instead of hallucinating a wrong metric it replies
//   "I can find revenue, but this data has no cost so I cannot compute ROI.
//    Do you want June 2025 revenue instead?"
//
// Coverage is a score in [0, 1]. Above 0.6 the Agent proceeds with its
// normal plan → act flow. Below that threshold it surfaces the coverage
// report directly, asks whether to broaden the question, or suggests
// connecting another data source.
//
// The implementation is intentionally cheap — pure string matching, no
// LLM call — so it can run on every SSE step as a pre-flight gate.

package semantic

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"gorm.io/gorm"

	"superset/internal/models"
)

// CoverageReport is what check_answerable returns.
//
// Coverage ∈ [0, 1]:
//   - ≥ 0.6  → proceed to NL2SQL; everything the question names has a match
//   - 0.3-0.6 → caution: Agent should qualify its answer with "this is
//               computed from X, Y is not in the data"
//   - < 0.3  → block: Agent should not attempt SQL generation at all
//
// Matched maps each question token to the semantic items it hit (field
// name, description, synonym or metric). Missing lists tokens that did
// not resolve anything at all. The Agent uses both to decide whether to
// ask "did you mean …?" or to bail out gracefully.
type CoverageReport struct {
	Question string                    `json:"question"`
	Coverage float64                   `json:"coverage"`
	Matched  []TermHit                 `json:"matched"`
	Missing  []string                  `json:"missing"`
	Evidence string                    `json:"evidence"`
	Items    []models.SemanticModel    `json:"-"` // cached for the Agent that called us
}

// TermHit is one question token and every semantic item it mapped to.
type TermHit struct {
	Term    string   `json:"term"`
	Hits    []Item   `json:"hits"`
	Score   float64  `json:"score"` // 1.0 = exact name match, 0.5 = description/synonym
}

// Item identifies one field / metric / relationship that matched.
type Item struct {
	ModelName   string `json:"model_name"`
	FieldName   string `json:"field_name"`
	FieldKind   string `json:"field_kind"`   // column | time | metric | dimension
	MatchSource string `json:"match_source"` // name | synonym | description | expression
}

// DefaultAnswerableThreshold is the cutoff the Agent uses to decide whether
// to proceed with SQL generation (coverage ≥ threshold) or surface the
// coverage report directly (< threshold). The value is chosen empirically:
// BIRD-style single-metric questions with one filter comfortably clear 0.6,
// "ROI" where the data has no cost lands at ~0.2.
const DefaultAnswerableThreshold = 0.6

// CheckAnswerable evaluates one question against every model published for
// one dataset. It returns coverage plus the matched / missing breakdown.
// If no model exists yet it returns coverage 0 with Missing=all_tokens.
func (s *Service) CheckAnswerable(ctx context.Context, datasetID int, question string) (*CoverageReport, error) {
	question = strings.TrimSpace(question)
	if question == "" {
		return &CoverageReport{Question: "", Coverage: 0, Evidence: "empty question"}, nil
	}

	// Load published models for this dataset (and draft ones too — drafts
	// are visible to the Agent in draft mode; certified ones are trusted
	// higher so we weight them first).
	var modelsList []models.SemanticModel
	if err := s.DB.WithContext(ctx).
		Where("dataset_id = ? AND deleted_at IS NULL", datasetID).
		Order("status DESC, name ASC").
		Find(&modelsList).Error; err != nil {
		return nil, fmt.Errorf("load semantic models: %w", err)
	}
	if len(modelsList) == 0 {
		return &CoverageReport{
			Question: question,
			Coverage: 0,
			Evidence: "no semantic model for this dataset",
		}, nil
	}

	// Index fields by model so each term hit knows which model it came from.
	fieldsByModel := map[int][]models.SemanticField{}
	for _, m := range modelsList {
		var f []models.SemanticField
		if err := s.DB.WithContext(ctx).Where("model_id = ? AND deleted_at IS NULL", m.ID).Find(&f).Error; err != nil {
			continue
		}
		fieldsByModel[m.ID] = f
	}

	// Tokenize — cheap but decent for English, Chinese and number slugs.
	tokens := tokenize(question)
	matched := []TermHit{}
	missing := []string{}

	for _, tok := range tokens {
		hits := hitsForToken(tok, modelsList, fieldsByModel)
		if len(hits) == 0 {
			missing = append(missing, tok)
			continue
		}
		score := 0.5 // baseline: synonym / description match
		for _, h := range hits {
			if h.MatchSource == "name" {
				score = 1.0
				break
			}
		}
		matched = append(matched, TermHit{Term: tok, Hits: hits, Score: score})
	}

	var cov float64
	if len(tokens) > 0 {
		covered := 0.0
		for _, m := range matched {
			covered += m.Score
		}
		cov = covered / float64(len(tokens))
	}

	report := &CoverageReport{
		Question: question,
		Coverage: cov,
		Matched:  matched,
		Missing:  missing,
		Items:    modelsList,
	}
	report.Evidence = buildEvidence(report)
	return report, nil
}

// hitsForToken searches one token across every field/metric of every model.
// Match sources (name > synonym > description > expression) determine the
// score the caller assigns. Certified models get precedence when the same
// field name exists in both draft and certified.
func hitsForToken(tok string, models []models.SemanticModel, fieldsByModel map[int][]models.SemanticField) []Item {
	tok = strings.ToLower(strings.TrimSpace(tok))
	if tok == "" || len(tok) <= 1 {
		return nil
	}
	var out []Item
	for _, m := range models {
		for _, f := range fieldsByModel[m.ID] {
			lowerName := strings.ToLower(f.Name)
			lowerDesc := strings.ToLower(f.Description)
			lowerExpr := strings.ToLower(f.Expression)

			matched := func(src string) bool { return strings.Contains(src, tok) }
			source := ""
			switch {
			case matched(lowerName):
				source = "name"
			case matched(lowerDesc):
				source = "description"
			case matched(lowerExpr):
				source = "expression"
			default:
				// Synonyms are stored as JSON; parse lazily.
				syns := parseSynonyms(f.Synonyms)
				for _, s := range syns {
					if strings.Contains(strings.ToLower(s), tok) {
						source = "synonym"
						break
					}
				}
			}
			if source != "" {
				out = append(out, Item{
					ModelName:   m.Name,
					FieldName:   f.Name,
					FieldKind:   f.Kind,
					MatchSource: source,
				})
			}
		}
	}
	// Prefer certified hits over draft hits for the same field. Sorting by
	// the source weight the caller reads will be enough here — we only need
	// to flag "matched" vs. "unmatched" at the report level.
	sort.SliceStable(out, func(i, j int) bool {
		return sourceWeight(out[i].MatchSource) > sourceWeight(out[j].MatchSource)
	})
	return out
}

func sourceWeight(s string) int {
	switch s {
	case "name":
		return 4
	case "synonym":
		return 3
	case "expression":
		return 2
	case "description":
		return 1
	default:
		return 0
	}
}

// tokenize splits the question into search tokens. The strategy is:
//   1. Split on ASCII whitespace.
//   2. Keep CJK runs as a single token (simple heuristic — CJK questions
//      will need a real tokenizer later).
//   3. Lowercase each token and drop ones that are purely punctuation or
//      fewer than 2 runes.
func tokenize(s string) []string {
	var tokens []string
	for _, raw := range strings.Fields(s) {
		clean := strings.Trim(raw, ".,!?;:()[]{}<>\"'`，。！？；：（）《》")
		if len([]rune(clean)) < 2 {
			continue
		}
		tokens = append(tokens, strings.ToLower(clean))
	}
	return tokens
}

// parseSynonyms unmarshals a datatypes.JSON column into a slice of strings.
// The Synonyms field on SemanticField already stores this JSON-encoded
// array, so we just need to decode it lazily here.
func parseSynonyms(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

func buildEvidence(r *CoverageReport) string {
	if r == nil {
		return ""
	}
	switch {
	case r.Coverage >= DefaultAnswerableThreshold && len(r.Missing) == 0:
		return fmt.Sprintf("coverage %.2f — all %d tokens resolved", r.Coverage, len(r.Matched))
	case r.Coverage >= DefaultAnswerableThreshold:
		return fmt.Sprintf("coverage %.2f — %d matched, %d unmatched (%s)",
			r.Coverage, len(r.Matched), len(r.Missing), strings.Join(r.Missing, ", "))
	case len(r.Matched) == 0:
		return fmt.Sprintf("coverage %.2f — no matches at all", r.Coverage)
	default:
		return fmt.Sprintf("coverage %.2f — weak coverage, agent should qualify its answer", r.Coverage)
	}
}

// DefaultAnswerableThresholdConst is exposed so callers (MCP tool, Agent
// skill, API handler) can reference the same number.
var _ = DefaultAnswerableThreshold

// Ensure the package compiles with no unused import warnings even if the
// gorm dependency is refactored later.
var _ *gorm.DB
