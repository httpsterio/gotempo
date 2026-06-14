package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"tinygo.org/x/bluetooth"
)

// ── constants ────────────────────────────────────────────────────────────────

const (
	connectScanTimeout = 10 * time.Second

	// After a healthy session drops, the file keeps the last BPM so a quick
	// reconnect transitions smoothly. If the disconnect lasts longer the file
	// is cleared so stale data isn't shown.
	staleBPMTimeout = 10 * time.Second

	maxSwitchSlots     = 6
	switchScanDuration = 15 * time.Second

	// In the persistent phase, log a heartbeat at most this often so a long
	// wait leaves a trace without flooding the log.
	persistScanLogInterval = 60 * time.Second
)

// retrySchedule is the finite, silent reconnection phase: 5×3s then 5×10s.
var retrySchedule = []struct {
	count    int
	interval time.Duration
}{
	{5, 3 * time.Second},
	{5, 10 * time.Second},
}

var (
	hrServiceUUID = bluetooth.New16BitUUID(0x180D)
	hrCharUUID    = bluetooth.New16BitUUID(0x2A37)
)

// Sentinel errors flowing out of the connection loop.
var (
	errStopped        = errors.New("stopped")
	errSwitched       = errors.New("switched")
	errSessionDropped = errors.New("session_dropped")
)

// describeConnectErr maps a raw BLE/BlueZ error into a short, human-readable
// reason. The raw error is returned verbatim if no specific case matches. The
// matched strings are BlueZ-specific; on other platforms the unmatched raw
// error falls through, which is fine.
func describeConnectErr(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "le-connection-abort-by-local"):
		return "device unreachable — sensor asleep, out of range, or electrodes dry"
	case strings.Contains(msg, "br-connection-page-timeout"),
		strings.Contains(msg, "Connection refused"):
		return "device did not respond"
	case strings.Contains(msg, "Software caused connection abort"):
		return "connection aborted by adapter"
	case strings.Contains(msg, "Operation already in progress"):
		return "another connection attempt is still in flight"
	case strings.Contains(msg, "not available"),
		strings.Contains(msg, "does not exist"):
		return "device not known to BlueZ — try `bluetoothctl scan on` once"
	case strings.Contains(msg, "could not find some services"):
		return "device does not expose the heart-rate service"
	case strings.Contains(msg, "could not find some characteristics"):
		return "device does not expose the heart-rate characteristic"
	case strings.Contains(msg, "timeout on DiscoverServices"):
		return "service discovery timed out"
	default:
		return msg
	}
}

// ── app ──────────────────────────────────────────────────────────────────────

type App struct {
	state   *AppState
	session *SessionLogger

	adapterMu sync.Mutex
	adapter   *bluetooth.Adapter // current adapter; may be re-resolved if it disappears

	scanMu sync.Mutex // serializes BLE scans (only one in flight at a time)

	cfgMu sync.Mutex
	cfg   *Config

	uiUpdates chan struct{}
	stop      chan struct{}
	switchCh  chan struct{}
}

func newApp(cfg *Config) *App {
	return &App{
		state:     &AppState{logging: cfg.AutoLog}, // autostart logging if enabled
		session:   newSessionLogger(sessionsDir(), cfg.sessionGap(), cfg.minBPM(), cfg.AutoLog),
		cfg:       cfg,
		uiUpdates: make(chan struct{}, 1),
		stop:      make(chan struct{}),
		switchCh:  make(chan struct{}, 1),
	}
}

// setLogging toggles BPM logging. setEnabled closes the CSV session on off and
// gates LogReading under the session mutex, so a reading racing the toggle can't
// reopen a session after it; turning it on lets the next valid reading open or
// resume one per the gap rule. The OBS overlay file is handled separately in
// handleBPM.
func (a *App) setLogging(v bool) {
	a.state.setLogging(v)
	a.session.setEnabled(v)
}

// gapCheckLoop runs the session's periodic upkeep: checkGap closes an idle
// session when no readings arrive at all (a dead connection), and Flush fsyncs
// the open session so power-loss exposure is bounded to one tick rather than
// fsyncing every row.
func (a *App) gapCheckLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-a.stop:
			return
		case now := <-ticker.C:
			a.session.checkGap(now)
			a.session.Flush()
		}
	}
}

// setAutoLog persists the autostart-logging preference (without changing the
// current logging state).
func (a *App) setAutoLog(v bool) {
	a.cfgMu.Lock()
	a.cfg.AutoLog = v
	snap := a.cfg.clone()
	a.cfgMu.Unlock()
	if err := saveConfig(snap); err != nil {
		log.Printf("config save: %v", err)
	}
}

// ensureAdapter returns a usable, enabled adapter. It reuses the current one if
// it is still alive, otherwise it asks the platform's openAdapter to resolve a
// fresh one. The adapter can change when the user power-cycles Bluetooth, so
// this must be re-checked before every scan/connect rather than resolved once
// at startup.
func (a *App) ensureAdapter() (*bluetooth.Adapter, error) {
	a.adapterMu.Lock()
	defer a.adapterMu.Unlock()

	if a.adapter != nil {
		if err := a.adapter.Enable(); err == nil {
			return a.adapter, nil
		}
	}
	cand, label, err := openAdapter()
	if err != nil {
		return nil, err
	}
	if a.adapter == nil {
		log.Printf("[BLE] using adapter %s", label)
	} else {
		log.Printf("[BLE] adapter changed to %s", label)
	}
	a.adapter = cand
	return cand, nil
}

func (a *App) signalUI() {
	select {
	case a.uiUpdates <- struct{}{}:
	default:
	}
}

func (a *App) signalSwitch() {
	select {
	case a.switchCh <- struct{}{}:
	default:
	}
}

func (a *App) currentMAC() string {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	return a.cfg.Current
}

func (a *App) snapshotConfig() Config {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	return a.cfg.clone()
}

// switchTo changes the active device, persists the config, and wakes the BLE
// worker so it reconnects against the new MAC.
func (a *App) switchTo(mac, name string) {
	a.cfgMu.Lock()
	a.cfg.Current = mac
	a.cfg.upsert(mac, name)
	snap := a.cfg.clone()
	a.cfgMu.Unlock()
	if err := saveConfig(snap); err != nil {
		log.Printf("config save: %v", err)
	}

	a.state.onSwitch()
	a.signalSwitch()
	a.signalUI()
	log.Printf("[BLE] switching to %s (%s)", name, mac)
}

// markConnected records a successful connection's timestamp and persists it.
func (a *App) markConnected(mac string) {
	a.cfgMu.Lock()
	a.cfg.touch(mac)
	snap := a.cfg.clone()
	a.cfgMu.Unlock()
	if err := saveConfig(snap); err != nil {
		log.Printf("config save: %v", err)
	}
}

func (a *App) handleBPM(bpm int) {
	a.state.mu.Lock()
	logging := a.state.logging
	a.state.mu.Unlock()
	if !logging {
		return
	}

	// CSV session log: every valid reading at full cadence (no dedup), so the
	// file keeps a row per second. Junk is filtered inside LogReading.
	if err := a.session.LogReading(time.Now(), bpm); err != nil {
		log.Printf("[CSV] %v", err)
	}

	// OBS overlay file: deduped to the last distinct value, with its own
	// stale-hold/clear lifecycle (onDisconnect/onSwitch). Independent of CSV.
	a.state.mu.Lock()
	if a.state.hasBPM && a.state.lastBPM == bpm {
		a.state.mu.Unlock()
		return
	}
	a.state.lastBPM = bpm
	a.state.hasBPM = true
	a.state.mu.Unlock()

	if err := os.WriteFile(outputPath(), []byte(strconv.Itoa(bpm)), 0644); err != nil {
		log.Printf("[BPM] could not write output: %v", err)
	}
}

// ── scanning ─────────────────────────────────────────────────────────────────

// scanDevices runs a blocking BLE scan for the given duration and returns the
// distinct heart-rate monitors seen.
func (a *App) scanDevices(d time.Duration) []KnownDevice {
	a.scanMu.Lock()
	defer a.scanMu.Unlock()

	adapter, err := a.ensureAdapter()
	if err != nil {
		log.Println("[BLE] scan skipped:", err)
		return nil
	}

	seen := map[string]KnownDevice{}
	var stopOnce sync.Once
	stop := func() { stopOnce.Do(func() { _ = adapter.StopScan() }) }
	timer := time.AfterFunc(d, stop)
	defer timer.Stop()

	if err := adapter.Scan(func(_ *bluetooth.Adapter, r bluetooth.ScanResult) {
		if !r.HasServiceUUID(hrServiceUUID) {
			return
		}
		mac := r.Address.MAC.String()
		if _, ok := seen[mac]; ok {
			return
		}
		seen[mac] = KnownDevice{MAC: mac, Name: r.LocalName()}
	}); err != nil {
		log.Println("[BLE] scan error:", err)
	}

	out := make([]KnownDevice, 0, len(seen))
	for _, k := range seen {
		out = append(out, k)
	}
	return out
}

// findDevice scans until the target MAC is seen (or the timeout elapses). This
// re-populates BlueZ's device cache so a subsequent connect-by-address works,
// which is required after the adapter has been power-cycled.
func (a *App) findDevice(target string, d time.Duration) (*bluetooth.Adapter, bool) {
	a.scanMu.Lock()
	defer a.scanMu.Unlock()

	adapter, err := a.ensureAdapter()
	if err != nil {
		log.Println("[BLE] scan skipped:", err)
		return nil, false
	}

	found := false
	// Both the timeout and a match stop the scan. The library's StopScan isn't
	// safe to call concurrently (it closes an unsynchronized channel), so funnel
	// both callers through a sync.Once to avoid a double-close panic if a match
	// lands at the same moment the timeout fires.
	var stopOnce sync.Once
	stop := func() { stopOnce.Do(func() { _ = adapter.StopScan() }) }
	timer := time.AfterFunc(d, stop)
	defer timer.Stop()

	if err := adapter.Scan(func(_ *bluetooth.Adapter, r bluetooth.ScanResult) {
		if strings.EqualFold(r.Address.MAC.String(), target) {
			found = true
			stop()
		}
	}); err != nil {
		log.Println("[BLE] scan error:", err)
		return adapter, found
	}
	return adapter, found
}

// ── BLE loop ─────────────────────────────────────────────────────────────────

func makeSchedule() []time.Duration {
	var s []time.Duration
	for _, p := range retrySchedule {
		for i := 0; i < p.count; i++ {
			s = append(s, p.interval)
		}
	}
	return s
}

// runBLE is the top-level worker. It (re-)reads the current device on every
// outer iteration so a switch simply causes the inner loop to return and the
// new MAC to be picked up.
func (a *App) runBLE() {
	for {
		select {
		case <-a.stop:
			return
		default:
		}

		mac := a.currentMAC()
		if mac == "" {
			// No device chosen yet — idle until one is picked from the tray.
			select {
			case <-a.stop:
				return
			case <-a.switchCh:
				continue
			}
		}
		parsed, err := bluetooth.ParseMAC(mac)
		if err != nil {
			log.Println("[BLE] invalid mac:", err)
			select {
			case <-a.stop:
				return
			case <-a.switchCh:
				continue
			}
		}
		addr := bluetooth.Address{MACAddress: bluetooth.MACAddress{MAC: parsed}}

		if errors.Is(a.connectLoop(addr), errStopped) {
			return
		}
		// errSwitched → fall through, re-read MAC.
	}
}

// connectLoop runs the reconnection state machine for a single device address.
// It retries silently through the finite schedule (5×3s, then 5×10s); a
// reconnect during that phase is silent. When the finite schedule exhausts it
// sends a single "device lost" notification and then scans continuously until
// the device reappears. A reconnect during that phase notifies "reconnected"
// (via connectAndMonitor) and resets the schedule. It never gives up; it returns
// only errStopped or errSwitched.
func (a *App) connectLoop(addr bluetooth.Address) error {
	schedule := makeSchedule()
	attempt := 0
	notifiedLoss := false

	for {
		select {
		case <-a.stop:
			return errStopped
		case <-a.switchCh:
			return errSwitched
		default:
		}

		if attempt < len(schedule) {
			err := a.connectOnce(addr, notifiedLoss)
			if errors.Is(err, errStopped) {
				return errStopped
			}
			if errors.Is(err, errSwitched) {
				return errSwitched
			}

			if errors.Is(err, errSessionDropped) {
				// connectAndMonitor logs the session length on drop.
				attempt = 0
				notifiedLoss = false
				a.state.onDisconnect()
			} else {
				log.Printf("[BLE] connect failed: %s", describeConnectErr(err))
				attempt++
			}
			a.signalUI()

			if attempt < len(schedule) {
				select {
				case <-a.stop:
					return errStopped
				case <-a.switchCh:
					return errSwitched
				case <-time.After(schedule[attempt]):
				}
				continue
			}
			// Schedule exhausted; fall through to persistent phase.
		}

		// Persistent phase: scan continuously until the device reappears.
		// persistentConnect only returns errStopped or errSwitched.
		if !notifiedLoss {
			notify("device lost")
			notifiedLoss = true
		}
		return a.persistentConnect(addr, notifiedLoss)
	}
}

// connectOnce scans for the device (with a timeout) then connects. Used during
// the finite schedule phase. It returns errStopped, errSwitched, errSessionDropped,
// or a raw connect/discovery error.
func (a *App) connectOnce(addr bluetooth.Address, wasNotified bool) error {
	log.Printf("[BLE] scanning for %s…", addr.MAC.String())
	adapter, ok := a.findDevice(addr.MAC.String(), connectScanTimeout)
	if adapter == nil {
		return errors.New("no bluetooth adapter available")
	}
	if !ok {
		return fmt.Errorf("device %s not found in scan", addr.MAC.String())
	}
	return a.connectAndMonitor(adapter, addr, wasNotified)
}

// persistScan repeatedly scans (one bounded round at a time) until the target
// device is seen or the app is stopped/switched. Holding scanMu only per round
// lets the tray's own scans (Rescan) interleave instead of blocking.
func (a *App) persistScan(target string) (*bluetooth.Adapter, error) {
	start := time.Now()
	var lastLog time.Time // zero value forces a log on the first round
	for {
		select {
		case <-a.stop:
			return nil, errStopped
		case <-a.switchCh:
			return nil, errSwitched
		default:
		}

		if time.Since(lastLog) >= persistScanLogInterval {
			log.Printf("[BLE] still scanning for %s (%s elapsed)", target, time.Since(start).Round(time.Second))
			lastLog = time.Now()
		}

		if adapter, found := a.findDevice(target, connectScanTimeout); found {
			log.Printf("[BLE] %s back in range after %s", target, time.Since(start).Round(time.Second))
			return adapter, nil
		}

		select {
		case <-a.stop:
			return nil, errStopped
		case <-a.switchCh:
			return nil, errSwitched
		case <-time.After(1 * time.Second):
		}
	}
}

// persistentConnect runs a continuous scan for the device; when it appears,
// connects and monitors the session. On session drop or connection failure it
// returns to scanning immediately. It returns errStopped or errSwitched when
// the application shuts down or switches devices.
func (a *App) persistentConnect(addr bluetooth.Address, wasNotified bool) error {
	for {
		adapter, err := a.persistScan(addr.MAC.String())
		if err != nil {
			return err
		}

		err = a.connectAndMonitor(adapter, addr, wasNotified)
		if errors.Is(err, errStopped) || errors.Is(err, errSwitched) {
			return err
		}
		if errors.Is(err, errSessionDropped) {
			// connectAndMonitor logs the session length on drop; flip state.
			a.state.onDisconnect()
			a.signalUI()
			wasNotified = false
		} else if err != nil {
			// A connect/discovery error here was previously swallowed, leaving
			// the persistent phase silent. Surface it before rescanning.
			log.Printf("[BLE] connect failed: %s", describeConnectErr(err))
		}
	}
}

// connectAndMonitor connects to a recently-scanned device, discovers the HR
// service and characteristic, enables notifications, and blocks until the
// session ends or the app stops/switches. It always returns a sentinel error:
// errStopped, errSwitched, errSessionDropped, or a raw connect/discovery error.
func (a *App) connectAndMonitor(adapter *bluetooth.Adapter, addr bluetooth.Address, wasNotified bool) error {
	log.Printf("[BLE] connecting to %s…", addr.MAC.String())
	device, err := adapter.Connect(addr, bluetooth.ConnectionParams{})
	if err != nil {
		return err
	}

	services, err := device.DiscoverServices([]bluetooth.UUID{hrServiceUUID})
	if err != nil {
		_ = device.Disconnect()
		return err
	}
	if len(services) == 0 {
		_ = device.Disconnect()
		return fmt.Errorf("HR service not found")
	}

	chars, err := services[0].DiscoverCharacteristics([]bluetooth.UUID{hrCharUUID})
	if err != nil {
		_ = device.Disconnect()
		return err
	}
	if len(chars) == 0 {
		_ = device.Disconnect()
		return fmt.Errorf("HR characteristic not found")
	}

	if err := chars[0].EnableNotifications(func(buf []byte) {
		if len(buf) < 2 {
			return
		}
		flags := buf[0]
		var bpm int
		if flags&0x01 != 0 {
			if len(buf) < 3 {
				return
			}
			bpm = int(buf[1]) | int(buf[2])<<8
		} else {
			bpm = int(buf[1])
		}
		a.handleBPM(bpm)
	}); err != nil {
		_ = device.Disconnect()
		return err
	}

	log.Println("[BLE] connected")
	connectedAt := time.Now()
	a.state.onConnect()
	a.markConnected(addr.MAC.String())
	if wasNotified {
		notify("reconnected")
	}
	a.signalUI()

	cleanup := func() {
		_ = chars[0].EnableNotifications(nil)
		_ = device.Disconnect()
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-a.stop:
			cleanup()
			return errStopped
		case <-a.switchCh:
			cleanup()
			return errSwitched
		case <-ticker.C:
			connected, err := device.Connected()
			if err != nil || !connected {
				cleanup()
				log.Printf("[BLE] session ended after %s; reconnecting", time.Since(connectedAt).Round(time.Second))
				return errSessionDropped
			}
		}
	}
}
