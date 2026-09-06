package app

import "testing"

// The real key name from a paired Polar H10, lowercased as the registry returns
// it. Parsing this correctly is what puts the strap in the tray menu.
const h10EnumKey = "{0000180d-0000-1000-8000-00805f9b34fb}_dev_ma&polar_electro_oy_mo&h10_fw&5.0.0_24acac1841cc"

func TestMACFromEnumKey(t *testing.T) {
	got, ok := macFromEnumKey(h10EnumKey)
	if !ok {
		t.Fatal("did not parse a MAC out of a real key name")
	}
	if want := "24:AC:AC:18:41:CC"; got != want {
		t.Errorf("macFromEnumKey = %q, want %q", got, want)
	}
}

// A key that does not end in twelve hex digits must be skipped, not turned into
// a device that can never connect.
func TestMACFromEnumKeyRejectsJunk(t *testing.T) {
	for _, name := range []string{
		"",
		"nounderscores",
		"{0000180d-...}_dev_ma&x_mo&y_zz", // too short
		"{0000180d-...}_dev_ma&x_mo&y_24acac1841cczz", // too long
		"{0000180d-...}_dev_ma&x_mo&y_24acac1841cg",   // 'g' is not hex
	} {
		if mac, ok := macFromEnumKey(name); ok {
			t.Errorf("macFromEnumKey(%q) = %q, want rejection", name, mac)
		}
	}
}

// The MAC must come out in the same shape the BLE stack and config use, since
// it is compared against scan results and written to config.json.
func TestFormatMACMatchesStackFormat(t *testing.T) {
	got, ok := formatMAC("24ACAC1841CC")
	if !ok || got != "24:AC:AC:18:41:CC" {
		t.Errorf("formatMAC(upper) = %q, %v", got, ok)
	}
	got, ok = formatMAC("24acac1841cc")
	if !ok || got != "24:AC:AC:18:41:CC" {
		t.Errorf("formatMAC(lower) = %q, %v", got, ok)
	}
}

// The fallback name, used when the friendly name is not readable.
func TestNameFromEnumKey(t *testing.T) {
	// Case is preserved here: this reads the key as Windows actually spells it.
	const key = "{0000180d-0000-1000-8000-00805f9b34fb}_Dev_MA&Polar_Electro_Oy_MO&H10_FW&5.0.0_24acac1841cc"
	if got, want := nameFromEnumKey(key), "Polar Electro Oy H10"; got != want {
		t.Errorf("nameFromEnumKey = %q, want %q", got, want)
	}
	if got := nameFromEnumKey("no vendor fields here"); got != "" {
		t.Errorf("nameFromEnumKey(junk) = %q, want empty", got)
	}
}

// The service filter must accept the heart-rate GUID and reject others, or the
// tray offers headphones as heart-rate monitors.
func TestHRServiceGUIDPrefix(t *testing.T) {
	if len(h10EnumKey) < len(hrServiceGUIDPrefix) ||
		h10EnumKey[:len(hrServiceGUIDPrefix)] != hrServiceGUIDPrefix {
		t.Errorf("real HR key does not match the filter prefix %q", hrServiceGUIDPrefix)
	}
	// 0x180F is the battery service, present on the same strap.
	const battery = "{0000180f-0000-1000-8000-00805f9b34fb}_dev_ma&polar_electro_oy_mo&h10_fw&5.0.0_24acac1841cc"
	if battery[:len(hrServiceGUIDPrefix)] == hrServiceGUIDPrefix {
		t.Error("battery service matched the heart-rate filter")
	}
}
