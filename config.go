package main

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

func configPath() string { return filepath.Join(configDir(), "config.json") }
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
	AutoLog bool          `json:"auto_log,omitempty"` // start logging automatically on launch

	SessionGapMinutes int `json:"session_gap_minutes,omitempty"` // gap that ends a CSV session
	MinBPMThreshold   int `json:"min_bpm_threshold,omitempty"`   // readings below this are junk
}

// Defaults for the CSV session logger, applied when the config value is unset
// (zero) so a hand-edited config only needs the keys it wants to override.
const (
	defaultSessionGapMinutes = 60
	defaultMinBPMThreshold   = 20
)

// sessionGap is the idle span that ends a session, as a duration.
func (c Config) sessionGap() time.Duration {
	m := c.SessionGapMinutes
	if m <= 0 {
		m = defaultSessionGapMinutes
	}
	return time.Duration(m) * time.Minute
}

// minBPM is the validity floor; readings below it are ignored by the CSV log.
func (c Config) minBPM() int {
	if c.MinBPMThreshold <= 0 {
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

// parseConfig decodes config JSON, migrating the legacy {mac, name} schema.
// Returns nil if the data is unusable (unparseable or no current device).
func parseConfig(data []byte) *Config {
	var raw struct {
		Current           string        `json:"current"`
		Known             []KnownDevice `json:"known"`
		AutoLog           bool          `json:"auto_log"`
		SessionGapMinutes int           `json:"session_gap_minutes"`
		MinBPMThreshold   int           `json:"min_bpm_threshold"`
		MAC               string        `json:"mac"`  // legacy
		Name              string        `json:"name"` // legacy
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	cfg := &Config{
		Current:           raw.Current,
		Known:             raw.Known,
		AutoLog:           raw.AutoLog,
		SessionGapMinutes: raw.SessionGapMinutes,
		MinBPMThreshold:   raw.MinBPMThreshold,
	}
	// Migrate legacy {mac, name} schema.
	if cfg.Current == "" && raw.MAC != "" {
		cfg.Current = raw.MAC
		cfg.upsert(raw.MAC, raw.Name)
	}
	// A device-less config is still valid (selection happens via the tray) and
	// must be kept so preferences like auto_log survive.
	return cfg
}

func loadConfig() *Config {
	data, err := os.ReadFile(configPath())
	if err != nil {
		return nil
	}
	return parseConfig(data)
}

func saveConfig(c Config) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath(), data, 0644)
}
