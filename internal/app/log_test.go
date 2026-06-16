package app

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

func TestParseLogLevel(t *testing.T) {
	for s, want := range map[string]logLevel{"error": logError, "info": logInfo, "debug": logDebug} {
		if got, ok := parseLogLevel(s); !ok || got != want {
			t.Errorf("parseLogLevel(%q) = %v, %v; want %v, true", s, got, ok, want)
		}
	}
	if _, ok := parseLogLevel("loud"); ok {
		t.Error("parseLogLevel(loud) ok = true, want false")
	}
}

func TestLogLevelGating(t *testing.T) {
	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(old)
	defer setLogLevel(currentLogLevel)

	emit := func() string {
		buf.Reset()
		logErrf("err-x")
		logInfof("info-x")
		logDebugf("debug-x")
		return buf.String()
	}

	setLogLevel(logError)
	out := emit()
	if !strings.Contains(out, "err-x") || strings.Contains(out, "info-x") || strings.Contains(out, "debug-x") {
		t.Errorf("error level: got %q, want only err-x", out)
	}

	setLogLevel(logInfo)
	out = emit()
	if !strings.Contains(out, "err-x") || !strings.Contains(out, "info-x") || strings.Contains(out, "debug-x") {
		t.Errorf("info level: got %q, want err-x+info-x, not debug-x", out)
	}

	setLogLevel(logDebug)
	out = emit()
	if !strings.Contains(out, "err-x") || !strings.Contains(out, "info-x") || !strings.Contains(out, "debug-x") {
		t.Errorf("debug level: got %q, want all three", out)
	}
}
