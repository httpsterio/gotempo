package app

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

// Connection phases reported in status.json. "connected:false" alone is
// uninformative, so phase distinguishes idle (no device), the finite connect
// retry, the persistent reconnect scan, and an active session.
const (
	phaseIdle         = "idle"
	phaseConnecting   = "connecting"
	phaseReconnecting = "reconnecting"
	phaseConnected    = "connected"
)

// appStatus is the cross-process status published to status.json by the running
// instance and read back by `gotempo --status`. It is maintained independent of
// the logging toggle (unlike gotempo-bpm.txt), so --status reflects the real app
// state. BPM is nil when not connected, or connected with no reading yet. Device
// is nil when none is configured.
type appStatus struct {
	Connected bool          `json:"connected"`
	Phase     string        `json:"phase"`
	Logging   bool          `json:"logging"`
	BPM       *int          `json:"bpm"`
	Device    *statusDevice `json:"device"`
	Updated   string        `json:"updated"`
}

type statusDevice struct {
	MAC  string `json:"mac"`
	Name string `json:"name"`
}

// writeStatus publishes the status atomically (temp file + rename) so a
// concurrent --status never reads a half-written file. Errors are logged and
// ignored, like the OBS-file writes below.
func writeStatus(st appStatus) {
	st.Updated = time.Now().Format(time.RFC3339)
	data, err := json.Marshal(st)
	if err != nil {
		logErrf("[status] marshal: %v", err)
		return
	}
	tmp, err := os.CreateTemp(dataDir(), "status-*.tmp")
	if err != nil {
		logErrf("[status] create temp: %v", err)
		return
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		logErrf("[status] write: %v", err)
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		logErrf("[status] close: %v", err)
		return
	}
	if err := os.Rename(tmpName, statusPath()); err != nil {
		os.Remove(tmpName)
		logErrf("[status] rename: %v", err)
	}
}

type AppState struct {
	mu         sync.Mutex
	connected  bool
	logging    bool
	phase      string
	statusBPM  *int // last reading regardless of logging; nil when not connected
	lastBPM    int
	hasBPM     bool
	staleTimer *time.Timer
}

func (s *AppState) snapshot() (connected, logging bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connected, s.logging
}

// statusView returns everything publishStatus needs in one lock.
func (s *AppState) statusView() (connected, logging bool, phase string, bpm *int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connected, s.logging, s.phase, s.statusBPM
}

// setPhase records the connection phase. A phase change always clears the
// status bpm (a fresh connection has no reading yet; any non-connected phase
// has none), so a stale value never lingers across a transition.
func (s *AppState) setPhase(p string) {
	s.mu.Lock()
	s.phase = p
	s.statusBPM = nil
	s.mu.Unlock()
}

// recordBPM stores the latest reading for status, regardless of logging.
func (s *AppState) recordBPM(bpm int) {
	s.mu.Lock()
	s.statusBPM = &bpm
	s.mu.Unlock()
}

func (s *AppState) setConnected(v bool) {
	s.mu.Lock()
	s.connected = v
	s.mu.Unlock()
}

func (s *AppState) setLogging(v bool) {
	s.mu.Lock()
	s.logging = v
	s.mu.Unlock()
}

func (s *AppState) onConnect() {
	s.mu.Lock()
	s.connected = true
	if s.staleTimer != nil {
		s.staleTimer.Stop()
		s.staleTimer = nil
	}
	s.mu.Unlock()
}

func (s *AppState) onDisconnect() {
	s.mu.Lock()
	s.connected = false
	if s.staleTimer != nil {
		s.staleTimer.Stop()
	}
	s.staleTimer = time.AfterFunc(staleBPMTimeout, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.connected {
			return
		}
		s.hasBPM = false
		if err := os.WriteFile(outputPath(), []byte{}, 0644); err != nil {
			logErrf("[BPM] could not clear output: %v", err)
		}
	})
	s.mu.Unlock()
}

func (s *AppState) onSwitch() {
	s.mu.Lock()
	s.connected = false
	s.mu.Unlock()
	s.clearOutput()
}

// clearOutput empties the OBS BPM file and resets the dedup/stale state, without
// changing the connection flag. Used on device switch and when logging is turned
// off, so the overlay goes blank immediately instead of freezing on the last
// value.
func (s *AppState) clearOutput() {
	s.mu.Lock()
	s.hasBPM = false
	if s.staleTimer != nil {
		s.staleTimer.Stop()
		s.staleTimer = nil
	}
	s.mu.Unlock()
	if err := os.WriteFile(outputPath(), []byte{}, 0644); err != nil {
		logErrf("[BPM] could not clear output: %v", err)
	}
}
