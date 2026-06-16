package app

import (
	"os"
	"testing"
)

// Stop logging must clear gotempo-bpm.txt at once, not leave the last value
// frozen (it's an OBS overlay source; README says "empty when not logging").
func TestSetLoggingClearsOutput(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	if err := os.MkdirAll(sessionsDir(), 0755); err != nil {
		t.Fatal(err)
	}

	cfg := defaultConfig()
	cfg.AutoLog = true
	a := newApp(cfg)

	a.handleBPM(72) // logging on: writes the current bpm to gotempo-bpm.txt
	if b, _ := os.ReadFile(outputPath()); string(b) != "72" {
		t.Fatalf("bpm file = %q, want 72", b)
	}

	a.setLogging(false) // stop: overlay must go blank
	if b, _ := os.ReadFile(outputPath()); len(b) != 0 {
		t.Errorf("bpm file after stop = %q, want empty", b)
	}
}
