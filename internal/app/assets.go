package app

// Tray icons are embedded per platform: PNG for Linux and macOS
// (assets_unix.go), ICO for Windows (assets_windows.go). Both files declare the
// same five variables, so nothing that draws an icon has to know the format.
//
// The split is required, not cosmetic. Windows loads tray and menu icons through
// LoadImageW with LR_LOADFROMFILE, which reads ICO only; PNG bytes make systray
// log "unable to set icon" and the app runs iconless. go:embed cannot pick a
// file at runtime, so the choice has to happen at build-tag level.

// version is injected at build time via -ldflags "-X gotempo/internal/app.version=…" (see the
// Makefile, which derives it from `git describe`). It is "dev" for a plain
// `go build` with no ldflags.
var version = "dev"
