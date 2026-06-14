package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// sessionsDir holds the per-session CSV files, under the platform data dir
// alongside gotempo-bpm.txt.
func sessionsDir() string { return filepath.Join(dataDir(), "sessions") }

// SessionLogger writes valid BPM readings to per-session CSV files. A session
// is a run of contiguous valid readings in one file; a gap longer than
// gapThreshold starts a new file, a shorter one appends. Junk readings (below
// minBPM) are ignored entirely: not written, no session, no gap extension, so
// dropouts show up as gaps in the timestamp column rather than rows. Every
// write is flushed, so a file is always complete if read mid-session or after
// a crash. All methods are safe for concurrent use.
type SessionLogger struct {
	dir          string
	gapThreshold time.Duration
	minBPM       int

	mu          sync.Mutex
	currentFile *os.File
	lastValid   time.Time
}

func newSessionLogger(dir string, gap time.Duration, minBPM int) *SessionLogger {
	return &SessionLogger{dir: dir, gapThreshold: gap, minBPM: minBPM}
}

// LogReading records one reading. Junk (bpm < minBPM) is dropped.
func (s *SessionLogger) LogReading(t time.Time, bpm int) error {
	if bpm < s.minBPM {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.currentFile != nil && t.Sub(s.lastValid) > s.gapThreshold {
		s.endSession()
	}
	if s.currentFile == nil {
		if err := s.openSession(t); err != nil {
			return err
		}
	}
	s.lastValid = t
	return s.writeLine(t, bpm)
}

// checkGap closes an idle session whose last reading is older than the gap. Run
// it periodically so a dead connection (no readings at all) still ends the
// session promptly. Correctness-wise it is cosmetic, since every write flushes;
// it only frees the handle and forces a clean new file when readings resume.
func (s *SessionLogger) checkGap(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.currentFile != nil && now.Sub(s.lastValid) > s.gapThreshold {
		s.endSession()
	}
}

// Close ends the current session, releasing the file handle. The file is
// already complete; this just frees the handle when logging is toggled off or
// the app quits. A later reading reopens via the gap rule.
func (s *SessionLogger) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.endSession()
}

func (s *SessionLogger) writeLine(t time.Time, bpm int) error {
	if _, err := fmt.Fprintf(s.currentFile, "%s,%d\n", t.Format(time.RFC3339), bpm); err != nil {
		return err
	}
	return s.currentFile.Sync() // flush: file stays complete if read mid-session or after a crash
}

// openSession resumes the most recent file if its last reading is within the
// gap, otherwise creates a new one. The same rule covers app restart and
// toggling logging back on. Caller holds s.mu.
func (s *SessionLogger) openSession(t time.Time) error {
	if err := os.MkdirAll(s.dir, 0755); err != nil {
		return err
	}
	if last, path, ok := mostRecentSession(s.dir); ok && t.Sub(last) <= s.gapThreshold {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return err
		}
		s.currentFile = f
		s.lastValid = last
		log.Printf("[CSV] resuming session %s", filepath.Base(path))
		return nil
	}
	name := t.Format("2006-01-02T15-04-05") + ".csv"
	f, err := os.Create(filepath.Join(s.dir, name))
	if err != nil {
		return err
	}
	if _, err := f.WriteString("timestamp,bpm\n"); err != nil {
		f.Close()
		return err
	}
	s.currentFile = f
	log.Printf("[CSV] new session %s", name)
	return nil
}

// endSession closes the open file, if any. Caller holds s.mu.
func (s *SessionLogger) endSession() {
	if s.currentFile != nil {
		s.currentFile.Close()
		s.currentFile = nil
	}
}

// mostRecentSession returns the timestamp of the last data row and the path of
// the newest session file that actually contains data. ok is false only when
// no session file with data exists.
//
// "Most recent" is just the lexicographically-last filename: filenames are
// "2006-01-02T15-04-05", so a descending string sort is chronological order,
// across days too. (The one wrinkle is a fall-back DST hour, where a name can
// sort an hour out of order once a year; harmless here, it only affects which
// file resume considers, not data integrity.)
//
// Header-only files are skipped, so a crash orphan (a run that opened a file
// but died before the first valid reading) never blocks resume: the loop falls
// through to the next-newest file with data. The orphan is left on disk; a
// retention pass can prune zero-data files later.
func mostRecentSession(dir string) (last time.Time, path string, ok bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return time.Time{}, "", false
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".csv") {
			names = append(names, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	for _, name := range names {
		p := filepath.Join(dir, name)
		if t, ok := lastTimestamp(p); ok {
			return t, p, true
		}
		// header-only / unparseable: skip to the next-newest
	}
	return time.Time{}, "", false
}

// lastTimestamp returns the timestamp from the final data row of a session
// file. ok is false for an empty or header-only file. Sessions are bounded
// (a few thousand rows), so reading the whole file is fine; switch to a
// tail-seek if that ever stops holding.
func lastTimestamp(path string) (time.Time, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}, false
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if line == "" || line == "timestamp,bpm" {
			continue
		}
		field, _, _ := strings.Cut(line, ",")
		if t, err := time.Parse(time.RFC3339, field); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
