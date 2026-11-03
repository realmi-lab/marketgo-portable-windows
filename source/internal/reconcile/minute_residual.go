package reconcile

import "math"

// CanonicalMinuteResidual is shared by evidence admission and the atomic
// runtime read. Its tolerance covers arithmetic round-off only, not an
// exchange's source precision policy. A zero residual grants no coverage.
func CanonicalMinuteResidual(official, observed float64) (float64, bool) {
	if official < 0 || observed < 0 || math.IsNaN(official) || math.IsNaN(observed) || math.IsInf(official, 0) || math.IsInf(observed, 0) {
		return 0, false
	}
	residual := official - observed
	tolerance := math.Max(1e-7, math.Max(official, observed)*1e-12)
	if residual < -tolerance {
		return 0, false
	}
	if residual <= tolerance {
		return 0, true
	}
	return residual, true
}
