
# polar-hr — Agent Handoff Document

## What you are building

A lightweight Linux system tray app that connects to a specific Polar H10 heart rate monitor via Bluetooth LE, shows connection status as a tray icon, and optionally writes the current BPM as a raw integer to a text file (for use as an OBS text source).

---

## Environment

- OS: Arch Linux, XFCE desktop
- Shell: fish
- Python: system Python 3, available as `python`
- Bluetooth stack: BlueZ, `bluetooth.service` must be running
- Available tools: `rg`, `fd`, `jq`, `tree`
- No Docker

---

## Verified device information

These facts have been confirmed by the user in a prior session. Do not rediscover them.

| Property | Value |
|---|---|
| Device name | Polar H10 1841CC31 |
| MAC address | 24:AC:AC:18:41:CC |
| BLE Heart Rate Service UUID | `0000180d-0000-1000-8000-00805f9b34fb` |
| BLE HR Measurement Characteristic UUID | `00002a37-0000-1000-8000-00805f9b34fb` |
| Connections | 2 simultaneous BLE (must be enabled in Polar Beat app — user has done this) |
| Protocol | Standard BLE GATT, no proprietary handshake required |
| Pairing required | No — the device does not need to be OS-paired to subscribe to HR notifications |
| HR data format | Flags byte at `data[0]`, bit 0 determines HR width: if 0, HR = `data[1]` (uint8); if 1, HR = `int.from_bytes(data[1:3], 'little')` (uint16). Always round to nearest int. |
| RR intervals | Packed as additional uint16 values after the HR field when flags bit 4 is set. Not needed for this project currently. |
| Update rate | 1 Hz |

The test script connecting to this device with `BleakClient` and subscribing to `00002a37-0000-1000-8000-00805f9b34fb` returned live BPM values successfully. The user's phone (running Polar Flow) may also be connected simultaneously — this is fine with dual BLE enabled.

---

## Project structure

All files live in one directory. No files go in `~/.config` or anywhere else. The goal is a self-contained folder.

```
polar-hr/
├── main.py
├── config.json          # written on first run, never committed
├── polar-h10.txt        # output file, overwritten on every BPM update
├── disconnected.png     # tray icon: no device
├── connected.png        # tray icon: device connected, not logging
├── running.png          # tray icon: device connected, logging active
└── requirements.txt
```

---

## Dependencies

```
bleak
pystray
pillow
```

Install with:
```
pip install bleak pystray pillow --break-system-packages
```

Also create `requirements.txt` with those three packages.

---

## First run — device selection

- On startup, check if `config.json` exists and contains a valid MAC
- If not: scan BLE for 15 seconds, filtering by heart rate service UUID `0000180d-0000-1000-8000-00805f9b34fb`
- Print a numbered list of discovered devices to stdout (name + MAC)
- User types a number to select their device
- Save MAC and name to `config.json`
- All future runs skip scanning and connect directly to the saved MAC

`config.json` format:
```json
{
  "mac": "24:AC:AC:18:41:CC",
  "name": "Polar H10 1841CC31"
}
```

---

## Connection and retry logic

On startup (after config is loaded), begin the connection cycle immediately.

### Retry schedule

| Attempts | Interval |
|---|---|
| 1–5 | 3s |
| 6–10 | 10s |
| 11–13 | 60s |
| After attempt 13 | Give up |

### On give-up

- Send desktop notification via `notify-send`: "polar-hr: could not connect to H10"
- Set icon to `disconnected.png`
- Show "Retry" option in tray menu
- Clicking Retry resets attempt counter to 0 and restarts the cycle

### On connection drop mid-session

- Attempts 1–3 (3s each): silent fast retry, keep current icon
- If logging was active when drop occurred: keep `running.png` during fast retries, resume logging automatically on reconnect
- If fast retries fail: notify "polar-hr: connection lost", icon to `disconnected.png`, start full retry schedule from attempt 1
- If reconnect succeeds after a notified loss: notify "polar-hr: reconnected"

---

## Tray icon and menu

Use `pystray`. Load images with Pillow from the script's own directory (use `Path(__file__).parent` to locate them).

### Icon states

| State | Icon |
|---|---|
| No device / gave up | `disconnected.png` |
| Connected, not logging | `connected.png` |
| Connected, logging | `running.png` |

### Tray menu

| App state | Menu items |
|---|---|
| Disconnected (gave up) | Retry, Quit |
| Connected | Start logging, Quit |
| Running | Stop logging, Quit |

---

## Logging

- Output file: `polar-hr/polar-h10.txt`
- Contents: a single raw integer, no newline required — e.g. `78`
- Behavior: overwrite the entire file on every BPM update, only if the value has changed from the last written value
- No timestamps, no headers, no CSV — just the number
- This file is read by OBS as a text source

---

## Notifications

Use `subprocess.run(["notify-send", "polar-hr", "<message>"])`. No extra dependencies. Works on XFCE with libnotify installed (standard on Arch/XFCE).

Notification cases:
- Could not connect after all retries exhausted
- Connection lost mid-session (after fast retries fail)
- Reconnected after a notified loss (optional but recommended)

---

## Concurrency model

- `pystray` must run on the main thread (Linux requirement)
- `bleak` (asyncio) runs in a background thread via `asyncio.run()` in a `threading.Thread`
- Share state between threads using a simple state object with `threading.Lock` or `threading.Event` flags
- No queues needed for this use case

Do not use `asyncio.run()` on the main thread — pystray will block it.

---

## Error handling requirements

- If Bluetooth is off or unavailable: catch the exception, set icon to `disconnected.png`, log a warning to stdout, keep retrying
- If device is not found during scan: same
- Never crash on missing BT adapter
- Never crash if `polar-h10.txt` can't be written — log to stdout and continue

---

## Stretch goal — single binary

After the app is working correctly, build a standalone executable with PyInstaller:

```
pip install pyinstaller --break-system-packages
pyinstaller --onefile --add-data "disconnected.png:." --add-data "connected.png:." --add-data "running.png:." main.py
```

Output: `dist/main` — a single file the user can run without Python installed. The three PNG files should be bundled into the binary. At runtime, BlueZ (`bluetooth.service`) must be running on the host — this is a system dependency that cannot be bundled.

To access bundled data files inside a PyInstaller binary, use:
```python
import sys, os
def resource_path(filename):
    base = getattr(sys, '_MEIPASS', Path(__file__).parent)
    return Path(base) / filename
```

---

## What not to do

- Do not use `~/.config` or any path outside the project folder for config or output files
- Do not pair or bond the device at the OS level — bleak connects directly
- Do not scan for all HR devices on every run — always use the saved MAC from `config.json` after first run
- Do not connect to any H10 other than MAC `24:AC:AC:18:41:CC`
- Do not write anything other than a raw integer to `polar-h10.txt`
- Do not use threading primitives that could deadlock pystray's main thread
