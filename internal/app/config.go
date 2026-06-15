package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// configDir is the XDG config location: $XDG_CONFIG_HOME/gotempo or
// ~/.config/gotempo. It is portable (os.UserConfigDir resolves the right base
// per OS), so it stays in shared code; dataDir is the platform-specific one.
func configDir() string {
	base, err := os.UserConfigDir()
	if err != nil {
		return "."
	}
	return filepath.Join(base, "gotempo")
}

// configPathOverride, when set (by the --config flag), replaces the default
// config location for the whole process.
var configPathOverride string

func configPath() string {
	if configPathOverride != "" {
		return configPathOverride
	}
	return filepath.Join(configDir(), "config.json")
}
func logsDir() string    { return dataDir() }
func outputPath() string { return filepath.Join(logsDir(), "gotempo-bpm.txt") }

type KnownDevice struct {
	MAC      string `json:"mac"`
	Name     string `json:"name"`
	LastUsed string `json:"last_used,omitempty"` // RFC3339 UTC, set on successful connect
}

type Config struct {
	Current string        `json:"current"`
	Known   []KnownDevice `json:"known"`
	AutoLog bool          `json:"auto_log"` // start logging automatically on launch

	SessionGapMinutes int `json:"session_gap_minutes"` // gap that ends a CSV session
	MinBPMThreshold   int `json:"min_bpm_threshold"`   // readings below this are junk
}

// Defaults for the CSV session logger. No omitempty on the keys above, so a
// freshly written config always lists every tunable with its current value;
// parseConfig falls back to these for any key that is missing or invalid.
const (
	defaultSessionGapMinutes = 60
	defaultMinBPMThreshold   = 20
)

// defaultConfig is a complete config with every field at its default. First run
// (no file) and an unparseable file both start from this, and it is the base
// parseConfig fills in over.
func defaultConfig() *Config {
	return &Config{
		Current:           "",
		Known:             []KnownDevice{},
		AutoLog:           false,
		SessionGapMinutes: defaultSessionGapMinutes,
		MinBPMThreshold:   defaultMinBPMThreshold,
	}
}

// sessionGap is the idle span that ends a session, as a duration.
func (c Config) sessionGap() time.Duration {
	m := c.SessionGapMinutes
	if m <= 0 {
		m = defaultSessionGapMinutes
	}
	return time.Duration(m) * time.Minute
}

// minBPM is the validity floor; readings below it are ignored by the CSV log.
// Zero is a valid floor (log everything); only a negative value is treated as
// invalid and replaced by the default.
func (c Config) minBPM() int {
	if c.MinBPMThreshold < 0 {
		return defaultMinBPMThreshold
	}
	return c.MinBPMThreshold
}

func (c Config) clone() Config {
	out := Config{
		Current:           c.Current,
		AutoLog:           c.AutoLog,
		SessionGapMinutes: c.SessionGapMinutes,
		MinBPMThreshold:   c.MinBPMThreshold,
	}
	out.Known = append([]KnownDevice(nil), c.Known...)
	return out
}

// upsert adds the device if absent, or updates its name if a non-empty one is
// supplied. It never clears an existing last_used timestamp.
func (c *Config) upsert(mac, name string) {
	for i := range c.Known {
		if strings.EqualFold(c.Known[i].MAC, mac) {
			if name != "" {
				c.Known[i].Name = name
			}
			return
		}
	}
	c.Known = append(c.Known, KnownDevice{MAC: mac, Name: name})
}

// touch stamps the device's last_used with the current time, adding it if absent.
func (c *Config) touch(mac string) {
	now := time.Now().UTC().Format(time.RFC3339)
	for i := range c.Known {
		if strings.EqualFold(c.Known[i].MAC, mac) {
			c.Known[i].LastUsed = now
			return
		}
	}
	c.Known = append(c.Known, KnownDevice{MAC: mac, LastUsed: now})
}

// sortedKnown returns a copy of known devices ordered most-recently-used first.
// Never-used devices (empty last_used) sort last.
func (c Config) sortedKnown() []KnownDevice {
	ks := append([]KnownDevice(nil), c.Known...)
	sort.SliceStable(ks, func(i, j int) bool {
		return ks[i].LastUsed > ks[j].LastUsed
	})
	return ks
}

// parseConfig decodes config JSON over a default config, validating each field
// independently: a missing key, a wrong type, or an out-of-range value falls
// back to the default rather than failing the whole load. The legacy {mac, name}
// schema is migrated. The returned bool is true when the on-disk form differed
// from the validated result (missing keys, corrected values, migration), so the
// caller can write the cleaned-up config back. Returns (nil, false) only when
// the data is not valid JSON at all.
//
// A device-less config is still valid (selection happens via the tray) and is
// kept so preferences like auto_log survive.
func parseConfig(data []byte) (*Config, bool) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, false
	}
	cfg := defaultConfig()
	changed := false

	if v, ok := raw["current"]; ok {
		var s string
		if json.Unmarshal(v, &s) == nil {
			cfg.Current = s
		} else {
			changed = true
		}
	} else {
		changed = true
	}

	if v, ok := raw["known"]; ok {
		var ks []KnownDevice
		if json.Unmarshal(v, &ks) == nil {
			if ks != nil {
				cfg.Known = ks
			}
		} else {
			changed = true
		}
	} else {
		changed = true
	}

	if v, ok := raw["auto_log"]; ok {
		var b bool
		if json.Unmarshal(v, &b) == nil {
			cfg.AutoLog = b
		} else {
			changed = true
		}
	} else {
		changed = true
	}

	// Whole number of minutes, strictly positive. A decimal, a string, or a
	// non-positive value is rejected (kept at the default).
	if v, ok := raw["session_gap_minutes"]; ok {
		var n int
		if json.Unmarshal(v, &n) == nil && n > 0 {
			cfg.SessionGapMinutes = n
		} else {
			changed = true
		}
	} else {
		changed = true
	}

	// Integer bpm, zero or more (zero = no floor). Negative or non-integer is
	// rejected.
	if v, ok := raw["min_bpm_threshold"]; ok {
		var n int
		if json.Unmarshal(v, &n) == nil && n >= 0 {
			cfg.MinBPMThreshold = n
		} else {
			changed = true
		}
	} else {
		changed = true
	}

	// Migrate legacy {mac, name} schema, only when no current device is set.
	if cfg.Current == "" {
		var legacy struct {
			MAC  string `json:"mac"`
			Name string `json:"name"`
		}
		_ = json.Unmarshal(data, &legacy)
		if legacy.MAC != "" {
			cfg.Current = legacy.MAC
			cfg.upsert(legacy.MAC, legacy.Name)
			changed = true
		}
	}

	return cfg, changed
}

// loadConfig reads, validates, and defaults the config. It never returns nil:
// a missing or unparseable file yields a full default config. The bool is true
// when the caller should persist the result (file absent, JSON corrupt, or any
// field missing/invalid), so the on-disk config self-heals into a complete,
// well-formed file.
func loadConfig() (*Config, bool) {
	data, err := os.ReadFile(configPath())
	if err != nil {
		return defaultConfig(), true
	}
	cfg, changed := parseConfig(data)
	if cfg == nil {
		return defaultConfig(), true
	}
	return cfg, changed
}

func saveConfig(c Config) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath(), data, 0644)
}
