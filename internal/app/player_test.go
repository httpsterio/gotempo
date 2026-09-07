package app

import (
	"os"
	"path/filepath"
	"testing"
)

// twoPlayerApp builds an App with two players whose OBS files are distinct, for
// tests that need to prove one strap's activity does not reach the other. It is
// hand-built rather than produced by newApp because wiring the second strap is
// still to come; what is being checked here is that the plumbing already keeps
// them apart.
func twoPlayerApp(t *testing.T) (*App, *player, *player) {
	t.Helper()
	dir := t.TempDir()

	cfg := defaultConfig()
	cfg.AutoLog = true
	a := newApp(cfg)
	a.players = append(a.players, newPlayer(a, cfg))

	for i, p := range a.players {
		p.state = newAppState(filepath.Join(dir, []string{"p1.txt", "p2.txt"}[i]), true)
		p.session = newSessionLogger(filepath.Join(dir, "sessions"), cfg.sessionGap(), cfg.minBPM(), true)
	}
	return a, a.players[0], a.players[1]
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
