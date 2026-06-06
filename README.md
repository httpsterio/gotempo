# gotempo

A lightweight Linux system-tray app that connects to a Bluetooth LE heart-rate monitor, shows connection status as a tray icon, and writes the current BPM to a text file. Useful as an OBS text source or anywhere you want live BPM data in a file.

Works with any BLE heart-rate monitor that uses the standard GATT HR profile (0x180D / 0x2A37) — Polar H10, Polar H9, and most other chest straps and optical armbands.

---

## Contents

- [Install](#install)
- [Requirements](#requirements)
- [Build from source](#build-from-source)
- [First launch](#first-launch)
- [Tray menu](#tray-menu)
- [Reconnection behaviour](#reconnection-behaviour)
- [Files](#files)
- [Configuration](#configuration)
- [Planned](#planned)

---

## Install

Download the latest binary and put it somewhere on your PATH:

```sh
curl -L https://github.com/httpsterio/gotempo/releases/latest/download/gotempo \
  -o ~/.local/bin/gotempo
chmod +x ~/.local/bin/gotempo
```

Put the binary somewhere permanent before first launch. `config.json` and the `logs/` directory are created next to the binary, so if you move it later you will lose your saved device.

---

## Requirements

- Linux with BlueZ (`bluetooth.service` must be running)
- A StatusNotifierItem-capable system tray — XFCE, KDE, and most modern panels qualify. Without a tray host the app will not start.
- `notify-send` (usually provided by `libnotify`) — optional, notifications are silently skipped if missing

---

## Build from source

```sh
git clone https://github.com/httpsterio/gotempo
cd gotempo
go build -o gotempo .
```

Requires Go 1.24+. The PNG icons are embedded in the binary at build time — no assets need to ship alongside it. BlueZ is a runtime dependency and cannot be bundled.

### Desktop integration (optional)

To install the binary plus an application menu entry and icon under `~/.local` (no sudo):

```sh
make install
```

This places `gotempo` in `~/.local/bin`, an icon under `~/.local/share/icons`, and `gotempo.desktop` in `~/.local/share/applications`, so gotempo appears in your app menu. Use `make uninstall` to remove them. Override the location with `PREFIX`, e.g. `sudo make install PREFIX=/usr/local`.

> Note: Linux binaries can't carry an embedded icon the way Windows `.exe` files do — the icon comes from the installed `.desktop` entry. The current icon is a placeholder; replace `assets/connected.png` (or point `ICON_SRC` in the Makefile at a real logo) before installing.

---

## First launch

```sh
./gotempo
```

On first launch with no `config.json`, the app starts scanning immediately. Open the **Devices** menu in the tray, wait for your monitor to appear (make sure it is awake and worn so it is advertising), and click it to connect. The choice is saved and subsequent launches connect directly without scanning.

---

## Tray menu

| Item | Behaviour |
|---|---|
| **Start logging** / **Stop logging** | Begin or stop writing BPM to `logs/gotempo-bpm.txt`. Greyed out when not applicable. |
| **Devices** | Lists known devices (most-recently-used first) plus any newly scanned ones. Click a device to switch to it. **Rescan for new devices** triggers a fresh 15-second scan. |
| **Autostart HR log** | When checked, logging starts automatically on every launch. |
| **Start on boot** | Adds or removes `~/.config/autostart/gotempo.desktop`. |
| **Quit** | Exits the app. |

---

## Reconnection behaviour

When the connection drops, gotempo retries silently in the background:

1. **5 attempts, 3 seconds apart** — fast silent retries. If the device comes back here, nothing is shown.
2. **5 attempts, 10 seconds apart** — still silent.
3. **60-second retries, indefinitely** — on entering this phase a single "device lost" notification is sent. The app keeps retrying every 60 seconds until it reconnects or is quit. No further notifications until a reconnect happens.

If the device reconnects during the 60-second phase, a "reconnected" notification is sent and the retry schedule resets from the beginning.

The app also survives the Bluetooth adapter being toggled off and on, including cases where it comes back on a different `hciN` index.

---

## Files

All paths are relative to the binary:

- `config.json` — saved device, known-device history, and preferences. Managed by the app; see [Configuration](#configuration) if you need to edit it by hand.
- `logs/gotempo-bpm.txt` — current BPM as a raw integer, overwritten on every change. Empty or absent when logging is not active.
- `assets/` — icon PNGs (`connected.png`, `disconnected.png`, `running.png`). Embedded in the binary at build time.

---

## Configuration

`config.json` is written and updated automatically. You can edit it by hand if needed — useful for headless setups where you want to set the device without going through the tray:

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
  "auto_log": false
}
```

Set `current` to your device MAC and add an entry to `known`. The app will connect to it on next launch without scanning.

---

## Planned

- macOS support
- Windows support
