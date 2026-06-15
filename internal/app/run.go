package app

import (
	"fmt"
	"log"
	"os"

	"fyne.io/systray"
)

func Run() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Println("gotempo " + version)
		return
	}

	// Single-instance guard: a second launch can't take the lock, so it exits
	// instead of adding another tray icon.
	release, ok := acquireInstanceLock()
	if !ok {
		log.Println("gotempo is already running; exiting")
		return
	}
	if release != nil {
		defer release()
	}

	if err := os.MkdirAll(configDir(), 0755); err != nil {
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
		t.mAutoLog = systray.AddMenuItemCheckbox("Autostart HR log", "", app.snapshotConfig().AutoLog)
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
		go t.loop(app.currentMAC() == "")
		go app.runBLE()
		go app.gapCheckLoop()
	}, func() {
		// onExit
		app.session.Close()
	})
}
