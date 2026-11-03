package movement

import (
	"errors"
	"fmt"
	"math"
)

var ErrDirectionChangeInvariant = errors.New("official movement direction/change invariant violated")

// ValidateEventDirection prevents an event label from contradicting the share
// values it exposes. Zero is valid while a movement is being released at its
// baseline; only a move to the opposite side is contradictory.
func ValidateEventDirection(event *Event) error {
	if event == nil {
		return fmt.Errorf("%w: event is nil", ErrDirectionChangeInvariant)
	}
	values := []float64{
		event.BaselineSharePct,
		event.CurrentSharePct,
		event.ExtremeSharePct,
		event.ChangePP,
		event.ExtremeChangePP,
	}
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("%w: share values must be finite", ErrDirectionChangeInvariant)
		}
	}
	currentDelta := event.CurrentSharePct - event.BaselineSharePct
	extremeDelta := event.ExtremeSharePct - event.BaselineSharePct
	if !sameMovementDelta(event.ChangePP, currentDelta) || !sameMovementDelta(event.ExtremeChangePP, extremeDelta) {
		return fmt.Errorf("%w: stored changes do not match the exposed shares", ErrDirectionChangeInvariant)
	}
	switch event.Direction {
	case "UP":
		if currentDelta < 0 || extremeDelta < 0 {
			return fmt.Errorf("%w: UP event has a negative change", ErrDirectionChangeInvariant)
		}
	case "DOWN":
		if currentDelta > 0 || extremeDelta > 0 {
			return fmt.Errorf("%w: DOWN event has a positive change", ErrDirectionChangeInvariant)
		}
	default:
		return fmt.Errorf("%w: unsupported direction %q", ErrDirectionChangeInvariant, event.Direction)
	}
	return nil
}

func sameMovementDelta(left, right float64) bool {
	tolerance := math.Max(1e-12, math.Max(math.Abs(left), math.Abs(right))*1e-12)
	return math.Abs(left-right) <= tolerance
}
