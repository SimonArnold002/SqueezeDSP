package foxFilters

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"sync"

	foxConvolver "github.com/Foxenfurter/foxAudioLib/foxConvolverPartition" // CHANGED: Use partitioned convolver
	"github.com/Foxenfurter/foxAudioLib/foxLog"
	"github.com/Foxenfurter/foxAudioLib/foxNormalizer"
	"github.com/Foxenfurter/foxAudioLib/foxPEQ"
)

const packageName = "foxFilters"
const signalDivisor = 3 // when we calculate the signal block length we divide the sample rate by this number

type PlayerConfig struct {
	Filters     []BiquadFilter
	Preamp      float64
	FIRWavFile  string
	Width       float64
	Balance     float64
	Delay       ChannelDelay
	Loudness    Loudness
	FIRStrength float64 // 0-100, 0=bypass convolver, 100=full impulse
	Crossfeed   string  // "off", "light", "medium", "strong"
}

type BiquadFilter struct {
	FilterType string
	Enabled    bool
	Frequency  float64
	Gain       float64
	SlopeType  string  // "Q" in this case
	Slope      float64 // Q value
}

type ChannelDelay struct {
	Units string
	Value float64
}

type Loudness struct {
	Enabled        bool
	ListeningLevel float64
}

type rawClientConfig struct {
	Client map[string]json.RawMessage `json:"Client"`
}

// CleanUpImpulse removes any leading or trailing silence from the impulse
func NewPEQFilter(targetSampleRate int, myLogger *foxLog.Logger) *foxPEQ.PEQFilter {
	myPEQ := foxPEQ.NewPEQFilter(targetSampleRate, 15) // Create a single PEQFilter
	myPEQ.DebugFunc = myLogger.Debug
	return &myPEQ
}

// BuildPlayerConfig takes already-normalised data
// Handles only the current Filters array format
func BuildPlayerConfig(raw map[string]json.RawMessage) (*PlayerConfig, error) {
	config := &PlayerConfig{
		Filters:     make([]BiquadFilter, 0),
		FIRStrength: 100.0, // default to full strength
	}

	if filtersRaw, ok := raw["Filters"]; ok {
		var filters []struct {
			FilterType string      `json:"FilterType"`
			Frequency  json.Number `json:"Frequency"`
			Gain       json.Number `json:"Gain"`
			Slope      json.Number `json:"Slope"`
			SlopeType  string      `json:"SlopeType"`
		}

		if err := json.Unmarshal(filtersRaw, &filters); err == nil {
			for _, f := range filters {
				// Normalize filter type casing
				filterType := strings.ToLower(f.FilterType)
				switch filterType {
				case "peak":
					filterType = "Peak"
				case "lowshelf":
					filterType = "LowShelf"
				case "highshelf":
					filterType = "HighShelf"
				case "lowpass":
					filterType = "LowPass"
				case "highpass":
					filterType = "HighPass"
				}

				config.Filters = append(config.Filters, BiquadFilter{
					FilterType: filterType,
					Enabled:    true, // All filters in array are enabled
					Frequency:  parseNumber(f.Frequency),
					Gain:       parseNumber(f.Gain),
					SlopeType:  f.SlopeType,
					Slope:      parseNumber(f.Slope),
				})
			}
		}
	}
	// Only processes current format fields:
	// Filters array, Preamp, FIRWavFile, Width, Balance, Delay, Loudness
	for key, value := range raw {
		switch {
		case key == "Filters":
			continue // Already processed

		case key == "Preamp":
			if s, err := parseRaw2Number(value); err == nil {
				config.Preamp, err = s.Float64()
				if err != nil {
					//output error message
					return nil, err
				}
			}
		case key == "FIRWavFile":
			if err := json.Unmarshal(value, &config.FIRWavFile); err == nil {
				if config.FIRWavFile == "-" {
					config.FIRWavFile = ""
				}
				config.FIRWavFile = strings.Trim(config.FIRWavFile, "\"")
			}
		case key == "Width":
			if s, err := parseRaw2Number(value); err == nil {
				config.Width, err = s.Float64()
				if err != nil {
					//output error message
					return nil, err
				}
			}
		case key == "Balance":
			if s, err := parseRaw2Number(value); err == nil {
				config.Balance, err = s.Float64()
				if err != nil {
					//output error message
					return nil, err
				}
			}
		case key == "Loudness":
			var ls struct {
				Enabled json.Number `json:"enabled"`
				Level   json.Number `json:"listening_level"`
			}
			if err := json.Unmarshal(value, &ls); err == nil {
				config.Loudness.Enabled = parseBool(ls.Enabled)
				if config.Loudness.Enabled {
					config.Loudness.ListeningLevel = parseNumber(ls.Level)
				}
			}
		case key == "Delay":
			var delay struct {
				Delay json.Number `json:"delay"`
				Units string      `json:"units"`
			}
			if err := json.Unmarshal(value, &delay); err == nil {
				config.Delay.Value = parseNumber(delay.Delay)

				config.Delay.Units = "ms"
			}
		case key == "FIRStrength":
			if s, err := parseRaw2Number(value); err == nil {
				if f, err := s.Float64(); err == nil {
					config.FIRStrength = f
				} else {
					config.FIRStrength = 100.0
				}
			}

		case key == "Crossfeed":
			var s string
			if err := json.Unmarshal(value, &s); err == nil {
				config.Crossfeed = strings.Trim(s, "\"")
			}
		}
	}
	return config, nil

}

func CleanUpImpulse(myImpulse [][]float64, sampleRate int, thresholdDB float64, myLogger *foxLog.Logger) ([][]float64, error) {
	const (
		windowTaper = 0.010 // 10ms window taper time (seconds)
		// Minimum required impulse length
		lengthBufferFactor = 1.2 // Return original if within 20% of min
		peakLocationRatio  = 0.1 // 10% threshold for peak location
	)
	minLength := int(float64(sampleRate) * 0.2)
	if len(myImpulse) == 0 || len(myImpulse[0]) == 0 {
		return nil, fmt.Errorf("empty impulse input")
	}

	originalLength := len(myImpulse[0])
	minThreshold := int(math.Ceil(float64(minLength) * lengthBufferFactor))

	if originalLength < minThreshold {
		myLogger.Debug(fmt.Sprintf("Original length %d under buffer threshold (%d), returning unchanged",
			originalLength, minThreshold))
		return myImpulse, nil
	}

	// --- Enhanced peak detection with position tracking ---
	peak := 0.0
	peakIndex := -1 // Track sample index where peak occurs
	for _, channel := range myImpulse {
		for i, sample := range channel {
			abs := math.Abs(sample)
			if abs > peak {
				peak = abs
				peakIndex = i
			}
		}
	}
	if peak == 0 {
		return nil, fmt.Errorf("impulse is completely silent")
	}

	threshold := peak * math.Pow(10, thresholdDB/20)
	myLogger.Debug(fmt.Sprintf("Peak: %.4f @ sample %d (%.2f%%), Threshold: %.6f",
		peak, peakIndex, 100*float64(peakIndex)/float64(originalLength), threshold))

	numSamples := originalLength
	start, end := -1, -1

	// Find first sample above threshold
	for i := 0; i < numSamples; i++ {
		for _, channel := range myImpulse {
			if math.Abs(channel[i]) >= threshold {
				start = i
				break
			}
		}
		if start != -1 {
			break
		}
	}

	// Find last sample above threshold
	for i := numSamples - 1; i >= 0; i-- {
		for _, channel := range myImpulse {
			if math.Abs(channel[i]) >= threshold {
				end = i
				break
			}
		}
		if end != -1 {
			break
		}
	}

	if start == -1 || end == -1 || start > end {
		return nil, fmt.Errorf("no non-silent section found")
	}

	// --- Conditionally disable start trimming/tapering ---
	skipStartTaper := false
	safetyMargin := int(float64(sampleRate) * 0.05)

	// Check if peak is in first 10% of original length
	if float64(peakIndex) < float64(originalLength)*peakLocationRatio {
		myLogger.Debug("Peak in first 10% - preserving start without trim/taper")
		start = 0 // Disable start trimming
		skipStartTaper = true
	} else {
		// Apply normal safety margins
		start = max(0, start-safetyMargin)
	}
	end = min(numSamples-1, end+safetyMargin*2) // Always apply end margin

	// --- Minimum length enforcement ---
	currentLength := end - start + 1
	if currentLength < minLength {
		needed := minLength - currentLength
		expandStart := needed / 2
		expandEnd := needed - expandStart

		// Adjust expansion based on available space and peak constraints
		availableBefore := start
		availableAfter := numSamples - 1 - end

		// If preserving start, prevent expansion before start
		if skipStartTaper {
			expandStart = 0 // Can't expand before start
			expandEnd = needed
		}

		// Normal expansion adjustments
		if expandStart > availableBefore {
			expandEnd += expandStart - availableBefore
			expandStart = availableBefore
		}
		if expandEnd > availableAfter {
			expandStart += expandEnd - availableAfter
			expandEnd = availableAfter
		}

		// Apply expansion
		start = max(0, start-expandStart)
		end = min(numSamples-1, end+expandEnd)

		// Final length check
		if (end - start + 1) < minLength {
			return nil, fmt.Errorf("cannot reach minimum length %d (max possible: %d)",
				minLength, end-start+1)
		}
		myLogger.Debug(fmt.Sprintf("Expanded region to %d samples (start: %d, end: %d)",
			end-start+1, start, end))
	}

	// Trim impulse
	trimmed := make([][]float64, len(myImpulse))
	for c := range trimmed {
		trimmed[c] = myImpulse[c][start : end+1]
	}

	// --- Conditional window application ---
	if skipStartTaper {
		// Apply right-side taper only
		taperSamples := int(float64(sampleRate) * windowTaper)
		if taperSamples > len(trimmed[0]) {
			taperSamples = len(trimmed[0])
		}
		if taperSamples > 0 {
			for c := range trimmed {
				// Taper only the END of the impulse
				for i := 0; i < taperSamples; i++ {
					idx := len(trimmed[c]) - 1 - i
					factor := float64(i) / float64(taperSamples)
					trimmed[c][idx] *= factor
				}
			}
			myLogger.Debug(fmt.Sprintf("Applied right-only taper (%d samples)", taperSamples))
		}
	} else {
		// Apply standard bidirectional taper
		applyWindow(trimmed, windowTaper, float64(sampleRate), myLogger)
	}

	return trimmed, nil
}

func applyWindow(impulse [][]float64, taperTime, sampleRate float64, logger *foxLog.Logger) {
	if len(impulse) == 0 || len(impulse[0]) == 0 {
		return
	}

	totalSamples := len(impulse[0])
	taperSamples := int(math.Ceil(sampleRate * taperTime))
	taperSamples = int(math.Min(float64(taperSamples), float64(totalSamples/2))) // Prevent over-tapering

	logger.Debug(fmt.Sprintf("Applying %d sample window taper", taperSamples))

	// Create window function (cosine taper)
	window := make([]float64, taperSamples)
	for i := range window {
		window[i] = 0.5 * (1 - math.Cos(math.Pi*float64(i)/float64(taperSamples-1)))
	}

	// Apply window to all channels
	for c := range impulse {
		// Fade in
		for i := 0; i < taperSamples; i++ {
			impulse[c][i] *= window[i]
		}

		// Fade out
		for i := 0; i < taperSamples; i++ {
			idx := totalSamples - taperSamples + i
			impulse[c][idx] *= window[taperSamples-1-i]
		}
	}
}

// ApplyFIRStrength scales the impulse response by the specified strength percentage (0-100)
func ApplyFIRStrengthToImpulse(full [][]float64, strength float64) [][]float64 {
	if len(full) == 0 || len(full[0]) == 0 {
		return make([][]float64, 0)
	}

	alpha := math.Max(0, math.Min(strength, 100)) / 100.0

	if alpha == 0.0 {
		return make([][]float64, 0)
	}

	if alpha == 1.0 {
		return full
	}

	out := make([][]float64, len(full))
	for ch := range full {
		out[ch] = make([]float64, len(full[ch]))
		for i := range full[ch] {
			baseVal := 0.0
			if i == 0 {
				baseVal = 1.0
			}
			out[ch][i] = baseVal + alpha*(full[ch][i]-baseVal)
		}
	}
	return out
}

// CrossfeedParams holds the parameters needed to implement headphone crossfeed.
// The cross signal is low-pass filtered then attenuated before being added
// to the opposite channel with a small delay.
type CrossfeedParams struct {
	FeedLinear float64 // linear gain of cross-channel feed
	LPCutoffHz float64 // low-pass cutoff for cross signal (Hz)
	DelayMs    float64 // inter-channel delay (ms)
}

// GetCrossfeedParams returns crossfeed parameters for a named level.
// Based on BS2B-inspired values targeting natural speaker imaging on headphones.
func GetCrossfeedParams(level string) *CrossfeedParams {
	switch strings.ToLower(level) {
	case "light":
		return &CrossfeedParams{
			FeedLinear: math.Pow(10, -9.0/20.0), // -9 dB
			LPCutoffHz: 800.0,
			DelayMs:    0.1,
		}
	case "medium":
		return &CrossfeedParams{
			FeedLinear: math.Pow(10, -6.0/20.0), // -3dB temporarily for testing
			LPCutoffHz: 700.0,
			DelayMs:    0.2,
		}
	case "strong":
		return &CrossfeedParams{
			FeedLinear: math.Pow(10, -4.5/20.0), // -4.5 dB
			LPCutoffHz: 650.0,
			DelayMs:    0.3,
		}
	case "bonkers":
		return &CrossfeedParams{
			FeedLinear: math.Pow(10, -1.0/20.0), // -1 dB
			LPCutoffHz: 5000.0,
			DelayMs:    60.0, // 0.6 is maximum natural ITD
		}
	default: // "off" or unrecognised
		return nil
	}
}

func BuildPEQFilter(
	myConfig *PlayerConfig,
	targetSampleRate int, myLogger *foxLog.Logger) (*foxPEQ.PEQFilter, error) {

	myPEQ := foxPEQ.NewPEQFilter(targetSampleRate, 15) // Create a single PEQFilter
	myPEQ.DebugFunc = myLogger.Debug
	generateImpulse := false
	if myConfig.Loudness.Enabled {
		myLogger.Debug(packageName + ": Loudness Filter Enabled")
		myLoudnessFilter := foxPEQ.NewLoudness()
		myLoudnessFilter.PlaybackPhon = myConfig.Loudness.ListeningLevel

		dfpl, err := myLoudnessFilter.DifferentialSPL(1.0)
		if err != nil {
			myLogger.Debug(packageName + ": DifferentialSPL error unable to proceed - " + err.Error())
			return nil, err
		}
		err = myPEQ.GenerateEQLoudnessFilter(dfpl)
		if err != nil {
			myLogger.Debug(packageName + ": Invalid filter definition, " + err.Error())
			return nil, err
		}
		generateImpulse = true
	}
	// Apply filters if enabled and there are filters in config
	numberOfFilters := len(myConfig.Filters)
	if len(myConfig.Filters) > 0 {
		for _, filter := range myConfig.Filters {
			err := myPEQ.CalcBiquadFilter(filter.FilterType, filter.Frequency, filter.Gain, filter.Slope, filter.SlopeType)
			// log error and continue - we will still generate an impulse
			if err != nil {
				myLogger.Debug(packageName + ": Filter Ignored: " + err.Error())
				numberOfFilters--
			} else {
				generateImpulse = true
			}
		}

	}

	/*	myLogger.Debug(packageName + fmt.Sprintf(" Coefficient count: %d", len(myPEQ.FilterCoefficients)))
		for _, c := range myPEQ.FilterCoefficients {
			myLogger.Debug(packageName + fmt.Sprintf(" b: %.10f, %.10f, %.10f", c.B[0], c.B[1], c.B[2]))
			myLogger.Debug(packageName + fmt.Sprintf(" a: %.10f, %.10f, %.10f", c.A[0], c.A[1], c.A[2]))
		}
	*/
	if generateImpulse {
		myLogger.Debug(packageName + ": PEQ Filter Built - using " + fmt.Sprintf(" %v", numberOfFilters) + " filters")
		myPEQ.UpdateFilterLength()
		//myPEQ.GenerateFilterImpulse()

	}

	return &myPEQ, nil
}

func NormalizeImpulse(myImpulse [][]float64, targetLevel float64, myLogger *foxLog.Logger) ([][]float64, error) {
	const functionName = "NormalizeImpulse"
	const MsgHeader = packageName + ": " + functionName + ": "
	myLogger.Debug(MsgHeader + "Normalizing impulse...")
	maxGain := 0.0

	for i := range myImpulse {
		maxGain = math.Max(maxGain, foxConvolver.MaxGainFromFFT(myImpulse[i]))

	}
	myLogger.Debug(MsgHeader + "Normalizing using gain: " + fmt.Sprintf("%.4f", maxGain))
	for i := range myImpulse {
		myImpulse[i] = foxNormalizer.NormalizeAudioChannel(myImpulse[i], targetLevel, maxGain)
	}

	return myImpulse, nil
}

// CombineFilters - Combine the filter impulse with the PEQ impulse and handle appropriate scenarios where one, both or neither are present
// CHANGED: Now returns []*foxConvolver.PartitionedConvolver instead of []foxConvolver.Convolver
// CombineFilters prepares the impulse data, normalizes it, and builds the Convolvers once.
func CombineFilters(filterImpulse [][]float64, myPEQ *foxPEQ.PEQFilter, NumChannels int, targetSampleRate int, myLogger *foxLog.Logger) ([]*foxConvolver.PartitionedConvolver, error) {

	// 1. DATA PREPARATION
	// We will build the final audio buffers first, before creating convolver objects.
	var finalImpulses [][]float64
	err := error(nil)
	hasFIR := len(filterImpulse) > 0
	hasPEQ := len(myPEQ.FilterCoefficients) > 0
	//NeedNormalization := false
	// Handle Channel Mapping (Mono FIR to Stereo)

	if hasFIR && len(filterImpulse) == 1 && NumChannels == 2 {
		original := filterImpulse[0]
		copyData := make([]float64, len(original))
		copy(copyData, original)
		filterImpulse = append(filterImpulse, copyData)
	}

	// Handle Channel Trimming
	if hasFIR && len(filterImpulse) > NumChannels {
		myLogger.Debug(packageName + fmt.Sprintf(": Trimming impulse from %d to %d channels", len(filterImpulse), NumChannels))
		filterImpulse = filterImpulse[:NumChannels]
	}
	switch {
	// Logic Branch: Determine the base impulse data
	case hasFIR && hasPEQ:
		myLogger.Debug(packageName + ": Merging PEQ and FIR Filters")
		finalImpulses = ApplyPEQToFIR(filterImpulse, myPEQ, targetSampleRate, myLogger)
		myLogger.Debug(packageName + ": Merging PEQ and FIR Filters - Done")

	case hasFIR && hasPEQ && 1 == 2:
		myLogger.Debug(packageName + ": Merging PEQ and FIR Filters")
		// Helper now returns raw float data

		myLogger.Debug(packageName + ": Merging PEQ and FIR Filters - Done")
	case hasFIR:
		myLogger.Debug(packageName + ": No PEQ Filter - using FIR")
		finalImpulses = filterImpulse
	case hasPEQ:

		myLogger.Debug(packageName + ": No FIR Filter - using PEQ")
		// Ensure the PEQ impulse has been generated (or generate it now)
		if len(myPEQ.Impulse) == 0 {
			myPEQ.GenerateFilterImpulse() // This should use the current FilterLength
		}
		finalImpulses = make([][]float64, NumChannels)
		for i := 0; i < NumChannels; i++ {
			imp := make([]float64, myPEQ.FilterLength) // Allocate based on PEQ's filter length
			copy(imp, myPEQ.Impulse)
			finalImpulses[i] = imp
		}
	default:
		myLogger.Debug(packageName + ": No FIR or PEQ Filter")
		finalImpulses = make([][]float64, NumChannels)
		for i := range finalImpulses {
			finalImpulses[i] = make([]float64, 0)
		}
	}

	//trim the final impulses to the correct length based on the PEQ filter length (if PEQ is present) or the FIR length (if no PEQ)
	if hasFIR || hasPEQ {
		finalImpulses, err = CleanUpImpulse(finalImpulses, targetSampleRate, -80.0, myLogger)
		if err != nil {
			myLogger.Debug(packageName + "Error cleaning final impulse: " + err.Error())
			return nil, err // actually propagate it
		}
	}
	// 3. OBJECT CREATION
	// Create convolvers once with the final, correct data.
	myConvolvers := make([]*foxConvolver.PartitionedConvolver, NumChannels)
	var wg sync.WaitGroup
	for i := range myConvolvers {
		wg.Add(1)
		go func(channel int) {
			defer wg.Done()
			impulse := []float64{}
			if finalImpulses != nil && channel < len(finalImpulses) {
				impulse = finalImpulses[channel]
			}
			myConvolvers[channel] = foxConvolver.NewPartitionedConvolver(impulse, targetSampleRate)
			myLogger.Debug(packageName + "Created convolver ")
		}(i)
	}
	wg.Wait()

	if len(myConvolvers) > 0 {
		myLogger.Debug(packageName + "Convolver Filters " + fmt.Sprintf("Number of channels %v, length of impulse %v", len(myConvolvers), len(myConvolvers[0].FilterImpulse)))
	}

	return myConvolvers, nil
}

// using peq.FilterLength to allocate enough extra samples for the tail.
func ApplyPEQToFIR(firImpulse [][]float64, peq *foxPEQ.PEQFilter, sampleRate int, logger *foxLog.Logger) [][]float64 {
	if len(firImpulse) == 0 || peq == nil || len(peq.FilterCoefficients) == 0 {
		return firImpulse
	}

	// Total combined length ≈ FIR length + PEQ impulse length (decay fully captured)
	// We add peq.FilterLength as extra samples; the filter will ring into the zero padding.
	merged := make([][]float64, len(firImpulse))

	for ch := range firImpulse {
		fir := firImpulse[ch]
		// Allocate: FIR + extra samples (safe upper bound)
		buf := make([]float64, len(fir)+peq.FilterLength)
		copy(buf, fir) // rest is zero

		// Apply each biquad section in cascade
		current := buf
		for _, coeff := range peq.FilterCoefficients {
			output := make([]float64, len(current))
			foxPEQ.IIRFilter(current, coeff.B, coeff.A, output)
			current = output
		}
		merged[ch] = current
	}

	// Optional: Trim using your existing CleanUpImpulse (which works on [][]float64)
	// trimmed, _ := CleanUpImpulse(merged, sampleRate, -80.0, logger)
	// return trimmed
	return merged
}

// MergePEQandFIRFilters takes the PEQ impulse and the FIR impulse(s) and merges them together using convolution, returning the raw merged impulse data.
func MergePEQandFIRFilters(myPEQ []float64, impulseSamples [][]float64, targetSampleRate int, myLogger *foxLog.Logger) [][]float64 {

	mergedImpulses := make([][]float64, len(impulseSamples))
	var wg sync.WaitGroup

	for i := range impulseSamples {
		wg.Add(1)
		go func(channel int) {
			defer wg.Done()

			// Create a temporary convolver just for the math
			// We use the PEQ as the "Filter" and the FIR file as the "Input Signal" (or vice versa, convolution is commutative)
			tempConvolver := foxConvolver.NewPartitionedConvolver(myPEQ, targetSampleRate)

			// ConvolveFFT returns the resulting float slice
			mergedImpulses[channel] = tempConvolver.ConvolveFFT(impulseSamples[channel])
		}(i)
	}
	wg.Wait()

	return mergedImpulses
}

// dBFS to Linear RMS
func dBFSToLinear(dBFS float64) float64 {
	return math.Pow(10, dBFS/20)
}

// simple width coefficient keeps centre channels gain vs crushing it with more complex function
func GetWidthCoefficients(widthDB float64) (float64, float64) {
	return 0.5, 0.5 * dBFSToLinear(widthDB)
}

// Function to calculate width coefficients
func GetWidthCoefficientsComplex(widthDB float64) (float64, float64) {
	if widthDB == 0 {
		return 0.5, 0.5 // Correct neutral gains
	}

	midGainDB := -widthDB / 2
	sideGainDB := widthDB / 2

	midGainLinear := dBFSToLinear(midGainDB)
	sideGainLinear := dBFSToLinear(sideGainDB)

	sumSquares := midGainLinear*midGainLinear + sideGainLinear*sideGainLinear
	k := math.Sqrt(0.5 / sumSquares) // Correct normalization

	return midGainLinear * k, sideGainLinear * k
}

func GetChannelsGain(myConfig *PlayerConfig, numChannels int, myLogger *foxLog.Logger) []float64 {

	gains := make([]float64, numChannels)
	// Convert preamp dB to linear
	preampLinear := dBFSToLinear(myConfig.Preamp)

	// Initialize gains with preamp
	for i := range numChannels {
		gains[i] = preampLinear
	}

	// Apply balance SAFELY (only attenuate, never boost) and only to first two channels
	if myConfig.Balance > 0 {
		// Positive balance = favor right → attenuate left
		gains[0] *= dBFSToLinear(-myConfig.Balance) // Attenuate left
	} else if myConfig.Balance < 0 {
		// Negative balance = favor left → attenuate right
		gains[1] *= dBFSToLinear(myConfig.Balance) // balanceDB is negative
	}

	// Clamp gains to prevent invalid values (optional)
	for i := range numChannels {
		gains[i] = math.Max(0, math.Min(gains[i], 1.0))
	}
	return gains
}

// generateSineSweepWithSilence, generates a 1 second sine wave with a max peak of 1.0 for calibration of impulse
func generateSineSweepWithSilence(sampleRate, durationSec, peak float64) []float64 {
	numSamples := int(sampleRate * durationSec)
	sweep := make([]float64, numSamples)
	startFreq := 20.0  // 20 Hz
	endFreq := 22000.0 // 22 kHz
	t := 0.0

	for i := range numSamples {
		freq := startFreq * math.Pow(endFreq/startFreq, t/durationSec)
		sweep[i] = peak * math.Sin(2*math.Pi*freq*t)
		t += 1.0 / sampleRate
	}

	// Add 10 ms of silence at start/end
	padSamples := int(0.01 * sampleRate) // 10 ms
	paddedSweep := make([]float64, len(sweep)+2*padSamples)
	copy(paddedSweep[padSamples:padSamples+len(sweep)], sweep)
	return paddedSweep
}

func generateWhiteNoise(sampleRate, durationSec float64, peak float64) []float64 {
	numSamples := int(sampleRate * durationSec)
	noise := make([]float64, numSamples)

	for i := 0; i < numSamples; i++ {
		noise[i] = (rand.Float64()*2 - 1) * peak
	}
	return noise
}

// CalibrateImpulse runs a 1 second sweep through the impulse and returns the peak value
// CHANGED: Uses partitioned convolver
func CalibrateImpulse(impulse []float64, sampleRate float64) float64 {
	var output []float64
	fastCalibration := false
	if fastCalibration {
		sweep := generateWhiteNoise(sampleRate, 0.1, 1.0)
		// in this scenario the sweep is likely shorter than the impulse so invert it

		myConvolver := foxConvolver.NewPartitionedConvolver(sweep, int(sampleRate)) // CHANGED: Use constructor
		output = myConvolver.ConvolveFFT(impulse)
	} else {
		sweep := generateSineSweepWithSilence(sampleRate, 0.25, 1.0)
		myConvolver := foxConvolver.NewPartitionedConvolver(sweep, int(sampleRate)) // CHANGED: Use constructor

		output = myConvolver.ConvolveFFT(sweep)
	}
	// Trim pre/post silence (10 ms) from output
	padSamples := int(0.01 * sampleRate) // 10 ms
	validOutput := output[padSamples : len(output)-padSamples]

	return foxNormalizer.CalculateMaxGain(validOutput)
}

// Delay structure for managing delay processing
type Delay struct {
	sampleRate float64     // Shared sample rate (Hz)
	Delays     []int       // Delay in samples per channel
	Buffers    [][]float64 // Delay buffers for each channel
}

// NewDelay creates a delay system for `channelCount` channels with zero delay.
func NewDelay(channelCount int, sampleRate float64) *Delay {
	return &Delay{
		sampleRate: sampleRate,
		Delays:     make([]int, channelCount),
		Buffers:    make([][]float64, channelCount),
	}
}

// AddDelay configures a delay for a specific channel (in milliseconds).
func (d *Delay) AddDelay(channel int, delayMs float64) {
	if channel < 0 || channel >= len(d.Delays) {
		return // Invalid channel
	}

	// Convert ms to samples (rounded)
	delaySamples := int(math.Round((delayMs * d.sampleRate) / 1000.0))
	if delaySamples < 0 {
		delaySamples = 0
	}

	d.Delays[channel] = delaySamples

	// Initialize buffer with zeros to enforce initial delay
	if delaySamples > 0 {
		d.Buffers[channel] = make([]float64, delaySamples) // Pre-filled with zeros
	} else {
		d.Buffers[channel] = nil
	}
}

func createPeakingFilter(filterType string, pf struct {
	Gain  json.Number `json:"gain"`
	Freq  json.Number `json:"freq"`
	Slope json.Number `json:"q"`
}) BiquadFilter {
	return BiquadFilter{
		FilterType: filterType,
		Enabled:    isNotZeroValue(pf.Gain), // Assume enabled if present
		Frequency:  parseNumber(pf.Freq),
		Gain:       parseNumber(pf.Gain),
		SlopeType:  "Q",
		Slope:      parseNumber(pf.Slope),
	}
}

func createShelfFilter(filterType string, sf struct {
	Gain    json.Number `json:"gain"`
	Freq    json.Number `json:"freq"`
	Slope   json.Number `json:"slope"`
	Enabled json.Number `json:"enabled"`
}) BiquadFilter {
	return BiquadFilter{
		FilterType: filterType,
		Enabled:    parseBool(sf.Enabled), // Assume enabled if present
		Frequency:  parseNumber(sf.Freq),
		Gain:       parseNumber(sf.Gain),
		SlopeType:  "Q",
		Slope:      parseNumber(sf.Slope),
	}
}

func createPassFilter(filterType string, pf struct {
	Freq    json.Number `json:"freq"`
	Slope   json.Number `json:"q"`
	Enabled json.Number `json:"enabled"`
}) BiquadFilter {
	return BiquadFilter{
		FilterType: filterType,
		Enabled:    parseBool(pf.Enabled), // Assume enabled if present
		Frequency:  parseNumber(pf.Freq),
		SlopeType:  "Q",
		Slope:      parseNumber(pf.Slope),
	}
}

func parseRaw2Number(rawMsg json.RawMessage) (json.Number, error) {
	var num json.Number
	err := json.Unmarshal(rawMsg, &num)
	if err != nil {
		num = "0"
		return num, err
	}
	return num, nil
}

func parseNumber(n json.Number) float64 {
	if s := n.String(); s != "" && s != "null" {
		f, _ := strconv.ParseFloat(s, 64)
		return f
	}
	return 0
}

func parseBool(n json.Number) bool {
	s := n.String()
	return s == "1" || strings.ToLower(s) == "true"
}

func isNullFilter(values ...json.Number) bool {
	for _, v := range values {
		if v.String() != "null" {
			return false
		}
	}
	return true
}

func isNotZeroValue(n json.Number) bool {
	if s := n.String(); s != "" && s != "null" {
		f, _ := strconv.ParseFloat(s, 64)
		if f == 0 {
			return false
		}
		return true
	}
	return true
}

// Crossfeed
type CrossfeedProcessor struct {
	Params *CrossfeedParams
	// shelf biquad coefficients - computed once at init
	b0, b1, b2 float64
	a1, a2     float64
	// biquad filter state - two samples per path
	x1L, x2L, y1L, y2L float64
	x1R, x2R, y1R, y2R float64
	// delay buffers - one per path
	delayBufL []float64
	delayBufR []float64
	delayPos  int
}

func NewCrossfeedProcessor(params *CrossfeedParams, sampleRate int) *CrossfeedProcessor {
	if params == nil {
		return nil
	}

	// low shelf biquad — boosts low frequencies relative to high in the cross signal
	// this models the head shadowing effect: lows cross freely, highs are attenuated
	// shelf gain is the difference between feed level and the HF floor
	// e.g. feed=-6dB, HF floor=-12dB gives 6dB of shelf boost at low frequencies
	fs := float64(sampleRate)
	fc := params.LPCutoffHz

	// shelf gain in linear — how much to boost lows relative to HF passthrough
	// HF floor is fixed at half the feed attenuation in dB
	feedDB := 20 * math.Log10(params.FeedLinear)
	shelfGainDB := feedDB / 2 // HF floor is half the feed attenuation
	shelfGain := math.Pow(10, shelfGainDB/20)

	// bilinear transform low shelf
	// based on Audio EQ Cookbook by Robert Bristow-Johnson
	A := math.Sqrt(params.FeedLinear / shelfGain)
	w0 := 2 * math.Pi * fc / fs
	cosw0 := math.Cos(w0)
	alpha := math.Sin(w0) / 2 * math.Sqrt((A+1/A)*(1/0.707-1)+2)

	b0 := A * ((A + 1) - (A-1)*cosw0 + 2*math.Sqrt(A)*alpha)
	b1 := 2 * A * ((A - 1) - (A+1)*cosw0)
	b2 := A * ((A + 1) - (A-1)*cosw0 - 2*math.Sqrt(A)*alpha)
	a0 := (A + 1) + (A-1)*cosw0 + 2*math.Sqrt(A)*alpha
	a1 := -2 * ((A - 1) + (A+1)*cosw0)
	a2 := (A + 1) + (A-1)*cosw0 - 2*math.Sqrt(A)*alpha

	delaySamples := int(math.Round(params.DelayMs * float64(sampleRate) / 1000.0))
	if delaySamples < 1 {
		delaySamples = 1
	}

	return &CrossfeedProcessor{
		Params:    params,
		b0:        b0 / a0,
		b1:        b1 / a0,
		b2:        b2 / a0,
		a1:        a1 / a0,
		a2:        a2 / a0,
		delayBufL: make([]float64, delaySamples),
		delayBufR: make([]float64, delaySamples),
	}
}

// ProcessChunk applies crossfeed with shelf filter to left and right slices in place
func (cf *CrossfeedProcessor) ProcessChunk(left, right []float64) {
	feed := cf.Params.FeedLinear
	bufLen := len(cf.delayBufL)

	for i := range left {
		// apply low shelf to left signal for cross feed to right
		filteredL := cf.b0*left[i] + cf.b1*cf.x1L + cf.b2*cf.x2L - cf.a1*cf.y1L - cf.a2*cf.y2L
		cf.x2L, cf.x1L = cf.x1L, left[i]
		cf.y2L, cf.y1L = cf.y1L, filteredL

		// apply low shelf to right signal for cross feed to left
		filteredR := cf.b0*right[i] + cf.b1*cf.x1R + cf.b2*cf.x2R - cf.a1*cf.y1R - cf.a2*cf.y2R
		cf.x2R, cf.x1R = cf.x1R, right[i]
		cf.y2R, cf.y1R = cf.y1R, filteredR

		// read delayed cross signal
		delayedL := cf.delayBufL[cf.delayPos]
		delayedR := cf.delayBufR[cf.delayPos]

		// write filtered signal into delay buffer
		cf.delayBufL[cf.delayPos] = filteredL
		cf.delayBufR[cf.delayPos] = filteredR
		cf.delayPos = (cf.delayPos + 1) % bufLen

		// mix delayed filtered cross signal into opposite channel
		left[i] += delayedR * feed
		right[i] += delayedL * feed
	}
}

// CrossfeedState holds the persisted state for gob serialisation
type CrossfeedState struct {
	X1L, X2L, Y1L, Y2L float64
	X1R, X2R, Y1R, Y2R float64
	DelayBufL          []float64
	DelayBufR          []float64
	DelayPos           int
}

func (cf *CrossfeedProcessor) SaveState() *CrossfeedState {
	if cf == nil {
		return nil
	}
	return &CrossfeedState{
		X1L:       cf.x1L,
		X2L:       cf.x2L,
		Y1L:       cf.y1L,
		Y2L:       cf.y2L,
		X1R:       cf.x1R,
		X2R:       cf.x2R,
		Y1R:       cf.y1R,
		Y2R:       cf.y2R,
		DelayBufL: append([]float64{}, cf.delayBufL...),
		DelayBufR: append([]float64{}, cf.delayBufR...),
		DelayPos:  cf.delayPos,
	}
}

func (cf *CrossfeedProcessor) LoadState(state *CrossfeedState) {
	if cf == nil || state == nil {
		return
	}
	cf.x1L, cf.x2L = state.X1L, state.X2L
	cf.y1L, cf.y2L = state.Y1L, state.Y2L
	cf.x1R, cf.x2R = state.X1R, state.X2R
	cf.y1R, cf.y2R = state.Y1R, state.Y2R
	if len(state.DelayBufL) == len(cf.delayBufL) {
		copy(cf.delayBufL, state.DelayBufL)
		copy(cf.delayBufR, state.DelayBufR)
		cf.delayPos = state.DelayPos
	}
}
