# polar-hr — Agent Handoff Document (Go rewrite)

## What you are building

A lightweight Linux system tray app written in Go. It connects to a specific Polar H10 heart rate monitor via Bluetooth LE, shows connection status as a tray icon, and optionally writes the current BPM as a raw integer to a text file for use as an OBS text source.

This is a rewrite of a working Python/bleak/pystray implementation. The Python version is included at the bottom of this document as a reference for all logic — connection handling, retry schedule, state machine, BPM parsing. Port the logic faithfully, do not redesign it.

---

## Environment

- OS: Arch Linux, XFCE desktop
- Shell: fish
- Bluetooth stack: BlueZ, `bluetooth.service` must be running
- User has Go installed (verify with `go version`)
- No Docker

---

## Verified device information

Confirmed in a prior session. Do not rediscover.

| Property | Value |
|---|---|
| Device name | Polar H10 1841CC31 |
| MAC address | 24:AC:AC:18:41:CC |
| BLE Heart Rate Service UUID | `0000180d-0000-1000-8000-00805f9b34fb` |
| BLE HR Measurement Characteristic UUID | `00002a37-0000-1000-8000-00805f9b34fb` |
| Connections | 2 simultaneous BLE (user has enabled dual-BLE in Polar Flow) |
| Protocol | Standard BLE GATT, no proprietary handshake required |
| Pairing required | No — OS pairing is not needed to subscribe to HR notifications |
| Update rate | 1 Hz |

**BPM parsing:** flags byte is `data[0]`. If bit 0 is 0, HR is `data[1]` (uint8). If bit 0 is 1, HR is `uint16(data[1]) | uint16(data[2])<<8` (little-endian). Always round to nearest int.

---

## Go libraries

### Systray: `fyne.io/systray`

Use this, not `getlantern/systray`. It is a fork that removes the GTK3 dependency and works via StatusNotifierItem directly — no system libs required at build time beyond what BlueZ already needs.

```
go get fyne.io/systray
```

API is identical to getlantern/systray:
```go
import "fyne.io/systray"

func main() {
    systray.Run(onReady, onExit)
}

func onReady() {
    systray.SetIcon(iconBytes)
    systray.SetTooltip("polar-hr")
    mStart := systray.AddMenuItem("Start logging", "")
    mQuit  := systray.AddMenuItem("Quit", "")
    go func() {
        for {
            select {
            case <-mStart.ClickedCh:
                // handle
            case <-mQuit.ClickedCh:
                systray.Quit()
            }
        }
    }()
}
```

Menu items are shown/hidden with `item.Show()` / `item.Hide()`. Icon is updated with `systray.SetIcon([]byte)`. `systray.Run` blocks the main goroutine — all other work goes in goroutines.

### BLE: `tinygo.org/x/bluetooth`

```
go get tinygo.org/x/bluetooth
```

Uses BlueZ via D-Bus on Linux. Requires BlueZ >= 5.48 (Arch ships a recent version, this is fine).

Basic usage pattern for this project:
```go
import "tinygo.org/x/bluetooth"

var adapter = bluetooth.DefaultAdapter

// enable
adapter.Enable()

// scan by address
adapter.Scan(func(adapter *bluetooth.Adapter, result bluetooth.ScanResult) {
    if result.Address.String() == targetMAC {
        adapter.StopScan()
        // connect
    }
})

// connect
device, err := adapter.Connect(address, bluetooth.ConnectionParams{})

// discover services
srvcs, err := device.DiscoverServices([]bluetooth.UUID{hrServiceUUID})

// discover characteristics
chars, err := srvcs[0].DiscoverCharacteristics([]bluetooth.UUID{hrCharUUID})

// subscribe
chars[0].EnableNotifications(func(buf []byte) {
    // parse BPM from buf
})
```

**Known limitation:** `tinygo.org/x/bluetooth` on Linux does not support connecting by MAC address directly in all versions. If `adapter.Connect` by address does not work, scan first, match by address string in the scan callback, stop scan, then connect to the found device. This is the safer pattern regardless.

### Notifications: `os/exec`

```go
exec.Command("notify-send", "polar-hr", message).Run()
```

No extra deps.

### Images: embed

Embed the PNG files into the binary at compile time using Go's `embed` package. Do not load from disk at runtime.

```go
import _ "embed"

//go:embed disconnected.png
var imgDisconnected []byte

//go:embed connected.png
var imgConnected []byte

//go:embed running.png
var imgRunning []byte
```

Pass the byte slices directly to `systray.SetIcon(imgDisconnected)`.

---

## Project structure

```
polar-hr/
├── main.go
├── go.mod
├── go.sum
├── config.json          # written on first run, gitignored
├── polar-h10.txt        # BPM output, overwritten each update
├── disconnected.png     # tray icon: no device
├── connected.png        # tray icon: connected, not logging
└── running.png          # tray icon: connected, logging active
```

All files in one directory. No `~/.config` usage. Self-contained folder.

Config file location: same directory as the binary, resolved via `os.Executable()`.
Output file location: same directory as the binary.

---

## Config

`config.json` format:
```json
{
  "mac": "24:AC:AC:18:41:CC",
  "name": "Polar H10 1841CC31"
}
```

On startup: check if config exists and has a valid MAC. If yes, skip scanning and connect directly.

If no config: scan for BLE devices advertising the heart rate service UUID `0000180d-0000-1000-8000-00805f9b34fb`. Show discovered devices as a submenu under a "Select device" tray menu item. User clicks a device name, it gets saved to config, connection begins. Remove the "Select device" item from the tray after selection.

---

## App state machine

Four states:

| State | Icon | Tray menu |
|---|---|---|
| `Disconnected` | `disconnected.png` | Quit |
| `GaveUp` | `disconnected.png` | Retry, Quit |
| `Connected` | `connected.png` | Start logging, Quit |
| `Running` | `running.png` | Stop logging, Quit |

State is shared between the BLE goroutine and the tray. Use a mutex or channel to update icon and menu items from the BLE goroutine. Call `systray.SetIcon` and `item.Show()`/`item.Hide()` to reflect state changes.

---

## Connection and retry logic

Port this exactly from the Python implementation.

### Retry schedule
```
Attempts 1–5:   3s between tries
Attempts 6–10:  10s between tries
Attempts 11–13: 60s between tries
After attempt 13: give up
```

### On give-up
- State → `GaveUp`
- `notify-send`: "could not connect to H10"
- Show "Retry" menu item
- Wait for user to click Retry
- On Retry: reset attempt counter, state → `Disconnected`, restart connection loop

### On successful connection
- Reset attempt counter
- State → `Connected`
- If this reconnect followed a notified loss: `notify-send` "reconnected"

### On connection drop mid-session (fast retry path)
1. Run 3 fast retries at 3s each, silently
2. If any fast retry succeeds: resume, no notification
3. If all 3 fail:
   - `notify-send` "connection lost"
   - State → `Disconnected`
   - Fall into main retry schedule from attempt 1
   - If logging was active: resume automatically on reconnect

---

## BPM output

File: `polar-h10.txt` in the same directory as the binary.

- Write only when logging is active (state == `Running`)
- Write only when BPM value changes from last written value
- Overwrite entire file with just the integer, no newline required
- Example file contents: `78`

---

## Notifications

```go
exec.Command("notify-send", "polar-hr", message).Run()
```

Cases:
- "could not connect to H10" — after retries exhausted
- "connection lost" — after fast retries fail mid-session
- "reconnected" — after reconnecting following a notified loss

---

## Build

Standard Go build, no special flags needed for the app logic:
```
go build -o polar-hr .
```

The binary embeds the PNG files via `//go:embed` so no assets need to ship alongside it. BlueZ (`bluetooth.service`) must be running on the host — this is a system dependency that cannot be bundled.

To suppress the terminal window if launched as a desktop app (not needed for now but useful later):
```
go build -ldflags "-w -s" -o polar-hr .
```

---

## Error handling requirements

- If Bluetooth adapter is unavailable: catch error, stay in `Disconnected` state, keep retrying per schedule
- If device not found during scan: same
- If `polar-h10.txt` cannot be written: print to stdout, continue, do not crash
- Never crash on BLE errors — all connection errors should feed into the retry loop

---

## Stretch goals (do not implement, just be aware)

**Autostart toggle:** write/delete `~/.config/autostart/polar-hr.desktop` to control whether the app launches on login. Expose as a checkable tray menu item "Start on boot".

Desktop file contents:
```ini
[Desktop Entry]
Type=Application
Name=polar-hr
Exec=/path/to/polar-hr
Hidden=false
NoDisplay=false
X-GNOME-Autostart-enabled=true
```

Use `os.Executable()` to get the binary path.

**Switch device:** tray submenu "Switch device" triggers a fresh BLE scan, populates submenu with found HR devices, clicking one overwrites config and reconnects. No restart required.

---

## Python reference implementation

The following is the working Python implementation this Go version must match in behavior. Use it as the authoritative reference for all logic.

```python
import asyncio
import json
import subprocess
import sys
import threading
from pathlib import Path

import bleak
from bleak import BleakClient, BleakScanner
from PIL import Image
import pystray

# ── constants ────────────────────────────────────────────────────────────────

HR_SERVICE_UUID = "0000180d-0000-1000-8000-00805f9b34fb"
HR_CHAR_UUID    = "00002a37-0000-1000-8000-00805f9b34fb"
CONFIG_FILE     = Path(__file__).parent / "config.json"
OUTPUT_FILE     = Path(__file__).parent / "polar-h10.txt"

RETRY_SCHEDULE = [(5, 3), (5, 10), (3, 60)]  # (attempts, interval_seconds)

# ── icon helpers ─────────────────────────────────────────────────────────────

def resource_path(filename):
    base = getattr(sys, "_MEIPASS", Path(__file__).parent)
    return Path(base) / filename

def load_image(name):
    return Image.open(resource_path(name))

# ── notifications ─────────────────────────────────────────────────────────────

def notify(msg):
    subprocess.run(["notify-send", "polar-hr", msg], check=False)

# ── config ────────────────────────────────────────────────────────────────────

def load_config():
    if CONFIG_FILE.exists():
        try:
            data = json.loads(CONFIG_FILE.read_text())
            if data.get("mac") and data.get("name"):
                return data
        except Exception:
            pass
    return None

def save_config(mac, name):
    CONFIG_FILE.write_text(json.dumps({"mac": mac, "name": name}, indent=2))

def scan_and_select():
    print("Scanning for Polar H10 (15 s)…")

    async def do_scan():
        devices = await BleakScanner.discover(
            timeout=15.0,
            service_uuids=[HR_SERVICE_UUID],
        )
        return devices

    devices = asyncio.run(do_scan())
    if not devices:
        print("No heart-rate devices found. Make sure the H10 is awake and dual-BLE is enabled.")
        sys.exit(1)

    for i, d in enumerate(devices):
        print(f"  {i+1}. {d.name}  ({d.address})")

    while True:
        try:
            choice = int(input("Select device number: ")) - 1
            if 0 <= choice < len(devices):
                break
        except (ValueError, EOFError):
            pass
        print("Invalid choice.")

    selected = devices[choice]
    save_config(selected.address, selected.name)
    print(f"Saved: {selected.name} ({selected.address})")
    return {"mac": selected.address, "name": selected.name}

# ── shared state ──────────────────────────────────────────────────────────────

class AppState:
    def __init__(self):
        self._lock = threading.Lock()
        self.connected = False
        self.logging   = False
        self.gave_up   = False
        self.last_bpm  = None

    def set(self, **kwargs):
        with self._lock:
            for k, v in kwargs.items():
                setattr(self, k, v)

    def get(self, *keys):
        with self._lock:
            if len(keys) == 1:
                return getattr(self, keys[0])
            return tuple(getattr(self, k) for k in keys)

# ── tray ──────────────────────────────────────────────────────────────────────

class TrayApp:
    def __init__(self, state: AppState, ble: "BLEWorker"):
        self.state = state
        self.ble   = ble
        self._icon = pystray.Icon(
            "polar-hr",
            load_image("disconnected.png"),
            "polar-hr",
            menu=self._build_menu(),
        )

    def _build_menu(self):
        return pystray.Menu(
            pystray.MenuItem("Retry",         self._on_retry,        visible=lambda _: self.state.gave_up),
            pystray.MenuItem("Start logging", self._on_start_log,    visible=lambda _: self.state.connected and not self.state.logging and not self.state.gave_up),
            pystray.MenuItem("Stop logging",  self._on_stop_log,     visible=lambda _: self.state.connected and self.state.logging),
            pystray.MenuItem("Quit",          self._on_quit),
        )

    def run(self):
        self._icon.run()

    def refresh(self):
        connected, logging, gave_up = self.state.get("connected", "logging", "gave_up")
        if gave_up or not connected:
            img_name = "disconnected.png"
        elif logging:
            img_name = "running.png"
        else:
            img_name = "connected.png"
        self._icon.icon = load_image(img_name)
        self._icon.update_menu()

    def _on_retry(self, icon, item):
        self.state.set(gave_up=False)
        self.ble.trigger_retry()

    def _on_start_log(self, icon, item):
        self.state.set(logging=True)
        self.refresh()

    def _on_stop_log(self, icon, item):
        self.state.set(logging=False)
        self.refresh()

    def _on_quit(self, icon, item):
        self.ble.stop()
        icon.stop()

# ── BLE worker ────────────────────────────────────────────────────────────────

class BLEWorker:
    FAST_RETRIES  = 3
    FAST_INTERVAL = 3

    def __init__(self, mac: str, state: AppState, tray: "TrayApp"):
        self.mac   = mac
        self.state = state
        self.tray  = tray
        self._stop_event   = threading.Event()
        self._retry_event  = threading.Event()
        self._thread       = threading.Thread(target=self._run, daemon=True)

    def start(self):
        self._thread.start()

    def stop(self):
        self._stop_event.set()

    def trigger_retry(self):
        self._retry_event.set()

    def _run(self):
        asyncio.run(self._connect_loop())

    async def _connect_loop(self):
        notified_loss = False

        def _full_schedule():
            for count, interval in RETRY_SCHEDULE:
                for _ in range(count):
                    yield interval

        main_gen = _full_schedule()

        while not self._stop_event.is_set():
            had_session = False
            try:
                await self._connect_once(notified_loss)
                return
            except Exception as e:
                had_session = str(e) == "session_dropped"
                print(f"[BLE] dropped: {e}")
                self.state.set(connected=False)
                self.tray.refresh()

            if had_session:
                recovered = False
                for _ in range(self.FAST_RETRIES):
                    if self._stop_event.is_set():
                        return
                    await asyncio.sleep(self.FAST_INTERVAL)
                    try:
                        await self._connect_once(False)
                        recovered = True
                        break
                    except Exception:
                        pass

                if recovered:
                    notified_loss = False
                    main_gen = _full_schedule()
                    continue
                else:
                    notify("connection lost")
                    notified_loss = True
                    self.tray.refresh()

            try:
                interval = next(main_gen)
            except StopIteration:
                self.state.set(gave_up=True)
                self.tray.refresh()
                notify("could not connect to H10")
                loop = asyncio.get_running_loop()
                await loop.run_in_executor(None, self._retry_event.wait)
                self._retry_event.clear()
                main_gen = _full_schedule()
                notified_loss = False
                continue

            await asyncio.sleep(interval)

    async def _connect_once(self, was_notified: bool):
        print(f"[BLE] scanning for {self.mac}…")
        device = await BleakScanner.find_device_by_address(self.mac, timeout=10.0)
        if device is None:
            raise ConnectionError(f"device {self.mac} not found in scan")
        print(f"[BLE] connecting…")
        async with BleakClient(device) as client:
            def hr_handler(sender, data: bytearray):
                flags = data[0]
                if flags & 0x01:
                    bpm = int.from_bytes(data[1:3], "little")
                else:
                    bpm = data[1]
                self._handle_bpm(bpm)

            await client.start_notify(HR_CHAR_UUID, hr_handler)

            print("[BLE] connected")
            self.state.set(connected=True, gave_up=False)
            if was_notified:
                notify("reconnected")
            self.tray.refresh()

            while not self._stop_event.is_set():
                if not client.is_connected:
                    break
                await asyncio.sleep(1)

            try:
                await client.stop_notify(HR_CHAR_UUID)
            except Exception:
                pass

        if self._stop_event.is_set():
            return
        raise ConnectionError("session_dropped")

    def _handle_bpm(self, bpm: int):
        if not self.state.get("logging"):
            return
        last = self.state.get("last_bpm")
        if bpm != last:
            self.state.set(last_bpm=bpm)
            try:
                OUTPUT_FILE.write_text(str(bpm))
            except Exception as e:
                print(f"[BPM] could not write output: {e}")

# ── entry point ───────────────────────────────────────────────────────────────

def main():
    config = load_config()
    if config is None:
        config = scan_and_select()

    mac  = config["mac"]
    name = config["name"]
    print(f"Using device: {name} ({mac})")

    state = AppState()

    worker = BLEWorker.__new__(BLEWorker)
    tray   = TrayApp(state, worker)
    BLEWorker.__init__(worker, mac, state, tray)

    worker.start()
    tray.run()


if __name__ == "__main__":
    main()
```
