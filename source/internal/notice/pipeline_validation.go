package notice

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"
)

func ValidatePipelineRequest(req PipelineRequest) error {
	bad := func(reason string) error { return fmt.Errorf("%w: %s", ErrPipelineInvalid, reason) }
	if !slices.Contains([]string{"snapshot", "capabilities", "source", "claim", "heartbeat", "object", "object_get", "page", "complete", "fail"}, req.Operation) {
		return bad("operation")
	}
	if req.Operation == "capabilities" || req.Operation == "snapshot" {
		return nil
	}
	if req.Exchange != "" && (!IsDomesticExchange(req.Exchange) || SourcePaused(req.Exchange)) {
		return bad("exchange")
	}
	if req.Operation != "claim" && req.Exchange == "" {
		return bad("exchange required")
	}
	if len(req.RequestID) > 200 || len(req.WorkerID) > 200 || len(req.ErrorCode) > 1000 {
		return bad("field limit")
	}
	if !slices.Contains([]string{"claim", "heartbeat", "object_get"}, req.Operation) && req.RequestID == "" {
		return bad("request id required")
	}
	if req.Operation == "source" {
		if req.Source == nil || req.Source.Generation < 1 || !slices.Contains([]string{"LEGACY", "V2"}, req.Source.CollectorMode) || !slices.Contains([]string{"LEGACY", "SHADOW", "V2"}, req.Source.ReadMode) {
			return bad("source")
		}
		return nil
	}
	if req.Operation == "claim" {
		if req.WorkerID == "" || !slices.Contains([]string{"LIST_PAGE", "BODY", "OCR", "SEMANTIC_GENERATE", "SEMANTIC_AUDIT"}, req.Stage) {
			return bad("claim")
		}
		return nil
	}
	if !pipelineUUID(req.JobID) {
		return bad("job id")
	}
	if req.Operation != "object" && !pipelineUUID(req.LeaseToken) {
		return bad("lease token")
	}
	if req.Operation == "object" {
		if req.Object == nil || len(req.Object.Bytes) == 0 || len(req.Object.Bytes) > PipelineMaxObjectBytes || len(req.Object.MediaType) > 200 || req.SourceGeneration < 1 {
			return bad("object")
		}
		// Caller supplied IDs cannot overwrite an existing object.
		if req.Object.ID != "" {
			return bad("object id is server assigned")
		}
	}
	if req.Operation == "object_get" && !pipelineUUID(req.ObjectID) {
		return bad("object id")
	}
	if req.Page != nil && (len(req.Page.Items) > HermesMaxBatchItems || len(req.Page.ObjectIDs) > 64 || req.Page.Ordinal < 0) {
		return bad("page limit")
	}
	return nil
}

func pipelineUUID(id string) bool {
	if len(id) != 36 {
		return false
	}
	for i, r := range id {
		if slices.Contains([]int{8, 13, 18, 23}, i) {
			if r != '-' {
				return false
			}
		} else if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}

func ValidatePipelineRevision(rev *PipelineRevision) error {
	item := rev.Item
	if !OfficialNoticeURLMatchesID(item.Exchange, item.NoticeID, item.URL) || len(item.OriginalBodyText) > 1_000_000 || !utf8.ValidString(item.OriginalBodyText) || len(item.Title) > 2000 || len(rev.Media) > 1024 || len(rev.ObjectIDs) > 64 {
		return ErrPipelineInvalid
	}
	if strings.TrimSpace(item.OriginalBodyText) == "" && len(rev.Media) == 0 {
		return ErrHermesEvidenceEmpty
	}
	for _, media := range rev.Media {
		u, err := url.Parse(media.URL)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || !slices.Contains([]string{"COMPLETE", "PENDING", "FAILED", "POLICY_UNSUPPORTED"}, media.Status) {
			return ErrPipelineInvalid
		}
		if media.Status == "COMPLETE" && media.ObjectID == "" {
			return ErrPipelineInvalid
		}
	}
	// Rebuild offsets at the trusted boundary; worker-provided blocks are not
	// evidence authority. Original HTML/JSON bytes remain separate objects.
	rev.Blocks = PipelineBlocks(item.OriginalBodyText)
	rev.Availability = "COMPLETE"
	for _, m := range rev.Media {
		if m.Status != "COMPLETE" {
			rev.Availability = "PARTIAL"
		}
	}
	return nil
}

func PipelineBlocks(text string) []PipelineBlock {
	var blocks []PipelineBlock
	start := 0
	for _, line := range strings.SplitAfter(text, "\n") {
		if strings.TrimSpace(line) != "" {
			blocks = append(blocks, PipelineBlock{ID: fmt.Sprintf("b%d", len(blocks)+1), Kind: "TEXT", Text: line, Start: start, End: start + len(line)})
		}
		start += len(line)
	}
	return blocks
}

func ValidatePipelineDerivation(rev PipelineRevision, d PipelineDerivation, audit bool) error {
	if d.RevisionID != rev.ID || d.Policy != PipelinePolicy || len(d.Facts) > 256 || len(d.Summary) > 5 || d.Provider == "" || d.Model == "" {
		return ErrPipelineInvalid
	}
	blocks := map[string]string{}
	for _, b := range rev.Blocks {
		blocks[b.ID] = b.Text
	}
	facts := map[string]bool{}
	for _, f := range d.Facts {
		if f.ID == "" || facts[f.ID] || f.Subject == "" || f.Action == "" || len(f.Evidence) == 0 || !slices.Contains([]string{"POSITIVE", "NEGATIVE"}, f.Polarity) || !slices.Contains([]string{"PLANNED", "COMPLETED", "POSSIBLE", "CONDITIONAL", "STATED"}, f.Assertion) || !slices.Contains([]string{"EXPLICIT", "NAME_ONLY", "UNKNOWN"}, f.AssetScope) {
			return ErrPipelineInvalid
		}
		facts[f.ID] = true
		if err := validatePipelineCondition(f.ConditionLogic, 0); err != nil {
			return err
		}
		if len(f.Conditions) > 1 && f.ConditionLogic == nil {
			return fmt.Errorf("%w: condition logic required", ErrPipelineInvalid)
		}
		if f.AssetScope != "EXPLICIT" && len(f.Assets) > 0 {
			return fmt.Errorf("%w: inferred asset symbols", ErrPipelineInvalid)
		}
		for _, e := range f.Evidence {
			if e.Quote == "" || !strings.Contains(blocks[e.BlockID], e.Quote) {
				return fmt.Errorf("%w: fact evidence", ErrPipelineInvalid)
			}
		}
		for _, asset := range f.Assets {
			found := false
			for _, e := range f.Evidence {
				for _, token := range noticeTokenPattern.FindAllString(e.Quote, -1) {
					if strings.EqualFold(asset, token) {
						found = true
					}
				}
			}
			if !found {
				return fmt.Errorf("%w: asset absent from fact evidence", ErrPipelineInvalid)
			}
		}
	}
	for _, line := range d.Summary {
		if strings.TrimSpace(line.Text) == "" || len(line.FactIDs) == 0 {
			return ErrPipelineInvalid
		}
		for _, id := range line.FactIDs {
			if !facts[id] {
				return ErrPipelineInvalid
			}
		}
	}
	if !audit {
		return nil
	}
	if d.Audit == nil || d.Audit.InputDigest != PipelineDigest(rev) || len(d.Audit.ReviewedBlockIDs) != len(blocks) {
		return fmt.Errorf("%w: incomplete original review", ErrPipelineInvalid)
	}
	seen := map[string]bool{}
	for _, id := range d.Audit.ReviewedBlockIDs {
		if seen[id] || blocks[id] == "" {
			return ErrPipelineInvalid
		}
		seen[id] = true
	}
	if !slices.Contains([]string{"SUPPORTED", "UNSUPPORTED", "UNRESOLVED"}, d.Audit.FactsVerdict) || !slices.Contains([]string{"SUPPORTED", "UNSUPPORTED", "UNRESOLVED"}, d.Audit.SummaryVerdict) {
		return ErrPipelineInvalid
	}
	return nil
}

func validatePipelineCondition(node *PipelineCondition, depth int) error {
	if node == nil {
		return nil
	}
	if depth > 8 {
		return ErrPipelineInvalid
	}
	switch node.Operator {
	case "LEAF":
		if strings.TrimSpace(node.Text) == "" || len(node.Children) != 0 {
			return ErrPipelineInvalid
		}
	case "NOT":
		if len(node.Children) != 1 {
			return ErrPipelineInvalid
		}
	case "AND", "OR", "BEFORE", "AFTER":
		if len(node.Children) < 2 || len(node.Children) > 32 {
			return ErrPipelineInvalid
		}
	default:
		return ErrPipelineInvalid
	}
	for i := range node.Children {
		if err := validatePipelineCondition(&node.Children[i], depth+1); err != nil {
			return err
		}
	}
	return nil
}
