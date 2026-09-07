package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestITGLineFormat(t *testing.T) {
	// 14:32:07 -> 14*3600 + 32*60 + 7 = 52327. The spec's worked example.
	now := time.Date(2026, 9, 4, 14, 32, 7, 0, time.Local)
	if got, want := itgLine(154, now), "154 20260904 52327\n"; got != want {
		t.Errorf("itgLine = %q, want %q", got, want)
	}
}

func TestITGLineZeroPadsDate(t *testing.T) {
	// A single-digit month and day must still be 8 digits: the module compares
	// the field against Year()*10000+Month()*100+Day() as an integer, so 202614
	// would never match 20260104.
	now := time.Date(2026, 1, 4, 0, 0, 5, 0, time.Local)
	if got, want := itgLine(60, now), "60 20260104 5\n"; got != want {
		t.Errorf("itgLine = %q, want %q", got, want)
	}
}

func TestITGHRPathFor(t *testing.T) {
	got := itgHRPathFor(filepath.Join("/home/u/.itgmania/Themes/Simply Love/Modules", "gotempo.lua"))
	want := filepath.Join("/home/u/.itgmania/Themes/Simply Love/Modules", "hr.txt")
	if got != want {
		t.Errorf("itgHRPathFor = %q, want %q", got, want)
	}
}

// writeModule creates a stand-in gotempo.lua and returns its path.
func writeModule(t *testing.T, dir string) string {
	t.Helper()
	module := filepath.Join(dir, "gotempo.lua")
	if err := os.WriteFile(module, []byte("-- module"), 0644); err != nil {
		t.Fatal(err)
	}
	return module
}

// setupITG must not enable the overlay for a path that isn't there: writing into
// a dead path is the failure that looks like success.
func TestSetupITGRejectsMissingAndDir(t *testing.T) {
	dir := t.TempDir()

	if setupITG(filepath.Join(dir, "nope.lua")).enabled() {
		t.Error("overlay enabled for a missing module")
	}
	if setupITG(dir).enabled() {
		t.Error("overlay enabled for a directory")
	}
	if setupITG("").enabled() {
		t.Error("overlay enabled for an empty path")
	}
}

func TestWriteAndClearITG(t *testing.T) {
	dir := t.TempDir()
	w := setupITG(writeModule(t, dir))
	if !w.enabled() {
		t.Fatal("overlay not enabled for an existing module")
	}
	hr := filepath.Join(dir, "hr.txt")
	if w.target() != hr {
		t.Fatalf("target = %q, want %q", w.target(), hr)
	}

	w.write(154, time.Date(2026, 9, 4, 14, 32, 7, 0, time.Local))
	data, err := os.ReadFile(hr)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "154 20260904 52327\n" {
		t.Errorf("hr.txt = %q", data)
	}

	// An unchanged value must still be written: the timestamp is the payload, so
	// a skipped write reads to the module as an unplugged sensor.
	w.write(154, time.Date(2026, 9, 4, 14, 32, 8, 0, time.Local))
	data, err = os.ReadFile(hr)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "154 20260904 52328\n" {
		t.Errorf("second write did not advance the timestamp: %q", data)
	}

	// Empty reads as "no reading" and hides the panel within one poll.
	w.clear()
	data, err = os.ReadFile(hr)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Errorf("clear left %q", data)
	}
}

// Two writers must be fully independent: this is the whole point of making them
// a struct, since a two-player cabinet publishes one hr.txt per side and one
// player disconnecting must not blank the other player's panel.
func TestITGWritersAreIndependent(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	a := setupITG(writeModule(t, dirA))
	b := setupITG(writeModule(t, dirB))

	now := time.Date(2026, 9, 4, 14, 32, 7, 0, time.Local)
	a.write(154, now)
	b.write(88, now)

	read := func(dir string) string {
		data, err := os.ReadFile(filepath.Join(dir, "hr.txt"))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	if got, want := read(dirA), "154 20260904 52327\n"; got != want {
		t.Errorf("writer A wrote %q, want %q", got, want)
	}
	if got, want := read(dirB), "88 20260904 52327\n"; got != want {
		t.Errorf("writer B wrote %q, want %q", got, want)
	}

	// Clearing one must leave the other's reading standing.
	a.clear()
	if got := read(dirA); len(got) != 0 {
		t.Errorf("clearing A left %q", got)
	}
	if got, want := read(dirB), "88 20260904 52327\n"; got != want {
		t.Errorf("clearing A also cleared B: %q, want %q", got, want)
	}
}

// The overlay being off must be inert, not a write to some default path. A nil
// writer is the same state, reached by an AppState that was never given one.
func TestWriteITGDisabledIsNoop(t *testing.T) {
	dir := t.TempDir()

	for name, w := range map[string]*itgWriter{"empty": setupITG(""), "nil": nil} {
		t.Run(name, func(t *testing.T) {
			if w.enabled() {
				t.Error("overlay reports enabled")
			}
			if w.target() != "" {
				t.Errorf("target = %q, want empty", w.target())
			}
			w.write(154, time.Now())
			w.clear()
		})
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("disabled overlay wrote %d files", len(entries))
	}
}

// clearOutput is the logging-toggle path; wiring the ITGmania file into it would
// make the in-game panel disappear when the user stops session logging.
func TestClearOutputLeavesITGFile(t *testing.T) {
	dir := t.TempDir()
	w := setupITG(writeModule(t, dir))
	w.write(154, time.Date(2026, 9, 4, 14, 32, 7, 0, time.Local))

	s := newAppState(filepath.Join(dir, "gotempo-bpm.txt"), false)
	s.attachITG(w)
	s.clearOutput()

	data, err := os.ReadFile(filepath.Join(dir, "hr.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "154 20260904 52327\n" {
		t.Errorf("clearOutput cleared the ITGmania file: %q", data)
	}
}

// A zero-value AppState has no OBS file and no overlay. It must be inert rather
// than writing gotempo-bpm.txt into the working directory.
func TestZeroAppStateWritesNothing(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	var s AppState
	s.putOut([]byte("72"), "write")
	s.clearOutput()
	s.onSwitch()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("zero AppState wrote %d files", len(entries))
	}
}

// Each AppState clears only its own OBS file, for the same reason the writers
// are independent: one strap dropping must not blank the other's overlay.
func TestAppStatesClearOwnOutput(t *testing.T) {
	dir := t.TempDir()
	pathA := filepath.Join(dir, "gotempo-bpm.txt")
	pathB := filepath.Join(dir, "gotempo-bpm-p2.txt")

	a := newAppState(pathA, true)
	b := newAppState(pathB, true)
	a.putOut([]byte("154"), "write")
	b.putOut([]byte("88"), "write")

	a.clearOutput()

	if data, _ := os.ReadFile(pathA); len(data) != 0 {
		t.Errorf("A not cleared: %q", data)
	}
	if data, _ := os.ReadFile(pathB); string(data) != "88" {
		t.Errorf("clearing A also cleared B: %q, want 88", data)
	}
}
