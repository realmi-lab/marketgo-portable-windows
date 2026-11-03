// Package ingestlog defines the durable boundary between collection and computation.
// It contains no calculation, HTTP API, or model execution code.
package ingestlog

import (
	"encoding/json"
	"errors"
	"fmt"
	"market-share-go/internal/domain"
)

const Version = 1
const MaxBatch = 512

// MaxReadBatch lets a restarted calculator drain an existing journal with
// fewer full-state checkpoint transactions. Collector append batches keep the
// smaller MaxBatch durability bound.
const MaxReadBatch = 4096

type Event struct {
	Version         int             `json:"version"`
	Cursor          int64           `json:"cursor"`
	ID              string          `json:"id"`
	Kind            string          `json:"kind"`
	ReceivedAtNS    int64           `json:"received_at_ns"`
	Payload         json.RawMessage `json:"payload"`
	ProjectionError string          `json:"projection_error,omitempty"`
}

func (e Event) Validate() error {
	if e.Version != Version || e.ID == "" || len(e.ID) > 256 || e.ReceivedAtNS <= 0 {
		return errors.New("invalid ingestion envelope")
	}
	{
		var marker struct {
			Archived bool
			CompactV int
		}
		if err := json.Unmarshal(e.Payload, &marker); err != nil {
			// Unmarshal already validates the complete document. Keep the
			// envelope error contract without a separate json.Valid pass.
			var syntax *json.SyntaxError
			if errors.As(err, &syntax) {
				return errors.New("invalid ingestion envelope")
			}
			return err
		}
		if marker.Archived {
			return errors.New("ingestion input has expired; historical recovery required")
		}
		if marker.CompactV != 0 {
			return errors.New("compact ingestion pointer must be resolved before replay")
		}
	}
	if e.Kind == "frame" {
		if err := validateFrameEnvelope(e.Payload, e.ReceivedAtNS); err != nil {
			return err
		}
	}
	switch e.Kind {
	case "frame", "loss", "registry", "official", "health":
		return nil
	default:
		return fmt.Errorf("unsupported ingestion event kind %q", e.Kind)
	}
}

func validateFrameEnvelope(payload []byte, receivedAtNS int64) error {
	var value struct {
		Frame            *domain.WebSocketFrame
		Message          json.RawMessage
		TradeMatchCounts []uint64
	}
	if err := json.Unmarshal(payload, &value); err != nil {
		return err
	}
	frame := value.Frame
	if frame == nil || !frame.Exchange.Valid() || frame.ConnectionID == "" || frame.Sequence == 0 || frame.ReceivedAtNS != receivedAtNS || (frame.MessageType != 1 && frame.MessageType != 2) {
		return errors.New("missing or inconsistent raw frame identity")
	}
	var parsed *struct{ Trades []json.RawMessage }
	if err := json.Unmarshal(value.Message, &parsed); err != nil {
		return err
	}
	if parsed == nil || len(parsed.Trades) != len(value.TradeMatchCounts) {
		return errors.New("missing parsed frame or trade match-count evidence")
	}
	return nil
}
