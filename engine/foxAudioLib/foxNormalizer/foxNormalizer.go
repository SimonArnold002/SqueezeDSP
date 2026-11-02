// Package: github.com/Foxenfurter/foxAudioLib/foxNormalizer
// filename foxNormalizer.go
// Package is designed to manage normalization of audio samples

package foxNormalizer

import (
	"math"
)

// Calculate the target gain level based on the sampling frequency
// parameters: From Sample Rate, To Sample Rate (int) returns the target gain level as float64
func TargetGainold(fromSampleRate, toSampleRate int, BaseLevel float64) float64 {
	// Let's normalize EQ Impulse
	NormaliseRatio := float64(fromSampleRate) / float64(toSampleRate)
	var targetLevel float64

	// 0.05 too high, 0.025 too low
	if toSampleRate == 192000 || toSampleRate == 176000 {
		// I have tweaked the formula for highest sample rate

		targetLevel = BaseLevel * (NormaliseRatio + (0.01 * 1 / NormaliseRatio))
	} else {
		targetLevel = BaseLevel * (NormaliseRatio + (0.035 * 1 / NormaliseRatio))
	}

	return targetLevel
}

func TargetGainCSharp(myInputSampleRate, myOutputSampleRate int, BaseLevel float64) float64 {
	targetLevel := BaseLevel
	normaliseRatio := float64(myInputSampleRate) / float64(myOutputSampleRate)
	if float64(myInputSampleRate)*1.1 < float64(myOutputSampleRate) {
		// lets normalise EQ Impulse
		targetLevel = targetLevel * (normaliseRatio + (0.2 * normaliseRatio))
	}
	if myInputSampleRate == 192000 || myInputSampleRate == 176000 {
		// I have tweaked the formula for highest sample rate

		targetLevel = targetLevel * (normaliseRatio + (0.01 * 1 / normaliseRatio))
	}
	if (myInputSampleRate == 96000 || myInputSampleRate == 88200) && (myOutputSampleRate == 192000 || myOutputSampleRate == 176400) {
		// I have tweaked the formula for highest sample rate as the low pass filter actually boosts levels following normalisation (average level)

		targetLevel = targetLevel * (normaliseRatio + (0.007 * 1 / normaliseRatio))
	}
	return targetLevel
}

func TargetGain(inputRate, outputRate int, baseLevel float64) float64 {
	// Calculate true resampling ratio
	ratio := float64(outputRate) / float64(inputRate)

	// For downsampling: compensate for filter attenuation
	if outputRate < inputRate {
		// Empirical correction factor (adjust based on your filter)
		// Typically 0.9-0.99 for good low-pass filters
		filterAttenuation := 0.95
		return baseLevel * (1.0 / (ratio * filterAttenuation))
	}

	// For upsampling: maintain original level
	return baseLevel
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

// calculates the safe maximum gain value for a filter
func CalculateSafeMaxGain(filter []float64) float64 {
	// Calculate both peak and RMS gains
	peak := 0.0
	sumSquares := 0.0

	for _, sample := range filter {
		abs := math.Abs(sample)
		if abs > peak {
			peak = abs
		}
		sumSquares += sample * sample
	}

	rms := math.Sqrt(sumSquares)

	// Use whichever requires more attenuation
	safeMaxGain := math.Max(peak, rms*3.0)

	return safeMaxGain
}

func CalculateImpulseRMS(impulse [][]float64) float64 {
	if len(impulse) == 0 || len(impulse[0]) == 0 {
		return 0
	}
	sum := 0.0
	count := 0
	for _, ch := range impulse {
		for _, v := range ch {
			sum += v * v
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return math.Sqrt(sum / float64(count))
}

// Normalises and audio signal to the target level using a max and target level.
// Max Level has been pre-calculated across all channels
func NormalizeAudioChannel(audioImpulse []float64, targetLevel float64, max float64) []float64 {
	// Check for divide by zero and no normalization needed

	if max == 0.0 || max == targetLevel {
		return audioImpulse
	}
	//println("NormalizeAudioImpulse max:", max, " target:", targetLevel)
	// Calculate the normalization factor
	normalizationFactor := targetLevel / max

	// Normalize the audio impulse
	normalizedImpulse := make([]float64, len(audioImpulse))
	for i := range audioImpulse {
		normalizedImpulse[i] = audioImpulse[i] * normalizationFactor
	}

	return normalizedImpulse
}

// Normalizes Audio Data in supplied samples
func NormalizePeak(inputSamples [][]float64, targetLevel float64) float64 {
	// Find max gain on all channels
	impulseGain := 0.0
	for _, channel := range inputSamples {
		impulseGain = math.Max(CalculateMaxGain(channel), math.Abs(impulseGain))
	}
	if impulseGain == 0 {
		return impulseGain
	}

	// Normalize gain
	for i := range inputSamples {
		inputSamples[i] = NormalizeAudioChannel(inputSamples[i], targetLevel, impulseGain) // Replace with actual implementation

	}

	return impulseGain
}
