package foxPEQ_test

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Foxenfurter/foxAudioLib/foxAudioEncoder" // Import your package
	"github.com/Foxenfurter/foxAudioLib/foxDSP"

	"github.com/Foxenfurter/foxAudioLib/foxPEQ"
)

func TestPEQBiquad(t *testing.T) {
	println("Testing Biquad with foxPEQ")
	sampleRate := 96000
	bitDepth := 24
	testImpulseFilename := "c:/temp/TestBiquadUsingFoxPEQ.wav"
	dtStartRun := time.Now()

	//NyQ := float64(Fc) / float64(sampleRate)

	var err error

	//filters = append(filters, coefficients)
	// Matches 82.5 - 75 phon
	myPEQFilter := foxPEQ.NewPEQFilter(sampleRate, 15)

	err = myPEQFilter.CalcBiquadFilter("lowshelf", 80, 1.8, 0.41, "Q")
	if err != nil {
		fmt.Printf("Invalid filter definitio, %s \n", err.Error())
	}
	err = myPEQFilter.CalcBiquadFilter("highshelf", 6300, 0.25, 0.41, "Q")
	if err != nil {
		fmt.Printf("Invalid filter definitio, %s \n", err.Error())
	}
	err = myPEQFilter.CalcBiquadFilter("peak", 2400, -1.8, 0.17, "Q")
	if err != nil {
		fmt.Printf("Invalid filter definitio, %s \n", err.Error())
	}
	err = myPEQFilter.CalcBiquadFilter("lowshelf", 35, 0.25, 1.25, "Q")
	if err != nil {
		fmt.Printf("Invalid filter definitio, %s \n", err.Error())
	}
	err = myPEQFilter.CalcBiquadFilter("peak", 1200, 0.25, 1.4, "Q")
	if err != nil {
		fmt.Printf("Invalid filter definitio, %s \n", err.Error())
	}

	fmt.Printf("Number of filters created: %v\n", len(myPEQFilter.FilterCoefficients))
	fmt.Printf("now generate impulses: \n")

	myPEQFilter.GenerateFilterImpulse()
	//TestImpulseResponse = append(TestImpulseResponse, newImpulse)
	fmt.Printf("impulse created - length: %v \n", len(myPEQFilter.Impulse))

	// Create a StreamManager instance

	impulseLength := len(myPEQFilter.Impulse)
	fmt.Println("Copy Impulse")
	InputImpulse := [][]float64{myPEQFilter.Impulse}
	fmt.Println("calculate data Size")
	dataSize := impulseLength * 1 * (bitDepth / 8)
	fmt.Println("Setting up Encoder")
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
	err = myEncoder.Initialise()
	if err != nil {
		fmt.Println("Test: panic on New Header...")
		panic(err)
	}
	err = myEncoder.EncodeData(InputImpulse)
	if err != nil {
		fmt.Println("Test: panic on New Encoder...")
		panic(err)
	}

	fmt.Printf("Wav  created: %s %v %v \n", testImpulseFilename, sampleRate, bitDepth)

	fmt.Printf("Time taken: %s\n", time.Since(dtStartRun))
}

func TestPEQLoudness(t *testing.T) {
	println("Testing Loudness with foxPEQ")
	sampleRate := 96000
	bitDepth := 24

	dtStartRun := time.Now()
	listeningLevelInPhon := 75.0
	testImpulseFilename := "c:/temp/TestLoudnessUsingFoxPEQ.wav"
	var err error

	myPEQFilter := foxPEQ.NewPEQFilter(sampleRate, 15)

	myLoudnessFilter := foxPEQ.NewLoudness()
	myLoudnessFilter.PlaybackPhon = listeningLevelInPhon

	dfpl, err := myLoudnessFilter.DifferentialSPL(1.0)
	if err != nil {
		fmt.Printf("DifferentialSPL error unable to proceed - %s", err.Error())
		return
	}
	err = myPEQFilter.GenerateEQLoudnessFilter(dfpl)
	if err != nil {
		fmt.Printf("Invalid filter definitio, %s \n", err.Error())
		return
	}
	myPEQFilter.GenerateFilterImpulse()

	//TestImpulseResponse = append(TestImpulseResponse, newImpulse)
	fmt.Printf("impulse created - length: %v \n", len(myPEQFilter.Impulse))

	// Create a StreamManager instance

	impulseLength := len(myPEQFilter.Impulse)
	fmt.Println("Copy Impulse")
	InputImpulse := [][]float64{myPEQFilter.Impulse}
	fmt.Println("calculate data Size")
	dataSize := impulseLength * 1 * (bitDepth / 8)
	fmt.Println("Setting up Encoder")
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
	err = myEncoder.Initialise()
	if err != nil {
		fmt.Println("Test: panic on New Header...")
		panic(err)
	}
	err = myEncoder.EncodeData(InputImpulse)
	if err != nil {
		fmt.Println("Test: panic on New Encoder...")
		panic(err)
	}

	fmt.Printf("Wav  created: %s %v %v \n", testImpulseFilename, sampleRate, bitDepth)

	fmt.Printf("Time taken: %s\n", time.Since(dtStartRun))
}

func TestDSPPEQ(t *testing.T) {
	println("Testing DSP with foxPEQ")
	sampleRate := 96000
	bitDepth := 24
	testImpulseFilename := "c:/temp/TestFilterUsingFoxPEQ.wav"
	dtStartRun := time.Now()

	//length := 8000
	//NyQ := float64(Fc) / float64(sampleRate)

	var err error

	//filters = append(filters, coefficients)
	// Matches 82.5 - 75 phon
	myPEQFilter := foxPEQ.NewPEQFilter(sampleRate, 15)
	//myPEQFilter.FilterLength = length
	//myPEQFilter.SampleRate = sampleRate

	err = myPEQFilter.CalcBiquadFilter("lowshelf", 80, 1.8, 0.41, "Q")
	if err != nil {
		fmt.Printf("Invalid filter definitio, %s \n", err.Error())
	}
	err = myPEQFilter.CalcBiquadFilter("highshelf", 6300, 0.25, 0.41, "Q")
	if err != nil {
		fmt.Printf("Invalid filter definitio, %s \n", err.Error())
	}
	err = myPEQFilter.CalcBiquadFilter("peak", 2400, -1.8, 0.17, "Q")
	if err != nil {
		fmt.Printf("Invalid filter definitio, %s \n", err.Error())
	}
	err = myPEQFilter.CalcBiquadFilter("lowshelf", 35, 0.25, 1.25, "Q")
	if err != nil {
		fmt.Printf("Invalid filter definitio, %s \n", err.Error())
	}
	err = myPEQFilter.CalcBiquadFilter("peak", 1200, 0.25, 1.4, "Q")
	if err != nil {
		fmt.Printf("Invalid filter definitio, %s \n", err.Error())
	}

	fmt.Printf("Number of filters created: %v\n", len(myPEQFilter.FilterCoefficients))
	fmt.Printf("now generate impulses: \n")

	myPEQFilter.GenerateFilterImpulse()
	//TestImpulseResponse = append(TestImpulseResponse, newImpulse)
	fmt.Printf("impulse created - length: %v \n", len(myPEQFilter.Impulse))

	// Create a StreamManager instance

	impulseLength := len(myPEQFilter.Impulse)
	fmt.Println("Copy Impulse")
	InputImpulse := [][]float64{myPEQFilter.Impulse}
	fmt.Println("calculate data Size")
	dataSize := impulseLength * 1 * (bitDepth / 8)
	fmt.Println("Setting up Encoder")
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
	err = myEncoder.Initialise()
	if err != nil {
		fmt.Println("Test: panic on New Header...")
		panic(err)
	}
	err = myEncoder.EncodeData(InputImpulse)
	if err != nil {
		fmt.Println("Test: panic on New Encoder...")
		panic(err)
	}

	fmt.Printf("Wav  created: %s %v %v \n", testImpulseFilename, sampleRate, bitDepth)

	fmt.Printf("Time taken: %s\n", time.Since(dtStartRun))
}

func TestDSPEncode(t *testing.T) {
	println("Testing DSP with foxDSP")
	sampleRate := 96000
	bitDepth := 24
	numChannels := 1
	testImpulseFilename := "c:/temp/TestFilterUsingFoxDSP.wav"
	dtStartRun := time.Now()

	impulseLength := 8000

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
