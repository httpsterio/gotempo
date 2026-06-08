package main

import (
	"encoding/json"
	"testing"
	"time"
)

func TestParseConfigMigratesLegacy(t *testing.T) {
	legacy := `{"mac":"24:AC:AC:18:41:CC","name":"Polar H10 1841CC31"}`
	cfg := parseConfig([]byte(legacy))
	if cfg == nil {
		t.Fatal("parseConfig returned nil for legacy config")
	}
	if cfg.Current != "24:AC:AC:18:41:CC" {
		t.Errorf("current = %q, want the legacy mac", cfg.Current)
	}
	if len(cfg.Known) != 1 || cfg.Known[0].Name != "Polar H10 1841CC31" {
		t.Errorf("known = %+v, want one migrated device", cfg.Known)
	}
}

func TestConfigAutoLogRoundTrip(t *testing.T) {
	orig := Config{
		Current: "24:AC:AC:18:41:CC",
		Known:   []KnownDevice{{MAC: "24:AC:AC:18:41:CC", Name: "H10"}},
		AutoLog: true,
	}
	// Save path goes through clone(), so exercise it here — a clone that drops
	// AutoLog would lose the preference on every write.
	data, err := json.MarshalIndent(orig.clone(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got := parseConfig(data)
	if got == nil {
		t.Fatal("parseConfig returned nil")
	}
	if !got.AutoLog {
		t.Errorf("AutoLog did not survive clone+round-trip: %s", data)
	}
}

func TestParseConfigKeepsDevicelessAutoLog(t *testing.T) {
	// A config with no current device but auto_log set must be preserved,
	// otherwise the preference is lost before a device is ever picked.
	data := `{"current":"","known":null,"auto_log":true}`
	got := parseConfig([]byte(data))
	if got == nil {
		t.Fatal("parseConfig discarded a device-less config")
	}
	if !got.AutoLog {
		t.Error("AutoLog lost for device-less config")
	}
}

func TestSortedKnownRecentFirstEmptyLast(t *testing.T) {
	c := Config{Known: []KnownDevice{
		{MAC: "A", LastUsed: ""},
		{MAC: "B", LastUsed: "2026-06-01T10:00:00Z"},
		{MAC: "C", LastUsed: "2026-06-05T10:00:00Z"},
	}}
	got := c.sortedKnown()
	order := []string{got[0].MAC, got[1].MAC, got[2].MAC}
	want := []string{"C", "B", "A"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

func TestUpsertAndTouch(t *testing.T) {
	c := &Config{}
	c.upsert("AA", "First")
	c.upsert("AA", "") // empty name must not clobber
	if len(c.Known) != 1 || c.Known[0].Name != "First" {
		t.Fatalf("upsert clobbered name: %+v", c.Known)
	}
	c.touch("aa") // case-insensitive
	if c.Known[0].LastUsed == "" {
		t.Fatal("touch did not stamp last_used")
	}
	if _, err := time.Parse(time.RFC3339, c.Known[0].LastUsed); err != nil {
		t.Fatalf("last_used not RFC3339: %v", err)
	}
}

func TestBuildEntriesMergeAndCap(t *testing.T) {
	cfg := Config{Current: "K1", Known: []KnownDevice{
		{MAC: "K1", Name: "Known1", LastUsed: "2026-06-05T10:00:00Z"},
		{MAC: "K2", Name: "Known2", LastUsed: "2026-06-04T10:00:00Z"},
	}}
	scanned := []KnownDevice{
		{MAC: "K1", Name: "Known1"}, // dup of known, must be skipped
		{MAC: "N1", Name: "New1"},   // genuinely new
	}
	entries := buildEntries(cfg, scanned)
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3: %+v", len(entries), entries)
	}
	if entries[0].mac != "K1" || !entries[0].known {
		t.Errorf("first entry should be most-recent known K1")
	}
	if entries[2].mac != "N1" || entries[2].known {
		t.Errorf("last entry should be new unknown N1, got %+v", entries[2])
	}

	// Cap test: 10 known should truncate to maxSwitchSlots.
	big := Config{}
	for i := 0; i < 10; i++ {
		big.Known = append(big.Known, KnownDevice{MAC: string(rune('A' + i))})
	}
	if got := len(buildEntries(big, nil)); got != maxSwitchSlots {
		t.Errorf("cap: got %d, want %d", got, maxSwitchSlots)
	}
}

func TestSlotLabel(t *testing.T) {
	cur := slotLabel(deviceEntry{mac: "M", name: "Foo", known: true}, true)
	if cur != "Foo (current)" {
		t.Errorf("current label = %q", cur)
	}
	nw := slotLabel(deviceEntry{mac: "M", name: "Foo", known: false}, false)
	if nw != "Foo — new" {
		t.Errorf("new label = %q", nw)
	}
	noName := slotLabel(deviceEntry{mac: "24:AC", name: "", known: true}, false)
	if noName != "24:AC" {
		t.Errorf("empty-name label = %q", noName)
	}
}
