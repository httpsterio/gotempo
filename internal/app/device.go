package app

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"tinygo.org/x/bluetooth"
)

// normalizeMAC validates a MAC address and returns it in the library's canonical
// form. ok is false if the string is not a MAC (so --device rejects an index or
// typo before touching config). bluetooth.ParseMAC only accepts uppercase,
// colon-separated input, so accept lowercase and dash separators by canonicalizing
// first — a CLI user shouldn't have to match an exact format.
func normalizeMAC(s string) (string, bool) {
	canon := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(s), "-", ":"))
	addr, err := bluetooth.ParseMAC(canon)
	if err != nil {
		return "", false
	}
	return addr.String(), true
}

// stdinIsTerminal reports whether stdin is an interactive terminal. A char
// device is a tty; a pipe or redirected file is not, so --select-device can
// refuse to block on input that will never come.
func stdinIsTerminal() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// deviceLabel formats a device for the picker: "Name (mac)" or "(unknown) (mac)".
func deviceLabel(d KnownDevice) string {
	name := d.Name
	if name == "" {
		name = "(unknown)"
	}
	return fmt.Sprintf("%s (%s)", name, d.MAC)
}

// mergeDevices returns known devices first, then any scanned device not already
// in the known list (matched by MAC, case-insensitive). A scanned name fills in
// for a known device that has none.
func mergeDevices(known, scanned []KnownDevice) []KnownDevice {
	out := append([]KnownDevice(nil), known...)
	for _, s := range scanned {
		found := false
		for i := range out {
			if strings.EqualFold(out[i].MAC, s.MAC) {
				if out[i].Name == "" {
					out[i].Name = s.Name
				}
				found = true
				break
			}
		}
		if !found {
			out = append(out, s)
		}
	}
	return out
}

// selectDeviceInteractive runs the --select-device picker on the terminal. It
// lists known devices first and offers an on-demand scan; a scan is only run if
// the user asks for it (picking a known device skips it). Returns the chosen
// device, or ok=false if the user cancels or stdin closes.
func (a *App) selectDeviceInteractive() (mac, name string, ok bool) {
	reader := bufio.NewReader(os.Stdin)
	known := a.snapshotConfig().sortedKnown()
	devices := known

	for {
		if len(devices) == 0 {
			fmt.Println("No known devices yet.")
		} else {
			fmt.Println("Devices:")
			for i, d := range devices {
				fmt.Printf("  %d) %s\n", i+1, deviceLabel(d))
			}
		}
		fmt.Println("  s) scan for new devices")
		fmt.Println("  q) cancel")
		fmt.Print("Select: ")

		line, err := reader.ReadString('\n')
		if err != nil { // EOF (e.g. Ctrl-D)
			fmt.Println()
			return "", "", false
		}
		choice := strings.TrimSpace(line)

		switch strings.ToLower(choice) {
		case "", "q":
			return "", "", false
		case "s":
			fmt.Printf("Scanning for %s...\n", switchScanDuration)
			scanned := a.scanDevices(switchScanDuration)
			if len(scanned) == 0 {
				fmt.Println("No devices found.")
			}
			devices = mergeDevices(known, scanned)
			continue
		}

		n, err := strconv.Atoi(choice)
		if err != nil || n < 1 || n > len(devices) {
			fmt.Println("Invalid choice.")
			continue
		}
		d := devices[n-1]
		return d.MAC, d.Name, true
	}
}

// applySetupDevice handles the --device / --select-device setup flags: it sets
// the current device in the in-memory config (the caller persists it) and lets
// the run proceed. proceed is false when the flag failed (bad MAC, no terminal,
// cancelled) and exit carries the code to return. changed is true when the
// config was modified and should be saved.
func (a *App) applySetupDevice(opts cliOptions) (exit int, changed, proceed bool) {
	switch {
	case opts.device != "":
		mac, valid := normalizeMAC(opts.device)
		if !valid {
			fmt.Fprintf(os.Stderr, "invalid device MAC: %q\n", opts.device)
			return 1, false, false
		}
		a.setCurrentDevice(mac, "")
		return 0, true, true

	case opts.selectDev:
		if !stdinIsTerminal() {
			fmt.Fprintln(os.Stderr, "--select-device needs an interactive terminal")
			return 1, false, false
		}
		mac, name, ok := a.selectDeviceInteractive()
		if !ok {
			fmt.Fprintln(os.Stderr, "no device selected")
			return 1, false, false
		}
		a.setCurrentDevice(mac, name)
		return 0, true, true
	}
	return 0, false, true
}

// applySetupITGModule handles --itgmania-module: it records the path to
// gotempo.lua in the in-memory config (the caller persists it) and lets the run
// proceed. Like --config, an explicit path must already exist, since the user
// pointed somewhere specific and a typo would otherwise become a silently dead
// overlay. It is orthogonal to the device flags, so it applies alongside them.
func (a *App) applySetupITGModule(opts cliOptions) (exit int, changed, proceed bool) {
	if opts.itgModule == "" {
		return 0, false, true
	}
	abs, err := filepath.Abs(opts.itgModule)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid --itgmania-module path: %v\n", err)
		return 1, false, false
	}
	info, err := os.Stat(abs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gotempo.lua not found: %s\n", abs)
		return 1, false, false
	}
	if info.IsDir() {
		fmt.Fprintf(os.Stderr, "--itgmania-module wants the gotempo.lua file, not a directory: %s\n", abs)
		return 1, false, false
	}
	a.cfgMu.Lock()
	a.cfg.ITGmaniaModule = abs
	a.cfgMu.Unlock()
	return 0, true, true
}

// setCurrentDevice adds the device (if absent) and marks it current. A blank
// name is left to backfill on first connect.
func (a *App) setCurrentDevice(mac, name string) {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	a.cfg.upsert(mac, name)
	a.cfg.Current = mac
}
