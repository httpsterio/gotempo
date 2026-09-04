# Bugs

## Strap won't reconnect after a long idle

Status: open. Recovery is known (below). Root cause not confirmed.

### Symptom
After the HR strap has been disconnected/idle for roughly an hour, gotempo cannot
reconnect to it. Short breaks (~10 min) reconnect fine. The only thing that
reliably restores it is removing the device in blueman (which clears the BlueZ
bond), after which it connects again.

### Ruled out
- **Strap hardware / contact:** taking the sensor off the band and re-clipping it
  does not help.
- **gotempo state:** restarting the app does not help.
- **The phone:** the user's phone being connected to the strap or not makes no
  difference (an earlier guess that the phone held a connection slot was wrong).

Because only clearing the BlueZ bond fixes it, the bad state lives in **BlueZ**,
not in the strap or in gotempo.

### Setup (for reproduction / context)
- Device: Polar H10, MAC `24:AC:AC:18:41:CC`, static public address. Standard GATT
  HR profile: service `0x180D`, measurement char `0x2A37`.
- Stack: Linux + BlueZ, adapter `hci0`. blueman runs in the session and provides
  the Bluetooth pairing **agent** (so Just Works pairing completes with no popup).
- gotempo writes its log to **stderr**. Launched from the GUI session it lands in
  `~/.local/share/sddm/xorg-session.log` (path is environment-specific; the point
  is logs go to the session's stderr, not a dedicated file). Grep it for `[BLE]`.
- gotempo connects via the `tinygo.org/x/bluetooth` library over BlueZ DBus.

### Permissions (important for the fix)
`bluetoothctl remove`, `pair`, and `trust` on this device all worked **as the
normal user, without sudo**. BlueZ lets the active local session manage its own
devices via polkit. So an in-app fix that calls BlueZ over DBus does **not** need
root or any install-time elevation.

### Evidence observed (2026-06-11)
Progression seen in the log during the failure:
1. `connect failed: device 24:AC:AC:18:41:CC not found in scan` repeated ~177x.
   The strap was not surfaced in scans, so gotempo never attempted a connect.
   (gotempo only connects to a device it has just seen advertising; see scan-gate
   below.)
2. After an app restart it found and connected but logged
   `connect failed: service discovery timed out` every ~15s, with the BLE link
   flapping (connected ~6s, then dropped). `HR service not found` never appeared,
   so GATT discovery was erroring/timing out, not returning empty.
3. After `bluetoothctl remove`, the next connect hung inside the connect call for
   ~3 min with no log line (waiting on pairing).

### Working state after the repair (2026-06-11/12)
The day after the `remove`+`pair`+`trust` recovery, the strap reconnected cleanly
every time: 11 connects over ~10h, including after 2h+ gaps, all ~16s with no
`not found in scan` and no `service discovery timed out`. The only thing that
changed from the failing state was the device's BlueZ state:
- the `remove` wiped the bond and **GATT cache** (rebuilt fresh on re-pair);
- `Trusted` went from `no` to `yes` (enables BlueZ background auto-reconnect).
Same gotempo build, same strap, same phone. So the difference is entirely
BlueZ-side, consistent with the bad state living in BlueZ.

Caveat: this is **not** proof of a fix. The repair reset the clock — a fresh
cache behaving well is expected whether or not the bug is gone. Also, those long
gaps were the strap **off/cold** then put back on, not the failing condition
(bonded + powered + idle ~1h). The exact failing condition has not been
re-challenged since the repair.

### Repro attempts (what does NOT trigger it)
- **Test 1 — off-idle, 2026-06-12.** From the fresh trusted bond: strap worn and
  connected, then taken off (powers down), gotempo left running, strap off ~1.5h
  (01:24 -> 02:52), then put back on. Reconnected clean in ~17s, BPM streaming,
  **zero** `service discovery timed out`; the only failures during the idle
  window were benign `not found in scan` (strap off, nothing to find). So the
  trigger is **not** "strap off for ~1h". The faithful real-world off-then-on
  path is reliable on a fresh bond.
- **Test 2 — suspend, 2026-06-12.** Strap off, gotempo running, laptop in deep
  suspend 14:12:13 -> 15:25:31 (1h13m, confirmed in journal `PM: suspend
  entry/exit`), plus several shorter suspends the night before. On resume the
  strap reconnected ~28s later (15:25:59), zero `service discovery timed out`.
  So a suspend/resume cycle does **not** trigger it either.
- Still untested: idle while the strap stays **powered + bonded** (worn or pads
  bridged, gotempo not holding the link); long-term **bond age** (original
  failures built up over days; the repair reset the clock); **trusted vs
  untrusted** (original failures may have been while untrusted).

So far nothing deliberate reproduces it on the fresh trusted bond: off-idle 1.5h,
deep suspend 1h+, shorter suspends, and ~2 days of normal use are all clean. The
strongest remaining hypothesis is slow **bond/cache aging** (or the trusted
flag), neither of which can be forced quickly. Practical path: keep using it
normally and capture the log the moment it fails again (before removing the
device) — which makes the logging improvement (PLAN.md) the priority, so the next
organic failure is self-diagnosing.

### Likely cause (NOT confirmed)
Stale BlueZ **GATT cache** for the bonded device. BlueZ caches the device's
attribute table; when it goes stale after a long disconnect, service discovery
times out, and removing the device wipes the cache. This matches both the
`service discovery timed out` symptom and "only removing the device fixes it".
Other possibilities not excluded: a stuck BlueZ background auto-reconnect for the
trusted device.

### Recovery that works (manual)
With the phone Bluetooth off (not required, but removes a variable):
```sh
pkill -x gotempo
bluetoothctl remove 24:AC:AC:18:41:CC               # drop the stale bond + cache
bluetoothctl --timeout 30 pair 24:AC:AC:18:41:CC    # Just Works, no popup
bluetoothctl trust 24:AC:AC:18:41:CC
# start gotempo -> connected in ~1s, BPM streaming
```
Pairing is Just Works (no passkey). This run set `Trusted: yes` (was `no` before),
which enables BlueZ background auto-reconnect; effect on the long-idle issue is
untested.

### To confirm the cause next time it is stuck (before removing anything)
- `sudo btmon` (plus `dmesg -w`) through a couple of failed attempts for the
  HCI-level reason.
- Inspect the BlueZ store for the device: `/var/lib/bluetooth/<adapter>/24:AC:AC:18:41:CC/`
  and any GATT cache under `/var/lib/bluetooth/<adapter>/cache/24:AC:AC:18:41:CC`.
- Cheap test of the cache theory: delete only the cached GATT db (not the bond)
  and `sudo systemctl restart bluetooth`. If that alone fixes it without a re-pair,
  the cause is the stale cache.

### Reproducing it on purpose (not yet confirmed to trigger)
The failing condition is the strap **powered + bonded + idle** for ~1h, not a
cold strap that was off. The H10 powers off when taken off (it needs skin
contact), so to age it while idle without wearing it, bridge the two electrode
pads with a damp cloth and a pinch of salt; that keeps it powered on the desk.

Controlled aging test:
1. Start from a fresh bond (post-`remove`+`pair` state).
2. Keep the strap powered but idle: bridge the pads (or wear it), let gotempo
   connect, then **quit gotempo** so nothing holds the link. The strap now sits
   bonded to BlueZ with no active connection.
3. Leave it 1-2h powered + bonded + idle.
4. Restart gotempo and watch the log. `service discovery timed out` / flapping
   confirms the trigger is time-idle-while-bonded.

Isolate one variable at a time across runs:
- **Suspend:** repeat but `systemctl suspend` for the idle window, resume,
  restart gotempo. If it only breaks with a suspend in the window, the trigger is
  resume-time controller/cache corruption, not wall-clock idle. Top suspect.
  (Check correlation with `journalctl -g suspend`.)
- **Trusted vs untrusted:** `bluetoothctl untrust` for one run. If untrusted
  reproduces and trusted does not, BlueZ background auto-reconnect (trusted only)
  is keeping the cache warm.
- **Phone present/absent:** believed irrelevant; one run to confirm.

Do the logging improvement (PLAN.md) first. The persistent phase is silent for
~50min stretches now, so a failure leaves no trace of what it tried. Per-round
logging plus the surfaced connect/discovery error makes the next failure
self-explanatory.

### Fix direction

Do NOT ship a system-config change. The targeted root-cause tweak is
`[GATT] Cache = no` in `/etc/bluetooth/main.conf` + `systemctl restart bluetooth`,
but that needs root, rewrites global Bluetooth config, is distro-fragile, and can
be reverted by updates. Keep it only as an optional note for power users. gotempo
installs without sudo and should stay that way.

**Preferred: in-app self-heal over DBus (no root).** When gotempo detects the
stuck state (repeated `service discovery timed out` on the configured device), it
should call `org.bluez` `Adapter1.RemoveDevice` for that device over DBus, then
reconnect. The re-pair is Just Works and headless via blueman's agent (proven
today: the manual remove+pair needed no sudo and no popup). This automates the
manual recovery unattended.

Implementation notes:
- `tinygo.org/x/bluetooth` likely does not expose `RemoveDevice`; call BlueZ via
  `github.com/godbus/dbus/v5` directly. It is already a transitive dependency
  (in `go.sum`); add it to `go.mod` as a direct require. Object path is
  `/org/bluez/hciN/dev_24_AC_AC_18_41_CC`; method
  `org.bluez.Adapter1.RemoveDevice(objectpath)` on `/org/bluez/hciN`.
- Gate it behind repeated-failure detection (e.g. N consecutive discovery
  timeouts), not eager use. Consider a config toggle.
- Caveat: headless re-pair needs a pairing agent present (blueman/GNOME provide
  one). On a bare setup with no agent it may prompt or fail.

**Rejected: clearing the GATT cache on a timer.** Considered and dropped:
- The clean clears need root. The cache file
  (`/var/lib/bluetooth/<adapter>/cache/<dev>`, mode 0700) and `main.conf`'s
  `[GATT] Cache = no` are both root-only. gotempo runs as the user, so neither is
  available without elevation we agreed not to ship.
- The no-root path is `RemoveDevice` over DBus, but that drops the **bond**, not
  just the cache, forcing a re-pair. On a timer that means unpairing/re-pairing a
  working device every cycle: a disconnect window each time, re-pair latency, and
  outright failure if no agent is up at that moment.
- BlueZ exposes **no** DBus method to invalidate the cache and rediscover on a
  live device, so there is no cheap per-device refresh to call periodically.
So detection-triggered self-heal (above) is strictly better than timer clearing:
it acts only when actually stuck, with no needless re-pairs.

**Lighter preventive to try first (no root, no re-pair):** on idle/drop, have
gotempo issue a clean `Device1.Disconnect` over DBus so BlueZ tears the link down
in a known state instead of letting it rot into a discovery timeout. Unproven but
low-risk; worth trying before the heavier self-heal.

Suggested sequence: logging first -> run the aging test to confirm the trigger
-> clean-Disconnect-on-idle -> detection-triggered self-heal if still needed.

### Related code (internal/app/ble.go)
- **Scan-gate removed (fixed).** Reconnection used to require a scan hit before
  connecting (`connectOnce`/`persistScan` via `findDevice`), which returned
  `device ... not found in scan` for a bonded strap that does not advertise. Both
  `findDevice` and `persistScan` are gone; `connectOnce` and `persistentConnect`
  now connect directly by address (`connectDevice` wraps `adapter.Connect`). This
  removes the scan-gate lockout *and* the continuous active scanning that probed
  neighbors. Note: there is no scan fallback, so the device must be known to
  BlueZ (bonded / picked once). If a stale/empty BlueZ cache is ever the cause,
  direct connect will fail until a Rescan re-populates it.
- **Swallowed error (fixed).** `persistentConnect` used to discard the error from
  `connectAndMonitor` on `errSessionDropped`/raw errors, which is why
  `service discovery timed out` was invisible until a restart dropped into the
  finite phase (`connectLoop` logs it). Both phases now log via
  `describeConnectErr`.
- **Error mapping:** `describeConnectErr` maps `timeout on DiscoverServices` ->
  `service discovery timed out`. Discovery call is `DiscoverServices` in
  `connectAndMonitor`; link connect is `adapter.Connect` in `connectDevice`.
