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

### Related code (main.go, line numbers approximate)
- **Scan-gate** (root of "not found in scan"): `connectOnce` requires `findDevice`
  to see the device advertising before connecting, else returns
  `device ... not found in scan` (~line 754, error at 761). A bonded/connected
  strap does not advertise, so this locks it out. A separate fix is to try a direct
  connect-by-address for a known device and fall back to scanning. `findDevice` is
  at ~384, `persistScan` ~769, `persistentConnect` ~797.
- **Swallowed error:** `persistentConnect` (~797) calls `connectAndMonitor` (~822)
  and discards its error on `errSessionDropped`/raw errors without logging, which
  is why `service discovery timed out` was invisible until a restart dropped into
  the finite phase (`connectLoop` ~694, which does log it). Add logging there.
- **Error mapping:** `describeConnectErr` maps `timeout on DiscoverServices` ->
  `service discovery timed out` (~240). Discovery call is `DiscoverServices` (~829);
  link connect is `adapter.Connect` (~824).
