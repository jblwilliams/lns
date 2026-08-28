//go:build !windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

func configureChildProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func interruptChildProcess(command *exec.Cmd) error {
	return syscall.Kill(-command.Process.Pid, syscall.SIGINT)
}

func killChildProcess(command *exec.Cmd) error {
	return syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
}

func handledSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}

func interruptedExit(err error) bool {
	exitError, ok := err.(*exec.ExitError)
	if !ok {
		return false
	}
	status, ok := exitError.Sys().(syscall.WaitStatus)
	return ok && status.Signaled() && (status.Signal() == syscall.SIGINT || status.Signal() == syscall.SIGTERM)
}
