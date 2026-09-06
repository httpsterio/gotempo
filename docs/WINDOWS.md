# Windows support plan

Status: implemented. Kept as the record of what was investigated, what was
decided, and why, so the next platform (macOS) does not repeat the work.

It started from two questions:

1. What needs to be installed on Windows so the project compiles?
2. What code changes are necessary to support building a Windows version?

## Findings

### 1. What to install on Windows

Almost nothing. The BLE backend differs by OS: Linux uses BlueZ over D-Bus,
Windows uses the WinRT Bluetooth APIs. The Windows backend is implemented in
`tinygo.org/x/bluetooth` via two pure-Go dependencies that are already in the
module graph:

- `github.com/go-ole/go-ole` (WinRT/COM bindings)
- `github.com/saltosystems/winrt-go` (generated WinRT interfaces)

Both are pure Go, no cgo, no `import "C"`, no C compiler required (verified in
the module cache: neither module contains a single cgo import; `go-ole` loads
`RoInitialize` and friends from `combase.dll` via `syscall`/`unsafe`).

Concrete requirement list:

- **Go 1.24+** (already required by `go.mod`; the same version as Linux).
- **Windows 10+ at runtime** (the WinRT BLE APIs it uses).
- **No C compiler, no Windows SDK, no MinGW, no BlueZ.**
- **`CGO_ENABLED=0` works.** The existing release workflow already sets it.

Cross-compilation from Linux with `GOOS=windows` also works (verified: the only
build errors are the missing platform files, listed below). Native Windows builds
work too.

The upstream README confirms: "Only the Go compiler itself is needed to compile
Go Bluetooth code targeting Windows."

### 2. Code changes required

The codebase was already split for this. `docs/PLAN.md` and `CONTRIBUTING.md`
describe a "platform contract", a set of functions that shared code calls by
name, implemented per-OS in files with `_<os>.go` suffixes. Linux implements them
in `internal/app/platform_linux.go`; the instance lock is `internal/app/lock_unix.go`
gated by `//go:build unix`. Shared code never branches on the OS.

Before the platform files existed, cross-compiling produced exactly these
errors, which is what mapped the gap:

```
internal/app/ble.go:229:22: undefined: openAdapter
internal/app/ble.go:497:4:  undefined: notify
internal/app/ble.go:663:3:  undefined: notify
internal/app/cli.go:154:9:  undefined: enableAutostart
internal/app/cli.go:156:9:  undefined: disableAutostart
internal/app/cli.go:205:17: undefined: acquireInstanceLock
internal/app/config.go:33:35: undefined: dataDir
internal/app/config.go:38:49: undefined: dataDir
internal/app/run.go:97:17:   undefined: acquireInstanceLock
internal/app/run.go:109:24:  undefined: dataDir
internal/app/tray.go:196:4:  undefined: openFolder
internal/app/tray.go:199:4:  undefined: openFolder
internal/app/tray.go:212:24: undefined: disableAutostart
internal/app/tray.go:218:23: undefined: enableAutostart
internal/app/run.go:244:31: undefined: autostartEnabled
```

(Go stops at ten errors per package, so a single run shows a truncated list.)

The platform contract (call sites in shared code):

| Symbol | Shared call sites |
|---|---|
| `dirs` (a `dirLayout` value, see below) | `config.go`, via `configDir()` and `dataDir()` |
| `notify(msg)` | `ble.go` (device lost / reconnected) |
| `openFolder(dir)` | `tray.go` (open log/config folder) |
| `enableAutostart()` / `disableAutostart()` / `autostartEnabled()` | `tray.go`, `cli.go` (`cmdAutostart`), `run.go` |
| `openAdapter()` | `ble.go` (`ensureAdapter`) |
| `acquireInstanceLock()` | `run.go`, `cli.go` (`cmdStatus`) |

`dataDir()` was a per-OS function, which is why it appears in that error list. It
became shared as part of the directory-layout change below, and the per-OS `dirs`
value took its place in the contract.

`stdinIsTerminal()` (`device.go:31`) is **not** part of the contract and does not
need to move. `os.Stdin.Stat()` reports `ModeCharDevice` for a Windows console:
Go maps `FILE_TYPE_CHAR` to `ModeDevice|ModeCharDevice`
(`$GOROOT/src/os/types_windows.go:202` and `:254`, asserted by
`os/os_windows_test.go`). The existing implementation works on Windows unchanged.

### 3. Connect-by-address carries over unchanged

gotempo reconnects by address rather than scanning (see the reconnection section
in the README), so this had to be confirmed rather than assumed. It works on
Windows with no code change:

- `Address` embeds `MACAddress` on Windows too (`gap_windows.go:20`), so
  `bluetooth.Address{MACAddress: ...}` at `ble.go:430` compiles as-is.
- `Adapter.Connect` (`gap_windows.go:334`) resolves the MAC through
  `BluetoothLEDeviceFromBluetoothAddressAsync`, which does not require a prior
  scan. It returns "device with the given address was not found" when the device
  is unknown to the system, which the existing retry loop already treats as a
  normal failed attempt.

## Decisions

- **Notifications**: `notify` is a **no-op on Windows** (matches Linux "skipped if
  missing"; no dependencies).
- **Autostart**: **Registry Run key** (`HKCU\Software\Microsoft\Windows\CurrentVersion\Run`),
  pure Go via `golang.org/x/sys/windows/registry`.
- **CI/release**: **one job**, cross-compiled from the existing Linux runner.
- **Binaries**: **two**, a GUI one and a console one (see below).
- **Directories**: **one folder** for config and data on Windows (see below).

## Changes

### New file: `internal/app/platform_windows.go`

Implements the platform contract for Windows:

- `dirs` → `LOCALAPPDATA` for both config and data (see the directory section).
- `notify(msg)` → no-op (return immediately).
- `openFolder(dir)` → `exec.Command("explorer.exe", dir).Start()`. Note `.Start()`,
  not `.Run()`: explorer exits non-zero even on success.
- `autostartEnabled()` → read the Run key value.
- `enableAutostart()` → write `gotempo` = quoted `os.Executable()` path to the
  Run key (`registry.CreateKey` + `SetStringValue`).
- `disableAutostart()` → delete the value if present; treat a missing key/value
  as success.
- `openAdapter()` → `return bluetooth.DefaultAdapter, "default", nil`.

  **Important:** `bluetooth.NewAdapter(id)` only exists on Linux
  (`adapter_linux.go:34`). Windows (and macOS) use the package-level
  `bluetooth.DefaultAdapter` (`adapter_windows.go:28`), and `Enable()` is what
  initializes the WinRT stack (`ole.RoInitialize`).

  `openAdapter` must call `Enable()` itself. `ensureAdapter` (`ble.go:220`) only
  re-`Enable`s an adapter it already holds; an adapter returned fresh from
  `openAdapter` is expected to be live, which is what the Linux implementation
  does too. Returning `DefaultAdapter` without enabling it would leave the WinRT
  stack uninitialized.

### New file: `internal/app/lock_windows.go` (`//go:build windows`)

- `acquireInstanceLock()` → named mutex:
  - `windows.CreateMutex(nil, false, "gotempo-instance")`
  - already running when `windows.GetLastError() == windows.ERROR_ALREADY_EXISTS`
    → return `(nil, false)`.
  - release func → `windows.CloseHandle(handle)`.

  The mutex name must be a fixed string. Both shipped binaries use it to find each
  other (`gotempo-cli.exe --status` detects the running tray app through this
  lock), so it must not be derived from the executable name or path.

  Windows has no `flock`; `lock_unix.go` is already tagged `unix` so it is
  correctly excluded from Windows builds.

### Directory layout: one folder on Windows

On Windows, config and data both live in `%LOCALAPPDATA%\gotempo`: `config.json`,
the session CSVs, `gotempo-bpm.txt` and `status.json`. Not Roaming, since logs
should not follow a roaming profile.

Reaching that means replacing the current arrangement, where `dataDir()` is
per-OS and `configDir()` is shared and hardcoded to `os.UserConfigDir()` (which
returns Roaming `%AppData%` on Windows). Left alone it would put `config.json` in
a different tree from everything else.

Both become shared functions over a per-OS table, so the resolution logic is
identical on every platform and the platform files hold data only:

```go
// internal/app/config.go
const appName = "gotempo"

type dirLayout struct {
	configEnv, dataEnv string   // env var that overrides, "" if none
	configRel, dataRel []string // path under the home dir otherwise
}

func resolve(env string, rel []string) string {
	if env != "" {
		if v := os.Getenv(env); v != "" {
			return filepath.Join(v, appName)
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, filepath.Join(rel...), appName)
}

func configDir() string { return resolve(dirs.configEnv, dirs.configRel) }
func dataDir() string   { return resolve(dirs.dataEnv, dirs.dataRel) }
```

```go
// internal/app/platform_linux.go
var dirs = dirLayout{
	configEnv: "XDG_CONFIG_HOME", configRel: []string{".config"},
	dataEnv:   "XDG_DATA_HOME",   dataRel:   []string{".local", "share"},
}

// internal/app/platform_windows.go
var dirs = dirLayout{
	configEnv: "LOCALAPPDATA", configRel: []string{"AppData", "Local"},
	dataEnv:   "LOCALAPPDATA", dataRel:   []string{"AppData", "Local"},
}
```

Adding macOS later is three lines of data, not a third implementation.

Notes:

- Linux behaviour is preserved exactly: `XDG_CONFIG_HOME` with a `~/.config`
  fallback, `XDG_DATA_HOME` with `~/.local/share`.
- `os.UserConfigDir()` leaves the codebase. It was doing this same table lookup
  internally; spelling it out is what makes the Windows collapse expressible.
- Resolution stays per call. Do not cache the paths in package-level variables
  filled by `init()`: `itgmania_test.go:138`, `cli_test.go:105` and
  `logging_test.go:12` relocate the data dir at runtime with
  `t.Setenv("XDG_DATA_HOME", ...)`.
- `lock_unix.go:23` falls back to `configDir()` when `XDG_RUNTIME_DIR` is empty.
  That file does not compile on Windows, so it is unaffected.
- This is the one shared-code change in the plan. It moves two functions and
  changes no Linux behaviour.

### Tray icons: Windows needs ICO

`internal/app/assets/` holds PNGs, embedded in `assets.go`. On Windows both
`systray.SetIcon` and `MenuItem.SetIcon` reach `LoadImageW` with
`IMAGE_ICON | LR_LOADFROMFILE` (`systray_windows.go:985-1001`), which reads ICO
only. PNG bytes make systray log `unable to set icon` and the app runs with no
icon, on the tray item and on every device row.

Six call sites are affected: `tray.go:102`, `:104`, `:106` (status icon),
`tray.go:150`, `:152` (device row dots) and `run.go:208`.

- Add ICO versions of the five embedded icons: `disconnected`, `connected`,
  `running`, `dot-connected`, `dot-none`.
- Split `assets.go` into `assets_windows.go` and `assets_unix.go`, each embedding
  its own format and exporting the same five variable names. `go:embed` cannot
  branch at runtime, so this has to happen at build-tag level.
- No call-site changes.

### Two binaries: GUI and console

A Windows GUI binary built without `-H=windowsgui` opens a console window on every
launch, which is wrong for a tray app. With `-H=windowsgui` the binary does not
attach to the parent console, so `--status` and `--print-bpm` run from PowerShell
print into nothing.

Ship both, from the same `package main` and the same ldflags otherwise:

- `gotempo.exe`, built with `-ldflags "-H=windowsgui ..."`, the tray app.
- `gotempo-cli.exe`, built without it, for `--status`, `--print-bpm`, `--no-tray`
  and the setup flags.

No code change, one extra build step. They coordinate through the named mutex in
`lock_windows.go`.

### Edit: `Makefile`

`make windows` cross-compiles both binaries with the same flags the release
workflow uses, so the two cannot drift. `make clean` removes them.

### Edit: `go.mod`

- Promote `golang.org/x/sys` from `// indirect` to a direct requirement
  (run `go mod tidy`). It is already in `go.sum` and already used transitively by
  the BLE and systray dependencies, so this is a promotion, not a new module.

### Edit: `.github/workflows/release.yml`

Keep the single `ubuntu-latest` job. The build is pure Go and cross-compiles, so a
second `windows-latest` publishing job is unnecessary, and two jobs calling
`softprops/action-gh-release@v2` on the same tag would race.

- Add a Windows build step: `GOOS=windows GOARCH=amd64 CGO_ENABLED=0`, twice, once
  with `-H=windowsgui` for `gotempo.exe` and once without for `gotempo-cli.exe`,
  same `-w -s -X gotempo/internal/app.version=${GITHUB_REF_NAME}`.
- Add a zip step producing `gotempo-${TAG}-windows-amd64.zip` with both binaries
  (no POSIX `install.sh`; a zip is sufficient for now).
- Attach both the tarball and the zip in the existing `action-gh-release` step.

Optional: a `windows-latest` job that only runs `go test ./...` and publishes
nothing, for native test coverage.

### Edit: docs

- README: update the platform badge (`Linux` → `Linux`/`Windows`), add a Windows
  note under Requirements and Install naming which binary is which, and adjust the
  "Planned" list (Windows support becomes done; macOS remains planned).
- `docs/CLI.md`: note that the command line lives in `gotempo-cli.exe` on Windows.
- `docs/CONFIGURATION.md`: add the Windows locations, everything under
  `%LOCALAPPDATA%\gotempo`. Note that the tray's "Open log folder" and "Open
  config folder" open the same directory there, by design.
- `docs/PLAN.md`: mark the Windows half of the "Cross-platform" section as done,
  leaving macOS as the remaining work.

The ITGmania overlay needs no Windows work: the target comes from the
`itgmania_module` config key, and `docs/CONFIGURATION.md` already lists the
`%APPDATA%\ITGmania\Themes\Simply Love\Modules\` location.

## Verification

Done on Linux:

- `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./...` and `go vet` are clean.
- `make windows` produces a GUI-subsystem `gotempo.exe` and a console-subsystem
  `gotempo-cli.exe` (confirmed with `file`).
- `go test ./...` stays green. The one shared-code change is the directory table,
  which preserves the existing Linux paths, including for the three tests that
  relocate the data dir via `XDG_DATA_HOME`.
- `dirs_test.go` covers the resolver, including the case where a platform points
  config and data at the same folder.
- `assets_test.go` asserts the embedded icons carry the magic bytes of the format
  the current platform's tray can load, so a PNG in `assets_windows.go` fails the
  build rather than a user's desktop.

Still needs a real Windows machine, none of which can be checked by cross-compiling:

- `go test ./...` on Windows.
- The tray icon renders. A blank icon means PNG bytes reached `LoadImageW`.
- BLE connect, reconnect after a dropout, and adapter power-cycle against a real
  strap. The WinRT paths have never been executed.
- Autostart: the Run key entry survives a logout/logon.
- `gotempo-cli.exe --status` finds a running `gotempo.exe` through the named
  mutex.

## Out of scope / known limitations

- **macOS**: still needs `platform_darwin.go` and a `dirs` entry; CoreBluetooth
  requires cgo (`CGO_ENABLED=1`) and an Apple SDK, a separate effort.
- **Windows installer**: `scripts/install.sh` is POSIX-only; shipping a zip with
  both binaries is the initial plan. A proper installer or `.bat` can come later.
- **Signal handling**: `signal.Notify(SIGTERM)` is a no-op on Windows, so
  `--no-tray` relies on Ctrl+C (`SIGINT`) or closing the console. Cosmetic,
  matches current behaviour; not addressed here.
- **`notify` is a no-op**: "device lost" and "reconnected" notifications do not
  fire on Windows. Chosen deliberately to avoid a new dependency.
- **`describeConnectErr`** (`ble.go`) matches BlueZ-specific error strings; on
  Windows unmatched errors fall through to the raw message, which is fine.
