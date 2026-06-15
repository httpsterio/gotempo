# gotempo

![gotempo](internal/app/assets/logo.png)

A small Linux tray app that connects to a Bluetooth LE heart-rate monitor, shows connection status in the tray, and writes the current BPM to a text file. Useful as an OBS text source.

Works with any BLE monitor using the standard GATT HR profile (0x180D / 0x2A37): Polar H10, H9, and most other chest straps and optical armbands.


## Contents

- [Install](#install)
- [Requirements](#requirements)
- [Build from source](#build-from-source)
- [First launch](#first-launch)
- [Tray menu](#tray-menu)
- [Command line](#command-line)
- [Reconnection behaviour](#reconnection-behaviour)
- [Files](#files)
- [Configuration](#configuration)
- [Planned](#planned)


## Install

Grab the latest build from [releases](https://github.com/httpsterio/gotempo/releases/latest) and extract it. Run `install.sh` to add gotempo to your applications menu, or just run `gotempo` directly. `uninstall.sh` removes it.

Config and data live under `~/.config/gotempo` and `~/.local/share/gotempo` (see [Files](#files)).


## Requirements

- Linux with BlueZ (`bluetooth.service` running)
- A StatusNotifierItem system tray (XFCE, KDE, most modern panels). The app won't start without one. GNOME has no built-in tray; install the [AppIndicator and KStatusNotifierItem Support](https://extensions.gnome.org/extension/615/appindicator-support/) extension to get the tray icon.
- `notify-send` (from `libnotify`), optional. Notifications are skipped if missing.


## Build from source

```sh
git clone https://github.com/httpsterio/gotempo
cd gotempo
go build -o gotempo .
```

Requires Go 1.24+. For desktop integration (`make install`), version stamping, and the release process, see [CONTRIBUTING.md](CONTRIBUTING.md).


## First launch

```sh
./gotempo
```

With no `config.json`, the app starts scanning. Open the tray menu, wait for your monitor to appear (it must be awake and worn to advertise), and click it to connect. The choice is saved, and later launches connect directly.

On first connect, accept the pairing prompt if it appears. Most straps allow one connection at a time, so disconnect the monitor from your phone or other apps first. Some allow more than one.


## Tray menu

| Item | Behaviour |
|---|---|
| **Start logging** / **Stop logging** | Begin or stop logging: writes the current BPM to `gotempo-bpm.txt` and appends each reading to a per-session CSV (see [Files](#files)). Greyed out when not applicable. |
| **Device list** | Known devices (most-recently-used first) plus newly scanned ones, listed directly in the menu. Click one to switch; the current device is marked and not clickable. Up to six shown. |
| **Rescan for new devices** | Runs a fresh 15-second scan; new monitors appear in the list. |
| **Autostart HR log** | When checked, logging starts automatically on launch. |
| **Start on boot** | Adds or removes `~/.config/autostart/gotempo.desktop`. |
| **Quit** | Exits the app. |


## Command line

gotempo runs in the tray by default. Flags let it run headless or answer
one-shot queries, for systems without a tray or for scripts and status bars.

| Flag | Effect |
|---|---|
| `--no-tray` | Run headless (no tray), connecting and logging per config, until `SIGINT`/`SIGTERM`. Needs a device set in `config.json`. |
| `--print-bpm` | Stream each reading to stdout as `<unix_time> <bpm>`. Implies `--no-tray`. |
| `--status` | Report the running app's status and exit: `72 bpm`, `no signal`, or `gotempo is not running`. |
| `--list-devices` | Scan for HR monitors, print `MAC<tab>name` per line, exit. |
| `--json` | Machine-readable JSON for `--status`, `--print-bpm`, `--list-devices`. |
| `--config <path>` | Use a config file at `<path>` (must already exist). |
| `--version`, `-v` | Print version and exit. |

`--status` reports the state of the running gotempo; it never connects to a
device itself. The running app owns the one BLE connection a strap allows, so
`--status` only inspects that app's published value and writes nothing. If
gotempo isn't running there's no status to report and it exits non-zero. This
makes it safe to poll from a status bar alongside the tray or `--no-tray` app.
(A standalone "read a BPM without a running app" mode would be a separate flag;
it isn't implemented.)

Exit codes:

| Code | Meaning |
|---|---|
| 0 | Clean exit; `--status`: running and connected |
| 1 | Config error (missing `--config` file, bad flags) |
| 2 | `--status`: running but not connected |
| 3 | Bluetooth adapter unavailable, or `--no-tray` with no device configured |
| 4 | `--status`: gotempo is not running |

Run headless as a systemd user service:

```ini
[Unit]
Description=gotempo HR monitor

[Service]
ExecStart=/usr/local/bin/gotempo --no-tray
Restart=on-failure

[Install]
WantedBy=default.target
```

Set the device and `"auto_log": true` in `config.json` first, since headless
mode has no device picker.


## Reconnection behaviour

When the connection drops, gotempo reconnects on its own:

- It retries silently for a short while. A device that returns in this window reconnects with no notification.
- If that fails, it sends one "device lost" notification and scans continuously until the device returns, then sends "reconnected".

The BPM file keeps its last value across a brief drop and clears after about ten seconds disconnected, so it doesn't leave a stale reading (see [Files](#files)).

It also survives the Bluetooth adapter being toggled off and on, including when it returns on a different `hciN` index.


## Files

gotempo uses standard XDG directories, created on first run:

- `~/.config/gotempo/config.json`: saved device, known-device history, and preferences. Managed by the app; see [Configuration](#configuration) to edit it by hand. Honors `$XDG_CONFIG_HOME`.
- `~/.local/share/gotempo/gotempo-bpm.txt`: current BPM as a raw integer, rewritten on each change. Empty when not logging. Keeps the last reading briefly across a short dropout, then clears after about ten seconds disconnected. Honors `$XDG_DATA_HOME`.
- `~/.local/share/gotempo/sessions/*.csv`: per-session history, one `timestamp,bpm` row per reading. Written while logging is on. A new file starts after a gap longer than `session_gap_minutes`; shorter breaks append to the current file. Readings below `min_bpm_threshold` (sensor off / no contact) are skipped, so they show as gaps in the timestamps rather than junk rows. Files are named by the session's first reading.
- `internal/app/assets/` (source tree only): tray status icons and `logo.png`, embedded in the binary at build time.


## Configuration

`config.json` is created on first run with all keys at their defaults, and updated automatically after that. You can edit it by hand, which is handy for headless setups where you set the device without the tray. On launch each value is validated; a missing, malformed, or out-of-range entry is reset to its default and the file is rewritten, so it never holds a value the app silently ignores:

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
  "auto_log": false,
  "session_gap_minutes": 60,
  "min_bpm_threshold": 20
}
```

Set `current` to your device MAC and add a matching `known` entry. The app connects to it on next launch without scanning.

`session_gap_minutes` (default 60) is the idle span that ends a CSV session: a longer gap between readings starts a new file, a shorter one continues the current session. `min_bpm_threshold` (default 20) is the validity floor; readings below it are treated as no-contact noise and left out of the CSV. Both keys are optional and only needed to override the defaults.


## Planned

- macOS support
- Windows support
