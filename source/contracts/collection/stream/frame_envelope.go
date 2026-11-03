package collector

import (
	"encoding/json"
	"errors"
)

func (e FrameEnvelope) MarshalJSON() ([]byte, error) {
	type wire FrameEnvelope
	e.TradeMatchCounts = make([]uint64, len(e.Message.Trades))
	for i, trade := range e.Message.Trades {
		e.TradeMatchCounts[i] = trade.MatchCount
	}
	return json.Marshal(wire(e))
}
func (e *FrameEnvelope) UnmarshalJSON(raw []byte) error {
	type wire FrameEnvelope
	var decoded wire
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	if len(decoded.TradeMatchCounts) != len(decoded.Message.Trades) {
		return errors.New("frame trade match-count evidence is incomplete")
	}
	for i, count := range decoded.TradeMatchCounts {
		decoded.Message.Trades[i].MatchCount = count
	}
	*e = FrameEnvelope(decoded)
	return nil
}
