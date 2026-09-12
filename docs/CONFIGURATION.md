# Files & configuration

Where gotempo keeps its state, and how to edit the config by hand. Back to the
[README](../README.md); for the flags that read/write these, see
[Command line](CLI.md).

## File locations

On Linux, gotempo uses standard XDG directories, created on first run:

- `~/.config/gotempo/config.json`: saved device, known-device history, and preferences. Managed by the app; edit it by hand as described below. Honors `$XDG_CONFIG_HOME`.
- `~/.local/share/gotempo/gotempo-bpm.txt`: current BPM as a raw integer, rewritten on each change. Empty when not logging (cleared the moment you stop). Keeps the last reading briefly across a short dropout, then clears after about ten seconds disconnected. Useful as an OBS text source. Honors `$XDG_DATA_HOME`.
- `~/.local/share/gotempo/sessions/*.csv`: per-session history, one `timestamp,bpm` row per reading. Written while logging is on. A new file starts after a gap longer than `session_gap_minutes`; shorter breaks append to the current file. Readings below `min_bpm_threshold` (sensor off / no contact) are skipped, so they show as gaps in the timestamps rather than junk rows. Files are named by the session's first reading.
- `<your ITGmania theme>/Modules/gotempo/hr.txt`: one line, `<bpm> <YYYYMMDD> <secondsSinceLocalMidnight>`, rewritten on every reading. Only written when `itgmania_module` is set; the location follows that setting, not the XDG dirs. See [ITGmania overlay](#itgmania-overlay).
- `~/.local/share/gotempo/status.json`: live app state published by the running app, independent of logging — connection, phase, logging flag, current BPM, and device. With a second strap in use it also carries a `player2` object. Read by `gotempo --status` (see [Command line](CLI.md)). Honors `$XDG_DATA_HOME`.
- `internal/app/assets/` (source tree only): tray status icons and `logo.png`, embedded in the binary at build time.

With [two straps](#two-straps) the second one writes `gotempo-bpm-p2.txt` and `hr-p2.txt` beside the first's files, and session CSVs gain `-p1`/`-p2` in their names. The first strap's paths never change.

### Windows

Windows has no config/data split, so everything lives in one folder,
`%LOCALAPPDATA%\gotempo` (typically `C:\Users\<you>\AppData\Local\gotempo`):
`config.json`, `gotempo-bpm.txt`, `sessions\*.csv` and `status.json`. Local, not
Roaming, so logs do not follow a roaming profile between machines.

The tray's **Open log folder** and **Open config folder** therefore open the same
directory on Windows. That is intended.

## config.json

`config.json` is created on first run with all keys at their defaults, and updated automatically after that. You can edit it by hand, which is handy for headless setups where you set the device without the tray. On launch each value is validated; a missing, malformed, or out-of-range entry is reset to its default and the file is rewritten, so it never holds a value the app silently ignores:

```json
{
  "current": "24:AC:AC:18:41:CC",
  "current_p2": "",
  "two_player": false,
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
  "strap_hold_minutes": 20,
  "strap_lost_minutes": 5,
  "itgmania_module": ""
}
```

Set `current` to your device MAC and add a matching `known` entry. The app connects to it on next launch without scanning.

`session_gap_minutes` (default 60) is the idle span that ends a CSV session: a longer gap between readings starts a new file, a shorter one continues the current session. `min_bpm_threshold` (default 20) is the validity floor; readings below it are treated as no-contact noise and left out of the CSV. Both keys are optional and only needed to override the defaults.

`strap_hold_minutes` (default 20) and `strap_lost_minutes` (default 5) control how long a strap keeps its connection after nothing is following it any more, which only happens when a game profile hands the slot back. A connection is kept so that rejoining is instant instead of costing a reconnect. The first budget applies while the strap is still sending readings, so it is worn and the player is simply between songs; the second applies once it has gone quiet, which is what taking a belt off looks like about a minute later. Whichever runs out first releases the strap. A strap a slot is actually following is never released by either.

## Two straps

gotempo can follow two heart-rate straps at once, for recording two people together or for a two-player ITGmania cabinet. It is off by default and costs nothing when unused.

`two_player` turns it on; `current_p2` is the second strap's MAC, in the same form as `current` and with a matching `known` entry. They are separate keys so that switching the mode off keeps the assignment: an operator can set a cabinet up once and toggle it without re-picking a strap.

```json
"current": "24:AC:AC:18:41:CC",
"current_p2": "11:22:33:44:55:66",
"two_player": true
```

`current_p2` is ignored while `two_player` is false, so that slot simply idles.

The second strap gets its own copy of every output. The first strap's filenames never change, so an OBS source or an installed theme module keeps working when you switch the mode on:

| | First strap | Second strap |
|---|---|---|
| OBS text source | `gotempo-bpm.txt` | `gotempo-bpm-p2.txt` |
| ITGmania overlay | `gotempo/hr.txt` | `gotempo/hr-p2.txt` |
| `status.json` | top-level fields | `player2` object |

Session CSVs are the exception, because a person reads those filenames rather than a program: with one strap they stay unsuffixed, and with two they become `2026-09-08T14-30-00-p1.csv` and `…-p2.csv`. Switching the mode therefore starts a new file rather than continuing the last one.

Session logging is one process-wide toggle: it is on or off for both straps together.

From the tray, tick **Two-player mode** and then click a device to cycle it through P1, P2 and unassigned; the row label shows `[P1]`/`[P2]`. From the command line, see [Two straps](CLI.md#two-straps).

## Straps from ITGmania profiles

A player can name their own strap in their ITGmania profile, and gotempo follows it while they are playing. This is for cabinets: picking a strap is otherwise a job someone does at the PC, which nobody does mid-session.

Add `gotempo.ini` to the profile directory (alongside the `GrooveStats.ini` and `ArrowCloud.ini` other modules use):

```ini
[gotempo]
Device=24:AC:AC:18:41:CC
```

You do not have to write that by hand. With the module installed, the player picks their
strap in game: **sort menu → Advanced → gotempo**, which lists the straps in range and
writes the choice into their own profile. `gotempo --list-devices` still prints MAC and
name if you would rather set it up from a terminal.

Nothing else is needed. The module publishes `players.txt` in its own folder beside `gotempo.lua` once a second while the game is on a song-select, gameplay or evaluation screen; gotempo reads it and moves the straps. The strap must be paired to the machine once beforehand, the same as any strap gotempo connects to.

| The player | gotempo follows |
|---|---|
| is not playing | nothing on that side |
| is playing, named a strap | that strap, and shows readings only from it |
| is playing, named nothing | the configured strap, exactly as before |

A player who named a strap gets that strap or nothing. gotempo will not fall back to the machine's configured strap for them, because that strap is on somebody else and drawing its readings as theirs would be wrong in a way nobody would notice.

Two players may name the same strap. That is a choice rather than a mistake -- they are swapping sides, or one has stopped playing and lent the belt out -- so gotempo connects it once and feeds both sides from the one connection. What it still refuses is handing one *configured* strap to two slots nobody asked for.

The assignment is never written to `config.json`. It lives in memory, so quitting gotempo, or ITGmania exiting or crashing, returns every slot to whatever the tray is set to. That also means a visiting player's strap never joins the machine's device list.

Two more things stop while a profile is driving a slot, for the same reason:

- **No CSV session file.** Those readings belong to whoever walked up to the cabinet, and a workout log per visitor is not what the folder is for. The game keeps its own record of the session. `gotempo-bpm.txt` is unaffected and still updates.
- **No loss or reconnect notifications**, so the desktop is not filled with news about people who have left.

The tray reflects it too: an **ITGmania is choosing straps** line appears above the device list, and the logging toggle, **Autostart HR log**, **Two-player mode** and every device row grey out. All four would write to `config.json` and then change nothing, because the profile keeps overriding it.

`players.txt` carries the module's own clock, and gotempo releases the straps when that stamp stops advancing. This is what covers the game being killed rather than closed.

The in-game picker rides the same file: it adds a `scan <token>` line, and gotempo answers by scanning and writing `devices.txt` beside `hr.txt`. That list is blanked about a minute later, and again when gotempo starts, so a folder full of other people's straps does not sit there for the rest of the day.

## ITGmania overlay

gotempo can drive `gotempo.lua`, a Simply Love theme module that draws your heart rate on ITGmania's gameplay screen. The module is installed separately; gotempo's side is one config key.

The module is [gotempo-sl-module](https://github.com/httpsterio/gotempo-sl-module). Set `itgmania_module` to the full path of its `gotempo.lua`, and gotempo writes `hr.txt` next to it:

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
