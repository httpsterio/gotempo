package app

import (
	"errors"
	"flag"
	"fmt"
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

	// Resolve stderr verbosity before anything logs. --quiet forces error level
	// (it wins over --log-level); an empty --log-level keeps the info default.
	level := logInfo
	if opts.logLevel != "" {
		l, ok := parseLogLevel(opts.logLevel)
		if !ok {
			fmt.Fprintf(os.Stderr, "invalid --log-level %q (want error|info|debug)\n", opts.logLevel)
			os.Exit(1)
		}
		level = l
	}
	if opts.quiet {
		level = logError
	}
	setLogLevel(level)

	if opts.version {
		fmt.Println("gotempo " + version)
		return
	}

	if opts.epoch && opts.timestamp {
		fmt.Fprintln(os.Stderr, "--epoch and --timestamp are mutually exclusive")
		os.Exit(1)
	}

	if opts.autostart && opts.noAutostart {
		fmt.Fprintln(os.Stderr, "--autostart and --no-autostart are mutually exclusive")
		os.Exit(1)
	}

	if opts.device != "" && opts.selectDev {
		fmt.Fprintln(os.Stderr, "--device and --select-device are mutually exclusive")
		os.Exit(1)
	}

	if opts.autoLog && opts.noAutoLog {
		fmt.Fprintln(os.Stderr, "--auto-log and --no-auto-log are mutually exclusive")
		os.Exit(1)
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

	// One-shot setup commands: write to disk and exit, without starting the app
	// or taking the instance lock.
	if opts.autostart || opts.noAutostart {
		os.Exit(cmdAutostart(opts.autostart))
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
		logInfoln("gotempo is already running; exiting")
		return 0
	}
	if release != nil {
		defer release()
	}

	if err := os.MkdirAll(filepath.Dir(configPath()), 0755); err != nil {
		logErrln("could not create config directory:", err)
	}
	if err := os.MkdirAll(dataDir(), 0755); err != nil {
		logErrln("could not create data directory:", err)
	}

	cfg, changed := loadConfig()
	app := newApp(cfg)

	// Setup flags --device / --select-device set the current device, then the run
	// continues. A failure (bad MAC, no terminal, cancelled) stops here.
	exit, devChanged, proceed := app.applySetupDevice(opts)
	if !proceed {
		return exit
	}
	changed = changed || devChanged

	// --itgmania-module is independent of the device flags and applies after
	// them, so a single invocation can set both.
	exit, itgChanged, proceed := app.applySetupITGModule(opts)
	if !proceed {
		return exit
	}
	changed = changed || itgChanged

	if changed {
		// First run (no file), a config that was missing keys / had invalid
		// values, or a device just set: write the validated, complete form back so
		// it self-documents.
		if err := saveConfig(app.snapshotConfig()); err != nil {
			logErrln("could not write config:", err)
		}
	}
	if cfg.Current == "" {
		logInfoln("no device configured — pick one from the tray ‘Devices’ menu")
	} else {
		logInfof("using device: %s", cfg.Current)
	}

	// Resolve the ITGmania target once, here: the module gets reinstalled and
	// games get moved, so a path that validated when it was set is re-checked
	// every launch rather than trusted. A miss disables the overlay for the run
	// and logs why.
	setupITG(app.snapshotConfig().ITGmaniaModule)

	// Apply the session-only logging override (config value, with headless
	// defaulting on and --auto-log/--no-auto-log winning). Not persisted.
	cfgAutoLog := app.snapshotConfig().AutoLog
	if al := opts.effectiveAutoLog(cfgAutoLog); al != cfgAutoLog {
		app.setLogging(al)
	}

	// Publish a fresh status for this run before the BLE worker starts, so a
	// --status racing startup can't read a previous run's leftover status.json
	// (the lock is already held, so the instance counts as live). runBLE advances
	// the phase from here.
	app.setPhase(phaseIdle)

	// Probe for an adapter, but don't fail if Bluetooth is currently off — the
	// worker keeps retrying once it comes back.
	if _, err := app.ensureAdapter(); err != nil {
		logInfoln("bluetooth adapter not ready (will keep retrying):", err)
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
		logErrln("no device configured; set one in config.json (headless mode has no picker)")
		return 3
	}

	if opts.printBPM {
		a.onReading = func(t time.Time, bpm int) { printReading(t, bpm, opts) }
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
