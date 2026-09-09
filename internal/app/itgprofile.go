package app

import (
	"fmt"
	"os"
	"path/filepath"
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

	for slot := range macs {
		if sides.joined[slot] && sides.claim[slot] != "" && !taken(sides.claim[slot]) {
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
func (a *App) followProfiles(path string) {
	logInfof("[ITG] following player profiles from %s", path)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-a.stop:
			return
		case <-ticker.C:
			a.applyProfiles(path)
		}
	}
}

// applyProfiles reads one poll's worth and hands it to the players. Both
// setAssignment and setClaim are no-ops when nothing changed, so running this
// every second costs nothing while a song plays.
func (a *App) applyProfiles(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		a.releaseProfiles()
		return
	}
	sides, ok := parsePlayers(data, time.Now())
	if !ok {
		a.releaseProfiles()
		return
	}

	macs, claims := resolveSides(sides, a.snapshotConfig())
	for slot, p := range a.players {
		p.setAssignment(true, macs[slot])
		p.setClaim(claims[slot])
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
