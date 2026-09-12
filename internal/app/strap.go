package app

import (
	"errors"
	"strings"
	"sync"
	"time"

	"tinygo.org/x/bluetooth"
)

// Straps outlive assignments.
//
// A strap is one physical belt and the connection to it. It is owned by the App
// and keyed by MAC, not by the slot wearing it, because connection lifetime and
// assignment are different questions with different answers. Who a reading
// belongs to changes constantly: a player leaves the song wheel, swaps from P1
// to P2, or ITGmania restarts. Whether the belt is worn changes rarely. Tying
// the first to the second meant every one of those cost a full reconnect, which
// is ~30s of no readings, because the retry schedule is 5x3s then 5x10s and the
// first attempt usually lands while the belt is still tearing down the old link.
//
// So slots subscribe to straps. Changing sides is two pointer swaps and no
// Bluetooth traffic at all; the belt never finds out. A strap with no
// subscribers keeps its connection and simply delivers to nobody, which is what
// makes rejoining instant.
//
// Nothing here keeps a belt awake. A belt sleeps about a minute after coming off
// skin and the link dies with it, whatever we do, so "link up" and "worn" are
// the same fact. That is what the retention rules below read.

const (
	// strapPoolCap bounds simultaneous connections. Reached only when people
	// keep arriving without the ones before them leaving; two new players
	// replacing two old ones is a real change of who is playing, and paying a
	// reconnect there is honest.
	strapPoolCap = 4

	strapSweepInterval = 15 * time.Second
)

// strap is one belt: its address, its connection goroutine, and the slots
// currently listening to it.
type strap struct {
	app  *App
	mac  string // canonical, and fixed for this strap's life
	addr bluetooth.Address

	// stopCh is closed to retire this strap. It replaces the per-player switch
	// channel: a strap never changes device, it is only ever created or retired,
	// so the connection loop has one exit condition instead of two.
	stopCh  chan struct{}
	retired sync.Once

	mu   sync.Mutex
	subs []*player
	// connected mirrors the link for the pool's own bookkeeping; each
	// subscriber's AppState carries its own copy for status.json.
	connected bool
	phase     string
	// idleSince is when the last subscriber left, zero while any remain. It
	// drives the long budget.
	idleSince time.Time
	// lastLive is the last moment this strap produced a usable reading. It
	// drives the short budget, and one field covers both ways of going quiet:
	// a dropped link stops the readings, and so does a belt streaming zeros.
	lastLive time.Time
}

func newStrap(a *App, mac string, addr bluetooth.Address) *strap {
	now := time.Now()
	return &strap{
		app:      a,
		mac:      mac,
		addr:     addr,
		stopCh:   make(chan struct{}),
		phase:    phaseConnecting,
		lastLive: now,
	}
}

// strapKey normalizes a MAC for the pool map, so config and a game profile that
// disagree on case land on one strap rather than two connections to one belt.
func strapKey(mac string) string { return strings.ToUpper(mac) }

// ── subscription ─────────────────────────────────────────────────────────────

func (s *strap) attach(p *player) {
	s.mu.Lock()
	s.subs = append(s.subs, p)
	s.idleSince = time.Time{}
	s.mu.Unlock()
}

func (s *strap) detach(p *player) {
	s.mu.Lock()
	for i, q := range s.subs {
		if q == p {
			s.subs = append(s.subs[:i], s.subs[i+1:]...)
			break
		}
	}
	if len(s.subs) == 0 {
		s.idleSince = time.Now()
	}
	s.mu.Unlock()
}

// subscribers snapshots the list so the fan-out below never holds s.mu while
// calling into a player, which would invite a lock cycle back through App.
func (s *strap) subscribers() []*player {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*player(nil), s.subs...)
}

func (s *strap) hasSub() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.subs) > 0
}

func (s *strap) isConnected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connected
}

func (s *strap) idleAt() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.idleSince
}

// notifies reports whether a loss or reconnect toast belongs to anybody. Only a
// slot following its own configured strap gets them: a profile-driven strap
// belongs to a visitor, and reporting on it fills the desktop with news about
// people who have gone home.
func (s *strap) notifies() bool {
	for _, p := range s.subscribers() {
		if !p.isDriven() {
			return true
		}
	}
	return false
}

// ── state fan-out ────────────────────────────────────────────────────────────

// setPhase records a connection-phase transition for the strap and every slot
// listening to it.
func (s *strap) setPhase(phase string) {
	s.mu.Lock()
	s.phase = phase
	s.mu.Unlock()
	for _, p := range s.subscribers() {
		p.setPhase(phase)
	}
}

// wentUp publishes a new session to the subscribers.
func (s *strap) wentUp() {
	s.mu.Lock()
	s.connected = true
	s.phase = phaseConnected
	s.lastLive = time.Now()
	s.mu.Unlock()
	for _, p := range s.subscribers() {
		p.adoptConnected(s.mac)
	}
}

// wentDown publishes the drop. The stale-hold on each subscriber's output file
// is what keeps a brief blip from blanking an overlay.
func (s *strap) wentDown() {
	s.mu.Lock()
	s.connected = false
	s.mu.Unlock()
	for _, p := range s.subscribers() {
		p.setConnMAC("")
		p.state.onDisconnect()
	}
}

// deliver hands one reading to every listening slot. A reading with no listener
// is not an error: it is an unsubscribed strap being kept warm, and the only
// thing it changes is that this strap is visibly still alive.
func (s *strap) deliver(bpm int) {
	s.mu.Lock()
	if bpm > 0 {
		s.lastLive = time.Now()
	}
	s.mu.Unlock()
	for _, p := range s.subscribers() {
		p.handleBPM(bpm)
	}
}

// markConnected records the connection in config, under the same rule as the
// notifications: a visitor's strap must not end up in the cabinet's device list.
func (s *strap) markConnected() {
	for _, p := range s.subscribers() {
		if !p.isDriven() {
			s.app.markConnected(s.mac)
			return
		}
	}
}

// ── retention ────────────────────────────────────────────────────────────────

// expired reports whether this strap has outlived its usefulness.
//
// Two independent budgets, whichever fires first. hold covers a strap still
// delivering readings: it is worn, the player is just not in a song, and
// keeping it costs nothing but a pool place, so it is generous. lost covers one
// that has gone quiet, which is the same event whether it disconnected (the
// normal case: off skin, asleep, link gone) or is holding the link open while
// streaming zeros, because lastLive advances on neither. Past that there is
// nothing to hold on to, only a retry loop firing at a belt in somebody's bag,
// so it is short: it covers the drops worth chasing, which come back inside a
// minute or two (adjusting the strap, stepping out of range, dry electrodes).
//
// Neither can touch a strap a slot is still listening to, whatever it is doing.
// Dropping one somebody is watching would blank a panel mid-song, and the slot
// would immediately ask for it back, which is a reconnect loop rather than a
// release.
func (s *strap) expired(now time.Time, hold, lost time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.subs) > 0 {
		return false
	}
	return now.Sub(s.idleSince) > hold || now.Sub(s.lastLive) > lost
}

// retire tears the strap down. The connection loop unwinds and disconnects on
// its way out, so this does not block.
func (s *strap) retire() {
	s.retired.Do(func() { close(s.stopCh) })
}

// ── pool ─────────────────────────────────────────────────────────────────────

// resubscribe points a slot at whatever it should now be following, creating or
// reusing the strap. It is the single entry point for every assignment change:
// the tray, the config, and the ITGmania profile follower all end up here.
//
// A slot already on the right strap returns untouched. That is what makes the
// common cases free: leaving the song wheel hands the slot back to config, and
// if config names the same belt the slot never notices, where the old code
// signalled its connection loop and paid a reconnect for a MAC that had not
// changed.
//
// drop retires the strap the slot is leaving instead of letting it age out.
// Picking a different device in the tray means "not that one"; the grace period
// is for implicit releases, where ITGmania simply stopped naming it.
func (a *App) resubscribe(p *player, drop bool) {
	want := p.effectiveMAC()

	a.strapMu.Lock()
	old := p.strap
	if old != nil && strings.EqualFold(old.mac, want) {
		a.strapMu.Unlock()
		return
	}
	var orphan *strap
	if old != nil {
		old.detach(p)
		p.strap = nil
		if drop && !old.hasSub() {
			delete(a.straps, strapKey(old.mac))
			orphan = old
		}
	}
	var next *strap
	if want != "" {
		if next = a.strapForLocked(want); next != nil {
			p.strap = next
			next.attach(p)
		}
	}
	a.strapMu.Unlock()

	if orphan != nil {
		logInfof("[BLE] releasing %s", orphan.mac)
		orphan.retire()
	}

	// Announced outside the lock: these reach into AppState and back out to
	// publishStatus, which takes the config mutex.
	p.state.onSwitch()
	switch {
	case next == nil:
		p.setConnMAC("")
		p.setPhase(phaseIdle)
	case next.isConnected():
		// Joining a strap that is already up: adopt the live session rather
		// than reporting a connect that is not going to happen.
		p.adoptConnected(next.mac)
	default:
		p.setConnMAC("")
		p.setPhase(phaseConnecting)
	}
	a.signalUI()
}

// strapForLocked returns the pooled strap for a MAC, creating it and making
// room if needed. Caller holds strapMu.
func (a *App) strapForLocked(mac string) *strap {
	key := strapKey(mac)
	if s := a.straps[key]; s != nil {
		return s
	}
	parsed, err := bluetooth.ParseMAC(mac)
	if err != nil {
		logErrln("[BLE] invalid mac:", err)
		return nil
	}
	a.makeRoomLocked()

	s := newStrap(a, mac, bluetooth.Address{MACAddress: bluetooth.MACAddress{MAC: parsed}})
	if a.straps == nil {
		a.straps = map[string]*strap{}
	}
	a.straps[key] = s
	if a.started {
		go s.run()
	}
	return s
}

// makeRoomLocked evicts unsubscribed straps, least recently released first,
// until there is space. A strap somebody is listening to is never evicted, so a
// full pool of live slots simply exceeds the cap rather than dropping a reading
// somebody can see. With two sides that cannot happen at a cap of four.
func (a *App) makeRoomLocked() {
	for len(a.straps) >= strapPoolCap {
		var victim *strap
		for _, s := range a.straps {
			if s.hasSub() {
				continue
			}
			if victim == nil || s.idleAt().Before(victim.idleAt()) {
				victim = s
			}
		}
		if victim == nil {
			return
		}
		delete(a.straps, strapKey(victim.mac))
		logInfof("[BLE] pool full, releasing %s", victim.mac)
		victim.retire()
	}
}

// sweepStraps retires everything past its budget. Runs on a ticker rather than
// per-strap timers so the rules are read in one place and can be tested against
// a clock.
func (a *App) sweepStraps(now time.Time) {
	cfg := a.snapshotConfig()
	hold, lost := cfg.strapHold(), cfg.strapLost()

	a.strapMu.Lock()
	var gone []*strap
	for key, s := range a.straps {
		if s.expired(now, hold, lost) {
			delete(a.straps, key)
			gone = append(gone, s)
		}
	}
	a.strapMu.Unlock()

	for _, s := range gone {
		logInfof("[BLE] releasing %s, unused for %s", s.mac, now.Sub(s.idleAt()).Round(time.Second))
		s.retire()
	}
}

func (a *App) sweepLoop() {
	ticker := time.NewTicker(strapSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-a.stop:
			return
		case now := <-ticker.C:
			a.sweepStraps(now)
		}
	}
}

// ── connection ───────────────────────────────────────────────────────────────

// run is the strap's connection goroutine. Unlike the per-player loop it
// replaces there is no outer loop re-reading a device: a strap has one address
// for its whole life, so the only way out is retirement or shutdown.
func (s *strap) run() { _ = s.connectLoop() }

// connectLoop runs the reconnection state machine. It retries silently through
// the finite schedule (5×3s, then 5×10s); a reconnect during that phase is
// silent. When the finite schedule exhausts it sends a single "device lost"
// notification and then retries connect-by-address until the device reappears. A
// reconnect during that phase notifies "reconnected" (via connectAndMonitor) and
// resets the schedule. It never gives up on its own; it returns only errStopped
// or errRetired, and the retention budgets are what bound it.
func (s *strap) connectLoop() error {
	schedule := makeSchedule()
	attempt := 0
	notifiedLoss := false

	for {
		select {
		case <-s.app.stop:
			return errStopped
		case <-s.stopCh:
			return errRetired
		default:
		}

		if attempt < len(schedule) {
			err := s.connectOnce(notifiedLoss)
			if errors.Is(err, errStopped) {
				return errStopped
			}
			if errors.Is(err, errRetired) {
				return errRetired
			}

			if errors.Is(err, errSessionDropped) {
				// connectAndMonitor logs the session length on drop.
				attempt = 0
				notifiedLoss = false
				s.wentDown()
			} else {
				logErrf("[BLE] connect failed: %s", describeConnectErr(err))
				attempt++
			}
			s.app.signalUI()

			if attempt < len(schedule) {
				select {
				case <-s.app.stop:
					return errStopped
				case <-s.stopCh:
					return errRetired
				case <-time.After(schedule[attempt]):
				}
				continue
			}
			// Schedule exhausted; fall through to persistent phase.
		}

		if !notifiedLoss {
			if s.notifies() {
				notify("device lost")
			}
			notifiedLoss = true
		}
		return s.persistentConnect(notifiedLoss)
	}
}

// connectOnce connects directly to the device by address, with no scan. Used
// during the finite schedule phase. A direct connect targets only the peer
// address, so it never emits scan-request probes to other devices in range. It
// returns errStopped, errRetired, errSessionDropped, or a raw connect/discovery
// error.
func (s *strap) connectOnce(wasNotified bool) error {
	s.setPhase(phaseConnecting)
	adapter, err := s.app.ensureAdapter()
	if err != nil {
		return err
	}
	return s.connectAndMonitor(adapter, wasNotified)
}

// persistentConnect retries a direct connect-by-address until the device comes
// back, then monitors the session; on a drop it goes straight back to retrying.
// It never scans, so it emits no scan-request probes to other devices while the
// strap is away (a belt is off most of the time it isn't worn). BlueZ must know
// the device for connect-by-address to work; a bonded strap qualifies, and the
// device is established by the user's tray pick / Rescan / --select-device,
// never by a background scan. It returns errStopped or errRetired only.
func (s *strap) persistentConnect(wasNotified bool) error {
	start := time.Now()
	var lastLog time.Time // zero value forces a log on the first round
	for {
		select {
		case <-s.app.stop:
			return errStopped
		case <-s.stopCh:
			return errRetired
		default:
		}

		s.setPhase(phaseReconnecting)
		if time.Since(lastLog) >= persistLogInterval {
			logInfof("[BLE] reconnecting to %s (%s elapsed)", s.mac, time.Since(start).Round(time.Second))
			lastLog = time.Now()
		}

		adapter, err := s.app.ensureAdapter()
		if err != nil {
			logErrf("[BLE] %v", err)
		} else if err = s.connectAndMonitor(adapter, wasNotified); errors.Is(err, errStopped) || errors.Is(err, errRetired) {
			return err
		} else if errors.Is(err, errSessionDropped) {
			// connectAndMonitor logs the session length on drop; flip state and
			// retry immediately so a brief blip reconnects fast.
			s.wentDown()
			s.app.signalUI()
			wasNotified = false
			start = time.Now()
			lastLog = time.Time{}
			continue
		} else if err != nil {
			// Expected while the strap is away (connect aborts/times out).
			logErrf("[BLE] connect failed: %s", describeConnectErr(err))
		}

		select {
		case <-s.app.stop:
			return errStopped
		case <-s.stopCh:
			return errRetired
		case <-time.After(persistentRetryInterval):
		}
	}
}

// connectDevice attempts a direct connect-by-address and returns once connected,
// failed, or the app stops / the strap retires. adapter.Connect targets only the
// peer address (no scan, no probes to other devices) but can block while BlueZ
// waits on the connection attempt, so it runs in a goroutine and is abandoned on
// stop/retire. An abandoned attempt that later connects is disconnected so it
// does not hold the device's single BLE slot.
func (s *strap) connectDevice(adapter *bluetooth.Adapter) (bluetooth.Device, error) {
	type result struct {
		dev bluetooth.Device
		err error
	}
	ch := make(chan result, 1)
	go func() {
		dev, err := adapter.Connect(s.addr, bluetooth.ConnectionParams{})
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
	case <-s.app.stop:
		return abandon(errStopped)
	case <-s.stopCh:
		return abandon(errRetired)
	case r := <-ch:
		return r.dev, r.err
	}
}

// connectAndMonitor connects to the device by address, discovers the HR service
// and characteristic, enables notifications, and blocks until the session ends
// or the app stops / the strap retires. It always returns a sentinel error:
// errStopped, errRetired, errSessionDropped, or a raw connect/discovery error.
func (s *strap) connectAndMonitor(adapter *bluetooth.Adapter, wasNotified bool) error {
	logInfof("[BLE] connecting to %s…", s.mac)
	device, err := s.connectDevice(adapter)
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
		return errors.New("HR service not found")
	}

	chars, err := services[0].DiscoverCharacteristics([]bluetooth.UUID{hrCharUUID})
	if err != nil {
		_ = device.Disconnect()
		return err
	}
	if len(chars) == 0 {
		_ = device.Disconnect()
		return errors.New("HR characteristic not found")
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
		s.deliver(bpm)
	}); err != nil {
		_ = device.Disconnect()
		return err
	}

	logInfoln("[BLE] connected")
	connectedAt := time.Now()
	s.wentUp()
	s.markConnected()
	if wasNotified && s.notifies() {
		notify("reconnected")
	}
	s.app.signalUI()

	cleanup := func() {
		_ = chars[0].EnableNotifications(nil)
		_ = device.Disconnect()
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.app.stop:
			cleanup()
			s.wentDown()
			return errStopped
		case <-s.stopCh:
			cleanup()
			s.wentDown()
			return errRetired
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
