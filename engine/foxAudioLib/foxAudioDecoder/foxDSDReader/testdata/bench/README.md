# DSD decoder bench (foxDSDReader)

Scripts that measured the engine's own DSD decoder against today's routes, 2026-11-02 (results:
`docs/fork-plan.md`, Phase 2, "Our DSD decoder"). Go ignores `testdata/`, so none of this is built into the engine.
On Simon's Mac only LMS's bundled darwin binaries, the engine and tools built from source here may run.

- `../../foxDSDReader_test.go`: the unit tests (`go test ./foxAudioDecoder/foxDSDReader/` in `engine/foxAudioLib`,
  with the env of `engine/build.sh`). Fixtures are made inside the tests.
- `mutate.py <repo> <work>`: 23 mutants (the decoder, its stage 3 and its engine flags), each must turn its own test red.
- `e2e_rational.py` (the arguments below, then `<before engine> [nospeed]`): stage 3's targets (48k family, 705.6k
  from DSD64): the 44.1k-family targets byte-identical to an engine built from before stage 3, level and noise
  against today's `ffmpeg | engine`, seeks/cue ends/cue tracks byte-exact, a gapless join at 96k, refusals, and
  60 s speed at 96k/384k/705.6k/768k.
- `e2e_dsd.py <engine> <ffmpeg> <dsdana> <fixtures> <work>`: the engine (`--formatin=DSD`) against today's
  `ffmpeg | engine` rule: level, WAV length, seeks (`--skip`) and cue ends (`--until`) byte-exact, refusals.
  `e2e_dsd_setup.py` is its setup alone (no tests), imported by:
- `e2e_dsd2.py` (same arguments): 60 s speed, gapless joins, Bypass, FLAC out. `flccheck.py`: FLAC out's 1-2 LSB
  difference from WAV out on today's PCM route (not sox dither: ledger §A3 `SOX DOES NOT DITHER AT 24 BITS`).
- `e2e_seek_hi.py` (same arguments): seeks and cue ends at DSD256 and DSD512, at 352.8k and 88.2k.
- `e2e_prefs.py` (same arguments): the engine takes `DSDCutoff`/`DSDGain` from the player's settings JSON, a
  `--dsdcutoff`/`--dsdgain` flag wins, an invalid value falls back to 30 kHz / 0 dB.
- `dsftool.py`: tile or split a DSF. `dsdana/`: the analyser (Kaiser beta 30, 2^n FFT), `go build` it.

Fixtures (`<fixtures>`), made with LMS's darwin sox (t/lmsbin) at -6.02 dB modulation, 6 s:
`sox -r 2822400 -n -c 2 d64_1k.dsf synth 6 sine 1000 gain -6.02 sdm -f clans-8` (and 20000 Hz; -r 5644800 d128,
-r 11289600 d256, -r 22579200 d512; `.dff` for DSDIFF). The ffmpeg used: built from source with the DSD decoders and soxr (an
earlier session's scratchpad `ffdsd/`).
