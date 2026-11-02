package main

import (
	"flag"
	"fmt"
	"sync"
	"time"

	"github.com/Foxenfurter/foxAudioLib/foxAudioDecoder"
	"github.com/Foxenfurter/foxAudioLib/foxAudioEncoder"
	foxConvolver "github.com/Foxenfurter/foxAudioLib/foxConvolverAdapt"
	"github.com/Foxenfurter/foxAudioLib/foxNormalizer"
	"github.com/Foxenfurter/foxAudioLib/foxPEQ"
	"github.com/Foxenfurter/foxAudioLib/foxResampler"
)

var (
	/* ---------------------------------------------------------
	FFT provider set here - options: scientificgo, gofft, foxfft_adapter_real, or foxfft
	---------------------------------------------------------*/
	fftProvider  = flag.String("fft", "foxfft_adapter", "FFT provider: scientificgo, gofft,foxfft_adapter, foxfft_adapter_real or foxfft_HOR")
	packageName  = "foxAudioLib"
	runBenchmark = flag.Bool("benchmark", true, "Run FFT benchmark before main processing")
	//benchmarkSize = flag.Int("benchsize", 16384, "FFT size for benchmarking")
	//benchmarkSize = flag.Int("benchsize", 32768, "FFT size for benchmarking")
	//benchmarkSize = flag.Int("benchsize", 65536, "FFT size for benchmarking")
	//benchmarkSize = flag.Int("benchsize", 8192, "FFT size for benchmarking")
	benchmarkSize = flag.Int("benchsize", 4096, "FFT size for benchmarking")
	//benchmarkSize = flag.Int("benchsize", 2048, "FFT size for benchmarking")
	//benchmarkIterations = flag.Int("benchiter", 500, "Number of iterations for benchmarking")
	//benchmarkIterations = flag.Int("benchiter", 250, "Number of iterations for benchmarking")
	//benchmarkIterations = flag.Int("benchiter", 1000, "Number of iterations for benchmarking")
	//benchmarkIterations = flag.Int("benchiter", 2000, "Number of iterations for benchmarking")
	benchmarkIterations = flag.Int("benchiter", 4000, "Number of iterations for benchmarking")
	//benchmarkIterations = flag.Int("benchiter", 8000, "Number of iterations for benchmarking")
)

func main() {
	const functionName = "Main"
	flag.Parse()
	fmt.Printf("Testing foxAudioLib with FFT provider: %s\n", *fftProvider)

	// Run benchmark if requested

	if *runBenchmark {
		runFFTBenchmark(*benchmarkSize, *benchmarkIterations)

		// Optional: exit after benchmark or continue with main processing
		// If you just want to benchmark and exit, uncomment the next line:
		// return
	}

	startTime := time.Now()

	// Initialize AudioDecoder FIRST
	myDecoder := foxAudioDecoder.AudioDecoder{
		Type: "WAV",
	}
	//myDecoder.Filename = "C:\\Users\\jonat\\Music\\Formats\\Pencil_1644-wav.wav"
	myDecoder.Filename = "C:\\Users\\jonat\\Music\\Formats\\01 Spanish Stroll.wav"
	firFile := "c:\\temp\\44100_00F12EE86F_Impulses_Cavern4Iloud.wav"
	firFile = "C:\\ProgramData\\Squeezebox\\Prefs\\SqueezeDSP\\Impulses\\ArcExtractiLoudFostexSub.wav"

	fmt.Println(functionName+": Decoding input file... ", myDecoder.Filename)
	err := myDecoder.Initialise()
	if err != nil {
		fmt.Printf("Test: Error initializing decoder: %v\n", err)
		panic(err)
	}

	fmt.Printf("Test: Decoder initialized - SampleRate: %d, Channels: %d, BitDepth: %d\n",
		myDecoder.SampleRate, myDecoder.NumChannels, myDecoder.BitDepth)

	// Initialize Audio Encoder AFTER decoder is ready
	myEncoder := foxAudioEncoder.AudioEncoder{
		Type:        "Wav",
		SampleRate:  myDecoder.SampleRate,
		BitDepth:    myDecoder.BitDepth,
		NumChannels: myDecoder.NumChannels,
		Size:        int64(myDecoder.Size),
		Filename:    "c:\\temp\\output.wav",
	}

	fmt.Println("Test: Output file: ", myEncoder.Filename)
	fmt.Printf("Test: Creating new Encoder SampleRate: %d, Channels: %d, BitDepth: %d\n",
		myEncoder.SampleRate, myEncoder.NumChannels, myEncoder.BitDepth)

	err = myEncoder.Initialise()
	if err != nil {
		fmt.Printf("Test: Error initializing encoder: %v\n", err)
		panic(err)
	}

	// Setup PEQ Filters
	var applyPEQ = true
	myPEQFilters := make([]foxPEQ.PEQFilter, myDecoder.NumChannels)
	for i := 0; i < myDecoder.NumChannels; i++ {
		myPEQ := foxPEQ.NewPEQFilter(myDecoder.SampleRate, 15)

		if applyPEQ {
			err = BuildPEQFilters(&myPEQ)
			if err != nil {
				fmt.Printf("Error building PEQ filters: %v\n", err)
				return
			}
		}
		myPEQFilters[i] = myPEQ
	}

	for i := 0; i < len(myPEQFilters); i++ {
		fmt.Printf("Convolver PEQ filter length for channel %d: %d\n", i, len(myPEQFilters[i].Impulse))
	}

	elapsedTime := int(time.Since(startTime).Milliseconds())
	fmt.Printf("\n============================================================================================\n")
	fmt.Printf("AudioLib: PEQ Setup Took: %d ms\n", elapsedTime)
	fmt.Printf("============================================================================================\n\n")

	// FIR convolution setup

	myConvolvers := make([]foxConvolver.Convolver, myDecoder.NumChannels)

	if firFile != "" {
		myImpulseDecoder := &foxAudioDecoder.AudioDecoder{
			Filename: firFile,
			Type:     "Wav",
		}

		err = myImpulseDecoder.Initialise()
		if err != nil {
			fmt.Printf("Test: Error initializing Impulse AudioDecoder: %v\n", err)
			panic(err)
		}

		myConvolvers = MergePEQandSingleFIRImpulse(&myPEQFilters, myImpulseDecoder, myDecoder.SampleRate, *fftProvider)
	} else {
		fmt.Println("No FIR Filter - mapping PEQ")
		for i := 0; i < len(myConvolvers); i++ {
			conv := foxConvolver.NewConvolver(myPEQFilters[i].Impulse)
			setFFTProvider(&conv, *fftProvider)
			myConvolvers[i] = conv

		}
	}
	/* Minimium amount of audio to be processed as proportion of 1 second */
	convolveSignalDivisor := 4
	myBlockLength := myDecoder.SampleRate * convolveSignalDivisor

	for i := 0; i < len(myConvolvers); i++ {
		conv := foxConvolver.NewConvolver(myPEQFilters[i].Impulse)
		setFFTProvider(&conv, *fftProvider)
		myConvolvers[i] = conv
		myConvolvers[i].SetSignalBlockLength(myBlockLength)
		myConvolvers[i].InitForStreaming()
	}

	incrementalTime := int(time.Since(startTime).Milliseconds()) - elapsedTime
	elapsedTime = int(time.Since(startTime).Milliseconds())
	fmt.Printf("\n============================================================================================\n")
	fmt.Printf("AudioLib: Setup Took: %d ms, FIR Load & Merge: %d ms\n", elapsedTime, incrementalTime)
	fmt.Printf("============================================================================================\n\n")

	// Pipeline setup
	var WG sync.WaitGroup

	DecodedSamplesChannel := make(chan [][]float64, 4)

	// Create channels for processing pipeline
	audioChannels := make([]chan []float64, myDecoder.NumChannels)
	convolvedChannels := make([]chan []float64, myDecoder.NumChannels)
	mergedChannel := make(chan [][]float64, 1)

	for i := 0; i < myDecoder.NumChannels; i++ {
		audioChannels[i] = make(chan []float64, 10)     // Increased buffer size
		convolvedChannels[i] = make(chan []float64, 10) // Increased buffer size
	}

	fmt.Println("Setting up processing pipeline...")

	// Start ALL processing stages in the correct order
	// 1. Encoder (final consumer)
	fmt.Println("Test: Starting encoding...")
	WG.Add(1)
	go func() {
		defer func() {
			fmt.Println("Encoder completed")
			WG.Done()
		}()
		err := myEncoder.EncodeSamplesChannel(mergedChannel)
		if err != nil {
			fmt.Printf("Error encoding samples: %v\n", err)
		}
	}()

	// 2. Merger (consumes from convolvers)
	WG.Add(1)
	go func() {
		defer func() {
			close(mergedChannel)
			fmt.Println("Channel merger completed")
			WG.Done()
		}()
		mergeChannels(convolvedChannels, mergedChannel)
	}()

	// 3. Convolvers (consume from splitter)
	for i := 0; i < myDecoder.NumChannels; i++ {
		WG.Add(1)
		go func(channelIdx int) {
			defer func() {
				close(convolvedChannels[channelIdx])
				fmt.Printf("Convolver %d completed\n", channelIdx)
				WG.Done()
			}()
			fmt.Printf("Convolver %d starting with impulse length: %d, FFT: %s\n",
				channelIdx, len(myConvolvers[channelIdx].FilterImpulse), *fftProvider)

			myConvolvers[channelIdx].DebugOn = true
			myConvolvers[channelIdx].DebugFunc = func(msg string) {
				fmt.Printf("Convolver[%d]: %s\n", channelIdx, msg)
			}

			myConvolvers[channelIdx].ConvolveChannel(audioChannels[channelIdx], convolvedChannels[channelIdx])
		}(i)
	}

	// 4. Splitter (consumes from decoder)
	WG.Add(1)
	go func() {
		defer func() {
			// Close all audio channels
			for i := 0; i < len(audioChannels); i++ {
				close(audioChannels[i])
			}
			fmt.Println("Channel splitter completed")
			WG.Done()
		}()
		channelSplitter(DecodedSamplesChannel, audioChannels)
	}()

	// 5. Decoder (produces data - starts the pipeline)
	fmt.Println("Test: Starting decoding...")
	WG.Add(1)
	go func() {
		defer func() {
			fmt.Println("Decoder completed")
			close(DecodedSamplesChannel)
			WG.Done()
		}()
		myDecoder.DecodeSamples(DecodedSamplesChannel)
	}()

	fmt.Println("Test: Waiting for processing to complete...")
	WG.Wait()

	incrementalTime = int(time.Since(startTime).Milliseconds()) - elapsedTime
	elapsedTime = int(time.Since(startTime).Milliseconds())
	fmt.Printf("\n============================================================================================\n")
	//fmt.Printf("AudioLib: total elapsed time: %d ms, Signal Processing: %d ms\n", elapsedTime, incrementalTime)
	fmt.Printf("AudioLib with FFT: %s : total elapsed time: %d ms, Signal Processing: %d ms\n", *fftProvider, elapsedTime, incrementalTime)
	fmt.Printf("============================================================================================\n\n")

}

func BuildPEQFilters(myPEQFilter *foxPEQ.PEQFilter) error {
	var err error

	err = myPEQFilter.CalcBiquadFilter("lowshelf", 80, 1.8, 0.41, "Q")
	if err != nil {
		return err
	}
	err = myPEQFilter.CalcBiquadFilter("highshelf", 6300, 0.25, 0.41, "Q")
	if err != nil {
		return err
	}
	err = myPEQFilter.CalcBiquadFilter("peak", 2400, -1.8, 0.17, "Q")
	if err != nil {
		return err
	}
	err = myPEQFilter.CalcBiquadFilter("lowshelf", 35, 0.25, 1.25, "Q")
	if err != nil {
		return err
	}
	err = myPEQFilter.CalcBiquadFilter("peak", 1200, 0.25, 1.4, "Q")
	if err != nil {
		return err
	}

	myPEQFilter.GenerateFilterImpulse()

	return nil
}

func MergePEQandSingleFIRImpulse(lclPEQFilters *[]foxPEQ.PEQFilter, myImpulseDecoder *foxAudioDecoder.AudioDecoder, TargetSampleRate int, fftProvider string) []foxConvolver.Convolver {
	startTime := time.Now()
	targetLevel := foxNormalizer.TargetGain(myImpulseDecoder.SampleRate, TargetSampleRate, 0.89)
	myConvolvers := make([]foxConvolver.Convolver, len(*lclPEQFilters))
	myConvolvedSignal := make([][]float64, len(*lclPEQFilters))

	DecodedImpulseChannel := make(chan [][]float64, 1000)
	var WG sync.WaitGroup

	// Start impulse decoding
	WG.Add(1)
	go func() {
		defer WG.Done()
		defer close(DecodedImpulseChannel)
		myImpulseDecoder.DecodeSamples(DecodedImpulseChannel)
	}()

	// Process impulse data in MAIN goroutine
	fmt.Println("Reading FIR impulse data...")
	myFirImpulse := make([][]float64, myImpulseDecoder.NumChannels)

	// Read all impulse data from channel
	for decodedResult := range DecodedImpulseChannel {
		for i := 0; i < len(decodedResult); i++ {
			if i < len(myFirImpulse) {
				myFirImpulse[i] = append(myFirImpulse[i], decodedResult[i]...)
			}
		}
	}

	// Wait for decoding to complete
	WG.Wait()

	elapsedTime := int(time.Since(startTime).Milliseconds())
	fmt.Printf("\n============================================================================================\n")
	fmt.Printf("AudioLib - Merge Reading FIR: %d ms, Impulse channels: %d, samples per channel: %d\n",
		elapsedTime, len(myFirImpulse), len(myFirImpulse[0]))
	fmt.Printf("============================================================================================\n\n")

	// Process based on channel configuration
	if len(myFirImpulse) == 1 && len(myConvolvers) >= 2 {
		fmt.Println("Mono FIR filter & multi-channel Audio - applying to first 2 channels")

		conv1 := foxConvolver.NewConvolver((*lclPEQFilters)[0].Impulse)
		conv2 := foxConvolver.NewConvolver((*lclPEQFilters)[1].Impulse)
		setFFTProvider(&conv1, fftProvider)
		setFFTProvider(&conv2, fftProvider)
		myConvolvers[0] = conv1
		myConvolvers[1] = conv2

		inputSamples, err := foxResampler.ResampleUpsample(myFirImpulse[0], myImpulseDecoder.SampleRate, TargetSampleRate, 10)
		if err != nil {
			fmt.Printf("Error resampling mono FIR: %v\n", err)
			return myConvolvers
		}

		myConvolvedSignal[0] = myConvolvers[0].ConvolveFFT(inputSamples)
		myConvolvedSignal[1] = myConvolvers[1].ConvolveFFT(inputSamples)

	} else {
		fmt.Printf("Multi channel FIR filter - matching %d channels to %d convolvers\n",
			len(myFirImpulse), len(myConvolvers))
		for i := 0; i < min(len(myFirImpulse), len(myConvolvers)); i++ {
			conv := foxConvolver.NewConvolver((*lclPEQFilters)[i].Impulse)
			setFFTProvider(&conv, fftProvider)
			myConvolvers[i] = conv

			inputSamples, err := foxResampler.ResampleUpsample(myFirImpulse[i], myImpulseDecoder.SampleRate, TargetSampleRate, 10)
			if err != nil {
				fmt.Printf("Error resampling channel %d: %v\n", i, err)
				return myConvolvers
			}

			myConvolvedSignal[i] = myConvolvers[i].ConvolveFFT(inputSamples)
			maxGain := myConvolvers[i].MaxGainFromFFT(myConvolvedSignal[i])
			fmt.Printf("Channel %d max gain: %f\n", i, maxGain)
		}
	}

	totalTime := int(time.Since(startTime).Milliseconds())
	fmt.Printf("\n============================================================================================\n")
	fmt.Printf("AudioLib - Resample & Convolve FIR: %d ms\n", totalTime)
	fmt.Printf("============================================================================================\n\n")

	fmt.Printf("Setting up normalizer with target level: %f\n", targetLevel)

	// Normalize and update convolvers
	gain := foxNormalizer.NormalizePeak(myConvolvedSignal, targetLevel)
	if gain >= 0.0 {
		fmt.Printf("Gain applied: %v\n", gain)
	}

	for i := 0; i < min(len(myConvolvedSignal), len(myConvolvers)); i++ {
		if myConvolvedSignal[i] != nil {
			myConvolvers[i].AmendFilterImpulse(myConvolvedSignal[i])
			fmt.Printf("Updated convolver %d with %d samples\n", i, len(myConvolvedSignal[i]))
		}
	}

	fmt.Println("FIR impulse processing completed")
	return myConvolvers
}

// Apply convolution (FIR filter)
func applyConvolution(inputCh, outputCh chan []float64, myImpulse []float64) {
	fmt.Printf("Convolver starting with impulse length: %d\n", len(myImpulse))
	myConvolver := foxConvolver.NewConvolver(myImpulse)

	// Enable debug similar to production code
	myConvolver.DebugOn = true
	myConvolver.DebugFunc = func(msg string) {
		fmt.Printf("Convolver: %s\n", msg)
	}

	myConvolver.ConvolveChannel(inputCh, outputCh)
	fmt.Printf("Convolution completed for impulse length: %d\n", len(myImpulse))
}

// Split audio data into separate channels
func channelSplitter(
	inputCh chan [][]float64,
	outputChs []chan []float64,
) {
	chunkCounter := 0
	channelCount := len(outputChs)

	fmt.Printf(packageName + ":Channel Splitter Starting\n")

	for chunk := range inputCh {
		//fmt.Printf("Splitter processing chunk %d\n", chunkCounter)
		for i := 0; i < channelCount; i++ {
			if i < len(chunk) {
				outputChs[i] <- chunk[i]
			}
		}
		chunkCounter++
	}

	fmt.Printf("%s: Channel Splitter processed %d chunks\n", packageName, chunkCounter)
}

// Merge audio data from all channels
func mergeChannels(inputChannels []chan []float64, outputChannel chan [][]float64) {
	numChannels := len(inputChannels)
	chunkCounter := 0

	fmt.Printf("Channel merger starting with %d channels\n", numChannels)

	for {
		merged := make([][]float64, numChannels)
		allClosed := true

		for i := 0; i < numChannels; i++ {
			data, ok := <-inputChannels[i]
			if ok {
				merged[i] = data
				allClosed = false
			} else {
				// Channel is closed, use nil to indicate no data
				merged[i] = nil
			}
		}

		// If all channels are closed and we have no data, break
		if allClosed {
			break
		}

		// Send the merged data
		outputChannel <- merged
		chunkCounter++
		//fmt.Printf("Merger sent chunk %d\n", chunkCounter)
	}

	fmt.Printf("Channel merger completed, processed %d chunks\n", chunkCounter)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Helper function to set FFT provider based on flag
func setFFTProvider(conv *foxConvolver.Convolver, provider string) {
	switch provider {
	case "foxfft_adapter":
		conv.SetFFTProviderByString("foxfft_adapter")
		fmt.Println("Using  partitioned foxFFT provider")
	case "foxfft_adapter_real":
		conv.SetFFTProviderByString("foxfft_adapter_real")
		fmt.Println("Using  partitioned foxFFT real-input adapter provider")
	case "gofft":
		conv.SetFFTProviderByString("gofft")
		fmt.Println("Using GoFFT (argusdusty) FFT provider")
	case "foxfft_HOR":
		conv.SetFFTProviderByString("foxfft_HOR")
		fmt.Println("Using FoxFFT (Rust) FFT provider")
	case "scientificgo":
		fallthrough
	default:
		conv.SetFFTProviderByString("scientificgo")
		fmt.Println("Using ScientificGo FFT provider")
	}
}

func runFFTBenchmark(size, iterations int) {
	fmt.Printf("\n=== FFT-Only Benchmark ===\n")
	testData := make([]complex128, size)
	for i := range testData {
		testData[i] = complex(float64(i%100), 0)
	}
	foxConvolver.CompareAllFFTs(testData, iterations)
}
