//go:build windows

package client

import "os/exec"

func detachCommand(_ *exec.Cmd) {}
