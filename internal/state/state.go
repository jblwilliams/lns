package state

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"lns/internal/config"
)

const (
	lockTimeout = 10 * time.Second
	staleLock   = 30 * time.Second
)

func WithGlobalLock(fn func() error) error {
	if err := config.EnsureConfigDirs(); err != nil {
		return err
	}

	lockPath := filepath.Join(config.GetConfigDir(), "state.lock")
	deadline := time.Now().Add(lockTimeout)
	for {
		if err := os.Mkdir(lockPath, 0755); err == nil {
			defer os.Remove(lockPath)
			return fn()
		} else if !os.IsExist(err) {
			return fmt.Errorf("acquire state lock: %w", err)
		}

		if info, err := os.Stat(lockPath); err == nil && time.Since(info.ModTime()) > staleLock {
			_ = os.Remove(lockPath)
			continue
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for lns state lock")
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func WriteFileAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".lns-write-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
