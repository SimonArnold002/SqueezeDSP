package foxConvolverAdapt

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"math"
	"math/bits"
	"math/cmplx"
	"strconv"
	"sync"
	"time"

	foxFFT "github.com/Foxenfurter/foxAudioLib/foxFFT"
	HOR "github.com/Foxenfurter/foxAudioLib/foxFFTHeartofRust"
	"scientificgo.org/fft"
)

const packageName = "foxConvolverAdapt"
const defaultFFTProviderName = "foxfft_adapter_real" // Default to the partitioned FoxFFT adapter for better performance in streaming convolution

// FFTProvider defines the interface for different FFT implementations
type FFTProvider interface {
	Transform(input []complex128, inverse bool) []complex128
}

// ScientificGoAdapter implements FFTProvider using scientificgo.org/fft
type ScientificGoAdapter struct{}

func (a *ScientificGoAdapter) Transform(input []complex128, inverse bool) []complex128 {
	return fft.Fft(input, inverse)
}

type FoxFFTAdapter struct{}

func (a *FoxFFTAdapter) Transform(input []complex128, inverse bool) []complex128 {
	// Create a copy since your FFT works in-place
	result := make([]complex128, len(input))
	copy(result, input)

	foxFFT.Fft(result, inverse)
	return result
}

// ─── FoxFFTRealAdapter ────────────────────────────────────────────────────────

// FoxFFTRealAdapter implements FFTProvider using foxFFT's real-to-complex path.
//
// The Transform interface is []complex128 → []complex128, so this adapter
// bridges the real FFT API while remaining a drop-in for the other adapters:
//
//	Forward:
//	  1. Extracts the real parts of the input (imaginary parts must be zero
//	     for the result to be correct — always true for audio convolver input).
//	  2. Calls RealFftUnchecked → produces N/2+1 unique complex bins.
//	  3. Mirrors the conjugate half so the returned slice is length N,
//	     identical in shape to what FoxFFTAdapterReal returns.
//
//	Inverse:
//	  1. Takes the first N/2+1 bins (the rest are redundant conjugate mirrors).
//	  2. Calls RealIfftUnchecked → produces N real samples.
//	  3. Packs them into []complex128 with zero imaginary parts.
//
// The round-trip is numerically equivalent to a full complex FFT on real input
// but runs ~1.85× faster because the inner transform is N/2 points.
//
// Usage — register alongside the other adapters:
//
//	case "foxfft_real":
//	    provider = &FoxFFTRealAdapter{}
type FoxFFTRealAdapter struct {
	realPool sync.Pool // pools []float64 work buffers of length N
	halfPool sync.Pool // pools []complex128 half-spectrum buffers of length N/2+1
}

func (a *FoxFFTRealAdapter) Transform(input []complex128, inverse bool) []complex128 {
	N := len(input)
	halfN := N/2 + 1

	if inverse {
		// ── Inverse ──────────────────────────────────────────────────────
		realBufPtr := a.getRealBuf(N)
		realBuf := *realBufPtr

		// Only the first N/2+1 bins are unique; the rest are conjugate mirrors.
		foxFFT.RealIfftUnchecked(input[:halfN], realBuf)

		// Pack real output back into []complex128 for interface compatibility.
		out := make([]complex128, N)
		for i := 0; i < N; i++ {
			out[i] = complex(realBuf[i], 0)
		}

		a.putRealBuf(realBufPtr)
		return out
	}

	// ── Forward ───────────────────────────────────────────────────────────
	// Extract real parts — input imaginary parts must be zero (audio signal).
	realBufPtr := a.getRealBuf(N)
	realBuf := *realBufPtr
	for i := 0; i < N; i++ {
		realBuf[i] = real(input[i])
	}

	halfBufPtr := a.getHalfBuf(halfN)
	halfBuf := *halfBufPtr

	foxFFT.RealFftUnchecked(realBuf, halfBuf)

	// Mirror conjugate half so the returned slice is length N, matching every
	// other adapter's output shape.  X[N-k] = conj(X[k]) for real input.
	out := make([]complex128, N)
	copy(out, halfBuf[:halfN])
	for k := 1; k < N/2; k++ {
		out[N-k] = complex(real(out[k]), -imag(out[k]))
	}

	a.putRealBuf(realBufPtr)
	a.putHalfBuf(halfBufPtr)
	return out
}

// getRealBuf returns a pooled []float64 of at least length N.
func (a *FoxFFTRealAdapter) getRealBuf(N int) *[]float64 {
	if p := a.realPool.Get(); p != nil {
		buf := p.(*[]float64)
		if len(*buf) >= N {
			return buf
		}
	}
	buf := make([]float64, N)
	return &buf
}

func (a *FoxFFTRealAdapter) putRealBuf(p *[]float64) { a.realPool.Put(p) }

// getHalfBuf returns a pooled []complex128 of at least length halfN.
func (a *FoxFFTRealAdapter) getHalfBuf(halfN int) *[]complex128 {
	if p := a.halfPool.Get(); p != nil {
		buf := p.(*[]complex128)
		if len(*buf) >= halfN {
			return buf
		}
	}
	buf := make([]complex128, halfN)
	return &buf
}

func (a *FoxFFTRealAdapter) putHalfBuf(p *[]complex128) { a.halfPool.Put(p) }

// FoxFFTAdapter implements FFTProvider using the internal Go implementation
type FoxFFTAdapter_internal struct{}

func (a *FoxFFTAdapter_internal) Transform(input []complex128, inverse bool) []complex128 {
	// Create a copy since your FFT works in-place
	result := make([]complex128, len(input))
	copy(result, input)

	if inverse {

		//ifft128Optimized(result)
		ifft128(result)
		/*
			if err := InvCompute(result); err != nil {
				fmt.Printf("IFFT error: %v", err)
			}*/
	} else {

		//fft128CacheOptimized(result)
		fft128enhanced(result)
		/*
			if err := Compute(result); err != nil {
				fmt.Printf("FFT error: %v", err)
			}*/
	}
	return result
}

// FoxFFTHORAdapter implements FFTProvider using internal Tukey implementation
type FoxFFTHORAdapter struct{}

func (a *FoxFFTHORAdapter) Transform(input []complex128, inverse bool) []complex128 {
	if inverse {
		return HOR.Fft(input, true)
	}
	return HOR.Fft(input, false)
}

// Convolver represents a convolver structure
type Convolver struct {
	FilterImpulse     []float64
	impulseLength     int
	signalBlockLength int
	overlapLength     int
	paddedLength      int
	outputLength      int
	impulseFFT        []complex128
	overlapTail       []float64
	Buffer            []float64
	overlappedBuffer  []float64
	outputBuffer      []float64
	complexBuffer     []complex128
	fftBuffer         []complex128

	// New field for FFT provider
	fftProvider     FFTProvider
	fftProviderName string

	DebugOn     bool
	DebugFunc   func(string)
	WarningFunc func(string)
}

// Function to handle debug calls, allowing for different logging implementations
func (myConvolver *Convolver) debug(message string) {
	if myConvolver.DebugOn {
		if myConvolver.DebugFunc != nil {
			myConvolver.DebugFunc(message)
		} else {
			println(message)
		}
	}
}

func (myConvolver *Convolver) warning(message string) {
	if myConvolver.WarningFunc != nil {
		myConvolver.WarningFunc(message)
	}
}

func (myConvolver *Convolver) GetPaddedLength() int {
	return myConvolver.paddedLength
}

func (myConvolver *Convolver) GetTail() []float64 {
	return myConvolver.overlapTail
}

func (myConvolver *Convolver) SetTail(tail []float64) {
	myConvolver.overlapTail = tail
}

func (myConvolver *Convolver) SetSignalBlockLength(signalBlockLength int) {
	myConvolver.signalBlockLength = signalBlockLength
}

// NewConvolver initializes the convolver structure
func NewConvolver(impulse []float64) Convolver {
	return Convolver{
		FilterImpulse:   impulse,
		fftProvider:     &ScientificGoAdapter{},
		fftProviderName: defaultFFTProviderName,
	}
}

// Option to set custom FFT provider
func (myConvolver *Convolver) setFFTProvider(provider FFTProvider) {
	if provider == nil {
		myConvolver.warning("FFT provider is nil - using default FFT provider")
		provider = &ScientificGoAdapter{}
	}

	// Recompute impulse FFT only if provider has changed
	if myConvolver.fftProvider != provider {
		myConvolver.fftProvider = provider
		myConvolver.recomputeImpulseFFT()
	}
}

// SetFFTProviderByString sets the FFT provider based on a string identifier
func (myConvolver *Convolver) SetFFTProviderByString(provider string) {
	switch provider {
	case "foxfft_adapter":
		myConvolver.setFFTProvider(&FoxFFTAdapter{})
		myConvolver.fftProviderName = "foxfft_adapter"
		if myConvolver.DebugOn {
			myConvolver.debug("Using partitioned FoxFFT provider")
		}
	case "foxfft_adapter_real":
		myConvolver.setFFTProvider(&FoxFFTRealAdapter{})
		myConvolver.fftProviderName = "foxfft_adapter_real"
		if myConvolver.DebugOn {
			myConvolver.debug("Using partitioned FoxFFT real-input adapter")
		}

	case "foxfft_internal":
		myConvolver.setFFTProvider(&FoxFFTAdapter_internal{})
		myConvolver.fftProviderName = "foxfft_internal"
		if myConvolver.DebugOn {
			myConvolver.debug("Using internal FoxFFT provider")
		}
	case "foxfft_HOR":
		myConvolver.setFFTProvider(&FoxFFTHORAdapter{})
		myConvolver.fftProviderName = "foxfft"
		if myConvolver.DebugOn {
			myConvolver.debug("Using FoxFFT (Tukey) FFT provider")
		}
	case "scientificgo":
		fallthrough
	default:
		myConvolver.fftProviderName = "scientificgo"
		myConvolver.setFFTProvider(&ScientificGoAdapter{})
		if myConvolver.DebugOn {
			myConvolver.debug("Using ScientificGo FFT provider")
		}
	}
}

func (myConvolver *Convolver) recomputeImpulseFFT() {
	if myConvolver.paddedLength == 0 {
		return
	}
	paddedFilterImpulse := make([]complex128, myConvolver.paddedLength)
	for i := 0; i < myConvolver.impulseLength; i++ {
		paddedFilterImpulse[i] = complex(myConvolver.FilterImpulse[i], 0)
	}
	for i := myConvolver.impulseLength; i < len(paddedFilterImpulse); i++ {
		paddedFilterImpulse[i] = complex(0, 0)
	}
	myConvolver.impulseFFT = myConvolver.fftProvider.Transform(paddedFilterImpulse, false)
}

// AmendFilterImpulse allows the impulse to be changed
func (myConvolver *Convolver) AmendFilterImpulse(impulse []float64) {
	myConvolver.FilterImpulse = impulse
}

// convertFloat64ToComplex128 converts an array of float64 to complex128 using input as the real numbers
func convertFloat64ToComplex128(data []float64) []complex128 {
	converted := make([]complex128, len(data))
	for i := range data {
		converted[i] = complex(data[i], 0)
	}
	return converted
}

// convertComplex128ToFloat64 converts an array of complex128 back to float64 extracting real numbers
func convertComplex128ToFloat64(data []complex128) []float64 {
	converted := make([]float64, len(data))
	for i, r := range data {
		converted[i] = real(r)
	}
	return converted
}

func NextPowerOf2(n int) int {
	if n <= 0 {
		return 1
	}
	return 1 << bits.Len(uint(n-1))
}

// GobEncode implements the gob.GobEncoder interface for convolver to cache data
func (c Convolver) GobEncode() ([]byte, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)

	// Encode ONLY the fields you want to persist
	if err := enc.Encode(c.FilterImpulse); err != nil {
		return nil, err
	}
	if err := enc.Encode(c.impulseFFT); err != nil {
		return nil, err
	}
	if err := enc.Encode(c.impulseLength); err != nil {
		return nil, err
	}
	if err := enc.Encode(c.signalBlockLength); err != nil {
		return nil, err
	}
	if err := enc.Encode(c.overlapLength); err != nil {
		return nil, err
	}
	if err := enc.Encode(c.paddedLength); err != nil {
		return nil, err
	}
	if err := enc.Encode(c.outputLength); err != nil {
		return nil, err
	}
	// Add any other static fields you want to save
	if err := enc.Encode(c.fftProviderName); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// GobDecode implements the gob.GobDecoder interface for convolver to read cached data
func (c *Convolver) GobDecode(data []byte) error {
	buf := bytes.NewBuffer(data)
	dec := gob.NewDecoder(buf)

	// Decode in the same order
	if err := dec.Decode(&c.FilterImpulse); err != nil {
		return err
	}
	if err := dec.Decode(&c.impulseFFT); err != nil {
		return err
	}
	if err := dec.Decode(&c.impulseLength); err != nil {
		return err
	}
	if err := dec.Decode(&c.signalBlockLength); err != nil {
		return err
	}
	if err := dec.Decode(&c.overlapLength); err != nil {
		return err
	}
	if err := dec.Decode(&c.paddedLength); err != nil {
		return err
	}
	if err := dec.Decode(&c.outputLength); err != nil {
		return err
	}
	// Add any other static fields you want to load
	if err := dec.Decode(&c.fftProviderName); err != nil {
		return err
	}

	// Reinitialize the provider object from its name
	c.SetFFTProviderByString(c.fftProviderName)

	return nil
}

// ConvolveOverlapSave using Overlap and save method. Allows for signal to be sent in blocks
func (myConvolver *Convolver) ConvolveOverlapSave(signalBlock []float64) []float64 {
	if len(myConvolver.FilterImpulse) == 0 {
		return signalBlock
	}
	// Reinitialize the FFT provider based on the saved name
	if myConvolver.fftProvider == nil {
		if myConvolver.fftProviderName == "" {
			myConvolver.SetFFTProviderByString(defaultFFTProviderName)
		} else {
			myConvolver.SetFFTProviderByString(myConvolver.fftProviderName)
		}
	}

	// Check to see if any inputs to the calculation have changed and recalulate if necessary
	if len(myConvolver.FilterImpulse) != myConvolver.impulseLength || len(signalBlock) != myConvolver.signalBlockLength {
		myConvolver.signalBlockLength = len(signalBlock)
		myConvolver.InitForStreaming()
	}

	overlappedSignal := append(myConvolver.overlapTail, signalBlock...)

	// make sure that the signal is the same length as the padded filter.
	if myConvolver.paddedLength > len(overlappedSignal) {
		signalPadding := make([]float64, myConvolver.paddedLength-len(overlappedSignal))
		overlappedSignal = append(overlappedSignal, signalPadding...)
	}
	// now create next tail - it needs to be done pre-convolution - ignore any padding
	start := myConvolver.signalBlockLength
	end := start + myConvolver.overlapLength

	myConvolver.overlapTail = overlappedSignal[start:end]

	if len(overlappedSignal) != len(myConvolver.impulseFFT) {
		if myConvolver.DebugOn {
			myConvolver.debug("signal length: " + strconv.Itoa(len(overlappedSignal)) + " Impulse: " + strconv.Itoa(len(myConvolver.impulseFFT)))
		}
		return signalBlock
	}
	// now do the convolution
	output := myConvolver.convolve(convertFloat64ToComplex128(overlappedSignal))
	start = myConvolver.overlapLength
	end = start + myConvolver.signalBlockLength

	return output[start:end]
}

// MaxGainFromFFT computes maximum magnitude in frequency domain
func MaxGainFromFFTOld(impulse []float64) float64 {

	n := len(impulse)
	if n == 0 {
		println("MaxGainFromFFT: Empty impulse, returning 0.0")
		return 0.0
	}
	paddedSize := NextPowerOf2(n)

	// Zero-pad to power of 2

	cdata := make([]complex128, paddedSize)

	for i := range impulse {
		cdata[i] = complex(impulse[i], 0)
	}

	// Use default provider or pass one as parameter
	defaultProvider := &ScientificGoAdapter{}

	cdata = defaultProvider.Transform(cdata, false)

	maxGain := 0.0
	for _, v := range cdata {
		mag := cmplx.Abs(v)
		if mag > maxGain {
			maxGain = mag
		}
	}
	println("MaxGainFromFFT: Empty impulse, returning maxGain: ", maxGain)
	return maxGain
}

func (myConvolver *Convolver) MaxGainFromFFT(impulse []float64) float64 {

	if myConvolver.fftProvider == nil {
		if myConvolver.fftProviderName == "" {
			myConvolver.SetFFTProviderByString(defaultFFTProviderName)
		} else {
			myConvolver.SetFFTProviderByString(myConvolver.fftProviderName)
		}
	}
	n := len(impulse)
	// next power of 2 for FFT
	fftlength := NextPowerOf2(n)
	fmt.Printf("length of Impulse %v : ", n)
	cdata := make([]complex128, fftlength)
	maxGain := 0.0
	for i := range impulse {
		cdata[i] = complex(impulse[i], 0)
	}
	fmt.Printf("length of cdata complex %v : ", len(cdata))
	// Use default provider or pass one as parameter

	cdata = myConvolver.fftProvider.Transform(cdata, false)
	fmt.Printf("length of cdata complex fft %v : ", len(cdata))

	for _, v := range cdata {
		mag := cmplx.Abs(v)
		if mag > maxGain {
			maxGain = mag
		}
	}
	fmt.Printf("Max gain from FFT: %f\n", maxGain)
	return maxGain
}

// InitForStreaming Initialise Convolver for Streaming convolution
func (myConvolver *Convolver) InitForStreaming() {
	// Check to see if any inputs to the calculation have changed and recalulate if necessary
	if myConvolver.fftProvider == nil {
		if myConvolver.fftProviderName == "" {
			myConvolver.SetFFTProviderByString(defaultFFTProviderName)
		} else {
			myConvolver.SetFFTProviderByString(myConvolver.fftProviderName)
		}
	}
	myConvolver.impulseLength = len(myConvolver.FilterImpulse)
	if myConvolver.impulseLength == 0 {
		return
	}
	myConvolver.overlapLength = myConvolver.impulseLength - 1
	myConvolver.outputLength = myConvolver.signalBlockLength
	myConvolver.paddedLength = NextPowerOf2(myConvolver.impulseLength + myConvolver.outputLength - 1)

	paddedFilterImpulse := make([]complex128, myConvolver.paddedLength)
	// copy impulse into the padded impulse
	for i := 0; i < myConvolver.impulseLength; i++ {
		paddedFilterImpulse[i] = complex(myConvolver.FilterImpulse[i], 0)
	}
	// Fill remainder of padded impulse array with zeros
	for i := myConvolver.impulseLength; i < len(paddedFilterImpulse); i++ {
		paddedFilterImpulse[i] = complex(0, 0)
	}
	myConvolver.impulseFFT = myConvolver.fftProvider.Transform(paddedFilterImpulse, false)

	// Check because the overlaptail may have been loaded from a tail file
	if len(myConvolver.overlapTail) == 0 {
		myConvolver.overlapTail = make([]float64, myConvolver.overlapLength)
	}
	if myConvolver.DebugOn {
		myConvolver.debug(packageName + ": InitForStreaming Impulse Length: " + strconv.Itoa(myConvolver.impulseLength) + " Overlap Length: " + strconv.Itoa(myConvolver.overlapLength) + " Tail Length: " + strconv.Itoa(len(myConvolver.overlapTail)))
	}
}

// initNormal Initialise Convolver for normal FFT type convolution
func (myConvolver *Convolver) initNormal(ImpulseLength int, SignalBlockLength int) {
	// Check to see if any inputs to the calculation have changed and recalulate if necessary
	if ImpulseLength == 0 {
		// Houston we have a problem
		return
	}
	if myConvolver.fftProvider == nil {
		if myConvolver.fftProviderName == "" {
			myConvolver.SetFFTProviderByString(defaultFFTProviderName)
		} else {
			myConvolver.SetFFTProviderByString(myConvolver.fftProviderName)
		}
	}
	myConvolver.impulseLength = ImpulseLength
	myConvolver.signalBlockLength = SignalBlockLength
	myConvolver.paddedLength = NextPowerOf2(ImpulseLength + SignalBlockLength - 1)
	myConvolver.outputLength = myConvolver.signalBlockLength

	myConvolver.overlappedBuffer = make([]float64, myConvolver.paddedLength)
	myConvolver.complexBuffer = make([]complex128, myConvolver.paddedLength)
	myConvolver.fftBuffer = make([]complex128, myConvolver.paddedLength)

	paddedFilterImpulse := make([]complex128, myConvolver.paddedLength)
	// copy impulse into the padded impulse
	for i := 0; i < myConvolver.impulseLength; i++ {
		paddedFilterImpulse[i] = complex(myConvolver.FilterImpulse[i], 0)
	}
	// Fill remainder of padded impulse array with zeros
	for i := myConvolver.impulseLength; i < len(paddedFilterImpulse); i++ {
		paddedFilterImpulse[i] = complex(0, 0)
	}
	myConvolver.impulseFFT = myConvolver.fftProvider.Transform(paddedFilterImpulse, false)
}

func (myConvolver *Convolver) convolve(signal []complex128) []float64 {
	signal = myConvolver.fftProvider.Transform(signal, false)
	for i := 0; i < len(signal); i++ {
		signal[i] *= myConvolver.impulseFFT[i]
	}
	signal = myConvolver.fftProvider.Transform(signal, true)
	return convertComplex128ToFloat64(signal)
}

// ConvolveChannel optimized for go channels
func (myConvolver *Convolver) ConvolveChannel(inputSignalChannel, outputSignalChannel chan []float64) {
	functionName := "ConvolveChannel"
	NoConvolverMessage := false
	CanConvolve := len(myConvolver.FilterImpulse) > 0
	targetSignalLength := myConvolver.outputLength

	if CanConvolve && myConvolver.DebugOn {
		myConvolver.debug(packageName + ": " + functionName + ": targetSignalLength: " + strconv.Itoa(targetSignalLength) + " Tail Length: " + strconv.Itoa(len(myConvolver.overlapTail)))
	}

	for inputBlock := range inputSignalChannel {
		if !CanConvolve {
			// Nothing to convolve with so just hand on the input - only log once
			if !NoConvolverMessage {
				myConvolver.debug(packageName + ": " + functionName + ": Nothing to convolve with")
				NoConvolverMessage = true
			}
			outputSignalChannel <- inputBlock
			continue
		} else {
			// The convolver has a buffer for accumulating and managing the signal
			myConvolver.Buffer = append(myConvolver.Buffer, inputBlock...)
			for len(myConvolver.Buffer) >= targetSignalLength {
				outputSignalChannel <- myConvolver.ConvolveOverlapSave(myConvolver.Buffer[:targetSignalLength])
				if !NoConvolverMessage && myConvolver.DebugOn {
					myConvolver.debug(packageName + ": " + functionName + ": Channel Convolving" + " Tail Length: " + strconv.Itoa(len(myConvolver.overlapTail)))
					NoConvolverMessage = true
				}
				myConvolver.Buffer = myConvolver.Buffer[targetSignalLength:]
			}
		}
	}

	if myConvolver.DebugOn {
		myConvolver.debug(packageName + ": " + functionName + ": Channel Convolver completed - Remaining Data in Buffer: " + strconv.Itoa(len(myConvolver.Buffer)) + " Tail Length: " + strconv.Itoa(len(myConvolver.overlapTail)))
	}
}

// ConvolveFFT use convolver structure with precomputed Impulse values
func (myConvolver *Convolver) ConvolveFFT(signal []float64) []float64 {
	if len(myConvolver.FilterImpulse) == 0 {
		return signal
	}
	signalLength := len(signal)

	myConvolver.initNormal(len(myConvolver.FilterImpulse), signalLength)

	paddedImpulse := make([]complex128, myConvolver.paddedLength)
	paddedSignal := make([]complex128, myConvolver.paddedLength)
	validLength := signalLength + myConvolver.impulseLength - 1
	for i := 0; i < myConvolver.impulseLength; i++ {
		paddedImpulse[i] = complex(myConvolver.FilterImpulse[i], 0)
	}

	for i := 0; i < signalLength; i++ {
		paddedSignal[i] = complex(signal[i], 0)
	}

	output := myConvolver.convolve(paddedSignal)

	// now truncate output back to original signal length
	return output[:validLength]
}

// TukeyFFT simple Cooley-Tukey based FFT
func TukeyFFT(X []complex128) []complex128 {
	N := len(X)
	if N <= 1 {
		return X
	}

	// Divide
	even := make([]complex128, N/2)
	odd := make([]complex128, N/2)
	for i := 0; i < N/2; i++ {
		even[i] = X[i*2]
		odd[i] = X[i*2+1]
	}

	// Conquer
	T := TukeyFFT(even)
	U := TukeyFFT(odd)
	x := make([]complex128, N)
	// Combine
	for i := 0; i < N/2; i++ {
		t := cmplx.Rect(1, -2*math.Pi*float64(i)/float64(N)) * U[i]
		x[i] = T[i] + t
		x[i+N/2] = T[i] - t
	}

	return x
}

// InverseTukeyFFT simple Cooley-Tukey based inverse FFT
func InverseTukeyFFT(X []complex128) []complex128 {
	N := len(X)
	x := make([]complex128, N)

	// Take the conjugate of the input sequence
	for i := 0; i < N; i++ {
		x[i] = cmplx.Conj(X[i])
	}

	// Compute the FFT of the conjugated sequence
	x = TukeyFFT(x)

	// Take the conjugate of the result and normalize
	for i := 0; i < N; i++ {
		x[i] = cmplx.Conj(x[i]) / complex(float64(N), 0)
	}

	return x
}

// CompareAllFFTs benchmarks different FFT implementations
func CompareAllFFTs(data []complex128, iterations int) {
	providers := map[string]FFTProvider{
		"ScientificGo": &ScientificGoAdapter{},
		"FoxFFT_Int":   &FoxFFTAdapter_internal{},
		"FoxFFT_Real":  &FoxFFTRealAdapter{},
		"FoxFFT_HOR":   &FoxFFTHORAdapter{},
		"FoxFFT_Lib":   &FoxFFTAdapter{},
	}

	fmt.Printf("Benchmarking %d iterations with data size %d\n", iterations, len(data))
	for name, provider := range providers {
		start := time.Now()
		for i := 0; i < iterations; i++ {
			_ = provider.Transform(data, false)
		}
		elapsed := time.Since(start)
		fmt.Printf("%-15s: %v total (%v per op)\n", name, elapsed, elapsed/time.Duration(iterations))
	}
}

// FFT package functions
// ===========================================
// 1. PRE-COMPUTED BIT-REVERSAL TABLES
// ===========================================
var bitRevCache = make(map[int][]int)
var bitRevLock sync.RWMutex

func getBitReversal(N int) []int {
	bitRevLock.RLock()
	if rev, ok := bitRevCache[N]; ok {
		bitRevLock.RUnlock()
		return rev
	}
	bitRevLock.RUnlock()

	bitRevLock.Lock()
	defer bitRevLock.Unlock()

	// Double check after acquiring write lock
	if rev, ok := bitRevCache[N]; ok {
		return rev
	}

	rev := make([]int, N)
	shift := 64 - uint64(bits.Len64(uint64(N-1)))
	for i := 0; i < N; i++ {
		rev[i] = int(bits.Reverse64(uint64(i)) >> shift)
	}
	bitRevCache[N] = rev
	return rev
}

func permuteEnhanced(x []complex128) {
	N := len(x)
	if N <= 8 {
		// Your original small cases
		switch N {
		case 1, 2:
			return
		case 4:
			x[1], x[2] = x[2], x[1]
			return
		case 8:
			x[1], x[4] = x[4], x[1]
			x[3], x[6] = x[6], x[3]
			return
		}
		return
	}

	rev := getBitReversal(N)
	for i := 0; i < N; i++ {
		j := rev[i]
		if i < j {
			x[i], x[j] = x[j], x[i]
		}
	}
}

// ===========================================
// 2. CACHE-OPTIMIZED FFT WITH BLOCK PROCESSING
// ===========================================
// Cache line size in bytes (64 bytes = 8 complex128)
const cacheLineComplex = 8

var twiddleCache = make(map[int][]complex128)
var twiddleCacheLock sync.RWMutex

func getTwiddles(N int) []complex128 {
	twiddleCacheLock.RLock()
	if twiddles, ok := twiddleCache[N]; ok {
		twiddleCacheLock.RUnlock()
		return twiddles
	}
	twiddleCacheLock.RUnlock()

	twiddleCacheLock.Lock()
	defer twiddleCacheLock.Unlock()

	if twiddles, ok := twiddleCache[N]; ok {
		return twiddles
	}

	twiddles := make([]complex128, N)
	// Use math.Sincos - it's measurably faster than separate Cos/Sin
	for k := 0; k < N; k++ {
		angle := -2 * math.Pi * float64(k) / float64(N)
		sin, cos := math.Sincos(angle) // Single function call
		twiddles[k] = complex(cos, sin)
	}

	twiddleCache[N] = twiddles
	return twiddles
}

func fft128CacheOptimized(x []complex128) {
	N := len(x)
	if N <= 4 {
		// Your existing small cases
		switch N {
		case 1:
			return
		case 2:
			x[0], x[1] = x[0]+x[1], x[0]-x[1]
			return
		case 4:
			f := complex(imag(x[1])-imag(x[3]), real(x[3])-real(x[1]))
			x[0], x[1], x[2], x[3] = x[0]+x[1]+x[2]+x[3], x[0]-x[2]+f, x[0]-x[1]+x[2]-x[3], x[0]-x[2]-f
			return
		}
	}

	// Use enhanced permutation
	permuteEnhanced(x)

	// First 2 steps (your original code)
	for i := 0; i < N; i += 4 {
		f := complex(imag(x[i+2])-imag(x[i+3]), real(x[i+3])-real(x[i+2]))
		x[i], x[i+1], x[i+2], x[i+3] = x[i]+x[i+1]+x[i+2]+x[i+3], x[i]-x[i+1]+f, x[i]-x[i+2]+x[i+1]-x[i+3], x[i]-x[i+1]-f
	}

	// Get twiddles (now uses math.Sincos)
	twiddles := getTwiddles(N)

	// Main loop (your original, proven fast code)
	for n := 4; n < N; n <<= 1 {
		m := n << 1
		step := N / m
		for o := 0; o < N; o += m {
			twiddleIdx := 0
			for k := 0; k < n; k++ {
				i := k + o
				//f := twiddles[k*step] * x[i+n]
				if k+8 < n {
					_ = x[i+8+n] // Hint to compiler/CPU
				}

				f := twiddles[twiddleIdx] * x[i+n]
				x[i], x[i+n] = x[i]+f, x[i]-f
				twiddleIdx += step
			}
		}
	}
}

// ===========================================
// 3. OPTIMIZED INVERSE FFT WITH COMBINED OPS
// ===========================================
func ifft128Optimized(x []complex128) {
	N := len(x)

	// Handle small cases
	switch N {
	case 1:
		return
	case 2:
		x[0], x[1] = x[0]+x[1], x[0]-x[1]
		x[0] /= 2
		x[1] /= 2
		return
	case 4:
		// Specialized 4-point IFFT
		a, b, c, d := x[0], x[1], x[2], x[3]
		x[0] = (a + b + c + d) / 4
		x[1] = (a - complex(0, 1)*b - c + complex(0, 1)*d) / 4
		x[2] = (a - b + c - d) / 4
		x[3] = (a + complex(0, 1)*b - c - complex(0, 1)*d) / 4
		return
	}

	// Combined reverse and conjugate in one pass
	/*

			j := N - i
			xi, xj := x[i], x[j]
			x[i] = complex(real(xj), -imag(xj))
			x[j] = complex(real(xi), -imag(xi))
		}

		// Handle middle element if N is even
		if N%2 == 0 {
			mid := N / 2
			x[mid] = complex(real(x[mid]), -imag(x[mid]))
		}
	*/
	for i := 1; i < N/2; i++ {
		j := N - i
		x[i], x[j] = x[j], x[i]
	}

	// Forward FFT using your enhanced version
	fft128CacheOptimized(x)

	// Scale
	invN := 1.0 / float64(N)
	for i := range x {
		x[i] = complex(real(x[i])*invN, imag(x[i])*invN)
	}
}

//===========================

func IsPow2(N int) bool {
	if N == 0 {
		return false
	}
	return (uint64(N) & uint64(N-1)) == 0
}

func checkLength(Context string, N int) error {
	if !IsPow2(N) {
		return fmt.Errorf("%s must be a power of 2, got %d", Context, N)
	}
	return nil
}

func Compute(x []complex128) error {
	if err := checkLength("FFT Input", len(x)); err != nil {
		return err
	}
	fft128enhanced(x)
	return nil
}

func InvCompute(x []complex128) error {
	if err := checkLength("IFFT Input", len(x)); err != nil {
		return err
	}
	ifft128(x)
	return nil
}

func getTwiddlesOld(N int) []complex128 {
	twiddleCacheLock.RLock()
	if twiddles, ok := twiddleCache[N]; ok {
		twiddleCacheLock.RUnlock()
		return twiddles
	}
	twiddleCacheLock.RUnlock()

	twiddleCacheLock.Lock()
	defer twiddleCacheLock.Unlock()

	// Double check
	if twiddles, ok := twiddleCache[N]; ok {
		return twiddles
	}

	twiddles := make([]complex128, N)
	for k := 0; k < N; k++ {
		angle := -2 * math.Pi * float64(k) / float64(N)
		twiddles[k] = complex(math.Cos(angle), math.Sin(angle))
	}
	twiddleCache[N] = twiddles
	return twiddles
}

// Instead of w = cmplx.Sqrt(w) in loop
// Pre-compute all needed roots of unity

func fft128enhanced(x []complex128) {
	N := len(x)
	if N <= 4 {
		// ... existing small cases
	}

	permute(x)

	// First 2 steps
	for i := 0; i < N; i += 4 {
		f := complex(imag(x[i+2])-imag(x[i+3]), real(x[i+3])-real(x[i+2]))
		x[i], x[i+1], x[i+2], x[i+3] = x[i]+x[i+1]+x[i+2]+x[i+3], x[i]-x[i+1]+f, x[i]-x[i+2]+x[i+1]-x[i+3], x[i]-x[i+1]-f
	}

	twiddles := getTwiddles(N)

	for n := 4; n < N; n <<= 1 {
		m := n << 1
		step := N / m
		for o := 0; o < N; o += m {
			for k := 0; k < n; k++ {
				i := k + o
				f := twiddles[k*step] * x[i+n]
				x[i], x[i+n] = x[i]+f, x[i]-f
			}
		}
	}
}

// fft does the actual work for FFT
func fft128(x []complex128) {
	N := len(x)
	// Handle small N quickly
	switch N {
	case 1:
		return
	case 2:
		x[0], x[1] = x[0]+x[1], x[0]-x[1]
		return
	case 4:
		f := complex(imag(x[1])-imag(x[3]), real(x[3])-real(x[1]))
		x[0], x[1], x[2], x[3] = x[0]+x[1]+x[2]+x[3], x[0]-x[2]+f, x[0]-x[1]+x[2]-x[3], x[0]-x[2]-f
		return
	}
	// Reorder the input array.
	permute(x)
	// Butterfly
	// First 2 steps
	for i := 0; i < N; i += 4 {
		f := complex(imag(x[i+2])-imag(x[i+3]), real(x[i+3])-real(x[i+2]))
		x[i], x[i+1], x[i+2], x[i+3] = x[i]+x[i+1]+x[i+2]+x[i+3], x[i]-x[i+1]+f, x[i]-x[i+2]+x[i+1]-x[i+3], x[i]-x[i+1]-f
	}
	// Remaining steps
	w := complex(0, -1)
	for n := 4; n < N; n <<= 1 {
		w = cmplx.Sqrt(w)
		for o := 0; o < N; o += (n << 1) {
			wj := complex(1, 0)
			for k := 0; k < n; k++ {
				i := k + o
				f := wj * x[i+n]
				x[i], x[i+n] = x[i]+f, x[i]-f
				wj *= w
			}
		}
	}
}

func ifft128(x []complex128) {
	N := len(x)
	// Reverse using XOR swap (slightly faster, no temp needed)
	for i := 1; i < N/2; i++ {
		j := N - i
		x[i], x[j] = x[j], x[i]
	}

	fft128enhanced(x)

	// Scale - compiler will optimize division by constant better
	invN := 1.0 / float64(N)
	for i := range x {
		x[i] = complex(real(x[i])*invN, imag(x[i])*invN)
	}
}

// permute permutes the input vector using bit reversal
func permute(x []complex128) {
	N := len(x)
	// Handle small N quickly
	switch N {
	case 1, 2:
		return
	case 4:
		x[1], x[2] = x[2], x[1]
		return
	case 8:
		x[1], x[4] = x[4], x[1]
		x[3], x[6] = x[6], x[3]
		return
	}
	shift := 64 - uint64(bits.Len64(uint64(N-1)))
	N2 := N >> 1
	for i := 0; i < N; i += 2 {
		ind := int(bits.Reverse64(uint64(i)) >> shift)
		// Skip cases where low bit isn't set while high bit is
		// This eliminates 25% of iterations
		if i < N2 {
			if ind > i {
				x[i], x[ind] = x[ind], x[i]
			}
		}
		ind |= N2 // Fast way to get int(bits.Reverse64(uint64(i+1)) >> shift) here
		if ind > i+1 {
			x[i+1], x[ind] = x[ind], x[i+1]
		}
	}
}
