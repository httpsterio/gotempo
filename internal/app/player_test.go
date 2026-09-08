package app

import (
	"os"
	"path/filepath"
	"testing"
)

// twoPlayerApp builds an App in two-player mode with both straps assigned and
// their output files redirected into a temp dir, for tests that need to prove
// one strap's activity does not reach the other.
func twoPlayerApp(t *testing.T) (*App, *player, *player) {
	t.Helper()
	dir := t.TempDir()

	cfg := defaultConfig()
	cfg.AutoLog = true
	cfg.TwoPlayer = true
	cfg.Current = "AA:AA:AA:AA:AA:AA"
	cfg.CurrentP2 = "BB:BB:BB:BB:BB:BB"
	a := newApp(cfg)

	for i, p := range a.players {
		p.state = newAppState(filepath.Join(dir, []string{"p1.txt", "p2.txt"}[i]), true)
		p.session = newSessionLogger(filepath.Join(dir, "sessions"), p.sessionSuffix, cfg.sessionGap(), cfg.minBPM(), true)
	}
	return a, a.players[slotP1], a.players[slotP2]
}

// Each player owns its switch channel. A shared one is what would make changing
// P1's strap tear down P2's live connection, which is the specific bug this
// split exists to prevent.
func TestPlayersHaveOwnSwitchChannel(t *testing.T) {
	_, p1, p2 := twoPlayerApp(t)

	if p1.switchCh == p2.switchCh {
		t.Fatal("players share one switch channel")
	}

	p1.signalSwitch()

	select {
	case <-p2.switchCh:
		t.Error("signalling P1 woke P2's connection loop")
	default:
	}
	select {
	case <-p1.switchCh:
	default:
		t.Error("signalling P1 did not wake P1")
	}
}

// signalSwitch must never block: the channel is buffered to one and a second
// signal while one is pending is dropped, since the loop re-reads the device
// when it wakes either way.
func TestSignalSwitchDoesNotBlock(t *testing.T) {
	_, p1, _ := twoPlayerApp(t)

	for i := 0; i < 5; i++ {
		p1.signalSwitch()
	}
	if len(p1.switchCh) != 1 {
		t.Errorf("switchCh holds %d signals, want 1", len(p1.switchCh))
	}
}

// The logging toggle is process-wide: one tray item drives every strap, so
// turning it off has to reach all of their outputs, not just the first.
func TestSetLoggingFansOutToAllPlayers(t *testing.T) {
	a, p1, p2 := twoPlayerApp(t)

	p1.state.putOut([]byte("154"), "write")
	p2.state.putOut([]byte("88"), "write")

	a.setLogging(false)

	for i, p := range []*player{p1, p2} {
		if data, _ := os.ReadFile(p.state.outPath); len(data) != 0 {
			t.Errorf("player %d output not cleared: %q", i+1, data)
		}
		if _, logging := p.state.snapshot(); logging {
			t.Errorf("player %d still reports logging", i+1)
		}
	}
}

// p1() is the marker for code that still assumes a single strap. It must agree
// with the slice it indexes.
func TestP1IsTheFirstPlayer(t *testing.T) {
	a := newApp(defaultConfig())
	if len(a.players) == 0 {
		t.Fatal("newApp built no players")
	}
	if a.p1() != a.players[0] {
		t.Error("p1() is not players[0]")
	}
	if a.p1().app != a {
		t.Error("player does not point back at its App")
	}
}

// The override is the ITGmania module's channel into device selection. It wins
// over config, and clearing it returns to whatever the operator configured,
// which is what makes a cabinet fall back cleanly when the game exits.
func TestEffectiveMACPrefersOverride(t *testing.T) {
	_, p1, _ := twoPlayerApp(t)

	if got := p1.effectiveMAC(); got != "AA:AA:AA:AA:AA:AA" {
		t.Errorf("with no override, effectiveMAC = %q, want the config value", got)
	}

	p1.setOverride("CC:CC:CC:CC:CC:CC")
	if got := p1.effectiveMAC(); got != "CC:CC:CC:CC:CC:CC" {
		t.Errorf("with an override, effectiveMAC = %q, want the override", got)
	}
	if got := p1.currentMAC(); got != "AA:AA:AA:AA:AA:AA" {
		t.Errorf("override leaked into config: currentMAC = %q", got)
	}

	p1.setOverride("")
	if got := p1.effectiveMAC(); got != "AA:AA:AA:AA:AA:AA" {
		t.Errorf("after clearing, effectiveMAC = %q, want the config value again", got)
	}
}

// Setting an override must wake the connection loop, or the strap would not
// change until something else happened to signal it.
func TestSetOverrideSignalsTheLoop(t *testing.T) {
	_, p1, p2 := twoPlayerApp(t)

	p1.setOverride("CC:CC:CC:CC:CC:CC")

	select {
	case <-p1.switchCh:
	default:
		t.Error("setOverride did not wake P1's loop")
	}
	select {
	case <-p2.switchCh:
		t.Error("setting P1's override woke P2")
	default:
	}

	// Setting the same value again is not a change and must not churn the
	// connection.
	p1.setOverride("CC:CC:CC:CC:CC:CC")
	select {
	case <-p1.switchCh:
		t.Error("re-setting the same override restarted the connection")
	default:
	}
}

// P2 reads as unconfigured while two-player mode is off. That is what makes its
// loop idle instead of needing to be started and stopped.
func TestP2IdlesWhenTwoPlayerIsOff(t *testing.T) {
	a, _, p2 := twoPlayerApp(t)

	if got := p2.effectiveMAC(); got != "BB:BB:BB:BB:BB:BB" {
		t.Fatalf("with the mode on, P2 = %q", got)
	}

	a.cfgMu.Lock()
	a.cfg.TwoPlayer = false
	a.cfgMu.Unlock()

	if got := p2.effectiveMAC(); got != "" {
		t.Errorf("with the mode off, P2 = %q, want empty", got)
	}
	// The assignment is kept, so switching back on restores it.
	if got := a.snapshotConfig().CurrentP2; got != "BB:BB:BB:BB:BB:BB" {
		t.Errorf("turning the mode off lost the P2 assignment: %q", got)
	}
}

// Session files are named for whoever reads the folder: unsuffixed with one
// strap, -p1/-p2 with two. Deliberately not the same rule as slotSuffix.
func TestSessionSuffixFollowsTheMode(t *testing.T) {
	a, p1, p2 := twoPlayerApp(t)

	if got, want := p1.sessionSuffix(), "-p1"; got != want {
		t.Errorf("P1 suffix = %q, want %q", got, want)
	}
	if got, want := p2.sessionSuffix(), "-p2"; got != want {
		t.Errorf("P2 suffix = %q, want %q", got, want)
	}

	a.cfgMu.Lock()
	a.cfg.TwoPlayer = false
	a.cfgMu.Unlock()

	if got := p1.sessionSuffix(); got != "" {
		t.Errorf("single-strap suffix = %q, want empty", got)
	}
}

// Headless runs must not assume the populated slot is the first one.
func TestAnyDeviceConfigured(t *testing.T) {
	a, _, _ := twoPlayerApp(t)

	a.cfgMu.Lock()
	a.cfg.Current = ""
	a.cfgMu.Unlock()
	if !a.anyDeviceConfigured() {
		t.Error("P2 alone should count as configured")
	}

	a.cfgMu.Lock()
	a.cfg.CurrentP2 = ""
	a.cfgMu.Unlock()
	if a.anyDeviceConfigured() {
		t.Error("no slot assigned, but reported as configured")
	}
}
