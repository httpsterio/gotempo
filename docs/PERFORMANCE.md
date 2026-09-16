# Performance

What gotempo and the ITGmania module cost the game, measured rather than estimated.

A 2.0.0 report described stuttering during gameplay, blamed on the module looking for a
`gotempo.ini` that did not exist. 2.1.0 caches that lookup. These measurements check both the
cost of the current version and whether the old behaviour can be reproduced.

## Method

Each run launches ITGmania, plays one song on autoplay with no input, and quits. Nothing is
pressed, so every run does the same work. Per run:

- Every frame's time is logged outside the game, with the overlay hidden.
- CPU, wakeups, memory and disk activity of the game, gotempo, `bluetoothd` and D-Bus are
  sampled once a second from `/proc`.
- Only the gameplay part of the song counts, with 2 seconds trimmed off each end.
- Thermal throttling is recorded; a throttled run is discarded.

Setups are run round-robin, five runs each, so drift spreads across all of them instead of
landing on whichever ran last. The spread between identical baseline runs is the noise floor.
A difference counts only if it beats that spread and a permutation test over the runs.

| Setup | Module | gotempo | Readings |
|---|---|---|---|
| baseline | not installed | not running | none |
| module | 2.1.0 | not running | none |
| gotempo | 2.1.0 | running | none |
| fakestrap | 2.1.0 | not running | written once a second, so the panel draws and the graph collects |
| oldfake | 2.0.0 | not running | same as fakestrap |

The module hides its panel and collects no samples unless readings are arriving, so a setup
with no readings only exercises its idle path. `fakestrap` writes `hr.txt` in gotempo's own
format, which the module cannot tell apart from a strap. `oldfake` is the same with 2.0.0 and a
profile that has no `gotempo.ini`, which is the reported stutter case.

## Results

ITGmania 1.3.0, Simply Love, Linux, i7-7700HQ with Intel HD 630, XFCE, frame rate uncapped.
Song: In The Groove / VerTex, Hard. Five runs per setup, medians.

| Setup | fps | Typical frame (p50) | Worst 1 in 1000 (p99.9) | Game CPU |
|---|---|---|---|---|
| baseline | 203.9 | 4.92 ms | 6.23 ms | 364 ms/s |
| module | 202.3 | 4.96 ms | 6.23 ms | 370 ms/s |
| gotempo | 202.5 | 4.95 ms | 6.20 ms | 370 ms/s |
| fakestrap | 201.8 | 4.97 ms | 6.12 ms | 368 ms/s |
| oldfake | 201.4 | 4.97 ms | 6.19 ms | 369 ms/s |

**The module costs about 1% of frame time.** Loading it costs 1.5 to 2.6 fps out of 204, 0.04 ms
on a typical frame, and 3 to 5 ms of CPU per second, which is 0.4% of one core. The three
measurements agree: 0.04 ms per frame at 200 fps is 8 ms per second.

**No stutter.** The tail never moves. p99 and p99.9 are the same as baseline in every setup, in
125 seconds of gameplay per run.

**Drawing the panel is free within measurement.** `fakestrap` draws the panel every frame and
collects graph samples; `module` does neither. No difference in frame times or frame rate.

**gotempo itself is negligible.** 0.51 ms of CPU per second with no strap, 15 MB resident.
`bluetoothd` and both D-Bus buses add 0.54 ms/s between them. With a strap configured but
switched off, gotempo retries the connection and uses 1.35 ms/s.

**The 2.0.0 stutter did not reproduce on Linux.** `oldfake` and `fakestrap` differ in nothing,
tail included, with the same missing `gotempo.ini` and the same live panel. The per-second ini
lookups cost nothing measurable here. That does not disprove the report: the cost of opening a
missing file is a property of the filesystem and of anything scanning it, so a Windows run is
still worth doing. It does mean the lookups alone are not enough to explain it.

### A song the machine cannot keep up with

Same method, song Notice Me Benpai 3 / Igaku, Challenge, frame rate capped at 60. This chart
ships 158 kB of its own Lua and the machine holds only 56 fps at baseline, so the game is
already missing frames before anything is added. `fakestrap10` writes `hr.txt` ten times a
second instead of once, as a headroom check on the file traffic.

| Setup | fps | Typical frame (p50) | Worst 1 in 1000 (p99.9) | Game CPU |
|---|---|---|---|---|
| baseline | 56.2 | 20.86 ms | 34.76 ms | 661 ms/s |
| fakestrap | 55.4 | 20.93 ms | 34.84 ms | 662 ms/s |
| fakestrap10 | 55.4 | 20.91 ms | 34.67 ms | 663 ms/s |
| oldfake | 55.3 | 20.88 ms | 34.63 ms | 662 ms/s |

Nothing here is significant: every setup sits 0.8 fps under baseline, at p=0.119, against a
baseline whose own p99.9 varies by 0.9 ms between runs. The tail does not move, so the module
does not make a struggling song worse. Ten writes a second are the same as one.

The cost is paid per frame, not per second. The module adds about 5 ms/s of CPU at 200 fps and
about 1 ms/s at 55 fps: the same work per frame, fewer frames. A slower machine pays less in
absolute terms, and roughly the same fraction of each frame.

### Two players

Same song and method, both sides joined, five runs each. `baseline2p` has no module;
`fakestrap2` draws a panel for each side off written readings; `twostraps` is the real thing,
gotempo connected to a strap per side.

| Setup | fps | Typical frame (p50) | Worst 1 in 1000 (p99.9) | Game CPU |
|---|---|---|---|---|
| baseline2p | 186.5 | 5.36 ms | 8.46 ms | 450 ms/s |
| fakestrap2 | 184.4 | 5.42 ms | 8.75 ms | 455 ms/s |
| twostraps | 184.4 | 5.41 ms | 8.75 ms | 453 ms/s |

The module costs the same with two panels as with one: 2.1 fps and 5.5 ms/s here against 1.7 fps
and 5.4 ms/s in single player. It builds actors for both sides whichever is joined, so the tree
it updates every frame is the same size either way.

Real straps add nothing the game can feel. `twostraps` and `fakestrap2` are the same on every
metric, and written readings are indistinguishable from a strap in single player too.

Two-player mode itself costs the game 17 fps and 85 ms/s, a second playfield to draw. That is
the game, not this.

### gotempo and Bluetooth

Measured outside the game's frame loop, five runs per condition.

| Condition | gotempo CPU | Wakeups | Memory | `bluetoothd` | D-Bus |
|---|---|---|---|---|---|
| no strap | 0.51 ms/s | 9/s | 14.2 MB | 0.16 ms/s | 0.17 ms/s |
| one strap connected | 4.01 ms/s | 70/s | 14.4 MB | 0.92 ms/s | 0.93 ms/s |
| two straps connected | 4.72 ms/s | 78/s | 14.7 MB | 0.93 ms/s | 0.96 ms/s |
| one strap feeding both slots | 4.41 ms/s | 78/s | 14.5 MB | 0.93 ms/s | 0.96 ms/s |

The first connection costs, the second barely: one strap adds 3.5 ms/s over idle, a second adds
0.7 more. Most of it is per-reading work rather than the radio link, which the shared-strap row
shows: one connection publishing to two slots costs nearly as much as two connections.
`bluetoothd` and D-Bus do not care how many straps there are. gotempo at its busiest is 0.5% of
one core.

## Caveats

- One machine, one song, one theme. Numbers are not portable; the method is.
- With five runs per setup the smallest reachable p-value is 0.008, so p=0.048 is modest
  evidence. The effect is believed because four independent setups agree, not because of any
  single test.
- Frame rate uncapped. With vsync on and frames to spare, none of this is visible at all.
- Most runs used written readings rather than a strap. That was checked against real straps and
  makes no difference in game.

## Reproducing

The harness lives outside this repository, in `~/gotempo-bench`: `setup.sh` puts the machine in
a setup, `run-once.sh` plays one song and collects everything, `batch.sh` runs setups
round-robin, and `compare.py` prints the tables above.

```
sudo -v
./batch.sh "baseline module fakestrap oldfake gotempo" 5
python3 compare.py --uncapped
```

## Still open

- Memory over an hour, for the game, gotempo and `bluetoothd`.
- Windows, where the original report came from.
