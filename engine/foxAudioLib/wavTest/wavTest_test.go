package wavTest

import (
	"fmt"
	"math"
	"os"
	"path/filepath"

	"testing"

	"github.com/Foxenfurter/foxAudioLib/foxAudioDecoder"
	"github.com/Foxenfurter/foxAudioLib/foxAudioEncoder"
	"github.com/Foxenfurter/foxAudioLib/foxConvolver"
	"github.com/Foxenfurter/foxAudioLib/foxLog"
	"github.com/Foxenfurter/foxAudioLib/foxResampler"
	"github.com/Foxenfurter/foxLyrionDSP/LyrionDSPFilters"
	"github.com/Foxenfurter/foxLyrionDSP/LyrionDSPSettings"
)

type AudioProcessor struct {
	Decoder       *foxAudioDecoder.AudioDecoder
	Encoder       *foxAudioEncoder.AudioEncoder
	Logger        *foxLog.Logger
	Config        *LyrionDSPSettings.ClientConfig
	Convolvers    []foxConvolver.Convolver
	ConvolverTail [][]float64
	UseTail       bool
	TailPath      string
	DelayPath     string
	AppSettings   *LyrionDSPSettings.AppSettings
	Args          *LyrionDSPSettings.Arguments
	Impulse       [][]float64
	SaveImpulse   bool
	Delay         *LyrionDSPFilters.Delay
}

func TestLoadAndProcessImpulse(t *testing.T) {
	// Setup test environment
	tempDir := "H:\\temp"
	testLogger := createTestLogger(t, tempDir)
	ap := AudioProcessor{}
	ap.Logger = testLogger
	ap.Logger.DebugEnabled = true
	targetLevel := 0.75

	// Create output directory if it doesn't exist
	outputDir := filepath.Join(tempDir, "MeasurementOutput")
	if _, err := os.Stat(outputDir); os.IsNotExist(err) {
		os.Mkdir(outputDir, os.ModePerm)
	}

	// Define target sample rates
	sampleRates := []int{44100, 48000, 88200, 96000, 176400, 192000} // Add your desired rates

	// Define test cases (input file and output base name)
	testCases := []struct {
		inputFile  string
		outputBase string
	}{
		{
			inputFile:  "iLoud Right-regen-2.wav",
			outputBase: "iLoud Right-regen-2",
		},
		{
			inputFile:  "Cavern4Iloud.wav",
			outputBase: "Cavern4Iloud",
		},
	}

	// Process each sample rate and test case combination
	for _, rate := range sampleRates {
		for _, tc := range testCases {
			inputPath := filepath.Join(tempDir, tc.inputFile)
			outputPath := filepath.Join(outputDir, fmt.Sprintf("%s-%d-clean.wav", tc.outputBase, rate))
			ProcessWithSox(tempDir, inputPath, outputPath, rate, targetLevel, &ap)
			//ProcessImpulses(tempDir, inputPath, outputPath, rate, targetLevel, &ap)
		}
	}
}

func ProcessWithSox(tempDir string, FirWavFile string, OutputFile string, targetSampleRate int, targetLevel float64, ap *AudioProcessor) {
	errorText := "wavTest: "
	myResampler := foxResampler.NewResampler()
	myResampler.ToSampleRate = targetSampleRate
	myResampler.DebugOn = false
	myResampler.DebugFunc = ap.Logger.Debug
	myResampler.SOXPath = "C:\\Program Files\\Lyrion\\server\\Bin\\MSWin32-x64-multi-thread\\sox.exe"

	myImpulse, err := myResampler.ReadnResampleFile2Buffer(FirWavFile)
	if err != nil {
		ap.Logger.Warn(errorText + "Error loading impulse: " + err.Error())
	}

	targetBitDepth := 64

	// remove silence and background noise from mpulse
	ap.Impulse, err = CleanUpImpulse(myImpulse, targetSampleRate, -75.0, ap.Logger)
	if err != nil {
		ap.Logger.Warn(errorText + "Error loading impulse: " + err.Error())
	}

	ap.Logger.Debug("Backup Impulse: " + OutputFile)
	foxAudioEncoder.WriteWavFile(
		OutputFile,
		ap.Impulse,
		targetSampleRate,
		targetBitDepth,
		len(ap.Impulse),
		true, // i.e.  overwrite
		ap.Logger,
	)
}

func ProcessImpulses(tempDir string, FirWavFile string, OutputFile string, targetSampleRate int, targetLevel float64, ap *AudioProcessor) {

	myImpulse, err := localLoadImpulse(FirWavFile, targetSampleRate, targetLevel, ap.Logger)
	errorText := "wavTest: "
	if err != nil {
		ap.Logger.Warn(errorText + "Error loading impulse: " + err.Error())
	}

	targetBitDepth := 32

	// remove silence and background noise from mpulse
	ap.Impulse, err = CleanUpImpulse(myImpulse, targetSampleRate, -75.0, ap.Logger)
	if err != nil {
		ap.Logger.Warn(errorText + "Error loading impulse: " + err.Error())
	}

	ap.Logger.Debug("Backup Impulse: " + OutputFile)
	foxAudioEncoder.WriteWavFile(
		OutputFile,
		ap.Impulse,
		targetSampleRate,
		targetBitDepth,
		len(ap.Impulse),
		true, // i.e.  overwrite
		ap.Logger,
	)
}

func localLoadImpulse(inputFile string, targetSampleRate int, targetLevel float64, myLogger *foxLog.Logger) ([][]float64, error) {
	const functionName = "LoadImpulse"
	const MsgHeader = "wavTest: " + functionName + ": "
	myLogger.Debug(MsgHeader + " Loading impulse...")

	// Decode the audio file
	myFilterDecoder := new(foxAudioDecoder.AudioDecoder)
	impulseSamples, err := myFilterDecoder.LoadFiletoSampleBuffer(inputFile, "WAV", myLogger)
	if err != nil {
		return nil, fmt.Errorf("%s: decoder init failed: %v", functionName, err)
	}
	if myLogger.DebugEnabled {
		myLogger.Debug(MsgHeader + fmt.Sprintf("Impulse Decoder initialized: SampleRate=%d, Channels=%d, Type=%s", myFilterDecoder.SampleRate, myFilterDecoder.NumChannels, myFilterDecoder.Type))
	}
	if myFilterDecoder.SampleRate != targetSampleRate {
		myLogger.Debug(MsgHeader + "Resampling impulse...")

		myResampler := foxResampler.NewResampler()
		myResampler.FromSampleRate = myFilterDecoder.SampleRate
		myResampler.ToSampleRate = targetSampleRate
		myResampler.Quality = 60
		myResampler.DebugOn = false
		myResampler.DebugFunc = myLogger.Debug
		myResampler.InputSamples = impulseSamples

		err = myResampler.Resample()
		if err != nil {
			myLogger.Error(MsgHeader + "Resampling failed: " + err.Error())
			return nil, err
		}

		return myResampler.InputSamples, nil
	} else {
		myLogger.Debug(MsgHeader + "Impulse is already at target sample rate, no resampling needed.")
		return impulseSamples, nil
	}
}

// createTestLogger configures a test-aware logger
func createTestLogger(t *testing.T, tempDir string) *foxLog.Logger {
	logPath := filepath.Join(tempDir, "test.log")
	logger, err := foxLog.NewLogger(logPath, "", false)
	if err != nil {
		t.Fatalf("Logger creation failed: %v", err)
	}
	return logger
}

// function should remove any leading or trailing silence from the impulse
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

// function should remove any leading or trailing silence from the impulse

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

// Helper functions
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
