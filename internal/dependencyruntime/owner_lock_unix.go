//go:build !windows

package dependencyruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

func withOwnerClaimLock(ownerPath string, fn func() error) error {
	directory, err := os.Open(filepath.Dir(ownerPath))
	if err != nil {
		return fmt.Errorf("open Docker dependency state directory: %w", err)
	}
	defer directory.Close()

	deadline := time.Now().Add(10 * time.Second)
	for {
		err = syscall.Flock(int(directory.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			return fmt.Errorf("lock Docker dependency ownership: %w", err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for Docker dependency ownership lock")
		}
		time.Sleep(25 * time.Millisecond)
	}
	defer syscall.Flock(int(directory.Fd()), syscall.LOCK_UN) //nolint:errcheck -- closing the descriptor also releases it
	return fn()
}
