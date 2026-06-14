# gotempo todo

## CLI

Goal: allow gotempo to run fully headless (no systray/AppIndicator) and be
controllable via flags, for users without tray support or who want to run it
as a service/from scripts.

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

#### `--print-bpm`
Print each BPM reading to stdout, one per line, as it's received. Format:

```
<unix_timestamp> <bpm>
```

or with `--json`:

```json
{"timestamp": "2026-06-08T14:23:00+03:00", "bpm": 72}
```

one JSON object per line (newline-delimited JSON), not a JSON array, so it
can be piped/streamed.

This runs alongside normal operation (BPM file writing, session logging
continue as configured), it's an additional output stream, not a replacement.

#### `--status [--json]`
One-shot mode: connect to the configured/specified device, wait for a single
valid BPM reading (or timeout after e.g. 10 seconds), print it, exit.

Plain output:
```
72 bpm
```

With `--json`:
```json
{"bpm": 72, "connected": true, "timestamp": "2026-06-08T14:23:00+03:00"}
```

If no reading within timeout, plain output prints `no signal` (or similar)
and JSON output sets `"connected": false, "bpm": null`. Exit code reflects
connection status: 0 if connected (reading received), 2 if no signal within
timeout.

Intended for status bar widgets (waybar, polybar) that poll periodically
rather than running gotempo persistently.

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
| 0 | Clean exit |
| 1 | Config error (missing/invalid config file, bad flag combination) |
| 2 | BLE connection failed / no signal (device not reachable or no reading within timeout for `--status`) |
| 3 | BLE adapter not available, or `--device` specified but not found during scan |
| 4 | File I/O error (can't write to log directory, permissions, etc) |

Document these in `--help` output and in the README.

### Signal handling

- `SIGTERM` / `SIGINT`: clean shutdown. Close current session file properly
  (per session logging spec: discard buffered junk, flush, close). Close BLE
  connection. Exit 0.
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
- **Split main.go into multiple files.** Done, and taken further than a flat
  split: the code is now cross-platform-ready. Shared files (`main.go`,
  `assets.go`, `config.go`, `state.go`, `ble.go`, `tray.go`) hold all behaviour;
  platform-specific calls sit behind a contract in `platform_linux.go` and
  `lock_unix.go`. See CONTRIBUTING.md "Code layout".
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
needs one `platform_<os>.go` implementing the contract (`dataDir`, `notify`,
`openLogFolder`, the autostart trio, `openAdapter`), plus `lock_windows.go` for
Windows (flock is shared via `lock_unix.go` on Linux/macOS). No shared files
should need editing. `describeConnectErr` strings are BlueZ-specific and fall
through harmlessly elsewhere.
