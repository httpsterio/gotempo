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
	fastRetries  = 3
	fastInterval = 3 * time.Second

	maxSwitchSlots     = 8
	switchScanDuration = 15 * time.Second
)

var retrySchedule = []struct {
	count    int
	interval time.Duration
}{
	{5, 3 * time.Second},
	{5, 10 * time.Second},
	{3, 60 * time.Second},
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

//go:embed disconnected.png
var imgDisconnected []byte

//go:embed connected.png
var imgConnected []byte

//go:embed running.png
var imgRunning []byte

// ── paths ────────────────────────────────────────────────────────────────────

func appDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

func configPath() string { return filepath.Join(appDir(), "config.json") }
func logsDir() string    { return filepath.Join(appDir(), "logs") }
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
}

func (c Config) clone() Config {
	out := Config{Current: c.Current}
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

func loadConfig() *Config {
	data, err := os.ReadFile(configPath())
	if err != nil {
		return nil
	}
	var raw struct {
		Current string        `json:"current"`
		Known   []KnownDevice `json:"known"`
		MAC     string        `json:"mac"`  // legacy
		Name    string        `json:"name"` // legacy
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	cfg := &Config{Current: raw.Current, Known: raw.Known}
	// Migrate legacy {mac, name} schema.
	if cfg.Current == "" && raw.MAC != "" {
		cfg.Current = raw.MAC
		cfg.upsert(raw.MAC, raw.Name)
	}
	if cfg.Current == "" {
		return nil
	}
	return cfg
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
func scanDevices(adapter *bluetooth.Adapter, d time.Duration) []KnownDevice {
	seen := map[string]KnownDevice{}
	timer := time.AfterFunc(d, func() { _ = adapter.StopScan() })
	defer timer.Stop()

	err := adapter.Scan(func(_ *bluetooth.Adapter, r bluetooth.ScanResult) {
		if !r.HasServiceUUID(hrServiceUUID) {
			return
		}
		mac := r.Address.MAC.String()
		if _, ok := seen[mac]; ok {
			return
		}
		seen[mac] = KnownDevice{MAC: mac, Name: r.LocalName()}
	})
	if err != nil {
		log.Println("scan error:", err)
	}

	out := make([]KnownDevice, 0, len(seen))
	for _, k := range seen {
		out = append(out, k)
	}
	return out
}

// ── shared state ─────────────────────────────────────────────────────────────

type AppState struct {
	mu        sync.Mutex
	connected bool
	logging   bool
	gaveUp    bool
	lastBPM   int
	hasBPM    bool
}

func (s *AppState) snapshot() (connected, logging, gaveUp bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connected, s.logging, s.gaveUp
}

func (s *AppState) setConnected(v bool) {
	s.mu.Lock()
	s.connected = v
	s.mu.Unlock()
}

func (s *AppState) setGaveUp(v bool) {
	s.mu.Lock()
	s.gaveUp = v
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
	s.gaveUp = false
	s.mu.Unlock()
}

// ── app ──────────────────────────────────────────────────────────────────────

type App struct {
	state   *AppState
	adapter *bluetooth.Adapter

	cfgMu sync.Mutex
	cfg   *Config

	uiUpdates chan struct{}
	stop      chan struct{}
	retry     chan struct{}
	switchCh  chan struct{}
}

func newApp(cfg *Config, adapter *bluetooth.Adapter) *App {
	return &App{
		state:     &AppState{},
		adapter:   adapter,
		cfg:       cfg,
		uiUpdates: make(chan struct{}, 1),
		stop:      make(chan struct{}),
		retry:     make(chan struct{}, 1),
		switchCh:  make(chan struct{}, 1),
	}
}

func (a *App) signalUI() {
	select {
	case a.uiUpdates <- struct{}{}:
	default:
	}
}

func (a *App) triggerRetry() {
	select {
	case a.retry <- struct{}{}:
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
	a.state.setGaveUp(false)
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

// connectLoop runs the retry state machine for a single device address.
// It returns errStopped or errSwitched.
func (a *App) connectLoop(addr bluetooth.Address) error {
	notifiedLoss := false
	schedule := makeSchedule()

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

		hadSession := errors.Is(err, errSessionDropped)
		if hadSession {
			log.Println("[BLE] session ended; attempting fast reconnect")
		} else {
			log.Printf("[BLE] connect failed: %s", describeConnectErr(err))
		}
		a.state.setConnected(false)
		a.signalUI()

		if hadSession {
			recovered := false
			for i := 0; i < fastRetries; i++ {
				select {
				case <-a.stop:
					return errStopped
				case <-a.switchCh:
					return errSwitched
				case <-time.After(fastInterval):
				}
				e := a.connectOnce(addr, false)
				if errors.Is(e, errStopped) {
					return errStopped
				}
				if errors.Is(e, errSwitched) {
					return errSwitched
				}
				if errors.Is(e, errSessionDropped) {
					// Reconnected and held a session before dropping again.
					recovered = true
					break
				}
			}
			if recovered {
				notifiedLoss = false
				schedule = makeSchedule()
				continue
			}
			notify("connection lost")
			notifiedLoss = true
			a.signalUI()
		}

		if len(schedule) == 0 {
			a.state.setGaveUp(true)
			a.signalUI()
			notify("could not connect to device")
			select {
			case <-a.stop:
				return errStopped
			case <-a.switchCh:
				return errSwitched
			case <-a.retry:
			}
			schedule = makeSchedule()
			notifiedLoss = false
			continue
		}
		interval := schedule[0]
		schedule = schedule[1:]
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
	log.Printf("[BLE] connecting to %s…", addr.MAC.String())
	device, err := a.adapter.Connect(addr, bluetooth.ConnectionParams{})
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

	mRetry     *systray.MenuItem
	mStartLog  *systray.MenuItem
	mStopLog   *systray.MenuItem
	mAutostart *systray.MenuItem
	mQuit      *systray.MenuItem

	mSwitch     *systray.MenuItem
	switchSlots []*systray.MenuItem
	slotMACs    []string
	slotNames   []string
	mScanning   *systray.MenuItem
	mRescan     *systray.MenuItem

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
	connected, logging, gaveUp := t.app.state.snapshot()

	switch {
	case gaveUp || !connected:
		systray.SetIcon(imgDisconnected)
	case logging:
		systray.SetIcon(imgRunning)
	default:
		systray.SetIcon(imgConnected)
	}

	if gaveUp {
		t.mRetry.Show()
	} else {
		t.mRetry.Hide()
	}
	if connected && !logging && !gaveUp {
		t.mStartLog.Show()
	} else {
		t.mStartLog.Hide()
	}
	if connected && logging {
		t.mStopLog.Show()
	} else {
		t.mStopLog.Hide()
	}

	t.renderSwitch()
}

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
	t.mScanning.Show()
	t.mRescan.Disable()
	go func() {
		t.scanDone <- scanDevices(t.app.adapter, switchScanDuration)
	}()
}

func (t *tray) loop() {
	for {
		select {
		case <-t.app.uiUpdates:
			t.refresh()
		case <-t.mRetry.ClickedCh:
			t.app.state.setGaveUp(false)
			t.app.triggerRetry()
			t.refresh()
		case <-t.mStartLog.ClickedCh:
			t.app.state.setLogging(true)
			t.refresh()
		case <-t.mStopLog.ClickedCh:
			t.app.state.setLogging(false)
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
			t.mScanning.Hide()
			t.mRescan.Enable()
			t.lastScanned = res
			t.renderSwitch()
		case <-t.mQuit.ClickedCh:
			close(t.app.stop)
			systray.Quit()
			return
		}
	}
}

// ── main ─────────────────────────────────────────────────────────────────────

func main() {
	if err := os.MkdirAll(logsDir(), 0755); err != nil {
		log.Println("could not create logs directory:", err)
	}

	adapter := bluetooth.DefaultAdapter
	if err := adapter.Enable(); err != nil {
		log.Println("failed to enable adapter:", err)
		os.Exit(1)
	}

	cfg := loadConfig()
	if cfg == nil {
		cfg = &Config{}
		log.Println("no device configured — pick one from the tray ‘Switch device’ menu")
	} else {
		log.Printf("using device: %s", cfg.Current)
	}

	app := newApp(cfg, adapter)

	systray.Run(func() {
		systray.SetIcon(imgDisconnected)
		systray.SetTitle("gotempo") // stable SNI Id so the panel remembers the item
		systray.SetTooltip("gotempo")

		t := &tray{
			app:        app,
			mRetry:     systray.AddMenuItem("Retry", ""),
			mStartLog:  systray.AddMenuItem("Start logging", ""),
			mStopLog:   systray.AddMenuItem("Stop logging", ""),
			slotMACs:   make([]string, maxSwitchSlots),
			slotNames:  make([]string, maxSwitchSlots),
			slotClicks: make(chan int),
			scanDone:   make(chan []KnownDevice, 1),
		}

		t.mSwitch = systray.AddMenuItem("Switch device", "")
		for i := 0; i < maxSwitchSlots; i++ {
			slot := t.mSwitch.AddSubMenuItem("", "")
			slot.Hide()
			t.switchSlots = append(t.switchSlots, slot)
			idx := i
			go func() {
				for range slot.ClickedCh {
					t.slotClicks <- idx
				}
			}()
		}
		t.mScanning = t.mSwitch.AddSubMenuItem("Scanning…", "")
		t.mScanning.Disable()
		t.mScanning.Hide()
		t.mRescan = t.mSwitch.AddSubMenuItem("Rescan for new devices", "")

		t.mAutostart = systray.AddMenuItemCheckbox("Start on boot", "", autostartEnabled())
		t.mQuit = systray.AddMenuItem("Quit", "")

		t.mRetry.Hide()
		t.mStartLog.Hide()
		t.mStopLog.Hide()
		t.renderSwitch()

		go t.loop()
		go app.runBLE()

		// With no device configured, scan straight away so the user can pick
		// one from the Switch device submenu.
		if app.currentMAC() == "" {
			t.startScan()
		}
	}, func() {
		// onExit
	})
}
