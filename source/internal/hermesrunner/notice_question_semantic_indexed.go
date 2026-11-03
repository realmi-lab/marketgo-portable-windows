package hermesrunner

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

type noticeSemanticEvidenceSpan struct {
	SourceID string
	Text     string
}

type noticeSemanticIndexedClaim struct {
	ClaimQuote  string   `json:"claim_quote"`
	Role        string   `json:"role"`
	Verdict     string   `json:"verdict"`
	EvidenceIDs []string `json:"evidence_ids"`
	Reason      string   `json:"reason"`
}

type noticeSemanticIndexedAudit struct {
	Verdict         string   `json:"verdict"`
	ReviewedSources []string `json:"reviewed_sources"`
	Segments        []struct {
		ID               int                          `json:"id"`
		Claims           []noticeSemanticIndexedClaim `json:"claims"`
		UnreviewedClaims []string                     `json:"unreviewed_claims"`
	} `json:"segments"`
}

func noticeSemanticIndexedPayload(segments []noticeSemanticSegment, sources []noticeSemanticSource) (string, map[string]noticeSemanticEvidenceSpan) {
	spans := map[string]noticeSemanticEvidenceSpan{}
	spanText := map[string]string{}
	indexedSources := []map[string]any{}
	for _, source := range sources {
		seen := map[string]string{}
		passages := [][]string{}
		for _, passage := range source.Passages {
			ids := []string{}
			for _, line := range strings.Split(passage, "\n") {
				id := seen[line]
				if id == "" {
					id = fmt.Sprintf("e%d", len(spans))
					seen[line] = id
					spans[id] = noticeSemanticEvidenceSpan{source.SourceID, line}
					spanText[id] = line
				}
				ids = append(ids, id)
			}
			passages = append(passages, ids)
		}
		indexedSources = append(indexedSources, map[string]any{"source_id": source.SourceID, "sha256": source.SHA256, "body_truncated": source.BodyTruncated, "body_scope": source.BodyScope, "exchange": source.Exchange, "assets": source.Assets, "kind": source.Kind, "uncited": source.Uncited, "ordered_passages": passages})
	}
	payload, _ := json.Marshal(map[string]any{"segments": segments, "sources": indexedSources, "evidence_spans": spanText})
	return string(payload), spans
}

func noticeSemanticExpandIndexed(input noticeSemanticIndexedAudit, sources []noticeSemanticSource, spans map[string]noticeSemanticEvidenceSpan) (noticeSemanticAudit, error) {
	audit := noticeSemanticAudit{Verdict: input.Verdict}
	for _, id := range input.ReviewedSources {
		found := false
		for _, source := range sources {
			if source.SourceID == id {
				audit.ReviewedSources = append(audit.ReviewedSources, struct {
					SourceID      string `json:"source_id"`
					SHA256        string `json:"sha256"`
					BodyTruncated *bool  `json:"body_truncated"`
					Scope         string `json:"scope"`
				}{id, source.SHA256, source.BodyTruncated, "ALL_PROVIDED_PASSAGES"})
				found = true
				break
			}
		}
		if !found {
			return audit, fmt.Errorf("indexed audit reviewed unknown source %s", id)
		}
	}
	for _, segment := range input.Segments {
		claims := []noticeSemanticClaim{}
		for _, claim := range segment.Claims {
			expanded := noticeSemanticClaim{ClaimQuote: claim.ClaimQuote, Role: claim.Role, Verdict: claim.Verdict, Reason: claim.Reason}
			for _, id := range claim.EvidenceIDs {
				span, ok := spans[id]
				if !ok {
					return audit, fmt.Errorf("indexed audit cited unknown span %s", id)
				}
				// Blank lines remain in ordered passages for exact reconstruction,
				// but cannot supply factual support. The validator still requires
				// a nonempty binding for every factual claim.
				if strings.TrimSpace(span.Text) == "" {
					continue
				}
				expanded.Bindings = append(expanded.Bindings, noticeSemanticBinding{span.SourceID, span.Text})
			}
			claims = append(claims, expanded)
		}
		audit.Segments = append(audit.Segments, struct {
			ID               int                   `json:"id"`
			Claims           []noticeSemanticClaim `json:"claims"`
			UnreviewedClaims []string              `json:"unreviewed_claims"`
		}{segment.ID, claims, segment.UnreviewedClaims})
	}
	return audit, nil
}

type noticeSemanticVerdictAudit struct {
	Rejection       *noticeSemanticRejection `json:"rejection,omitempty"`
	Verdict         string                   `json:"verdict"`
	ReviewedSources []string                 `json:"reviewed_sources"`
	Checks          []struct {
		ID                int      `json:"id"`
		Verdict           string   `json:"v"`
		Roles             []string `json:"r"`
		EvidenceIDs       []string `json:"e"`
		AllClaimsReviewed bool     `json:"a"`
	} `json:"checks"`
}

func noticeSemanticVerdictInput(segments []noticeSemanticSegment, sources []noticeSemanticSource) (string, map[string]noticeSemanticEvidenceSpan) {
	payload, spans := noticeSemanticIndexedPayload(segments, sources)
	prompt := noticeQuestionAuditPromptTemplate
	return prompt + payload, spans
}

func noticeSemanticExpandVerdicts(input noticeSemanticVerdictAudit, segments []noticeSemanticSegment, sources []noticeSemanticSource, spans map[string]noticeSemanticEvidenceSpan) (noticeSemanticAudit, error) {
	if input.Rejection != nil {
		return noticeSemanticExpandRejection(input, segments, sources, spans)
	}
	indexed := noticeSemanticIndexedAudit{Verdict: input.Verdict, ReviewedSources: input.ReviewedSources}
	for _, check := range input.Checks {
		if check.ID < 1 || check.ID > len(segments) || len(check.Roles) == 0 {
			return noticeSemanticAudit{}, fmt.Errorf("invalid verdict segment or missing roles")
		}
		for _, role := range check.Roles {
			if !slices.Contains([]string{"ASSERTION", "DOCUMENT_ABSENCE", "EVIDENCE_LIMIT", "INQUIRY_TOPIC", "NONFACTUAL"}, role) {
				return noticeSemanticAudit{}, fmt.Errorf("invalid verdict role")
			}
		}
		role := check.Roles[0]
		if len(check.Roles) > 1 {
			role = "MIXED"
		}
		unreviewed := []string{}
		if !check.AllClaimsReviewed {
			unreviewed = append(unreviewed, "not all independent claims were reviewed")
		}
		indexed.Segments = append(indexed.Segments, struct {
			ID               int                          `json:"id"`
			Claims           []noticeSemanticIndexedClaim `json:"claims"`
			UnreviewedClaims []string                     `json:"unreviewed_claims"`
		}{check.ID, []noticeSemanticIndexedClaim{{ClaimQuote: segments[check.ID-1].Text, Role: role, Verdict: check.Verdict, EvidenceIDs: check.EvidenceIDs, Reason: "compact verdict; exact quotes reconstructed from immutable IDs"}}, unreviewed})
	}
	return noticeSemanticExpandIndexed(indexed, sources, spans)
}
