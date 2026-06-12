//go:build unix

package main

import (
	"os"
	"path/filepath"
	"syscall"
)

// acquireInstanceLock takes an exclusive, non-blocking lock so only one
// gotempo runs at a time. The lock is held for the process lifetime and the
// kernel releases it automatically on exit (even a crash), so there are no
// stale locks. Returns ok=false if another instance already holds it. On ok,
// the caller must call the returned release func at shutdown (it closes the
// lock file, dropping the lock).
//
// flock is identical on Linux and macOS, so this file is shared via the unix
// build tag. Windows has no flock and uses a named mutex (lock_windows.go).
func acquireInstanceLock() (release func(), ok bool) {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = configDir()
	}
	lf, err := os.OpenFile(filepath.Join(dir, "gotempo.lock"), os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return func() {}, true // can't create a lock file — don't block startup
	}
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lf.Close()
		return nil, false
	}
	return func() { _ = lf.Close() }, true
}
