package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"

	marketanalysis "market-share-go/internal/analysis"
)

// assignAnalysisContextID fingerprints the complete immutable payload rather
// than a partial list of dependencies. Any result-changing input therefore
// produces a new broadcaster identity, while byte-equivalent frames retain the
// same identity and can be safely suppressed.
func assignAnalysisContextID(result *marketanalysis.Analysis) error {
	if result == nil {
		return errors.New("analysis result is required")
	}
	result.Context.AnalysisContextID = ""
	if result.ReactionSummary != nil {
		result.ReactionSummary["analysis_context_id"] = ""
	}
	digest := sha256.New()
	writer := &jsonDigestWriter{target: digest}
	if err := json.NewEncoder(writer).Encode(result); err != nil {
		return err
	}
	if err := writer.finish(); err != nil {
		return err
	}
	sum := digest.Sum(nil)
	identity := "ms-" + hex.EncodeToString(sum[:16])
	result.Context.AnalysisContextID = identity
	if result.ReactionSummary != nil {
		result.ReactionSummary["analysis_context_id"] = identity
	}
	return nil
}

// jsonDigestWriter keeps only the final byte pending so Encoder's trailing
// newline can be discarded. The resulting digest is byte-for-byte identical
// to json.Marshal without retaining a multi-megabyte analysis payload solely
// for identity calculation.
type jsonDigestWriter struct {
	target  io.Writer
	last    byte
	hasLast bool
}

func (w *jsonDigestWriter) Write(payload []byte) (int, error) {
	length := len(payload)
	if length == 0 {
		return 0, nil
	}
	if w.hasLast {
		previous := [1]byte{w.last}
		if _, err := w.target.Write(previous[:]); err != nil {
			return 0, err
		}
	}
	if length > 1 {
		if _, err := w.target.Write(payload[:length-1]); err != nil {
			return 0, err
		}
	}
	w.last = payload[length-1]
	w.hasLast = true
	return length, nil
}

func (w *jsonDigestWriter) finish() error {
	if !w.hasLast || w.last == '\n' {
		return nil
	}
	last := [1]byte{w.last}
	_, err := w.target.Write(last[:])
	return err
}
