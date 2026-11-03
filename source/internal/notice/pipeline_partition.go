package notice

import (
	"encoding/json"
	"fmt"
)

// PipelinePartitions never cuts a block or table row. Every partition retains
// the title and the original block IDs/offsets. A section result is explicitly
// scoped: it cannot establish the absence of conditions elsewhere in a notice.
func PipelinePartitions(rev PipelineRevision) ([]PipelineRevision, error) {
	const limit = 8_000 // Reserves space for shared context and audit output.
	base := PipelineRevision{ID: rev.ID, Item: Item{Title: rev.Item.Title, OriginalTitle: rev.Item.OriginalTitle}, Availability: rev.Availability}
	var parts []PipelineRevision
	part := base
	for _, block := range rev.Blocks {
		candidate := part
		candidate.Blocks = append(append([]PipelineBlock(nil), part.Blocks...), block)
		raw, err := json.Marshal(candidate)
		if err != nil {
			return nil, err
		}
		if len(raw) > limit {
			if len(part.Blocks) == 0 {
				return nil, fmt.Errorf("%w: indivisible source block exceeds partition budget", ErrPipelineInvalid)
			}
			parts = append(parts, part)
			part = base
			part.Blocks = []PipelineBlock{block}
			raw, _ = json.Marshal(part)
			if len(raw) > limit {
				return nil, fmt.Errorf("%w: indivisible source block exceeds partition budget", ErrPipelineInvalid)
			}
		} else {
			part = candidate
		}
	}
	if len(part.Blocks) > 0 {
		parts = append(parts, part)
	}
	// Repeat structural headings, table headers and boundary notes rather than
	// letting an isolated row lose the subject/unit or a trailing qualification.
	if len(parts) > 1 {
		shared := map[string]bool{}
		for i, b := range rev.Blocks {
			if i == 0 || i == len(rev.Blocks)-1 || b.Kind == "HEADING" || (b.Kind == "TABLE_ROW" && (i == 0 || rev.Blocks[i-1].Kind != "TABLE_ROW")) {
				shared[b.ID] = true
			}
		}
		for i := range parts {
			keep := map[string]bool{}
			for _, b := range parts[i].Blocks {
				keep[b.ID] = true
			}
			parts[i].Blocks = nil
			for _, b := range rev.Blocks {
				if keep[b.ID] || shared[b.ID] {
					parts[i].Blocks = append(parts[i].Blocks, b)
				}
			}
			raw, _ := json.Marshal(parts[i])
			if len(raw) > 12_000 {
				return nil, fmt.Errorf("%w: shared context exceeds partition budget", ErrPipelineInvalid)
			}
		}
	}
	return parts, nil
}

type PipelinePartition struct {
	Ordinal  int              `json:"ordinal"`
	Total    int              `json:"total"`
	Revision PipelineRevision `json:"revision"`
}
