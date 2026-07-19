//go:build windows

package main

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

type instanceLock struct{ f *os.File }

// acquireLock takes an exclusive non-blocking LockFileEx on
// <stateDir>/launcher.lock. Failure means another dock is running (the caller
// opens its URL and exits). The lock releases automatically if we crash.
func acquireLock(dir string) (*instanceLock, error) {
	f, err := os.OpenFile(filepath.Join(dir, "launcher.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	ol := new(windows.Overlapped)
	err = windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol)
	if err != nil {
		f.Close()
		return nil, err
	}
	return &instanceLock{f}, nil
}

func (l *instanceLock) release() { l.f.Close() }
