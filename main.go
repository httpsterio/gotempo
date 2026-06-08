package main

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/systray"
	"tinygo.org/x/bluetooth"
)

// ── constants ────────────────────────────────────────────────────────────────

const (
	connectScanTimeout = 10 * time.Second

	// After the finite (silent) schedule exhausts, reconnection continues
	// forever at this interval.
	indefiniteInterval = 60 * time.Second

	maxSwitchSlots     = 6
	switchScanDuration = 15 * time.Second
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

//go:embed assets/disconnected.png
var imgDisconnected []byte

//go:embed assets/connected.png
var imgConnected []byte

//go:embed assets/running.png
var imgRunning []byte

// ── paths ────────────────────────────────────────────────────────────────────

// configDir is the XDG config location: $XDG_CONFIG_HOME/gotempo or
// ~/.config/gotempo.
func configDir() string {
	base, err := os.UserConfigDir()
	if err != nil {
		return "."
	}
	return filepath.Join(base, "gotempo")
}

// dataDir is the XDG data location: $XDG_DATA_HOME/gotempo or
// ~/.local/share/gotempo.
func dataDir() string {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "gotempo")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, ".local", "share", "gotempo")
}

func configPath() string { return filepath.Join(configDir(), "config.json") }
func logsDir() string    { return dataDir() }
func outputPath() string { return filepath.Join(logsDir(), "gotempo-bpm.txt") }

func autostartPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "autostart", "gotempo.desktop")
}

func autostartEnabled() bool {
	p := autostartPath()
	if p == "" {
		return false
	}
	_, err := os.Stat(p)
	return err == nil
}

func enableAutostart() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	p := autostartPath()
	if p == "" {
		return fmt.Errorf("no home directory")
	}
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return err
	}
	body := "[Desktop Entry]\n" +
		"Type=Application\n" +
		"Name=Gotempo\n" +
		"Exec=" + exe + "\n" +
		"Icon=gotempo\n" +
		"Hidden=false\n" +
		"NoDisplay=false\n" +
		"X-GNOME-Autostart-enabled=true\n"
	return os.WriteFile(p, []byte(body), 0644)
}

func disableAutostart() error {
	p := autostartPath()
	if p == "" {
		return nil
	}
	err := os.Remove(p)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ── notifications ────────────────────────────────────────────────────────────

func notify(msg string) {
	_ = exec.Command("notify-send", "gotempo", msg).Run()
}

// describeConnectErr maps a raw BLE/BlueZ error into a short, human-readable
// reason. The raw error is returned verbatim if no specific case matches.
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

// ── config ───────────────────────────────────────────────────────────────────

type KnownDevice struct {
	MAC      string `json:"mac"`
	Name     string `json:"name"`
	LastUsed string `json:"last_used,omitempty"` // RFC3339 UTC, set on successful connect
}

type Config struct {
	Current string        `json:"current"`
	Known   []KnownDevice `json:"known"`
	AutoLog bool          `json:"auto_log,omitempty"` // start logging automatically on launch
}

func (c Config) clone() Config {
	out := Config{Current: c.Current, AutoLog: c.AutoLog}
	out.Known = append([]KnownDevice(nil), c.Known...)
	return out
}

// upsert adds the device if absent, or updates its name if a non-empty one is
// supplied. It never clears an existing last_used timestamp.
func (c *Config) upsert(mac, name string) {
	for i := range c.Known {
		if strings.EqualFold(c.Known[i].MAC, mac) {
			if name != "" {
				c.Known[i].Name = name
			}
			return
		}
	}
	c.Known = append(c.Known, KnownDevice{MAC: mac, Name: name})
}

// touch stamps the device's last_used with the current time, adding it if absent.
func (c *Config) touch(mac string) {
	now := time.Now().UTC().Format(time.RFC3339)
	for i := range c.Known {
		if strings.EqualFold(c.Known[i].MAC, mac) {
			c.Known[i].LastUsed = now
			return
		}
	}
	c.Known = append(c.Known, KnownDevice{MAC: mac, LastUsed: now})
}

// sortedKnown returns a copy of known devices ordered most-recently-used first.
// Never-used devices (empty last_used) sort last.
func (c Config) sortedKnown() []KnownDevice {
	ks := append([]KnownDevice(nil), c.Known...)
	sort.SliceStable(ks, func(i, j int) bool {
		return ks[i].LastUsed > ks[j].LastUsed
	})
	return ks
}

// parseConfig decodes config JSON, migrating the legacy {mac, name} schema.
// Returns nil if the data is unusable (unparseable or no current device).
func parseConfig(data []byte) *Config {
	var raw struct {
		Current string        `json:"current"`
		Known   []KnownDevice `json:"known"`
		AutoLog bool          `json:"auto_log"`
		MAC     string        `json:"mac"`  // legacy
		Name    string        `json:"name"` // legacy
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	cfg := &Config{Current: raw.Current, Known: raw.Known, AutoLog: raw.AutoLog}
	// Migrate legacy {mac, name} schema.
	if cfg.Current == "" && raw.MAC != "" {
		cfg.Current = raw.MAC
		cfg.upsert(raw.MAC, raw.Name)
	}
	// A device-less config is still valid (selection happens via the tray) and
	// must be kept so preferences like auto_log survive.
	return cfg
}

func loadConfig() *Config {
	data, err := os.ReadFile(configPath())
	if err != nil {
		return nil
	}
	return parseConfig(data)
}

func saveConfig(c Config) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath(), data, 0644)
}

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
	timer := time.AfterFunc(d, func() { _ = adapter.StopScan() })
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
		log.Println("scan error:", err)
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
	timer := time.AfterFunc(d, func() { _ = adapter.StopScan() })
	defer timer.Stop()

	if err := adapter.Scan(func(_ *bluetooth.Adapter, r bluetooth.ScanResult) {
		if strings.EqualFold(r.Address.MAC.String(), target) {
			found = true
			_ = adapter.StopScan()
		}
	}); err != nil {
		log.Println("scan error:", err)
		return adapter, false
	}
	return adapter, found
}

// ── shared state ─────────────────────────────────────────────────────────────

type AppState struct {
	mu        sync.Mutex
	connected bool
	logging   bool
	lastBPM   int
	hasBPM    bool
}

func (s *AppState) snapshot() (connected, logging bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connected, s.logging
}

func (s *AppState) setConnected(v bool) {
	s.mu.Lock()
	s.connected = v
	s.mu.Unlock()
}

func (s *AppState) setLogging(v bool) {
	s.mu.Lock()
	s.logging = v
	s.mu.Unlock()
}

func (s *AppState) resetBPM() {
	s.mu.Lock()
	s.hasBPM = false
	s.mu.Unlock()
}

func (s *AppState) onConnect() {
	s.mu.Lock()
	s.connected = true
	s.mu.Unlock()
}

// ── app ──────────────────────────────────────────────────────────────────────

type App struct {
	state *AppState

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
		cfg:       cfg,
		uiUpdates: make(chan struct{}, 1),
		stop:      make(chan struct{}),
		switchCh:  make(chan struct{}, 1),
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
// it is still alive, otherwise it scans hci0..hci9 for one that enables. The hci
// index can change when the user power-cycles Bluetooth, so this must be
// re-checked before every scan/connect rather than resolved once at startup.
func (a *App) ensureAdapter() (*bluetooth.Adapter, error) {
	a.adapterMu.Lock()
	defer a.adapterMu.Unlock()

	if a.adapter != nil {
		if err := a.adapter.Enable(); err == nil {
			return a.adapter, nil
		}
	}
	for i := 0; i < 10; i++ {
		cand := bluetooth.NewAdapter(fmt.Sprintf("hci%d", i))
		if err := cand.Enable(); err == nil {
			if a.adapter == nil {
				log.Printf("[BLE] using adapter hci%d", i)
			} else {
				log.Printf("[BLE] adapter changed to hci%d", i)
			}
			a.adapter = cand
			return cand, nil
		}
	}
	return nil, errors.New("no bluetooth adapter available")
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

	a.state.setConnected(false)
	a.state.resetBPM()
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
	if !a.state.logging {
		a.state.mu.Unlock()
		return
	}
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
			log.Println("invalid mac:", err)
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
// sends a single "device lost" notification and then retries forever at
// indefiniteInterval. A reconnect during that phase notifies "reconnected" (via
// connectOnce) and resets the schedule. It never gives up; it returns only
// errStopped or errSwitched.
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

		err := a.connectOnce(addr, notifiedLoss)
		if errors.Is(err, errStopped) {
			return errStopped
		}
		if errors.Is(err, errSwitched) {
			return errSwitched
		}

		if errors.Is(err, errSessionDropped) {
			// A real session was held and then lost — start the schedule over.
			log.Println("[BLE] session ended; reconnecting")
			attempt = 0
			notifiedLoss = false
		} else {
			log.Printf("[BLE] connect failed: %s", describeConnectErr(err))
		}
		a.state.setConnected(false)
		a.signalUI()

		var interval time.Duration
		if attempt < len(schedule) {
			interval = schedule[attempt]
			attempt++
		} else {
			if !notifiedLoss {
				notify("device lost")
				notifiedLoss = true
			}
			interval = indefiniteInterval
		}

		select {
		case <-a.stop:
			return errStopped
		case <-a.switchCh:
			return errSwitched
		case <-time.After(interval):
		}
	}
}

// connectOnce attempts one connection and, on success, blocks until the session
// ends. It always returns a sentinel error: errStopped, errSwitched,
// errSessionDropped, or a raw connect/discovery error.
func (a *App) connectOnce(addr bluetooth.Address, wasNotified bool) error {
	// Scan first so BlueZ has a fresh device object (and to pick up an adapter
	// index change). This mirrors the proven find-then-connect flow.
	log.Printf("[BLE] scanning for %s…", addr.MAC.String())
	adapter, ok := a.findDevice(addr.MAC.String(), connectScanTimeout)
	if adapter == nil {
		return errors.New("no bluetooth adapter available")
	}
	if !ok {
		return fmt.Errorf("device %s not found in scan", addr.MAC.String())
	}

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
				return errSessionDropped
			}
		}
	}
}

// ── tray ─────────────────────────────────────────────────────────────────────

type tray struct {
	app *App

	mStartLog  *systray.MenuItem
	mStopLog   *systray.MenuItem
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
		return "● " + name + " (current)"
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

	// Logging controls are always visible; greyed out when not actionable.
	if connected && !logging {
		t.mStartLog.Enable()
	} else {
		t.mStartLog.Disable()
	}
	if connected && logging {
		t.mStopLog.Enable()
	} else {
		t.mStopLog.Disable()
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
		case <-t.mStartLog.ClickedCh:
			connected, logging := t.app.state.snapshot()
			if connected && !logging {
				t.app.state.setLogging(true)
			}
			t.refresh()
		case <-t.mStopLog.ClickedCh:
			t.app.state.setLogging(false)
			t.refresh()
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

// ── main ─────────────────────────────────────────────────────────────────────

func main() {
	if err := os.MkdirAll(configDir(), 0755); err != nil {
		log.Println("could not create config directory:", err)
	}
	if err := os.MkdirAll(dataDir(), 0755); err != nil {
		log.Println("could not create data directory:", err)
	}

	cfg := loadConfig()
	if cfg == nil {
		cfg = &Config{}
	}
	if cfg.Current == "" {
		log.Println("no device configured — pick one from the tray ‘Devices’ menu")
	} else {
		log.Printf("using device: %s", cfg.Current)
	}

	app := newApp(cfg)

	// Probe for an adapter, but don't fail if Bluetooth is currently off — the
	// tray still launches and the worker keeps retrying once it comes back.
	if _, err := app.ensureAdapter(); err != nil {
		log.Println("bluetooth adapter not ready (will keep retrying):", err)
	}

	systray.Run(func() {
		systray.SetIcon(imgDisconnected)
		systray.SetTitle("gotempo") // stable SNI Id so the panel remembers the item
		systray.SetTooltip("gotempo")

		t := &tray{
			app:        app,
			mStartLog:  systray.AddMenuItem("Start logging", ""),
			mStopLog:   systray.AddMenuItem("Stop logging", ""),
			slotMACs:   make([]string, maxSwitchSlots),
			slotNames:  make([]string, maxSwitchSlots),
			slotClicks: make(chan int),
			scanDone:   make(chan []KnownDevice, 1),
		}

		// Device selection lives directly in the top-level menu rather than a
		// nested submenu: XFCE's old libdbusmenu intermittently renders a nested
		// submenu as an empty box, while top-level items render reliably. The
		// slots are pre-created here so they keep their position between the
		// logging controls and the toggles, then shown/hidden as devices appear.
		systray.AddSeparator()
		for i := 0; i < maxSwitchSlots; i++ {
			slot := systray.AddMenuItem("", "")
			slot.Hide()
			t.switchSlots = append(t.switchSlots, slot)
			idx := i
			go func() {
				for range slot.ClickedCh {
					t.slotClicks <- idx
				}
			}()
		}
		t.mRescan = systray.AddMenuItem("Rescan for new devices", "")
		systray.AddSeparator()

		t.mAutoLog = systray.AddMenuItemCheckbox("Autostart HR log", "", app.snapshotConfig().AutoLog)
		t.mAutostart = systray.AddMenuItemCheckbox("Start on boot", "", autostartEnabled())
		t.mQuit = systray.AddMenuItem("Quit", "")

		// Build the initial device list before the panel reads the menu, so its
		// first read is the final layout.
		t.refresh()

		// loop owns all subsequent UI mutation; auto-scan on startup if no
		// device is set.
		go t.loop(app.currentMAC() == "")
		go app.runBLE()
	}, func() {
		// onExit
	})
}
