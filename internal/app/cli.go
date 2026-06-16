package app

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"
)

// cliOptions is the parsed command line. The zero value (no flags) means "run
// the tray normally".
type cliOptions struct {
	version     bool
	noTray      bool
	listDevices bool
	status      bool
	printBPM    bool
	json        bool
	epoch       bool
	timestamp   bool
	autostart   bool
	noAutostart bool
	selectDev   bool
	autoLog     bool
	noAutoLog   bool
	quiet       bool
	device      string
	logLevel    string
	config      string
}

// headless reports whether the long-running app should skip the tray. Printing
// readings to stdout implies a foreground/headless process, so --print-bpm
// forces it too.
func (o cliOptions) headless() bool { return o.noTray || o.printBPM }

// effectiveAutoLog decides whether session logging starts on for this run. It is
// session-only and never written back to config. The base is the config value;
// --no-tray is the explicit "run as a daemon" signal, so it defaults logging on
// (even alongside --print-bpm, which is additive — "also stream to stdout", not
// "watch-only"). A bare --print-bpm (without --no-tray) does not enable logging.
// The explicit --auto-log / --no-auto-log flags override either way.
func (o cliOptions) effectiveAutoLog(cfgAutoLog bool) bool {
	v := cfgAutoLog
	if o.noTray {
		v = true
	}
	if o.autoLog {
		v = true
	}
	if o.noAutoLog {
		v = false
	}
	return v
}

// tsMode is how --print-bpm renders each reading's time.
type tsMode int

const (
	tsClock tsMode = iota // hh:mm:ss (default)
	tsEpoch               // unix seconds (--epoch)
	tsFull                // RFC3339 (--timestamp)
)

func (o cliOptions) tsMode() tsMode {
	switch {
	case o.epoch:
		return tsEpoch
	case o.timestamp:
		return tsFull
	default:
		return tsClock
	}
}

func parseFlags(args []string) (cliOptions, error) {
	var o cliOptions
	var vShort bool
	fs := flag.NewFlagSet("gotempo", flag.ContinueOnError)
	fs.BoolVar(&o.version, "version", false, "print version and exit")
	fs.BoolVar(&vShort, "v", false, "print version and exit (shorthand)")
	fs.BoolVar(&o.noTray, "no-tray", false, "run headless, without the system tray")
	fs.BoolVar(&o.listDevices, "list-devices", false, "scan for HR monitors, print them, and exit")
	fs.BoolVar(&o.status, "status", false, "report the running app's status and current BPM, then exit")
	fs.BoolVar(&o.printBPM, "print-bpm", false, "stream each BPM reading to stdout (implies --no-tray)")
	fs.BoolVar(&o.json, "json", false, "machine-readable JSON for --status / --print-bpm / --list-devices")
	fs.BoolVar(&o.epoch, "epoch", false, "--print-bpm: timestamp as unix seconds")
	fs.BoolVar(&o.timestamp, "timestamp", false, "--print-bpm: timestamp as RFC3339 (default: hh:mm:ss)")
	fs.BoolVar(&o.autostart, "autostart", false, "enable launch-on-login (writes the autostart entry), then exit")
	fs.BoolVar(&o.noAutostart, "no-autostart", false, "disable launch-on-login (removes the autostart entry), then exit")
	fs.StringVar(&o.device, "device", "", "set the current device by MAC in config, then run")
	fs.BoolVar(&o.selectDev, "select-device", false, "interactively pick the current device, then run")
	fs.BoolVar(&o.autoLog, "auto-log", false, "force session logging on for this run (overrides config)")
	fs.BoolVar(&o.noAutoLog, "no-auto-log", false, "force session logging off for this run (overrides config)")
	fs.BoolVar(&o.quiet, "quiet", false, "suppress info logging (errors only); same as --log-level error")
	fs.StringVar(&o.logLevel, "log-level", "", "stderr verbosity: error|info|debug (default info)")
	fs.StringVar(&o.config, "config", "", "path to config.json (must already exist)")
	fs.Usage = func() {
		out := fs.Output()
		// Hand-rolled, grouped help: flag.PrintDefaults sorts alphabetically,
		// which scatters --epoch/--timestamp away from the --print-bpm they
		// modify. Listing them indented under it shows the dependency.
		line := func(flag, desc string) { fmt.Fprintf(out, "  %-20s %s\n", flag, desc) }
		fmt.Fprintln(out, "gotempo - Bluetooth LE heart-rate monitor")
		fmt.Fprintln(out, "\nUsage: gotempo [flags]\n\nWith no flags, gotempo runs in the system tray.")
		fmt.Fprintln(out, "\nModes:")
		line("--no-tray", "run headless, without the system tray")
		line("--print-bpm", "stream each BPM reading to stdout (implies --no-tray)")
		line("  --epoch", "with --print-bpm: timestamp as unix seconds")
		line("  --timestamp", "with --print-bpm: timestamp as RFC3339 (default hh:mm:ss)")
		line("--status", "report the running app's state, then exit")
		line("--list-devices", "scan for HR monitors, print them, and exit")
		fmt.Fprintln(out, "\nSetup (persist to config/disk):")
		line("--autostart", "enable launch-on-login, then exit")
		line("--no-autostart", "disable launch-on-login, then exit")
		line("--device <mac>", "set the current device by MAC, then run")
		line("--select-device", "interactively pick the current device, then run")
		fmt.Fprintln(out, "\nOptions:")
		line("--auto-log", "force session logging on for this run")
		line("--no-auto-log", "force session logging off for this run")
		line("--log-level <lvl>", "stderr verbosity: error|info|debug (default info)")
		line("--quiet", "errors only; same as --log-level error")
		line("--json", "machine-readable JSON for --status/--print-bpm/--list-devices")
		line("--config <path>", "path to config.json (must already exist)")
		line("--version, -v", "print version and exit")
		fmt.Fprintln(out, "\nExit codes:")
		fmt.Fprintln(out, "  0  clean exit / --status: running and connected")
		fmt.Fprintln(out, "  1  config/setup error (bad flags or value, missing --config, setup write failed)")
		fmt.Fprintln(out, "  2  --status: running but not connected")
		fmt.Fprintln(out, "  3  bluetooth adapter unavailable, or --no-tray with no device")
		fmt.Fprintln(out, "  4  --status: gotempo is not running")
	}
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	o.version = o.version || vShort
	return o, nil
}

// cmdAutostart enables or disables launch-on-login and exits. It is a setup
// command: it writes to disk (the autostart entry) and does not start the app.
// enableAutostart overwrites an existing entry silently; disableAutostart treats
// a missing entry as success (rm -f). Only a real write/remove failure (e.g.
// permissions) is an error (exit 1).
func cmdAutostart(enable bool) int {
	var err error
	if enable {
		err = enableAutostart()
	} else {
		err = disableAutostart()
	}
	if err != nil {
		action := "enable"
		if !enable {
			action = "disable"
		}
		fmt.Fprintf(os.Stderr, "could not %s autostart: %v\n", action, err)
		return 1
	}
	if enable {
		fmt.Println("autostart enabled")
	} else {
		fmt.Println("autostart disabled")
	}
	return 0
}

// cmdListDevices scans once and prints the heart-rate monitors found. Exit 0
// even when none are found; only an unusable adapter is an error (exit 3). It
// runs its own scan and does not take the instance lock, so it works alongside
// a running tray app.
func cmdListDevices(opts cliOptions) int {
	cfg, _ := loadConfig()
	app := newApp(cfg)
	if _, err := app.ensureAdapter(); err != nil {
		fmt.Fprintln(os.Stderr, "bluetooth adapter not available:", err)
		return 3
	}
	for _, d := range app.scanDevices(switchScanDuration) {
		printDevice(d, opts.json)
	}
	return 0
}

// cmdStatus reports the status of the running gotempo. It never connects to a
// device itself: a strap's single BLE connection belongs to the running app, so
// --status only inspects that app's published state. The instance lock is the
// "is it running" signal; the per-instance status.json (maintained independent
// of logging) carries the connection state and current bpm.
//
//   - lock free        → nothing is running (exit 4)
//   - running, connected with a reading → exit 0
//   - running otherwise (connecting / not connected) → exit 2
//
// It takes no lock of its own (drops the probe lock at once), opens no adapter,
// and writes no files. A stale status.json from a crash is ignored because it is
// only read when the lock is held (a crashed process releases the lock).
func cmdStatus(opts cliOptions) int {
	release, ok := acquireInstanceLock()
	if ok {
		// We acquired it, so no instance is running. Drop it again at once;
		// --status must not hold the lock or do any BLE work.
		if release != nil {
			release()
		}
		printStatus(false, appStatus{}, opts.json)
		return 4
	}

	// Lock held by a running instance: report its published status.
	st, ok := readStatus()
	if !ok {
		// Running but nothing published yet (just started).
		st = appStatus{Phase: phaseIdle}
	}
	printStatus(true, st, opts.json)
	if st.Connected && st.BPM != nil {
		return 0
	}
	return 2
}

// readStatus reads the running instance's status.json. ok is false when the file
// is missing or unparseable.
func readStatus() (appStatus, bool) {
	data, err := os.ReadFile(statusPath())
	if err != nil {
		return appStatus{}, false
	}
	var st appStatus
	if err := json.Unmarshal(data, &st); err != nil {
		return appStatus{}, false
	}
	return st, true
}

// printReading writes one streamed reading for --print-bpm. The timestamp format
// follows the flags: hh:mm:ss by default, unix seconds with --epoch, RFC3339 with
// --timestamp. In JSON, --epoch emits a number; the others emit a string.
func printReading(t time.Time, bpm int, o cliOptions) {
	mode := o.tsMode()
	if o.json {
		var ts any
		if mode == tsEpoch {
			ts = t.Unix()
		} else {
			ts = formatTS(t, mode)
		}
		b, _ := json.Marshal(struct {
			Timestamp any `json:"timestamp"`
			BPM       int `json:"bpm"`
		}{ts, bpm})
		fmt.Println(string(b))
		return
	}
	fmt.Printf("%s %d\n", formatTS(t, mode), bpm)
}

func formatTS(t time.Time, mode tsMode) string {
	switch mode {
	case tsEpoch:
		return strconv.FormatInt(t.Unix(), 10)
	case tsFull:
		return t.Format(time.RFC3339)
	default:
		return t.Format("15:04:05")
	}
}

// printStatus writes the --status result. JSON carries the full state (running,
// connected, phase, logging, bpm, device) so a poller has everything; plain text
// is a single human-readable line.
func printStatus(running bool, st appStatus, asJSON bool) {
	if asJSON {
		b, _ := json.Marshal(struct {
			Running   bool          `json:"running"`
			Connected bool          `json:"connected"`
			Phase     string        `json:"phase"`
			Logging   bool          `json:"logging"`
			BPM       *int          `json:"bpm"`
			Device    *statusDevice `json:"device"`
			Timestamp string        `json:"timestamp"`
		}{running, st.Connected, st.Phase, st.Logging, st.BPM, st.Device, time.Now().Format(time.RFC3339)})
		fmt.Println(string(b))
		return
	}
	if !running {
		fmt.Println("gotempo is not running")
		return
	}

	dev := "no device"
	if st.Device != nil {
		if st.Device.Name != "" {
			dev = st.Device.Name
		} else {
			dev = st.Device.MAC
		}
	}
	logging := "logging off"
	if st.Logging {
		logging = "logging on"
	}

	switch st.Phase {
	case phaseConnected:
		if st.BPM != nil {
			fmt.Printf("connected, %d bpm, %s, %s\n", *st.BPM, dev, logging)
		} else {
			fmt.Printf("connected, no reading yet, %s, %s\n", dev, logging)
		}
	case phaseConnecting:
		fmt.Printf("connecting, %s, %s\n", dev, logging)
	case phaseReconnecting:
		fmt.Printf("reconnecting, %s, %s\n", dev, logging)
	default: // idle / unknown
		fmt.Printf("idle, %s\n", dev)
	}
}

// printDevice writes one scanned device for --list-devices.
func printDevice(d KnownDevice, asJSON bool) {
	if asJSON {
		b, _ := json.Marshal(struct {
			MAC  string `json:"mac"`
			Name string `json:"name"`
		}{d.MAC, d.Name})
		fmt.Println(string(b))
		return
	}
	name := d.Name
	if name == "" {
		name = "(unknown)"
	}
	fmt.Printf("%s\t%s\n", d.MAC, name)
}
