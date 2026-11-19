# The SqueezeDSP engine, with our changes

Source of the `SqueezeDSP` binary the plugin runs: Jonathan Fox's
[foxLyrionDSP](https://github.com/Foxenfurter/foxLyrionDSP) and
[foxAudioLib](https://github.com/Foxenfurter/foxAudioLib) (MIT, his `LICENSE` files kept), at the commits
named in `UPSTREAM`, plus the changes below. Only source is copied (`*.go`, `go.mod`, `go.sum`, `LICENSE`,
`README.md`), not his binaries and test media.

**Names:** a package that is wholly ours is named `gipsy…`: `foxAudioLib/gipsyUpsampler`, `foxAudioLib/gipsySpeed`,
`foxAudioLib/foxAudioDecoder/gipsyDSDReader`, `foxAudioLib/foxAudioEncoder/gipsyDSDEncoder` (named `fox…` until
2026-11-19). Fox's packages and modules keep their `fox…` names, our changes inside them included.

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
- **The true length, stated** (the exact-length round, 2026-11-15: `LyrionDSPProcessAudio/length.go`,
  `flacseek.go`, `tail.go`; `gipsyDSDEncoder` `Count`, `HeldCount`, `HeldBack`). A stream's header states the frames it
  carries where the input's count is known at its start: a DSD file, a WAV input's own header, `--insamples=N`, or a
  decoder's WAV header the plugin vouches for (`--insamples=header`); the cut points come from the continuous stream's
  positions, so a gapless album never drifts. For a player that needs the true length before the first byte (the
  plugin's note `exact=1`) the end is decided at the stream's start and every stream, a held one too, is exactly its
  stated count; `--tail` plays a held stream's tail out on its own; `--flacseek=OFFSET` reads a remote FLAC's count
  from its URL after a seek. The WAV reader's "unknown size" note goes to stderr (it went into the audio).
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
- **The engine's own speed** (`foxAudioLib/gipsySpeed`, new; `foxAudioEncoder` `EncodeData`; `LyrionDSPProcessAudio`
  `attachMeters`, `reportSpeed`). The FILL: the audio the engine writes to stdout and the time taken, its waits for
  the reader left out, while the reader (LMS, so the player) takes everything, at a stream's start until the player's
  buffer is full; window by window of 2 s of audio, the first window the reader holds back (its writes waiting over a
  quarter of its time and over 50 ms) ends it and the figure stays that of the windows before. Exact by construction:
  nothing measured while the player paces the stream is. Timing the output after the fill reads high (the stages before
  the encoder work while its write waits: WAV out with the engine upsampling to 705.6k, no FIR, 37.9x flat out read 63x
  paced like a player), and so does each stage's own working time (the stages also wait on each other and for a core:
  DSD64 with a 1 s FIR 3.97x flat out, the busiest stage's own time 5.43x). The fill against the reader's own view of
  it, M3, the 1 s FIR: DSD512 from 96k 2.14x / 2.13x, DSD64 4.72x / 4.63x, the WAV route 5.30x / 5.02x. A stream that
  starts with the player's buffer full (a gapless change) has no fill: none reported. Each stage also counts its own
  arithmetic (`Meter`: the WAV reader's conversion, our DSD reader's decode, the upsampler, each channel's
  convolution in `foxConvolverPartition` `ConvolveChannel`, the merger's gains, width and crossfeed in `mergeChannels`,
  the encoding), which names the BUSIEST stage. Every second the engine writes `<temp>/speed_<player>.json` (pid,
  the fill's audio and time, whether it is over, the busiest stage). Tests: `go test ./gipsySpeed/`, `go test -vet=off
  -run 'TestMeter|TestFill' ./foxAudioEncoder/`, `go test -vet=off -run TestMeter ./foxConvolverPartition/`, `go test
  -run TestMeter ./foxAudioDecoder/gipsyDSDReader/` (in `foxAudioLib`), `go test ./LyrionDSPProcessAudio/` (in
  `foxLyrionDSP`; the WAV reader's test is there, as Fox's own test file in `foxWavReader` does not compile).
- **The engine's speed while it plays** (0.1.72.46; `foxAudioLib/gipsySpeed/runs.go`, new: `Lead`, `Runs`;
  `foxAudioEncoder` `Out`, `WritesStdout`; `LyrionDSPProcessAudio` `attachLead`, `entryStage`, `reportSpeed`,
  `RunsSpeed`). The fill measures only a stream's first seconds: once the player paces the stream nothing runs flat out
  again. The engine now keeps its output ahead of the reader in a lead and refills it in runs, flat out: once the lead
  holds its run's mark, its writes wait until the reader has taken it down to 1 s. The mark, set as each run starts, is
  that 1 s, plus 2 s, plus the audio in the pipeline then (taken in, not yet out), up to 15 s: a shorter run is met from
  what the stages after an early slowest stage hold and measures nothing (48k to 768k with the upsampler slowest). Each
  run is timed where the audio ENTERS the pipeline (`entryStage`): a run starts with every stage held, so a block goes
  in each time the slowest stage takes one, whichever stage that is (at the output a run reads high). The report holds
  the runs of the last 10 s once there are any, else the fill, and is written from the engine's start; the end line
  says `speed while playing`. NOT on Windows: LMS runs every Windows pipeline through its socketwrapper, which gives
  each process 2 s once the pipeline's source has ended, then ends it (`socketwrapper.cpp`, `tidy`), and LMS reads the
  lead at the player's pace, so a track's end would be cut: there the fill alone, as before. The output is unchanged:
  13 routes byte for byte against the engine before, read flat out and read like a player, the FIR loaded (M3).
  On an M3, read like a player against the same input read flat out: DSD256 from 44.1k 7.95-8.39x against 8.49, 48k to
  768k by Kaiser extra-long (the upsampler the slowest) 7.61-7.95 against 7.74, a FIR-bound 705.6k 27-36 against 31.1;
  at the limit (DSD512, two busy processes a core) 1.63-1.70 against 1.74. A load showed from the next run; with every
  core busy and margin left the runs read up to 44% above a run that never rests (3.65-3.84 against 2.67). Tests: `go test ./gipsySpeed/` (in `foxAudioLib`), `go test -run 'Entry|AttachLead|ReportSpeed'
  ./LyrionDSPProcessAudio/` (in `foxLyrionDSP`), `python3 -I t/engine_mutants.py 'lead|runs|entry|report:'`.
- **Bounds checks out of the hot loops** (Simon: "saving any time is plus regardless of how small a cpu gain it is").
  The same sums in the same order, so the same bits: six routes byte-identical before and after on Apple silicon and
  on x86 (under Rosetta), the FIR loaded. `foxConvolverPartition` `processChunk` (Fox's): the partitions' multiply-adds
  on local slices of one length (12 bounds checks per four bins to one; the result's slice no longer reloaded from the
  convolver at every bin), the copies `copy`; `TestChunkBitsUnchanged` holds it to Fox's loop bit for bit, his kept in
  the test; `BenchmarkChunkFox` against `BenchmarkChunkOurs` (a 1 s FIR at 705.6k, 87 partitions of 8192): 1.43-1.99
  against 1.12 ms a chunk. `gipsyUpsampler` `poly.push` (ours): eight checks per four taps to one (CD short intermediate
  32.5 to 20.8 ms, long linear 133.6 to 124.7, hi-res short linear 17.0 to 14.8, `go test -bench .`). `gipsyDSDReader`
  (ours): stage 1 four checks per four bytes to two, stage 3 none per tap.
- **The FFT's plans above 8192 points** (`foxAudioLib/foxFFT`, `bigPlan`, `bigRealPlan`, `bigWorkPool`). Fox's plans,
  twiddle tables and pooled work buffers stopped at 8192 points, the sizes his header comment gives for 176.4k, but
  the convolver there runs partitions of 8192, so a real FFT of 16,384 points: a `Sincos` per bin both ways and a
  128 KB buffer allocated at every call. Now built on first use up to 131,072 complex / 262,144 real points. The same
  twiddles by the same expression and the same loops, so the same bits: `TestBigPlansSameBits` against his paths, kept
  verbatim in the test, at every size, on Apple silicon and x86; six engine routes byte-identical with the FIR loaded.
  `BenchmarkChunkOurs` at 705.6k with Fox's partitions: 1.15 to 0.95 ms a chunk, 262 KB of allocation to none.
- **Larger partitions for a long impulse at 176.4k and up** (`foxConvolverPartition`, `partitionSize`). Each output
  sample costs a complex multiply-add per partition: a 1 s FIR at 705.6k was 87 partitions of 8192. Now Fox's 8192
  doubles until the impulse needs at most 16, up to 65,536; below 176.4k, outside streaming and for a short impulse
  (the PEQ alone) his sizes stay. The same convolution to rounding (`TestPartitionSizesSameConvolution`: -300 dB),
  so PCM out byte-identical on the bench; DSD out's bits differ (the modulator carries any change). `MaxGain` (the
  level's headroom) was still measured over his partitions here (0.1.72.36), so the level did not move; since
  0.1.72.37 it is the whole filter's peak (below). The filter cache keeps the size (`TestCacheKeepsPartitionSize`).
  `BenchmarkPartition` (one channel, ms a second of audio, M3 / x86 under Rosetta, against 8192): 705.6k with 705,600
  taps 37.7 / 71.6 against 85.9 / 144.7; 480,000 taps 37.2 / 72.1 against 68.7 / 116.3; 352.8k with 352,800 taps 17.2 /
  33.3 against 25.1 / 46.4.
- **An exact gapless join** (`foxConvolverPartition` `ConvolveChannel`, `EncodeState`, `DecodeState`;
  `LyrionDSPProcessAudio` `CACHE_VERSION` 6). Fox's state hand-over went on from the zero-padded last partition, with
  the tail of the partition before it: the next track's first outputs, as many as the filter is long, heard the last
  track's end shifted by up to a partition (it replayed and then skipped what the track ended with). The bench, a
  1 s FIR, two tracks against one run of the whole: 0.1.72.35 wrong for 1.0 s after the join at 96k (worst -7.9 dBFS)
  and 945 ms at 705.6k (-1.2 dBFS). Now the next track goes on from the last whole partition: the ring is put back
  after the padding, the input short of a partition is kept in the state and starts the next track, whose first
  outputs, sent by the last track already, are dropped. Byte-identical to the whole run at both rates; `TestGaplessJoin`
  (a delay of none, under a partition and over several, partitions of 4096 to 65,536, three tracks, a track shorter
  than what the last left over). The FFT, partition and join tests: `go test ./foxFFT/` and `go test -vet=off -run
  'TestPartition|TestMaxGain|TestCache|TestGapless' ./foxConvolverPartition/` (in `foxAudioLib`); `go test -run XXX
  -bench Partition ./foxConvolverPartition/` for the sizes.
- **PCM to any rate, the filter at the lower Nyquist** (`foxAudioLib/gipsyUpsampler` `New`; `--upsample` from 44.1k,
  `LyrionDSPSettings`). The plugin's Upsample (PCM) now resamples here, before the DSP, in place of ffmpeg's soxr
  (0.1.72.37, config 0.3.20), so every PCM target goes through `New`, up or down, both families. Stage A, the filter
  with the character (the player's choice of phase and length), now always runs, a x2 stage between the lower of the
  two rates and twice it: going up, A from the source then B (rational, gentle) to the target, B going down when the
  target is under twice the source (44.1k to 48k is 88.2k to 48k); going down, B from the source to twice the target,
  then A as a 2:1 decimator (gain 1), with the widths of the target's class (a hi-res source down to 48k meets the
  CD-rate edges). B is flat to the lower Nyquist and stopped from where an image or an alias would land below it.
  Until then a target under twice the source, or under it, went through B alone, flat to 45% of the lower rate and
  stopped at 55%: images and aliases of the band's top 10% at -7 to -15 dB (44.1k to 48k, 176.4k to 192k, every
  downward rate), and no character filter at all. The grid (8 source rates, 9 targets each, all six filters, end to
  end, tones through `New`/`Process`/`Flush`): the worst image or alias -170.9 dB (was -6.7 / -14.5), the level within
  0.0013 dB at 19 kHz; the long filters cost up to 65 ms a second of audio a channel on the M3 wherever they run (as
  44.1k to 705.6k already did). Up to twice the source and up from there, the bits are unchanged (the converter to DSD
  from 44.1k to 352.8k). Tests: `TestStageB` (every pair: the order, B's edges and stop band), `TestSineExact` (every
  pair, -150 dB), `TestNoImagesOrAliases` (every way through, -165 dB; it fails on the earlier code at -6.7 to -51 dB),
  `TestDownWidths`, `TestBlocksAndLength` and `TestAligned` with downward pairs: `go test ./gipsyUpsampler/` (in
  `foxAudioLib`).
- **The filter family** (`foxAudioLib/gipsyUpsampler`: `Family`, `Length`, `Choices`, `widths`, `shaped`; design.go's
  `gaussLowpass`, `sqrtHalf`, `nonRinging`; 0.1.72.44, ledger THE FILTER FAMILY and FAMILY STUDY). Stage A is one of 25
  filters: Kaiser short, long, extra-long (`short-int`, `long-lin`, `xlong-min`: the six before keep their names and
  their bits, checked sample for sample on arm64 and amd64), Gaussian short, medium, long (`gauss-medium-lin`: a
  Gaussian-windowed sinc designed in closed form, no search), Half-band and Slow roll-off (Kaiser edges either side of
  the lower Nyquist), each in linear, intermediate or minimum phase, and Non-ringing (`nonring`: every tap positive).
  Kaiser and Gaussian stop everything from the lower Nyquist on; the last three let images through by design, and going
  down run as Kaiser short in their phase. The new intermediates are a square-root half times its minimum phase, so
  they keep the linear one's magnitude. B passes a leaky A's band where it can. Design at a stream's start, M3: up to
  325 ms (Kaiser extra-long minimum; today's long minimum 171). Tests: `TestStopBands` (every filter, every lower rate),
  `TestIntermediateKeepsMagnitude`, `TestNonRinging`, `TestLeaky`, `TestWidths`, `TestParseChoice`, the rest with the
  new families; `t/engine_gapless.py` adds five routes through them.
- **`MaxGain` the whole filter's peak** (`foxConvolverPartition` `peakGain`, `filterCacheVersion`). Fox's `MaxGain` (the
  level's headroom: `PrepareChannelGains` divides by it) was the largest bin of any one partition's FFT: it moved with
  the partition size, read Simon's impulse up to 9% high (the level up to 0.76 dB low) and, with FIR Strength under 100
  at 176.4k and up, about half the true peak (the output about 5 dB over: the dry half and the filter's main lobe sit in
  different partitions). Now it is the filter's largest |H(f)|: the impulse's real FFT padded to at least four times its
  length, every local maximum within 0.3 dB of the largest bin refined by a parabola on a dB scale. Within 0.01 dB of a
  grid 16 points a tap fine, a narrow resonance halfway between two bins included; the same at any partition size. The
  filter cache leads with a version (-2) so a cache an earlier engine wrote is built again (it is otherwise taken
  whenever it is newer than the settings). Tests: `TestMaxGainIsTheFiltersPeak`, `TestMaxGainBetweenBins`,
  `TestMaxGainStrengthBlend` (the ledger's 30 kHz tone: -6.02 dB, Fox's measure -1.14), `TestFilterCacheVersion`; the
  end to end `t/engine_gain.py` (ReplayGain off at 352.8k, 384k and 705.6k: -0.001 dB, where the earlier engine read
  -0.34 to -0.39).

Until 0.1.72.37, without the switches, and with ReplayGain off or a FIR at its own rate, the engine's DSP output was
byte-identical to Fox's shipped 0_2_40 (bench, raw 16/44.1 and 24/96 input with a preamp, four filters and
loudness; ReplayGain off with a 96 kHz FIR at 96 and 384 kHz, 2026-11-01). With ReplayGain on, or a FIR at
another rate with ReplayGain on, it differs by design. Since 0.1.72.37 the level with a filter (a FIR or the PEQ)
is normalised to the whole filter's peak, so it differs from Fox's by the gap between the two measures wherever the
filter spans more than one partition (`MaxGain`, above). Fox's 0_2_40 rejects `--wavlength` (unknown flag), so
the plugin's PCM rules need this build.

Tests of these three: `go test -run 'Scale|Cap|Resolve' ./LyrionDSPProcessAudio/` (in `foxLyrionDSP`),
`go test -run RMSGain ./foxConvolverPartition/` (in `foxAudioLib`), the end-to-end
`python3 -I t/engine_gain.py`, and `python3 -I t/engine_mutants.py`.

## Build

    sh engine/build.sh               # Linux x64 (the rig) into SqueezeDSP/Bin/publishLinux-x64/
    sh engine/build.sh all           # the six platforms the plugin ships (all six ours since 0.1.72.46)
    OUT=/tmp/x sh engine/build.sh osx-arm64

Go 1.25 or later. The settings are those of Fox's `Build-All.bat` (pure Go, `GOEXPERIMENT=jsonv2`, stripped),
plus `-trimpath`. `GOTOOLCHAIN=local` means Go never downloads a toolchain. The two modules the source needs
come from the Go proxy as source, cached in `build/go`.

## The DSD encoder's spec bench

    sh engine/encoder-spec.sh                    # tones and stability, every rate and modulator -> build/encoder-spec.md
    sh engine/encoder-spec.sh a.dsf b.dsf        # and what the modulator adds to real music, each file at its own rate
    ALLRATES=1 sh engine/encoder-spec.sh a.dsf   # each file at every DSD rate

`foxAudioLib/foxAudioEncoder/gipsyDSDEncoder/spec_test.go`, skipped by a plain `go test`. About two minutes for tones
plus one file at every rate. It runs the encoder's shipped path and reads the DSF back in double precision. The
noise in band (20 Hz-20 kHz), per octave out of band, and where it rises through each level, per third octave.
The loop's resets for steady signals and for signals that start at the limit. `START`, `SECS`, `OUT` and `WRITE`
(write the DSF files, to compare with the engine's own output) are in the script's header.

## Fox's later changes

    sh engine/merge-upstream.sh status   # what Fox has committed since our base
    sh engine/merge-upstream.sh          # merge it in, three-way, file by file; UPSTREAM moves on
    sh engine/merge-upstream.sh diff foxAudioLib   # our changes to one repo, against the base

A merge that clashes with our changes leaves the file marked `<<<<<<< ours` / `||||||| base` / `>>>>>>> fox`
and exits 1. The `diff` output is what a pull request to Fox would carry: in his repo,
`git apply -p2 <patch>`.
