package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var refNow = time.Date(2026, 9, 8, 14, 32, 7, 0, time.Local) // 20260908, 52327

// stamped builds a players.txt whose header is `age` seconds behind refNow, for
// parse tests that pass refNow in as the clock.
func stamped(age int, body string) []byte { return stampedAt(refNow, age, body) }

// stampedNow is for tests that go through applyProfiles, which reads the real
// clock: a file stamped on some other day is correctly rejected as stale.
func stampedNow(age int, body string) []byte { return stampedAt(time.Now(), age, body) }

func stampedAt(t time.Time, age int, body string) []byte {
	return []byte(fmt.Sprintf("%08d %d\n%s", dateStamp(t), secondsOfDay(t)-age, body))
}

func TestParsePlayers(t *testing.T) {
	cases := []struct {
		name   string
		data   []byte
		wantOK bool
		joined [2]bool
		claim  [2]string
	}{
		{
			name:   "both sides, one claiming",
			data:   stamped(0, "p1 24:AC:AC:18:41:CC\np2 -\n"),
			wantOK: true, joined: [2]bool{true, true},
			claim: [2]string{"24:AC:AC:18:41:CC", ""},
		},
		{
			// The case that motivated reporting joined-ness at all.
			name:   "lone P2, claiming nothing",
			data:   stamped(0, "p2 -\n"),
			wantOK: true, joined: [2]bool{false, true},
		},
		{
			// MACs arrive however the profile was written; normalizeMAC settles it.
			name:   "lowercase and dashes are canonicalized",
			data:   stamped(0, "p1 24-ac-ac-18-41-cc\n"),
			wantOK: true, joined: [2]bool{true, false},
			claim: [2]string{"24:AC:AC:18:41:CC", ""},
		},
		{
			name:   "an unknown side label does not discard the rest",
			data:   stamped(0, "p3 24:AC:AC:18:41:CC\np1 11:22:33:44:55:66\n"),
			wantOK: true, joined: [2]bool{true, false},
			claim: [2]string{"11:22:33:44:55:66", ""},
		},
		{
			name:   "junk where a MAC belongs reads as claiming nothing",
			data:   stamped(0, "p1 not-a-mac\n"),
			wantOK: true, joined: [2]bool{true, false},
		},
		{
			name:   "nobody joined is still a valid report",
			data:   stamped(0, ""),
			wantOK: true,
		},
		{
			// Clock skew. gotempo.lua makes the same allowance reading hr.txt.
			name:   "a stamp from the near future is fresh",
			data:   stamped(-3, "p1 24:AC:AC:18:41:CC\n"),
			wantOK: true, joined: [2]bool{true, false},
			claim: [2]string{"24:AC:AC:18:41:CC", ""},
		},

		{name: "too old", data: stamped(3600, "p1 24:AC:AC:18:41:CC\n")},
		{name: "yesterday", data: []byte("20260907 52327\np1 24:AC:AC:18:41:CC\n")},
		{name: "empty", data: []byte("")},
		{name: "no stamp", data: []byte("p1 24:AC:AC:18:41:CC\n")},
		{name: "truncated stamp", data: []byte("20260908\np1 24:AC:AC:18:41:CC\n")},
		{name: "not numbers", data: []byte("hello world\np1 24:AC:AC:18:41:CC\n")},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sides, ok := parsePlayers(c.data, refNow)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v", ok, c.wantOK)
			}
			if !ok {
				return
			}
			if sides.joined != c.joined {
				t.Errorf("joined = %v, want %v", sides.joined, c.joined)
			}
			if sides.claim != c.claim {
				t.Errorf("claim = %v, want %v", sides.claim, c.claim)
			}
		})
	}
}

// The resolution table from the plan, asserted row by row.
func TestResolveSides(t *testing.T) {
	const (
		houseP1 = "AA:AA:AA:AA:AA:AA"
		houseP2 = "BB:BB:BB:BB:BB:BB"
		mine    = "CC:CC:CC:CC:CC:CC"
	)
	oneStrap := Config{Current: houseP1}
	twoStraps := Config{Current: houseP1, CurrentP2: houseP2, TwoPlayer: true}

	cases := []struct {
		name       string
		cfg        Config
		sides      itgSides
		wantMACs   [2]string
		wantClaims [2]string
	}{
		{
			name: "nobody playing leaves both slots idle",
			cfg:  twoStraps,
		},
		{
			name:     "P1 alone, claiming nothing, gets the configured strap",
			cfg:      oneStrap,
			sides:    itgSides{joined: [2]bool{true, false}},
			wantMACs: [2]string{houseP1, ""},
		},
		{
			// The bug that exists today: this must land on slot 2 so it writes
			// hr-p2.txt, which is the file that side's panel reads.
			name:     "P2 alone, claiming nothing, gets the configured strap on slot 2",
			cfg:      oneStrap,
			sides:    itgSides{joined: [2]bool{false, true}},
			wantMACs: [2]string{"", houseP1},
		},
		{
			name:       "a claim is followed and gated",
			cfg:        oneStrap,
			sides:      itgSides{joined: [2]bool{true, false}, claim: [2]string{mine, ""}},
			wantMACs:   [2]string{mine, ""},
			wantClaims: [2]string{mine, ""},
		},
		{
			name:     "two players each keep their own slot's configured strap",
			cfg:      twoStraps,
			sides:    itgSides{joined: [2]bool{true, true}},
			wantMACs: [2]string{houseP1, houseP2},
		},
		{
			name: "one claims, one does not",
			cfg:  twoStraps,
			sides: itgSides{
				joined: [2]bool{true, true},
				claim:  [2]string{"", mine},
			},
			wantMACs:   [2]string{houseP1, mine},
			wantClaims: [2]string{"", mine},
		},
		{
			// Claims must beat configuration, or the person actually wearing the
			// strap loses it to the slot that merely has it configured.
			name:       "a claimed strap is not stolen by the slot configured to it",
			cfg:        oneStrap,
			sides:      itgSides{joined: [2]bool{false, true}, claim: [2]string{"", houseP1}},
			wantMACs:   [2]string{"", houseP1},
			wantClaims: [2]string{"", houseP1},
		},
		{
			// Two people naming one strap is a choice, not a misconfiguration:
			// they are swapping sides, or one has stopped playing and lent the
			// belt out. The pool connects it once and feeds both slots, and both
			// gates open because both claims match the strap that is connected.
			name: "the same strap claimed twice goes to both slots",
			cfg:  twoStraps,
			sides: itgSides{
				joined: [2]bool{true, true},
				claim:  [2]string{mine, mine},
			},
			wantMACs:   [2]string{mine, mine},
			wantClaims: [2]string{mine, mine},
		},
		{
			name:     "both joined, neither claiming, only one strap configured",
			cfg:      oneStrap,
			sides:    itgSides{joined: [2]bool{true, true}},
			wantMACs: [2]string{houseP1, ""},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			macs, claims := resolveSides(c.sides, c.cfg)
			if macs != c.wantMACs {
				t.Errorf("macs = %v, want %v", macs, c.wantMACs)
			}
			if claims != c.wantClaims {
				t.Errorf("claims = %v, want %v", claims, c.wantClaims)
			}
		})
	}
}

// The end-to-end poll: a file on disk moves the straps, and losing the file
// hands them back.
func TestApplyProfiles(t *testing.T) {
	module, path := itgChannel(t)
	a, p1, p2 := twoPlayerApp(t)

	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(path, stampedNow(0, body), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// A claim on P1 takes over; P2 keeps its configured strap.
	write("p1 CC:CC:CC:CC:CC:CC\np2 -\n")
	a.applyProfiles(module)
	if got := p1.effectiveMAC(); got != "CC:CC:CC:CC:CC:CC" {
		t.Errorf("P1 = %q, want the claimed strap", got)
	}
	if got := p2.effectiveMAC(); got != "BB:BB:BB:BB:BB:BB" {
		t.Errorf("P2 = %q, want its configured strap", got)
	}

	// The player moves to the other side. The strap must follow, and must not be
	// left behind on the slot they vacated.
	write("p2 CC:CC:CC:CC:CC:CC\n")
	a.applyProfiles(module)
	if got := p1.effectiveMAC(); got != "" {
		t.Errorf("after moving to P2, P1 = %q, want idle", got)
	}
	if got := p2.effectiveMAC(); got != "CC:CC:CC:CC:CC:CC" {
		t.Errorf("after moving to P2, P2 = %q, want the claimed strap", got)
	}

	// The game goes away. Both slots return to the operator's config.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	a.applyProfiles(module)
	if got := p1.effectiveMAC(); got != "AA:AA:AA:AA:AA:AA" {
		t.Errorf("after the file went away, P1 = %q, want config", got)
	}
	if got := p2.effectiveMAC(); got != "BB:BB:BB:BB:BB:BB" {
		t.Errorf("after the file went away, P2 = %q, want config", got)
	}
}

// A stale file is as good as no file: ITGmania cannot say goodbye when it
// crashes, so a stamp that stops advancing is what releases the straps.
func TestApplyProfilesReleasesOnStaleFile(t *testing.T) {
	module, path := itgChannel(t)
	a, p1, _ := twoPlayerApp(t)

	if err := os.WriteFile(path, stampedNow(0, "p1 CC:CC:CC:CC:CC:CC\n"), 0644); err != nil {
		t.Fatal(err)
	}
	a.applyProfiles(module)
	if got := p1.effectiveMAC(); got != "CC:CC:CC:CC:CC:CC" {
		t.Fatalf("P1 = %q, want the claimed strap", got)
	}

	if err := os.WriteFile(path, stampedNow(3600, "p1 CC:CC:CC:CC:CC:CC\n"), 0644); err != nil {
		t.Fatal(err)
	}
	a.applyProfiles(module)
	if got := p1.effectiveMAC(); got != "AA:AA:AA:AA:AA:AA" {
		t.Errorf("P1 = %q, want config after the stamp went stale", got)
	}
}

// Two straps following profiles must not share one CSV, even with the
// two-player config flag off. Keyed off active slots rather than that flag.
func TestSessionSuffixFollowsActiveSlotsNotTheFlag(t *testing.T) {
	a, p1, p2 := twoPlayerApp(t)

	a.cfgMu.Lock()
	a.cfg.TwoPlayer = false
	a.cfg.Current = ""
	a.cfg.CurrentP2 = ""
	a.cfgMu.Unlock()

	if got := p1.sessionSuffix(); got != "" {
		t.Errorf("no straps active: suffix = %q, want empty", got)
	}

	p1.setAssignment(true, "AA:AA:AA:AA:AA:AA")
	if got := p1.sessionSuffix(); got != "" {
		t.Errorf("one strap active: suffix = %q, want empty", got)
	}

	p2.setAssignment(true, "BB:BB:BB:BB:BB:BB")
	if got, want := p1.sessionSuffix(), "-p1"; got != want {
		t.Errorf("two straps active: P1 suffix = %q, want %q", got, want)
	}
	if got, want := p2.sessionSuffix(), "-p2"; got != want {
		t.Errorf("two straps active: P2 suffix = %q, want %q", got, want)
	}
}

// A visiting player's strap must not be written into the cabinet's config.
func TestMarkConnectedSkipsDrivenSlots(t *testing.T) {
	a, p1, _ := twoPlayerApp(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	p1.setAssignment(true, "CC:CC:CC:CC:CC:CC")
	p1.strap.markConnected()

	for _, k := range a.snapshotConfig().Known {
		if strings.EqualFold(k.MAC, "CC:CC:CC:CC:CC:CC") {
			t.Error("a driven connection was recorded in config")
		}
	}

	// An operator-driven connection still is, which is how the tray list learns
	// names and last-used times.
	p1.setAssignment(false, "") // back to the configured strap
	p1.strap.markConnected()
	found := false
	for _, k := range a.snapshotConfig().Known {
		if strings.EqualFold(k.MAC, "AA:AA:AA:AA:AA:AA") {
			found = true
		}
	}
	if !found {
		t.Error("an operator-driven connection was not recorded")
	}
}

// itgChannel builds a throwaway module and its data folder, and returns the
// module path callers pass to applyProfiles alongside the players.txt they
// write. Both are derived the way the running app derives them, so a change to
// the layout cannot leave a test writing where nothing reads.
func itgChannel(t *testing.T) (module, players string) {
	t.Helper()
	dir := t.TempDir()
	module = writeModule(t, dir)
	if err := os.MkdirAll(itgDir(module), 0755); err != nil {
		t.Fatal(err)
	}
	return module, playersPathFor(module)
}

func TestPlayersPathFor(t *testing.T) {
	module := filepath.Join("/themes", "Simply Love", "Modules", "gotempo.lua")
	want := filepath.Join(filepath.Dir(module), "gotempo", "players.txt")
	if got := playersPathFor(module); got != want {
		t.Errorf("playersPathFor = %q, want %q", got, want)
	}
}

// The bytes gotempo.lua actually emits, captured by driving its real
// WritePlayers under a stub. Hand-written fixtures elsewhere in this file test
// the parser's tolerance; this one tests that the two repos agree on the format
// at all, which nothing else would catch until a cabinet was silent.
func TestParsesWhatTheModuleWrites(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		joined [2]bool
		claim  [2]string
	}{
		{
			name:   "both joined, one naming a strap",
			body:   "p1 24:AC:AC:18:41:CC\np2 -\n",
			joined: [2]bool{true, true},
			claim:  [2]string{"24:AC:AC:18:41:CC", ""},
		},
		{
			name:   "lone P2, no profile",
			body:   "p2 -\n",
			joined: [2]bool{false, true},
		},
		{
			name: "nobody joined: the stamp alone",
			body: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sides, ok := parsePlayers(stamped(0, c.body), refNow)
			if !ok {
				t.Fatal("parser rejected what the module writes")
			}
			if sides.joined != c.joined {
				t.Errorf("joined = %v, want %v", sides.joined, c.joined)
			}
			if sides.claim != c.claim {
				t.Errorf("claim = %v, want %v", sides.claim, c.claim)
			}
		})
	}
}

// ── the strap picker's channel ───────────────────────────────────────────────

// The scan request rides in players.txt so it inherits that file's stamp. An
// unknown label must not cost the sides around it, which is what lets an older
// gotempo ignore the line instead of choking on it.
func TestParsePlayersReadsScanRequest(t *testing.T) {
	sides, ok := parsePlayers(stamped(0, "p1 24:AC:AC:18:41:CC\nscan 52327\np2 -\n"), refNow)
	if !ok {
		t.Fatal("a file carrying a scan line was rejected")
	}
	if sides.scan != 52327 {
		t.Errorf("scan = %d, want 52327", sides.scan)
	}
	if !sides.joined[slotP1] || sides.claim[slotP1] != "24:AC:AC:18:41:CC" {
		t.Error("the scan line ate P1")
	}
	if !sides.joined[slotP2] {
		t.Error("the scan line ate P2")
	}

	// No request is the normal state, and must be distinguishable from one.
	quiet, _ := parsePlayers(stamped(0, "p1 -\n"), refNow)
	if quiet.scan != 0 {
		t.Errorf("scan = %d with no request, want 0", quiet.scan)
	}

	// A malformed token is not a request. It reaches here from a file gotempo
	// does not write.
	junk, _ := parsePlayers(stamped(0, "scan soon\n"), refNow)
	if junk.scan != 0 {
		t.Errorf("scan = %d for a non-numeric token, want 0", junk.scan)
	}
}

// The list the picker reads. Its contents are the point: a scan alone cannot
// see a connected strap, so one player could never pick the strap another is
// already wearing.
func TestPublishDevices(t *testing.T) {
	module, _ := itgChannel(t)
	a, p1, _ := twoPlayerApp(t)

	// A strap in the pool, which is what a connected belt looks like.
	p1.setAssignment(true, "CC:CC:CC:CC:CC:CC")

	a.publishDevices(module, []KnownDevice{{MAC: "11:22:33:44:55:66", Name: "Polar H10 0F22B1C4"}})

	data, err := os.ReadFile(devicesPathFor(module))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 2 {
		t.Fatalf("devices.txt = %q, want a stamp and at least one strap", data)
	}

	var date, secs int
	if n, err := fmt.Sscanf(lines[0], "%d %d", &date, &secs); n != 2 || err != nil {
		t.Errorf("first line is not a stamp: %q", lines[0])
	}
	if date != dateStamp(time.Now()) {
		t.Errorf("stamp date = %d, want today", date)
	}

	body := strings.Join(lines[1:], "\n")
	for _, want := range []string{
		"11:22:33:44:55:66\tPolar H10 0F22B1C4", // scanned
		"AA:AA:AA:AA:AA:AA",                     // configured
		"CC:CC:CC:CC:CC:CC",                     // pooled, and so not advertising
	} {
		if !strings.Contains(body, want) {
			t.Errorf("devices.txt is missing %q:\n%s", want, body)
		}
	}
}

// One belt is one entry however its MAC is spelled, or the picker would offer
// the same strap twice and the two rows would disagree about who owns it.
func TestPublishDevicesDedupes(t *testing.T) {
	module, _ := itgChannel(t)
	a, _, _ := twoPlayerApp(t)

	a.publishDevices(module, []KnownDevice{
		{MAC: "11:22:33:44:55:66", Name: "Polar H10"},
		{MAC: "11:22:33:44:55:66", Name: "Polar H10"},
		{MAC: "aa:aa:aa:aa:aa:aa", Name: "lowercase"},
	})

	data, _ := os.ReadFile(devicesPathFor(module))
	if got := strings.Count(string(data), "11:22:33:44:55:66"); got != 1 {
		t.Errorf("a repeated MAC appears %d times, want 1", got)
	}
	if got := strings.Count(strings.ToUpper(string(data)), "AA:AA:AA:AA:AA:AA"); got != 1 {
		t.Errorf("one belt spelled two ways appears %d times, want 1", got)
	}
}

// The list names straps belonging to whoever was in the room. It has no
// business outliving the person reading it.
func TestDevicesExpire(t *testing.T) {
	module, _ := itgChannel(t)
	a, _, _ := twoPlayerApp(t)

	a.publishDevices(module, []KnownDevice{{MAC: "11:22:33:44:55:66", Name: "Polar"}})
	path := devicesPathFor(module)

	// Inside its window it stays readable, or the picker would blank mid-pick.
	a.expireDevices(module, time.Now())
	if data, _ := os.ReadFile(path); len(data) == 0 {
		t.Fatal("the list was cleared while still current")
	}

	a.expireDevices(module, time.Now().Add(devicesTTL+time.Second))
	if data, _ := os.ReadFile(path); len(data) != 0 {
		t.Errorf("an expired list still holds %q", data)
	}

	// Empty, not absent: the module reads an empty file as "nothing here", the
	// same way it reads an empty hr.txt.
	if _, err := os.Stat(path); err != nil {
		t.Errorf("devices.txt was removed rather than blanked: %v", err)
	}
}

// The picker leaves its line in place for as long as it is open, so a token
// already served must not start another scan every second. A retry is a new
// token.
func TestServeScanIgnoresARepeatedToken(t *testing.T) {
	a, _, _ := twoPlayerApp(t)

	if !a.claimScanToken(52327) {
		t.Error("the first request was not served")
	}
	// Finish it first, or "busy" would reject the repeat and the token rule
	// would never be exercised.
	a.finishScan()
	if a.claimScanToken(52327) {
		t.Error("the same token was served twice")
	}

	if !a.claimScanToken(52400) {
		t.Error("a retry with a new token was not served")
	}
}
