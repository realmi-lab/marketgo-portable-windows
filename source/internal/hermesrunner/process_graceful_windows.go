//go:build windows

package hermesrunner

import "os"

func signalProcessForGracefulShutdown(_ *os.Process) bool {
	// Windows uses taskkill /T /F in terminateProcessTree, which follows the
	// complete process tree rather than Unix process-group boundaries.
	return false
}
