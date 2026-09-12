package app

import (
	"os"
	"strings"
	"testing"
	"time"
)

// poolApp is an App with the pool wired but no goroutines: a.started stays
// false, so straps are built and subscribed exactly as in a live run while
// nothing ever opens an adapter.
func poolApp(t *testing.T) *App {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if err := os.MkdirAll(dataDir(), 0755); err != nil {
		t.Fatal(err)
	}

	cfg := defaultConfig()
	cfg.TwoPlayer = true
	cfg.Current = "AA:AA:AA:AA:AA:AA"
	cfg.CurrentP2 = "BB:BB:BB:BB:BB:BB"
	a := newApp(cfg)
	a.followAll()
	return a
}

// mkStrap puts a strap in the pool directly, for the retention tests, which
// care about the budgets rather than about who asked for the belt.
func mkStrap(t *testing.T, a *App, mac string) *strap {
	t.Helper()
	a.strapMu.Lock()
	defer a.strapMu.Unlock()
	s := a.strapForLocked(mac)
	if s == nil {
		t.Fatalf("could not build a strap for %q", mac)
	}
	return s
}

// The two budgets, and the one thing neither may do. A strap somebody is
// listening to is not a candidate however long it has been quiet: dropping it
// would blank a panel a player is looking at.
func TestStrapExpiryBudgets(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.Local)

	cases := []struct {
		name       string
		subscribed bool
		idleSince  time.Time
		lastLive   time.Time
		want       bool
	}{
		{"subscribed and silent for an hour", true, time.Time{}, now.Add(-time.Hour), false},
		{"worn, idle 19 minutes", false, now.Add(-19 * time.Minute), now.Add(-time.Second), false},
		{"worn, idle 21 minutes", false, now.Add(-21 * time.Minute), now.Add(-time.Second), true},
		{"just released, still delivering", false, now.Add(-time.Second), now.Add(-time.Second), false},
		{"released 2 minutes ago, silent since", false, now.Add(-2 * time.Minute), now.Add(-2 * time.Minute), false},
		{"released 6 minutes ago, silent since", false, now.Add(-6 * time.Minute), now.Add(-6 * time.Minute), true},
		// The belt came off after the player had already stopped: the short
		// budget runs from the last reading, not from the release, so a belt
		// put down at minute 14 does not survive to minute 20.
		{"idle 16 minutes, silent for the last 6", false, now.Add(-16 * time.Minute), now.Add(-6 * time.Minute), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &strap{idleSince: tc.idleSince, lastLive: tc.lastLive}
			if tc.subscribed {
				s.subs = []*player{{}}
			}
			if got := s.expired(now, defaultStrapHoldMinutes*time.Minute, defaultStrapLostMinutes*time.Minute); got != tc.want {
				t.Errorf("expired = %v, want %v", got, tc.want)
			}
		})
	}
}

// The sweeper is what turns the budgets into an actual release.
func TestSweepRetiresExpiredStraps(t *testing.T) {
	a := poolApp(t)
	now := time.Now()

	keep := mkStrap(t, a, "CC:CC:CC:CC:CC:CC")
	drop := mkStrap(t, a, "DD:DD:DD:DD:DD:DD")
	keep.idleSince, keep.lastLive = now.Add(-time.Minute), now
	drop.idleSince, drop.lastLive = now.Add(-time.Hour), now.Add(-time.Hour)

	a.sweepStraps(now)

	if retired(keep) {
		t.Error("a strap inside its budget was released")
	}
	if !retired(drop) {
		t.Error("an expired strap was not released")
	}
	a.strapMu.Lock()
	_, still := a.straps[strapKey(drop.mac)]
	a.strapMu.Unlock()
	if still {
		t.Error("the expired strap is still in the pool")
	}
}

// At the cap, the strap released longest ago goes first, and a strap a slot is
// listening to is never a candidate.
func TestPoolCapEvictsLeastRecentlyReleased(t *testing.T) {
	a := poolApp(t)
	p1, p2 := a.players[slotP1], a.players[slotP2]

	// Two subscribed (P1 and P2's configured straps) plus two idle fills it.
	oldest := mkStrap(t, a, "CC:CC:CC:CC:CC:CC")
	newer := mkStrap(t, a, "DD:DD:DD:DD:DD:DD")
	oldest.idleSince = time.Now().Add(-10 * time.Minute)
	newer.idleSince = time.Now().Add(-time.Minute)

	a.strapMu.Lock()
	if len(a.straps) != strapPoolCap {
		a.strapMu.Unlock()
		t.Fatalf("pool holds %d straps, want %d before the test", len(a.straps), strapPoolCap)
	}
	a.strapMu.Unlock()

	arriving := mkStrap(t, a, "EE:EE:EE:EE:EE:EE")

	if !retired(oldest) {
		t.Error("the least recently released strap was not evicted")
	}
	if retired(newer) {
		t.Error("the wrong strap was evicted")
	}
	for _, p := range []*player{p1, p2} {
		if retired(p.strap) {
			t.Errorf("P%d's live strap was evicted", p.slot+1)
		}
	}
	if retired(arriving) {
		t.Error("the arriving strap was evicted")
	}
}

// A belt is one belt however its MAC is spelled. Config and a game profile
// disagreeing on case must not open two connections to one device.
func TestPoolKeysAreCaseInsensitive(t *testing.T) {
	a := poolApp(t)

	upper := mkStrap(t, a, "CC:CC:CC:CC:CC:CC")
	lower := mkStrap(t, a, "cc:cc:cc:cc:cc:cc")

	if upper != lower {
		t.Error("the same belt was pooled twice")
	}
}

// Swapping the two players' sides is the case the pool exists for after the
// menu round trip: both belts stay connected and only the subscriptions move.
func TestSideSwapMovesNoConnections(t *testing.T) {
	a := poolApp(t)
	p1, p2 := a.players[slotP1], a.players[slotP2]

	p1.setAssignment(true, "AA:AA:AA:AA:AA:AA")
	p2.setAssignment(true, "BB:BB:BB:BB:BB:BB")
	first, second := p1.strap, p2.strap

	// The players trade sides.
	p1.setAssignment(true, "BB:BB:BB:BB:BB:BB")
	p2.setAssignment(true, "AA:AA:AA:AA:AA:AA")

	if p1.strap != second || p2.strap != first {
		t.Error("the slots did not trade straps")
	}
	if retired(first) || retired(second) {
		t.Error("a side swap tore down a connection")
	}
}

// Subscribing to a strap that is already up must adopt the live session. The
// slot would otherwise sit reporting "connecting" for a belt that is connected
// and delivering, and its panel would never appear.
func TestSubscribingAdoptsALiveConnection(t *testing.T) {
	a := poolApp(t)
	p1, p2 := a.players[slotP1], a.players[slotP2]

	p1.strap.wentUp()
	if !p1.isConnected() {
		t.Fatal("the subscribed slot did not see its strap connect")
	}

	// P2 joins the belt P1 is already on.
	p2.setAssignment(true, "AA:AA:AA:AA:AA:AA")

	if p2.strap != p1.strap {
		t.Fatal("P2 did not land on P1's strap")
	}
	if !p2.isConnected() {
		t.Error("P2 subscribed to a live strap but reports disconnected")
	}
	if _, _, phase, _ := p2.state.statusView(); phase != phaseConnected {
		t.Errorf("P2 phase = %q, want %q", phase, phaseConnected)
	}
}

// Readings reach every listening slot and stop when one leaves. An unsubscribed
// strap delivering to nobody is the normal warm-pool state, not an error.
func TestDeliverFollowsSubscriptions(t *testing.T) {
	a := poolApp(t)
	p1 := a.players[slotP1]
	s := p1.strap

	s.deliver(154)
	if _, _, _, bpm := p1.state.statusView(); bpm == nil || *bpm != 154 {
		t.Errorf("subscribed slot bpm = %v, want 154", bpm)
	}

	p1.setAssignment(true, "") // this side is deliberately idle
	if p1.strap != nil {
		t.Fatal("the slot is still subscribed")
	}
	s.deliver(88)
	if _, _, _, bpm := p1.state.statusView(); bpm != nil && *bpm == 88 {
		t.Error("a reading reached a slot that had stopped listening")
	}
}

// A reading only counts as proof of life when it is a real one. A belt that
// holds the link open and streams zeros is the hardware variant the short
// budget has to catch, since its link state never changes.
func TestZeroReadingsDoNotCountAsLive(t *testing.T) {
	a := poolApp(t)
	s := mkStrap(t, a, "CC:CC:CC:CC:CC:CC")

	s.lastLive = time.Now().Add(-time.Hour)
	s.deliver(0)
	if time.Since(s.lastLive) < time.Minute {
		t.Error("a zero reading was treated as proof the belt is worn")
	}

	s.deliver(61)
	if time.Since(s.lastLive) > time.Minute {
		t.Error("a real reading did not refresh the strap")
	}
}

// Loss and reconnect toasts belong to the cabinet's own equipment. A visiting
// player's strap arrives from their game profile, and reporting on it fills the
// desktop with news about people who have gone home.
func TestNotifiesOnlyForConfiguredStraps(t *testing.T) {
	a := poolApp(t)
	p1 := a.players[slotP1]

	if !p1.strap.notifies() {
		t.Error("a configured strap does not notify")
	}

	p1.setAssignment(true, "CC:CC:CC:CC:CC:CC")
	if p1.strap.notifies() {
		t.Error("a profile-driven strap notifies")
	}

	// An unsubscribed strap being kept warm belongs to nobody.
	driven := p1.strap
	p1.setAssignment(false, "")
	if driven.notifies() {
		t.Error("a strap with no listeners notifies")
	}
}

// An invalid MAC must not enter the pool. It reaches here from config or from a
// game profile, neither of which gotempo controls.
func TestPoolRejectsAnInvalidMAC(t *testing.T) {
	a := poolApp(t)

	a.strapMu.Lock()
	s := a.strapForLocked("not-a-mac")
	n := len(a.straps)
	a.strapMu.Unlock()

	if s != nil {
		t.Error("an invalid MAC produced a strap")
	}
	if n != 2 {
		t.Errorf("pool holds %d straps, want the 2 configured ones", n)
	}
}

// The pool is keyed on the MAC alone, so the slot a strap arrived for is not
// part of its identity.
func TestOneStrapServesBothSlots(t *testing.T) {
	a := poolApp(t)
	p1, p2 := a.players[slotP1], a.players[slotP2]

	p1.setAssignment(true, "CC:CC:CC:CC:CC:CC")
	p2.setAssignment(true, "CC:CC:CC:CC:CC:CC")

	if p1.strap != p2.strap {
		t.Fatal("one belt opened two connections")
	}
	if got := len(p1.strap.subscribers()); got != 2 {
		t.Errorf("strap has %d subscribers, want 2", got)
	}
	if !strings.EqualFold(p1.strap.mac, "CC:CC:CC:CC:CC:CC") {
		t.Errorf("strap mac = %q", p1.strap.mac)
	}
}
