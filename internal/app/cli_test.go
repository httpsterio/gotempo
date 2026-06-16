package app

import (
	"os"
	"testing"
	"time"
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
		{"epoch", []string{"--print-bpm", "--epoch"}, cliOptions{printBPM: true, epoch: true}},
		{"timestamp", []string{"--print-bpm", "--timestamp"}, cliOptions{printBPM: true, timestamp: true}},
		{"config", []string{"--config", "/tmp/c.json"}, cliOptions{config: "/tmp/c.json"}},
		{"autostart", []string{"--autostart"}, cliOptions{autostart: true}},
		{"no-autostart", []string{"--no-autostart"}, cliOptions{noAutostart: true}},
		{"device", []string{"--device", "24:AC:AC:18:41:CC"}, cliOptions{device: "24:AC:AC:18:41:CC"}},
		{"select-device", []string{"--select-device"}, cliOptions{selectDev: true}},
		{"auto-log", []string{"--auto-log"}, cliOptions{autoLog: true}},
		{"no-auto-log", []string{"--no-auto-log"}, cliOptions{noAutoLog: true}},
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

func TestFormatTS(t *testing.T) {
	tm := time.Date(2026, 6, 16, 1, 2, 3, 0, time.UTC)
	if got := formatTS(tm, tsClock); got != "01:02:03" {
		t.Errorf("tsClock = %q, want 01:02:03", got)
	}
	if got := formatTS(tm, tsEpoch); got != "1781571723" {
		t.Errorf("tsEpoch = %q, want 1781571723", got)
	}
	if got := formatTS(tm, tsFull); got != "2026-06-16T01:02:03Z" {
		t.Errorf("tsFull = %q, want RFC3339", got)
	}
}

func TestEffectiveAutoLog(t *testing.T) {
	cases := []struct {
		name       string
		opts       cliOptions
		cfgAutoLog bool
		want       bool
	}{
		{"tray, config off", cliOptions{}, false, false},
		{"tray, config on", cliOptions{}, true, true},
		{"headless defaults on", cliOptions{noTray: true}, false, true},
		{"print-bpm does not force", cliOptions{printBPM: true}, false, false},
		{"no-tray print-bpm does not force", cliOptions{noTray: true, printBPM: true}, false, false},
		{"no-auto-log wins over headless", cliOptions{noTray: true, noAutoLog: true}, false, false},
		{"auto-log forces on in tray", cliOptions{autoLog: true}, false, true},
		{"auto-log forces on with print-bpm", cliOptions{printBPM: true, autoLog: true}, false, true},
		{"no-auto-log wins over config on", cliOptions{noAutoLog: true}, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.opts.effectiveAutoLog(c.cfgAutoLog); got != c.want {
				t.Errorf("effectiveAutoLog(%+v, cfg=%v) = %v, want %v", c.opts, c.cfgAutoLog, got, c.want)
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

func TestStatusRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	if err := os.MkdirAll(dataDir(), 0755); err != nil {
		t.Fatal(err)
	}

	// Missing file.
	if _, ok := readStatus(); ok {
		t.Error("missing status file should not be ok")
	}

	// Connected with a reading, logging on, a device, phase connected.
	bpm := 72
	writeStatus(appStatus{
		Connected: true, Phase: phaseConnected, Logging: true, BPM: &bpm,
		Device: &statusDevice{MAC: "AA:BB", Name: "H10"},
	})
	st, ok := readStatus()
	if !ok || !st.Connected || st.BPM == nil || *st.BPM != 72 {
		t.Errorf("readStatus() = %+v, %v; want connected with bpm 72", st, ok)
	}
	if st.Phase != phaseConnected || !st.Logging {
		t.Errorf("phase/logging not round-tripped: %+v", st)
	}
	if st.Device == nil || st.Device.Name != "H10" {
		t.Errorf("device not round-tripped: %+v", st.Device)
	}
	if st.Updated == "" {
		t.Error("status should carry an updated timestamp")
	}

	// Disconnected: connected false, bpm nil, phase reconnecting.
	writeStatus(appStatus{Phase: phaseReconnecting})
	st, ok = readStatus()
	if !ok || st.Connected || st.BPM != nil || st.Phase != phaseReconnecting {
		t.Errorf("readStatus() = %+v, %v; want reconnecting with nil bpm", st, ok)
	}

	// Unparseable.
	if err := os.WriteFile(statusPath(), []byte("{not json"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, ok := readStatus(); ok {
		t.Error("unparseable status file should not be ok")
	}
}
