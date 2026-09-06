package app

import (
	"errors"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/sys/windows/registry"
	"tinygo.org/x/bluetooth"
)

// This file holds the Windows implementations of the platform contract. Shared
// code only ever calls them by name. The contract is: dirs, notify, openFolder,
// pairedDevices, the autostart trio, and openAdapter.

// dirs puts config and data in the same folder: %LOCALAPPDATA%\gotempo. Windows
// has no XDG-style split, and logs should not follow a roaming profile, so the
// Roaming %AppData% that os.UserConfigDir would pick is deliberately not used.
var dirs = dirLayout{
	configEnv: "LOCALAPPDATA", configRel: []string{"AppData", "Local"},
	dataEnv: "LOCALAPPDATA", dataRel: []string{"AppData", "Local"},
}

// notify is a no-op on Windows. Toast notifications need a registered AppUserModelID
// and a dependency to build the XML payload; Linux already treats notifications as
// optional (skipped when notify-send is missing), so dropping them here is
// consistent rather than a regression.
func notify(msg string) {}

// openFolder opens a directory in Explorer. Start, not Run: explorer.exe exits
// with a non-zero code even when it opened the window, so waiting on it would
// report a failure that did not happen.
func openFolder(dir string) {
	if err := exec.Command("explorer.exe", dir).Start(); err != nil {
		logErrf("[open] could not open %s: %v", dir, err)
	}
}

// The Windows PnP device tree, where paired BLE devices live. Both keys are
// readable without elevation.
//
// bthleDeviceKey has one subkey per service per paired device, named
// <service guid>_Dev_MA&<vendor>_MO&<model>_FW&<version>_<mac>. Filtering it on
// the heart-rate GUID gives the straps and nothing else.
//
// bthleKey holds the device itself, as Dev_<mac>, and somewhere below it the
// friendly name Windows shows in Settings.
const (
	bthleDeviceKey = `SYSTEM\CurrentControlSet\Enum\BTHLEDevice`
	bthleKey       = `SYSTEM\CurrentControlSet\Enum\BTHLE`
)

// pairedDevices lists heart-rate monitors Windows has already paired.
//
// This is the discovery path that matters on Windows: a bonded device stops
// advertising, so scanning cannot see the strap the user just paired. Reading
// the registry finds it whether or not it is advertising, or even switched on.
//
// Failures are quiet and return what was gathered so far. The tray still has the
// scan and the config's known devices; a missing registry key means "no paired
// straps", not an error worth interrupting the user for.
func pairedDevices() []KnownDevice {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, bthleDeviceKey, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		logDebugf("[BLE] no paired-device registry key: %v", err)
		return nil
	}
	defer k.Close()

	names, err := k.ReadSubKeyNames(-1)
	if err != nil {
		logDebugf("[BLE] could not enumerate paired devices: %v", err)
		return nil
	}

	var out []KnownDevice
	seen := map[string]bool{}
	for _, name := range names {
		lower := strings.ToLower(name)
		if !strings.HasPrefix(lower, hrServiceGUIDPrefix) {
			continue
		}
		mac, ok := macFromEnumKey(lower)
		if !ok || seen[mac] {
			continue
		}
		seen[mac] = true

		label := friendlyName(mac)
		if label == "" {
			label = nameFromEnumKey(name)
		}
		out = append(out, KnownDevice{MAC: mac, Name: label})
	}
	return out
}

// friendlyName reads the name Windows shows for a paired device, from
// BTHLE\Dev_<mac>\<instance>\FriendlyName. The instance subkey in the middle is
// generated per pairing, so it is enumerated rather than assumed.
//
// Returns "" when anything is missing, and the caller falls back to the vendor
// fields in the enum key.
func friendlyName(mac string) string {
	dev := "Dev_" + strings.ToLower(strings.ReplaceAll(mac, ":", ""))
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, bthleKey+`\`+dev, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return ""
	}
	defer k.Close()

	instances, err := k.ReadSubKeyNames(-1)
	if err != nil {
		return ""
	}
	for _, inst := range instances {
		ik, err := registry.OpenKey(registry.LOCAL_MACHINE, bthleKey+`\`+dev+`\`+inst, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		v, _, err := ik.GetStringValue("FriendlyName")
		ik.Close()
		if err == nil && v != "" {
			return v
		}
	}
	return ""
}

// runKey is the per-user autostart list. HKCU needs no elevation, and entries
// run at logon for that user only, which matches the Linux ~/.config/autostart
// behaviour.
const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// autostartValue is the value name under runKey. Fixed, so enable/disable and
// the tray checkbox all agree regardless of where the binary lives.
const autostartValue = appName

func autostartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	v, _, err := k.GetStringValue(autostartValue)
	return err == nil && v != ""
}

// enableAutostart writes the current executable's path to the Run key. The path
// is quoted: Windows splits an unquoted Run entry on spaces, and the default
// install location has several.
func enableAutostart() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue(autostartValue, `"`+exe+`"`)
}

// disableAutostart removes the entry. A missing key or value is success, not an
// error: the desired state is "not starting on boot", and it already holds.
func disableAutostart() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return err
	}
	defer k.Close()
	if err := k.DeleteValue(autostartValue); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}

// openAdapter returns the system Bluetooth adapter.
//
// Unlike Linux there is nothing to enumerate: bluetooth.NewAdapter is
// Linux-only, and Windows exposes a single package-level DefaultAdapter. Enable
// is what initializes the WinRT stack (ole.RoInitialize), and it is called here
// rather than by the caller: ensureAdapter only re-Enables an adapter it already
// holds, so a fresh one arrives expected to be live.
func openAdapter() (*bluetooth.Adapter, string, error) {
	adapter := bluetooth.DefaultAdapter
	if err := adapter.Enable(); err != nil {
		return nil, "", err
	}
	return adapter, "default", nil
}
