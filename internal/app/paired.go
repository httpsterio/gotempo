package app

import (
	"strings"
)

// Paired devices.
//
// Scanning is how Linux finds a strap, and it is enough there. On Windows it is
// not: once a BLE device is bonded, Windows holds the link and the device stops
// advertising, so an advertisement watcher never sees the very strap the user
// just paired. The tray then shows an empty list and the only way in is to know
// your own MAC, which nobody does.
//
// So the platform contract gains pairedDevices(): whatever the OS already knows
// is paired, independent of advertising. Linux returns nil (its scan already
// works, and BlueZ-known devices are reachable anyway), Windows reads them out
// of the PnP registry.
//
// The parsing below lives in shared code so it is testable without Windows.

// hrServiceGUIDPrefix is the heart-rate service (0x180D) as Windows spells a
// 16-bit BLE UUID in the device tree. Filtering on it keeps headphones and
// keyboards out of a list of heart-rate monitors.
const hrServiceGUIDPrefix = "{0000180d-0000-1000-8000-00805f9b34fb}"

// macFromEnumKey pulls the address out of a BTHLEDevice enum key name, which
// looks like:
//
//	{0000180d-0000-1000-8000-00805f9b34fb}_Dev_MA&Polar_Electro_Oy_MO&H10_FW&5.0.0_24acac1841cc
//
// The address is the final underscore-separated field, twelve hex digits and no
// separators. Everything before it is vendor text that varies per device and is
// not worth parsing for identity.
func macFromEnumKey(name string) (string, bool) {
	i := strings.LastIndex(name, "_")
	if i < 0 {
		return "", false
	}
	return formatMAC(name[i+1:])
}

// formatMAC turns twelve hex digits into the colon-separated uppercase form the
// rest of the app uses ("24acac1841cc" -> "24:AC:AC:18:41:CC"). It rejects
// anything that is not exactly twelve hex digits, so a malformed or unexpected
// key name is skipped rather than turned into a device that cannot connect.
func formatMAC(hex string) (string, bool) {
	if len(hex) != 12 {
		return "", false
	}
	var b strings.Builder
	b.Grow(17)
	for i := 0; i < 12; i++ {
		c := hex[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
			c -= 'a' - 'A'
		case c >= 'A' && c <= 'F':
		default:
			return "", false
		}
		if i > 0 && i%2 == 0 {
			b.WriteByte(':')
		}
		b.WriteByte(c)
	}
	return b.String(), true
}

// nameFromEnumKey builds a display name from the vendor fields in the enum key,
// used when the friendly name is not readable. The key encodes them as
// MA&<manufacturer>_MO&<model>_FW&<firmware>, with spaces written as
// underscores, so "MA&Polar_Electro_Oy_MO&H10_FW&5.0.0" becomes
// "Polar Electro Oy H10".
//
// It is a fallback: the friendly name Windows shows ("Polar H10 1841CC31") is
// better, and matches what the same strap is called on Linux.
func nameFromEnumKey(name string) string {
	field := func(tag string) string {
		i := strings.Index(name, tag)
		if i < 0 {
			return ""
		}
		rest := name[i+len(tag):]
		// Fields run until the next "_XX&" tag, so cut at the first one.
		for _, next := range []string{"_MA&", "_MO&", "_FW&"} {
			if j := strings.Index(rest, next); j >= 0 {
				rest = rest[:j]
			}
		}
		return strings.TrimSpace(strings.ReplaceAll(rest, "_", " "))
	}

	parts := []string{}
	for _, p := range []string{field("MA&"), field("MO&")} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, " ")
}
