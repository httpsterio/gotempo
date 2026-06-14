# gotempo todo

## Done

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
