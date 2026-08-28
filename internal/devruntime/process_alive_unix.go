//go:build !windows

package devruntime

import (
	"os"
	"syscall"
)

func processAlive(pid int) bool {
	if pid < 1 {
		return false
	}
	process, err := os.FindProcess(pid)
	return err == nil && process.Signal(syscall.Signal(0)) == nil
}
