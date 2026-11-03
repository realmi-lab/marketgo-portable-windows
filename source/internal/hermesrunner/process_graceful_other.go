//go:build !darwin && !linux && !windows

package hermesrunner

import "os"

func signalProcessForGracefulShutdown(_ *os.Process) bool { return false }
