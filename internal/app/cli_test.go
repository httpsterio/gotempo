package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseFlags(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want cliOptions
	}{
		{"none", nil, cliOptions{}},
		{"version", []string{"--version"}, cliOptions{version: true}},
		{"v shorthand", []string{"-v"}, cliOptions{version: true}},
		{"status json", []string{"--status", "--json"}, cliOptions{status: true, json: true}},
		{"list", []string{"--list-devices"}, cliOptions{listDevices: true}},
		{"no-tray", []string{"--no-tray"}, cliOptions{noTray: true}},
		{"print-bpm", []string{"--print-bpm"}, cliOptions{printBPM: true}},
		{"config", []string{"--config", "/tmp/c.json"}, cliOptions{config: "/tmp/c.json"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseFlags(c.args)
			if err != nil {
				t.Fatalf("parseFlags(%v) error: %v", c.args, err)
			}
			if got != c.want {
				t.Errorf("parseFlags(%v) = %+v, want %+v", c.args, got, c.want)
			}
		})
	}
}

func TestParseFlagsBad(t *testing.T) {
	if _, err := parseFlags([]string{"--bogus"}); err == nil {
		t.Error("parseFlags(--bogus) = nil error, want a parse error")
	}
}

func TestHeadless(t *testing.T) {
	if (cliOptions{}).headless() {
		t.Error("no flags should not be headless")
	}
	if !(cliOptions{noTray: true}).headless() {
		t.Error("--no-tray should be headless")
	}
	if !(cliOptions{printBPM: true}).headless() {
		t.Error("--print-bpm should imply headless")
	}
}

func TestReadBPMFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	if err := os.MkdirAll(filepath.Dir(outputPath()), 0755); err != nil {
		t.Fatal(err)
	}

	write := func(s string) {
		if err := os.WriteFile(outputPath(), []byte(s), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Missing file.
	if _, ok := readBPMFile(); ok {
		t.Error("missing file should not be ok")
	}
	// Valid value (with surrounding whitespace).
	write(" 72\n")
	if bpm, ok := readBPMFile(); !ok || bpm != 72 {
		t.Errorf("readBPMFile() = %d, %v; want 72, true", bpm, ok)
	}
	// Empty (not logging / cleared).
	write("")
	if _, ok := readBPMFile(); ok {
		t.Error("empty file should not be ok")
	}
	// Garbage.
	write("abc")
	if _, ok := readBPMFile(); ok {
		t.Error("unparseable file should not be ok")
	}
}
