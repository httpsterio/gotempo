# gotempo

![gotempo](assets/logo.png)

A lightweight Linux system-tray app that connects to a Bluetooth LE heart-rate monitor, shows connection status as a tray icon, and writes the current BPM to a text file. Useful as an OBS text source or anywhere you want live BPM data in a file.

Works with any BLE heart-rate monitor that uses the standard GATT HR profile (0x180D / 0x2A37) — Polar H10, Polar H9, and most other chest straps and optical armbands.


## Contents

- [Install](#install)
- [Requirements](#requirements)
- [Build from source](#build-from-source)
- [First launch](#first-launch)
- [Tray menu](#tray-menu)
- [Reconnection behaviour](#reconnection-behaviour)
- [Files](#files)
- [Configuration](#configuration)
- [Releasing](#releasing)
- [Planned](#planned)


## Install

Download the latest release tarball, extract it, and run the installer:

```sh
curl -LO https://github.com/httpsterio/gotempo/releases/latest/download/gotempo-VERSION-linux-amd64.tar.gz
tar -xzf gotempo-VERSION-linux-amd64.tar.gz
cd gotempo-VERSION-linux-amd64
./install.sh
```

Replace `VERSION` with the release tag (e.g. `v1.0.0`). `install.sh` copies the binary to `~/.local/bin/gotempo`, installs the icon and an app-menu entry, and refreshes the desktop caches — no sudo required. Make sure `~/.local/bin` is on your PATH. Run `./uninstall.sh` to remove everything.

Config and data are stored under `~/.config/gotempo` and `~/.local/share/gotempo` (see [Files](#files)), so the binary can be moved freely without losing your saved device.


## Requirements

- Linux with BlueZ (`bluetooth.service` must be running)
- A StatusNotifierItem-capable system tray — XFCE, KDE, and most modern panels qualify. Without a tray host the app will not start.
- `notify-send` (usually provided by `libnotify`) — optional, notifications are silently skipped if missing


## Build from source

```sh
git clone https://github.com/httpsterio/gotempo
cd gotempo
go build -o gotempo .
```

Requires Go 1.24+. The tray status icons are embedded in the binary at build time. BlueZ is a runtime dependency and cannot be bundled.

The build stamps a version into the binary (shown as the first item in the tray menu, and printed by `gotempo --version`). A plain `go build` reports `dev`; build through `make` to get the git-derived version (see [Releasing](#releasing)).

For desktop integration from a source build (app-menu entry + icon under `~/.local`, no sudo):

```sh
make install     # make uninstall to remove
```

Override the location with `PREFIX`, e.g. `sudo make install PREFIX=/usr/local`.


## First launch

```sh
./gotempo
```

On first launch with no `config.json`, the app starts scanning immediately. Open the tray menu, wait for your monitor to appear in the device list (make sure it is awake and worn so it is advertising), and click it to connect. The choice is saved and subsequent launches connect directly without scanning.


## Tray menu

| Item | Behaviour |
|---|---|
| **Start logging** / **Stop logging** | Begin or stop writing BPM to `logs/gotempo-bpm.txt`. Greyed out when not applicable. |
| **Device list** | Known devices (most-recently-used first) plus any newly scanned ones are listed directly in the menu. Click a device to switch to it; the current device is marked and not clickable. Up to six are shown at once. |
| **Rescan for new devices** | Triggers a fresh 15-second scan; newly found monitors appear in the list. |
| **Autostart HR log** | When checked, logging starts automatically on every launch. |
| **Start on boot** | Adds or removes `~/.config/autostart/gotempo.desktop`. |
| **Quit** | Exits the app. |


## Reconnection behaviour

When the connection drops, gotempo retries silently in the background:

1. **5 attempts, 3 seconds apart** — fast silent retries. If the device comes back here, nothing is shown.
2. **5 attempts, 10 seconds apart** — still silent.
3. **60-second retries, indefinitely** — on entering this phase a single "device lost" notification is sent. The app keeps retrying every 60 seconds until it reconnects or is quit. No further notifications until a reconnect happens.

If the device reconnects during the 60-second phase, a "reconnected" notification is sent and the retry schedule resets from the beginning.

The app also survives the Bluetooth adapter being toggled off and on, including cases where it comes back on a different `hciN` index.


## Files

gotempo stores its data in standard XDG directories (created automatically on first run):

- `~/.config/gotempo/config.json` — saved device, known-device history, and preferences. Managed by the app; see [Configuration](#configuration) if you need to edit it by hand. Honors `$XDG_CONFIG_HOME`.
- `~/.local/share/gotempo/gotempo-bpm.txt` — current BPM as a raw integer, overwritten on every change. Empty or absent when logging is not active. Honors `$XDG_DATA_HOME`.
- `assets/` (source tree only) — tray status icons (`connected.png`, `disconnected.png`, `running.png`), embedded in the binary at build time, plus `logo.png` (512×512) used as the application icon by `make install`.


## Configuration

`config.json` is written and updated automatically. You can edit it by hand if needed — useful for headless setups where you want to set the device without going through the tray:

```json
{
  "current": "24:AC:AC:18:41:CC",
  "known": [
    {
      "mac": "24:AC:AC:18:41:CC",
      "name": "Polar H10 1841CC31",
      "last_used": "2026-06-07T00:00:00Z"
    }
  ],
  "auto_log": false
}
```

Set `current` to your device MAC and add an entry to `known`. The app will connect to it on next launch without scanning.


## Releasing

The version is derived from git tags — there is no version number to edit by hand. The tag you push *is* the version that ships.

**Local builds** pick up the version automatically from `git describe`:

```sh
make install        # builds with the embedded version, installs under ~/.local
gotempo --version   # prints e.g. "gotempo v1.0.1"
```

What the version looks like depends on where you are relative to tags:

| Situation | Reported version |
|---|---|
| On a tagged commit | `v1.0.1` |
| A few commits past the last tag | `v1.0.1-3-gabc123` |
| Uncommitted local changes | `…-dirty` appended |
| No tags yet | `dev` |

So a local build is clearly marked as a work-in-progress, distinct from an official release.

**Cutting a release** is one command:

```sh
make release VERSION=v1.0.2
```

It checks the version format, ensures the working tree is clean, runs the tests, creates an annotated tag, and pushes it. Pushing a `v*.*.*` tag triggers `.github/workflows/release.yml`, which builds the binary (stamped with the exact tag), packages the `linux-amd64` tarball with the installer, and publishes a GitHub release. Users who download that tarball get a binary that reports the clean tag version — no commit hash, no `-dirty`.


## Planned

- macOS support
- Windows support
