# Files & configuration

Where gotempo keeps its state, and how to edit the config by hand. Back to the
[README](../README.md); for the flags that read/write these, see
[Command line](CLI.md).

## File locations

gotempo uses standard XDG directories, created on first run:

- `~/.config/gotempo/config.json`: saved device, known-device history, and preferences. Managed by the app; edit it by hand as described below. Honors `$XDG_CONFIG_HOME`.
- `~/.local/share/gotempo/gotempo-bpm.txt`: current BPM as a raw integer, rewritten on each change. Empty when not logging (cleared the moment you stop). Keeps the last reading briefly across a short dropout, then clears after about ten seconds disconnected. Useful as an OBS text source. Honors `$XDG_DATA_HOME`.
- `~/.local/share/gotempo/sessions/*.csv`: per-session history, one `timestamp,bpm` row per reading. Written while logging is on. A new file starts after a gap longer than `session_gap_minutes`; shorter breaks append to the current file. Readings below `min_bpm_threshold` (sensor off / no contact) are skipped, so they show as gaps in the timestamps rather than junk rows. Files are named by the session's first reading.
- `~/.local/share/gotempo/status.json`: live app state published by the running app, independent of logging — connection, phase, logging flag, current BPM, and device. Read by `gotempo --status` (see [Command line](CLI.md)). Honors `$XDG_DATA_HOME`.
- `internal/app/assets/` (source tree only): tray status icons and `logo.png`, embedded in the binary at build time.

## config.json

`config.json` is created on first run with all keys at their defaults, and updated automatically after that. You can edit it by hand, which is handy for headless setups where you set the device without the tray. On launch each value is validated; a missing, malformed, or out-of-range entry is reset to its default and the file is rewritten, so it never holds a value the app silently ignores:

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
  "auto_log": false,
  "session_gap_minutes": 60,
  "min_bpm_threshold": 20
}
```

Set `current` to your device MAC and add a matching `known` entry. The app connects to it on next launch without scanning.

`session_gap_minutes` (default 60) is the idle span that ends a CSV session: a longer gap between readings starts a new file, a shorter one continues the current session. `min_bpm_threshold` (default 20) is the validity floor; readings below it are treated as no-contact noise and left out of the CSV. Both keys are optional and only needed to override the defaults.
