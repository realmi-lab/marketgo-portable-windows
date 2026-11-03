package hermesrunner

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

type noticeSemanticSource struct {
	SourceID      string   `json:"source_id"`
	SHA256        string   `json:"sha256"`
	BodyTruncated *bool    `json:"body_truncated"`
	Passages      []string `json:"all_provided_passages"`
	Exchange      string   `json:"exchange"`
	Assets        []string `json:"assets"`
	Kind          string   `json:"kind"`
	BodyScope     string   `json:"body_scope"`
	Uncited       bool     `json:"uncited"`
}

type noticeSemanticSegment struct {
	ID    int    `json:"id"`
	Text  string `json:"text"`
	Field string `json:"field,omitempty"`
	Start int    `json:"start_byte"`
	End   int    `json:"end_byte"`
}

type noticeSemanticBinding struct {
	SourceID    string `json:"source_id"`
	SourceQuote string `json:"source_quote"`
}

type noticeSemanticClaim struct {
	ClaimQuote string                  `json:"claim_quote"`
	Role       string                  `json:"role"`
	Verdict    string                  `json:"verdict"`
	Bindings   []noticeSemanticBinding `json:"bindings"`
	Reason     string                  `json:"reason"`
}

type noticeSemanticAudit struct {
	EarlyRejection  *noticeSemanticRejection `json:"early_rejection,omitempty"`
	Verdict         string                   `json:"verdict"`
	ReviewedSources []struct {
		SourceID      string `json:"source_id"`
		SHA256        string `json:"sha256"`
		BodyTruncated *bool  `json:"body_truncated"`
		Scope         string `json:"scope"`
	} `json:"reviewed_sources"`
	Segments []struct {
		ID               int                   `json:"id"`
		Claims           []noticeSemanticClaim `json:"claims"`
		UnreviewedClaims []string              `json:"unreviewed_claims"`
	} `json:"segments"`
}

func noticeSemanticHash(payload []byte) string { return fmt.Sprintf("%x", sha256.Sum256(payload)) }

func noticeSemanticSegments(answer noticeQuestionModelResult) []noticeSemanticSegment {
	segments := []noticeSemanticSegment{}
	texts := append(append([]string{answer.Answer}, answer.KeyPoints...), answer.Caveat)
	for field, text := range texts {
		name := fmt.Sprintf("key_points[%d]", field-1)
		if field == 0 {
			name = "answer"
		} else if field == len(texts)-1 {
			name = "caveat"
		}
		start := 0
		for at, r := range text {
			end := at + len(string(r))
			if r != '\n' && !strings.ContainsRune(".!?", r) {
				continue
			}
			if r != '\n' && end < len(text) && text[end] != ' ' && text[end] != '\n' {
				continue
			}
			if strings.TrimSpace(text[start:end]) == "" {
				continue
			}
			segments = append(segments, noticeSemanticSegment{ID: len(segments) + 1, Text: text[start:end], Field: name, Start: start, End: end})
			start = end
		}
		if start < len(text) {
			segments = append(segments, noticeSemanticSegment{ID: len(segments) + 1, Text: text[start:], Field: name, Start: start, End: len(text)})
		}
	}
	return segments
}

func noticeSemanticSources(result noticeQuestionModelResult, evidence []byte) []noticeSemanticSource {
	sources := []noticeSemanticSource{}
	for _, source := range noticeQuestionClaimSources(evidence) {
		if strings.HasPrefix(source.SourceID, "server-volume:") {
			continue
		}
		payload, _ := json.Marshal(source)
		passages := append([]string{"TITLE (document heading, not publication/event time): " + source.Title}, source.Passages...)
		sources = append(sources, noticeSemanticSource{SourceID: source.SourceID, SHA256: noticeSemanticHash(payload), BodyTruncated: source.BodyTruncated, Passages: passages, Exchange: source.Exchange, Assets: source.Assets, Kind: "OFFICIAL_NOTICE", BodyScope: source.BodyScope, Uncited: !slices.Contains(result.SourceNoticeIDs, source.SourceID)})
	}
	var root map[string]json.RawMessage
	_ = json.Unmarshal(evidence, &root)
	passages := []string{}
	for _, field := range []string{"retrieval", "reference_time_kst", "source_availability", "daily_asset_volumes", "volume_scope"} {
		if value := root[field]; len(value) > 0 {
			passages = append(passages, field+": "+string(value))
		}
	}
	if len(passages) > 0 {
		payload, _ := json.Marshal(passages)
		complete := false
		sources = append(sources, noticeSemanticSource{SourceID: "server:scope", SHA256: noticeSemanticHash(payload), BodyTruncated: &complete, Passages: passages, Kind: "SERVER_FACTS"})
	}
	return sources
}

func noticeSemanticMechanicalGate(result noticeQuestionModelResult, evidence []byte) error {
	if err := validateNoticeQuestionResultNumbers(result, "", evidence); err != nil {
		return err
	}
	var root struct {
		Notices []struct {
			SourceID string `json:"source_id"`
		} `json:"notices"`
	}
	if err := json.Unmarshal(evidence, &root); err != nil {
		return err
	}
	if len(result.SourceNoticeIDs) == 0 {
		return errors.New("source catalog is empty")
	}
	for _, id := range result.SourceNoticeIDs {
		if !slices.ContainsFunc(root.Notices, func(row struct {
			SourceID string `json:"source_id"`
		}) bool {
			return row.SourceID == id
		}) {
			return fmt.Errorf("uncatalogued source: %s", id)
		}
	}
	return nil
}

func validateNoticeSemanticAudit(audit noticeSemanticAudit, segments []noticeSemanticSegment, sources []noticeSemanticSource) error {
	if audit.EarlyRejection != nil {
		return validateNoticeSemanticEarlyRejection(audit, segments, sources)
	}
	if !slices.Contains([]string{"SUPPORTED", "UNSUPPORTED", "UNRESOLVED"}, audit.Verdict) {
		return errors.New("unknown verdict")
	}
	if len(audit.ReviewedSources) != len(sources) || len(audit.Segments) != len(segments) {
		return errors.New("missing full-source review or answer segments")
	}
	seenSources := map[string]bool{}
	for _, receipt := range audit.ReviewedSources {
		i := slices.IndexFunc(sources, func(source noticeSemanticSource) bool { return source.SourceID == receipt.SourceID })
		if i < 0 || seenSources[receipt.SourceID] || receipt.Scope != "ALL_PROVIDED_PASSAGES" || receipt.SHA256 != sources[i].SHA256 {
			return errors.New("invalid source review receipt")
		}
		left, _ := json.Marshal(receipt.BodyTruncated)
		right, _ := json.Marshal(sources[i].BodyTruncated)
		if string(left) != string(right) {
			return errors.New("source truncation boundary changed")
		}
		seenSources[receipt.SourceID] = true
	}
	seen := map[int]bool{}
	for _, segment := range audit.Segments {
		if segment.ID < 1 || segment.ID > len(segments) || seen[segment.ID] || len(segment.Claims) == 0 {
			return errors.New("unknown, repeated or empty segment")
		}
		seen[segment.ID] = true
		text := segments[segment.ID-1].Text
		covered := make([]bool, len(text))
		for _, claim := range segment.Claims {
			if !slices.Contains([]string{"ASSERTION", "DOCUMENT_ABSENCE", "EVIDENCE_LIMIT", "INQUIRY_TOPIC", "NONFACTUAL", "MIXED"}, claim.Role) || !slices.Contains([]string{"SUPPORTED", "UNSUPPORTED", "UNRESOLVED"}, claim.Verdict) {
				return errors.New("unknown typed claim role or verdict")
			}
			at := strings.Index(text, claim.ClaimQuote)
			if claim.ClaimQuote == "" || at < 0 {
				return errors.New("claim quote is not an exact answer span")
			}
			for i := at; i < at+len(claim.ClaimQuote); i++ {
				covered[i] = true
			}
			if len(claim.Bindings) == 0 && claim.Role != "NONFACTUAL" {
				return errors.New("unbound factual claim")
			}
			for _, binding := range claim.Bindings {
				i := slices.IndexFunc(sources, func(source noticeSemanticSource) bool { return source.SourceID == binding.SourceID })
				if i < 0 || binding.SourceQuote == "" || !slices.ContainsFunc(sources[i].Passages, func(passage string) bool { return strings.Contains(passage, binding.SourceQuote) }) {
					return errors.New("source quote is not an exact cited official span")
				}
				if audit.Verdict == "SUPPORTED" && sources[i].Uncited {
					return errors.New("support borrowed an uncited source")
				}
			}
			if audit.Verdict == "SUPPORTED" && claim.Verdict != "SUPPORTED" {
				return errors.New("overall support contradicts a claim verdict")
			}
		}
		if len(segment.UnreviewedClaims) > 0 && audit.Verdict == "SUPPORTED" {
			return errors.New("partial review cannot support the answer")
		}
		for i, r := range text {
			if !covered[i] && !strings.ContainsRune(" \t\r\n*-_|:#.,;!?()[]“”‘’", r) {
				return errors.New("unreviewed answer text outside audited claim spans")
			}
		}
	}
	return nil
}
