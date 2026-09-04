# Files & configuration

Where gotempo keeps its state, and how to edit the config by hand. Back to the
[README](../README.md); for the flags that read/write these, see
[Command line](CLI.md).

## File locations

gotempo uses standard XDG directories, created on first run:

- `~/.config/gotempo/config.json`: saved device, known-device history, and preferences. Managed by the app; edit it by hand as described below. Honors `$XDG_CONFIG_HOME`.
- `~/.local/share/gotempo/gotempo-bpm.txt`: current BPM as a raw integer, rewritten on each change. Empty when not logging (cleared the moment you stop). Keeps the last reading briefly across a short dropout, then clears after about ten seconds disconnected. Useful as an OBS text source. Honors `$XDG_DATA_HOME`.
- `~/.local/share/gotempo/sessions/*.csv`: per-session history, one `timestamp,bpm` row per reading. Written while logging is on. A new file starts after a gap longer than `session_gap_minutes`; shorter breaks append to the current file. Readings below `min_bpm_threshold` (sensor off / no contact) are skipped, so they show as gaps in the timestamps rather than junk rows. Files are named by the session's first reading.
- `<your ITGmania theme>/Modules/hr.txt`: one line, `<bpm> <YYYYMMDD> <secondsSinceLocalMidnight>`, rewritten on every reading. Only written when `itgmania_module` is set; the location follows that setting, not the XDG dirs. See [ITGmania overlay](#itgmania-overlay).
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
  "min_bpm_threshold": 20,
  "itgmania_module": ""
}
```

Set `current` to your device MAC and add a matching `known` entry. The app connects to it on next launch without scanning.

`session_gap_minutes` (default 60) is the idle span that ends a CSV session: a longer gap between readings starts a new file, a shorter one continues the current session. `min_bpm_threshold` (default 20) is the validity floor; readings below it are treated as no-contact noise and left out of the CSV. Both keys are optional and only needed to override the defaults.

## ITGmania overlay

gotempo can drive `gotempo.lua`, a Simply Love theme module that draws your heart rate on ITGmania's gameplay screen. The module is installed separately; gotempo's side is one config key.

Set `itgmania_module` to the full path of `gotempo.lua`, and gotempo writes `hr.txt` next to it:

```json
"itgmania_module": "/home/you/.itgmania/Themes/Simply Love/Modules/gotempo.lua"
```

`gotempo --itgmania-module <path>` sets the same key (see [Command line](CLI.md)). Empty means off, which is the default.

The module lives in ITGmania's per-user data folder, not the install directory:

| Platform | Location |
|---|---|
| Linux | `~/.itgmania/Themes/Simply Love/Modules/` |
| Windows | `%APPDATA%\ITGmania\Themes\Simply Love\Modules\` |
| macOS | `~/Library/Application Support/ITGmania/Themes/Simply Love/Modules/` |

**Point at the copy inside the theme you actually play.** The module looks for `hr.txt` in whichever theme ITGmania has selected, while gotempo writes beside the file you named. If you have `gotempo.lua` sitting in a second theme's `Modules/` folder and point gotempo at that one, everything looks correctly configured and nothing appears in game.

The path is checked on every launch, not just when you set it. If `gotempo.lua` has moved, gotempo logs `[ITG] module not found, overlay disabled` and runs without the overlay rather than writing into a dead path. It never creates the directory: a wrong path is an error, not a new folder. `gotempo --status` prints the resolved `hr.txt` when the overlay is on.

The file format is one line of `<bpm> <YYYYMMDD> <secondsSinceLocalMidnight>` in **local** time, e.g. `154 20260904 52327`. The module hides the panel when the date differs from its own or the time is more than 60 seconds behind it, so a file left over from a previous session never shows as a live reading. gotempo rewrites the file on every reading, including unchanged ones, since it is the timestamp rather than the BPM that keeps the panel up.

Unlike `gotempo-bpm.txt`, this file is **not** tied to the logging toggle: the in-game panel works whether or not you are recording a session. It is emptied when the strap disconnects or you switch devices, which hides the panel within a second.
