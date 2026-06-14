package app

import _ "embed"

//go:embed assets/disconnected.png
var imgDisconnected []byte

//go:embed assets/connected.png
var imgConnected []byte

//go:embed assets/running.png
var imgRunning []byte

// Per-row indicator for the device list: a green dot on the connected device,
// and a transparent placeholder on the rest so the icon column stays aligned.
//
//go:embed assets/dot-connected.png
var imgDotConnected []byte

//go:embed assets/dot-none.png
var imgDotNone []byte

// version is injected at build time via -ldflags "-X gotempo/internal/app.version=…" (see the
// Makefile, which derives it from `git describe`). It is "dev" for a plain
// `go build` with no ldflags.
var version = "dev"
