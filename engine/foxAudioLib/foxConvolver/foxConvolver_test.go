package foxConvolver_test

import (
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Foxenfurter/foxAudioLib/foxAudioDecoder"
	"github.com/Foxenfurter/foxAudioLib/foxAudioEncoder" // Import your package
	"github.com/Foxenfurter/foxAudioLib/foxConvolver"
	"github.com/Foxenfurter/foxAudioLib/foxNormalizer"
	"github.com/Foxenfurter/foxAudioLib/foxPEQ"
	"github.com/Foxenfurter/foxAudioLib/foxResampler"

	goDSP "github.com/mjibson/go-dsp/fft"
	Sci "scientificgo.org/fft"
)

func TestFoxFFT(t *testing.T) {
	// Create a list of complex arrays
	inputs := [][]complex128{
		{1 + 1i, 2 + 2i, 3 + 3i, 4 + 4i},
		{1 + 1i, 1 + 1i, 1 + 1i, 1 + 1i},
		{1 + 2i, 2 + 3i, 3 + 4i, 4 + 5i},
	}

	// Expected FFT results
	expected := [][]complex128{
		{10 + 10i, -2 - 2i, -2 - 2i, -2 - 2i},
		{4 + 4i, 0 + 0i, 0 + 0i, 0 + 0i},
		{10 + 14i, -2 - 2i, -2 - 2i, -2 - 2i},
	}
	//fftSize := 4
	//fft := foxConvolver.NewFft64(fftSize)

	for i, original := range inputs {
		println("Testing Fox  FFT")
		fmt.Printf("Input: %v\n", original)
		fmt.Printf("Expected FFT result: %v\n", expected[i])
		// Compute the FFT
		fftResult := foxConvolver.TukeyFFT(original)

		fmt.Printf("Actual FFT result: %v\n", fftResult)
		// Compute the inverse FFT
		ifftResult := foxConvolver.InverseTukeyFFT(fftResult)

		// Compare the original array with the result of the IFFT
		for j, v := range ifftResult {
			// Due to numerical precision issues, we need to round the result
			vRounded := complex(round(real(v)), round(imag(v)))
			if original[j] != vRounded {
				fmt.Printf("Mismatch at index %d: expected %v, got %v\n", j, original[j], vRounded)
			}
		}
		fmt.Println()
	}
}

func TestGoDSPFFT(t *testing.T) {
	// Create a list of complex arrays
	inputs := [][]complex128{
		{1 + 1i, 2 + 2i, 3 + 3i, 4 + 4i},
		{1 + 1i, 1 + 1i, 1 + 1i, 1 + 1i},
		{1 + 2i, 2 + 3i, 3 + 4i, 4 + 5i},
	}

	// Expected FFT results
	expected := [][]complex128{
		{10 + 10i, -2 - 2i, -2 - 2i, -2 - 2i},
		{4 + 4i, 0 + 0i, 0 + 0i, 0 + 0i},
		{10 + 14i, -2 - 2i, -2 - 2i, -2 - 2i},
	}

	for i, original := range inputs {
		println("Testing GoDSP  FFT")
		// Compute the FFT
		//"github.com/mjibson/go-dsp/fft"
		fftResult := goDSP.FFT(original)

		fmt.Printf("Input: %v\n", original)
		fmt.Printf("Expected FFT result: %v\n", expected[i])
		fmt.Printf("Actual FFT result: %v\n", fftResult)

		//"github.com/mjibson/go-dsp/fft"
		ifftResult := goDSP.IFFT(fftResult)

		// Compare the original array with the result of the IFFT
		for j, v := range ifftResult {
			// Due to numerical precision issues, we need to round the result
			vRounded := complex(round(real(v)), round(imag(v)))
			if original[j] != vRounded {
				fmt.Printf("Mismatch at index %d: expected %v, got %v\n", j, original[j], vRounded)
			}
		}
		fmt.Println()
	}
}

func TestScientificFFT(t *testing.T) {
	// Create a list of complex arrays
	inputs := [][]complex128{
		{1 + 1i, 2 + 2i, 3 + 3i, 4 + 4i},
		{1 + 1i, 1 + 1i, 1 + 1i, 1 + 1i},
		{1 + 2i, 2 + 3i, 3 + 4i, 4 + 5i},
	}

	// Expected FFT results
	expected := [][]complex128{
		{10 + 10i, -2 - 2i, -2 - 2i, -2 - 2i},
		{4 + 4i, 0 + 0i, 0 + 0i, 0 + 0i},
		{10 + 14i, -2 - 2i, -2 - 2i, -2 - 2i},
	}

	for i, original := range inputs {
		println("Testing Scientific FFT")
		// Compute the FFT
		//	"scientificgo.org/fft"
		fftResult := Sci.Fft(original, false)

		fmt.Printf("Input: %v\n", original)
		fmt.Printf("Expected FFT result: %v\n", expected[i])
		fmt.Printf("Actual FFT result: %v\n", fftResult)

		// Compute the inverse FFT
		//	"scientificgo.org/fft"
		ifftResult := Sci.Fft(fftResult, true)

		// Compare the original array with the result of the IFFT
		for j, v := range ifftResult {
			// Due to numerical precision issues, we need to round the result
			vRounded := complex(round(real(v)), round(imag(v)))
			if original[j] != vRounded {
				fmt.Printf("Mismatch at index %d: expected %v, got %v\n", j, original[j], vRounded)
			}
		}
		fmt.Println()
	}
}

func round(f float64) float64 {
	if f < 0 {
		return float64(int(f - 0.5))
	}
	return float64(int(f + 0.5))
}

func TestPEQConvolveVsImpulse(t *testing.T) {
	println("Testing PEQ Convolve Vs Impulese")
	sampleRate := 176400
	bitDepth := 24

	dtStartRun := time.Now()

	var err error
	// abit OTT for testing a convolver. But simplistic tests won't cut it.
	// create an filter impulse and then run it vs an imported impules.
	// Export the results
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

	/* Examples using internal Resampler*/

	myInternalDecoder := &foxAudioDecoder.AudioDecoder{
		//Filename: "c:\\temp\\Pencil_1644.wav", // Replace with your test file
		//Filename: "C:\\Users\\jonat\\Music\\Resolution\\Pencil_1644.wav",
		//Filename: "C:\\Users\\jonat\\Music\\Resolution\\Pencil_24192.wav",
		//Filename: "c:\\temp\\Opera_with_Sub_REW_96k.wav",
		//Filename: "c:\\temp\\96000_0069465CE0_Impulses_Cavern4Opera.wav",
		Filename: "c:\\temp\\CavernLeft.wav", // Replace with your test file
		//Filename: "c:\\temp\\OperaExperiment-192k.wav",
		//Filename: "c:\\temp\\96000_Impulses_Cavern4Iloud.wav",
		//Filename: "c:\\temp\\Loudness_yz.wav", // Replace with your test file

		Type: "Wav",
	}

	fmt.Println("Test: Decoding input file... ", myInternalDecoder.Filename)
	err = myInternalDecoder.Initialise()
	if err != nil {
		t.Fatalf("Test: Error initializing AudioDecoder: %v", err)
	}

	var WG sync.WaitGroup
	DecodedSamplesChannel := make(chan [][]float64, 10)
	WG.Add(1)
	go func() {
		defer WG.Done()

		myInternalDecoder.DecodeSamples(DecodedSamplesChannel)
	}()

	// Close the doneProcessing channel to signal that decoding is finished
	fmt.Println("Test: Setting up Convolver")
	myConvolver := foxConvolver.NewConvolver(myPEQFilter.Impulse)
	myConvolvedSignal := make([]float64, 0)
	myResampler := foxResampler.NewResampler()
	// these values do not change
	myResampler.FromSampleRate = myInternalDecoder.SampleRate
	myResampler.ToSampleRate = sampleRate
	myResampler.Quality = 30
	//Internal
	targetLevel := foxNormalizer.TargetGain(myInternalDecoder.SampleRate, int(sampleRate), .89)

	//Test zeozeozeo resampler
	if err != nil {
		panic(err)
	}
	var copyRawImpulse []float64
	WG.Add(1)
	go func() {
		var ResultCounter int

		//var tempConv []float64
		defer WG.Done()
		for decodedResult := range DecodedSamplesChannel {
			// Discard decoded results
			// this is where we convolve Dude!

			ResultCounter += len(decodedResult[0])
			//fmt.Println("Test: Convolving", ResultCounter)
			//resamples in place
			// quality 30 is pretty good
			myResampler.InputSamples = decodedResult
			err = myResampler.Resample()
			if err != nil {
				println("Error resampling: ", err.Error())
				return
			}

			//myConvolvedSignal = foxConvolver.ConvolveImpulsesSlow(myConvolver.Impulse, decodedResult[0])

			//myConvolvedSignal = foxConvolver.ConvolveImpulsesSimple(decodedResult[0], myConvolver.Impulse)

			// Build up the result by appending the samples from the channel
			//myConvolvedSignal = append(myConvolvedSignal, myConvolver.ConvolveImpulsesFFT(decodedResult[0])[:]...)

			//seems to be working
			//tempConv = myConvolver.ConvolveImpulseOverlapSave(decodedResult[0])
			//tempConv = myConvolver.ConvolveImpulsesFFT(decodedResult[0])

			myConvolvedSignal = append(myConvolvedSignal, myConvolver.ConvolveFFT(decodedResult[0])...)
			//myConvolvedSignal = append(myConvolvedSignal, myConvolver.ConvolveImpulseOverlapSave(decodedResult[0])...)
			copyRawImpulse = append(copyRawImpulse, (decodedResult[0])[:]...)

		}

		fmt.Println("Test: Not waiting anymore", ResultCounter)

	}()
	fmt.Println("Test: Are we still waiting")
	// Wait for the asynchronous decoding to complete

	WG.Wait()
	fmt.Println("Test: closing decoder")

	//myDecoder.Close()

	fmt.Println("Setting up Encoder: ", targetLevel)
	OutputSignal := [][]float64{myConvolvedSignal}
	//Normalisation here lowers the level of 24 bit and drops a bit from output
	_ = foxNormalizer.NormalizePeak(OutputSignal, targetLevel)
	fmt.Println("calculate data Size")
	dataSize := len(OutputSignal[0]) * len(OutputSignal) * (bitDepth / 8)
	testImpulseFilename := "c:/temp/ConvolvedImpulse2.wav"
	myEncoder := foxAudioEncoder.AudioEncoder{
		Type:        "Wav",
		SampleRate:  sampleRate,
		BitDepth:    bitDepth, // Adjust as needed
		NumChannels: 1,
		Size:        int64(dataSize), // Size for a 5-second file (adjust as needed)
		Filename:    testImpulseFilename,
		DebugOn:     false,
	}
	fmt.Println("Test: Creating new Encoder...")
	// Create encoder
	err = myEncoder.Initialise()
	if err != nil {
		fmt.Println("Test: panic on New Header...")
		panic(err)
	}

	err = myEncoder.EncodeData(OutputSignal)
	if err != nil {
		fmt.Println("Test: panic on New Encoder...")
		panic(err)
	}

	// now output the loaded impulse pre-convolution
	testImpulseFilename = "c://temp//ConvolverRawImpulse.wav"

	myEncoder.Filename = testImpulseFilename
	OutputSignal = [][]float64{copyRawImpulse}
	dataSize = len(OutputSignal[0]) * len(OutputSignal) * (bitDepth / 8)
	myEncoder.Size = int64(dataSize)
	err = myEncoder.Initialise()
	if err != nil {
		fmt.Println("Test: panic on New Header...")
		panic(err)
	}

	myEncoder.EncodeData(OutputSignal)

	fmt.Printf("Wav  created: %s %v %v \n", testImpulseFilename, sampleRate, bitDepth)

	// Now check the convolver Impulse is OK
	testImpulseFilename = "c://temp//ConvolverPEQImpulse.wav"

	myEncoder.Filename = testImpulseFilename
	OutputSignal = [][]float64{myConvolver.FilterImpulse}
	dataSize = len(OutputSignal[0]) * len(OutputSignal) * (bitDepth / 8)
	myEncoder.Size = int64(dataSize)
	err = myEncoder.Initialise()
	if err != nil {
		fmt.Println("Test: panic on New Header...")
		panic(err)
	}

	myEncoder.EncodeData(OutputSignal)

	fmt.Printf("Wav  created: %s %v %v \n", testImpulseFilename, sampleRate, bitDepth)

	// Now output PEQ used as reference
	testImpulseFilename = "c://temp//PEQImpulse.wav"
	myEncoder.Filename = testImpulseFilename
	OutputSignal = [][]float64{myPEQFilter.Impulse}
	dataSize = len(OutputSignal[0]) * len(OutputSignal) * (bitDepth / 8)
	myEncoder.Size = int64(dataSize)
	err = myEncoder.Initialise()
	if err != nil {
		fmt.Println("Test: panic on New Header...")
		panic(err)
	}

	myEncoder.EncodeData(OutputSignal)

	fmt.Printf("Wav  created: %s %v %v \n", testImpulseFilename, sampleRate, bitDepth)

	fmt.Printf("Time taken: %s\n", time.Since(dtStartRun))
}

func TestFIRConvolvePrototype(t *testing.T) {
	println("")
	println("")
	println("Testing FIR Convolve")
	println("============================================================")
	dtStartRun := time.Now()

	var err error
	mySignalDecoder := &foxAudioDecoder.AudioDecoder{
		Filename: "c:\\Users\\jonat\\Music\\Resolution\\Pencil_1644.wav", // Replace with your test file
		Type:     "Wav",
	}

	fmt.Println("Test: Decoding input file... ", mySignalDecoder.Filename)
	err = mySignalDecoder.Initialise()
	if err != nil {
		t.Fatalf("Test: Error initializing AudioDecoder: %v", err)
	}
	bitDepth := 24
	sampleRate := mySignalDecoder.SampleRate

	/* Examples using internal Resampler*/

	myFIRDecoder := &foxAudioDecoder.AudioDecoder{
		//Filename: "c:\\temp\\Pencil_1644.wav", // Replace with your test file
		//Filename: "C:\\Users\\jonat\\Music\\Resolution\\Pencil_1644.wav",
		//Filename: "C:\\Users\\jonat\\Music\\Resolution\\Pencil_24192.wav",
		//Filename: "c:\\temp\\Opera_with_Sub_REW_96k.wav",
		//Filename: "c:\\temp\\96000_0069465CE0_Impulses_Cavern4Opera.wav",
		Filename: "c:\\temp\\CavernLeft.wav", // Replace with your test file
		//Filename: "c:\\temp\\OperaExperiment-192k.wav",
		//Filename: "c:\\temp\\96000_Impulses_Cavern4Iloud.wav",
		//Filename: "c:\\temp\\Loudness_yz.wav", // Replace with your test file

		Type: "Wav",
	}

	fmt.Println("Test: Decoding input file... ", myFIRDecoder.Filename)
	err = myFIRDecoder.Initialise()
	if err != nil {
		t.Fatalf("Test: Error initializing AudioDecoder: %v", err)
	}

	var dWG sync.WaitGroup
	DecodedSamplesChannel := make(chan [][]float64, 10)
	dWG.Add(1)
	go func() {
		defer dWG.Done()
		myFIRDecoder.DecodeSamples(DecodedSamplesChannel)
	}()

	// Close the doneProcessing channel to signal that decoding is finished
	fmt.Println("Test: Setting up Convolver")
	myConvolver := foxConvolver.Convolver{}
	myResampler := foxResampler.NewResampler()
	myResampler.DebugOn = true
	myResampler.FromSampleRate = myFIRDecoder.SampleRate
	myResampler.ToSampleRate = sampleRate
	myResampler.Quality = 10
	myImpulse := make([][]float64, myFIRDecoder.NumChannels)
	//Internal
	targetLevel := foxNormalizer.TargetGain(myFIRDecoder.SampleRate, int(sampleRate), 0.89)

	//Test zeozeozeo resampler
	if err != nil {
		panic(err)
	}

	dWG.Add(1)
	go func() {
		var ResultCounter int

		//var tempConv []float64
		defer dWG.Done()
		for decodedResult := range DecodedSamplesChannel {
			ResultCounter += len(decodedResult[0])

			//Resample Impulse to match signal SampleRate
			// quality 30 is pretty good
			//err = myResampler.Resample(decodedResult, myFIRDecoder.SampleRate, sampmyResampler.InputSamplesleRate, 10)
			myResampler.InputSamples = decodedResult
			err = myResampler.Resample()

			if err != nil {
				println("Error resampling: ", err.Error())
				return
			}
			//Append samples to impulse
			for i := 0; i < min(len(myResampler.InputSamples[0]), len(myImpulse)); i++ {
				myImpulse[i] = append(myImpulse[i], myResampler.InputSamples[i]...)
			}
		}
		fmt.Println("Test: Length of Impulse", ResultCounter)

	}()
	fmt.Println("Test: Waiting for FIR to Load")
	// Wait for the asynchronous decoding to complete
	dWG.Wait()
	// normalize impulse
	_ = foxNormalizer.NormalizePeak(myImpulse, targetLevel)
	// Let's start with a single impulse
	myConvolver = foxConvolver.NewConvolver(myImpulse[0])

	// easiest way to get a convolver per channel
	var myConvolvers []foxConvolver.Convolver
	myConvolvers = append(myConvolvers, myConvolver)
	if mySignalDecoder.NumChannels == 2 {
		myConvolvers = append(myConvolvers, myConvolver)
	}
	fmt.Println("Test: closing decoder")
	// Now Start Decoding the Signal channel
	var sWG sync.WaitGroup
	DecodedSignalChannel := make(chan [][]float64, 10)
	OutputSignal := make([][]float64, mySignalDecoder.NumChannels)

	//N
	N := foxConvolver.NextPowerOf2(4 * len(myConvolver.FilterImpulse))
	targetSignalLength := N - len(myConvolver.FilterImpulse)

	println("targetSignalLength", targetSignalLength)
	sWG.Add(1)
	go func() {
		defer sWG.Done()
		mySignalDecoder.DecodeSamples(DecodedSignalChannel)
	}()
	sWG.Add(1)
	go func() {
		var ResultCounter int

		//use a buffer to hold decoded signal and allow the convolver signal to be optimal length for processing
		cnvBuffer := make([][]float64, mySignalDecoder.NumChannels)
		//use a swap buffer to retain data when Optimal length shorter than decoded signal block size
		cnvBufferSwap := make([][]float64, mySignalDecoder.NumChannels)
		defer sWG.Done()
		for decodedResult := range DecodedSignalChannel {
			ResultCounter += len(decodedResult[0])
			// each audio channel
			for i := 0; i < len(decodedResult); i++ {
				//		OutputSignal[i] = append(OutputSignal[i], myConvolvers[i].BufferedConvolver(decodedResult[i])...)
				//	}
				//	}
				cnvBuffer[i] = append(cnvBuffer[i], decodedResult[i]...)

				for {
					if len(cnvBuffer[i]) >= targetSignalLength {
						cnvBufferSwap[i] = cnvBuffer[i][targetSignalLength:]
						// dropouts ~ 1s
						//convOut := myConvolvers[i].ConvolveOverlapSave(cnvBuffer[i][:targetSignalLength])
						// ** No dropouts ~ 1.6s - NB this convolver is not doing this, it is the wrapper
						// handling OLS properly
						convOut := myConvolvers[i].ConvolveOverlapSave(cnvBuffer[i][:targetSignalLength])
						// dropouts ~ 1.3s
						//convOut := myConvolvers[i].ConvolveImpulsesFFT(cnvBuffer[i][:targetSignalLength])

						// works but slow ~ 2 seconds - not sure if it is actually convolving
						//tmpResult[i] = fftConvolver.Process(tmpResult[i])
						//dropouts ~ 31 s
						//convOut := myConvolvers[i].ConvolveImpulsesSlow(cnvBuffer[i][:targetSignalLength])

						OutputSignal[i] = append(OutputSignal[i], convOut...)
						// copy remaining output back to the temporary store
						cnvBuffer[i] = cnvBufferSwap[i]

					} else {
						//means that we need to get more data.
						break
					}
				}

			}

		}

		fmt.Println("Test: Length of Signal", ResultCounter)

	}()

	fmt.Println("Test: Waiting for Signal to Process")
	// Wait for the asynchronous decoding to complete
	sWG.Wait()
	println("TOTAL Convolved signal:", len(OutputSignal[0]))

	fmt.Println("Setting up Encoder: ", targetLevel)

	fmt.Println("calculate data Size")
	dataSize := len(OutputSignal[0]) * len(OutputSignal) * (bitDepth / 8)
	testImpulseFilename := "c:/temp/ConvolvedSignal.wav"
	myEncoder := foxAudioEncoder.AudioEncoder{
		Type:        "Wav",
		SampleRate:  sampleRate,
		BitDepth:    bitDepth, // Adjust as needed
		NumChannels: mySignalDecoder.NumChannels,
		Size:        int64(dataSize), // Size for a 5-second file (adjust as needed)
		Filename:    testImpulseFilename,
		DebugOn:     false,
	}
	fmt.Println("Test: Creating new Encoder...")
	// Create encoder
	err = myEncoder.Initialise()
	if err != nil {
		fmt.Println("Test: panic on New Header...")
		panic(err)
	}

	err = myEncoder.EncodeData(OutputSignal)
	if err != nil {
		fmt.Println("Test: panic on New Encoder...")
		panic(err)
	}

	fmt.Printf("Time taken: %s\n", time.Since(dtStartRun))
}

func TestFIRConvolve(t *testing.T) {

	println("")
	println("")
	println("Testing FIR Convolve")
	println("============================================================")
	dtStartRun := time.Now()

	var err error
	mySignalDecoder := &foxAudioDecoder.AudioDecoder{
		Filename: "c:\\Users\\jonat\\Music\\Resolution\\Pencil_1644.wav", // Replace with your test file
		//Filename: "C:\\Users\\jonat\\Music\\Resolution\\Pencil_24192.wav",
		Type: "Wav",
	}

	fmt.Println("Test: Decoding input file... ", mySignalDecoder.Filename)
	err = mySignalDecoder.Initialise()
	if err != nil {
		t.Fatalf("Test: Error initializing AudioDecoder: %v", err)
	}
	bitDepth := 24
	sampleRate := mySignalDecoder.SampleRate

	/* Examples using internal Resampler*/

	myFIRDecoder := &foxAudioDecoder.AudioDecoder{
		//Filename: "c:\\temp\\Pencil_1644.wav", // Replace with your test file
		//Filename: "C:\\Users\\jonat\\Music\\Resolution\\Pencil_1644.wav",
		//Filename: "C:\\Users\\jonat\\Music\\Resolution\\Pencil_24192.wav",
		//Filename: "c:\\temp\\Opera_with_Sub_REW_96k.wav",
		//Filename: "c:\\temp\\96000_0069465CE0_Impulses_Cavern4Opera.wav",
		//Filename: "c:\\temp\\CavernLeft.wav", // Replace with your test file
		//Filename: "c:\\temp\\OperaExperiment-192k.wav",
		//Filename: "c:\\temp\\96000_Impulses_Cavern4Iloud.wav",
		Filename: "c:\\temp\\Loudness_yz.wav", // Replace with your test file

		Type: "Wav",
	}

	fmt.Println("Test: Decoding input file... ", myFIRDecoder.Filename)
	err = myFIRDecoder.Initialise()
	if err != nil {
		t.Fatalf("Test: Error initializing AudioDecoder: %v", err)
	}

	var dWG sync.WaitGroup
	DecodedSamplesChannel := make(chan [][]float64, 10)
	dWG.Add(1)
	go func() {
		defer dWG.Done()
		myFIRDecoder.DecodeSamples(DecodedSamplesChannel)
	}()

	// Close the doneProcessing channel to signal that decoding is finished
	fmt.Println("Test: Setting up Convolver")
	myResampler := foxResampler.NewResampler()
	myResampler.FromSampleRate = myFIRDecoder.SampleRate

	myResampler.ToSampleRate = sampleRate
	myResampler.Quality = 10
	myImpulse := make([][]float64, myFIRDecoder.NumChannels)
	//Internal
	targetLevel := foxNormalizer.TargetGain(myFIRDecoder.SampleRate, int(sampleRate), 0.89)

	if err != nil {
		panic(err)
	}

	dWG.Add(1)
	go func() {
		var ResultCounter int

		//var tempConv []float64
		defer dWG.Done()
		for decodedResult := range DecodedSamplesChannel {
			ResultCounter += len(decodedResult[0])

			//Resample Impulse to match signal SampleRate
			// quality 30 is pretty good
			myResampler.InputSamples = decodedResult
			err = myResampler.Resample()
			if err != nil {
				println("Error resampling: ", err.Error())
				return
			}
			//Append samples to impulse
			for i := 0; i < min(len(decodedResult), len(myImpulse)); i++ {
				myImpulse[i] = append(myImpulse[i], decodedResult[i]...)
			}
		}
		fmt.Println("Test: Length of Impulse", ResultCounter)

	}()
	fmt.Println("Test: Waiting for FIR to Load")
	// Wait for the asynchronous decoding to complete
	dWG.Wait()
	// normalize impulse
	_ = foxNormalizer.NormalizePeak(myImpulse, targetLevel)
	// Let's start with a single impulse
	myConvolver := foxConvolver.NewConvolver(myImpulse[0])
	// easiest way to get a convolver per channel
	var myConvolvers []foxConvolver.Convolver
	myConvolvers = append(myConvolvers, myConvolver)
	if mySignalDecoder.NumChannels == 2 {
		myConvolvers = append(myConvolvers, myConvolver)
	}
	fmt.Println("Test: closing decoder")
	// Now Start Decoding the Signal channel
	var sWG sync.WaitGroup
	DecodedSignalChannel := make(chan [][]float64, 10)
	OutputSignal := make([][]float64, mySignalDecoder.NumChannels)

	//N

	sWG.Add(1)
	go func() {
		defer sWG.Done()
		mySignalDecoder.DecodeSamples(DecodedSignalChannel)
	}()
	// Create go channel for each audio channel
	audioChannels := make([]chan []float64, mySignalDecoder.NumChannels)
	for i := 0; i < mySignalDecoder.NumChannels; i++ {
		audioChannels[i] = make(chan []float64)
	}
	// Split audio data into separate channels
	channelSplitter(DecodedSignalChannel, audioChannels, mySignalDecoder.NumChannels)
	// Apply convolution (FIR filter)
	convolvedChannels := make([]chan []float64, mySignalDecoder.NumChannels)

	sWG.Add(1)
	go func() {
		defer sWG.Done()
		for i := 0; i < mySignalDecoder.NumChannels; i++ {

			convolvedChannels[i] = make(chan []float64)
			go myConvolvers[i].ConvolveChannel(audioChannels[i], convolvedChannels[i])
		}

	}()

	fmt.Println("Test: Waiting for Signal to Process")
	// Wait for the asynchronous decoding to complete

	println("TOTAL Convolved signal:", len(OutputSignal[0]))

	fmt.Println("Setting up Encoder: ", targetLevel)

	fmt.Println("calculate data Size")
	dataSize := len(OutputSignal[0]) * len(OutputSignal) * (bitDepth / 8)
	testImpulseFilename := "c:/temp/ConvolvedSignalJonny.wav"
	myEncoder := foxAudioEncoder.AudioEncoder{
		Type:        "Wav",
		SampleRate:  sampleRate,
		BitDepth:    bitDepth, // Adjust as needed
		NumChannels: mySignalDecoder.NumChannels,
		Size:        int64(dataSize), // Size for a 5-second file (adjust as needed)
		Filename:    testImpulseFilename,
		DebugOn:     false,
	}
	fmt.Println("")
	fmt.Println("Test: Creating new Encoder...")
	// Create encoder
	err = myEncoder.Initialise()
	if err != nil {
		fmt.Println("Test: panic on New Header...")
		panic(err)
	}

	mergedChannel := make(chan [][]float64)
	sWG.Add(1)
	go mergeChannels(convolvedChannels, mergedChannel, mySignalDecoder.NumChannels)
	sWG.Done()

	fmt.Println("Test: Encoding Data...")
	sWG.Add(1)
	go func() {

		defer sWG.Done()
		myEncoder.EncodeSamplesChannel(mergedChannel)

	}()

	sWG.Wait()

	fmt.Printf("Time taken: %s\n", time.Since(dtStartRun))

}

func TestMain(m *testing.M) {
	// Run tests
	fmt.Println("Running DSP Test:")
	exitCode := m.Run()

	// Exit with the test result code
	os.Exit(exitCode)
}

// Split audio data into separate channels
func channelSplitter(inputCh chan [][]float64, outputChs []chan []float64, channelCount int) {
	totals := 0
	go func() {
		for chunk := range inputCh {
			totals += len(chunk[0])
			for i := 0; i < channelCount; i++ {
				channelData := chunk[i]
				outputChs[i] <- channelData
			}
		}

		// Close all output channels after processing
		for _, ch := range outputChs {
			close(ch)
		}
		println("Split length: ", totals)
	}()
}

// Merge audio data from all channels
func mergeChannels(inputChannels []chan []float64, outputChannel chan [][]float64, numChannels int) {
	go func() {
		// Iterate through all input channels
		for {
			var mergedChunks [][]float64 // Temporary slice to hold data from each channel

			// Receive data from all channels
			for i := 0; i < numChannels; i++ {
				chunk, ok := <-inputChannels[i]
				if !ok {
					// Channel closed
					inputChannels[i] = nil // Mark as closed
					continue
				}
				mergedChunks = append(mergedChunks, chunk)
			}

			// Break if all channels are closed
			if len(mergedChunks) == 0 {
				break
			}

			// Send merged chunk
			outputChannel <- mergedChunks
		}

		// Close the output channel

		println("channelMerger - closing output")
		close(outputChannel)
	}()
}
