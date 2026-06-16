# gotempo todo

## CLI

Goal: allow gotempo to run fully headless (no systray/AppIndicator) and be
controllable via flags, for users without tray support or who want to run it
as a service/from scripts.

Core slice shipped (see Done): `--no-tray`, `--list-devices`, `--status`,
`--print-bpm`, `--json`, `--config`, `--version`/`-v`. Still deferred:
`--device`, `--autostart`/`--no-autostart`, `--auto-log`/`--no-auto-log`,
`--quiet`, `--log-level`. The per-flag specs below remain the reference for the
deferred set; the shipped flags' specs describe implemented behavior.

### Flags

#### `--no-tray`
Run without initializing the systray/AppIndicator at all. App runs as a plain
foreground or background process. All other functionality (BLE connection,
BPM file writing, session logging) works identically.

#### `--config <path>`
Override the default config file location. If not provided, use the existing
default path. If the file doesn't exist, error out (exit code 1) rather than
silently using defaults, since the user explicitly pointed somewhere.

#### `--device <id>`
Connect to a specific BLE device by ID (MAC address or whatever identifier
the BLE lib exposes), instead of showing a device picker or using the
previously paired device from config. Useful when multiple Polar straps are
available and you want to pick one without UI.

If the specified device isn't found during scan, error out with exit code 3
(see exit codes below).

#### `--list-devices`
Scan for available BLE heart rate devices, print them (one per line: ID and
name), then exit immediately. Does not connect, does not start logging.
Exit code 0 on successful scan (even if zero devices found), non-zero only
on BLE adapter failure.

#### `--autostart` / `--no-autostart`
Override the config's `autostart` setting for this run only. Does not modify
`config.json`. If neither flag is passed, use the config value.

#### `--auto-log` / `--no-auto-log`
Same as above but for the `auto_log` setting (whether session CSV logging
starts automatically when valid BPM readings begin, per the session logging
spec).

#### `--print-bpm [--epoch | --timestamp]`
Print each BPM reading to stdout, one per line, as it's received. Format:

```
<time> <bpm>
```

The time defaults to `hh:mm:ss`; `--epoch` renders unix seconds and
`--timestamp` renders RFC3339 (the two are mutually exclusive, bad combo →
exit 1). With `--json` each line is one object (newline-delimited, not an
array, so it streams):

```json
{"timestamp": "14:23:00", "bpm": 72}
```

`timestamp` is a JSON number under `--epoch`, a string otherwise.

This runs alongside normal operation (BPM file writing, session logging
continue as configured), it's an additional output stream, not a replacement.

#### `--status [--json]`
Report the status of the *running* gotempo, then exit. It never connects to a
device itself: a strap allows one BLE connection and that belongs to the running
app, so `--status` only inspects the app's published state and writes nothing.
The instance lock is the "is it running" signal (acquired, then dropped at once);
the running app maintains `status.json` (connection, phase, logging, bpm,
device) independent of logging, which `--status` reads.

- lock free → nothing running, no status (exit 4)
- lock held + status.json connected with a reading → exit 0
- lock held + status.json otherwise (connecting / not connected) → exit 2

Plain output is one line: `connected, 61 bpm, Polar H10, logging off`,
`reconnecting, Polar H10, logging on`, `idle, no device`, or `gotempo is not
running`.

With `--json`, the full state for scripting:
```json
{"running":true,"connected":true,"phase":"connected","logging":false,"bpm":61,"device":{"mac":"…","name":"Polar H10"},"timestamp":"2026-06-08T14:23:00+03:00"}
```

`phase` is `idle` (no device) / `connecting` / `reconnecting` (device lost,
scanning) / `connected`, so a poller distinguishes those rather than flattening
them into `connected:false`.

Intended for status bar widgets (waybar, polybar) polling alongside the tray or
`--no-tray` app. A standalone "give me a reading even with no app running" is a
different operation and would get its own flag (e.g. `--read`); deliberately not
folded into `--status`.

#### `--quiet`
Suppress all stdout/stderr output except errors. Errors still print to
stderr and still produce the appropriate exit code. Does not suppress
`--print-bpm` or `--status` output, those are explicit output requests, not
logging.

#### `--log-level error|info|debug`
Controls verbosity of operational logging to stderr. Default: `info`.

- `error`: only errors (connection failures, file write errors, etc)
- `info`: error + lifecycle events (connected, session started/ended,
  config loaded)
- `debug`: info + per-reading detail (every BPM value received, gap timer
  state, etc) — useful for debugging the session-splitting logic

`--quiet` is equivalent to `--log-level error` plus suppressing the info
banner on startup, if any.

### Exit codes

| Code | Meaning |
|------|---------|
| 0 | Clean exit; `--status`: running and connected |
| 1 | Config error (missing `--config` file, bad flag combination) |
| 2 | `--status`: running but not connected |
| 3 | BLE adapter not available, `--no-tray` with no device, or (deferred) `--device` not found |
| 4 | `--status`: gotempo is not running |

Documented in `--help` output and the README. (File-I/O failures currently log
and continue rather than mapping to a distinct exit code.)

### Signal handling

- `SIGTERM` / `SIGINT`: clean shutdown. Flush and close the current CSV session
  (writes are unbuffered, so this is just a final fsync + close). Stop the BLE
  worker. Exit 0.
- No need for `SIGHUP`/config-reload support initially, out of scope.

### Service-friendly behavior

- With `--no-tray`, gotempo should be runnable as a systemd user service:

```ini
[Unit]
Description=gotempo HR monitor

[Service]
ExecStart=/usr/local/bin/gotempo --no-tray --auto-log
Restart=on-failure

[Install]
WantedBy=default.target
```

- No interactive prompts in `--no-tray` mode. If a device needs to be picked
  and none is configured/specified, behavior should be: use the last-paired
  device from config if available, otherwise error (exit code 3) rather than
  blocking on input.

### Flag interaction notes

- `--print-bpm` and `--status` are independent: `--status` is one-shot and
  exits, `--print-bpm` is continuous. Using both together is undefined/not
  meaningful, `--status` should take precedence and exit before
  `--print-bpm` would produce continuous output.
- `--list-devices` exits before any other mode (logging, tray, etc) starts.
  It's purely informational.
- CLI flags are session-only overrides. They do not modify `config.json`.
  Persistent setting changes require editing the config file directly.

### Out of scope (not in this spec)

- IPC/runtime control of a running daemon (e.g. start/stop session via signal
  to an already-running process). If needed later, would require a unix
  socket or PID-file + signal scheme. Not needed for initial CLI support,
  since `--auto-log` + the existing gap-based session logic covers normal
  usage without manual start/stop.

## Done

- **CLI / headless core.** `cli.go` adds flag parsing and the one-shot/headless
  modes; `run.go` dispatches. `--no-tray` (and `--print-bpm`, which implies it)
  run the BLE worker with no tray until SIGINT/SIGTERM, taking the instance lock
  and exiting 3 if no device is configured (no picker headless). `--list-devices`
  and `--status` are one-shot. `--status` reports the *running* app's state only:
  it probes the instance lock (then drops it), and if an instance holds it reads
  that instance's `status.json` — published independent of logging, so it is
  correct even when logging is off (connected+reading → 0, otherwise → 2); if the
  lock is free, nothing is running (exit 4). It never connects to a device, opens
  an adapter, or writes files. `status.json` carries connection, a coarse `phase`
  (idle/connecting/reconnecting/connected), the logging flag, current bpm, and
  device; it is written atomically on connect, every reading, logging toggle, and
  immediately on disconnect/switch (`App.publishStatus` / `setPhase` / `recordBPM`
  in `ble.go`, phase transitions wired through the connect state machine).
  `--print-bpm` renders the time as `hh:mm:ss` by default, unix seconds with
  `--epoch`, or RFC3339 with `--timestamp`. `--json` switches
  `--status`/`--print-bpm`/`--list-devices` to machine output; `--config <path>`
  overrides the config location (must exist). Exit codes 0/1/2/3/4 per the table.
  `onReading` hook in `handleBPM` feeds the raw stream (used by `--print-bpm`).
  Tests in `cli_test.go`.
- **CSV session logging.** `session.go`: `SessionLogger` writes valid readings
  to per-session `sessions/<start>.csv` files, gated by the same logging toggle
  as the OBS file. Junk (below `min_bpm_threshold`) is dropped, so dropouts are
  timestamp gaps not rows; a gap over `session_gap_minutes` starts a new file,
  shorter breaks append. Restart and toggle-on resume the latest file via one
  gap rule (`mostRecentSession`/`lastTimestamp`), skipping header-only crash
  orphans. Writes are unbuffered so files are crash-readable without per-row
  fsync; `gapCheckLoop` (per-minute) closes idle sessions and fsyncs the open
  one, bounding power-loss exposure to ~60s. Logging is gated by `enabled` under
  the session mutex, closing the toggle-off race. `gotempo-bpm.txt` is untouched,
  keeps its stale-hold. Defaults 60min / 20bpm in `config.go`; tests in
  `session_test.go`.
- **Package split and repo tidy.** Code moved from a flat root `package main`
  into one `internal/app` package; the root holds only a `main.go` shim
  (`func main() { app.Run() }`). Platform-specific calls sit behind a contract
  (`platform_linux.go`, `lock_unix.go`) resolved by build-tag suffix within the
  package, so adding macOS/Windows is drop-in. Docs live in `docs/`, end-user
  installers in `scripts/`, and the `.desktop` entry is generated by `make
  install` rather than stored in the tree. See CONTRIBUTING.md "Code layout".
- **Dead code cleanup.** Removed unused `resetBPM`; `onSwitch` now logs its
  `WriteFile` error like `handleBPM`.
- **Improve logging.** The persistent phase is no longer silent:
  - swallowed connect/discovery errors are surfaced (`persistentConnect`);
  - session length logged on drop (`connectAndMonitor`);
  - per-minute "still scanning" heartbeat plus "back in range after N" on return
    (`persistScan`);
  - `[BLE]` prefix on the bare `scan error:` / `invalid mac:` lines.

## BLE reconnect robustness
See [BUGS.md](BUGS.md) (strap won't reconnect after a long idle). Logging is
done, so the next organic failure should be self-diagnosing. Remaining, in
order:
- **Connect a known device by address** instead of requiring a scan hit. A
  bonded or already-connected strap does not advertise, so the scan-gate locks
  it out. Try direct connect, fall back to scan. Most likely tied to the bug.
- **Self-heal** by dropping the BlueZ bond and reconnecting after repeated
  `service discovery timed out` on a known device (only safe for Just Works
  devices). Do the aging test from BUGS.md first to confirm the trigger.

## Cross-platform: macOS and Windows
The split prepared the ground; the implementations are not written. Each new OS
needs one `internal/app/platform_<os>.go` implementing the contract (`dataDir`,
`notify`, `openLogFolder`, the autostart trio, `openAdapter`), plus
`lock_windows.go` for Windows (flock is shared via `lock_unix.go` on
Linux/macOS). No shared files should need editing. `describeConnectErr` strings
are BlueZ-specific and fall through harmlessly elsewhere. Note macOS BLE uses
cgo (CoreBluetooth), so that build needs `CGO_ENABLED=1` and an SDK.
