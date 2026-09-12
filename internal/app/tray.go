package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"fyne.io/systray"
)

type tray struct {
	app *App

	mLog       *systray.MenuItem // single Start/Stop logging toggle
	mDriven    *systray.MenuItem // inert banner, shown only while a profile drives a slot
	mTwoPlayer *systray.MenuItem
	mOpenLogs  *systray.MenuItem
	mOpenConf  *systray.MenuItem
	mAutoLog   *systray.MenuItem
	mAutostart *systray.MenuItem
	mQuit      *systray.MenuItem

	switchSlots []*systray.MenuItem
	slotMACs    []string
	slotNames   []string
	mRescan     *systray.MenuItem // doubles as the scan-status row

	slotClicks  chan int
	scanDone    chan []KnownDevice
	lastScanned []KnownDevice
	scanning    bool

	// cabinet is set when itgmania_module is configured. This install feeds a
	// game rather than a stream or a workout log, so the OBS and CSV controls
	// are hidden outright instead of greyed: greying says "not right now", and
	// these are not part of what this machine does at all.
	cabinet bool
}

// deviceEntry is one rendered row in the switch submenu.
type deviceEntry struct {
	mac, name string
	lastUsed  string
	known     bool
}

// buildEntries merges known devices (recent first) with freshly scanned
// unknowns, capping at maxSwitchSlots.
func buildEntries(cfg Config, scanned []KnownDevice) []deviceEntry {
	var entries []deviceEntry
	knownSet := map[string]bool{}
	for _, k := range cfg.sortedKnown() {
		knownSet[strings.ToUpper(k.MAC)] = true
		entries = append(entries, deviceEntry{mac: k.MAC, name: k.Name, lastUsed: k.LastUsed, known: true})
	}
	for _, s := range scanned {
		if knownSet[strings.ToUpper(s.MAC)] {
			continue
		}
		entries = append(entries, deviceEntry{mac: s.MAC, name: s.Name, known: false})
	}

	// Assigned straps float to the front so the cap can never hide one. Ordering
	// is otherwise most-recently-used, and a strap assigned but never yet
	// connected sorts last by that rule, so an operator could assign a new belt
	// and find it missing from the list with no way to unassign it.
	sort.SliceStable(entries, func(i, j int) bool {
		return assignedSlot(cfg, entries[i].mac) >= 0 && assignedSlot(cfg, entries[j].mac) < 0
	})

	if len(entries) > maxSwitchSlots {
		entries = entries[:maxSwitchSlots]
	}
	return entries
}

func humanizeSince(ts string) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// assignedSlot returns the slot a strap occupies, or -1 when it is unassigned.
// A strap can hold at most one slot, which is what makes the tray's cycling
// unambiguous.
func assignedSlot(cfg Config, mac string) int {
	switch {
	case mac == "":
		return -1
	case strings.EqualFold(mac, cfg.Current):
		return slotP1
	case strings.EqualFold(mac, cfg.CurrentP2):
		return slotP2
	}
	return -1
}

// cycleAssignment moves a strap to its next slot: unassigned -> P1 -> P2 ->
// unassigned. It returns the complete new assignment, indexed by slot.
//
// A slot holds one strap and a strap holds one slot, so taking an occupied slot
// displaces whoever was there. That is what keeps one click understandable: the
// list can never show the same strap twice, or two straps claiming P1.
func cycleAssignment(cfg Config, mac string) [2]string {
	next := assignedSlot(cfg, mac) + 1 // -1 -> P1, P1 -> P2, P2 -> 2 (off the end)

	out := [2]string{cfg.Current, cfg.CurrentP2}
	for slot, held := range out {
		if strings.EqualFold(held, mac) {
			out[slot] = "" // lift it out of wherever it was
		}
	}
	if next <= slotP2 {
		out[next] = mac // displacing whatever held that slot
	}
	return out
}

func slotLabel(e deviceEntry, assigned int, twoPlayer bool) string {
	name := e.name
	if name == "" {
		name = e.mac
	}
	if twoPlayer {
		// The marker replaces the "(current)"/age suffix: which side a strap is
		// on is the only thing worth reading once two are in play.
		switch assigned {
		case slotP1:
			return "[P1] " + name
		case slotP2:
			return "[P2] " + name
		}
	} else if assigned == slotP1 {
		return name + " (current)"
	}
	switch {
	case !e.known:
		return name + " — new"
	case e.lastUsed == "":
		return name
	default:
		return name + " — " + humanizeSince(e.lastUsed)
	}
}

func (t *tray) refresh() {
	// The icon reflects the whole app, so one live strap is enough to show
	// connected: with two in play the tray has only one icon to say it with.
	connected := t.app.anyConnected()
	_, logging := t.app.p1().state.snapshot()

	switch {
	case !connected:
		systray.SetIcon(imgDisconnected)
	case logging:
		systray.SetIcon(imgRunning)
	default:
		systray.SetIcon(imgConnected)
	}

	// While a game profile is choosing straps, the controls that would change
	// that choice cannot take effect. Greying them is the honest report: the
	// alternative is a menu that accepts clicks and silently ignores them.
	driven := t.app.anyDriven()
	if driven {
		t.mDriven.Show()
	} else {
		t.mDriven.Hide()
	}

	// One toggle that reflects the current state: "Stop logging" while logging,
	// otherwise "Start logging". Greyed out when there's no connection to log
	// from (nothing to start or stop), and while a profile drives a slot, since
	// a driven strap's readings are not written to a CSV.
	switch {
	case driven:
		t.mLog.SetTitle("Start logging")
		t.mLog.Disable()
	case connected && logging:
		t.mLog.SetTitle("Stop logging")
		t.mLog.Enable()
	case connected:
		t.mLog.SetTitle("Start logging")
		t.mLog.Enable()
	default:
		t.mLog.SetTitle("Start logging")
		t.mLog.Disable()
	}

	if driven {
		t.mAutoLog.Disable()
		// The game owns sides while it drives: the driven path never consults
		// currentFor, so this box can be unchecked while a profile puts a strap
		// on P2. Leaving it clickable lets it go on saying otherwise.
		t.mTwoPlayer.Disable()
	} else {
		t.mAutoLog.Enable()
		t.mTwoPlayer.Enable()
	}

	if t.cabinet {
		// Hidden every refresh rather than once at build time, since systray
		// has no "stays hidden" state and a later Show elsewhere would undo it.
		t.mLog.Hide()
		t.mAutoLog.Hide()
		t.mOpenLogs.Hide()
	}

	t.renderSwitch()
}

// renderSwitch updates the device rows. They live in the top-level menu rather
// than a nested submenu: XFCE's old libdbusmenu (the 18.10 build it still
// ships) intermittently renders a nested submenu as an empty box, but top-level
// items render reliably. Only slots backed by a real device (known or freshly
// scanned) are shown; the rest are hidden, so the list tracks the actual device
// count.
func (t *tray) renderSwitch() {
	cfg := t.app.snapshotConfig()
	driven := t.app.anyDriven()
	entries := buildEntries(cfg, t.lastScanned)
	for i, s := range t.switchSlots {
		if i < len(entries) {
			e := entries[i]
			assigned := assignedSlot(cfg, e.mac)
			s.SetTitle(slotLabel(e, assigned, cfg.TwoPlayer))

			// With one strap the current row is inert, since clicking it would
			// switch to what is already selected. With two, every row stays
			// clickable: clicking an assigned strap is how it is cycled onward
			// and eventually off.
			//
			// None of them are while a profile is driving: a click would write
			// config and then change nothing, because effectiveMAC keeps
			// returning the override. The banner above says why.
			if driven || (!cfg.TwoPlayer && assigned == slotP1) {
				s.Disable()
			} else {
				s.Enable()
			}

			// Green dot on each assigned strap that is actually connected; a
			// transparent placeholder on the rest keeps the icon column aligned.
			if assigned >= 0 && t.app.players[assigned].isConnected() {
				s.SetIcon(imgDotConnected)
			} else {
				s.SetIcon(imgDotNone)
			}
			t.slotMACs[i] = e.mac
			t.slotNames[i] = e.name
			s.Show()
		} else {
			t.slotMACs[i] = ""
			t.slotNames[i] = ""
			s.Hide()
		}
	}
}

func (t *tray) startScan() {
	if t.scanning {
		return
	}
	t.scanning = true
	t.mRescan.SetTitle("Scanning…")
	t.mRescan.Disable()
	go func() {
		t.scanDone <- t.app.scanDevices(switchScanDuration)
	}()
}

// loop owns all tray UI mutation. The caller builds the initial menu state via
// refresh() *before* starting this goroutine, so the panel's first read is the
// final layout. autoScan kicks off a scan immediately, used on startup when no
// device is configured yet.
func (t *tray) loop(autoScan bool) {
	if autoScan {
		t.startScan()
	}
	for {
		select {
		case <-t.app.uiUpdates:
			t.refresh()
		case <-t.mLog.ClickedCh:
			// Same rule refresh() uses to enable this item, or with only P2
			// connected it would look clickable and do nothing.
			_, logging := t.app.p1().state.snapshot()
			if t.app.anyConnected() && !t.app.anyDriven() {
				t.app.setLogging(!logging)
			}
			t.refresh()
		case <-t.mOpenLogs.ClickedCh:
			go openFolder(logsDir())
		case <-t.mOpenConf.ClickedCh:
			// Dir of configPath, not configDir, so --config points here too.
			go openFolder(filepath.Dir(configPath()))
		case <-t.mAutoLog.ClickedCh:
			if t.app.anyDriven() {
				break
			}
			// Only sets the launch preference; does not change current logging.
			if t.mAutoLog.Checked() {
				t.mAutoLog.Uncheck()
				t.app.setAutoLog(false)
			} else {
				t.mAutoLog.Check()
				t.app.setAutoLog(true)
			}
			t.refresh()
		case <-t.mAutostart.ClickedCh:
			if t.mAutostart.Checked() {
				if err := disableAutostart(); err != nil {
					logErrf("[autostart] disable failed: %v", err)
				} else {
					t.mAutostart.Uncheck()
				}
			} else {
				if err := enableAutostart(); err != nil {
					logErrf("[autostart] enable failed: %v", err)
				} else {
					t.mAutostart.Check()
				}
			}
		case <-t.mTwoPlayer.ClickedCh:
			if t.app.anyDriven() {
				break
			}
			on := !t.mTwoPlayer.Checked()
			if on {
				t.mTwoPlayer.Check()
			} else {
				t.mTwoPlayer.Uncheck()
			}
			t.app.setTwoPlayer(on)
			t.refresh()
		case i := <-t.slotClicks:
			mac := t.slotMACs[i]
			name := t.slotNames[i]
			if mac == "" || t.app.anyDriven() {
				break
			}
			cfg := t.app.snapshotConfig()
			if cfg.TwoPlayer {
				// Cycle: unassigned -> P1 -> P2 -> unassigned.
				t.app.assignSlots(cycleAssignment(cfg, mac), KnownDevice{MAC: mac, Name: name})
			} else if !strings.EqualFold(mac, t.app.p1().currentMAC()) {
				t.app.p1().switchTo(mac, name)
			}
			t.refresh()
		case <-t.mRescan.ClickedCh:
			t.startScan()
		case res := <-t.scanDone:
			t.scanning = false
			t.mRescan.SetTitle("Rescan for new devices")
			t.mRescan.Enable()
			t.lastScanned = res
			t.renderSwitch()
		case <-t.mQuit.ClickedCh:
			close(t.app.stop)
			systray.Quit()
			// Guarantee the process dies even if a dbus teardown stalls,
			// so it can be relaunched cleanly.
			go func() {
				time.Sleep(2 * time.Second)
				os.Exit(0)
			}()
			return
		}
	}
}
