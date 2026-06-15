package app

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"fyne.io/systray"
)

func Run() {
	opts, err := parseFlags(os.Args[1:])
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return // -h: flag already printed usage
		}
		os.Exit(1) // bad flag: config error
	}

	if opts.version {
		fmt.Println("gotempo " + version)
		return
	}

	if opts.config != "" {
		// An explicit path must exist: the user pointed somewhere specific, so
		// silently falling back to defaults would hide a typo.
		if _, err := os.Stat(opts.config); err != nil {
			fmt.Fprintf(os.Stderr, "config file not found: %s\n", opts.config)
			os.Exit(1)
		}
		configPathOverride = opts.config
	}

	// One-shot read commands: no instance lock, no dir creation, exit when done.
	if opts.listDevices {
		os.Exit(cmdListDevices(opts))
	}
	if opts.status {
		os.Exit(cmdStatus(opts))
	}

	os.Exit(cmdRun(opts))
}

// cmdRun is the long-running path: tray by default, headless with
// --no-tray/--print-bpm. It takes the single-instance lock so a second launch
// can't add another tray icon or fight over the BLE connection.
func cmdRun(opts cliOptions) int {
	release, ok := acquireInstanceLock()
	if !ok {
		log.Println("gotempo is already running; exiting")
		return 0
	}
	if release != nil {
		defer release()
	}

	if err := os.MkdirAll(filepath.Dir(configPath()), 0755); err != nil {
		log.Println("could not create config directory:", err)
	}
	if err := os.MkdirAll(dataDir(), 0755); err != nil {
		log.Println("could not create data directory:", err)
	}

	cfg, changed := loadConfig()
	if changed {
		// First run (no file) or a config that was missing keys / had invalid
		// values: write the validated, complete form back so it self-documents.
		if err := saveConfig(*cfg); err != nil {
			log.Println("could not write config:", err)
		}
	}
	if cfg.Current == "" {
		log.Println("no device configured — pick one from the tray ‘Devices’ menu")
	} else {
		log.Printf("using device: %s", cfg.Current)
	}

	app := newApp(cfg)

	// Probe for an adapter, but don't fail if Bluetooth is currently off — the
	// worker keeps retrying once it comes back.
	if _, err := app.ensureAdapter(); err != nil {
		log.Println("bluetooth adapter not ready (will keep retrying):", err)
	}

	if opts.headless() {
		return app.runHeadless(opts)
	}
	app.runTray()
	return 0
}

// runHeadless runs the BLE worker with no tray, until SIGINT/SIGTERM. With
// --print-bpm it streams every reading to stdout. Headless has no device
// picker, so a missing device is a hard error (exit 3) rather than an idle wait.
func (a *App) runHeadless(opts cliOptions) int {
	if a.currentMAC() == "" {
		log.Println("no device configured; set one in config.json (headless mode has no picker)")
		return 3
	}

	if opts.printBPM {
		a.onReading = func(t time.Time, bpm int) { printReading(t, bpm, opts.json) }
	}

	go a.runBLE()
	go a.gapCheckLoop()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	// Clean shutdown: stop the worker and flush/close the open CSV session.
	close(a.stop)
	a.session.Close()
	return 0
}

// runTray runs the system tray UI and the BLE worker. It blocks until the user
// quits.
func (a *App) runTray() {
	systray.Run(func() {
		systray.SetIcon(imgDisconnected)
		systray.SetTitle("gotempo") // stable SNI Id so the panel remembers the item
		systray.SetTooltip("gotempo")

		t := &tray{
			app:        a,
			mLog:       systray.AddMenuItem("Start logging", ""),
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

		t.mOpenLogs = systray.AddMenuItem("Open log folder", "")
		t.mAutoLog = systray.AddMenuItemCheckbox("Autostart HR log", "", a.snapshotConfig().AutoLog)
		t.mAutostart = systray.AddMenuItemCheckbox("Start on boot", "", autostartEnabled())

		// Version banner (non-interactive), set off by separators above Quit.
		systray.AddSeparator()
		mVersion := systray.AddMenuItem("gotempo "+version, "")
		mVersion.Disable()
		systray.AddSeparator()

		t.mQuit = systray.AddMenuItem("Quit", "")

		// Build the initial device list before the panel reads the menu, so its
		// first read is the final layout.
		t.refresh()

		// loop owns all subsequent UI mutation; auto-scan on startup if no
		// device is set.
		go t.loop(a.currentMAC() == "")
		go a.runBLE()
		go a.gapCheckLoop()
	}, func() {
		// onExit
		a.session.Close()
	})
}
