package app

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"tinygo.org/x/bluetooth"
)

// ── constants ────────────────────────────────────────────────────────────────

const (
	// After a healthy session drops, the file keeps the last BPM so a quick
	// reconnect transitions smoothly. If the disconnect lasts longer the file
	// is cleared so stale data isn't shown.
	staleBPMTimeout = 10 * time.Second

	maxSwitchSlots     = 6
	switchScanDuration = 15 * time.Second

	// Reconnection is direct connect-by-address (no scan), so the gap between
	// failed attempts in the persistent phase is just a courtesy pause; BlueZ's
	// own connect attempt already blocks for a while before failing.
	persistentRetryInterval = 5 * time.Second

	// In the persistent phase, log a heartbeat at most this often so a long
	// wait leaves a trace without flooding the log.
	persistLogInterval = 60 * time.Second
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

// App owns what is shared across straps: the adapter, the config, and the
// process-wide signals. Anything tied to one strap lives on a player.
type App struct {
	// players is one entry per slot, indexed by it. Both always exist; P2 simply
	// idles while two-player mode is off, which is cheaper and less racy than
	// starting and stopping its goroutine.
	players []*player

	adapterMu sync.Mutex
	adapter   *bluetooth.Adapter // current adapter; may be re-resolved if it disappears

	scanMu sync.Mutex // serializes BLE scans (only one in flight at a time)

	cfgMu sync.Mutex
	cfg   *Config

	uiUpdates chan struct{}
	stop      chan struct{}
}

// player is one strap: its connection loop, its live state, and its CSV log.
// Each owns a private switchCh so that changing one strap's device does not
// interrupt another's connection.
type player struct {
	app      *App
	slot     int // slotP1 or slotP2; picks this strap's config keys and file names
	state    *AppState
	session  *SessionLogger
	switchCh chan struct{}

	// onReading, if set, is called for every reading this strap receives (before
	// the logging gate and junk filter), used by --print-bpm. It is per-strap so
	// that streaming to stdout stays one unambiguous series even when two are
	// connected; --player picks which. Set once before the worker starts, then
	// read-only.
	onReading func(time.Time, int)

	// assignMu guards the transient assignment, set by something outside the
	// operator's config (the ITGmania module, once that lands) and never
	// persisted, so quitting returns to config.json.
	//
	// driven says whether that outside source is currently assigning this slot.
	// It is what makes an *empty* override mean "idle" rather than "fall back to
	// config", which matters for the side nobody is standing on: without it, a
	// lone player on P2 would have slot 1 quietly connect the configured strap
	// as well.
	//
	// override is the strap this side should follow, meaningful only while
	// driven. claimed is the strap the person on this side says is theirs, read
	// from their game profile; empty means they named nothing. The two are
	// separate because the second gates output rather than selecting a device:
	// see publishes.
	assignMu sync.Mutex
	driven   bool
	override string
	claimed  string

	// connMu guards connMAC, the strap actually delivering readings right now.
	// Distinct from effectiveMAC, which is what this side is *trying* to follow:
	// during a switch the outgoing connection can still deliver a reading or two
	// before its cleanup lands, and attributing those to the incoming player is
	// exactly the mistake the gate exists to prevent.
	connMu  sync.Mutex
	connMAC string
}

// setConnMAC records the strap now delivering readings, or "" once it is gone.
func (p *player) setConnMAC(mac string) {
	p.connMu.Lock()
	p.connMAC = mac
	p.connMu.Unlock()
}

func (p *player) connectedMAC() string {
	p.connMu.Lock()
	defer p.connMu.Unlock()
	return p.connMAC
}

// setClaim records the strap the person on this side says is theirs, read from
// their game profile. Passing "" means they claimed nothing, which opens the
// gate to whatever is configured.
//
// A change can shut the gate, so the panel is blanked at once rather than
// leaving the previous player's reading up for the module's 60s staleness
// window, and the CSV session is broken so the next reading starts a new file
// instead of appending a different person to the last one.
func (p *player) setClaim(mac string) {
	p.assignMu.Lock()
	changed := !strings.EqualFold(p.claimed, mac)
	p.claimed = mac
	p.assignMu.Unlock()
	if !changed {
		return
	}

	p.state.itg.clear()
	p.session.breakSession()
	logInfof("[ITG] P%d claims %s (%s)", p.slot+1, orNone(mac), gateState(p.publishes()))
	p.app.signalUI()
}

// publishes reports whether this strap's readings may be attributed to the
// person on this side. It is the guard against showing one person's heart rate
// as another's, which is otherwise silent and looks like it is working.
//
// A player who claimed no strap gets whatever is configured, which is what lets
// a cabinet keep a house belt for casual players while regulars carry their own.
// A player who claimed one gets it or nothing: falling back to the house belt
// would draw whoever is wearing that, plausibly someone on the next machine.
func (p *player) publishes() bool {
	p.assignMu.Lock()
	claim := p.claimed
	p.assignMu.Unlock()
	if claim == "" {
		return true
	}
	return strings.EqualFold(claim, p.connectedMAC())
}

func gateState(open bool) string {
	if open {
		return "publishing"
	}
	return "not publishing"
}

func newApp(cfg *Config) *App {
	a := &App{
		cfg:       cfg,
		uiUpdates: make(chan struct{}, 1),
		stop:      make(chan struct{}),
	}
	a.players = []*player{newPlayer(a, slotP1, cfg), newPlayer(a, slotP2, cfg)}
	return a
}

func newPlayer(a *App, slot int, cfg *Config) *player {
	p := &player{
		app:      a,
		slot:     slot,
		state:    newAppState(outputPath(slot), cfg.AutoLog), // autostart logging if enabled
		switchCh: make(chan struct{}, 1),
	}
	p.session = newSessionLogger(sessionsDir(), p.sessionSuffix, cfg.sessionGap(), cfg.minBPM(), cfg.AutoLog)
	return p
}

// sessionSuffix names this strap's CSV files. With one strap they are
// unsuffixed, exactly as before two-player support existed; with two, each
// carries -p1/-p2 so a human reading the directory can tell whose is whose.
//
// Note this is not slotSuffix: the machine-read files (gotempo-bpm.txt, hr.txt)
// keep fixed names per slot so an OBS source or a Lua module never sees a path
// move, while these are named for whoever reads the folder.
func (p *player) sessionSuffix() string {
	// Keyed off how many straps are actually being followed, not off the
	// two-player config flag: a profile can drive the second slot with that flag
	// off, and both loggers would then write the same unsuffixed filename and
	// interleave two people's readings into one file.
	active := 0
	for _, q := range p.app.players {
		if q.effectiveMAC() != "" {
			active++
		}
	}
	if active < 2 {
		return ""
	}
	return "-p" + strconv.Itoa(p.slot+1)
}

// setAssignment installs the transient strap assignment and wakes the
// connection loop. driven=false hands the slot back to config; driven=true with
// an empty mac means this side is deliberately idle, which is not the same
// thing.
func (p *player) setAssignment(driven bool, mac string) {
	p.assignMu.Lock()
	changed := p.driven != driven || p.override != mac
	p.driven, p.override = driven, mac
	p.assignMu.Unlock()
	if !changed {
		return
	}
	p.reassigned()
	p.app.signalUI()
}

// effectiveMAC is the strap this slot should be following: the transient
// override when one is set, otherwise the operator's config. Empty means idle,
// which is how P2 sits quiet while two-player mode is off.
func (p *player) effectiveMAC() string {
	p.assignMu.Lock()
	driven, override := p.driven, p.override
	p.assignMu.Unlock()
	if driven {
		return override // authoritative, empty included
	}
	return p.currentMAC()
}

// p1 is the first strap, for the places that mean it specifically rather than
// "some strap": status.json's top-level fields, the single-strap tray click
// path, and reading the logging flag, which is process-wide and which every
// player holds the same value of.
func (a *App) p1() *player { return a.players[slotP1] }

// attachITG resolves the overlay target for every strap from the one configured
// module path. Each writes its own file beside gotempo.lua: hr.txt and
// hr-p2.txt. A missing or wrong module disables the overlay for all of them.
func (a *App) attachITG(module string) {
	base := setupITG(module, slotP1) // validates, and logs why a bad path is rejected
	for _, p := range a.players {
		w := base
		if p.slot != slotP1 {
			w = base.sibling(module, p.slot)
		}
		p.state.attachITG(w)

		// Announce only files that will actually be written. An idle slot has a
		// resolved path but never publishes to it, and saying otherwise sends
		// someone looking for an hr-p2.txt that will never appear.
		if t := w.target(); t != "" && p.effectiveMAC() != "" {
			logInfof("[ITG] writing %s", t)
		}
	}
}

// anyDeviceConfigured reports whether any slot has a strap to follow. Headless
// runs use it: a cabinet set up with only P2 is unusual but valid, so the check
// must not assume the first slot is the populated one.
func (a *App) anyDeviceConfigured() bool {
	for _, p := range a.players {
		if p.effectiveMAC() != "" {
			return true
		}
	}
	return false
}

// isConnected reports whether this strap has a live connection.
func (p *player) isConnected() bool {
	connected, _ := p.state.snapshot()
	return connected
}

// anyConnected reports whether any strap is connected. The tray has one icon
// for the whole app, so one live strap is enough to show as connected.
func (a *App) anyConnected() bool {
	for _, p := range a.players {
		if p.isConnected() {
			return true
		}
	}
	return false
}

// setTwoPlayer turns the second strap on or off, persists it, and wakes P2's
// loop so it picks up or drops its strap. P1 is deliberately untouched: toggling
// the mode must never interrupt a session already running on the first strap.
func (a *App) setTwoPlayer(v bool) {
	a.cfgMu.Lock()
	if a.cfg.TwoPlayer == v {
		a.cfgMu.Unlock()
		return
	}
	a.cfg.TwoPlayer = v
	snap := a.cfg.clone()
	a.cfgMu.Unlock()
	if err := saveConfig(snap); err != nil {
		logErrf("config save: %v", err)
	}

	a.players[slotP2].reassigned()
	a.signalUI()
	logInfof("[BLE] two-player mode %s", onOff(v))
}

// assignSlots applies a complete slot assignment: both slots at once, persisted
// once, waking only the loops whose strap actually changed. The tray's cycling
// can move a strap into a slot and displace another in the same click, so this
// has to be one operation rather than two switchTo calls.
//
// learned is the strap the user just acted on, recorded in Known so its name
// survives a rescan. The other slot's strap is already known.
func (a *App) assignSlots(macs [2]string, learned KnownDevice) {
	a.cfgMu.Lock()
	var old [2]string
	for slot := range macs {
		old[slot] = a.cfg.currentFor(slot)
		a.cfg.setCurrentFor(slot, macs[slot])
	}
	if learned.MAC != "" {
		a.cfg.upsert(learned.MAC, learned.Name)
	}
	snap := a.cfg.clone()
	a.cfgMu.Unlock()
	if err := saveConfig(snap); err != nil {
		logErrf("config save: %v", err)
	}

	for slot, p := range a.players {
		if strings.EqualFold(old[slot], macs[slot]) {
			continue
		}
		p.reassigned()
		logInfof("[BLE] P%d is now %s", slot+1, orNone(macs[slot]))
	}
	a.signalUI()
}

// reassigned drops whatever this strap was doing and wakes its loop to pick up
// its new device, or to idle when it no longer has one.
func (p *player) reassigned() {
	p.state.onSwitch()
	if p.effectiveMAC() == "" {
		p.setPhase(phaseIdle)
	} else {
		p.setPhase(phaseConnecting)
	}
	p.signalSwitch()
}

func onOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

// startWorkers launches one BLE connection loop per strap.
func (a *App) startWorkers() {
	for _, p := range a.players {
		go p.runBLE()
	}
}

// closeSessions ends every open CSV session at shutdown, releasing the handles.
// The files are already complete; see SessionLogger.Close.
func (a *App) closeSessions() {
	for _, p := range a.players {
		p.session.Close()
	}
}

// setLogging toggles BPM logging. setEnabled closes the CSV session on off and
// gates LogReading under the session mutex, so a reading racing the toggle can't
// reopen a session after it; turning it on lets the next valid reading open or
// resume one per the gap rule. Turning it off also clears the OBS overlay file
// at once, so it doesn't freeze on the last value (handleBPM only writes it while
// logging is on).
func (a *App) setLogging(v bool) {
	for _, p := range a.players {
		p.state.setLogging(v)
		p.session.setEnabled(v)
		if !v {
			p.state.clearOutput()
		}
	}
	a.publishStatus() // reflect the new logging state at once
}

// publishStatus writes the current status (connection, phase, logging, bpm,
// device) to status.json for `gotempo --status` to read. Assembled from the live
// AppState plus the configured device.
func (a *App) publishStatus() {
	p := a.p1()
	connected, logging, phase, bpm := p.state.statusView()
	st := appStatus{
		Connected: connected,
		Phase:     phase,
		Logging:   logging,
		BPM:       bpm,
		Device:    p.statusDevice(),
		ITGmania:  p.state.itg.target(),
	}

	// The second strap is reported only when it is in use, so a single-strap
	// status.json keeps exactly the shape it has always had.
	if p2 := a.players[slotP2]; p2.effectiveMAC() != "" {
		connected, _, phase, bpm := p2.state.statusView()
		st.Player2 = &playerStatus{
			Connected: connected,
			Phase:     phase,
			BPM:       bpm,
			Device:    p2.statusDevice(),
			ITGmania:  p2.state.itg.target(),
		}
	}
	writeStatus(st)
}

// statusDevice is this strap's device for status.json, nil when none is set.
func (p *player) statusDevice() *statusDevice {
	mac, name := p.currentDevice()
	if mac == "" {
		return nil
	}
	return &statusDevice{MAC: mac, Name: name}
}

// setPhase records a connection-phase transition and publishes it.
func (p *player) setPhase(phase string) {
	p.state.setPhase(phase)
	p.app.publishStatus()
}

// recordBPM stores the latest reading (regardless of logging) and publishes it.
func (p *player) recordBPM(bpm int) {
	p.state.recordBPM(bpm)
	p.app.publishStatus()
}

// currentDevice returns the configured device's MAC and (if known) its name.
func (p *player) currentDevice() (mac, name string) {
	mac = p.effectiveMAC()
	if mac == "" {
		return "", ""
	}
	a := p.app
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	for _, k := range a.cfg.Known {
		if strings.EqualFold(k.MAC, mac) {
			return mac, k.Name
		}
	}
	return mac, ""
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
			for _, p := range a.players {
				p.session.checkGap(now)
				p.session.Flush()
			}
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
		logErrf("config save: %v", err)
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
		logInfof("[BLE] using adapter %s", label)
	} else {
		logInfof("[BLE] adapter changed to %s", label)
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

func (p *player) signalSwitch() {
	select {
	case p.switchCh <- struct{}{}:
	default:
	}
}

func (p *player) currentMAC() string {
	a := p.app
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	return a.cfg.currentFor(p.slot)
}

func (a *App) snapshotConfig() Config {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	return a.cfg.clone()
}

// switchTo changes the active device, persists the config, and wakes the BLE
// worker so it reconnects against the new MAC.
func (p *player) switchTo(mac, name string) {
	a := p.app
	a.cfgMu.Lock()
	a.cfg.setCurrentFor(p.slot, mac)
	if mac != "" { // "" unassigns the slot; don't record it as a device
		a.cfg.upsert(mac, name)
	}
	snap := a.cfg.clone()
	a.cfgMu.Unlock()
	if err := saveConfig(snap); err != nil {
		logErrf("config save: %v", err)
	}

	p.state.onSwitch()
	p.setPhase(phaseConnecting) // new device; worker will reconnect
	p.signalSwitch()
	a.signalUI()
	logInfof("[BLE] switching to %s (%s)", name, mac)
}

// markConnected records the connection unless this slot is being driven from
// outside. A visiting player's strap must not end up in the cabinet's config:
// ITGmania never writes there, and the tray's device list would otherwise fill
// with straps belonging to people who have gone home.
func (p *player) markConnected(mac string) {
	p.assignMu.Lock()
	driven := p.driven
	p.assignMu.Unlock()
	if driven {
		return
	}
	p.app.markConnected(mac)
}

// markConnected records a successful connection's timestamp and persists it.
func (a *App) markConnected(mac string) {
	a.cfgMu.Lock()
	a.cfg.touch(mac)
	snap := a.cfg.clone()
	a.cfgMu.Unlock()
	if err := saveConfig(snap); err != nil {
		logErrf("config save: %v", err)
	}
}

func (p *player) handleBPM(bpm int) {
	now := time.Now()
	logDebugf("[BPM] reading %d", bpm)

	// Raw output stream (--print-bpm): every reading as received, independent of
	// the logging toggle and junk filter.
	if p.onReading != nil {
		p.onReading(now, bpm)
	}

	// Publish live status (bpm) regardless of logging, so --status and external
	// pollers see the real reading even when logging is off.
	p.recordBPM(bpm)

	// Everything below publishes this reading *as this side's player's*, so it
	// stops here when the connected strap is not the one they claimed. --print-bpm
	// and status.json above are exempt: both name their strap explicitly and are
	// diagnostic rather than attributed.
	if !p.publishes() {
		return
	}

	// ITGmania overlay: every reading, undeduped and ungated by logging, because
	// the timestamp in the line is what tells the module the strap is still live.
	// See itgmania.go.
	p.state.itg.write(bpm, now)

	p.state.mu.Lock()
	logging := p.state.logging
	p.state.mu.Unlock()
	if !logging {
		return
	}

	// CSV session log: every valid reading at full cadence (no dedup), so the
	// file keeps a row per second. Junk is filtered inside LogReading.
	if err := p.session.LogReading(now, bpm); err != nil {
		logErrf("[CSV] %v", err)
	}

	// OBS overlay file: deduped to the last distinct value, with its own
	// stale-hold/clear lifecycle (onDisconnect/onSwitch). Independent of CSV.
	p.state.mu.Lock()
	if p.state.hasBPM && p.state.lastBPM == bpm {
		p.state.mu.Unlock()
		return
	}
	p.state.lastBPM = bpm
	p.state.hasBPM = true
	p.state.mu.Unlock()

	p.state.putOut([]byte(strconv.Itoa(bpm)), "write")
}

// ── scanning ─────────────────────────────────────────────────────────────────

// scanDevices returns the distinct heart-rate monitors available to pick: the
// ones the OS already has paired, plus whatever a blocking BLE scan of the given
// duration turns up. Every discovery path (the tray's Rescan, --list-devices and
// --select-device) goes through here, so they all agree.
func (a *App) scanDevices(d time.Duration) []KnownDevice {
	a.scanMu.Lock()
	defer a.scanMu.Unlock()

	adapter, err := a.ensureAdapter()
	if err != nil {
		logErrln("[BLE] scan skipped:", err)
		return nil
	}

	seen := map[string]KnownDevice{}

	// Devices the OS already has paired come first, and seed the map so a later
	// advertisement cannot overwrite the friendlier name they carry. On Windows
	// this is the only way a bonded strap turns up at all, since it stops
	// advertising once bonded; on Linux the list is empty and nothing changes.
	// See paired.go.
	for _, p := range pairedDevices() {
		seen[p.MAC] = p
	}

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
		logErrln("[BLE] scan error:", err)
	}

	out := make([]KnownDevice, 0, len(seen))
	for _, k := range seen {
		out = append(out, k)
	}
	return out
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
func (p *player) runBLE() {
	for {
		select {
		case <-p.app.stop:
			return
		default:
		}

		mac := p.effectiveMAC()
		if mac == "" {
			// No device chosen yet — idle until one is picked from the tray.
			p.setPhase(phaseIdle)
			select {
			case <-p.app.stop:
				return
			case <-p.switchCh:
				continue
			}
		}
		parsed, err := bluetooth.ParseMAC(mac)
		if err != nil {
			logErrln("[BLE] invalid mac:", err)
			select {
			case <-p.app.stop:
				return
			case <-p.switchCh:
				continue
			}
		}
		addr := bluetooth.Address{MACAddress: bluetooth.MACAddress{MAC: parsed}}

		if errors.Is(p.connectLoop(addr), errStopped) {
			return
		}
		// errSwitched → fall through, re-read MAC.
	}
}

// connectLoop runs the reconnection state machine for a single device address.
// It retries silently through the finite schedule (5×3s, then 5×10s); a
// reconnect during that phase is silent. When the finite schedule exhausts it
// sends a single "device lost" notification and then retries connect-by-address
// until the device reappears. A reconnect during that phase notifies
// "reconnected" (via connectAndMonitor) and resets the schedule. It never gives
// up; it returns only errStopped or errSwitched. No phase scans, so reconnection
// never probes other devices in range.
func (p *player) connectLoop(addr bluetooth.Address) error {
	schedule := makeSchedule()
	attempt := 0
	notifiedLoss := false

	for {
		select {
		case <-p.app.stop:
			return errStopped
		case <-p.switchCh:
			return errSwitched
		default:
		}

		if attempt < len(schedule) {
			err := p.connectOnce(addr, notifiedLoss)
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
				p.state.onDisconnect()
			} else {
				logErrf("[BLE] connect failed: %s", describeConnectErr(err))
				attempt++
			}
			p.app.signalUI()

			if attempt < len(schedule) {
				select {
				case <-p.app.stop:
					return errStopped
				case <-p.switchCh:
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
		return p.persistentConnect(addr, notifiedLoss)
	}
}

// connectOnce connects directly to the device by address, with no scan. Used
// during the finite schedule phase. A direct connect targets only the peer
// address, so it never emits scan-request probes to other devices in range. It
// returns errStopped, errSwitched, errSessionDropped, or a raw connect/discovery
// error.
func (p *player) connectOnce(addr bluetooth.Address, wasNotified bool) error {
	p.setPhase(phaseConnecting)
	adapter, err := p.app.ensureAdapter()
	if err != nil {
		return err
	}
	return p.connectAndMonitor(adapter, addr, wasNotified)
}

// persistentConnect retries a direct connect-by-address until the device comes
// back, then monitors the session; on a drop it goes straight back to retrying.
// It never scans, so it emits no scan-request probes to other devices while the
// strap is away (the H10 is off most of the time it isn't worn). BlueZ must know
// the device for connect-by-address to work; a bonded strap qualifies, and the
// device is established by the user's tray pick / Rescan / --select-device, never
// by a background scan. It returns errStopped or errSwitched only.
func (p *player) persistentConnect(addr bluetooth.Address, wasNotified bool) error {
	start := time.Now()
	var lastLog time.Time // zero value forces a log on the first round
	for {
		select {
		case <-p.app.stop:
			return errStopped
		case <-p.switchCh:
			return errSwitched
		default:
		}

		p.setPhase(phaseReconnecting)
		if time.Since(lastLog) >= persistLogInterval {
			logInfof("[BLE] reconnecting to %s (%s elapsed)", addr.MAC.String(), time.Since(start).Round(time.Second))
			lastLog = time.Now()
		}

		adapter, err := p.app.ensureAdapter()
		if err != nil {
			logErrf("[BLE] %v", err)
		} else if err = p.connectAndMonitor(adapter, addr, wasNotified); errors.Is(err, errStopped) || errors.Is(err, errSwitched) {
			return err
		} else if errors.Is(err, errSessionDropped) {
			// connectAndMonitor logs the session length on drop; flip state and
			// retry immediately so a brief blip reconnects fast.
			p.state.onDisconnect()
			p.app.signalUI()
			wasNotified = false
			start = time.Now()
			lastLog = time.Time{}
			continue
		} else if err != nil {
			// Expected while the strap is away (connect aborts/times out).
			logErrf("[BLE] connect failed: %s", describeConnectErr(err))
		}

		select {
		case <-p.app.stop:
			return errStopped
		case <-p.switchCh:
			return errSwitched
		case <-time.After(persistentRetryInterval):
		}
	}
}

// connectDevice attempts a direct connect-by-address and returns once connected,
// failed, or the app stops/switches. adapter.Connect targets only the peer
// address (no scan, no probes to other devices) but can block while BlueZ waits
// on the connection attempt, so it runs in a goroutine and is abandoned on
// stop/switch. An abandoned attempt that later connects is disconnected so it
// does not hold the device's single BLE slot.
func (p *player) connectDevice(adapter *bluetooth.Adapter, addr bluetooth.Address) (bluetooth.Device, error) {
	type result struct {
		dev bluetooth.Device
		err error
	}
	ch := make(chan result, 1)
	go func() {
		dev, err := adapter.Connect(addr, bluetooth.ConnectionParams{})
		ch <- result{dev, err}
	}()
	abandon := func(sentinel error) (bluetooth.Device, error) {
		go func() {
			if r := <-ch; r.err == nil {
				_ = r.dev.Disconnect()
			}
		}()
		return bluetooth.Device{}, sentinel
	}
	select {
	case <-p.app.stop:
		return abandon(errStopped)
	case <-p.switchCh:
		return abandon(errSwitched)
	case r := <-ch:
		return r.dev, r.err
	}
}

// connectAndMonitor connects to the device by address, discovers the HR service
// and characteristic, enables notifications, and blocks until the session ends
// or the app stops/switches. It always returns a sentinel error: errStopped,
// errSwitched, errSessionDropped, or a raw connect/discovery error.
func (p *player) connectAndMonitor(adapter *bluetooth.Adapter, addr bluetooth.Address, wasNotified bool) error {
	logInfof("[BLE] connecting to %s…", addr.MAC.String())
	device, err := p.connectDevice(adapter, addr)
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
		p.handleBPM(bpm)
	}); err != nil {
		_ = device.Disconnect()
		return err
	}

	logInfoln("[BLE] connected")
	connectedAt := time.Now()
	p.setConnMAC(addr.MAC.String())
	p.state.onConnect()
	p.setPhase(phaseConnected)
	p.markConnected(addr.MAC.String())
	if wasNotified {
		notify("reconnected")
	}
	p.app.signalUI()

	cleanup := func() {
		p.setConnMAC("")
		_ = chars[0].EnableNotifications(nil)
		_ = device.Disconnect()
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-p.app.stop:
			cleanup()
			return errStopped
		case <-p.switchCh:
			cleanup()
			return errSwitched
		case <-ticker.C:
			connected, err := device.Connected()
			if err != nil || !connected {
				cleanup()
				logInfof("[BLE] session ended after %s; reconnecting", time.Since(connectedAt).Round(time.Second))
				return errSessionDropped
			}
		}
	}
}
