package main

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
	s := newSessionLogger(dir, time.Hour, 20, true)
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
	s := newSessionLogger(dir, time.Hour, 20, true)
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

	s1 := newSessionLogger(dir, time.Hour, 20, true)
	if err := s1.LogReading(base, 70); err != nil {
		t.Fatal(err)
	}
	s1.Close()
	first := onlyName(t, dir)

	// Restart, reading within gap → same file.
	s2 := newSessionLogger(dir, time.Hour, 20, true)
	if err := s2.LogReading(base.Add(10*time.Minute), 71); err != nil {
		t.Fatal(err)
	}
	s2.Close()
	if files := readSessions(t, dir); len(files) != 1 {
		t.Fatalf("resume within gap should reuse the file, got: %v", files)
	}

	// Restart, reading past gap → new file.
	s3 := newSessionLogger(dir, time.Hour, 20, true)
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

	last, path, ok := mostRecentSession(dir)
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
	off := newSessionLogger(dir, time.Hour, 20, false)
	if err := off.LogReading(base, 70); err != nil {
		t.Fatal(err)
	}
	if files := readSessions(t, dir); len(files) != 0 {
		t.Fatalf("disabled logger wrote a file: %v", files)
	}

	// Enabled, write, then toggle off and replay a stray reading: it must not
	// create a new file or add a row.
	s := newSessionLogger(dir, time.Hour, 20, true)
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
