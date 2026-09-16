# Performance

Measured cost of gotempo and gotempo-sl-module (the module) to ITGmania. Results are from one
Linux machine: ITGmania 1.3.0, Simply Love, i7-7700HQ with Intel HD 630, XFCE.

## Summary

Current versions: gotempo 2.1.0, module 2.1.1. Single player, VerTex Hard, frame rate uncapped,
five runs per setup. The module runs with simulated readings, so its panel is drawn and its graph
records.

| | Without the module | With the module | Change |
|---|---|---|---|
| Frames per second | 204.11 | 202.70 | -1.41 (-0.69%) |
| Typical frame (p50) | 4.916 ms | 4.946 ms | +0.030 (+0.61%) |
| Slowest 1 in 1000 frames (p99.9) | 6.141 ms | 6.158 ms | +0.017 (+0.28%) |
| Game CPU | 363.7 ms/s | 365.9 ms/s | +2.27 (+0.63%) |

- No stutter was measured. No setup changed p99.9 frame time significantly.
- On a song the machine cannot hold at 60 fps, no difference was measurable.
- gotempo uses 0.51 ms of CPU per second with no strap and 4.72 with two connected. It stays
  between 14.2 and 14.7 MB and never runs on the game's frame thread.
- The module added no memory growth over an hour.
- Module 2.0.0 and 2.1.0 measured the same. The stutter reported against 2.0.0 did not reproduce
  on Linux.

## Method

Each run launches ITGmania, plays one song to the end on autoplay with no input, and quits.

- MangoHud logs every frame's time from outside the game, with its overlay hidden.
- A sampler reads CPU, wakeups, memory and disk activity for the game, gotempo, `bluetoothd` and
  D-Bus from `/proc` once a second.
- Only gameplay counts. Two seconds are trimmed off each end of the song.
- Runs with thermal throttling are discarded. None were throttled.

Setups run round-robin so drift spreads across all of them. Figures are medians across runs. A
difference is reported as real when a permutation test over the runs gives p < 0.05 and it is
larger than the spread between runs of the same setup.

Simulated readings: several setups write `hr.txt` once a second in gotempo's own format instead
of running gotempo. The module then draws its panel and records the graph without a strap. The
module treats these the same as a strap, checked below.

The gotempo menu is the strap and appearance menu opened from ITGmania's sort menu.

## Runs

95 runs. Setup testing before these is not counted, nor one batch cancelled before its first
song.

| Song | Frame rate | Setup | Players | Module | gotempo | Readings | Runs |
|---|---|---|---|---|---|---|---|
| VerTex, Hard | uncapped | baseline | 1 | none | off | none | 10 |
| VerTex, Hard | uncapped | module | 1 | 2.1.0 | off | none | 5 |
| VerTex, Hard | uncapped | gotempo | 1 | 2.1.0 | on | none | 5 |
| VerTex, Hard | uncapped | fakestrap | 1 | 2.1.0 | off | simulated | 10 |
| VerTex, Hard | uncapped | fakestrap | 1 | 2.1.1 | off | simulated | 5 |
| VerTex, Hard | uncapped | nopicker | 1 | 2.1.0 without the gotempo menu | off | simulated | 10 |
| VerTex, Hard | uncapped | oldfake | 1 | 2.0.0 | off | simulated | 5 |
| VerTex, Hard | uncapped | strap | 1 | 2.1.0 | on | H10 | 5 |
| VerTex, Hard | uncapped | baseline2p | 2 | none | off | none | 5 |
| VerTex, Hard | uncapped | fakestrap2 | 2 | 2.1.0 | off | simulated, both players | 5 |
| VerTex, Hard | uncapped | twostraps | 2 | 2.1.0 | on | H10 and a Polar watch | 5 |
| VerTex, Hard | uncapped | twoplayers | 2 | 2.1.0 | on | one H10 for both players | 3 |
| Igaku, Challenge | capped 60 | baseline | 1 | none | off | none | 5 |
| Igaku, Challenge | capped 60 | fakestrap | 1 | 2.1.0 | off | simulated | 5 |
| Igaku, Challenge | capped 60 | fakestrap10 | 1 | 2.1.0 | off | simulated, 10 per second | 5 |
| Igaku, Challenge | capped 60 | oldfake | 1 | 2.0.0 | off | simulated | 5 |
| Eurobeat Is Fantastic, 60 minutes | uncapped | baseline | 1 | none | off | none | 1 |
| Eurobeat Is Fantastic, 60 minutes | uncapped | fakestrap | 1 | 2.1.0 | off | simulated | 1 |

The `oldfake` setups load a profile with no `gotempo.ini`, the case the 2.0.0 report described.
`twoplayers` used real profiles instead of the empty bench profiles, so it is left out of the
frame time comparisons. Its gotempo figures are in the Bluetooth table.

## Results

Each table compares against its first row, measured in the same session.

### Single player, uncapped

VerTex Hard, module 2.1.0 before the gotempo menu was changed.

| Setup | fps | Change | p50 ms | Change | p99.9 ms | Change | Game CPU ms/s | Change |
|---|---|---|---|---|---|---|---|---|
| baseline | 203.94 | reference | 4.919 | reference | 6.229 | reference | 364.3 | reference |
| module | 202.28 | -1.66 (-0.81%) | 4.956 | +0.037 (+0.76%) | 6.229 | -0.001 (-0.01%) | 369.7 | +5.40 (+1.48%) |
| gotempo | 202.47 | -1.46 (-0.72%) | 4.952 | +0.033 (+0.67%) | 6.196 | -0.033 (-0.53%) | 369.7 | +5.34 (+1.47%) |
| fakestrap | 201.75 | -2.18 (-1.07%) | 4.965 | +0.046 (+0.94%) | 6.122 | -0.107 (-1.72%) | 367.6 | +3.32 (+0.91%) |
| oldfake | 201.38 | -2.55 (-1.25%) | 4.970 | +0.051 (+1.04%) | 6.193 | -0.036 (-0.57%) | 368.6 | +4.28 (+1.17%) |

Loading the module costs 1.46 to 2.55 fps and 3.32 to 5.40 ms of game CPU per second (p = 0.048).
p99.9 is unchanged in every setup. Drawing the panel and recording the graph (`fakestrap`) cost
no more than loading the module (`module`). Running gotempo alongside (`gotempo`) made no
difference inside the game.

`oldfake` against `fakestrap` compares module 2.0.0 with 2.1.0 under identical conditions. No
metric differs.

### Capped at 60 fps

Notice Me Benpai 3 / Igaku, Challenge. The song ships 158 kB of its own Lua, and the machine holds
56.16 fps without the module. `fakestrap10` writes readings ten times a second.

| Setup | fps | Change | p50 ms | Change | p99.9 ms | Change | Game CPU ms/s | Change |
|---|---|---|---|---|---|---|---|---|
| baseline | 56.16 | reference | 20.858 | reference | 34.764 | reference | 661.0 | reference |
| fakestrap | 55.38 | -0.78 (-1.39%) | 20.926 | +0.069 (+0.33%) | 34.844 | +0.080 (+0.23%) | 662.1 | +1.02 (+0.15%) |
| fakestrap10 | 55.38 | -0.78 (-1.39%) | 20.912 | +0.054 (+0.26%) | 34.666 | -0.097 (-0.28%) | 662.8 | +1.78 (+0.27%) |
| oldfake | 55.32 | -0.84 (-1.50%) | 20.881 | +0.024 (+0.11%) | 34.632 | -0.132 (-0.38%) | 661.7 | +0.62 (+0.09%) |

No difference reaches significance. `fakestrap` adds 1.02 ms of CPU per second here and 3.32 on
VerTex at 204 fps. The module's cost is paid per frame, and this song runs fewer frames.

### Two players

VerTex Hard, both players joined, empty bench profiles. `twostraps` was measured earlier the same
day.

| Setup | fps | Change | p50 ms | Change | p99.9 ms | Change | Game CPU ms/s | Change |
|---|---|---|---|---|---|---|---|---|
| baseline2p | 186.51 | reference | 5.355 | reference | 8.456 | reference | 449.5 | reference |
| fakestrap2 | 184.40 | -2.11 (-1.13%) | 5.418 | +0.062 (+1.17%) | 8.748 | +0.291 (+3.45%) | 455.0 | +5.54 (+1.23%) |
| twostraps | 184.41 | -2.11 (-1.13%) | 5.412 | +0.056 (+1.06%) | 8.754 | +0.297 (+3.52%) | 453.4 | +3.91 (+0.87%) |

The module costs 2.11 fps and 5.54 ms/s with two panels (p = 0.048), close to its single player
cost. Real straps (`twostraps`) and simulated readings (`fakestrap2`) measure the same. Joining a
second player costs the game itself 17.42 fps and 85.2 ms/s.

### Simulated readings against a real strap

VerTex Hard, single player. `strap` ran with gotempo connected to an H10, earlier the same day.

| Setup | fps | Change | p50 ms | Change | p99.9 ms | Change | Game CPU ms/s | Change |
|---|---|---|---|---|---|---|---|---|
| fakestrap | 201.75 | reference | 4.965 | reference | 6.122 | reference | 367.6 | reference |
| strap | 201.27 | -0.48 (-0.24%) | 4.973 | +0.008 (+0.17%) | 6.211 | +0.089 (+1.46%) | 369.4 | +1.74 (+0.47%) |

No metric differs.

### gotempo and Bluetooth

CPU, wakeups and memory of the processes outside the game, during the same runs.

| Condition | gotempo CPU ms/s | Change | Wakeups/s | Memory MB | `bluetoothd` ms/s | System D-Bus ms/s |
|---|---|---|---|---|---|---|
| no strap | 0.51 | reference | 9.0 | 14.2 | 0.16 | 0.17 |
| one strap | 4.01 | +3.50 | 69.7 | 14.4 | 0.92 | 0.93 |
| two straps | 4.72 | +4.21 | 78.0 | 14.7 | 0.93 | 0.96 |
| one strap feeding both players | 4.41 | +3.90 | 77.9 | 14.6 | 0.93 | 0.96 |

The first strap adds 3.50 ms/s. The second adds 0.71 more. One strap feeding both players costs
4.41 ms/s, close to two straps, so most of the cost comes from handling readings. `bluetoothd`
and the system D-Bus use the same CPU with one strap or two.

### One hour

Eurobeat Is Fantastic, a 60 minute chart, one run each.

| Setup | fps | Change | p50 ms | Change | p99.9 ms | Change | Game CPU ms/s | Change |
|---|---|---|---|---|---|---|---|---|
| baseline | 207.93 | reference | 4.822 | reference | 6.099 | reference | 349.3 | reference |
| fakestrap | 204.76 | -3.17 (-1.52%) | 4.904 | +0.082 (+1.71%) | 6.152 | +0.053 (+0.87%) | 355.9 | +6.63 (+1.90%) |

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
updated every frame. The gotempo menu is 211 actors: two panels of 104 (16 for the frame, header,
status and footer, and 8 rows of 11) and 3 for its clock and input guard.

Module 2.1.0 against a build with the gotempo menu removed, same session:

| Setup | fps | Change | p50 ms | Change | p99.9 ms | Change | Game CPU ms/s | Change |
|---|---|---|---|---|---|---|---|---|
| fakestrap | 201.30 | reference | 4.976 | reference | 6.210 | reference | 369.8 | reference |
| nopicker | 202.16 | +0.85 (+0.42%) | 4.958 | -0.018 (-0.36%) | 6.096 | -0.113 (-1.82%) | 367.0 | -2.87 (-0.78%) |

The gotempo menu cost 0.85 fps and 2.87 ms/s, 68 ns per actor per frame.

Module 2.1.1 hibernates the gotempo menu one second after the song wheel closes and wakes it when
the wheel returns. Hibernated actors are not updated. Measured with its own baseline:

| Setup | fps | Change | p50 ms | Change | p99.9 ms | Change | Game CPU ms/s | Change |
|---|---|---|---|---|---|---|---|---|
| baseline | 204.11 | reference | 4.916 | reference | 6.141 | reference | 363.7 | reference |
| fakestrap | 202.70 | -1.41 (-0.69%) | 4.946 | +0.030 (+0.61%) | 6.158 | +0.017 (+0.28%) | 365.9 | +2.27 (+0.63%) |
| nopicker | 202.77 | -1.34 (-0.66%) | 4.943 | +0.027 (+0.55%) | 6.236 | +0.096 (+1.56%) | 366.1 | +2.47 (+0.68%) |

2.1.1 and the build without the gotempo menu measure the same (fps p = 1.0). This session's
baseline is 0.18 fps above the first session's. Baseline runs spread by 0.94 fps in the first
session and 1.16 fps in this one.

## Caveats

- With vsync on and frames to spare, none of these differences are visible.
- One machine, one theme, three songs. The size of each figure will differ on other hardware.
- Five runs per setup allow p = 0.008 at best, so p = 0.048 is moderate evidence. Findings are
  reported where several setups agree.

## Reproducing

The harness is in `~/gotempo-bench`, outside this repository. `setup.sh` switches setups,
`run-once.sh` plays one song and collects the data, `batch.sh` runs setups round-robin, and
`compare.py` prints the comparisons.

```
sudo -v
./batch.sh "baseline module fakestrap oldfake gotempo" 5
python3 compare.py --uncapped
```

Windows has not been measured.
