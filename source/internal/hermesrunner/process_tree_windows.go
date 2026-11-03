//go:build windows

package hermesrunner

import (
	"os"
	"os/exec"
)

func configureProcessTree(_ *exec.Cmd) {}

func terminateProcessGroup(_ *os.Process) bool { return false }
