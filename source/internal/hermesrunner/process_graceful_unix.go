//go:build darwin || linux

package hermesrunner

import (
	"os"
	"syscall"
)

func signalProcessForGracefulShutdown(process *os.Process) bool {
	return process != nil && process.Signal(syscall.SIGTERM) == nil
}
