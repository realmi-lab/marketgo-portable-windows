//go:build !rustffi

package aggregate

func newRollingEngine(windows []int64) (rollingEngine, error) {
	return newPureRollingEngine(windows)
}
