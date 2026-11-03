//go:build rustffi && (!cgo || (!linux && !windows))

package aggregate

import "errors"

func newRollingEngine([]int64) (rollingEngine, error) {
	return nil, errors.New("rust-ffi aggregation requires Linux or Windows with cgo enabled")
}
