//go:build darwin || linux

package hermesrunner

import (
	"os"
	"os/exec"
	"syscall"
)

func configureProcessTree(command *exec.Cmd) {
	if command == nil {
		return
	}
	if command.SysProcAttr == nil {
		command.SysProcAttr = &syscall.SysProcAttr{}
	}
	command.SysProcAttr.Setpgid = true
}

func terminateProcessGroup(process *os.Process) bool {
	return process != nil && syscall.Kill(-process.Pid, syscall.SIGKILL) == nil
}
