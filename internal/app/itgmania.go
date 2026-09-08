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

// itgHRBase is the module-facing filename. P1 writes exactly "hr.txt" so an
// already-installed module keeps working with no change; the second strap gets
// "hr-p2.txt". There is deliberately no hr-p1.txt, so the updated module reads
// the two names directly and needs no fallback logic in Lua.
const itgHRBase = "hr"

// itgWriter owns one hr.txt. There is one per connected strap rather than one
// per process: a two-player cabinet writes a separate file per side, and the
// AppState that blanks the panel on disconnect holds its own writer, so the
// clear paths reach the right file without consulting anything global.
//
// A nil *itgWriter and one with an empty path are both "overlay off", and every
// method below is a no-op in that state. So a caller that never configured an
// overlay (a run without itgmania_module, or a test building an AppState
// directly) needs no special case.
type itgWriter struct {
	mu sync.Mutex
	// path is the resolved hr.txt for this run, empty when the feature is off
	// (no itgmania_module set) or the module was missing at startup.
	path string
	// errLogged suppresses repeat write errors. Writes run at ~1Hz, so a
	// directory that disappears mid-run would otherwise log 3600 lines an hour.
	errLogged bool
}

// itgHRPathFor derives the target from the module's location: hr.txt beside
// gotempo.lua.
//
// The module resolves its own path from THEME:GetCurrentThemeDirectory(), the
// theme selected in-game, so this agrees with the game only when the module
// pointed at is the one inside the active theme. A gotempo.lua in some other
// theme's Modules/ folder is a real, existing file that the running game never
// reads, and nothing here can detect that.
func itgHRPathFor(module string, slot int) string {
	return filepath.Join(filepath.Dir(module), itgHRBase+slotSuffix(slot)+".txt")
}

// setupITG validates the configured module path once, at startup, and returns
// the writer for the run. An empty path means the feature is off. A path that
// isn't there disables it and says so: writing into a dead path would leave the
// app looking like it worked while nothing reached the game.
//
// It never creates the directory. os.WriteFile's O_CREATE makes the file only,
// so a wrong path keeps failing loudly instead of building a phantom tree.
//
// It always returns a usable writer; a disabled one simply has no path.
func setupITG(module string, slot int) *itgWriter {
	w := &itgWriter{}
	if module == "" {
		return w
	}
	info, err := os.Stat(module)
	if err != nil {
		logErrf("[ITG] module not found, overlay disabled: %s", module)
		return w
	}
	if info.IsDir() {
		logErrf("[ITG] itgmania_module is a directory, want the gotempo.lua file: %s", module)
		return w
	}
	w.path = itgHRPathFor(module, slot)
	return w
}

// sibling returns a writer for another slot's file beside the same module,
// carrying this one's enabled/disabled verdict rather than re-deriving it. The
// module path is checked once per run, so a wrong one is reported once instead
// of once per strap.
func (w *itgWriter) sibling(module string, slot int) *itgWriter {
	if !w.enabled() {
		return &itgWriter{}
	}
	return &itgWriter{path: itgHRPathFor(module, slot)}
}

// enabled reports whether the overlay resolved to a usable path.
func (w *itgWriter) enabled() bool { return w.target() != "" }

// target returns the resolved hr.txt path, empty when the overlay is off.
func (w *itgWriter) target() string {
	if w == nil {
		return ""
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.path
}

// itgLine formats one reading. The time must be local: see the note above.
func itgLine(bpm int, now time.Time) string {
	return fmt.Sprintf("%d %04d%02d%02d %d\n",
		bpm,
		now.Year(), int(now.Month()), now.Day(),
		now.Hour()*3600+now.Minute()*60+now.Second(),
	)
}

// write publishes one reading, for every reading received. os.WriteFile is the
// right call here: it truncates in place with a single Write, keeping the same
// inode and directory entry. The temp-file-and-rename idiom would be worse,
// since StepMania's RageFileManager caches directory listings and may not pick
// the swapped entry up promptly.
func (w *itgWriter) write(bpm int, now time.Time) {
	w.put([]byte(itgLine(bpm, now)), "write")
}

// clear truncates hr.txt so the panel hides within one poll instead of waiting
// out the module's 60s staleness threshold. An empty file reads as no reading,
// same as a 0. Called on disconnect and device switch, deliberately not when
// logging is turned off.
func (w *itgWriter) clear() {
	w.put([]byte{}, "clear")
}

// put is the one write path, shared so the disabled check and the
// error-suppression state cannot drift between writing and clearing.
func (w *itgWriter) put(data []byte, what string) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.path == "" {
		return
	}
	if err := os.WriteFile(w.path, data, 0644); err != nil {
		if !w.errLogged {
			logErrf("[ITG] could not %s %s: %v", what, w.path, err)
			w.errLogged = true
		}
		return
	}
	w.errLogged = false
}
