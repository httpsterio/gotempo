# Command line

gotempo runs in the tray by default. Flags let it run headless or answer one-shot
queries, for systems without a tray or for scripts and status bars. For the files
and config it reads/writes, see [Configuration](CONFIGURATION.md). Back to the
[README](../README.md).

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
| `--itgmania-module <path>` | Set the path to `gotempo.lua` in `config.json` (`hr.txt` is written beside it), then run. |
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
and then exit; `--device`/`--select-device`/`--itgmania-module` write to
`config.json` and then continue into a normal run. `--autostart` overwrites an
existing entry silently; `--no-autostart` on a missing entry is a no-op success.

`--device` takes a MAC address (case and `:`/`-` separators are normalized) and
rejects anything that isn't one. `--select-device` lists the known devices first
and offers an on-demand scan (`s`); picking a known device skips scanning. It
needs an interactive terminal and exits non-zero if run without one.

`--itgmania-module` takes the path to the `gotempo.lua` theme module and stores
it, enabling the in-game heart-rate panel; gotempo then writes `hr.txt` in the
same folder. Like `--config`, the path must already exist, and a directory is
rejected: a typo would otherwise become a silently dead overlay. It is
independent of the device flags, so `--device` and `--itgmania-module` can be
given together. See [ITGmania overlay](CONFIGURATION.md#itgmania-overlay) for the
file format and for which copy of the module to point at.

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
running`. When the ITGmania overlay is on, a second line names the resolved
target, e.g. `itgmania: /home/you/.itgmania/Themes/Simply Love/Modules/hr.txt`,
so a misconfigured path is visible rather than silent. `--json` gives the full
state for scripting:

```json
{"running":true,"connected":true,"phase":"connected","logging":false,"bpm":61,"device":{"mac":"24:AC:AC:18:41:CC","name":"Polar H10"},"itgmania":"/home/you/.itgmania/Themes/Simply Love/Modules/hr.txt","timestamp":"2026-06-16T02:40:00+03:00"}
```

`phase` is one of `idle` (no device), `connecting`, `reconnecting` (device lost,
retrying), or `connected`, so a poller can tell "reconnecting" apart from "no
device" rather than collapsing both into `connected:false`. `itgmania` is absent
when the overlay is off.

`--config <path>` relocates only the config file. The BPM/status files and the
single-instance lock stay at the shared data/runtime locations, so two
`--config`s can't run side by side.

Exit code 1 covers a `--itgmania-module` path that doesn't exist or is a
directory.

Exit codes:

| Code | Meaning |
|---|---|
| 0 | Clean exit; `--status`: running and connected |
| 1 | Config/setup error (bad flag or value, missing `--config`, invalid `--device` MAC, missing `--itgmania-module` file, `--select-device` without a terminal, setup write/remove failed) |
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

Set the device in `config.json` first, since headless mode has no device picker
(see [Configuration](CONFIGURATION.md)). The device must also be known to BlueZ
(paired or connected at least once): gotempo connects by address and never scans
in the background, so headless it cannot discover an unknown device on its own.
Pair it once (e.g. with `bluetoothctl` or blueman), or run once with the tray and
pick it, then headless reconnects work. `--no-tray` logs by default, so no extra
flag is needed for a recording service.
