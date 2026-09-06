package app

import (
	"bytes"
	"runtime"
	"testing"
)

// The embedded icons must be in the format the platform's tray can actually
// load. Windows reads ICO only, and a PNG there fails at runtime with nothing
// but a log line, so the format is checked here rather than discovered on a
// user's desktop.
func TestEmbeddedIconFormat(t *testing.T) {
	icons := map[string][]byte{
		"disconnected":  imgDisconnected,
		"connected":     imgConnected,
		"running":       imgRunning,
		"dot-connected": imgDotConnected,
		"dot-none":      imgDotNone,
	}

	// ICO: reserved 0, type 1 (icon). PNG: the standard 8-byte signature.
	magic, format := []byte{0x00, 0x00, 0x01, 0x00}, "ICO"
	if runtime.GOOS != "windows" {
		magic, format = []byte("\x89PNG\r\n\x1a\n"), "PNG"
	}

	for name, data := range icons {
		if len(data) == 0 {
			t.Errorf("%s: embedded icon is empty", name)
			continue
		}
		if !bytes.HasPrefix(data, magic) {
			t.Errorf("%s: not a %s (first bytes %x)", name, format, data[:min(4, len(data))])
		}
	}
}
