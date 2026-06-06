package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/systray"
	"tinygo.org/x/bluetooth"
)

// ── constants ────────────────────────────────────────────────────────────────

const (
	hrServiceUUIDStr = "0000180d-0000-1000-8000-00805f9b34fb"
	hrCharUUIDStr    = "00002a37-0000-1000-8000-00805f9b34fb"

	fastRetries  = 3
	fastInterval = 3 * time.Second
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
func outputPath() string { return filepath.Join(appDir(), "polar-h10.txt") }

func autostartPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "autostart", "polar-hr.desktop")
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
		"Name=polar-hr\n" +
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
	_ = exec.Command("notify-send", "polar-hr", msg).Run()
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

type Config struct {
	MAC  string `json:"mac"`
	Name string `json:"name"`
}

func loadConfig() *Config {
	data, err := os.ReadFile(configPath())
	if err != nil {
		return nil
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil
	}
	if c.MAC == "" || c.Name == "" {
		return nil
	}
	return &c
}

func saveConfig(c Config) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath(), data, 0644)
}

func scanAndSelect(adapter *bluetooth.Adapter) Config {
	fmt.Println("Scanning for Polar H10 (15 s)…")

	type found struct{ addr, name string }
	seen := map[string]found{}

	stopAt := time.AfterFunc(15*time.Second, func() {
		_ = adapter.StopScan()
	})
	defer stopAt.Stop()

	err := adapter.Scan(func(_ *bluetooth.Adapter, r bluetooth.ScanResult) {
		if !r.HasServiceUUID(hrServiceUUID) {
			return
		}
		addr := r.Address.MAC.String()
		if _, ok := seen[addr]; ok {
			return
		}
		seen[addr] = found{addr, r.LocalName()}
	})
	if err != nil {
		log.Println("scan error:", err)
		os.Exit(1)
	}

	if len(seen) == 0 {
		log.Println("no heart-rate devices found — make sure the H10 is awake and dual-BLE is enabled")
		os.Exit(1)
	}

	list := make([]found, 0, len(seen))
	for _, f := range seen {
		list = append(list, f)
	}
	for i, f := range list {
		fmt.Printf("  %d. %s  (%s)\n", i+1, f.name, f.addr)
	}

	var choice int
	for {
		fmt.Print("Select device number: ")
		var line string
		if _, err := fmt.Scanln(&line); err == nil {
			if n, err := strconv.Atoi(line); err == nil && n >= 1 && n <= len(list) {
				choice = n
				break
			}
		}
		fmt.Println("Invalid choice.")
	}
	sel := list[choice-1]
	c := Config{MAC: sel.addr, Name: sel.name}
	if err := saveConfig(c); err != nil {
		log.Println("save config error:", err)
	}
	log.Printf("saved: %s (%s)", sel.name, sel.addr)
	return c
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

func (s *AppState) onConnect() {
	s.mu.Lock()
	s.connected = true
	s.gaveUp = false
	s.mu.Unlock()
}

// ── app ──────────────────────────────────────────────────────────────────────

type App struct {
	mac     string
	state   *AppState
	adapter *bluetooth.Adapter

	uiUpdates chan struct{}
	stop      chan struct{}
	retry     chan struct{}
}

func newApp(mac string, adapter *bluetooth.Adapter) *App {
	return &App{
		mac:       mac,
		state:     &AppState{},
		adapter:   adapter,
		uiUpdates: make(chan struct{}, 1),
		stop:      make(chan struct{}),
		retry:     make(chan struct{}, 1),
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

func (a *App) runBLE() {
	parsed, err := bluetooth.ParseMAC(a.mac)
	if err != nil {
		log.Println("invalid mac:", err)
		return
	}
	addr := bluetooth.Address{MACAddress: bluetooth.MACAddress{MAC: parsed}}

	notifiedLoss := false
	schedule := makeSchedule()

	for {
		select {
		case <-a.stop:
			return
		default:
		}

		err := a.connectOnce(addr, notifiedLoss)
		if err == nil {
			return // stop requested
		}

		hadSession := err.Error() == "session_dropped"
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
					return
				case <-time.After(fastInterval):
				}
				if err := a.connectOnce(addr, false); err == nil {
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
			notify("could not connect to H10")
			select {
			case <-a.stop:
				return
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
			return
		case <-time.After(interval):
		}
	}
}

// connectOnce attempts one connection. Returns nil if stop was requested
// mid-session. Returns an error otherwise — "session_dropped" if a real
// session was established and then lost, or another error if the attempt
// failed before subscription succeeded.
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
	if wasNotified {
		notify("reconnected")
	}
	a.signalUI()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-a.stop:
			_ = chars[0].EnableNotifications(nil)
			_ = device.Disconnect()
			return nil
		case <-ticker.C:
			connected, err := device.Connected()
			if err != nil || !connected {
				_ = chars[0].EnableNotifications(nil)
				_ = device.Disconnect()
				return fmt.Errorf("session_dropped")
			}
		}
	}
}

// ── tray ─────────────────────────────────────────────────────────────────────

type tray struct {
	app        *App
	mRetry     *systray.MenuItem
	mStartLog  *systray.MenuItem
	mStopLog   *systray.MenuItem
	mAutostart *systray.MenuItem
	mQuit      *systray.MenuItem
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
		case <-t.mQuit.ClickedCh:
			close(t.app.stop)
			systray.Quit()
			return
		}
	}
}

// ── main ─────────────────────────────────────────────────────────────────────

func main() {
	adapter := bluetooth.DefaultAdapter
	if err := adapter.Enable(); err != nil {
		log.Println("failed to enable adapter:", err)
		os.Exit(1)
	}

	cfg := loadConfig()
	if cfg == nil {
		c := scanAndSelect(adapter)
		cfg = &c
	}
	log.Printf("using device: %s (%s)", cfg.Name, cfg.MAC)

	app := newApp(cfg.MAC, adapter)

	systray.Run(func() {
		systray.SetIcon(imgDisconnected)
		systray.SetTooltip("polar-hr")

		t := &tray{
			app:        app,
			mRetry:     systray.AddMenuItem("Retry", ""),
			mStartLog:  systray.AddMenuItem("Start logging", ""),
			mStopLog:   systray.AddMenuItem("Stop logging", ""),
			mAutostart: systray.AddMenuItemCheckbox("Start on boot", "", autostartEnabled()),
			mQuit:      systray.AddMenuItem("Quit", ""),
		}
		t.mRetry.Hide()
		t.mStartLog.Hide()
		t.mStopLog.Hide()

		go t.loop()
		go app.runBLE()
	}, func() {
		// onExit
	})
}
