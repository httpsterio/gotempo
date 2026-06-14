package app

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"tinygo.org/x/bluetooth"
)

// This file holds the Linux/BlueZ implementations of the platform contract.
// Mac (platform_darwin.go) and Windows (platform_windows.go) provide their own
// versions of the same functions when those builds are added; shared code only
// ever calls them by name. The contract is: dataDir, notify, openLogFolder, the
// autostart trio, and openAdapter.

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

func notify(msg string) {
	_ = exec.Command("notify-send", "gotempo", msg).Run()
}

// openLogFolder opens the log directory in a file browser. It prefers
// xdg-open, but that fails when the system's inode/directory handler is
// misconfigured (it exits non-zero after launching), so it falls back to
// common file managers. xdg-open is run synchronously to observe its exit
// code; the fallbacks are just launched. Call this from its own goroutine —
// xdg-open can block briefly.
func openLogFolder() {
	dir := logsDir()
	if err := exec.Command("xdg-open", dir).Run(); err == nil {
		return
	}
	for _, fm := range []string{"exo-open", "thunar", "nautilus", "dolphin", "pcmanfm", "nemo", "caja", "gio"} {
		path, err := exec.LookPath(fm)
		if err != nil {
			continue
		}
		cmd := exec.Command(path, dir)
		if fm == "gio" {
			cmd = exec.Command(path, "open", dir)
		}
		if err := cmd.Start(); err == nil {
			return
		}
	}
	log.Printf("[logs] could not open %s: no working file-manager opener found", dir)
}

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

// openAdapter resolves a freshly enabled Bluetooth adapter. On Linux/BlueZ the
// adapter is one of hci0..hci9 (the index can change across Bluetooth power
// cycles), so it scans for the first that enables. The returned label is used
// only for logging which adapter was picked.
func openAdapter() (*bluetooth.Adapter, string, error) {
	for i := 0; i < 10; i++ {
		cand := bluetooth.NewAdapter(fmt.Sprintf("hci%d", i))
		if err := cand.Enable(); err == nil {
			return cand, fmt.Sprintf("hci%d", i), nil
		}
	}
	return nil, "", errors.New("no bluetooth adapter available")
}
