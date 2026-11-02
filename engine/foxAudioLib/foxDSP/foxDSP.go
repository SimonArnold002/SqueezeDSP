package foxDSP

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io/ioutil"
	"math"
	"os"

	"scientificgo.org/fft"
)

const FilterLength = 4000

func hammingWindow(length int) []float64 {
	window := make([]float64, length)
	alpha := 0.54
	beta := 0.46
	for i := 0; i < length; i++ {
		window[i] = alpha - beta*math.Cos(2*math.Pi*float64(i)/float64(length-1))
	}
	return window
}

func ConvolveImpulsesFFTwithTail(impulse, signal []float64, overlapSize int, tail []float64) ([]float64, []float64) {
	impulseLength := len(impulse)
	signalLength := len(signal)
	outputLength := impulseLength + signalLength - 1

	// Pad input arrays to the nearest power of 2
	paddedLength := int(math.Pow(2, math.Ceil(math.Log2(float64(impulseLength+signalLength-1)))))
	paddedImpulse := make([]complex128, paddedLength)
	paddedSignal := make([]complex128, paddedLength)

	// Apply Hamming window to impulse and signal
	hammingWindowImpulse := hammingWindow(impulseLength)
	hammingWindowSignal := hammingWindow(signalLength)

	// Map impulse and signal to padded arrays with windowing
	for i := 0; i < impulseLength; i++ {
		paddedImpulse[i] = complex(impulse[i]*hammingWindowImpulse[i], 0)
	}
	for i := 0; i < signalLength; i++ {
		paddedSignal[i] = complex(signal[i]*hammingWindowSignal[i], 0)
	}

	// Combine tail and signal into a combined block
	combinedBlock := make([]float64, len(tail)+signalLength)
	copy(combinedBlock, tail)
	copy(combinedBlock[len(tail):], signal)

	// Pad combined block to the padded length
	paddedCombinedBlock := make([]complex128, paddedLength)
	for i := 0; i < len(combinedBlock); i++ {
		paddedCombinedBlock[i] = complex(combinedBlock[i], 0)
	}

	// Apply FFT to impulse and combined block
	fft.Fft(paddedImpulse, false)
	fft.Fft(paddedCombinedBlock, false)

	// Multiply the transformed arrays element-wise
	multipliedResult := make([]complex128, paddedLength)
	for i := 0; i < paddedLength; i++ {
		multipliedResult[i] = paddedImpulse[i] * paddedCombinedBlock[i]
	}

	// Apply inverse FFT to the multiplied result
	fft.Fft(multipliedResult, true)

	// Extract convolved output excluding overlap region
	convolvedOutput := make([]float64, outputLength)
	for i := overlapSize; i < outputLength+overlapSize; i++ {
		convolvedOutput[i-overlapSize] = real(multipliedResult[i])
	}

	// Update tail for next iteration
	tail = make([]float64, overlapSize)
	for i := 0; i < overlapSize; i++ {
		tail[i] = real(multipliedResult[i+outputLength])
	}

	return convolvedOutput, tail
}

func ConvolveImpulsesFFT(impulse, signal []float64) []float64 {
	impulseLength := len(impulse)
	signalLength := len(signal)
	outputLength := impulseLength + signalLength - 1
	result := make([]float64, outputLength)

	// Pad input arrays to the nearest power of 2
	paddedLength := int(math.Pow(2, math.Ceil(math.Log2(float64(impulseLength+signalLength-1)))))
	paddedImpulse := make([]complex128, paddedLength)
	paddedSignal := make([]complex128, paddedLength)

	// Apply Hamming window to impulse and signal
	hammingWindowImpulse := hammingWindow(impulseLength)
	hammingWindowSignal := hammingWindow(signalLength)

	// Map impulse and signal over the length of the padded array with applied window
	for i := 0; i < impulseLength; i++ {
		paddedImpulse[i] = complex(impulse[i]*hammingWindowImpulse[i], 0)
	}

	for i := 0; i < signalLength; i++ {
		paddedSignal[i] = complex(signal[i]*hammingWindowSignal[i], 0)
	}

	// Apply FFT to both padded input arrays
	fft.Fft(paddedImpulse, false)
	fft.Fft(paddedSignal, false)

	// Multiply the transformed arrays element-wise
	multipliedResult := make([]complex128, paddedLength)
	for i := 0; i < paddedLength; i++ {
		multipliedResult[i] = paddedImpulse[i] * paddedSignal[i]
	}

	// Apply inverse FFT to the multiplied result
	fft.Fft(multipliedResult, true)

	// Extract the real part and assign it to the result array
	for i := 0; i < outputLength; i++ {
		result[i] = real(multipliedResult[i])
	}

	return result
}

func IIRFilter(input []float64, b [3]float64, a [3]float64, output []float64) {
	N := len(input)
	M := len(b)
	//L := len(a)
	y := make([]float64, N)
	w := make([]float64, M)

	for n := 0; n < N; n++ {
		y[n] = b[0]*input[n] + w[0]
		for i := 1; i < M; i++ {
			w[i-1] = b[i]*input[n] + w[i] - a[i]*y[n]
		}
		output[n] = y[n]
	}
}

func CascadeFilters(coefficients [][]float64, myFilterLength int) []float64 {
	//results are more consistent with REW if filters are cascaded before generating an impulse response rather than merging a set of impulses
	maxLength := len(coefficients)
	if maxLength == 0 {
		fmt.Printf("No coeeficients: ")
		return nil
	}
	if myFilterLength == 0 {
		myFilterLength = FilterLength
	}
	var a, b [3]float64
	impulse := make([]float64, myFilterLength)
	signal := make([]float64, myFilterLength)

	impulse[0] = 1

	for i := 0; i < maxLength; i++ {
		a[0] = coefficients[i][0]
		a[1] = coefficients[i][1]
		a[2] = coefficients[i][2]
		b[0] = coefficients[i][3]
		b[1] = coefficients[i][4]
		b[2] = coefficients[i][5]

		IIRFilter(impulse, b, a, signal)

		impulse = make([]float64, len(signal))
		copy(impulse, signal)
	}

	return signal
}

func GenerateImpulseResponse(a0, a1, a2, b0, b1, b2 float64) []float64 {
	a := [3]float64{a0, a1, a2}
	b := [3]float64{b0, b1, b2}

	// Create an array of zeros of length 'FilterLength'
	impulse := make([]float64, FilterLength)

	// Set the first element of the impulse array to 1
	impulse[0] = 1

	// Create a signal array of length 'FilterLength'
	signal := make([]float64, FilterLength)

	// Apply the filter coefficients to the impulse array using the IIRFilter function
	IIRFilter(impulse, b, a, signal)

	return signal
}

func CalcBiquadFilter(filterType string, centreFrequency, sampleRate int, peakGain, width float64, slopeType string) []float64 {
	// Nyquist frequency approximation
	Nyquist := 0.445

	if centreFrequency < 10 || centreFrequency > 25000 {
		panic(fmt.Sprintf("frequency should be between 10 and 25000, got: %d", centreFrequency))
	}
	if sampleRate < 10000 || sampleRate > 400000 {
		panic(fmt.Sprintf("sampleRate should be a recognized sample rate, got: %d", sampleRate))
	}
	if peakGain < -30 || peakGain > 20 {
		panic(fmt.Sprintf("peakGain should be between -30 and +20, got: %f", peakGain))
	}

	// Adjust frequency if it exceeds Nyquist frequency
	if float64(centreFrequency)/float64(sampleRate) > Nyquist {
		// Need to add in Error Logger here.
		centreFrequency = int(float64(sampleRate) * Nyquist)
	}

	b0, b1, b2, a0, a1, a2, norm := 1.0, 0.0, 0.0, 1.0, 0.0, 0.0, 0.0

	ampl := math.Pow(10, math.Abs(peakGain)/40)
	if peakGain < 0 {
		ampl = 1 / ampl
	}

	SQRTA := math.Sqrt(ampl)
	var alpha, beta float64

	if filterType == "lowpass" || filterType == "highcut" {
		// Correct for the low pass filter rolling off too early
		desiredLevel := -1.0
		x := math.Pow(10, desiredLevel/20.0)
		desiredStartingPoint := float64(centreFrequency) + float64(centreFrequency) - (float64(centreFrequency) * x)

		centreFrequency = int(desiredStartingPoint)
		filterType = "lowpass"
	}

	if filterType == "highpass" || filterType == "lowcut" {
		filterType = "highpass"
		desiredLevel := 2.0
		desiredStartingPoint := float64(centreFrequency) * math.Sqrt(math.Pow(10, desiredLevel/10.0)-1)
		centreFrequency = int(desiredStartingPoint)
	}

	omega := 2 * math.Pi * float64(centreFrequency) / float64(sampleRate)
	coso := math.Cos(omega)
	sino := math.Sin(omega)

	switch slopeType {
	case "slope":
		alpha = sino / 2 * math.Sqrt((ampl+1/ampl)*(1/width-1)+2)
	case "Q":
		alpha = sino / (2 * width)
	case "octave":
		alpha = sino * math.Sinh(math.Log(2)/2*width*omega/sino)
	default:
		panic(fmt.Sprintf("slopeType %s was not recognized [slope, Q, octave]", slopeType))
	}

	switch filterType {
	case "lowpass":
		norm = 1 / (1.0 + alpha)

		b0 = norm * ((1.0 - coso) / 2.0)
		b1 = norm * (1.0 - coso)
		b2 = norm * ((1.0 - coso) / 2.0)

		a0 = 1.0
		a1 = norm * (-2.0 * coso)
		a2 = norm * (1.0 - alpha)

	case "highpass":
		norm = 1 / (1.0 + alpha)

		b0 = norm * ((1.0 + coso) / 2.0)
		b1 = norm * (-(1.0 + coso))
		b2 = b0

		a0 = 1.0
		a1 = norm * (-2.0 * coso)
		a2 = norm * (1.0 - alpha)

	case "bandpass":
		norm = 1 / (1.0 + alpha)

		b0 = norm * (alpha)
		b1 = 0.0
		b2 = norm * (-alpha)

		a0 = 1.0
		a1 = norm * (-2.0 * coso)
		a2 = norm * (1.0 - alpha)

	case "notch":
		norm = 1 / (1.0 + alpha)

		b0 = norm
		b1 = norm * -2.0 * coso
		b2 = b0

		a0 = 1.0
		a1 = norm * -2.0 * coso
		a2 = norm * (1.0 - alpha)

	case "peak":
		norm = 1 / (1 + alpha/ampl)

		b0 = norm * (1 + alpha*ampl)
		b1 = norm * (-2 * coso)
		b2 = norm * (1 - alpha*ampl)

		a0 = 1
		a1 = norm * (-2 * coso)
		a2 = norm * (1 - alpha/ampl)

	case "lowshelf":
		beta = 2.0 * SQRTA * alpha
		norm = 1 / (ampl + 1 + (ampl-1)*coso + 2*SQRTA*alpha)

		b0 = norm * (ampl * (ampl + 1 - (ampl-1)*coso + beta))
		b1 = norm * (2 * ampl * (ampl - 1 - (ampl+1)*coso))
		b2 = norm * (ampl * (ampl + 1 - (ampl-1)*coso - beta))

		a0 = 1
		a1 = norm * (-2 * (ampl - 1 + (ampl+1)*coso))
		a2 = norm * (ampl + 1 + (ampl-1)*coso - beta)

	case "highshelf":
		norm = 1 / ((ampl + 1) - (ampl-1)*coso + 2*SQRTA*alpha)
		b0 = norm * (ampl * ((ampl + 1) + (ampl-1)*coso + 2*SQRTA*alpha))
		b1 = norm * (-2 * ampl * ((ampl - 1) + (ampl+1)*coso))
		b2 = norm * (ampl * ((ampl + 1) + (ampl-1)*coso - 2*SQRTA*alpha))
		a0 = 1.0
		a1 = norm * (2 * ((ampl - 1) - (ampl+1)*coso))
		a2 = norm * ((ampl + 1) - (ampl-1)*coso - 2*SQRTA*alpha)

	default:
		panic(fmt.Sprintf("filterType %s was not set to a recognized filter type", filterType))
	}

	myCoefficients := []float64{a0, a1, a2, b0, b1, b2}
	return myCoefficients
}

//===================Stream Manager ==========================

// StreamManager is the equivalent of the C# StreamManager struct
type StreamManager struct {
	InputImpulse [][]float64
}

// ExportWavFile exports the WAV file using the CreateMemoryStream function
func ExportWavFile(filename string, sampleRate, bitDepth int, sm *StreamManager) error {
	// Create memory stream
	wavData, err := sm.CreateMemoryStream(sampleRate, bitDepth)
	if err != nil {
		return err
	}

	// Write the WAV data to a file
	//err = ioutil.WriteFile(filename, wavData, 0666)
	err = ioutil.WriteFile(filename, wavData.Bytes(), 0666)
	if err != nil {
		return err
	}

	fmt.Printf("WAV file successfully exported to %s\n", filename)
	return nil
}

// CreateMemoryStream is the equivalent of the C# CreateMemoryStream method
func (sm *StreamManager) CreateMemoryStream(sampleRate, bitDepth int) (*bytes.Buffer, error) {
	if bitDepth != 16 && bitDepth != 24 && bitDepth != 32 {
		return nil, errors.New("bit depth should be 16, 24, or 32 bits")
	}

	numSamples := len(sm.InputImpulse[0])
	numChannels := len(sm.InputImpulse)

	for channel := 0; channel < numChannels; channel++ {
		if numSamples != len(sm.InputImpulse[channel]) {
			return nil, errors.New("all channel arrays must have the same length")
		}
	}

	// Calculate the total data size in bytes
	dataSize := numSamples * numChannels * (bitDepth / 8)

	// Create a new bytes.Buffer to hold the WAV stream

	buffer := new(bytes.Buffer)
	// Write the RIFF header
	buffer.WriteString("RIFF")
	binary.Write(buffer, binary.LittleEndian, int32(dataSize+36)) // Total file size - 36 bytes for the header
	buffer.WriteString("WAVE")

	// Write the format chunk
	buffer.WriteString("fmt ")
	binary.Write(buffer, binary.LittleEndian, int32(16))                                  // Size of the format chunk
	binary.Write(buffer, binary.LittleEndian, int16(1))                                   // Audio format (PCM)
	binary.Write(buffer, binary.LittleEndian, int16(numChannels))                         // Number of channels
	binary.Write(buffer, binary.LittleEndian, int32(sampleRate))                          // Sample rate
	binary.Write(buffer, binary.LittleEndian, int32(sampleRate*numChannels*(bitDepth/8))) // Byte rate
	binary.Write(buffer, binary.LittleEndian, int16(numChannels*(bitDepth/8)))            // Block align
	binary.Write(buffer, binary.LittleEndian, int16(bitDepth))                            // Bits per sample

	// Write the data chunk header
	buffer.WriteString("data")
	binary.Write(buffer, binary.LittleEndian, int32(dataSize))

	for i := 0; i < numSamples; i++ {
		for channel := 0; channel < numChannels; channel++ {
			sample := math.Max(-1.0, math.Min(1.0, sm.InputImpulse[channel][i]))
			var sampleBytes []byte

			switch bitDepth {
			case 16:
				sampleBytes = convertTo16BitSample(sample)
			case 24:
				sampleBytes = convertTo24BitSample(sample)
			case 32:
				sampleBytes = convertTo32BitSample(sample)
			}

			buffer.Write(sampleBytes)
		}
	}

	return buffer, nil
}

func convertTo16BitSample(sample float64) []byte {
	const bitDepth = 16
	maxValue := (1 << (bitDepth - 1)) - 1
	minValue := -maxValue

	// Scale the sample to the range of 16-bit signed integers
	scaledValue := sample * float64(maxValue)

	// Round to the nearest integer
	roundedValue := int(math.Round(scaledValue))

	// Clip the value to the valid range
	clippedValue := int(math.Max(float64(minValue), math.Min(float64(maxValue), float64(roundedValue))))

	// Convert the clipped value to a byte array in little-endian format
	return []byte{
		byte(clippedValue & 0xFF),
		byte((clippedValue >> 8) & 0xFF),
	}
}

func convertTo24BitSample(sample float64) []byte {
	const bitDepth = 24
	maxValue := (1 << (bitDepth - 1)) - 1
	minValue := -maxValue

	// Scale the sample to the range of 24-bit signed integers
	scaledValue := sample * float64(maxValue)

	// Round to the nearest integer
	roundedValue := int(math.Round(scaledValue))

	// Clip the value to the valid range
	clippedValue := int(math.Max(float64(minValue), math.Min(float64(maxValue), float64(roundedValue))))

	// Convert the clipped value to a byte array in little-endian format
	return []byte{
		byte(clippedValue & 0xFF),
		byte((clippedValue >> 8) & 0xFF),
		byte((clippedValue >> 16) & 0xFF),
	}
}

func convertTo32BitSample(sample float64) []byte {
	const bitDepth = 32
	maxValue := (1 << (bitDepth - 1)) - 1
	minValue := -maxValue

	// Scale the sample to the range of 32-bit signed integers
	scaledValue := sample * float64(maxValue)

	// Round to the nearest integer
	roundedValue := int64(math.Round(scaledValue))

	// Clip the value to the valid range
	clippedValue := int64(math.Max(float64(minValue), math.Min(float64(maxValue), float64(roundedValue))))

	// Convert the clipped value to a byte array in little-endian format
	return []byte{
		byte(clippedValue & 0xFF),
		byte((clippedValue >> 8) & 0xFF),
		byte((clippedValue >> 16) & 0xFF),
		byte((clippedValue >> 24) & 0xFF),
	}
}

// Calculates the Maximum Gain Value - used for Normalization functions
func CalculateMaxGain(audioData []float64) float64 {
	maxGain := 0.0
	if audioData == nil {
		return maxGain
	}
	for _, sample := range audioData {
		sampleAbs := math.Abs(sample)
		if sampleAbs > maxGain {
			maxGain = sampleAbs
		}
	}
	return maxGain
}

func NormalizeAudioImpulse(audioImpulse []float64, targetLevel float64, max float64) []float64 {
	// Check for divide by zero and no normalization needed
	if max == 0.0 || max == targetLevel {
		return audioImpulse
	}

	// Calculate the normalization factor
	normalizationFactor := targetLevel / max

	// Normalize the audio impulse
	normalizedImpulse := make([]float64, len(audioImpulse))
	for i := range audioImpulse {
		normalizedImpulse[i] = audioImpulse[i] * normalizationFactor
	}

	return normalizedImpulse
}

// Normalizes Audio Data in supploed samples
func Normalize(inputSamples [][]float64, targetLevel float64) float64 {
	// Find max gain of channels
	impulseGain := 0.0
	for _, channel := range inputSamples {
		testGain := CalculateMaxGain(channel)
		if testGain > impulseGain {
			impulseGain = testGain
		}
	}
	if impulseGain == 0 {
		return impulseGain
	}

	// Normalize gain
	for i := range inputSamples {
		inputSamples[i] = NormalizeAudioImpulse(inputSamples[i], targetLevel, impulseGain) // Replace with actual implementation
	}

	return impulseGain
}

func resampleChannel(inputSamples []float64, fromSampleRate, toSampleRate, quality int) []float64 {
	// If no resampling required
	if fromSampleRate == toSampleRate {
		return inputSamples
	}

	// Best so far, slight hump where low pass filter is applied.
	var samples []float64

	srcLength := len(inputSamples)
	destLength := int(float64(srcLength) * float64(toSampleRate) / float64(fromSampleRate))
	dx := float64(srcLength) / float64(destLength)

	if fromSampleRate < 88000 {
		// Apply filter before resampling to avoid a bump
		//num := float64(toSampleRate) / float64(fromSampleRate)
		// Fmax: Nyquist half of destination sampleRate
		// Fmax / sampleRater = 0.5
		// Apply a low pass before resampling - this has been hand-tested
		myFilters := [][]float64{CalcBiquadFilter("lowpass", 18000, fromSampleRate, 0, 0.3, "Q")}
		lowPassImpulse := CascadeFilters(myFilters, 0)
		inputSamples = ConvolveImpulsesFFT(lowPassImpulse, inputSamples)
	}

	fmaxDivSR := 0.5
	rG := 2 * fmaxDivSR

	// Quality is half the window width
	wndWidth2 := quality
	wndWidth := quality * 2

	x := 0.0
	var rY, rW, rA, rSnc float64
	var tau, j int
	for i := 0; i < destLength; i++ {
		rY = 0.0
		for tau = -wndWidth2; tau < wndWidth2; tau++ {
			// Input sample index
			j = int(x + float64(tau))

			// Hann Window. Scale and calculate sinc
			rW = 0.5 - 0.5*math.Cos(2*math.Pi*(0.5+(float64(j)-x)/float64(wndWidth)))
			rA = 2 * math.Pi * (float64(j) - x) * fmaxDivSR
			rSnc = 1.0
			if rA != 0 {
				rSnc = math.Sin(rA) / rA
			}

			if j >= 0 && j < srcLength {
				rY += rG * rW * rSnc * inputSamples[j]
			}
		}
		samples = append(samples, rY)
		x += dx
	}

	return samples
}

func resampler(inputSamples [][]float64, fromSampleRate, toSampleRate, quality int) [][]float64 {
	if fromSampleRate == toSampleRate {
		return inputSamples
	}

	myChannelsLength := len(inputSamples)
	for c := 0; c < myChannelsLength; c++ {
		inputSamples[c] = resampleChannel(inputSamples[c], fromSampleRate, toSampleRate, quality)
	}

	return inputSamples
}

func ReadWavFile(filename string) ([][]float64, uint32, uint16, error) {
	file, err := os.OpenFile(filename, os.O_RDONLY, 0644)
	if err != nil {
		return nil, 0, 0, err
	}
	defer file.Close()

	//

	if err != nil {
		panic(err) // Handle the error appropriately
	}
	// Need to plod through this and convert c# function line by line. See SimpleDSP ReadWav

	// Read the header
	var chunkID [4]byte
	var chunkSize uint32
	var format [4]byte
	var subchunk1ID [4]byte
	var subchunk1Size uint32
	var audioFormat uint16
	var numChannels uint16
	var sampleRate uint32
	var byteRate uint32
	var blockAlign uint16
	var bitsPerSample uint16
	var subchunk2ID [4]byte
	var subchunk2Size uint32

	binary.Read(file, binary.LittleEndian, &chunkID)
	binary.Read(file, binary.LittleEndian, &chunkSize)
	binary.Read(file, binary.LittleEndian, &format)
	binary.Read(file, binary.LittleEndian, &subchunk1ID)
	binary.Read(file, binary.LittleEndian, &subchunk1Size)
	binary.Read(file, binary.LittleEndian, &audioFormat)
	binary.Read(file, binary.LittleEndian, &numChannels)
	binary.Read(file, binary.LittleEndian, &sampleRate)
	binary.Read(file, binary.LittleEndian, &byteRate)
	binary.Read(file, binary.LittleEndian, &blockAlign)
	binary.Read(file, binary.LittleEndian, &bitsPerSample)
	binary.Read(file, binary.LittleEndian, &subchunk2ID)
	binary.Read(file, binary.LittleEndian, &subchunk2Size)

	// Read the data
	numSamples := subchunk2Size / uint32(numChannels/(bitsPerSample/8))
	samples := make([][]float64, numChannels)
	for i := range samples {
		samples[i] = make([]float64, numSamples)
	}

	for i := uint32(0); i < numSamples; i++ {
		for j := uint16(0); j < numChannels; j++ {
			switch bitsPerSample {
			case 8:
				var sample uint8
				binary.Read(file, binary.LittleEndian, &sample)
				samples[j][i] = float64(sample)
			case 16:
				var sample int16
				binary.Read(file, binary.LittleEndian, &sample)
				samples[j][i] = float64(sample) / 32768.0
			case 24:
				var sample [3]byte
				binary.Read(file, binary.LittleEndian, &sample)
				sample32 := int32(sample[0]) | int32(sample[1])<<8 | int32(sample[2])<<16
				samples[j][i] = float64(sample32) / 8388608.0
			case 32:
				var sample int32
				binary.Read(file, binary.LittleEndian, &sample)
				samples[j][i] = float64(sample) / 2147483648.0
			}
		}
	}

	return samples, sampleRate, bitsPerSample, nil
}
