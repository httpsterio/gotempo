//go:build windows

package app

import _ "embed"

// The ICO files are generated from the PNGs of the same name. The status icons
// carry 16/24/32/48/64/128 px frames so Windows can pick the one that fits the
// notification area and the DPI; the row dots are 16 px only, which is the size
// a menu item bitmap is drawn at.

//go:embed assets/disconnected.ico
var imgDisconnected []byte

//go:embed assets/connected.ico
var imgConnected []byte

//go:embed assets/running.ico
var imgRunning []byte

// Per-row indicator for the device list: a green dot on the connected device,
// and a transparent placeholder on the rest so the icon column stays aligned.
//
//go:embed assets/dot-connected.ico
var imgDotConnected []byte

//go:embed assets/dot-none.ico
var imgDotNone []byte
