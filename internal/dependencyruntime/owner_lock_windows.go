//go:build windows

package dependencyruntime

import (
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

func withOwnerClaimLock(ownerPath string, fn func() error) error {
	// Keep a stable lock inode. Removing it after unlock could let a new opener
	// bypass a waiter that still holds the old file handle.
	lock, err := os.OpenFile(ownerPath+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("open Docker dependency ownership lock: %w", err)
	}
	defer lock.Close()

	handle := windows.Handle(lock.Fd())
	overlapped := new(windows.Overlapped)
	deadline := time.Now().Add(10 * time.Second)
	for {
		err = windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, overlapped)
		if err == nil {
			break
		}
		if err != windows.ERROR_LOCK_VIOLATION {
			return fmt.Errorf("lock Docker dependency ownership: %w", err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for Docker dependency ownership lock")
		}
		time.Sleep(25 * time.Millisecond)
	}
	defer windows.UnlockFileEx(handle, 0, 1, 0, overlapped) //nolint:errcheck -- closing the handle also releases it
	return fn()
}
