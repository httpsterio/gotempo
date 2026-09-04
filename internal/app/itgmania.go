package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ITGmania overlay.
//
// The gotempo.lua theme module draws a heart-rate panel on ITGmania's gameplay
// screen. The game's Lua sandbox has no networking, so the only channel between
// the two is a text file the module polls once a second: hr.txt, in the same
// folder as the module. gotempo writes one line to it per reading:
//
//	<bpm> <YYYYMMDD> <secondsSinceLocalMidnight>
//	154 20260904 52327
//
// The module hides the panel when the date differs from its own or the time is
// more than 60s behind it, which is how "no strap this session" is told apart
// from a live reading. Two consequences shape the code below.
//
// Local time, not UTC: the module compares against the game's own
// Hour()/Minute()/Second() (it has no os.time to convert an epoch), so a UTC
// stamp reads as a constant hours-old age and the panel never appears.
//
// Write every reading, deduped by nothing and gated by nothing: the timestamp is
// the payload, not the bpm. A write skipped because the value was unchanged
// looks exactly like an unplugged sensor, so a steady heart rate would blank the
// panel after a minute. This is why the write does not reuse the OBS file's
// dedup, and why it sits above the logging toggle (gotempo-bpm.txt only updates
// while logging is on; the game panel should not need a recording session).
//
// Values outside the module's 20-999 range hide the panel within one poll, so
// the 0 a strap reports when it isn't on skin is passed through unfiltered
// rather than suppressed: it is the fast hide.

const itgHRFile = "hr.txt"

var (
	itgMu sync.Mutex
	// itgPath is the resolved hr.txt for this run, empty when the feature is off
	// (no itgmania_module set) or the module was missing at startup. Package
	// level like the other output paths, so the clear paths in AppState reach it
	// without a back-reference to App.
	itgPath string
	// itgErrLogged suppresses repeat write errors. Writes run at ~1Hz, so a
	// directory that disappears mid-run would otherwise log 3600 lines an hour.
	itgErrLogged bool
)

// itgHRPathFor derives the target from the module's location: hr.txt beside
// gotempo.lua.
//
// The module resolves its own path from THEME:GetCurrentThemeDirectory(), the
// theme selected in-game, so this agrees with the game only when the module
// pointed at is the one inside the active theme. A gotempo.lua in some other
// theme's Modules/ folder is a real, existing file that the running game never
// reads, and nothing here can detect that.
func itgHRPathFor(module string) string {
	return filepath.Join(filepath.Dir(module), itgHRFile)
}

// setupITG validates the configured module path once, at startup, and enables
// the overlay for the run. An empty path means the feature is off. A path that
// isn't there disables it and says so: writing into a dead path would leave the
// app looking like it worked while nothing reached the game.
//
// It never creates the directory. os.WriteFile's O_CREATE makes the file only,
// so a wrong path keeps failing loudly instead of building a phantom tree.
func setupITG(module string) {
	itgMu.Lock()
	defer itgMu.Unlock()
	itgPath, itgErrLogged = "", false

	if module == "" {
		return
	}
	info, err := os.Stat(module)
	if err != nil {
		logErrf("[ITG] module not found, overlay disabled: %s", module)
		return
	}
	if info.IsDir() {
		logErrf("[ITG] itgmania_module is a directory, want the gotempo.lua file: %s", module)
		return
	}
	itgPath = itgHRPathFor(module)
	logInfof("[ITG] writing %s", itgPath)
}

// itgEnabled reports whether the overlay resolved to a usable path.
func itgEnabled() bool {
	itgMu.Lock()
	defer itgMu.Unlock()
	return itgPath != ""
}

// itgTarget returns the resolved hr.txt path, empty when the overlay is off.
func itgTarget() string {
	itgMu.Lock()
	defer itgMu.Unlock()
	return itgPath
}

// itgLine formats one reading. The time must be local: see the note above.
func itgLine(bpm int, now time.Time) string {
	return fmt.Sprintf("%d %04d%02d%02d %d\n",
		bpm,
		now.Year(), int(now.Month()), now.Day(),
		now.Hour()*3600+now.Minute()*60+now.Second(),
	)
}

// writeITG publishes one reading, for every reading received. os.WriteFile is
// the right call here: it truncates in place with a single Write, keeping the
// same inode and directory entry. The temp-file-and-rename idiom would be worse,
// since StepMania's RageFileManager caches directory listings and may not pick
// the swapped entry up promptly.
func writeITG(bpm int, now time.Time) {
	itgMu.Lock()
	defer itgMu.Unlock()
	if itgPath == "" {
		return
	}
	if err := os.WriteFile(itgPath, []byte(itgLine(bpm, now)), 0644); err != nil {
		if !itgErrLogged {
			logErrf("[ITG] could not write %s: %v", itgPath, err)
			itgErrLogged = true
		}
		return
	}
	itgErrLogged = false
}

// clearITG truncates hr.txt so the panel hides within one poll instead of
// waiting out the module's 60s staleness threshold. An empty file reads as no
// reading, same as a 0. Called on disconnect and device switch, deliberately not
// when logging is turned off.
func clearITG() {
	itgMu.Lock()
	defer itgMu.Unlock()
	if itgPath == "" {
		return
	}
	if err := os.WriteFile(itgPath, []byte{}, 0644); err != nil {
		if !itgErrLogged {
			logErrf("[ITG] could not clear %s: %v", itgPath, err)
			itgErrLogged = true
		}
		return
	}
	itgErrLogged = false
}
