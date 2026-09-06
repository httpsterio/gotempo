//go:build windows

package app

import (
	"errors"

	"golang.org/x/sys/windows"
)

// instanceMutex is the single-instance token. It is a fixed string on purpose:
// the tray app and gotempo-cli.exe are separate binaries that find each other
// through this name, so deriving it from the executable path or name would make
// `gotempo-cli --status` report "not running" while the tray app is up.
//
// The Local\ prefix scopes it to the logon session, matching the per-user lock
// on Linux. A second user logged into the same machine gets their own.
const instanceMutex = `Local\gotempo-instance`

// acquireInstanceLock takes a named mutex so only one gotempo runs at a time.
// Windows has no flock; lock_unix.go is tagged `unix` and excluded here.
//
// The handle is held for the process lifetime and Windows releases it when the
// process exits, including on a crash, so there are no stale locks. Returns
// ok=false if another instance already holds it. On ok, the caller must call the
// returned release func at shutdown.
func acquireInstanceLock() (release func(), ok bool) {
	name, err := windows.UTF16PtrFromString(instanceMutex)
	if err != nil {
		return func() {}, true // can't build the name, don't block startup
	}

	// CreateMutex returns a valid handle even when the mutex already exists, and
	// reports that through the error. Both paths must close their handle.
	h, err := windows.CreateMutex(nil, false, name)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		if h != 0 {
			windows.CloseHandle(h)
		}
		return nil, false
	}
	if err != nil {
		return func() {}, true // can't create the mutex, don't block startup
	}
	return func() { windows.CloseHandle(h) }, true
}
