# gotempo todo

## CLI

Goal: allow gotempo to run fully headless (no systray/AppIndicator) and be
controllable via flags, for users without tray support or who want to run it
as a service/from scripts.

The full flag set has shipped (see Done): `--no-tray`, `--list-devices`,
`--status`, `--print-bpm` (+`--epoch`/`--timestamp`), `--json`, `--config`,
`--version`/`-v`, `--autostart`/`--no-autostart`, `--device`, `--select-device`,
`--auto-log`/`--no-auto-log`, `--quiet`, `--log-level`. The per-flag specs below
describe the implemented behavior.

Flags fall into two classes. **Session-only** flags (everything except the
setup flags) affect the single run and never write `config.json` or disk.
**Setup** flags persist a change: `--autostart`/`--no-autostart` write or remove
the autostart entry and exit; `--device`/`--select-device` save the chosen device
to config and then continue into a normal run respecting the other flags.

### Flags

#### `--no-tray`
Run without initializing the systray/AppIndicator at all. App runs as a plain
foreground or background process. All other functionality (BLE connection,
BPM file writing, session logging) works identically.

#### `--config <path>`
Override the default config file location. If not provided, use the existing
default path. If the file doesn't exist, error out (exit code 1) rather than
silently using defaults, since the user explicitly pointed somewhere.

#### `--device <mac>` (shipped)
Setup flag. Sets the current device by MAC in `config.json`, then continues into
a normal run (respecting other flags like `--no-tray`). Accepts a MAC only — case
and `:`/`-` separators are normalized (`normalizeMAC`); anything that isn't a MAC
(an index, a typo) is rejected with exit 1 before config is touched. An unknown
MAC is added to the known list with a blank name (it backfills on first connect);
no scan is run just to resolve the name. Index selection lives only in
`--select-device`, where the numbering is stable within one scan.

#### `--list-devices`
Scan for available BLE heart rate devices, print them (one per line: ID and
name), then exit immediately. Does not connect, does not start logging.
Exit code 0 on successful scan (even if zero devices found), non-zero only
on BLE adapter failure.

#### `--autostart` / `--no-autostart` (shipped)
Setup flags. `--autostart` writes the autostart entry (launch-on-login),
`--no-autostart` removes it; both then exit without starting the app. They wrap
the platform autostart contract (`enableAutostart`/`disableAutostart`), so this
is the CLI equivalent of the tray's "Start on boot" toggle. `--autostart`
overwrites an existing entry silently; `--no-autostart` on a missing entry is a
no-op success (exit 0, like `rm -f`). Only a real write/remove failure (e.g.
permissions) is an error (exit 1). The two together is a bad combination
(exit 1). The entry always launches the tray with no flags.

#### `--select-device` (shipped)
Setup flag, interactive. Lists the known devices first as a numbered list, with
`s` to scan on demand and `q` to cancel; picking a known device skips the scan
entirely. A scan merges its results with the known list (dedup by MAC), then the
user picks by number. The selection is saved as current in `config.json` and the
run continues. Needs an interactive terminal: if stdin is not a tty (piped,
redirected, under a service) it exits 1 rather than blocking. Cancelling exits 1.
Mutually exclusive with `--device` (both → exit 1).

#### `--auto-log` / `--no-auto-log` (shipped)
Session-only override of `auto_log`; never written to `config.json`
(`cliOptions.effectiveAutoLog`). Base is the config value. `--no-tray` is the
explicit daemon signal, so it defaults logging on (even with `--print-bpm`, which
is additive — "also stream", not "watch-only"); `--no-auto-log` opts back out. A
bare `--print-bpm` (without `--no-tray`) does **not** enable logging; pair it with
`--auto-log` for that. The explicit flags win over the headless default either
way. The two flags together → exit 1.

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

#### `--quiet` (shipped)
Errors only: same as `--log-level error`. Suppresses the info/lifecycle lines on
stderr; errors still print and the exit code is unchanged. It does not suppress
`--print-bpm` or `--status` output (those are explicit data requests, not
logging). When combined with `--log-level`, `--quiet` wins.

#### `--log-level error|info|debug` (shipped)
Controls operational logging verbosity to stderr (`log.go`, gated by
`currentLogLevel`). Default `info`; an unrecognized value exits 1.

- `error`: only failures (connect failures, file/status write errors)
- `info`: error + lifecycle events (adapter, connect/scan, session start/end,
  device, switching)
- `debug`: info + per-reading detail (`[BPM] reading N` in `handleBPM`)

The leveled helpers (`logErrf`/`logInfof`/`logDebugf`, plus `…ln` variants)
replace the bare `log.*` calls package-wide; error logs always print.

### Exit codes

| Code | Meaning |
|------|---------|
| 0 | Clean exit; `--status`: running and connected |
| 1 | Config error (missing `--config` file, bad flag combination) |
| 2 | `--status`: running but not connected |
| 3 | BLE adapter not available, `--no-tray` with no device, or (deferred) `--device` not found |
| 4 | `--status`: gotempo is not running |

Documented in `--help` output and [docs/CLI.md](CLI.md). (File-I/O failures
currently log and continue rather than mapping to a distinct exit code.)

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
- Most CLI flags are session-only overrides and do not modify `config.json`.
  The setup flags are the exception (see the two-class note up top): the
  autostart flags write/remove the autostart entry and exit; the deferred device
  flags save the chosen device and continue. Other persistent setting changes
  require editing the config file directly.

### Out of scope (not in this spec)

- IPC/runtime control of a running daemon (e.g. start/stop session via signal
  to an already-running process). If needed later, would require a unix
  socket or PID-file + signal scheme. Not needed for initial CLI support,
  since `--auto-log` + the existing gap-based session logic covers normal
  usage without manual start/stop.

## Done

- **CLI batch 2: device, auto-log, logging.** Completes the flag set on top of
  the autostart flags.
  - `--device <mac>` / `--select-device` (`device.go`): setup flags that set the
    current device in config then continue the run. `--device` validates a MAC
    (`normalizeMAC` canonicalizes case and `:`/`-`; non-MAC → exit 1 before
    touching config) and upserts it with a blank name. `--select-device` is the
    interactive picker — known devices first, on-demand scan (`s`), cancel (`q`);
    refuses a non-tty (exit 1) and a cancel (exit 1). The two are mutually
    exclusive (exit 1). Wiring in `cmdRun` via `applySetupDevice`.
  - `--auto-log` / `--no-auto-log` (`cliOptions.effectiveAutoLog`): session-only
    override, never persisted. `--no-tray` (the daemon signal) defaults logging on
    — including with `--print-bpm`, which is additive; a bare `--print-bpm` does
    not. `--no-auto-log` opts out; explicit flags win; the two together → exit 1.
    Applied in `cmdRun` via `setLogging` before the worker.
  - `--quiet` / `--log-level error|info|debug` (`log.go`): leveled logging. The
    bare `log.*` calls package-wide became `logErrf`/`logInfof`/`logDebugf`
    (+`…ln`); errors always print, info is lifecycle, debug adds `[BPM] reading N`.
    Default info; `--quiet` == error and wins over `--log-level`; bad level →
    exit 1. Resolved at the top of `Run` before anything logs. Tests in
    `cli_test.go` (`effectiveAutoLog`, parse cases) and `log_test.go`
    (`parseLogLevel`, level gating).
- **Autostart setup flags.** `--autostart` / `--no-autostart` (`cmdAutostart` in
  `cli.go`, dispatched as a one-shot in `run.go` before the run path). They wrap
  the existing platform autostart contract, so they match the tray's "Start on
  boot" toggle. Enable overwrites silently; disable on a missing entry is a no-op
  success (exit 0); a write/remove failure or the two flags together is exit 1.
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
done, so the next organic failure should be self-diagnosing.

- **Connect a known device by address (done).** Reconnection (both the finite
  schedule and the persistent phase) now connects straight to the device by
  address with no scan, replacing the old scan-gate that required a scan hit
  first. A direct connect targets only the peer address, so it emits no
  scan-request probes to other devices in range — this was also a privacy fix
  (continuous active scanning was probing neighbors). There is deliberately **no
  scan fallback**: background scanning is gone entirely, so the device must be
  known to BlueZ (bonded / seen once via the tray pick or Rescan). `findDevice`
  and `persistScan` were removed; `connectDevice` (in `ble.go`) wraps
  `adapter.Connect` so a blocking attempt is still cancellable on stop/switch.
- **Self-heal** by dropping the BlueZ bond and reconnecting after repeated
  `service discovery timed out` on a known device (only safe for Just Works
  devices). Do the aging test from BUGS.md first to confirm the trigger.

## Cross-platform: macOS and Windows
The split prepared the ground; the implementations are not written. Each new OS
needs one `internal/app/platform_<os>.go` implementing the contract (`dataDir`,
`notify`, `openFolder`, the autostart trio, `openAdapter`), plus
`lock_windows.go` for Windows (flock is shared via `lock_unix.go` on
Linux/macOS). No shared files should need editing. `describeConnectErr` strings
are BlueZ-specific and fall through harmlessly elsewhere. Note macOS BLE uses
cgo (CoreBluetooth), so that build needs `CGO_ENABLED=1` and an SDK.
