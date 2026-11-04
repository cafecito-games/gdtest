//go:build !windows

package client

import (
	"os/exec"
	"syscall"
)

func detachCommand(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
