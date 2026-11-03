package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/hermesrunner"
	"market-share-go/internal/marketquestion"
)

type marketQuestionSemanticParagraph struct {
	Index  int                                           `json:"index"`
	Text   string                                        `json:"text"`
	Claims []marketanalysis.MarketQuestionClaimReference `json:"claims"`
}

// Exchange metric fulfillment follows validated fact references and values;
// a compound paragraph must not fail merely because its wording differs from
// the old display-ledger sentence parser. Scope/time are separately validated.
func marketQuestionTypedExchangeExplanationComplete(blocks []marketanalysis.MarketQuestionExplanationBlock, bundle marketquestion.EvidenceBundleSnapshot, question ...string) bool {
	facts := map[string]marketquestion.EvidenceFact{}
	for _, f := range bundle.Facts {
		facts[f.ID] = f
	}
	// The quantitative table includes both share and turnover. Prose must
	// fulfill the requested metrics, not repeat every available table cell.
	requestedMetrics := map[string]bool{}
	if len(question) > 0 {
		query := marketanalysis.MarketQuestionQuery{Metric: bundle.QuestionContract.Metric, Comparison: bundle.QuestionContract.Comparison}
		for _, group := range marketQuestionCompletenessMetricGroups(query, question...) {
			for _, metric := range group {
				requestedMetrics[metric] = true
			}
		}
	}
	checked := false
	for _, req := range bundle.QuestionContract.Requirements {
		if !req.Required || (!strings.HasPrefix(req.ID, "metric_") && !strings.HasPrefix(req.ID, "change_")) || req.Status != marketquestion.RequirementSatisfied {
			continue
		}
		if len(requestedMetrics) > 0 {
			requested := false
			for _, id := range req.FactIDs {
				requested = requested || requestedMetrics[facts[id].Metric]
			}
			if !requested {
				continue
			}
		}
		checked = true
		covered := false
		for _, block := range blocks {
			if block.Status != marketanalysis.MarketQuestionValidationSupported {
				continue
			}
			for _, ref := range block.ClaimRefs {
				for _, id := range req.FactIDs {
					if ref == id && marketQuestionClaimContainsTypedValue(block.Text, facts[id].Value) {
						covered = true
					}
				}
			}
		}
		if !covered {
			return false
		}
	}
	return checked
}

// Numeric grounding is local to the cited claim, never a global number list.
// A semantic verdict cannot override a bad ID, relation or explicit amount.
func marketQuestionReviewHardCheck(block marketanalysis.MarketQuestionClaimBlock, bundle marketquestion.EvidenceBundleSnapshot) string {
	if len(block.Claims) == 0 {
		return "BLOCK_HAS_NO_TYPED_FACT_REFERENCE"
	}
	facts := map[string]marketquestion.EvidenceFact{}
	for _, f := range bundle.Facts {
		facts[f.ID] = f
	}
	for _, claim := range block.Claims {
		f, ok := facts[claim.FactID]
		if !ok {
			return "UNKNOWN_FACT_REFERENCE"
		}
		if strings.TrimSpace(claim.Text) == "" || !strings.Contains(block.Text, claim.Text) {
			return "CLAIM_TEXT_IS_NOT_IN_BLOCK"
		}
		if claim.Relation != marketQuestionEvidenceFactRelation(f) && claim.Relation != "COINCIDENCE_ONLY" {
			return "CLAIM_RELATION_CONFLICTS_WITH_TYPED_FACT"
		}
		for _, loc := range marketQuestionNumericToken.FindAllStringIndex(claim.Text, -1) {
			unit := marketQuestionNumericUnit(claim.Text[loc[1]:])
			if unit == "" || strings.HasPrefix(unit, "TIME_") {
				continue
			} // dates/durations are checked against time identities by review
			// Venue cardinality describes the evidence scope, not a trade-count
			// fact. Check that exact numeric span against the included venues.
			if isScope, matches := marketQuestionReviewScopeCount(claim.Text, loc, bundle.Scope); isScope {
				if !matches {
					return "CLAIM_DENOMINATOR_SCOPE_CONFLICT"
				}
				continue
			}
			// A compound sentence can bind each numeric value to a different fact.
			matched := false
			for _, other := range block.Claims {
				if other.Text != claim.Text {
					continue
				}
				value, exists := facts[other.FactID]
				if !exists {
					continue
				}
				token := claim.Text[loc[0]:loc[1]]
				suffix := map[string]string{"PCT": "%", "PP": "%p", "KRW": "원", "KRW_TRILLION": "조원", "KRW_HUNDRED_MILLION": "억원", "KRW_TEN_THOUSAND": "만원", "COUNT": "건"}[unit]
				if suffix != "" && marketQuestionClaimContainsTypedValue(token+suffix, value.Value) {
					matched = true
					break
				}
			}
			if !matched {
				return "CLAIM_QUANTITY_CONFLICTS_WITH_TYPED_FACT"
			}
		}
	}
	return ""
}

func marketQuestionReviewScopeCount(text string, loc []int, scope marketquestion.ScopeIdentity) (bool, bool) {
	for _, span := range marketQuestionExchangeCount.FindAllStringSubmatchIndex(text, -1) {
		if span[2] != loc[0] || span[3] != loc[1] {
			continue
		}
		included := map[string]bool{}
		for _, exchange := range scope.IncludedExchanges {
			if venue := strings.ToLower(strings.TrimSpace(exchange)); venue != "" {
				included[venue] = true
			}
		}
		count, err := strconv.Atoi(text[loc[0]:loc[1]])
		return true, err == nil && len(included) > 0 && count == len(included)
	}
	return false, false
}

func marketQuestionReviewInput(answer marketanalysis.MarketQuestionAnswer, bundle marketquestion.EvidenceBundleSnapshot) ([]byte, []marketQuestionSemanticParagraph) {
	paragraphs := []marketQuestionSemanticParagraph{}
	for i, b := range answer.ClaimBlocks {
		if marketQuestionReviewHardCheck(b, bundle) == "" {
			claims := append([]marketanalysis.MarketQuestionClaimReference(nil), b.Claims...)
			for index := range claims {
				if claims[index].Text == b.Text {
					claims[index].Text = ""
				}
			}
			paragraphs = append(paragraphs, marketQuestionSemanticParagraph{i, b.Text, claims})
		}
	}
	relations := map[string]string{}
	for _, fact := range bundle.Facts {
		relations[fact.ID] = marketQuestionEvidenceFactRelation(fact)
	}
	input, _ := json.Marshal(map[string]any{"bundle": bundle, "paragraphs": paragraphs, "fact_relations": relations, "contribution_target_exchange": "bithumb"})
	return input, paragraphs
}

func marketQuestionApplySemanticReview(answer marketanalysis.MarketQuestionAnswer, bundle marketquestion.EvidenceBundleSnapshot, verdicts []hermesrunner.MarketQuestionReviewVerdict) (marketanalysis.MarketQuestionAnswer, []marketanalysis.MarketQuestionExplanationBlock, marketanalysis.MarketQuestionValidationStatus) {
	byIndex := map[int]hermesrunner.MarketQuestionReviewVerdict{}
	for _, v := range verdicts {
		byIndex[v.Index] = v
	}
	accepted := []string{}
	blocks := []marketanalysis.MarketQuestionExplanationBlock{}
	overall := marketanalysis.MarketQuestionValidationSupported
	for i, b := range answer.ClaimBlocks {
		item := marketanalysis.MarketQuestionExplanationBlock{Index: i, Status: marketanalysis.MarketQuestionValidationUndetermined, Reason: "SEMANTIC_REVIEW_MISSING", ClaimRefs: []string{}}
		for _, c := range b.Claims {
			item.ClaimRefs = append(item.ClaimRefs, c.FactID)
		}
		if reason := marketQuestionReviewHardCheck(b, bundle); reason != "" {
			item.Status = marketanalysis.MarketQuestionValidationConflict
			item.Reason = reason
		} else if verdict, exists := byIndex[i]; exists {
			item.Status = marketanalysis.MarketQuestionValidationStatus(verdict.Status)
			item.Reason = "SEMANTIC_REVIEW_" + verdict.Status
			if item.Status == marketanalysis.MarketQuestionValidationSupported {
				item.Text = b.Text
				item.Reason = ""
				accepted = append(accepted, b.Text)
			}
		}
		overall = marketQuestionMergeValidationStatus(overall, item.Status)
		blocks = append(blocks, item)
	}
	// The provider must not smuggle answer paragraphs outside its bound blocks.
	joined := []string{}
	for _, b := range answer.ClaimBlocks {
		joined = append(joined, strings.TrimSpace(b.Text))
	}
	if strings.TrimSpace(answer.Answer) != strings.Join(joined, "\n\n") {
		overall = marketQuestionMergeValidationStatus(overall, marketanalysis.MarketQuestionValidationUndetermined)
		blocks = append(blocks, marketanalysis.MarketQuestionExplanationBlock{Index: len(blocks), Status: marketanalysis.MarketQuestionValidationUndetermined, Reason: "ANSWER_HAS_UNBOUND_CONTENT", ClaimRefs: []string{}})
	}
	return marketanalysis.MarketQuestionAnswer{Answer: strings.Join(accepted, "\n\n"), Source: answer.Source, Model: answer.Model, AsOfMS: answer.AsOfMS}, blocks, overall
}

func marketQuestionSemanticReview(ctx context.Context, config hermesrunner.MarketQuestionConfig, answer marketanalysis.MarketQuestionAnswer, bundle marketquestion.EvidenceBundleSnapshot) ([]hermesrunner.MarketQuestionReviewVerdict, error) {
	input, paragraphs := marketQuestionReviewInput(answer, bundle)
	if len(paragraphs) == 0 {
		return nil, nil
	}
	verdicts, err := hermesrunner.ReviewMarketQuestionExplanation(ctx, config, input)
	if err != nil {
		return nil, err
	}
	// Reject incomplete, duplicate and out-of-range verdict sets before applying.
	expected := map[int]bool{}
	for _, p := range paragraphs {
		expected[p.Index] = true
	}
	if len(verdicts) != len(expected) {
		return nil, fmt.Errorf("incomplete semantic verdict set")
	}
	for _, v := range verdicts {
		if !expected[v.Index] {
			return nil, fmt.Errorf("unexpected semantic verdict index")
		}
		delete(expected, v.Index)
	}
	return verdicts, nil
}
