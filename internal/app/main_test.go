package app

import (
	"encoding/json"
	"testing"
	"time"
)

func TestParseConfigMigratesLegacy(t *testing.T) {
	legacy := `{"mac":"24:AC:AC:18:41:CC","name":"Polar H10 1841CC31"}`
	cfg, _ := parseConfig([]byte(legacy))
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
	got, _ := parseConfig(data)
	if got == nil {
		t.Fatal("parseConfig returned nil")
	}
	if !got.AutoLog {
		t.Errorf("AutoLog did not survive clone+round-trip: %s", data)
	}
}

func TestConfigITGmaniaModuleRoundTrip(t *testing.T) {
	orig := Config{ITGmaniaModule: "/home/u/.itgmania/Themes/Simply Love/Modules/gotempo.lua"}
	// Same trap as AutoLog: the save path goes through clone(), so a clone that
	// drops the field would silently unset the overlay on every config write.
	data, err := json.MarshalIndent(orig.clone(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := parseConfig(data)
	if got == nil {
		t.Fatal("parseConfig returned nil")
	}
	if got.ITGmaniaModule != orig.ITGmaniaModule {
		t.Errorf("ITGmaniaModule = %q, want %q", got.ITGmaniaModule, orig.ITGmaniaModule)
	}
}

func TestParseConfigKeepsDevicelessAutoLog(t *testing.T) {
	// A config with no current device but auto_log set must be preserved,
	// otherwise the preference is lost before a device is ever picked.
	data := `{"current":"","known":null,"auto_log":true}`
	got, _ := parseConfig([]byte(data))
	if got == nil {
		t.Fatal("parseConfig discarded a device-less config")
	}
	if !got.AutoLog {
		t.Error("AutoLog lost for device-less config")
	}
}

func TestParseConfigDefaultsMissingKeys(t *testing.T) {
	// Only current set; everything else must come from defaults, and the config
	// must be flagged changed so it gets rewritten complete.
	cfg, changed := parseConfig([]byte(`{"current":"AA:BB"}`))
	if cfg == nil {
		t.Fatal("parseConfig returned nil")
	}
	if !changed {
		t.Error("changed = false, want true for a config missing keys")
	}
	if cfg.SessionGapMinutes != defaultSessionGapMinutes {
		t.Errorf("gap = %d, want default %d", cfg.SessionGapMinutes, defaultSessionGapMinutes)
	}
	if cfg.MinBPMThreshold != defaultMinBPMThreshold {
		t.Errorf("minBPM = %d, want default %d", cfg.MinBPMThreshold, defaultMinBPMThreshold)
	}
	if cfg.AutoLog {
		t.Error("auto_log should default to false")
	}
}

func TestParseConfigRejectsInvalidValues(t *testing.T) {
	// Wrong types and out-of-range values must each fall back to the default,
	// and the config must be flagged changed.
	data := `{"current":"AA","auto_log":"yes","session_gap_minutes":"oops","min_bpm_threshold":-5}`
	cfg, changed := parseConfig([]byte(data))
	if cfg == nil {
		t.Fatal("parseConfig returned nil")
	}
	if !changed {
		t.Error("changed = false, want true for invalid values")
	}
	if cfg.AutoLog {
		t.Error("invalid auto_log should fall back to false")
	}
	if cfg.SessionGapMinutes != defaultSessionGapMinutes {
		t.Errorf("invalid gap kept: %d", cfg.SessionGapMinutes)
	}
	if cfg.MinBPMThreshold != defaultMinBPMThreshold {
		t.Errorf("negative minBPM kept: %d", cfg.MinBPMThreshold)
	}
}

func TestParseConfigRejectsDecimalGap(t *testing.T) {
	// session_gap_minutes is a whole number; a decimal is invalid.
	cfg, changed := parseConfig([]byte(`{"session_gap_minutes":60.5}`))
	if cfg.SessionGapMinutes != defaultSessionGapMinutes {
		t.Errorf("decimal gap accepted: %d", cfg.SessionGapMinutes)
	}
	if !changed {
		t.Error("changed = false for decimal gap")
	}
}

func TestParseConfigAcceptsValidValues(t *testing.T) {
	// A complete, valid config (zero is a valid floor) round-trips unchanged.
	data := `{"current":"AA","known":[],"auto_log":true,"session_gap_minutes":30,"min_bpm_threshold":0,"itgmania_module":""}`
	cfg, changed := parseConfig([]byte(data))
	if cfg == nil {
		t.Fatal("parseConfig returned nil")
	}
	if changed {
		t.Error("changed = true for an already-complete valid config")
	}
	if cfg.SessionGapMinutes != 30 {
		t.Errorf("gap = %d, want 30", cfg.SessionGapMinutes)
	}
	if cfg.MinBPMThreshold != 0 {
		t.Errorf("minBPM = %d, want 0 (no floor)", cfg.MinBPMThreshold)
	}
	if cfg.minBPM() != 0 {
		t.Errorf("minBPM() = %d, want 0 (zero is a valid floor)", cfg.minBPM())
	}
	if !cfg.AutoLog {
		t.Error("auto_log lost")
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
