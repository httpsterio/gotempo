package app

import "log"

// logLevel controls how much operational logging reaches stderr. error shows
// only failures; info adds lifecycle events (connect, session start/end, device);
// debug adds per-reading detail.
type logLevel int

const (
	logError logLevel = iota
	logInfo
	logDebug
)

// currentLogLevel gates the leveled helpers below. Default info. Set once at
// startup (from --log-level / --quiet) before any worker goroutine starts, then
// only read, so it needs no synchronization.
var currentLogLevel = logInfo

func setLogLevel(l logLevel) { currentLogLevel = l }

// parseLogLevel maps a --log-level value to a level; ok is false for an
// unrecognized value.
func parseLogLevel(s string) (logLevel, bool) {
	switch s {
	case "error":
		return logError, true
	case "info":
		return logInfo, true
	case "debug":
		return logDebug, true
	}
	return logInfo, false
}

// Error logs always print: a failure is worth showing at every level.
func logErrf(format string, args ...any) { log.Printf(format, args...) }
func logErrln(args ...any)               { log.Println(args...) }

// Info logs print at info and debug (lifecycle events).
func logInfof(format string, args ...any) {
	if currentLogLevel >= logInfo {
		log.Printf(format, args...)
	}
}
func logInfoln(args ...any) {
	if currentLogLevel >= logInfo {
		log.Println(args...)
	}
}

// Debug logs print only at debug (per-reading / verbose detail).
func logDebugf(format string, args ...any) {
	if currentLogLevel >= logDebug {
		log.Printf(format, args...)
	}
}
