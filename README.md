# gotempo

![gotempo](assets/logo.png)

A small Linux tray app that connects to a Bluetooth LE heart-rate monitor, shows connection status in the tray, and writes the current BPM to a text file. Useful as an OBS text source.

Works with any BLE monitor using the standard GATT HR profile (0x180D / 0x2A37): Polar H10, H9, and most other chest straps and optical armbands.


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


## Install

Grab the latest build from [releases](https://github.com/httpsterio/gotempo/releases/latest) and extract it. Run `install.sh` to add gotempo to your applications menu, or just run `gotempo` directly. `uninstall.sh` removes it.

Config and data live under `~/.config/gotempo` and `~/.local/share/gotempo` (see [Files](#files)).


## Requirements

- Linux with BlueZ (`bluetooth.service` running)
- A StatusNotifierItem system tray (XFCE, KDE, most modern panels). The app won't start without one. GNOME has no built-in tray; install the [AppIndicator and KStatusNotifierItem Support](https://extensions.gnome.org/extension/615/appindicator-support/) extension to get the tray icon.
- `notify-send` (from `libnotify`), optional. Notifications are skipped if missing.


## Build from source

```sh
git clone https://github.com/httpsterio/gotempo
cd gotempo
go build -o gotempo .
```

Requires Go 1.24+. For desktop integration (`make install`), version stamping, and the release process, see [CONTRIBUTING.md](CONTRIBUTING.md).


## First launch

```sh
./gotempo
```

With no `config.json`, the app starts scanning. Open the tray menu, wait for your monitor to appear (it must be awake and worn to advertise), and click it to connect. The choice is saved, and later launches connect directly.

On first connect, accept the pairing prompt if it appears. Most straps allow one connection at a time, so disconnect the monitor from your phone or other apps first. Some allow more than one.


## Tray menu

| Item | Behaviour |
|---|---|
| **Start logging** / **Stop logging** | Begin or stop writing BPM to `logs/gotempo-bpm.txt`. Greyed out when not applicable. |
| **Device list** | Known devices (most-recently-used first) plus newly scanned ones, listed directly in the menu. Click one to switch; the current device is marked and not clickable. Up to six shown. |
| **Rescan for new devices** | Runs a fresh 15-second scan; new monitors appear in the list. |
| **Autostart HR log** | When checked, logging starts automatically on launch. |
| **Start on boot** | Adds or removes `~/.config/autostart/gotempo.desktop`. |
| **Quit** | Exits the app. |


## Reconnection behaviour

When the connection drops, gotempo reconnects on its own:

- It retries silently for a short while. A device that returns in this window reconnects with no notification.
- If that fails, it sends one "device lost" notification and scans continuously until the device returns, then sends "reconnected".

The BPM file keeps its last value across a brief drop and clears after about ten seconds disconnected, so it doesn't leave a stale reading (see [Files](#files)).

It also survives the Bluetooth adapter being toggled off and on, including when it returns on a different `hciN` index.


## Files

gotempo uses standard XDG directories, created on first run:

- `~/.config/gotempo/config.json`: saved device, known-device history, and preferences. Managed by the app; see [Configuration](#configuration) to edit it by hand. Honors `$XDG_CONFIG_HOME`.
- `~/.local/share/gotempo/gotempo-bpm.txt`: current BPM as a raw integer, rewritten on each change. Empty when not logging. Keeps the last reading briefly across a short dropout, then clears after about ten seconds disconnected. Honors `$XDG_DATA_HOME`.
- `assets/` (source tree only): tray status icons and `logo.png`, embedded in the binary at build time.


## Configuration

`config.json` is written and updated automatically. You can edit it by hand, which is handy for headless setups where you set the device without the tray:

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

Set `current` to your device MAC and add a matching `known` entry. The app connects to it on next launch without scanning.


## Planned

- macOS support
- Windows support
