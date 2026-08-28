//go:build windows

package main

import (
	"os"
	"os/exec"
)

func configureChildProcess(command *exec.Cmd) {}

func interruptChildProcess(command *exec.Cmd) error {
	return command.Process.Signal(os.Interrupt)
}

func killChildProcess(command *exec.Cmd) error {
	return command.Process.Kill()
}

func handledSignals() []os.Signal {
	return []os.Signal{os.Interrupt}
}

func interruptedExit(err error) bool {
	return false
}
