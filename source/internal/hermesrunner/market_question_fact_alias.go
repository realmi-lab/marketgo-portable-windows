package hermesrunner

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Short references exist only at the provider boundary. Server evidence/cache
// identities remain canonical; unknown or mixed references fail closed.
func compactMarketQuestionFactIDs(evidence []byte) ([]byte, map[string]string) {
	var input map[string]json.RawMessage
	if json.Unmarshal(evidence, &input) != nil {
		return evidence, nil
	}
	raw := input["typed_evidence_bundle"]
	if len(raw) == 0 {
		raw = input["bundle"]
	}
	var bundle struct {
		Facts []struct {
			ID string `json:"id"`
		} `json:"facts"`
	}
	if json.Unmarshal(raw, &bundle) != nil || len(bundle.Facts) == 0 {
		return evidence, nil
	}
	ids := map[string]string{}
	text := string(evidence)
	for i, f := range bundle.Facts {
		if !strings.HasPrefix(f.ID, "fact_") {
			return evidence, nil
		}
		alias := fmt.Sprintf("fact_%d", i)
		// Avoid replacing an original ID that could collide with a generated alias.
		for _, other := range bundle.Facts {
			if other.ID == alias {
				return evidence, nil
			}
		}
		ids[alias] = f.ID
	}
	for alias, id := range ids {
		text = strings.ReplaceAll(text, `"`+id+`"`, `"`+alias+`"`)
	}
	// Explicitly mark this as a wire projection, not a canonical persisted bundle.
	var projection map[string]json.RawMessage
	_ = json.Unmarshal([]byte(text), &projection)
	projection["fact_reference_encoding"] = json.RawMessage(`"LOCAL_ALIASES_V1"`)
	encoded, _ := json.Marshal(projection)
	return encoded, ids
}

func expandMarketQuestionFactIDs(result *marketQuestionModelResult, ids map[string]string) error {
	if len(ids) == 0 {
		return nil
	}
	for bi := range result.ClaimBlocks {
		for ci := range result.ClaimBlocks[bi].Claims {
			claim := &result.ClaimBlocks[bi].Claims[ci]
			canonical, ok := ids[claim.FactID]
			if !ok {
				return fmt.Errorf("unknown compact fact reference")
			}
			claim.FactID = canonical
		}
	}
	return nil
}
