# gotempo

[![Release build](https://github.com/httpsterio/gotempo/actions/workflows/release.yml/badge.svg)](https://github.com/httpsterio/gotempo/actions/workflows/release.yml)
[![License: GPL-3.0](https://img.shields.io/badge/license-GPL--3.0-blue)](LICENSE.md)
[![Go](https://img.shields.io/badge/Go-1.24-00ADD8?logo=go&logoColor=white)](go.mod)
[![Platform: Linux | Windows](https://img.shields.io/badge/platform-Linux%20%7C%20Windows-blue)](#requirements)

![gotempo](docs/gotempo-logo.png)

A small tray app for Linux and Windows that connects to a Bluetooth LE heart-rate monitor, shows connection status in the tray, and writes the current BPM to a text file. Useful as an OBS text source.

Works with any BLE monitor using the standard GATT HR profile (0x180D / 0x2A37): Polar H10, H9, and most other chest straps and optical armbands.

![gotempo tray menu](docs/gotempo-screenshot.png)


## Install

Grab the latest build from [releases](https://github.com/httpsterio/gotempo/releases/latest) and extract it.

On Linux, run `install.sh` to add gotempo to your applications menu, or just run `gotempo` directly. `uninstall.sh` removes it. Config and data live under `~/.config/gotempo` and `~/.local/share/gotempo`.

On Windows, extract the zip and run `gotempo.exe`. It holds everything in `%LOCALAPPDATA%\gotempo`. The zip also contains `gotempo-cli.exe`, the same app built as a console binary, for the flags in [Command line](docs/CLI.md).

See [Files & configuration](docs/CONFIGURATION.md) for the full list.


## Requirements

Windows 10 or later. Nothing to install: the tray and the Bluetooth stack are
both built in.

On Linux:

- BlueZ (`bluetooth.service` running)
- A StatusNotifierItem system tray (XFCE, KDE, most modern panels). The app won't start without one. GNOME has no built-in tray; install the [AppIndicator and KStatusNotifierItem Support](https://extensions.gnome.org/extension/615/appindicator-support/) extension to get the tray icon.
- `notify-send` (from `libnotify`), optional. Notifications are skipped if missing.


## Build from source

```sh
git clone https://github.com/httpsterio/gotempo
cd gotempo
go build -o gotempo .
```

Requires Go 1.24+. `make windows` cross-compiles the Windows binaries from Linux; the Windows Bluetooth backend is pure Go, so no Windows toolchain is involved.

For desktop integration (`make install`), version stamping, and the release process, see [CONTRIBUTING.md](CONTRIBUTING.md).


## First launch

```sh
./gotempo
```

With no `config.json`, the app starts scanning. Open the tray menu, wait for your monitor to appear (it must be awake and worn to advertise), and click it to connect. The choice is saved, and later launches connect directly.

Pair and trust the strap before relying on it. On Linux that is `bluetoothctl` (`pair`, then `trust`); trusting is what lets BlueZ reconnect on its own, and an untrusted bond is a suspect in past reconnect failures (see [Known issues](docs/BUGS.md)). On Windows, pair it in Settings → Bluetooth & devices, after which the tray lists it whether or not it is advertising, which a scan alone cannot find.

Most straps allow one connection at a time, so disconnect the monitor from your phone or other apps first. Some allow more than one.


## Tray menu

| Item | Behaviour |
|---|---|
| **Start logging** / **Stop logging** | Begin or stop logging: writes the current BPM to `gotempo-bpm.txt` and appends each reading to a per-session CSV (see [Files & configuration](docs/CONFIGURATION.md)). Greyed out when not applicable. |
| **Device list** | Known devices (most-recently-used first) plus newly scanned ones, listed directly in the menu. Click one to switch; the current device is marked and not clickable. Up to six shown. |
| **Rescan for new devices** | Runs a fresh 15-second scan; new monitors appear in the list. On Windows the list also includes monitors already paired in Settings, which a scan cannot see. |
| **Open log folder** | Opens the log and CSV directory in a file browser. |
| **Open config folder** | Opens the directory holding `config.json`. |
| **Autostart HR log** | When checked, logging starts automatically on launch. |
| **Start on boot** | Adds or removes `~/.config/autostart/gotempo.desktop`. |
| **Quit** | Exits the app. |


## Command line

gotempo also runs headless, for systems without a tray or for scripts and status bars: connect without the tray (`--no-tray`), stream readings (`--print-bpm`), or report the running app's state (`--status`). Full flag reference, output formats, exit codes, and a systemd unit in [docs/CLI.md](docs/CLI.md).


## ITGmania overlay

gotempo can drive `gotempo.lua`, a Simply Love theme module that draws your heart rate on ITGmania's gameplay screen. Install the module, then set one key in `config.json` to its full path:

```json
"itgmania_module": "/home/you/.itgmania/Themes/Simply Love/Modules/gotempo.lua"
```

Restart gotempo. It writes `hr.txt` beside the module, and the panel appears in game. The overlay does not depend on the logging toggle.

`gotempo --itgmania-module <path>` sets the same key. See [ITGmania overlay](docs/CONFIGURATION.md#itgmania-overlay) for the file format and where the module lives on each OS.


## Reconnection behaviour

When the connection drops, gotempo reconnects on its own:

- It retries silently for a short while. A device that returns in this window reconnects with no notification.
- If that fails, it sends one "device lost" notification and keeps retrying until the device returns, then sends "reconnected".

Reconnection connects straight to your device by address, it does not scan, so it never probes other Bluetooth devices in range while waiting for your strap to come back. Scanning happens only when you pick a device or hit Rescan. Connecting by address needs the device to be known to BlueZ already, which it is once you have paired or connected it once.

The BPM file keeps its last value across a brief drop and clears after about ten seconds disconnected, so it doesn't leave a stale reading (see [Files & configuration](docs/CONFIGURATION.md)).

It also survives the Bluetooth adapter being toggled off and on, including when it returns on a different `hciN` index.


## Documentation

- [Command line](docs/CLI.md): headless mode, all flags, output formats, exit codes, systemd
- [Files & configuration](docs/CONFIGURATION.md): file locations and formats, `config.json` schema, ITGmania overlay
- [CONTRIBUTING.md](CONTRIBUTING.md): build from source, versioning, releases
- [Known issues](docs/BUGS.md)


## Planned

- macOS support
