//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"syscall"
)

type instanceLock struct{ f *os.File }

// acquireLock takes an exclusive non-blocking flock on <stateDir>/launcher.lock.
// Failure means another dock is running (the caller opens its URL and exits).
func acquireLock(dir string) (*instanceLock, error) {
	f, err := os.OpenFile(filepath.Join(dir, "launcher.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, err
	}
	return &instanceLock{f}, nil
}

func (l *instanceLock) release() { l.f.Close() }
