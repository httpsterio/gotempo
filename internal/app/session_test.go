package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// readSessions returns the .csv files in dir, sorted, with their contents.
func readSessions(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return out
		}
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".csv") {
			data, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			out[e.Name()] = string(data)
		}
	}
	return out
}

func TestSessionLoggerDropsJunk(t *testing.T) {
	dir := t.TempDir()
	s := newSessionLogger(dir, nil, time.Hour, 20, true)
	base := time.Date(2026, 6, 8, 14, 0, 0, 0, time.UTC)

	if err := s.LogReading(base, 5); err != nil { // junk: below threshold
		t.Fatal(err)
	}
	if files := readSessions(t, dir); len(files) != 0 {
		t.Fatalf("junk before any valid reading created a file: %v", files)
	}

	if err := s.LogReading(base.Add(time.Second), 72); err != nil {
		t.Fatal(err)
	}
	if err := s.LogReading(base.Add(2*time.Second), 4); err != nil { // junk mid-session
		t.Fatal(err)
	}
	if err := s.LogReading(base.Add(3*time.Second), 73); err != nil {
		t.Fatal(err)
	}
	s.Close()

	files := readSessions(t, dir)
	if len(files) != 1 {
		t.Fatalf("want 1 session file, got %d: %v", len(files), files)
	}
	for _, body := range files {
		if strings.Contains(body, ",4\n") || strings.Contains(body, ",5\n") {
			t.Errorf("junk reading written to file:\n%s", body)
		}
		if got := strings.Count(body, "\n"); got != 3 { // header + 2 valid rows
			t.Errorf("want header + 2 rows, got %d lines:\n%s", got, body)
		}
	}
}

func TestSessionLoggerGapStartsNewFile(t *testing.T) {
	dir := t.TempDir()
	s := newSessionLogger(dir, nil, time.Hour, 20, true)
	base := time.Date(2026, 6, 8, 14, 0, 0, 0, time.UTC)

	if err := s.LogReading(base, 70); err != nil {
		t.Fatal(err)
	}
	// Within the gap: appends to the same file.
	if err := s.LogReading(base.Add(30*time.Minute), 71); err != nil {
		t.Fatal(err)
	}
	if files := readSessions(t, dir); len(files) != 1 {
		t.Fatalf("reading within gap should not start a new file: %v", files)
	}
	// Past the gap: new file.
	if err := s.LogReading(base.Add(2*time.Hour), 72); err != nil {
		t.Fatal(err)
	}
	if files := readSessions(t, dir); len(files) != 2 {
		t.Fatalf("reading past gap should start a new file, got: %v", files)
	}
}

// A fresh logger (simulating restart or toggle-on) resumes the latest file when
// the next reading is within the gap, and starts a new one when it is not.
func TestSessionLoggerResume(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 6, 8, 14, 0, 0, 0, time.UTC)

	s1 := newSessionLogger(dir, nil, time.Hour, 20, true)
	if err := s1.LogReading(base, 70); err != nil {
		t.Fatal(err)
	}
	s1.Close()
	first := onlyName(t, dir)

	// Restart, reading within gap → same file.
	s2 := newSessionLogger(dir, nil, time.Hour, 20, true)
	if err := s2.LogReading(base.Add(10*time.Minute), 71); err != nil {
		t.Fatal(err)
	}
	s2.Close()
	if files := readSessions(t, dir); len(files) != 1 {
		t.Fatalf("resume within gap should reuse the file, got: %v", files)
	}

	// Restart, reading past gap → new file.
	s3 := newSessionLogger(dir, nil, time.Hour, 20, true)
	if err := s3.LogReading(base.Add(3*time.Hour), 72); err != nil {
		t.Fatal(err)
	}
	s3.Close()
	if files := readSessions(t, dir); len(files) != 2 {
		t.Fatalf("resume past gap should start a new file, got: %v", files)
	}
	if _, ok := readSessions(t, dir)[first]; !ok {
		t.Errorf("original session file %q disappeared", first)
	}
}

// A header-only crash orphan must not block resume: the next reading within the
// gap should resume the older file with data, not the empty newest one.
func TestSessionLoggerSkipsEmptyOrphan(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 6, 8, 14, 0, 0, 0, time.UTC)

	// Real session with data.
	withData := filepath.Join(dir, base.Format("2006-01-02T15-04-05")+".csv")
	if err := os.WriteFile(withData, []byte("timestamp,bpm\n"+base.Format(time.RFC3339)+",70\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Newer header-only orphan.
	orphanTime := base.Add(5 * time.Minute)
	orphan := filepath.Join(dir, orphanTime.Format("2006-01-02T15-04-05")+".csv")
	if err := os.WriteFile(orphan, []byte("timestamp,bpm\n"), 0644); err != nil {
		t.Fatal(err)
	}

	last, path, ok := mostRecentSession(dir, "")
	if !ok {
		t.Fatal("mostRecentSession found nothing")
	}
	if filepath.Base(path) != filepath.Base(withData) {
		t.Errorf("resumed %q, want the file with data %q", filepath.Base(path), filepath.Base(withData))
	}
	if !last.Equal(base) {
		t.Errorf("last = %v, want %v", last, base)
	}
}

// A disabled logger ignores readings, and toggling off mid-session closes the
// file without dropping the data already written. This guards the toggle-off
// race: a reading arriving after setEnabled(false) must not reopen a session.
func TestSessionLoggerDisabled(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 6, 8, 14, 0, 0, 0, time.UTC)

	// Created disabled: readings are no-ops, no file appears.
	off := newSessionLogger(dir, nil, time.Hour, 20, false)
	if err := off.LogReading(base, 70); err != nil {
		t.Fatal(err)
	}
	if files := readSessions(t, dir); len(files) != 0 {
		t.Fatalf("disabled logger wrote a file: %v", files)
	}

	// Enabled, write, then toggle off and replay a stray reading: it must not
	// create a new file or add a row.
	s := newSessionLogger(dir, nil, time.Hour, 20, true)
	if err := s.LogReading(base, 70); err != nil {
		t.Fatal(err)
	}
	if err := s.LogReading(base.Add(time.Second), 71); err != nil {
		t.Fatal(err)
	}
	s.setEnabled(false)
	if err := s.LogReading(base.Add(2*time.Second), 72); err != nil {
		t.Fatal(err)
	}
	files := readSessions(t, dir)
	if len(files) != 1 {
		t.Fatalf("want exactly one file after toggle-off, got: %v", files)
	}
	for _, body := range files {
		if got := strings.Count(body, "\n"); got != 3 { // header + 2 rows, no stray
			t.Errorf("want header + 2 rows (no post-toggle row), got %d lines:\n%s", got, body)
		}
	}

	// Toggle back on within the gap resumes the same file.
	s.setEnabled(true)
	if err := s.LogReading(base.Add(3*time.Second), 73); err != nil {
		t.Fatal(err)
	}
	if files := readSessions(t, dir); len(files) != 1 {
		t.Fatalf("toggle-on within gap should resume, got: %v", files)
	}
}

func onlyName(t *testing.T, dir string) string {
	t.Helper()
	files := readSessions(t, dir)
	if len(files) != 1 {
		t.Fatalf("want exactly one file, got %d: %v", len(files), files)
	}
	for name := range files {
		return name
	}
	return ""
}

// Every strap logs into one directory, so a logger must never adopt another's
// file. This is the one place the two-player naming can silently corrupt data:
// resuming across suffixes would interleave two people's readings into one CSV.
func TestSessionMatchesIsAnchoredNotSuffixed(t *testing.T) {
	cases := []struct {
		name, suffix string
		want         bool
	}{
		{"2026-06-08T14-00-00.csv", "", true},
		{"2026-06-08T14-00-00-p1.csv", "-p1", true},
		{"2026-06-08T14-00-00-p2.csv", "-p2", true},

		// The trap: HasSuffix(".csv") would make all of these true.
		{"2026-06-08T14-00-00-p1.csv", "", false},
		{"2026-06-08T14-00-00-p2.csv", "", false},
		{"2026-06-08T14-00-00.csv", "-p1", false},
		{"2026-06-08T14-00-00-p2.csv", "-p1", false},

		{"notatimestamp.csv", "", false},
		{"2026-06-08T14-00-00.txt", "", false},
	}
	for _, c := range cases {
		if got := sessionMatches(c.name, c.suffix); got != c.want {
			t.Errorf("sessionMatches(%q, %q) = %v, want %v", c.name, c.suffix, got, c.want)
		}
	}
}

// sessionName and sessionMatches must agree, or a logger cannot find the file it
// just wrote and every session opens a new one.
func TestSessionNameRoundTrips(t *testing.T) {
	now := time.Date(2026, 6, 8, 14, 0, 0, 0, time.UTC)
	for _, suffix := range []string{"", "-p1", "-p2"} {
		name := sessionName(now, suffix)
		if !sessionMatches(name, suffix) {
			t.Errorf("sessionName(%q) = %q, which sessionMatches rejects", suffix, name)
		}
	}
}

// A second strap's logger must not resume the first strap's file, even though
// that file is newer and sits in the same directory.
func TestSessionLoggerDoesNotResumeAnotherSlot(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 6, 8, 14, 0, 0, 0, time.UTC)

	p1 := newSessionLogger(dir, func() string { return "-p1" }, time.Hour, 20, true)
	if err := p1.LogReading(base, 150); err != nil {
		t.Fatal(err)
	}
	p1.Close()

	// One minute later, well inside the gap, so resume is what would happen if
	// the filter were not slot-aware.
	p2 := newSessionLogger(dir, func() string { return "-p2" }, time.Hour, 20, true)
	if err := p2.LogReading(base.Add(time.Minute), 88); err != nil {
		t.Fatal(err)
	}
	p2.Close()

	names := sessionFileNames(t, dir)
	if len(names) != 2 {
		t.Fatalf("got %d session files (%v), want one per strap", len(names), names)
	}
	for _, want := range []string{"-p1.csv", "-p2.csv"} {
		found := false
		for _, n := range names {
			if strings.HasSuffix(n, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("no %s file in %v", want, names)
		}
	}
}

// The reverse direction: turning two-player mode off means the next session is
// unsuffixed, and it must not resume a -p1 file.
func TestUnsuffixedLoggerDoesNotResumeSlotFile(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 6, 8, 14, 0, 0, 0, time.UTC)

	two := newSessionLogger(dir, func() string { return "-p1" }, time.Hour, 20, true)
	if err := two.LogReading(base, 150); err != nil {
		t.Fatal(err)
	}
	two.Close()

	one := newSessionLogger(dir, nil, time.Hour, 20, true)
	if err := one.LogReading(base.Add(time.Minute), 151); err != nil {
		t.Fatal(err)
	}
	one.Close()

	if names := sessionFileNames(t, dir); len(names) != 2 {
		t.Errorf("got %d session files (%v), want 2", len(names), names)
	}
}

func sessionFileNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".csv") {
			out = append(out, e.Name())
		}
	}
	return out
}

// The resume rule is a time gap, which answers "is this the same workout" but
// not "is this the same person". Two players swapping inside the gap would
// otherwise have their readings appended into one CSV with nothing marking the
// handover, so a claim change breaks the session.
func TestBreakSessionForcesANewFile(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 6, 8, 14, 0, 0, 0, time.UTC)

	s := newSessionLogger(dir, nil, time.Hour, 20, true)
	if err := s.LogReading(base, 150); err != nil {
		t.Fatal(err)
	}

	s.breakSession()

	// One minute later, well inside the gap, so this would resume without the break.
	if err := s.LogReading(base.Add(time.Minute), 88); err != nil {
		t.Fatal(err)
	}
	s.Close()

	if names := sessionFileNames(t, dir); len(names) != 2 {
		t.Errorf("got %d session files (%v), want a fresh one after the handover", len(names), names)
	}
}

// The break is one-shot: an ordinary gap-based resume must still work
// afterwards, or every session after a handover would be a new file.
func TestBreakSessionIsOneShot(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 6, 8, 14, 0, 0, 0, time.UTC)

	s := newSessionLogger(dir, nil, time.Hour, 20, true)
	if err := s.LogReading(base, 150); err != nil {
		t.Fatal(err)
	}
	s.breakSession()
	if err := s.LogReading(base.Add(time.Minute), 88); err != nil {
		t.Fatal(err)
	}
	s.Close() // closes the handle; the next reading resumes by the gap rule

	if err := s.LogReading(base.Add(2*time.Minute), 89); err != nil {
		t.Fatal(err)
	}
	s.Close()

	if names := sessionFileNames(t, dir); len(names) != 2 {
		t.Errorf("got %d session files (%v), want the break to apply once only", len(names), names)
	}
}
