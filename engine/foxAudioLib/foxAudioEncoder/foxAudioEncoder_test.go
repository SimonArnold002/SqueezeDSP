package foxAudioEncoder_test

import (
	"fmt"
	"math/rand"
	"os"
	"sync"
	"testing"

	"github.com/Foxenfurter/foxAudioLib/foxAudioEncoder" // Import your package
)

func TestEncoderAsyncBardNew(t *testing.T) {
	// Encoder configuration
	myEncoder := foxAudioEncoder.AudioEncoder{
		Type:        "Wav",
		SampleRate:  44100,
		BitDepth:    16, // Adjust as needed
		NumChannels: 2,
		Size:        int64(44100 * 5), // Size for a 5-second file (adjust as needed)
		Filename:    "NewFunkyTest.wav",
	}
	fmt.Println("Test: Creating new Encoder...")
	// Create encoder
	err := myEncoder.Initialise()
	if err != nil {
		fmt.Println("Test: panic on New Header...")
		panic(err)
	}
	// Number of samples each buffer should hold will actually be numChannels * BufferSize
	BufferSize := 1024
	// Create a channel for sample communication
	samplesChan := make(chan [][]float64)
	// Initialize WaitGroup and add for sample generation goroutine
	wg := sync.WaitGroup{}
	wg.Add(1) // Add for the sample generation goroutine
	// Start sample generation asynchronously
	fmt.Println("Test: Start sample generation asynchronously...")
	go func() {
		numChannels := myEncoder.NumChannels
		numSamplesPerCall := BufferSize // Adjust as needed
		dataSize := myEncoder.Size

		for dataSize > 0 {
			fmt.Println("Test: generate sample...")
			samples := generateSamples(numSamplesPerCall, numChannels)
			samplesChan <- samples

			dataSize -= int64(numSamplesPerCall * numChannels * 4) // Assuming 4 bytes per sample
		}

		close(samplesChan) // Signal completion
		wg.Done()          // Signal completion
	}()

	// Encode and write samples as they arrive
	for samples := range samplesChan {
		// Add for each sample group encoding
		wg.Add(1)

		go func(samples [][]float64) {
			fmt.Println("Test: Encode and write sample...")
			err := myEncoder.EncodeData(samples) // Write handled within EncodeData
			if err != nil {
				// Handle the error appropriately
				fmt.Println("Test: Error Encoding Data")
				panic(err) // Adjust error handling as needed
			}
			wg.Done() // Decrement after successful encoding
		}(samples)
	}

	wg.Wait() // Wait for all encoding and writing to finish

	// Add test assertions here to verify the encoded audio file
	// For example, you could check the file size, header contents, or sample values
}

func TestMain(m *testing.M) {
	// Run tests
	fmt.Println("Running Encoder  Test:")
	exitCode := m.Run()

	// Exit with the test result code
	os.Exit(exitCode)
}

func generateSamples(numSamples int, numChannels int) [][]float64 {
	samples := make([][]float64, numChannels) // Create numChannels slices for samples
	for i := 0; i < numChannels; i++ {
		samples[i] = make([]float64, numSamples) // Allocate space for numSamples in each channel
	}

	for i := 0; i < numSamples; i++ {
		for j := 0; j < numChannels; j++ {
			samples[j][i] = rand.Float64()*2 - 1 // Generate sample for each channel
		}
	}
	return samples
}
