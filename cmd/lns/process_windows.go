//go:build windows

package main

import (
	"os"
	"os/exec"
)

func detachProcess(command *exec.Cmd) {}

func handledSignals() []os.Signal {
	return []os.Signal{os.Interrupt}
}

func interruptedExit(err error) bool {
	return false
}
