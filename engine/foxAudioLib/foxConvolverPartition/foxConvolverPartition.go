package foxConvolverPartition

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"math"
	"math/bits"
	"math/cmplx"
	"strconv"
	"time"

	fft "github.com/Foxenfurter/foxAudioLib/foxFFT"
	"github.com/Foxenfurter/foxAudioLib/foxUtils"
	"github.com/Foxenfurter/foxAudioLib/gipsySpeed"
)

var (
	PartitionedChunkSize        = 4096 // New samples per chunk (L)
	PartitionedFFTSize          = 8192 // FFT size = 2 × chunk size (N)
	PartitionedHalfFFTSize      = 4097 // N/2+1 — unique bins from RealFft
	PartitionedImpulseChunkSize = 4096 // Impulse partition size
	PartitionedOverlapSize      = 4096 // overlap samples (L-1 conceptually, kept = L)

	ProcessingMode = foxUtils.ModeStreaming // default
)

const packageName = "foxConvolverPartition"

type PartitionedConvolver struct {
	FilterImpulse []float64
	impulseLength int
	outputLength  int // Target output block size for downstream

	// Partitioned convolution state
	// impulsePartitions[p] has length PartitionedHalfFFTSize (N/2+1)
	impulsePartitions [][]complex128
	numPartitions     int

	// Ring buffer for audio chunk FFTs — each slot length PartitionedHalfFFTSize
	audioFFTRing [][]complex128
	ringPosition int
	ringSize     int
	ringFilled   bool

	// Overlap-save state
	previousChunkTail []float64 // PartitionedOverlapSize samples from previous chunk

	// Input / output buffering
	Buffer       []float64
	outputBuffer []float64

	// (SqueezeDSP fork) the gapless hand-over (EncodeState / DecodeState): the input a track ended with short of a
	// whole partition, which the next track starts from, and how many outputs it then drops, sent by the last track
	pending []float64
	skip    int

	// Pre-allocated working buffers
	workResult  []complex128 // length PartitionedHalfFFTSize
	workRealBuf []float64    // length PartitionedFFTSize — real input + IFFT output
	workOutput  []float64    // length PartitionedChunkSize
	MaxGain     float64
	RMSGain     float64
	sampleRate  int // the rate the impulse runs at, for RMSGain's audio band; 0 when not known

	// Debug
	DebugOn     bool
	DebugFunc   func(string)
	WarningFunc func(string)

	// Meter (SqueezeDSP fork): ConvolveChannel's own work, for the engine's speed (gipsySpeed); its waits for the
	// stages either side left out. Not in the filter cache (GobEncode names its fields).
	Meter *gipsySpeed.Meter
}

// ─── Size configuration ───────────────────────────────────────────────────────

func SetChunkSizeForSampleRate(sampleRate int) {
	switch {
	case sampleRate >= 176400: // 192 kHz, 176.4 kHz
		PartitionedChunkSize = 8192
	case sampleRate >= 88200: // 96 kHz, 88.2 kHz
		PartitionedChunkSize = 4096
	default: // 44.1 kHz, 48 kHz
		PartitionedChunkSize = 4096
	}

	PartitionedFFTSize = PartitionedChunkSize * 2
	PartitionedHalfFFTSize = PartitionedFFTSize/2 + 1
	PartitionedImpulseChunkSize = PartitionedChunkSize
	PartitionedOverlapSize = PartitionedChunkSize
}

func SetPartitionSizes(ChunkSize int) {

	PartitionedChunkSize = ChunkSize
	PartitionedFFTSize = PartitionedChunkSize * 2
	PartitionedHalfFFTSize = PartitionedFFTSize/2 + 1
	PartitionedImpulseChunkSize = PartitionedChunkSize
	PartitionedOverlapSize = PartitionedChunkSize
}

// ─── Constructor ──────────────────────────────────────────────────────────────

// (SqueezeDSP fork) partitionSize is the partition for an impulse of taps at sampleRate. Fox's sizes
// (foxUtils.CalculateChunkSize) stay below 176.4k, outside streaming and for short impulses. At 176.4k and up a long
// impulse gets partitions doubled from his 8192 until it needs at most partitionsWanted of them, up to maxPartition:
// each output sample costs a complex multiply-add per partition, and a 1 s FIR at 705.6k needed 87, most of the
// convolver's time (BenchmarkPartition). The same convolution to rounding, about -300 dB
// (TestPartitionSizesSameConvolution). MaxGain is the whole filter's (peakGain), whatever the partitions.
// All channels' impulses have one length (CleanUpImpulse cuts them alike), so every channel gets the same size: the
// sizes are package-wide.
const (
	partitionsWanted = 16
	maxPartition     = 65536
)

func partitionSize(sampleRate, taps int, mode foxUtils.ProcessingMode) int {
	L := foxUtils.CalculateChunkSize(sampleRate, mode)
	if mode != foxUtils.ModeStreaming || sampleRate < 176400 {
		return L
	}
	for L < maxPartition && (taps+L-1)/L > partitionsWanted {
		L <<= 1
	}
	return L
}

func NewPartitionedConvolver(impulse []float64, sampleRate int) *PartitionedConvolver {
	if sampleRate > 0 {
		SetPartitionSizes(partitionSize(sampleRate, len(impulse), ProcessingMode))
	}

	pc := &PartitionedConvolver{
		FilterImpulse:     impulse,
		impulseLength:     len(impulse),
		Buffer:            make([]float64, 0, PartitionedChunkSize*2),
		outputBuffer:      make([]float64, 0, PartitionedChunkSize*4),
		previousChunkTail: make([]float64, PartitionedOverlapSize),
		// workResult is half the FFT size — only unique bins are stored
		workResult:   make([]complex128, PartitionedHalfFFTSize),
		workRealBuf:  make([]float64, PartitionedFFTSize),
		workOutput:   make([]float64, PartitionedChunkSize),
		outputLength: PartitionedChunkSize,
		ringFilled:   false,
		sampleRate:   sampleRate,
	}

	pc.debug(packageName + ": NewPartitionedConvolver - Creating New partition")
	pc.initializePartitions()
	return pc
}

func NextPowerOf2(n int) int {
	if n <= 0 {
		return 1
	}
	return 1 << bits.Len(uint(n-1))
}

// ─── Partition initialisation ─────────────────────────────────────────────────

func (pc *PartitionedConvolver) initializePartitions() {
	if pc.impulseLength == 0 {
		pc.numPartitions = 0
		pc.ringSize = 1
		pc.impulsePartitions = make([][]complex128, 0)
		pc.audioFFTRing = make([][]complex128, 1)
		// Empty-impulse ring slot still uses half-size
		pc.audioFFTRing[0] = make([]complex128, PartitionedHalfFFTSize)
		pc.ringPosition = 0

		if len(pc.previousChunkTail) == 0 {
			pc.previousChunkTail = make([]float64, PartitionedOverlapSize)
		}

		pc.debug("PartitionedConvolver initialized: empty impulse (passthrough)")
		return
	}

	pc.numPartitions = (pc.impulseLength + PartitionedImpulseChunkSize - 1) / PartitionedImpulseChunkSize
	pc.impulsePartitions = make([][]complex128, pc.numPartitions)

	// Reusable float64 buffer for building each impulse partition before
	// calling RealFftUnchecked. Allocated once here, zeroed per partition.
	impulseBuf := make([]float64, PartitionedFFTSize)

	for p := 0; p < pc.numPartitions; p++ {
		start := p * PartitionedImpulseChunkSize
		end := start + PartitionedImpulseChunkSize
		if end > pc.impulseLength {
			end = pc.impulseLength
		}

		// Zero the whole buffer then fill the active region.
		// RealFft requires a full-length real slice; unused samples must be 0.
		clear(impulseBuf)
		for i := start; i < end; i++ {
			impulseBuf[i-start] = pc.FilterImpulse[i]
		}

		// Each partition stores only the N/2+1 unique complex bins.
		partition := make([]complex128, PartitionedHalfFFTSize)
		fft.RealFftUnchecked(impulseBuf, partition)
		pc.impulsePartitions[p] = partition
	}

	// Ring buffer — each slot is N/2+1 complex values, not N.
	pc.ringSize = pc.numPartitions
	pc.audioFFTRing = make([][]complex128, pc.ringSize)
	for i := range pc.audioFFTRing {
		pc.audioFFTRing[i] = make([]complex128, PartitionedHalfFFTSize)
	}
	pc.ringPosition = 0

	pc.debug("PartitionedConvolver initialized: " +
		strconv.Itoa(pc.numPartitions) + " partitions, " +
		"ring size: " + strconv.Itoa(pc.ringSize))
	pc.debug(fmt.Sprintf("Chunk=%d, FFT=%d, HalfFFT=%d, ImpChunk=%d, Overlap=%d",
		PartitionedChunkSize, PartitionedFFTSize, PartitionedHalfFFTSize,
		PartitionedImpulseChunkSize, PartitionedOverlapSize))

	if len(pc.previousChunkTail) == 0 {
		pc.previousChunkTail = make([]float64, PartitionedOverlapSize)
	}

	pc.MaxGain = peakGain(pc.FilterImpulse) // (SqueezeDSP fork) the whole filter's; Fox's was one partition's (maxGainFromPartitions)
	if pc.sampleRate > 0 {
		pc.RMSGain = RMSGainInBand(pc.FilterImpulse, pc.sampleRate, 20, 20000)
	} else {
		pc.RMSGain = pc.rmsGainFromImpulse()
	}
}

// ─── Streaming initialisation ─────────────────────────────────────────────────

func (pc *PartitionedConvolver) InitForStreaming() {
	if len(pc.FilterImpulse) != pc.impulseLength {
		pc.impulseLength = len(pc.FilterImpulse)
		pc.initializePartitions()
	} else if len(pc.impulsePartitions) == 0 && len(pc.FilterImpulse) > 0 {
		pc.initializePartitions()
	}

	if pc.outputLength == 0 {
		pc.outputLength = PartitionedChunkSize
	}

	if pc.DebugOn {
		pc.debug(packageName + ": InitForStreaming - Impulse Length: " +
			strconv.Itoa(pc.impulseLength) + " Output Length: " +
			strconv.Itoa(pc.outputLength) + " Ring Position: " +
			strconv.Itoa(pc.ringPosition) + " Ring Filled: " +
			strconv.FormatBool(pc.ringFilled))
	}
}

// ─── Core hot path ────────────────────────────────────────────────────────────

func (pc *PartitionedConvolver) processChunk(chunk []float64, updateTail bool) []float64 {
	if pc.ringSize == 0 {
		return chunk
	}

	L := PartitionedChunkSize
	N := PartitionedFFTSize
	halfN := PartitionedHalfFFTSize // N/2+1
	overlapSize := L - 1

	// ── Build real input buffer ───────────────────────────────────────────
	// Write directly into workRealBuf as float64 — no complex conversion.
	realBuf := pc.workRealBuf
	copy(realBuf[:overlapSize], pc.previousChunkTail[:overlapSize])
	copy(realBuf[overlapSize:overlapSize+L], chunk[:L])
	realBuf[N-1] = 0 // only element neither copy above touches

	// ── Forward real FFT → N/2+1 unique bins ─────────────────────────────
	// Previously: filled []complex128 with complex(sample,0), ran N-point FFT.
	// Now: N/2-point complex FFT + O(N) post-processing butterfly.
	currentSlot := pc.audioFFTRing[pc.ringPosition]
	fft.RealFftUnchecked(realBuf, currentSlot)
	pc.audioFFTRing[pc.ringPosition] = currentSlot

	if updateTail {
		copy(pc.previousChunkTail, chunk[1:])
	}

	// ── Frequency-domain accumulation ────────────────────────────────────
	// Loop bound is halfN (4097 or 2049) instead of N (8192 or 4096).
	// ~half the complex multiplies per partition compared to the complex path.
	clear(pc.workResult)

	var maxPartitions int
	if pc.ringFilled {
		maxPartitions = pc.numPartitions
	} else {
		maxPartitions = pc.ringPosition + 1
		if maxPartitions > pc.numPartitions {
			maxPartitions = pc.numPartitions
		}
	}

	// (SqueezeDSP fork) the same sums in the same order, a partition at a time (multiplyAdd), the result's slice not
	// reloaded from pc at every bin (TestChunkBitsUnchanged).
	res := pc.workResult[:halfN]
	for p := 0; p < maxPartitions; p++ {
		audioIdx := (pc.ringPosition - p + pc.ringSize) % pc.ringSize
		multiplyAdd(res, pc.audioFFTRing[audioIdx], pc.impulsePartitions[p])
	}

	// ── Inverse real FFT → float64 output ────────────────────────────────
	// Previously: N-point complex IFFT, then real() extraction.
	// Now: N/2-point complex IFFT + O(N) pre-processing; output already float64.
	fft.RealIfftUnchecked(pc.workResult, realBuf)

	output := pc.workOutput
	copy(output[:L], realBuf[overlapSize:overlapSize+L]) // no real() conversion needed

	// Advance ring
	pc.ringPosition = (pc.ringPosition + 1) % pc.ringSize
	if !pc.ringFilled && pc.ringPosition == 0 {
		pc.ringFilled = true
	}

	return output
}

// (SqueezeDSP fork) multiplyAdd: res[i] += audioFFT[i] * impulseFFT[i] over res: two bins a step in AVX2 where the
// engine is built with it and the CPU has it (mac_avx2.go), else multiplyAddScalar; the same bits either way.
func multiplyAdd(res, audioFFT, impulseFFT []complex128) {
	if haveAVX2 {
		multiplyAddAVX2(res, audioFFT, impulseFFT)
		return
	}
	multiplyAddScalar(res, audioFFT, impulseFFT)
}

// (SqueezeDSP fork) AVX2: whether multiplyAdd runs in AVX2 here; the engine logs it at a stream's start, as the bits
// are the same either way.
func AVX2() bool { return haveAVX2 }

// multiplyAddScalar: four bins a step on local slices of one length, no bounds check per bin; the reference for
// multiplyAddAVX2 (TestMultiplyAddAVX2Bits).
func multiplyAddScalar(res, audioFFT, impulseFFT []complex128) {
	audioFFT, impulseFFT = audioFFT[:len(res)], impulseFFT[:len(res)]
	i := 0
	for ; i+4 <= len(res); i += 4 {
		r4, a4, h4 := res[i:i+4:i+4], audioFFT[i:i+4:i+4], impulseFFT[i:i+4:i+4]
		r4[0] += a4[0] * h4[0]
		r4[1] += a4[1] * h4[1]
		r4[2] += a4[2] * h4[2]
		r4[3] += a4[3] * h4[3]
	}
	for ; i < len(res); i++ {
		res[i] += audioFFT[i] * impulseFFT[i]
	}
}

// ─── Public convolution methods ───────────────────────────────────────────────
// These are unchanged from the original — all FFT changes are in processChunk.

func (pc *PartitionedConvolver) ConvolveChannel(inputSignalChannel, outputSignalChannel chan []float64) {
	functionName := "ConvolveChannel"
	NoConvolverMessage := false
	CanConvolve := len(pc.FilterImpulse) > 0

	if CanConvolve {
		pc.InitForStreaming()
		if pc.DebugOn {
			pc.debug(packageName + ": " + functionName + ": Partitioned convolution ready")
		}
	}

	for inputBlock := range inputSignalChannel {
		if !CanConvolve {
			if !NoConvolverMessage {
				pc.debug(packageName + ": " + functionName + ": Nothing to convolve with")
				NoConvolverMessage = true
			}
			pc.Meter.Add(len(inputBlock), 0) // passed along: no work
			outputSignalChannel <- inputBlock
			continue
		}

		t0 := time.Now()
		pc.Buffer = append(pc.Buffer, inputBlock...)

		for len(pc.Buffer) >= PartitionedChunkSize {
			chunk := pc.Buffer[:PartitionedChunkSize]
			processedOutput := pc.processChunk(chunk, true)
			pc.outputBuffer = append(pc.outputBuffer, processedOutput...)
			pc.Buffer = pc.Buffer[PartitionedChunkSize:]
		}
		pc.Meter.Add(len(inputBlock), time.Since(t0))
		pc.dropSent()

		for len(pc.outputBuffer) >= pc.outputLength {
			outputSignalChannel <- pc.outputBuffer[:pc.outputLength]
			pc.outputBuffer = pc.outputBuffer[pc.outputLength:]
		}

		if !NoConvolverMessage && pc.DebugOn {
			pc.debug(packageName + ": " + functionName + ": Partitioned convolution active")
			NoConvolverMessage = true
		}
	}

	// End of track — flush remaining input with zero-padding
	if len(pc.Buffer) > 0 {
		t0 := time.Now()
		remainingSamples := len(pc.Buffer)
		if pc.DebugOn {
			pc.debug(packageName + ": " + functionName +
				": Processing final " + strconv.Itoa(remainingSamples) + " samples with zero-padding")
		}
		paddedChunk := make([]float64, PartitionedChunkSize)
		copy(paddedChunk, pc.Buffer)
		// (SqueezeDSP fork) The padded partition gives this track's last outputs; the next track goes on from the last
		// whole one: the ring put back, this input kept for it (pending, EncodeState) and these outputs dropped there.
		// Fox's hand-over went on from the padding, with the tail of the partition before: the next track's first
		// outputs, as many as the filter's delay, replayed and then skipped what this track ended with, up to a
		// partition (TestGaplessJoin).
		pos, filled := pc.ringPosition, pc.ringFilled
		finalOutput := pc.processChunk(paddedChunk, false)
		pc.ringPosition, pc.ringFilled = pos, filled
		pc.pending = append(pc.pending[:0], pc.Buffer...)
		pc.outputBuffer = append(pc.outputBuffer, finalOutput[:remainingSamples]...)
		pc.Buffer = make([]float64, 0)
		pc.Meter.Add(0, time.Since(t0))
	}
	pc.dropSent()

	if len(pc.outputBuffer) > 0 {
		outputSignalChannel <- pc.outputBuffer
		if pc.DebugOn {
			pc.debug(packageName + ": " + functionName +
				": Flushed " + strconv.Itoa(len(pc.outputBuffer)) + " final output samples")
		}
		pc.outputBuffer = make([]float64, 0)
	}

	if pc.DebugOn {
		pc.debug(packageName + ": " + functionName + ": Track completed. State preserved for gapless continuation.")
	}
}

// dropSent (SqueezeDSP fork) drops the outputs the last track sent already (skip, from DecodeState).
func (pc *PartitionedConvolver) dropSent() {
	if pc.skip > 0 {
		n := min(pc.skip, len(pc.outputBuffer))
		pc.outputBuffer, pc.skip = pc.outputBuffer[n:], pc.skip-n
	}
}

func (pc *PartitionedConvolver) ConvolveOverlapSave(signalBlock []float64) []float64 {
	if len(pc.FilterImpulse) == 0 {
		return signalBlock
	}
	if len(pc.impulsePartitions) == 0 {
		pc.initializePartitions()
	}

	pc.Buffer = append(pc.Buffer, signalBlock...)
	output := make([]float64, 0, len(signalBlock))

	for len(pc.Buffer) >= PartitionedChunkSize {
		chunk := pc.Buffer[:PartitionedChunkSize]
		chunkOutput := pc.processChunk(chunk, true)
		output = append(output, chunkOutput...)
		pc.Buffer = pc.Buffer[PartitionedChunkSize:]
	}

	return output
}

func (pc *PartitionedConvolver) ConvolveFFT(signal []float64) []float64 {
	if len(pc.FilterImpulse) == 0 {
		return signal
	}

	fullLength := len(signal) + pc.impulseLength - 1
	pc.Reset()

	paddedSignal := make([]float64, fullLength)
	copy(paddedSignal, signal)

	result := pc.ConvolveOverlapSave(paddedSignal)

	if len(pc.Buffer) > 0 {
		paddedChunk := make([]float64, PartitionedChunkSize)
		copy(paddedChunk, pc.Buffer)
		finalOutput := pc.processChunk(paddedChunk, false)
		result = append(result, finalOutput...)
		pc.Buffer = make([]float64, 0)
	}

	if len(result) > fullLength {
		result = result[:fullLength]
	}
	return result
}

// ─── Gain utilities ───────────────────────────────────────────────────────────

// MaxGainFromFFT uses the real FFT path — only N/2+1 bins need scanning.
func MaxGainFromFFT(impulse []float64) float64 {
	n := len(impulse)
	if n == 0 {
		return 0.0
	}
	paddedSize := NextPowerOf2(n)
	realIn := make([]float64, paddedSize)
	halfOut := make([]complex128, paddedSize/2+1)
	copy(realIn, impulse)
	fft.RealFft(realIn, halfOut)

	// Conjugate symmetry means the maximum magnitude is in the unique half.
	maxGain := 0.0
	for _, v := range halfOut {
		if mag := cmplx.Abs(v); mag > maxGain {
			maxGain = mag
		}
	}
	return maxGain
}

// RMSGainInBand is the RMS of the impulse's magnitude response over lo..hi Hz (hi capped at the
// Nyquist frequency), on a grid of 2 Hz or finer. The impulse's L2 norm (rmsGainFromImpulse) is the
// same RMS over 0 Hz to Nyquist, so it fell as the rate rose: at 384 kHz seven eighths of that band
// is above 48 kHz, where a filter made at 96 kHz or below passes nothing. Over the audio band it
// does not depend on the rate. 1 for an empty or silent impulse, as rmsGainFromImpulse.
func RMSGainInBand(impulse []float64, sampleRate int, lo, hi float64) float64 {
	if len(impulse) == 0 || sampleRate <= 0 {
		return 1.0
	}
	hi = math.Min(hi, float64(sampleRate)/2)
	n := NextPowerOf2(max(len(impulse), sampleRate/2))
	realIn := make([]float64, n)
	halfOut := make([]complex128, n/2+1)
	copy(realIn, impulse)
	fft.RealFft(realIn, halfOut)

	sumSq := 0.0
	count := 0
	for k, v := range halfOut {
		if f := float64(k) * float64(sampleRate) / float64(n); f >= lo && f <= hi {
			mag := cmplx.Abs(v)
			sumSq += mag * mag
			count++
		}
	}
	if count == 0 || sumSq == 0 {
		return 1.0
	}
	return math.Sqrt(sumSq / float64(count))
}

func (pc *PartitionedConvolver) rmsGainFromImpulse() float64 {
	if len(pc.FilterImpulse) == 0 {
		return 1.0
	}
	sumSq := 0.0
	for _, v := range pc.FilterImpulse {
		sumSq += v * v
	}
	if sumSq == 0 {
		return 1.0
	}
	return math.Sqrt(sumSq)
}

func (pc *PartitionedConvolver) rmsGainFromFFTPartitions() float64 {
	if len(pc.impulsePartitions) == 0 {
		return 1.0
	}

	sumSq := 0.0
	count := 0

	for _, partition := range pc.impulsePartitions {
		for _, v := range partition {
			mag := cmplx.Abs(v)
			sumSq += mag * mag
			count++
		}
	}

	if count == 0 {
		return 1.0
	}

	return math.Sqrt(sumSq / float64(count))
}

// peakGain (SqueezeDSP fork) is the filter's peak gain, its largest |H(f)| over the whole band, which MaxGain is (the
// level's headroom: PrepareChannelGains). The impulse's real FFT, zero-padded to at least four times its length, so the
// narrowest peak the filter can have is never more than 0.22 dB under its nearest bin; then every local maximum within
// 0.3 dB of the largest bin refined by the parabola through it and its neighbours on a dB scale, the largest of those
// taken (never below a bin). Within 0.01 dB of a grid 16 points a tap fine, a narrow resonance halfway between two
// bins included (TestMaxGainBetweenBins: its bin 0.08 dB low). 0 for an empty impulse. Fox's MaxGain was the largest
// bin of any one partition's FFT (maxGainFromPartitions, kept below, no longer read): it changed with the partition
// size, read Simon's impulse up to 9% high (the level up to 0.76 dB low), and with FIR Strength under 100 at 176.4k
// and up about half the true peak, the output then up to about 5 dB over (ledger PER-PARTITION PEAK READS THE FILTER
// WRONG; Simon 2026-11-05: "okay we look to fix that in next round along with PCM upsampling work").
func peakGain(h []float64) float64 {
	if len(h) == 0 {
		return 0
	}
	n := NextPowerOf2(max(4*len(h), 1<<16))
	in := make([]float64, n)
	out := make([]complex128, n/2+1)
	copy(in, h)
	if err := fft.RealFft(in, out); err != nil {
		return 0
	}
	mag := make([]float64, len(out))
	top := 0.0
	for i, v := range out {
		mag[i] = cmplx.Abs(v)
		top = math.Max(top, mag[i])
	}
	peak := top
	near := top * math.Pow(10, -0.3/20)
	for k := 1; k < len(mag)-1; k++ {
		b := mag[k]
		if b < near || b < mag[k-1] || b < mag[k+1] || mag[k-1] <= 0 || mag[k+1] <= 0 {
			continue
		}
		a, c, y := math.Log(mag[k-1]), math.Log(mag[k+1]), math.Log(b)
		if d := a - 2*y + c; d < 0 {
			peak = math.Max(peak, math.Exp(y-0.125*(a-c)*(a-c)/d))
		}
	}
	return peak
}

func (pc *PartitionedConvolver) maxGainFromPartitions() float64 {
	maxGain := 0.0
	for _, partition := range pc.impulsePartitions {
		for _, v := range partition {
			if mag := cmplx.Abs(v); mag > maxGain {
				maxGain = mag
			}
		}
	}
	return maxGain
}

// ─── Serialisation ────────────────────────────────────────────────────────────
//
// GobEncode/GobDecode persist the filter configuration (impulse partitions,
// size globals) but NOT the audio stream state, which is managed separately
// via EncodeState/DecodeState.
//
// Note: impulsePartitions now store []complex128 of length PartitionedHalfFFTSize
// (N/2+1) rather than N. The size globals encode this implicitly — a decoder
// that restores PartitionedHalfFFTSize before allocating ring buffers will
// produce correctly-sized slots.

// filterCacheVersion (SqueezeDSP fork) leads each convolver in the filter cache (filter_<client>_<rate>.gob), so a cache
// an earlier engine wrote fails to decode and the filter is built again (LoadConvolvers' error), as after a change of
// settings: the cache is otherwise taken whenever it is newer than the settings, and it holds MaxGain. -2: MaxGain is the
// whole filter's peak (peakGain), 0.1.72.37. Negative, so a cache from before it, which leads with PartitionedChunkSize,
// never reads as one.
const filterCacheVersion = -2

func (pc *PartitionedConvolver) GobEncode() ([]byte, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)

	for _, v := range []any{
		filterCacheVersion,
		PartitionedChunkSize,
		PartitionedFFTSize,
		PartitionedHalfFFTSize, // NEW — must be encoded so decoder can size buffers
		PartitionedImpulseChunkSize,
		PartitionedOverlapSize,
		pc.FilterImpulse,
		pc.impulseLength,
		pc.outputLength,
		pc.numPartitions,
		pc.ringSize,
		pc.impulsePartitions, // each slice is now length N/2+1
		pc.MaxGain,
		pc.RMSGain,
	} {
		if err := enc.Encode(v); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

func (pc *PartitionedConvolver) GobDecode(data []byte) error {
	buf := bytes.NewBuffer(data)
	dec := gob.NewDecoder(buf)

	var version int
	if err := dec.Decode(&version); err != nil {
		return err
	}
	if version != filterCacheVersion {
		return fmt.Errorf("filter cache version %d, want %d: built again", version, filterCacheVersion)
	}
	var chunkSize, fftSize, halfFFTSize, impulseChunkSize, overlapSize int
	for _, p := range []any{
		&chunkSize, &fftSize, &halfFFTSize, &impulseChunkSize, &overlapSize,
	} {
		if err := dec.Decode(p); err != nil {
			return err
		}
	}
	PartitionedChunkSize = chunkSize
	PartitionedFFTSize = fftSize
	PartitionedHalfFFTSize = halfFFTSize
	PartitionedImpulseChunkSize = impulseChunkSize
	PartitionedOverlapSize = overlapSize

	for _, p := range []any{
		&pc.FilterImpulse,
		&pc.impulseLength,
		&pc.outputLength,
		&pc.numPartitions,
		&pc.ringSize,
		&pc.impulsePartitions,
		&pc.MaxGain,
		&pc.RMSGain,
	} {
		if err := dec.Decode(p); err != nil {
			return err
		}
	}

	// Re-initialise transient buffers at the restored sizes.
	pc.Buffer = make([]float64, 0)
	pc.outputBuffer = make([]float64, 0)
	pc.pending, pc.skip = nil, 0

	pc.audioFFTRing = make([][]complex128, pc.ringSize)
	for i := range pc.audioFFTRing {
		pc.audioFFTRing[i] = make([]complex128, PartitionedHalfFFTSize) // N/2+1
	}
	pc.ringPosition = 0
	pc.ringFilled = false

	pc.previousChunkTail = make([]float64, PartitionedOverlapSize)
	pc.workOutput = make([]float64, PartitionedChunkSize)
	pc.workResult = make([]complex128, PartitionedHalfFFTSize) // N/2+1
	pc.workRealBuf = make([]float64, PartitionedFFTSize)
	return nil
}

// EncodeState / DecodeState persist the audio stream state for gapless playback.
// The ring buffer slots are now length PartitionedHalfFFTSize; everything else
// is unchanged.

func (pc *PartitionedConvolver) EncodeState() ([]byte, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)

	for _, v := range []any{
		pc.audioFFTRing,
		pc.ringPosition,
		pc.ringFilled,
		pc.previousChunkTail,
		append([]float64{}, pc.pending...), // (SqueezeDSP fork) the input short of a partition; CACHE_VERSION 6
	} {
		if err := enc.Encode(v); err != nil {
			return nil, err
		}
	}

	pc.debug("saving Ring Position: " + strconv.Itoa(pc.ringPosition) +
		" Ring Filled: " + strconv.FormatBool(pc.ringFilled))
	return buf.Bytes(), nil
}

func (pc *PartitionedConvolver) DecodeState(data []byte) error {
	buf := bytes.NewBuffer(data)
	dec := gob.NewDecoder(buf)

	var pending []float64
	for _, p := range []any{
		&pc.audioFFTRing,
		&pc.ringPosition,
		&pc.ringFilled,
		&pc.previousChunkTail,
		&pending,
	} {
		if err := dec.Decode(p); err != nil {
			return err
		}
	}

	// (SqueezeDSP fork) this track starts from the last one's input short of a partition, its outputs sent already
	pc.Buffer = append(make([]float64, 0, PartitionedChunkSize*2), pending...)
	pc.outputBuffer = make([]float64, 0)
	pc.pending, pc.skip = nil, len(pending)
	return nil
}

// ScaleState (SqueezeDSP fork, 2026-11-20, a gain change at a gapless join): multiplies all the convolver holds of the
// input so far (its spectra, the overlap tail, the input short of a partition: DecodeState's) by f. The engine's gain
// comes after the convolver, so the next track's engine scales the last track's held input by the last track's gain
// over its own, and the last track's decay plays at the last track's gain (Simon: "a playlist needs to carry track
// gain"). All of it is linear in the input, so this is exact.
func (pc *PartitionedConvolver) ScaleState(f float64) {
	c := complex(f, 0)
	for _, slot := range pc.audioFFTRing {
		for i := range slot {
			slot[i] *= c
		}
	}
	for i := range pc.previousChunkTail {
		pc.previousChunkTail[i] *= f
	}
	for i := range pc.Buffer {
		pc.Buffer[i] *= f
	}
}

// ─── State management ─────────────────────────────────────────────────────────

func (pc *PartitionedConvolver) Reset() {
	for i := range pc.audioFFTRing {
		clear(pc.audioFFTRing[i])
	}
	pc.ringPosition = 0
	pc.Buffer = make([]float64, 0)
	pc.outputBuffer = make([]float64, 0)
	pc.pending, pc.skip = nil, 0
	pc.previousChunkTail = make([]float64, PartitionedOverlapSize)

	if pc.DebugOn {
		pc.debug("PartitionedConvolver reset")
	}
}

func (pc *PartitionedConvolver) AmendFilterImpulse(impulse []float64) {
	pc.FilterImpulse = impulse
	pc.impulseLength = len(impulse)
	pc.initializePartitions()
	pc.Reset()

	if pc.DebugOn {
		pc.debug("FilterImpulse amended - " + strconv.Itoa(len(impulse)) + " samples")
	}
}

// ─── Compatibility accessors ──────────────────────────────────────────────────

func (pc *PartitionedConvolver) GetPaddedLength() int { return PartitionedFFTSize }

func (pc *PartitionedConvolver) GetTail() []float64 {
	tail := make([]float64, len(pc.previousChunkTail))
	copy(tail, pc.previousChunkTail)
	return tail
}

func (pc *PartitionedConvolver) SetTail(tail []float64) {
	if len(tail) == PartitionedOverlapSize {
		copy(pc.previousChunkTail, tail)
	}
}

func (pc *PartitionedConvolver) GetInputBuffer() []float64 {
	buffer := make([]float64, len(pc.Buffer))
	copy(buffer, pc.Buffer)
	return buffer
}

func (pc *PartitionedConvolver) GetOutputBuffer() []float64 {
	buffer := make([]float64, len(pc.outputBuffer))
	copy(buffer, pc.outputBuffer)
	return buffer
}

// ─── Debug helpers ────────────────────────────────────────────────────────────

func (pc *PartitionedConvolver) debug(message string) {
	if pc.DebugOn {
		if pc.DebugFunc != nil {
			pc.DebugFunc(message)
		} else {
			println(message)
		}
	}
}

func (pc *PartitionedConvolver) warning(message string) {
	if pc.WarningFunc != nil {
		pc.WarningFunc(message)
	} else {
		println("WARNING: " + message)
	}
}
