package app

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
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
	config      string
}

// headless reports whether the long-running app should skip the tray. Printing
// readings to stdout implies a foreground/headless process, so --print-bpm
// forces it too.
func (o cliOptions) headless() bool { return o.noTray || o.printBPM }

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
	fs.StringVar(&o.config, "config", "", "path to config.json (must already exist)")
	fs.Usage = func() {
		out := fs.Output()
		fmt.Fprintln(out, "gotempo - Bluetooth LE heart-rate monitor")
		fmt.Fprintln(out, "\nUsage: gotempo [flags]\n\nWith no flags, gotempo runs in the system tray.\n\nFlags:")
		fs.PrintDefaults()
		fmt.Fprintln(out, "\nExit codes:")
		fmt.Fprintln(out, "  0  clean exit / --status: running and connected")
		fmt.Fprintln(out, "  1  config error (missing --config file, bad flags)")
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
// --status only inspects that app's state. The instance lock is the "is it
// running" signal.
//
//   - lock free      → nothing is running, no status to report (exit 4)
//   - lock held, file has a value → running and connected (exit 0)
//   - lock held, file empty       → running but not connected (exit 2)
//
// It takes no lock of its own (it drops the probe lock immediately), opens no
// adapter, and writes no files.
func cmdStatus(opts cliOptions) int {
	release, ok := acquireInstanceLock()
	if ok {
		// We acquired it, so no instance is running. Drop it again at once;
		// --status must not hold the lock or do any BLE work.
		if release != nil {
			release()
		}
		printStatus(false, false, 0, opts.json)
		return 4
	}

	// Lock held by a running instance: report its state from the bpm file.
	if bpm, ok := readBPMFile(); ok {
		printStatus(true, true, bpm, opts.json)
		return 0
	}
	printStatus(true, false, 0, opts.json)
	return 2
}

// readBPMFile returns the current value from gotempo-bpm.txt. ok is false when
// the file is missing, empty (not logging / cleared after a stale disconnect),
// or unparseable.
func readBPMFile() (int, bool) {
	data, err := os.ReadFile(outputPath())
	if err != nil {
		return 0, false
	}
	s := strings.TrimSpace(string(data))
	if s == "" {
		return 0, false
	}
	bpm, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return bpm, true
}

// printReading writes one streamed reading for --print-bpm.
func printReading(t time.Time, bpm int, asJSON bool) {
	if asJSON {
		b, _ := json.Marshal(struct {
			Timestamp string `json:"timestamp"`
			BPM       int    `json:"bpm"`
		}{t.Format(time.RFC3339), bpm})
		fmt.Println(string(b))
		return
	}
	fmt.Printf("%d %d\n", t.Unix(), bpm)
}

// printStatus writes the --status result. JSON always carries running and
// connected so a poller can branch on either; bpm is null unless connected.
// Plain text is "<n> bpm", "no signal" (running, not connected), or "gotempo is
// not running".
func printStatus(running, connected bool, bpm int, asJSON bool) {
	if asJSON {
		var p *int
		if connected {
			p = &bpm
		}
		b, _ := json.Marshal(struct {
			Running   bool   `json:"running"`
			Connected bool   `json:"connected"`
			BPM       *int   `json:"bpm"`
			Timestamp string `json:"timestamp"`
		}{running, connected, p, time.Now().Format(time.RFC3339)})
		fmt.Println(string(b))
		return
	}
	switch {
	case !running:
		fmt.Println("gotempo is not running")
	case connected:
		fmt.Printf("%d bpm\n", bpm)
	default:
		fmt.Println("no signal")
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
