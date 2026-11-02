package foxWavEncoder_test

import (
	"fmt"
	"math"
	"reflect"
	"testing"

	foxWavEncoder "github.com/Foxenfurter/foxAudioLib/foxAudioEncoder/foxWavEncoder"
)

func TestFoxWAVEncoder(t *testing.T) {
	// Create a FoxWAVEncoder instance for testing
	encoder := &foxWavEncoder.FoxEncoder{
		SampleRate:  44100,
		BitDepth:    16,
		NumChannels: 2,
		Size:        0,
	}

	// Test EncodeWavHeader
	headerResult, errH := encoder.EncodeHeader()
	if errH != nil {
		t.Fatalf("EncodeWavHeader failed with error: %v", errH)
	}
	fmt.Println("Header Byte array:", headerResult)

	// Create a sample buffer with 2 channels, sampled at 44.1 kHz
	sampleRate := float64(encoder.SampleRate)
	durationSeconds := 3
	numChannels := encoder.NumChannels
	numSamples := int(sampleRate) * durationSeconds
	numSamples = 2
	buffer := make([][]float64, numChannels)
	for i := range buffer {
		buffer[i] = make([]float64, numSamples)
	}

	// Generate a simple sine wave for each channel
	frequency1 := 440.0 // 440 Hz
	frequency2 := 880.0 // 880 Hz

	for t := 0; t < numSamples; t++ {
		buffer[0][t] = 0.5 * math.Sin(2.0*math.Pi*frequency1*float64(t)/sampleRate)
		buffer[1][t] = 0.5 * math.Sin(2.0*math.Pi*frequency2*float64(t)/sampleRate)
	}

	// Define the expected PCM data for 16-bit, little-endian
	expectedPCMData := []byte{
		// Channel 1 samples
		0x00, 0x00, // 0 (16-bit)
		0x92, 0x30, // Sine wave at 440 Hz
		//0x24, 0x62,
		// ... more samples ...

		// Channel 2 samples
		0x00, 0x00, // 0 (16-bit)
		0x24, 0xC5, // Sine wave at 880 Hz
		//0x49, 0x8A,
		// ... more samples ...
	}

	// Convert the buffer to 16-bit PCM data
	//targetBitDepth := 16
	pcmData, err := encoder.EncodeData(buffer)

	if err != nil {
		t.Fatalf("Error converting to PCM: %v", err)
	}
	//pcmData := actualResult.EncodedBuffer
	// Compare the result with the expected PCM data

	fmt.Println("Expected Byte array: ", expectedPCMData)
	fmt.Println("Actual Byte array: ", pcmData)

	if !reflect.DeepEqual(pcmData, expectedPCMData) {
		t.Errorf("PCM data does not match the expected result.")
	}

	// Print some information
	//fmt.Printf("Test passed! PCM data matches the expected result.\n")

	// Test EncodeWavData
	// Create a buffer of test samples
	/*
		testSamples := [][]float64{0.5, -0.7, 0.8, -0.2}

		// Set the expected result manually for testing purposes
		expectedResult := foxWavEncoder.EncodedResult{

			EncodedBuffer: []byte{
				82, 73, 70, 70, 36, 0, 0, 0, 87, 65, 86, 69, 16, 0, 0, 0, 1, 0, 2, 0,
				68, 172, 0, 0, 16, 177, 2, 0, 4, 0, 16, 0, 100, 97, 116, 97, 0, 0, 0, 0,
			},
			Err: nil,
		}

		// hardcoded as per Bard
		//var expectedResult []byte = []byte{0x32, 0x7f, 0x32, 0x7f, 0x80, 0x8c, 0x80, 0x8c, 0xd0, 0x7f, 0xd0, 0x7f, 0xf0, 0xfd, 0xf0, 0xfd}
		dataResult := encoder.EncodePCMData(testSamples, encoder.NumChannels)

		fmt.Println("Expected Byte array:", expectedResult.EncodedBuffer)
		fmt.Println("Actual Byte array:", dataResult.EncodedBuffer)
		// Compare the actual result with the expected result
		if !bytes.Equal(dataResult.EncodedBuffer, expectedResult.EncodedBuffer) {
			//if !bytes.Equal(dataResult.EncodedBuffer, expectedResult.EncodedBuffer) {
			t.Errorf("EncodeWavData result does not match expected result")
		}

		if dataResult.Err != nil {
			t.Fatalf("EncodeWavData failed with error: %v", dataResult.Err)
		}
	*/
}
