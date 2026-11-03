package hermesrunner

import (
	"errors"
	"slices"
	"strings"
)

// This path can only reject. Exact receipts and scope checks do not prove
// semantic contradiction; that remains a model judgement evaluated separately.
type noticeSemanticRejection struct {
	ID          int      `json:"id"`
	Role        string   `json:"role"`
	Basis       string   `json:"basis"`
	EvidenceIDs []string `json:"e"`
	Why         string   `json:"why"`
}

func noticeSemanticExpandRejection(input noticeSemanticVerdictAudit, segments []noticeSemanticSegment, sources []noticeSemanticSource, spans map[string]noticeSemanticEvidenceSpan) (noticeSemanticAudit, error) {
	r := input.Rejection
	if input.Verdict != "UNSUPPORTED" || len(input.Checks) != 0 || r.ID < 1 || r.ID > len(segments) || len(r.EvidenceIDs) == 0 || len(strings.TrimSpace(r.Why)) < 12 || len(r.Why) > 1600 {
		return noticeSemanticAudit{}, errors.New("invalid early rejection boundary")
	}
	if !slices.Contains([]string{"ASSERTION", "DOCUMENT_ABSENCE", "EVIDENCE_LIMIT"}, r.Role) || !slices.Contains([]string{"COUNTEREVIDENCE", "NO_SUPPORT_IN_FULL_SCOPE"}, r.Basis) {
		return noticeSemanticAudit{}, errors.New("unresolved rejection role or basis")
	}
	indexed := noticeSemanticIndexedAudit{Verdict: "UNSUPPORTED", ReviewedSources: input.ReviewedSources}
	indexed.Segments = append(indexed.Segments, struct {
		ID               int                          `json:"id"`
		Claims           []noticeSemanticIndexedClaim `json:"claims"`
		UnreviewedClaims []string                     `json:"unreviewed_claims"`
	}{r.ID, []noticeSemanticIndexedClaim{{ClaimQuote: segments[r.ID-1].Text, Role: r.Role, Verdict: "UNSUPPORTED", EvidenceIDs: r.EvidenceIDs, Reason: r.Why}}, nil})
	audit, err := noticeSemanticExpandIndexed(indexed, sources, spans)
	if err != nil {
		return audit, err
	}
	audit.EarlyRejection = r
	return audit, nil
}

func validateNoticeSemanticEarlyRejection(audit noticeSemanticAudit, segments []noticeSemanticSegment, sources []noticeSemanticSource) error {
	r := audit.EarlyRejection
	if audit.Verdict != "UNSUPPORTED" || len(audit.Segments) != 1 || r.ID < 1 || r.ID > len(segments) || len(audit.Segments[0].Claims) != 1 {
		return errors.New("early rejection cannot admit a positive answer")
	}
	claim := audit.Segments[0].Claims[0]
	if claim.Verdict != "UNSUPPORTED" || claim.ClaimQuote != segments[r.ID-1].Text || len(claim.Bindings) == 0 {
		return errors.New("early rejection lacks an exact rejected answer segment")
	}
	reviewed := []noticeSemanticSource{}
	for _, receipt := range audit.ReviewedSources {
		for _, source := range sources {
			if source.SourceID == receipt.SourceID {
				reviewed = append(reviewed, source)
				break
			}
		}
	}
	if len(reviewed) == 0 {
		return errors.New("early rejection reviewed no source")
	}
	// Reuse strict hash/quote/coverage checks on the exact one rejected segment.
	checked := audit
	checked.EarlyRejection = nil
	checked.Segments = append(checked.Segments[:0:0], audit.Segments...)
	checked.Segments[0].ID = 1
	if err := validateNoticeSemanticAudit(checked, []noticeSemanticSegment{{ID: 1, Text: segments[r.ID-1].Text}}, reviewed); err != nil {
		return err
	}
	claimSources := []noticeQuestionClaimSource{}
	for _, source := range sources {
		if source.Kind == "OFFICIAL_NOTICE" {
			claimSources = append(claimSources, noticeQuestionClaimSource{SourceID: source.SourceID, Exchange: source.Exchange, Assets: source.Assets})
		}
	}
	groups, err := noticeEvidenceSourceGroups(claim.ClaimQuote, claimSources)
	if err != nil {
		return err
	}
	if r.Basis == "NO_SUPPORT_IN_FULL_SCOPE" {
		// Missing policy is not established by a lone quote or an excerpt.
		// Require full review of every relevant source in this bounded snapshot.
		for _, group := range groups {
			for _, required := range group {
				at := slices.IndexFunc(reviewed, func(s noticeSemanticSource) bool { return s.SourceID == required.SourceID })
				if at < 0 || reviewed[at].BodyScope != "FULL_STORED_ORIGINAL" {
					return errors.New("absence rejection lacks full relevant stored-source review")
				}
			}
		}
	}
	// At least one cited counterevidence span must belong to the claim's scope.
	bound := false
	for _, group := range groups {
		for _, source := range group {
			for _, binding := range claim.Bindings {
				if binding.SourceID == source.SourceID {
					bound = true
				}
			}
		}
	}
	if !bound {
		return errors.New("early rejection counterevidence has another claim scope")
	}
	return nil
}
