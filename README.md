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
| `--print-bpm` | Stream each reading to stdout as `<time> <bpm>`. Implies `--no-tray`. |
| `--epoch` | With `--print-bpm`, render the time as unix seconds. |
| `--timestamp` | With `--print-bpm`, render the time as RFC3339 (default is `hh:mm:ss`). |
| `--status` | Report the running app's state and exit (see below). |
| `--list-devices` | Scan for HR monitors, print `MAC<tab>name` per line, exit. |
| `--autostart` | Enable launch-on-login (write the autostart entry), then exit. |
| `--no-autostart` | Disable launch-on-login (remove the autostart entry), then exit. |
| `--device <mac>` | Set the current device by MAC in `config.json`, then run. |
| `--select-device` | Interactively pick the current device, then run (needs a terminal). |
| `--auto-log` | Force session logging on for this run (overrides config). |
| `--no-auto-log` | Force session logging off for this run (overrides config). |
| `--log-level <lvl>` | stderr verbosity: `error`, `info` (default), or `debug`. |
| `--quiet` | Errors only; same as `--log-level error`. |
| `--json` | Machine-readable JSON for `--status`, `--print-bpm`, `--list-devices`. |
| `--config <path>` | Use a config file at `<path>` (must already exist). |
| `--version`, `-v` | Print version and exit. |

Most flags are *session-only* and never touch `config.json` or disk; they affect
the single run (the `--auto-log`/`--no-auto-log` and logging flags included).
*Setup* flags are the exception: `--autostart`/`--no-autostart` persist a change
and then exit; `--device`/`--select-device` set the current device in
`config.json` and then continue into a normal run. `--autostart` overwrites an
existing entry silently; `--no-autostart` on a missing entry is a no-op success.

`--device` takes a MAC address (case and `:`/`-` separators are normalized) and
rejects anything that isn't one. `--select-device` lists the known devices first
and offers an on-demand scan (`s`); picking a known device skips scanning. It
needs an interactive terminal and exits non-zero if run without one.

Both start a run, so they take the single-instance lock: if a gotempo is already
running they print "already running" and exit without changing the device. To
switch devices, quit the running instance first (or use the tray's `Devices`
menu, which switches live).

`--no-tray` is the "run as a daemon" signal, so it defaults session logging on
(a service that connects but never records is useless); `--no-auto-log` opts out.
`--print-bpm` is additive, not a mode switch: `--no-tray --print-bpm` both logs
and streams. A *bare* `--print-bpm` (without `--no-tray`) does not enable logging
on its own; add `--auto-log` if you want it to log too.

`--print-bpm` prints one reading per line. By default the time is `hh:mm:ss`;
`--epoch` switches it to unix seconds and `--timestamp` to RFC3339 (the two are
mutually exclusive). With `--json` each line is an object `{"timestamp":…,
"bpm":…}` (timestamp is a number under `--epoch`).

`--status` reports the state of the running gotempo; it never connects to a
device itself. The running app owns the one BLE connection a strap allows, so
`--status` only inspects that app's published state and writes nothing. It
reflects the live state whether or not logging is on. If gotempo isn't running
there's no status to report and it exits non-zero. This makes it safe to poll
from a status bar alongside the tray or `--no-tray` app. (A standalone "read a
BPM without a running app" mode would be a separate flag; it isn't implemented.)

Plain output is a single line, e.g. `connected, 61 bpm, Polar H10, logging off`,
`reconnecting, Polar H10, logging on`, `idle, no device`, or `gotempo is not
running`. `--json` gives the full state for scripting:

```json
{"running":true,"connected":true,"phase":"connected","logging":false,"bpm":61,"device":{"mac":"24:AC:AC:18:41:CC","name":"Polar H10"},"timestamp":"2026-06-16T02:40:00+03:00"}
```

`phase` is one of `idle` (no device), `connecting`, `reconnecting` (device lost,
scanning), or `connected`, so a poller can tell "reconnecting" apart from "no
device" rather than collapsing both into `connected:false`.

`--config <path>` relocates only the config file. The BPM/status files and the
single-instance lock stay at the shared data/runtime locations, so two
`--config`s can't run side by side.

Exit codes:

| Code | Meaning |
|---|---|
| 0 | Clean exit; `--status`: running and connected |
| 1 | Config/setup error (bad flag or value, missing `--config`, invalid `--device` MAC, `--select-device` without a terminal, setup write/remove failed) |
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
- `~/.local/share/gotempo/status.json`: live app state published by the running app, independent of logging — connection, phase, logging flag, current BPM, and device. Read by `gotempo --status`. Honors `$XDG_DATA_HOME`.
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
