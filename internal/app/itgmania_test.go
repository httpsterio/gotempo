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

// setupITG must not enable the overlay for a path that isn't there: writing into
// a dead path is the failure that looks like success.
func TestSetupITGRejectsMissingAndDir(t *testing.T) {
	t.Cleanup(func() { setupITG("") })
	dir := t.TempDir()

	setupITG(filepath.Join(dir, "nope.lua"))
	if itgEnabled() {
		t.Error("overlay enabled for a missing module")
	}

	setupITG(dir)
	if itgEnabled() {
		t.Error("overlay enabled for a directory")
	}

	setupITG("")
	if itgEnabled() {
		t.Error("overlay enabled for an empty path")
	}
}

func TestWriteAndClearITG(t *testing.T) {
	t.Cleanup(func() { setupITG("") })
	dir := t.TempDir()
	module := filepath.Join(dir, "gotempo.lua")
	if err := os.WriteFile(module, []byte("-- module"), 0644); err != nil {
		t.Fatal(err)
	}

	setupITG(module)
	if !itgEnabled() {
		t.Fatal("overlay not enabled for an existing module")
	}
	hr := filepath.Join(dir, "hr.txt")
	if itgTarget() != hr {
		t.Fatalf("itgTarget = %q, want %q", itgTarget(), hr)
	}

	writeITG(154, time.Date(2026, 9, 4, 14, 32, 7, 0, time.Local))
	data, err := os.ReadFile(hr)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "154 20260904 52327\n" {
		t.Errorf("hr.txt = %q", data)
	}

	// An unchanged value must still be written: the timestamp is the payload, so
	// a skipped write reads to the module as an unplugged sensor.
	writeITG(154, time.Date(2026, 9, 4, 14, 32, 8, 0, time.Local))
	data, err = os.ReadFile(hr)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "154 20260904 52328\n" {
		t.Errorf("second write did not advance the timestamp: %q", data)
	}

	// Empty reads as "no reading" and hides the panel within one poll.
	clearITG()
	data, err = os.ReadFile(hr)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Errorf("clearITG left %q", data)
	}
}

// The overlay being off must be inert, not a write to some default path.
func TestWriteITGDisabledIsNoop(t *testing.T) {
	t.Cleanup(func() { setupITG("") })
	dir := t.TempDir()
	setupITG("")

	writeITG(154, time.Now())
	clearITG()

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
	t.Cleanup(func() { setupITG("") })
	dir := t.TempDir()
	module := filepath.Join(dir, "gotempo.lua")
	if err := os.WriteFile(module, []byte("-- module"), 0644); err != nil {
		t.Fatal(err)
	}
	setupITG(module)
	writeITG(154, time.Date(2026, 9, 4, 14, 32, 7, 0, time.Local))

	// outputPath() lives under dataDir(); point it at the temp dir so the test
	// does not touch the real one.
	t.Setenv("XDG_DATA_HOME", dir)

	var s AppState
	s.clearOutput()

	data, err := os.ReadFile(filepath.Join(dir, "hr.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "154 20260904 52327\n" {
		t.Errorf("clearOutput cleared the ITGmania file: %q", data)
	}
}
