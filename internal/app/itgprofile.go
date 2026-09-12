package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Straps follow the player's ITGmania profile.
//
// A player can name their own strap in their game profile, and while they are
// playing gotempo follows it instead of whatever the tray is set to. Choosing a
// strap is otherwise a job someone does at the PC, which nobody does at a
// cabinet.
//
// The module publishes players.txt beside gotempo.lua, next to hr.txt, and this
// polls it once a second:
//
//	20260908 52327
//	p1 24:AC:AC:18:41:CC
//	p2 -
//
// The first line is a local date and seconds-since-midnight stamp, the same
// shape hr.txt uses in the other direction and for the same reason: when
// ITGmania exits or crashes there is nothing to send a goodbye, so a stamp that
// stops advancing is what returns control to the operator's config. Local time
// because the game's Lua has no os.time to build an epoch from.
//
// A line means that side is joined; "-" (or anything that is not a MAC) means
// joined with no strap named; no line means nobody is on that side. The
// difference between the last two is load-bearing, because "nobody is on P2"
// and "someone is on P2 who has configured nothing" need opposite behaviour:
// the first must leave the slot idle, the second must put the configured strap
// there so it lands in that side's files.
//
// Nothing here writes to config.json. The assignment lives in memory and is
// dropped on exit, so quitting returns to whatever the operator set up.

const playersFile = "players.txt"

// itgClaimTimeout is how far behind the stamp may fall before the file is
// treated as dead. The module rewrites it every second, so this is generous;
// it only has to outlast a stutter, not a song.
const itgClaimTimeout = 10 * time.Second

// playersPathFor derives the file from the module's location, the same way
// itgHRPathFor does. Both sides resolve this directory independently, which is
// why the channel needs no configuration of its own.
func playersPathFor(module string) string {
	return filepath.Join(itgDir(module), playersFile)
}

// itgSides is what the game reports: per slot, whether anyone is on that side
// and which strap they say is theirs.
type itgSides struct {
	joined [2]bool
	claim  [2]string

	// scan is the token from a "scan <token>" line, zero when the game is not
	// asking. The module sends the second of day it made the request in, and
	// gotempo serves a token it has not served before. That is what lets the
	// line sit in the file for as long as the picker is open without
	// re-triggering a scan every poll, while a retry is simply a new token.
	scan int
}

// dateStamp and secondsOfDay are the wire format's two halves, shared with the
// hr.txt writer so both directions of the channel agree by construction.
func dateStamp(t time.Time) int    { return t.Year()*10000 + int(t.Month())*100 + t.Day() }
func secondsOfDay(t time.Time) int { return t.Hour()*3600 + t.Minute()*60 + t.Second() }

// parsePlayers reads the file. ok is false when it is unusable for any reason:
// malformed, from another day, or too old. Every one of those means the same
// thing to the caller, which is to stop following it, so they are not
// distinguished.
func parsePlayers(data []byte, now time.Time) (sides itgSides, ok bool) {
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 {
		return itgSides{}, false
	}

	var date, secs int
	if n, err := fmt.Sscanf(strings.TrimSpace(lines[0]), "%d %d", &date, &secs); n != 2 || err != nil {
		return itgSides{}, false
	}
	if date != dateStamp(now) {
		return itgSides{}, false
	}

	// Same calendar day, so this can only go negative on clock skew. Treat a
	// stamp from the near future as fresh rather than ancient, matching the
	// allowance gotempo.lua makes reading in the other direction.
	age := time.Duration(secondsOfDay(now)-secs) * time.Second
	if age < 0 {
		age = 0
	}
	if age > itgClaimTimeout {
		return itgSides{}, false
	}

	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		var slot int
		switch strings.ToLower(fields[0]) {
		case "p1":
			slot = slotP1
		case "p2":
			slot = slotP2
		case "scan":
			// The in-game strap picker asking for a device list. Not a side, so
			// it neither joins anything nor carries a claim.
			if n, err := strconv.Atoi(fields[1]); err == nil {
				sides.scan = n
			}
			continue
		default:
			continue // an unknown side label is not a reason to drop the rest
		}
		sides.joined[slot] = true

		// Anything that is not a MAC, "-" included, is "joined, named nothing".
		if mac, valid := normalizeMAC(fields[1]); valid {
			sides.claim[slot] = mac
		}
	}
	return sides, true
}

// resolveSides works out which strap each slot follows and which of those the
// player has actually claimed. It is pure so the rules can be tested without a
// BLE stack or a game.
//
// A strap may hold only one slot: two loops connecting one strap would draw one
// person's heart rate as two people's. Claims are resolved before configuration
// so the tie goes the right way. Someone playing alone on P2 with their own
// strap, on a cabinet whose config happens to name that same strap, must keep
// it on their side rather than lose it to slot 1 on ordering.
func resolveSides(sides itgSides, cfg Config) (macs, claims [2]string) {
	taken := func(mac string) bool {
		if mac == "" {
			return false
		}
		for _, m := range macs {
			if strings.EqualFold(m, mac) {
				return true
			}
		}
		return false
	}

	// Two sides may name the same strap, and deliberately so: people swap mid
	// session, or one stops playing and lends their belt out. The pool connects
	// it once and feeds both slots, and both gates open because both claims
	// match the connected strap. There is no taken() check here for that reason.
	//
	// The check below, on the configuration fallback, stays: config handing one
	// strap to two slots nobody asked for is the wrong-person bug, not a choice.
	for slot := range macs {
		if sides.joined[slot] && sides.claim[slot] != "" {
			macs[slot] = sides.claim[slot]
			claims[slot] = sides.claim[slot]
		}
	}

	// Joined sides that named nothing fall back to the operator's setup: this
	// slot's own key when it has one, otherwise the single configured strap,
	// which is what puts it on the side somebody is actually standing on.
	for slot := range macs {
		// A side that named a strap gets that strap or nothing, even when the
		// naming failed because someone else already holds it. Falling back to
		// configuration here would hand them a strap they did not ask for with
		// the gate open, which is the wrong-person bug the gate exists to stop.
		if !sides.joined[slot] || macs[slot] != "" || sides.claim[slot] != "" {
			continue
		}
		for _, cand := range []string{cfg.currentFor(slot), cfg.Current} {
			if cand != "" && !taken(cand) {
				macs[slot] = cand
				break
			}
		}
	}
	return macs, claims
}

// followProfiles polls players.txt until the app stops. Started only when the
// ITGmania module path is configured, so a desktop user never pays for it.
//
// It also carries the device list's upkeep, because that runs on the same
// once-a-second beat and needs no timer of its own.
func (a *App) followProfiles(module string) {
	path := playersPathFor(module)
	logInfof("[ITG] following player profiles from %s", path)

	// A run that died mid-scan leaves a list behind. Blank it before anyone can
	// read it as current.
	a.clearDevices(module)

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-a.stop:
			return
		case now := <-ticker.C:
			a.applyProfiles(module)
			a.expireDevices(module, now)
		}
	}
}

// applyProfiles reads one poll's worth and hands it to the players. Both
// setAssignment and setClaim are no-ops when nothing changed, so running this
// every second costs nothing while a song plays.
func (a *App) applyProfiles(module string) {
	data, err := os.ReadFile(playersPathFor(module))
	if err != nil {
		a.releaseProfiles()
		return
	}
	sides, ok := parsePlayers(data, time.Now())
	if !ok {
		a.releaseProfiles()
		return
	}

	if sides.scan != 0 {
		a.serveScan(module, sides.scan)
	}

	macs, claims := resolveSides(sides, a.snapshotConfig())
	for slot, p := range a.players {
		p.setAssignment(true, macs[slot])
		p.setClaim(claims[slot])
	}
}

// The device list.
//
// The in-game picker cannot scan: the module has no Bluetooth, which is the
// whole point of the split. So it asks, gotempo scans, and the answer lands
// beside hr.txt in the same stamped format everything else in this channel
// uses.
//
// The list is short-lived on purpose. It names straps belonging to whoever
// happened to be in the room, and a file full of other people's belts has no
// business sitting in a theme folder for the rest of the day. It is blanked a
// minute after publishing, and again at startup.

// devicesTTL is how long a published list stays readable. Long enough to pick
// from, short enough that it is gone before the next person walks up.
const devicesTTL = 60 * time.Second

// scanDuration matches the tray's Rescan, so both discovery paths see the same
// straps. A strap that advertises slowly needs most of it.
const scanDuration = switchScanDuration

// serveScan answers one request. The token is the module's, and a token already
// served is ignored: the picker leaves its line in place for as long as it is
// open, so without this every poll would start another scan. A retry is a new
// token.
func (a *App) serveScan(module string, token int) {
	if !a.claimScanToken(token) {
		return
	}
	go func() {
		defer a.finishScan()
		logInfof("[ITG] strap picker asked for a scan")
		a.publishDevices(module, a.scanDevices(scanDuration))
	}()
}

// claimScanToken reserves the radio for one request, and is the whole of the
// de-duplication rule. Split out from serveScan so it can be tested without
// opening an adapter.
func (a *App) claimScanToken(token int) bool {
	a.scanReqMu.Lock()
	defer a.scanReqMu.Unlock()
	if a.scanBusy || token == a.scanToken {
		return false
	}
	a.scanBusy, a.scanToken = true, token
	return true
}

func (a *App) finishScan() {
	a.scanReqMu.Lock()
	a.scanBusy = false
	a.scanReqMu.Unlock()
}

// publishDevices writes the list the picker reads.
//
// Three sources, not just the scan. A connected strap is not advertising, so a
// scan cannot see it -- and the case where one player picks the strap another is
// already wearing is a wanted one, not an accident. Leaving the pool out would
// hide exactly the strap somebody was looking for.
func (a *App) publishDevices(module string, scanned []KnownDevice) {
	seen := map[string]string{}
	add := func(mac, name string) {
		if mac == "" {
			return
		}
		key := strings.ToUpper(mac)
		// First writer wins: scan results carry the friendliest names.
		if _, ok := seen[key]; !ok {
			seen[key] = name
		}
	}
	for _, d := range scanned {
		add(d.MAC, d.Name)
	}
	for _, k := range a.snapshotConfig().Known {
		add(k.MAC, k.Name)
	}
	for _, mac := range a.pooledMACs() {
		add(mac, "")
	}

	macs := make([]string, 0, len(seen))
	for mac := range seen {
		macs = append(macs, mac)
	}
	sort.Strings(macs) // stable order, so a redraw cannot reshuffle the list

	var b strings.Builder
	now := time.Now()
	fmt.Fprintf(&b, "%08d %d\n", dateStamp(now), secondsOfDay(now))
	for _, mac := range macs {
		fmt.Fprintf(&b, "%s\t%s\n", mac, seen[mac])
	}

	path := devicesPathFor(module)
	if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
		logErrf("[ITG] could not write %s: %v", path, err)
		return
	}

	a.scanReqMu.Lock()
	a.devicesUntil = now.Add(devicesTTL)
	a.scanReqMu.Unlock()
	logInfof("[ITG] published %d straps to the picker", len(macs))
}

// expireDevices blanks the list once its time is up. It rides the profile poll
// rather than a timer: one beat, one place to read the rule, and no way for a
// stale timer to blank a list published after it.
func (a *App) expireDevices(module string, now time.Time) {
	a.scanReqMu.Lock()
	due := !a.devicesUntil.IsZero() && now.After(a.devicesUntil)
	if due {
		a.devicesUntil = time.Time{}
	}
	a.scanReqMu.Unlock()
	if due {
		a.clearDevices(module)
	}
}

// clearDevices truncates the list. Empty reads as "nothing here", the same way
// an empty hr.txt reads as no reading, so there is no file to delete and no
// directory entry to churn.
func (a *App) clearDevices(module string) {
	path := devicesPathFor(module)
	if _, err := os.Stat(path); err != nil {
		return
	}
	if err := os.WriteFile(path, nil, 0644); err != nil {
		logErrf("[ITG] could not clear %s: %v", path, err)
	}
}

// releaseProfiles hands the slots back to config, for when the game is not
// running or has stopped updating the file.
func (a *App) releaseProfiles() {
	for _, p := range a.players {
		p.setAssignment(false, "")
		p.setClaim("")
	}
}
