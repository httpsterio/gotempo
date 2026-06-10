# gotempo todo

## Split main.go into multiple files
main.go is ~1240 lines. Split along existing section seams, all still `package main`:
- `paths.go`: XDG paths, autostart, instance lock
- `config.go`: Config/KnownDevice, load/save, upsert/touch/sortedKnown
- `ble.go`: scanning, ensureAdapter, AppState, connect/reconnect state machine
- `tray.go`: tray struct, refresh/renderSwitch/loop, buildEntries/slotLabel
- `main.go`: main(), constants, embeds, notify
No behaviour change. Split tests to match. Optional, low priority.

## Dead code cleanup
- Remove unused `resetBPM` (no callers).
- Log the `os.WriteFile` error in `onSwitch` for consistency with handleBPM.

## BLE reconnect robustness
See [BUGS.md](BUGS.md) (strap won't reconnect after a long idle).
- Connect a known device by address instead of requiring a scan hit. A bonded or already-connected strap does not advertise, so the scan-gate locks it out. Try direct connect, fall back to scan.
- Log the connect/discovery error in the persistent phase. `persistentConnect` swallows it now, so failures are invisible until a restart drops into the finite phase.
- Maybe: self-heal by dropping the BlueZ bond and reconnecting after repeated `service discovery timed out` on a known device (only safe for Just Works devices).

## Improve logging
Not just errors. Make the log useful for debugging connection issues.
- Log lifecycle events: scan start/result, connect, disconnect, session length, reconnect, device switch, adapter changes.
- Consider log levels (info/warn/error) and a quiet default.
- Timestamps are already there; keep them.
