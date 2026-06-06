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
        attempt = 0
        notified_loss = False

        def _schedule_interval():
            used = 0
            for count, interval in RETRY_SCHEDULE:
                for _ in range(count):
                    yield interval
                    used += 1
            # exhausted
            return

        gen = _schedule_interval()

        while not self._stop_event.is_set():
            try:
                interval = next(gen)
            except StopIteration:
                # gave up
                self.state.set(connected=False, gave_up=True)
                self.tray.refresh()
                notify("could not connect to H10")
                loop = asyncio.get_running_loop()
                await loop.run_in_executor(None, self._retry_event.wait)
                self._retry_event.clear()
                gen = _schedule_interval()
                notified_loss = False
                continue

            try:
                await self._connect_once(notified_loss)
                # successful session — reset on next drop
                gen = _schedule_interval()
                notified_loss = False
                attempt = 0
            except Exception as e:
                print(f"[BLE] connection error: {e}")
                self.state.set(connected=False)
                self.tray.refresh()
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
        raise ConnectionError("session_dropped")  # had a real session, then lost it

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

# ── fast-retry wrapper for mid-session drops ──────────────────────────────────

class BLEWorkerWithFastRetry(BLEWorker):
    FAST_RETRIES   = 3
    FAST_INTERVAL  = 3

    async def _connect_loop(self):
        was_connected = False
        notified_loss = False

        def _schedule():
            for interval in (self._full_schedule()):
                yield interval

        def _full_schedule():
            for count, interval in RETRY_SCHEDULE:
                for _ in range(count):
                    yield interval

        main_gen = _full_schedule()

        while not self._stop_event.is_set():
            try:
                await self._connect_once(notified_loss)
                was_connected = True
                notified_loss = False
                main_gen = _full_schedule()
            except Exception as e:
                if str(e) == "session_dropped":
                    was_connected = True
                print(f"[BLE] dropped: {e}")
                self.state.set(connected=False)

                if was_connected:
                    # fast retries first
                    recovered = False
                    logging_was_active = self.state.get("logging")
                    for i in range(self.FAST_RETRIES):
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
                        was_connected = True
                        notified_loss = False
                        main_gen = _full_schedule()
                        continue
                    else:
                        notify("connection lost")
                        notified_loss = True
                        self.tray.refresh()
                        was_connected = False

                # fall through to main retry schedule
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
                    was_connected = False
                    continue

                await asyncio.sleep(interval)

# ── entry point ───────────────────────────────────────────────────────────────

def main():
    config = load_config()
    if config is None:
        config = scan_and_select()

    mac  = config["mac"]
    name = config["name"]
    print(f"Using device: {name} ({mac})")

    state = AppState()

    # tray and worker reference each other; build tray with a placeholder worker
    # then assign
    worker = BLEWorkerWithFastRetry.__new__(BLEWorkerWithFastRetry)
    tray   = TrayApp(state, worker)
    BLEWorkerWithFastRetry.__init__(worker, mac, state, tray)

    worker.start()
    tray.run()  # blocks on main thread


if __name__ == "__main__":
    main()
