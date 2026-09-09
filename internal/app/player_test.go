package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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

// The assignment is the game's channel into device selection. While it is
// driving it wins over config, and releasing it returns to whatever the operator
// configured, which is what makes a cabinet fall back cleanly when the game
// exits.
func TestEffectiveMACPrefersAssignment(t *testing.T) {
	_, p1, _ := twoPlayerApp(t)

	if got := p1.effectiveMAC(); got != "AA:AA:AA:AA:AA:AA" {
		t.Errorf("undriven, effectiveMAC = %q, want the config value", got)
	}

	p1.setAssignment(true, "CC:CC:CC:CC:CC:CC")
	if got := p1.effectiveMAC(); got != "CC:CC:CC:CC:CC:CC" {
		t.Errorf("driven, effectiveMAC = %q, want the assignment", got)
	}
	if got := p1.currentMAC(); got != "AA:AA:AA:AA:AA:AA" {
		t.Errorf("assignment leaked into config: currentMAC = %q", got)
	}

	// Driven with nothing means deliberately idle, NOT "fall back to config".
	// This is the distinction the driven flag exists for: the side nobody is
	// standing on must not quietly connect the configured strap as well.
	p1.setAssignment(true, "")
	if got := p1.effectiveMAC(); got != "" {
		t.Errorf("driven with no strap, effectiveMAC = %q, want idle", got)
	}

	// Releasing is what hands the slot back.
	p1.setAssignment(false, "")
	if got := p1.effectiveMAC(); got != "AA:AA:AA:AA:AA:AA" {
		t.Errorf("after release, effectiveMAC = %q, want the config value again", got)
	}
}

// Setting an override must wake the connection loop, or the strap would not
// change until something else happened to signal it.
func TestSetAssignmentSignalsTheLoop(t *testing.T) {
	_, p1, p2 := twoPlayerApp(t)

	p1.setAssignment(true, "CC:CC:CC:CC:CC:CC")

	select {
	case <-p1.switchCh:
	default:
		t.Error("setAssignment did not wake P1's loop")
	}
	select {
	case <-p2.switchCh:
		t.Error("assigning P1 woke P2")
	default:
	}

	// Setting the same value again is not a change and must not churn the
	// connection.
	p1.setAssignment(true, "CC:CC:CC:CC:CC:CC")
	select {
	case <-p1.switchCh:
		t.Error("re-setting the same assignment restarted the connection")
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

const unassignedSlot = -1

func TestAssignedSlot(t *testing.T) {
	cfg := Config{Current: "AA", CurrentP2: "BB"}
	for _, c := range []struct {
		mac  string
		want int
	}{
		{"AA", slotP1},
		{"aa", slotP1}, // MACs are compared case-insensitively everywhere else too
		{"BB", slotP2},
		{"CC", unassignedSlot},
		{"", unassignedSlot}, // an empty slot must not match an unassigned strap
	} {
		if got := assignedSlot(cfg, c.mac); got != c.want {
			t.Errorf("assignedSlot(%q) = %d, want %d", c.mac, got, c.want)
		}
	}
}

// One click walks a strap through every state and back to the start, which is
// what makes the tray's single-click assignment learnable.
func TestCycleAssignmentIsAFullLoop(t *testing.T) {
	cfg := Config{}
	for i, want := range [][2]string{
		{"AA", ""}, // unassigned -> P1
		{"", "AA"}, // P1 -> P2
		{"", ""},   // P2 -> unassigned
		{"AA", ""}, // and round again
	} {
		got := cycleAssignment(cfg, "AA")
		if got != want {
			t.Fatalf("click %d: got %v, want %v", i+1, got, want)
		}
		cfg.Current, cfg.CurrentP2 = got[slotP1], got[slotP2]
	}
}

// A slot holds one strap and a strap holds one slot. Taking an occupied slot
// displaces whoever was there, so the list can never show two straps claiming
// P1, or the same strap on both sides.
func TestCycleAssignmentDisplaces(t *testing.T) {
	// BB holds P1; AA cycles into it and must evict BB entirely.
	got := cycleAssignment(Config{Current: "BB"}, "AA")
	if want := [2]string{"AA", ""}; got != want {
		t.Errorf("into an occupied P1: got %v, want %v", got, want)
	}

	// AA on P1 moving to P2 must evict BB from P2 and leave P1 empty, not
	// leave AA on both.
	got = cycleAssignment(Config{Current: "AA", CurrentP2: "BB"}, "AA")
	if want := [2]string{"", "AA"}; got != want {
		t.Errorf("P1 to an occupied P2: got %v, want %v", got, want)
	}
}

// The end-to-end tray path: cycling persists, and wakes only the loops whose
// strap actually changed.
func TestAssignSlotsWakesOnlyChangedLoops(t *testing.T) {
	a, p1, p2 := twoPlayerApp(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	drain := func() {
		select {
		case <-p1.switchCh:
		default:
		}
		select {
		case <-p2.switchCh:
		default:
		}
	}
	drain()

	// Reassign P2 only; P1's live connection must not be disturbed.
	a.assignSlots([2]string{"AA:AA:AA:AA:AA:AA", "CC:CC:CC:CC:CC:CC"}, KnownDevice{MAC: "CC:CC:CC:CC:CC:CC"})

	select {
	case <-p1.switchCh:
		t.Error("reassigning P2 interrupted P1's connection")
	default:
	}
	select {
	case <-p2.switchCh:
	default:
		t.Error("P2 was reassigned but its loop was not woken")
	}
	if got := p2.effectiveMAC(); got != "CC:CC:CC:CC:CC:CC" {
		t.Errorf("P2 = %q, want the new strap", got)
	}
}

// Turning the mode off must not disturb the first strap: someone recording a
// session on P1 should not lose it because they toggled two-player.
func TestSetTwoPlayerLeavesP1Alone(t *testing.T) {
	a, p1, p2 := twoPlayerApp(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	select {
	case <-p1.switchCh:
	default:
	}

	a.setTwoPlayer(false)

	select {
	case <-p1.switchCh:
		t.Error("toggling two-player mode interrupted P1")
	default:
	}
	select {
	case <-p2.switchCh:
	default:
		t.Error("toggling the mode did not wake P2 to drop its strap")
	}
	if got := p2.effectiveMAC(); got != "" {
		t.Errorf("P2 still following %q after the mode went off", got)
	}
}

// The tray has one icon for the whole app, so one live strap shows connected.
func TestAnyConnected(t *testing.T) {
	a, p1, p2 := twoPlayerApp(t)

	if a.anyConnected() {
		t.Error("nothing connected, but anyConnected() is true")
	}
	p2.state.setConnected(true)
	if !a.anyConnected() {
		t.Error("P2 connected, but anyConnected() is false")
	}
	p2.state.setConnected(false)
	p1.state.setConnected(true)
	if !a.anyConnected() {
		t.Error("P1 connected, but anyConnected() is false")
	}
}

// The output gate. This is the guard against drawing one person's heart rate as
// another's, which is silent and looks like it is working, so the table is
// spelled out in full.
func TestPublishesGate(t *testing.T) {
	for _, c := range []struct {
		name      string
		claimed   string
		connected string
		want      bool
	}{
		{"no claim, house belt connected", "", "YY", true},
		{"no claim, nothing connected", "", "", true},
		{"claim met", "XX", "XX", true},
		{"claim met, different case", "xx", "XX", true},

		// The one that matters: the player's own belt is unreachable and gotempo
		// is sitting on the cabinet's configured belt, which someone else is
		// wearing. Publishing here would draw a stranger's heart rate as theirs.
		{"claimed X, connected to the house belt", "XX", "YY", false},
		{"claimed X, connected to nothing", "XX", "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, p1, _ := twoPlayerApp(t)
			p1.claimed = c.claimed
			p1.setConnMAC(c.connected)
			if got := p1.publishes(); got != c.want {
				t.Errorf("publishes() = %v, want %v", got, c.want)
			}
		})
	}
}

// A shut gate must stop the overlay and the CSV, while leaving the diagnostic
// paths alone: --status should still report what is really connected.
func TestShutGateStopsPublishingButNotStatus(t *testing.T) {
	dir := t.TempDir()
	_, p1, _ := twoPlayerApp(t)
	t.Setenv("XDG_DATA_HOME", dir)

	hr := filepath.Join(dir, "hr.txt")
	p1.state.attachITG(&itgWriter{path: hr})
	p1.session = newSessionLogger(filepath.Join(dir, "sessions"), nil, time.Hour, 20, true)
	p1.state.setLogging(true)

	// Someone else's belt is connected while this player claims their own.
	p1.claimed = "XX:XX:XX:XX:XX:XX"
	p1.setConnMAC("YY:YY:YY:YY:YY:YY")

	p1.handleBPM(154)

	if _, err := os.Stat(hr); !os.IsNotExist(err) {
		data, _ := os.ReadFile(hr)
		t.Errorf("shut gate wrote the overlay: %q", data)
	}
	if _, err := os.Stat(filepath.Join(dir, "sessions")); !os.IsNotExist(err) {
		t.Error("shut gate opened a CSV session")
	}
	if b, _ := os.ReadFile(p1.state.outPath); len(b) != 0 {
		t.Errorf("shut gate wrote the OBS file: %q", b)
	}

	// status.json is diagnostic, not attributed, so it still carries the reading.
	if _, _, _, bpm := p1.state.statusView(); bpm == nil || *bpm != 154 {
		t.Errorf("status lost the reading: %v", bpm)
	}
	// Opening the gate lets the same reading through.
	p1.setConnMAC("XX:XX:XX:XX:XX:XX")
	p1.handleBPM(154)
	if data, err := os.ReadFile(hr); err != nil || len(data) == 0 {
		t.Errorf("open gate did not write the overlay: %q, %v", data, err)
	}
}

// --print-bpm is exempt: it names its strap with --device/--player, so it is
// never ambiguous about whose reading it is, and gating it would make a
// debugging tool go silent exactly when something is wrong.
func TestPrintBPMIsNotGated(t *testing.T) {
	_, p1, _ := twoPlayerApp(t)
	p1.claimed = "XX:XX:XX:XX:XX:XX"
	p1.setConnMAC("YY:YY:YY:YY:YY:YY")

	got := 0
	p1.onReading = func(_ time.Time, bpm int) { got = bpm }
	p1.handleBPM(154)

	if got != 154 {
		t.Errorf("--print-bpm callback got %d, want 154 even with the gate shut", got)
	}
}

// Changing who is on this side must blank the panel at once, rather than
// leaving the previous player's reading up for the module's 60s staleness window.
func TestSetClaimClearsTheOverlay(t *testing.T) {
	dir := t.TempDir()
	_, p1, _ := twoPlayerApp(t)

	hr := filepath.Join(dir, "hr.txt")
	if err := os.WriteFile(hr, []byte("154 20260904 52327\n"), 0644); err != nil {
		t.Fatal(err)
	}
	p1.state.attachITG(&itgWriter{path: hr})

	p1.setClaim("XX:XX:XX:XX:XX:XX")

	if data, _ := os.ReadFile(hr); len(data) != 0 {
		t.Errorf("a new claim left the previous player's reading up: %q", data)
	}
}

// Re-setting the same claim is not a handover and must not disturb anything.
func TestSetClaimIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	_, p1, _ := twoPlayerApp(t)

	hr := filepath.Join(dir, "hr.txt")
	p1.state.attachITG(&itgWriter{path: hr})
	p1.setClaim("XX:XX:XX:XX:XX:XX")

	if err := os.WriteFile(hr, []byte("154 20260904 52327\n"), 0644); err != nil {
		t.Fatal(err)
	}
	p1.setClaim("xx:xx:xx:xx:xx:xx") // same strap, different case

	if data, _ := os.ReadFile(hr); len(data) == 0 {
		t.Error("re-claiming the same strap blanked a live panel")
	}
}
