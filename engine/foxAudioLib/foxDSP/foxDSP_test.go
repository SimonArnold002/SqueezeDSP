package foxDSP_test

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Foxenfurter/foxAudioLib/foxDSP"

	"github.com/Foxenfurter/foxAudioLib/foxAudioEncoder" // Import your package
)

func TestDSP(t *testing.T) {
	sampleRate := 96000
	bitDepth := 24
	testImpulseFilename := "c:/temp/Testfilter75_go.wav"
	dtStartRun := time.Now()
	var Fc int = 20000
	Nyquist := 0.445
	length := 8000
	//NyQ := float64(Fc) / float64(sampleRate)
	NyR := float64(Fc) / float64(sampleRate)

	if NyR > Nyquist {
		// Need to add in Error Logger here.
		Fc = int(float64(sampleRate) * Nyquist)
	}
	coefficients := foxDSP.CalcBiquadFilter("lowpass", 1000, 44100, 0.5, 0.1, "Q")
	if len(coefficients) != 6 {
		t.Errorf("Expected 6 coefficients, got %d", len(coefficients))
	}
	var filters [][]float64
	//filters = append(filters, coefficients)
	// Matches 82.5 - 75 phon
	filters = append(filters, foxDSP.CalcBiquadFilter("lowshelf", 80, sampleRate, 1.8, 0.41, "Q"))
	filters = append(filters, foxDSP.CalcBiquadFilter("highshelf", 6300, sampleRate, 0.25, 0.41, "Q"))
	filters = append(filters, foxDSP.CalcBiquadFilter("peak", 2400, sampleRate, -1.8, 0.17, "Q"))
	filters = append(filters, foxDSP.CalcBiquadFilter("lowshelf", 35, sampleRate, 0.25, 1.25, "Q"))
	filters = append(filters, foxDSP.CalcBiquadFilter("peak", 1200, sampleRate, 0.25, 1.4, "Q"))
	fmt.Printf("Number of filters created: %v\n", filters)
	fmt.Printf("now generate impulses: \n")
	TestImpulseResponse := make([][]float64, 0)

	TestImpulseResponse = append(TestImpulseResponse, foxDSP.CascadeFilters(filters, length))
	//TestImpulseResponse = append(TestImpulseResponse, newImpulse)
	fmt.Printf("impulse created - length: %v \n", len(TestImpulseResponse[0]))

	// Create a StreamManager instance
	streamManager := foxDSP.StreamManager{
		InputImpulse: TestImpulseResponse,
	}

	// Call ExportWavFile to export the WAV file
	err := foxDSP.ExportWavFile(testImpulseFilename, sampleRate, bitDepth, &streamManager)
	if err != nil {
		fmt.Println("Error exporting WAV file:", err)
	} else {
		fmt.Println("WAV file exported successfully!")
	}

	//maxWav := foxDSP.WriteWavFile(testImpulseFilename, TestImpulseResponse, sampleRate, bitDepth)
	//fmt.Printf("Wav  created: %s %v %v %v \n", testImpulseFilename, sampleRate, bitDepth, maxWav)

	fmt.Printf("Time taken: %s\n", time.Since(dtStartRun))
}

func TestDSPEncode(t *testing.T) {
	sampleRate := 48000
	bitDepth := 16
	numChannels := 1
	testImpulseFilename := "c:/temp/TestFilterUsingEncoder.wav"
	dtStartRun := time.Now()
	var Fc int = 20000
	Nyquist := 0.445
	impulseLength := 8000
	//NyQ := float64(Fc) / float64(sampleRate)
	NyR := float64(Fc) / float64(sampleRate)

	if NyR > Nyquist {
		// Need to add in Error Logger here.
		Fc = int(float64(sampleRate) * Nyquist)
	}
	coefficients := foxDSP.CalcBiquadFilter("lowpass", 1000, 44100, 0.5, 0.1, "Q")
	if len(coefficients) != 6 {
		t.Errorf("Expected 6 coefficients, got %d", len(coefficients))
	}
	var filters [][]float64
	//filters = append(filters, coefficients)
	// Matches 82.5 - 75 phon
	filters = append(filters, foxDSP.CalcBiquadFilter("lowshelf", 80, sampleRate, 1.8, 0.41, "Q"))
	filters = append(filters, foxDSP.CalcBiquadFilter("highshelf", 6300, sampleRate, 0.25, 0.41, "Q"))
	filters = append(filters, foxDSP.CalcBiquadFilter("peak", 2400, sampleRate, -1.8, 0.17, "Q"))
	filters = append(filters, foxDSP.CalcBiquadFilter("lowshelf", 35, sampleRate, 0.25, 1.25, "Q"))
	filters = append(filters, foxDSP.CalcBiquadFilter("peak", 1200, sampleRate, 0.25, 1.4, "Q"))
	fmt.Printf("Number of filters created: %v\n", filters)
	fmt.Printf("now generate impulses: \n")
	TestImpulseResponse := make([][]float64, 0)

	TestImpulseResponse = append(TestImpulseResponse, foxDSP.CascadeFilters(filters, impulseLength))
	//TestImpulseResponse = append(TestImpulseResponse, newImpulse)
	fmt.Printf("impulse created - length: %v \n", len(TestImpulseResponse[0]))
	dataSize := impulseLength * numChannels * (bitDepth / 8)

	myEncoder := foxAudioEncoder.AudioEncoder{
		Type:        "Wav",
		SampleRate:  sampleRate,
		BitDepth:    bitDepth, // Adjust as needed
		NumChannels: 1,
		Size:        int64(dataSize), // Size for a 5-second file (adjust as needed)
		Filename:    testImpulseFilename,
	}
	fmt.Println("Test: Creating new Encoder...")
	// Create encoder
	err := myEncoder.Initialise()
	if err != nil {
		fmt.Println("Test: panic on New Header...")
		panic(err)
	}
	// test sending output in 2 different chunks
	halfLength := len(TestImpulseResponse[0]) / 2
	fmt.Println("Test: slicing impulse", halfLength)

	// Create two separate slices, each representing a 2D array
	firstHalf := make([][]float64, len(TestImpulseResponse))
	secondHalf := make([][]float64, len(TestImpulseResponse))

	for i := range TestImpulseResponse {
		firstHalf[i] = TestImpulseResponse[i][:halfLength]
		secondHalf[i] = TestImpulseResponse[i][halfLength:]
	}

	fmt.Println("Test: sending first chunk of impulse")
	err = myEncoder.EncodeData(firstHalf)
	if err != nil {
		fmt.Println("Test: panic on New Encoder...")
		panic(err)
	}
	err = myEncoder.EncodeData(secondHalf)

	if err != nil {
		fmt.Println("Test: panic on 2nd Encoder...")
		panic(err)
	}

	fmt.Printf("Time taken: %s\n", time.Since(dtStartRun))
}

func TestMain(m *testing.M) {
	// Run tests
	fmt.Println("Running DSP Test:")
	exitCode := m.Run()

	// Exit with the test result code
	os.Exit(exitCode)
}
