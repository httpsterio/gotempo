# Contributing

Build and release notes for working on gotempo. For installing and using a release, see the [README](README.md).

## Build from source

```sh
git clone https://github.com/httpsterio/gotempo
cd gotempo
go build -o gotempo .
```

Requires Go 1.24+. Tray icons are embedded at build time. BlueZ is a runtime dependency.

The build stamps a version into the binary (shown in the tray menu and by `gotempo --version`). A plain `go build` reports `dev`; build through `make` for the git-derived version.

For desktop integration from a source build (app-menu entry and icon under `~/.local`, no sudo):

```sh
make install     # make uninstall to remove
```

Override the location with `PREFIX`, e.g. `sudo make install PREFIX=/usr/local`.

## Code layout

`main.go` in the repo root is a shim (`func main() { app.Run() }`); all the code
lives in one `internal/app` package, split by concern:

| File (`internal/app/`) | Holds |
|---|---|
| `run.go` | startup, flag dispatch, tray and headless run loops |
| `cli.go` | flag parsing, the one-shot/headless commands, output formatting |
| `device.go` | `--device`/`--select-device`: MAC validation and the interactive picker |
| `log.go` | leveled logging (`--log-level`/`--quiet`) |
| `assets.go` | embedded icons (`assets/`), build-stamped `version` |
| `config.go` | config load/save, `configDir` (portable) |
| `state.go` | `AppState`, the BPM-file lifecycle, and the `status.json` publisher |
| `ble.go` | scanning, the connect/reconnect state machine, constants |
| `session.go` | per-session CSV logging |
| `tray.go` | tray menu and rendering |

It is one package, not several, so the files share unexported identifiers
freely; the split is for readability, and the `internal/` location just keeps the
repo root clean. `version` is stamped via `-ldflags "-X
gotempo/internal/app.version=…"` (the Makefile path matters; see Releasing).

Platform-specific code sits behind a contract so the shared files never branch
on the OS. The contract is `dirs`, `notify`, `openFolder`, `pairedDevices`, the autostart
trio, `openAdapter`, and `acquireInstanceLock`. Linux implements it in
`platform_linux.go` and `lock_unix.go` (flock; the `unix` build tag also covers
macOS); Windows in `platform_windows.go` and `lock_windows.go` (named mutex). Go
picks the right file by its `_linux` / `_darwin` / `_windows` suffix, within the
`internal/app` package. Adding an OS means writing one `platform_<os>.go` against
that contract, touching no shared files.

`dirs` is a `dirLayout` value rather than a function: it names the env var and
home-relative path for the config and data directories, and shared code in
`config.go` resolves both. Linux keeps them apart per XDG, Windows points both at
`%LOCALAPPDATA%\gotempo`.

Tray icons are embedded per platform, PNG in `assets_unix.go` and ICO in
`assets_windows.go`, because Windows loads tray icons through `LoadImageW` and
reads ICO only. Both files declare the same five variables. The `.ico` files are
generated from the `.png` of the same name:

```sh
magick connected.png -define icon:auto-resize=16,24,32,48,64,128 connected.ico
magick dot-connected.png -define icon:auto-resize=16 dot-connected.ico
```

## Releasing

The version comes from git tags. The tag you push is the version that ships.

Local builds pick it up from `git describe`:

```sh
make install        # builds with the embedded version
gotempo --version   # e.g. "gotempo v1.0.1"
```

| Situation | Reported version |
|---|---|
| On a tagged commit | `v1.0.1` |
| Past the last tag | `v1.0.1-3-gabc123` |
| Uncommitted changes | `…-dirty` appended |
| No tags yet | `dev` |

Cutting a release is one command:

```sh
make release VERSION=v1.0.2
```

It validates the version, checks the tree is clean, runs the tests, then tags and pushes. Pushing a `v*.*.*` tag triggers `.github/workflows/release.yml`, which builds the binary, packages the `linux-amd64` tarball with the installer, and publishes a GitHub release.

The end-user installers live in `scripts/` (`install.sh`, `uninstall.sh`); the workflow copies them to the tarball root, so a user extracts and runs `./install.sh`. `make install` is the source-build path and generates the `.desktop` entry itself, so there is no `.desktop` file in the tree.
