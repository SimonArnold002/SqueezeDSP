package foxConvolverPartition

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"math"
	"math/bits"
	"math/cmplx"
	"strconv"

	fft "github.com/Foxenfurter/foxAudioLib/foxFFT"
	"github.com/Foxenfurter/foxAudioLib/foxUtils"
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

func NewPartitionedConvolver(impulse []float64, sampleRate int) *PartitionedConvolver {
	if sampleRate > 0 {
		chunkSize := foxUtils.CalculateChunkSize(sampleRate, ProcessingMode)
		SetPartitionSizes(chunkSize) // uncomment to auto-size
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

	pc.MaxGain = pc.maxGainFromPartitions()
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
	for i := 0; i < overlapSize; i++ {
		realBuf[i] = pc.previousChunkTail[i]
	}
	for i := 0; i < L; i++ {
		realBuf[overlapSize+i] = chunk[i]
	}
	realBuf[N-1] = 0 // only element neither loop above touches

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

	for p := 0; p < maxPartitions; p++ {
		audioIdx := (pc.ringPosition - p + pc.ringSize) % pc.ringSize
		audioFFT := pc.audioFFTRing[audioIdx]
		impulseFFT := pc.impulsePartitions[p]

		i := 0
		for i < halfN-3 {
			pc.workResult[i] += audioFFT[i] * impulseFFT[i]
			pc.workResult[i+1] += audioFFT[i+1] * impulseFFT[i+1]
			pc.workResult[i+2] += audioFFT[i+2] * impulseFFT[i+2]
			pc.workResult[i+3] += audioFFT[i+3] * impulseFFT[i+3]
			i += 4
		}
		for ; i < halfN; i++ {
			pc.workResult[i] += audioFFT[i] * impulseFFT[i]
		}
	}

	// ── Inverse real FFT → float64 output ────────────────────────────────
	// Previously: N-point complex IFFT, then real() extraction.
	// Now: N/2-point complex IFFT + O(N) pre-processing; output already float64.
	fft.RealIfftUnchecked(pc.workResult, realBuf)

	output := pc.workOutput
	for i := 0; i < L; i++ {
		output[i] = realBuf[overlapSize+i] // no real() conversion needed
	}

	// Advance ring
	pc.ringPosition = (pc.ringPosition + 1) % pc.ringSize
	if !pc.ringFilled && pc.ringPosition == 0 {
		pc.ringFilled = true
	}

	return output
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
			outputSignalChannel <- inputBlock
			continue
		}

		pc.Buffer = append(pc.Buffer, inputBlock...)

		for len(pc.Buffer) >= PartitionedChunkSize {
			chunk := pc.Buffer[:PartitionedChunkSize]
			processedOutput := pc.processChunk(chunk, true)
			pc.outputBuffer = append(pc.outputBuffer, processedOutput...)
			pc.Buffer = pc.Buffer[PartitionedChunkSize:]
		}

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
		remainingSamples := len(pc.Buffer)
		if pc.DebugOn {
			pc.debug(packageName + ": " + functionName +
				": Processing final " + strconv.Itoa(remainingSamples) + " samples with zero-padding")
		}
		paddedChunk := make([]float64, PartitionedChunkSize)
		copy(paddedChunk, pc.Buffer)
		finalOutput := pc.processChunk(paddedChunk, false)
		pc.outputBuffer = append(pc.outputBuffer, finalOutput[:remainingSamples]...)
		pc.Buffer = make([]float64, 0)
	}

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

func (pc *PartitionedConvolver) GobEncode() ([]byte, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)

	for _, v := range []any{
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

	for _, p := range []any{
		&pc.audioFFTRing,
		&pc.ringPosition,
		&pc.ringFilled,
		&pc.previousChunkTail,
	} {
		if err := dec.Decode(p); err != nil {
			return err
		}
	}

	pc.Buffer = make([]float64, 0)
	pc.outputBuffer = make([]float64, 0)
	return nil
}

// ─── State management ─────────────────────────────────────────────────────────

func (pc *PartitionedConvolver) Reset() {
	for i := range pc.audioFFTRing {
		clear(pc.audioFFTRing[i])
	}
	pc.ringPosition = 0
	pc.Buffer = make([]float64, 0)
	pc.outputBuffer = make([]float64, 0)
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
