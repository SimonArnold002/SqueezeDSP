# DSD encoder bench (gipsyDSDEncoder)

The engine's native DSD output (`--formatout=DSF --dsdrate=N [--dsdmod=standard|clans] [--dsdoutgain=dB]`), measured
2026-11-02 (results: `docs/fork-plan.md`, Phase 2, "Our DSD encoder"). Go ignores `testdata/`, so none of this is built
into the engine. On Simon's Mac only LMS's bundled darwin binaries, the engine and tools built from source here may run.

- `../../gipsyDSDEncoder_test.go`: the unit tests (`go test ./foxAudioEncoder/gipsyDSDEncoder/` in `engine/foxAudioLib`, with
  the env of `engine/build.sh`). `-run TestGenerateTables -args -update` remakes `tables.go` from `ntf.go` and refuses a
  design that runs away at 0.75. `bench_test.go`: per-stage speed (`-bench .`).
- `mutate.py <repo> <work>`: 138 mutants (the loop, the pair, the interpolator, the DSF stream, the designs, adaptive,
  the source's designs and limit, the interpolator's overshoot held and when hold may skip, the encoder's pipeline, the
  upsampler `gipsyUpsampler`, the engine flags and its upsample stage, the engine's own speed for the page: the fill and
  its end, each stage's meter leaving out its waits and counting every frame, every stage given one, the busiest taken,
  the report; the convolver's and the upsampler's loops as the bounds-check round left them; the FFT's plans and pooled
  buffers above 8192 points, the convolver's partition rule, MaxGain kept at Fox's size, the filter cache keeping the
  size, the gapless hand-over), each must turn its own
  test red. Tests run with `-vet=off`: Fox's `foxAudioEncoder` fails vet (its `Sprintf` calls),
  which would fail every build there; by hand: `go test -vet=off -run TestMeter ./foxAudioEncoder/`. A mutant of
  `foxAudioLib` may run its test in `foxLyrionDSP` (the WAV reader's: Fox's own test file there does not compile).
- `e2e_dsf.py <engine> <ffmpeg> <dsdana> <dsdplay> <fixtures> <work> [nospeed]`: DSD in and PCM in, DSF out at DSD64-512,
  all three modulators (standard, clans, adaptive): the header against the bytes written, the level back through four readers (the engine's decoder,
  ffmpeg, LMS's sox, dsdplay), the limit and resets from the engine's log, noise against LMS's sox `sdm`, `--skip`,
  `--until`, refusals, 60 s speed. Since 0.1.72.33 also the converter: WAVs at 44.1k, 48k and 96k upsampled by the
  engine itself (`--upsample=705600 --upfilter --dsdsource=pcm --dsdoutgain=-6.02`) to DSD64 and DSD256: the length
  stated, the filter the engine logs for the source's rate, the PCM source's designs held at 0.63, the level back,
  inter-sample overs held, a seek, a pipe, the new refusals. Since 0.1.72.35 the engine's own speed report, left in the
  temp folder at a run's end: its busiest stage, a stage of the route; its fill, the whole track here as the bench
  reads flat out, over (DSD in, the converter, PCM out). 160 checks. The fixtures and tools are the decoder bench's (`gipsyDSDReader/testdata/bench`).
- `../../spec_test.go` (`sh engine/encoder-spec.sh [a.dsf ...]`, report `build/encoder-spec.md`): THE SPEC BENCH, kept
  so it is never rebuilt (Simon 2026-11-03). It needs no tool from outside the package. It covers DSD64-512, every
  modulator, and 1 kHz at SACD -6 and 0 dB:
  - the noise in band and its sub-bands;
  - the noise out of band per octave, to Nyquist;
  - where it rises through -140 to -40 dB, per third octave, and the whole third-octave profile;
  - the loop's resets for steady and starting signals at the limit;
  - with DSF files, what the modulator adds to the music: its output less its own input.

  It reads the DSF back as +-1 in double precision, after a self-check of its own analysis. 2026-11-03: its DSF
  output for Factory Girl 0:30-0:40 equalled the engine binary's, byte for byte, at all 12 rate and modulator
  pairs.
- `dsf_section.py <LMS base URL> <out dir> <name> <track id> <start s> <length s>`: a section of a DSF from an LMS
  library over HTTP byte ranges, written as a valid DSF, so a 1.7 GB DSD512 track costs 62 MB for 11 s. Fetch 11 s and
  run the bench with `START=1 SECS=10`.
