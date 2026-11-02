package main

import (
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Foxenfurter/foxAudioLib/foxAudioDecoder"
	"github.com/Foxenfurter/foxAudioLib/foxAudioEncoder"
	"github.com/Foxenfurter/foxAudioLib/foxNormalizer"
	"github.com/Foxenfurter/foxAudioLib/foxResampler"
)

// ProcessAudio reads a WAV file, resamples it, and writes it to a target folder.
// ... [previous imports and code]

func ProcessAudio(inputFile string, outputFile string, targetSampleRate int, targetBitDepth int) error {
	const functionName = "Main"
	fmt.Printf("Testing something foxAudioLib \n")
	startTime := time.Now()

	// Decode the audio file
	myDecoder := foxAudioDecoder.AudioDecoder{
		Type: "WAV",
	}
	myDecoder.Filename = inputFile
	err := myDecoder.Initialise()
	if err != nil {
		return fmt.Errorf("%s: decoder init failed: %v", functionName, err)
	}

	// Calculate new size using sample rate and bit depth ratios
	sizeRatio := (float64(targetSampleRate) / float64(myDecoder.SampleRate)) *
		(float64(targetBitDepth) / float64(myDecoder.BitDepth))
	newSize := int64(float64(myDecoder.Size) * sizeRatio)

	// Initialize Audio Encoder
	myEncoder := foxAudioEncoder.AudioEncoder{
		Type:        "Wav",
		SampleRate:  targetSampleRate,
		BitDepth:    targetBitDepth, // Use targetBitDepth, not myDecoder.BitDepth
		NumChannels: myDecoder.NumChannels,
		Size:        newSize,
		Filename:    outputFile,
	}

	fmt.Println("Test: Output file: ", myEncoder.Filename)
	fmt.Println("Test: Creating new Encoder SampleRate: ", myEncoder.SampleRate, " Channels: ", myEncoder.NumChannels, " BitDepth: ", myEncoder.BitDepth)

	err = myEncoder.Initialise()
	if err != nil {
		panic(err)
	}

	myResampler := foxResampler.NewResampler()
	myResampler.FromSampleRate = myDecoder.SampleRate
	myResampler.ToSampleRate = myEncoder.SampleRate
	myResampler.Quality = 10

	var WG sync.WaitGroup
	DecodedSamplesChannel := make(chan [][]float64, 10000)
	ResampledChannel := make(chan [][]float64, 10000)

	fmt.Println("Test: Decoding Data...")
	WG.Add(1)
	go func() {
		defer func() {
			WG.Done()
			// We are done so close the channel
			//os.Stdin.Sync()
			close(DecodedSamplesChannel)
		}()
		myDecoder.DecodeSamples(DecodedSamplesChannel, nil)
		//close(DecodedSamplesChannel) // Close the channel after decoding
	}()

	WG.Add(1)
	go func() {
		defer WG.Done()
		minGains := make([]float64, 0)
		minGainLocations := make([]int, 0)
		for samples := range DecodedSamplesChannel {
			// Initialize to -1 to indicate no gain found yet
			if len(minGains) == 0 {
				minGains = make([]float64, len(samples))
				minGainLocations = make([]int, len(samples))
				for i := range minGainLocations {
					minGainLocations[i] = -1
					minGains[i] = 1.0
				}
			}
			for channelIndex, channelSamples := range samples {
				for sampleIndex, sample := range channelSamples {
					sampleAbs := math.Abs(sample)
					if sampleAbs < minGains[channelIndex] && sampleAbs != 0.0 {
						minGains[channelIndex] = sampleAbs
						minGainLocations[channelIndex] = sampleIndex
					}
				}
			}
			myResampler.InputSamples = samples
			err := myResampler.Resample()
			if err != nil {
				fmt.Printf("Resampling failed: %v\n", err)
				continue
			}
			ResampledChannel <- myResampler.InputSamples
		}

		fmt.Println("Max Gains:", minGains)
		fmt.Println("Max Gain Locations:", minGainLocations)
		close(ResampledChannel) // Close the channel after resampling
	}()

	fmt.Println("Test: Encoding Data...")
	//WG.Add(1)
	//go func() {
	//	defer WG.Done()
	outputSamples := make([][]float64, myDecoder.NumChannels)
	for i := range outputSamples {
		outputSamples[i] = make([]float64, 0)
	}
	fmt.Println("Test: Structure built now build output Samples...")
	for samples := range ResampledChannel {
		for channelIdx, channelData := range samples {
			outputSamples[channelIdx] = append(outputSamples[channelIdx], channelData...)
		}
	}
	fmt.Println("Test: Ready to Normalize...")
	targetLevel := 0.89
	targetLevel = foxNormalizer.TargetGainCSharp(myDecoder.SampleRate, myEncoder.SampleRate, targetLevel)
	fmt.Println("Test: Target Gain: ", targetLevel)
	foxNormalizer.Normalize(outputSamples, targetLevel)
	fmt.Println("Test: Outputting...")
	myEncoder.EncodeData(outputSamples)
	//}()

	fmt.Println("Test: Waiting...")
	WG.Wait()

	//myDecoder.Close()

	// Fix elapsedTime declaration order
	elapsedTime := int(time.Since(startTime).Milliseconds())

	println("\n============================================================================================")
	println("AudioLib: Incremental (ms): ", elapsedTime)
	println("============================================================================================\n")

	return nil
} // <-- ProcessAudio ends here

func TestProcessAudio(t *testing.T) {
	inputFiles := []string{
		"C:\\temp\\InputFilters\\Cavern4Iloud_44k.wav",
		"C:\\temp\\InputFilters\\Test_filter-44k.wav",
		//	"C:\\temp\\InputFilters\\Test_filter-96k.wav",
		"C:\\temp\\InputFilters\\Opera_with_Sub_REW_20230303.wav",
		"C:\\temp\\InputFilters\\iloudSubMini_48k.wav",
		"C:\\temp\\InputFilters\\96000_Impulses_Cavern4Iloud.wav",
	}
	targetSampleRates := []int{96000, 44100, 48000, 192000, 88000}
	for _, inputFile := range inputFiles {
		for _, targetSampleRate := range targetSampleRates {
			outputFile := "C:\\temp\\OutputFilters\\" + strings.TrimSuffix(filepath.Base(inputFile), filepath.Ext(inputFile)) + "_" + strconv.Itoa(targetSampleRate) + "k.wav"
			err := ProcessAudio(inputFile, outputFile, targetSampleRate, 16)
			if err != nil {
				t.Fatalf("Test failed for %s at %d: %v", inputFile, targetSampleRate, err)
			}
		}
	}
}
