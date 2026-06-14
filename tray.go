package main

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"fyne.io/systray"
)

type tray struct {
	app *App

	mLog       *systray.MenuItem // single Start/Stop logging toggle
	mOpenLogs  *systray.MenuItem
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

func slotLabel(e deviceEntry, current bool) string {
	name := e.name
	if name == "" {
		name = e.mac
	}
	switch {
	case current:
		return name + " (current)"
	case !e.known:
		return name + " — new"
	case e.lastUsed == "":
		return name
	default:
		return name + " — " + humanizeSince(e.lastUsed)
	}
}

func (t *tray) refresh() {
	connected, logging := t.app.state.snapshot()

	switch {
	case !connected:
		systray.SetIcon(imgDisconnected)
	case logging:
		systray.SetIcon(imgRunning)
	default:
		systray.SetIcon(imgConnected)
	}

	// One toggle that reflects the current state: "Stop logging" while logging,
	// otherwise "Start logging". Greyed out when there's no connection to log
	// from (nothing to start or stop).
	switch {
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
	connected, _ := t.app.state.snapshot()
	entries := buildEntries(cfg, t.lastScanned)
	for i, s := range t.switchSlots {
		if i < len(entries) {
			e := entries[i]
			isCur := strings.EqualFold(e.mac, cfg.Current)
			s.SetTitle(slotLabel(e, isCur))
			if isCur {
				s.Disable()
			} else {
				s.Enable()
			}
			// Green dot on the connected device (always the current one);
			// a transparent placeholder on the rest keeps the icon column aligned.
			if isCur && connected {
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
			connected, logging := t.app.state.snapshot()
			if connected {
				t.app.setLogging(!logging)
			}
			t.refresh()
		case <-t.mOpenLogs.ClickedCh:
			go openLogFolder()
		case <-t.mAutoLog.ClickedCh:
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
					log.Printf("[autostart] disable failed: %v", err)
				} else {
					t.mAutostart.Uncheck()
				}
			} else {
				if err := enableAutostart(); err != nil {
					log.Printf("[autostart] enable failed: %v", err)
				} else {
					t.mAutostart.Check()
				}
			}
		case i := <-t.slotClicks:
			mac := t.slotMACs[i]
			name := t.slotNames[i]
			if mac == "" || strings.EqualFold(mac, t.app.currentMAC()) {
				break
			}
			t.app.switchTo(mac, name)
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
