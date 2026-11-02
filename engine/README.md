# The SqueezeDSP engine, with our changes

Source of the `SqueezeDSP` binary the plugin runs: Jonathan Fox's
[foxLyrionDSP](https://github.com/Foxenfurter/foxLyrionDSP) and
[foxAudioLib](https://github.com/Foxenfurter/foxAudioLib) (MIT, his `LICENSE` files kept), at the commits
named in `UPSTREAM`, plus the changes below. Only source is copied (`*.go`, `go.mod`, `go.sum`, `LICENSE`,
`README.md`), not his binaries and test media.

## Our changes

- **A WAV or AIFF file is read to its end** (`foxAudioLib/foxAudioDecoder/foxWavReader/foxWavReader.go`,
  `DecodeInput`). The read loop stopped as soon as the end of the data chunk was inside its 512 KB buffer,
  dropping what it had not handed out yet: up to about 3 s of a 16/44.1 file. It now reads up to the data
  chunk's end and processes everything it holds before it stops.
- **`--skip` starts part way in** (`foxAudioLib/foxAudioDecoder/foxAudioDecoder.go`, `skip`). It was parsed but
  never applied. It now passes over whole frames: a file is sought past them, a pipe has them read away. A
  known length shrinks to match, so the WAV header states what remains.
- **`--wavlength=SECONDS`** (`foxLyrionDSP/LyrionDSPSettings`, `LyrionDSPProcessAudio`). The length a WAV
  output's header states, in whole frames. Above 4 GB, or with 0, the header says the length is unknown.
  Without the switch the header is as before. The plugin passes the length still to play plus 1 s, so a
  player that reads the header (squeezelite `-W`, a UPnP renderer) knows the track's length.
- **A missing `}`** in `EncoderAdapter.EncodeData` (`foxAudioLib/foxAudioEncoder/foxAdapterEncoder`), the
  one compile error in Fox's committed source.
- **A FIR impulse keeps its gain at any rate** (`foxAudioLib/foxResampler`, `ReadnResampleFile2Buffer`;
  `foxLyrionDSP/LyrionDSPProcessAudio`, `scaleImpulse`). sox resamples the impulse as it would audio, keeping
  its sample values, so at R times its own rate the filter had R times its gain: a 96 kHz impulse played
  +12 dB loud at 384 kHz, and the gain compensation took back only part of it. The resampler now reads the
  file's own rate (`sox --i -r`, `FromSampleRate`) and the engine scales the impulse by its rate over the
  stream's.
- **`RMSGain` over the audio band** (`foxAudioLib/foxConvolverPartition`, `RMSGainInBand`). It was the
  impulse's L2 norm, the RMS of its response from 0 Hz to Nyquist, so at 384 kHz it was mostly the band
  above 48 kHz where a filter made at 96 kHz passes nothing, and ReplayGain's weighted compensation added
  gain that grew with the rate. It is now the RMS over 20 Hz to 20 kHz, the same at every rate. A
  convolver made without a rate keeps the L2 norm.
- **ReplayGain capped by its peak** (`LyrionDSPProcessAudio`, `ResolveReplayGain`, `capByPeak`), as LMS caps
  it (`Slim::Player::ReplayGain::preventClipping`): no track or album gain takes the peak it was measured
  with past full scale. The peaks were fetched and only logged. An album gain with no album peak is capped
  by the track's own peak (`albumPeak`), where LMS leaves it uncapped.

Without the switches, and with ReplayGain off or a FIR at its own rate, the engine's DSP output is
byte-identical to Fox's shipped 0_2_40 (bench, raw 16/44.1 and 24/96 input with a preamp, four filters and
loudness; ReplayGain off with a 96 kHz FIR at 96 and 384 kHz, 2026-11-01). With ReplayGain on, or a FIR at
another rate with ReplayGain on, it differs by design. Fox's 0_2_40 rejects `--wavlength` (unknown flag), so
the plugin's PCM rules need this build.

Tests of these three: `go test -run 'Scale|Cap|Resolve' ./LyrionDSPProcessAudio/` (in `foxLyrionDSP`),
`go test -run RMSGain ./foxConvolverPartition/` (in `foxAudioLib`), the end-to-end
`python3 -I t/engine_gain.py`, and `python3 -I t/engine_mutants.py`.

## Build

    sh engine/build.sh               # Linux x64 (the rig) into SqueezeDSP/Bin/publishLinux-x64/
    sh engine/build.sh all           # the six platforms the plugin ships
    OUT=/tmp/x sh engine/build.sh osx-arm64

Go 1.25 or later. The settings are those of Fox's `Build-All.bat` (pure Go, `GOEXPERIMENT=jsonv2`, stripped),
plus `-trimpath`. `GOTOOLCHAIN=local` means Go never downloads a toolchain. The two modules the source needs
come from the Go proxy as source, cached in `build/go`.

## Fox's later changes

    sh engine/merge-upstream.sh status   # what Fox has committed since our base
    sh engine/merge-upstream.sh          # merge it in, three-way, file by file; UPSTREAM moves on
    sh engine/merge-upstream.sh diff foxAudioLib   # our changes to one repo, against the base

A merge that clashes with our changes leaves the file marked `<<<<<<< ours` / `||||||| base` / `>>>>>>> fox`
and exits 1. The `diff` output is what a pull request to Fox would carry: in his repo,
`git apply -p2 <patch>`.
