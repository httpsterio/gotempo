# Performance

Measured cost of gotempo and gotempo-sl-module (the module) to ITGmania. Results are from one Linux
machine: ITGmania 1.3.0, Simply Love, i7-7700HQ with Intel HD Graphics 630, XFCE.

Every figure in this document is computed from the benchmark's result files by `report.py`.

## Summary

Current versions: gotempo 2.1.0, module 2.1.1. Single player, VerTex Hard, frame rate uncapped.
`fakestrap` runs the module with simulated readings, so its panel is drawn and its graph records.

| Setup | Runs | fps | Change | p50 ms | Change | p99.9 ms | Change | Game CPU ms/s | Change |
|---|---|---|---|---|---|---|---|---|---|
| baseline | 5 | 204.11 | reference | 4.916 | reference | 6.141 | reference | 363.7 | reference |
| fakestrap | 5 | 202.70 | -1.41 (-0.69%) | 4.946 | +0.030 (+0.61%) | 6.158 | +0.017 (+0.28%) | 365.9 | +2.27 (+0.63%) |

- No stutter was measured. No setup changed p99.9 frame time significantly on local disk.
- At 120 Hz, 0.020% of frames missed a refresh with the module and 0.020% without it. At 144 Hz it
  was 0.036% with the module and 0.035% without.
- On a song the machine cannot hold at 60 fps, no difference was measurable.
- gotempo uses 0.51 ms of CPU per second with no strap and 4.72 with two connected. It stays between
  14.2 and 14.7 MB and never runs on the game's frame thread.
- The module added no memory growth over an hour.
- The 2.0.0 stutter reproduces with the profile on NFS when the client does not cache missing files.
  1.044% of frames missed a 144 Hz refresh with 2.0.0 and 0.040% with 2.1.1. With profiles on local
  disk or a default NFS mount, the two versions measured the same.

## Method

Each run launches ITGmania, plays one song to the end on autoplay with no input, and quits.

- MangoHud logs every frame's time from outside the game, with its overlay hidden.
- A sampler reads CPU, wakeups, memory and disk activity for the game, gotempo, `bluetoothd` and
  D-Bus from `/proc` once a second.
- Only gameplay counts. 2 seconds are trimmed off each end of the song.
- Runs with thermal throttling are discarded. Runs found throttled: 0.

Setups run round-robin so drift spreads across all of them. Figures are medians across runs. A
difference is reported as real when a permutation test over the runs gives p < 0.05 and it is larger
than the spread between runs of the same setup. Where the text says two setups do not differ, no
metric meets that rule.

Simulated readings: several setups write `hr.txt` once a second in gotempo's own format instead of
running gotempo. The module then draws its panel and records the graph without a strap. The module
treats these the same as a strap, checked below.

The gotempo menu is the strap and appearance menu opened from ITGmania's sort menu.

## Runs

115 runs. Not counted: setup testing before these, 2 runs cancelled before their song finished, and
2 runs made under strace to count file calls.

| Song | Frame rate | Setup | Players | Module | gotempo | Readings | Profile | Runs |
|---|---|---|---|---|---|---|---|---|
| VerTex, Hard | uncapped | baseline | 1 | none | off | none | local | 10 |
| VerTex, Hard | uncapped | module | 1 | 2.1.0 | off | none | local | 5 |
| VerTex, Hard | uncapped | gotempo | 1 | 2.1.0 | on | none | local | 5 |
| VerTex, Hard | uncapped | fakestrap | 1 | 2.1.0 | off | simulated | local | 10 |
| VerTex, Hard | uncapped | fakestrap | 1 | 2.1.1 | off | simulated | local | 5 |
| VerTex, Hard | uncapped | nopicker | 1 | 2.1.0 without the gotempo menu | off | simulated | local | 5 |
| VerTex, Hard | uncapped | nopicker | 1 | 2.1.1 without the gotempo menu | off | simulated | local | 5 |
| VerTex, Hard | uncapped | oldfake | 1 | 2.0.0 | off | simulated | local | 5 |
| VerTex, Hard | uncapped | strap | 1 | 2.1.0 | on | H10 | local | 5 |
| VerTex, Hard | uncapped | baseline2p | 2 | none | off | none | local | 5 |
| VerTex, Hard | uncapped | fakestrap2 | 2 | 2.1.0 | off | simulated, both players | local | 5 |
| VerTex, Hard | uncapped | twostraps | 2 | 2.1.0 | on | H10 and a Polar watch | local | 5 |
| VerTex, Hard | uncapped | twoplayers | 2 | 2.1.0 | on | one H10 for both players | local | 3 |
| Eurobeat Is Fantastic, 60 minutes | uncapped | baseline | 1 | none | off | none | local | 1 |
| Eurobeat Is Fantastic, 60 minutes | uncapped | fakestrap | 1 | 2.1.0 | off | simulated | local | 1 |
| Igaku, Challenge | capped 60 | baseline | 1 | none | off | none | local | 5 |
| Igaku, Challenge | capped 60 | fakestrap | 1 | 2.1.0 | off | simulated | local | 5 |
| Igaku, Challenge | capped 60 | oldfake | 1 | 2.0.0 | off | simulated | local | 5 |
| Igaku, Challenge | capped 60 | fakestrap10 | 1 | 2.1.0 | off | simulated, 10 per second | local | 5 |
| VerTex, Hard | uncapped | fakestrap | 1 | 2.1.1 | off | simulated | NFS, default mount | 5 |
| VerTex, Hard | uncapped | fakestrap | 1 | 2.1.1 | off | simulated | NFS, misses not cached | 5 |
| VerTex, Hard | uncapped | oldfake | 1 | 2.0.0 | off | simulated | NFS, default mount | 5 |
| VerTex, Hard | uncapped | oldfake | 1 | 2.0.0 | off | simulated | NFS, misses not cached | 5 |

The `oldfake` setups load a profile with no `gotempo.ini`, as in the 2.0.0 report. Songs were on a
local SSD in every run. Profiles were too, apart from the NFS runs. `twoplayers` used real profiles
instead of the empty bench profiles, so it is left out of the frame time comparisons. Its gotempo
figures are in the Bluetooth table.

## Results

Each table compares against its first row, measured in the same session unless noted.

### Single player, uncapped

VerTex Hard, module 2.1.0 before the gotempo menu was changed.

| Setup | Runs | fps | Change | p50 ms | Change | p99.9 ms | Change | Game CPU ms/s | Change |
|---|---|---|---|---|---|---|---|---|---|
| baseline | 5 | 203.94 | reference | 4.919 | reference | 6.229 | reference | 364.3 | reference |
| module | 5 | 202.28 | -1.66 (-0.81%) | 4.956 | +0.037 (+0.76%) | 6.228 | -0.001 (-0.01%) | 369.7 | +5.40 (+1.48%) |
| gotempo | 5 | 202.47 | -1.46 (-0.72%) | 4.952 | +0.033 (+0.67%) | 6.196 | -0.033 (-0.53%) | 369.7 | +5.34 (+1.47%) |
| fakestrap | 5 | 201.75 | -2.18 (-1.07%) | 4.965 | +0.046 (+0.94%) | 6.122 | -0.107 (-1.72%) | 367.6 | +3.32 (+0.91%) |
| oldfake | 5 | 201.38 | -2.55 (-1.25%) | 4.970 | +0.051 (+1.04%) | 6.193 | -0.036 (-0.57%) | 368.6 | +4.27 (+1.17%) |

Loading the module costs 1.46 to 2.55 fps and 3.32 to 5.40 ms of game CPU per second. Each fps cost
has p of at most 0.048. p99.9 is unchanged in every setup. Drawing the panel and recording the graph
(`fakestrap`) cost no more than loading the module (`module`). fps and p50 do not differ between the
two. `fakestrap` measured lower on p99.9 (6.122 ms against 6.228) and on game CPU (367.6 ms/s
against 369.7), both at p = 0.048. More drawing does not lower either, so with five runs these two
differences are noise. Running gotempo alongside (`gotempo`) made no difference inside the game.

`oldfake` against `fakestrap` compares module 2.0.0 with 2.1.0 under identical conditions, with
profiles on local disk. No metric differs.

### Missed refreshes at arcade refresh rates

Arcade displays usually run at 120 Hz or more. The table gives the share of frames slower than one
refresh at each rate, from uncapped VerTex runs. A cabinet paced to its refresh rate schedules
frames differently, so these are measured frame times held against each budget.

| Session | Setup | Runs | 120 Hz (8.33 ms) | 144 Hz (6.94 ms) | 165 Hz (6.06 ms) | 240 Hz (4.17 ms) |
|---|---|---|---|---|---|---|
| module 2.1.1 | baseline | 5 | 0.020% | 0.035% | 0.130% | 99.619% |
| module 2.1.1 | fakestrap | 5 | 0.020% | 0.036% | 0.150% | 99.741% |
| module 2.1.1 | nopicker | 5 | 0.012% | 0.044% | 0.186% | 99.731% |
| module 2.1.0 | baseline | 5 | 0.020% | 0.039% | 0.137% | 99.628% |
| module 2.1.0 | module | 5 | 0.016% | 0.048% | 0.155% | 99.750% |
| module 2.1.0 | fakestrap | 5 | 0.020% | 0.040% | 0.139% | 99.821% |
| module 2.1.0 | oldfake | 5 | 0.024% | 0.040% | 0.179% | 99.765% |

At 120 and 144 Hz the module does not change how often a refresh is missed, and the runs of every
setup overlap with the baseline's. At 165 Hz the medians in the 2.1.1 session differ by up to 0.056
percentage points, and the runs still overlap. Baseline runs ranged from 0.098% to 0.142% and the
module's from 0.119% to 0.166%. This machine cannot run VerTex at 240 Hz with or without the module,
since its typical frame of 4.92 ms is longer than the 4.17 ms budget. The module's 0.030 ms per
frame is 0.36% of a 120 Hz frame, 0.43% of a 144 Hz frame and 0.72% of a 240 Hz frame.

### Profiles on network storage

The 2.0.0 stutter was reported on a machine with songs and profiles on a NAS. These runs put the
`bench-plain` profile on an NFS share served by a second Linux machine. This laptop reached it over
Wi-Fi. Round trips during the second batch took 3.05 ms at the median, 9.11 ms at p99 and 30.20 ms
at worst. Songs stayed local.

On local disk, strace shows 2.0.0 opening the missing `gotempo.ini` 4.00 times per second during a
song, every call from the game's main thread. 2.1.1 opened it 0 times. On a network mount each of
those opens can wait for the server.

The NFS client caches a missing file by default, so two mounts were measured. The default mount, and
one with `lookupcache=positive`, where a missing file is looked up on the server every time.

Default mount:

| Setup | Runs | fps | Change | p50 ms | Change | p99.9 ms | Change | Game CPU ms/s | Change |
|---|---|---|---|---|---|---|---|---|---|
| fakestrap | 5 | 202.42 | reference | 4.953 | reference | 6.118 | reference | 369.7 | reference |
| oldfake | 5 | 201.48 | -0.94 (-0.46%) | 4.971 | +0.017 (+0.35%) | 6.210 | +0.092 (+1.51%) | 370.7 | +0.98 (+0.27%) |

`lookupcache=positive`:

| Setup | Runs | fps | Change | p50 ms | Change | p99.9 ms | Change | Game CPU ms/s | Change |
|---|---|---|---|---|---|---|---|---|---|
| fakestrap | 5 | 202.64 | reference | 4.948 | reference | 6.147 | reference | 367.0 | reference |
| oldfake | 5 | 200.41 | -2.23 (-1.10%) | 4.966 | +0.017 (+0.35%) | 9.632 | +3.485 (+56.70%) | 369.2 | +2.22 (+0.60%) |

| `lookupcache=positive` | fakestrap (2.1.1) | oldfake (2.0.0) |
|---|---|---|
| NFS open requests per run | 4 | 334 |
| Slowest frame | 12.342 ms | 23.999 ms |
| Frames missing a 120 Hz refresh | 0.020% | 0.641% |
| Frames missing a 144 Hz refresh | 0.040% | 1.044% |
| Frames missing a 165 Hz refresh | 0.135% | 1.175% |
| Frames over 10 ms, per minute | 1.4 | 5.8 |

On the default mount the versions measured the same. The client caches a missing file, so most of
2.0.0's lookups need not reach the server. Request counters were not recorded for that batch.

With misses not cached, every 2.0.0 run sent the server 334 open requests. p99.9 rose by 3.49 ms (p
= 0.048), the slowest frame was 1.94 times as long, and 1.044% of frames missed a 144 Hz refresh.
2.0.0's p99.9 ranged from 9.44 to 9.80 ms across its runs and 2.1.1's from 6.05 to 6.22 ms. 2.1.1
sent 4 open requests per run. Its p99.9 was 6.147 ms on the share and 6.158 ms on local disk.

### Capped at 60 fps

Notice Me Benpai 3 / Igaku, Challenge. The song ships 158 kB of its own Lua, and the machine holds
56.16 fps without the module. That is below 60, so the result is the same on a 60 Hz or a 144 Hz
display. `fakestrap10` writes readings ten times a second.

| Setup | Runs | fps | Change | p50 ms | Change | p99.9 ms | Change | Game CPU ms/s | Change |
|---|---|---|---|---|---|---|---|---|---|
| baseline | 5 | 56.16 | reference | 20.858 | reference | 34.764 | reference | 661.0 | reference |
| fakestrap | 5 | 55.38 | -0.78 (-1.39%) | 20.926 | +0.069 (+0.33%) | 34.844 | +0.080 (+0.23%) | 662.1 | +1.02 (+0.15%) |
| fakestrap10 | 5 | 55.38 | -0.78 (-1.39%) | 20.912 | +0.054 (+0.26%) | 34.666 | -0.097 (-0.28%) | 662.8 | +1.79 (+0.27%) |
| oldfake | 5 | 55.32 | -0.84 (-1.50%) | 20.881 | +0.024 (+0.11%) | 34.632 | -0.132 (-0.38%) | 661.7 | +0.62 (+0.09%) |

No difference is real by the rule in Method. `fakestrap` adds 1.02 ms of CPU per second here and
3.32 on VerTex at 204 fps. The module's cost is paid per frame, and this song runs fewer frames.

### Two players

VerTex Hard, both players joined, empty bench profiles. `twostraps` was measured earlier the same
day.

| Setup | Runs | fps | Change | p50 ms | Change | p99.9 ms | Change | Game CPU ms/s | Change |
|---|---|---|---|---|---|---|---|---|---|
| baseline2p | 5 | 186.51 | reference | 5.355 | reference | 8.456 | reference | 449.5 | reference |
| fakestrap2 | 5 | 184.40 | -2.11 (-1.13%) | 5.418 | +0.062 (+1.16%) | 8.748 | +0.291 (+3.45%) | 455.0 | +5.54 (+1.23%) |
| twostraps | 5 | 184.41 | -2.11 (-1.13%) | 5.412 | +0.056 (+1.05%) | 8.754 | +0.297 (+3.52%) | 453.4 | +3.91 (+0.87%) |

The module costs 2.11 fps and 5.54 ms/s with two panels (p = 0.048), close to its single player
cost. Real straps (`twostraps`) and simulated readings (`fakestrap2`) measure the same. Joining a
second player costs the game itself 17.42 fps and 85.2 ms/s, against the single player baseline from
another session.

### Simulated readings against a real strap

VerTex Hard, single player. `strap` ran with gotempo connected to an H10, in a later session.

| Setup | Runs | fps | Change | p50 ms | Change | p99.9 ms | Change | Game CPU ms/s | Change |
|---|---|---|---|---|---|---|---|---|---|
| fakestrap | 5 | 201.75 | reference | 4.965 | reference | 6.122 | reference | 367.6 | reference |
| strap | 5 | 201.27 | -0.48 (-0.24%) | 4.973 | +0.008 (+0.16%) | 6.211 | +0.089 (+1.46%) | 369.4 | +1.74 (+0.47%) |

No metric differs.

### gotempo and Bluetooth

CPU, wakeups and memory of the processes outside the game, during the same runs.

| Condition | Runs | gotempo CPU ms/s | Change | Wakeups/s | Memory MB | `bluetoothd` ms/s | System D-Bus ms/s |
|---|---|---|---|---|---|---|---|
| no strap | 5 | 0.51 | reference | 9.0 | 14.2 | 0.16 | 0.17 |
| one strap | 5 | 4.01 | +3.50 | 69.7 | 14.4 | 0.92 | 0.93 |
| two straps | 5 | 4.72 | +4.21 | 78.0 | 14.7 | 0.93 | 0.96 |
| one strap feeding both players | 3 | 4.41 | +3.90 | 77.9 | 14.5 | 0.93 | 0.96 |

The first strap adds 3.50 ms/s. The second adds 0.71 more. One strap feeding both players costs 4.41
ms/s, close to two straps, so most of the cost comes from handling readings. `bluetoothd` used 0.92
ms/s with one strap and 0.93 with two, and the system D-Bus 0.93 and 0.96.

### One hour

Eurobeat Is Fantastic, a 60 minute chart, one run each.

| Setup | Runs | fps | Change | p50 ms | Change | p99.9 ms | Change | Game CPU ms/s | Change |
|---|---|---|---|---|---|---|---|---|---|
| baseline | 1 | 207.93 | reference | 4.822 | reference | 6.099 | reference | 349.3 | reference |
| fakestrap | 1 | 204.76 | -3.17 (-1.52%) | 4.905 | +0.082 (+1.71%) | 6.152 | +0.053 (+0.87%) | 355.9 | +6.63 (+1.90%) |

| | baseline | fakestrap |
|---|---|---|
| Slowest frame | 28.789 ms | 26.074 ms |
| Package power | 16.58 W | 16.61 W |
| Game memory after 2 minutes | 399.4 MB | 399.9 MB |
| Game memory at the end | 464.8 MB | 464.6 MB |
| Game memory growth, fitted | 59.5 MB/h | 58.9 MB/h |
| Growth, first half | 79.3 MB/h | 78.9 MB/h |
| Growth, second half | 51.3 MB/h | 50.6 MB/h |

The game's memory grows at the same rate with and without the module. `bluetoothd` and the reading
writer stayed flat. gotempo was not running in these runs.

### Actor cost

The module's actors stay in ITGmania's system layer for the whole session. A hidden actor is still
updated every frame. Counted from the module's source, the gotempo menu is 211 actors: 2 panels of
104 (16 for the frame, header, status and footer, and 8 rows of 11) and 3 for its clock and input
guard.

Module 2.1.0 against a build with the gotempo menu removed, same session:

| Setup | Runs | fps | Change | p50 ms | Change | p99.9 ms | Change | Game CPU ms/s | Change |
|---|---|---|---|---|---|---|---|---|---|
| fakestrap | 5 | 201.30 | reference | 4.976 | reference | 6.210 | reference | 369.8 | reference |
| nopicker | 5 | 202.16 | +0.86 (+0.42%) | 4.958 | -0.018 (-0.36%) | 6.096 | -0.113 (-1.82%) | 367.0 | -2.87 (-0.78%) |

The gotempo menu cost 0.86 fps and 2.87 ms/s, 68 ns per actor per frame.

Module 2.1.1 hibernates the gotempo menu one second after the song wheel closes and wakes it when
the wheel returns. Hibernated actors are not updated. Measured with its own baseline:

| Setup | Runs | fps | Change | p50 ms | Change | p99.9 ms | Change | Game CPU ms/s | Change |
|---|---|---|---|---|---|---|---|---|---|
| baseline | 5 | 204.11 | reference | 4.916 | reference | 6.141 | reference | 363.7 | reference |
| fakestrap | 5 | 202.70 | -1.41 (-0.69%) | 4.946 | +0.030 (+0.61%) | 6.158 | +0.017 (+0.28%) | 365.9 | +2.27 (+0.63%) |
| nopicker | 5 | 202.77 | -1.34 (-0.66%) | 4.943 | +0.027 (+0.55%) | 6.236 | +0.096 (+1.56%) | 366.1 | +2.47 (+0.68%) |

2.1.1 and the build without the gotempo menu measure the same (fps p = 1.000). This session's
baseline is 0.18 fps above the first session's. Baseline runs spread by 0.94 fps in the first
session and 1.16 fps in this one.

## Caveats

- Network storage was measured with one NFS server over Wi-Fi, with only the profile on the share. A
  slower server stalls longer on each uncached lookup. Songs on network storage were not measured.
- Frame rate was uncapped, or held to 60 by the driver on the Igaku runs. Pacing to 120 or 144 Hz
  was not measured directly.
- One machine, one theme, three songs. The size of each figure will differ on other hardware.
- Five runs per setup allow p = 0.008 at best, so p = 0.048 is moderate evidence. Findings are
  reported where several setups agree.

## Reproducing

The harness is in `~/gotempo-bench`, outside this repository. `setup.sh` switches setups,
`run-once.sh` plays one song and collects the data, `batch.sh` runs setups round-robin, `compare.py`
prints the comparisons and `report.py` writes this document from the results.

```
sudo -v
./batch.sh "baseline module fakestrap oldfake gotempo" 5
python3 report.py
```
