package app

import (
	"errors"
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
	errRetired        = errors.New("retired")
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

	// straps is the connection pool, keyed by upper-case MAC, plus the slot
	// pointers into it. One mutex covers both: every change is a swap between
	// them, and splitting the two would let a slot point at a strap that has
	// just been evicted. started gates launching connection goroutines, so a
	// strap built before the workers start (the ITGmania profile follower runs
	// one pass first, to avoid connecting the configured strap and dropping it
	// a second later) waits for startWorkers instead of racing it.
	strapMu sync.Mutex
	straps  map[string]*strap
	started bool

	cfgMu sync.Mutex
	cfg   *Config

	uiUpdates chan struct{}
	stop      chan struct{}
}

// player is one slot: its live state, its CSV log, and a subscription to the
// strap it is currently following. It does not own a connection. See strap.go
// for why those are pooled instead.
type player struct {
	app     *App
	slot    int // slotP1 or slotP2; picks this slot's config keys and file names
	state   *AppState
	session *SessionLogger

	// strap is the belt this slot is listening to, nil when idle. Guarded by
	// App.strapMu along with the pool itself, never by assignMu: this is the
	// result of an assignment, not part of one.
	strap *strap

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
		app:   a,
		slot:  slot,
		state: newAppState(outputPath(slot), cfg.AutoLog), // autostart logging if enabled
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
	wasDriven := p.driven
	p.driven, p.override = driven, mac
	p.assignMu.Unlock()
	if !changed {
		return
	}
	// Taking the slot over ends the operator's CSV session rather than leaving
	// a file open that nothing will write to again until the game lets go.
	if driven && !wasDriven {
		p.session.Close()
	}
	p.reassigned()
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

// anyDriven reports whether a game profile is currently choosing straps. While
// it is, the tray's assignment controls cannot take effect, so they are greyed
// rather than left looking clickable.
func (a *App) anyDriven() bool {
	for _, p := range a.players {
		if p.isDriven() {
			return true
		}
	}
	return false
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

	a.players[slotP2].released()
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
		p.released()
		logInfof("[BLE] P%d is now %s", slot+1, orNone(macs[slot]))
	}
	a.signalUI()
}

// reassigned moves this slot onto whatever it should now be following. The
// strap it leaves keeps its connection and ages out on the retention budgets,
// which is what makes a menu round trip or a side swap free.
func (p *player) reassigned() { p.app.resubscribe(p, false) }

// released is reassigned for an explicit act by the operator: picking a
// different device, or switching two-player mode off. That says "not that one",
// so the strap being left goes at once rather than being kept warm.
func (p *player) released() { p.app.resubscribe(p, true) }

func onOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

// startWorkers brings the connection pool to life: any strap already built by a
// pre-start assignment pass gets its goroutine, every slot is subscribed to
// whatever it should be following, and the retention sweeper starts.
func (a *App) startWorkers() {
	a.strapMu.Lock()
	a.started = true
	for _, s := range a.straps {
		go s.run()
	}
	a.strapMu.Unlock()

	a.followAll()
	go a.sweepLoop()
}

// followAll subscribes every slot to whatever it should be following. Safe to
// call repeatedly: a slot already on the right strap is left untouched.
func (a *App) followAll() {
	for _, p := range a.players {
		a.resubscribe(p, false)
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

// isDriven reports whether this slot's strap comes from outside the operator's
// config (the ITGmania profile follower). It gates the things that belong to a
// cabinet's own equipment rather than to a visitor: desktop notifications and
// the config's device list.
func (p *player) isDriven() bool {
	p.assignMu.Lock()
	defer p.assignMu.Unlock()
	return p.driven
}

// adoptConnected brings this slot into a live session, whether it was here when
// the strap connected or subscribed to one that was already up.
func (p *player) adoptConnected(mac string) {
	p.setConnMAC(mac)
	p.state.onConnect()
	p.setPhase(phaseConnected)
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

	p.released()
	logInfof("[BLE] switching to %s (%s)", name, mac)
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
	//
	// Not while a game profile is driving this slot. Those readings belong to
	// whoever walked up to the cabinet, and a workout log per visitor is not
	// what the folder is for; the game has its own record of the session. Only
	// this call is skipped, so the OBS overlay below keeps working.
	if !p.isDriven() {
		if err := p.session.LogReading(now, bpm); err != nil {
			logErrf("[CSV] %v", err)
		}
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
