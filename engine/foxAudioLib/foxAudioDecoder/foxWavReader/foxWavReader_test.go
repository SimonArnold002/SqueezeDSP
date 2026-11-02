package foxWavReader_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Foxenfurter/foxAudioLib/foxAudioDecoder/foxWavReader"
)

// ... (DecodeAudioData function and helper functions from previous responses) ...

const InputBufferSize = 65000

func TestAudioDecoderSoXLoader(t *testing.T) {
	//filename := "c:\\temp\\Pencil_1644.wav"
	filename := "C:\\temp\\Opera_with_Sub_REW_20230303.wav"
	filename = filepath.ToSlash(filepath.Clean(filename))
	fmt.Println("Test: Running TestAudioDecoderSoXLoader...", filename)

	// Create an *exec.Cmd
	cmd := exec.Command("E:\\Local Audio\\SOX\\sox.exe", filename, "-r", "96000", "-t", "wav", "-")

	// Create a bytes.Buffer to capture the output
	var out bytes.Buffer
	cmd.Stdout = &out

	// Run the command
	err := cmd.Run()
	if err != nil {
		fmt.Println("Error: ", err)
		panic(err)
	}

	// The output is now in the 'out' buffer.
	// You can convert it to a bytes.Reader with bytes.NewReader()
	myReader := bytes.NewReader(out.Bytes())

	var myDecoder foxWavReader.WavReader

	myDecoder.Input = myReader

	err = myDecoder.DecodeWavHeader()
	if err != nil {
		println("Test: Header Read Error...", err)
		panic(err)
	}

	var WG sync.WaitGroup
	DecodedSamplesChannel := make(chan [][]float64, 100)
	fmt.Println("Test: Decoding Data...")
	WG.Add(1)
	go func() {
		defer WG.Done()
		myDecoder.DecodeInput(DecodedSamplesChannel, nil)

	}()

	mySamples := make([][]float64, myDecoder.NumChannels)
	fmt.Println("Test: Encoding Data...")
	WG.Add(1)
	go func() {

		defer WG.Done()
		var ResultCounter int

		for decodedResult := range DecodedSamplesChannel {
			// Discard decoded results
			for i := int(0); i < len(decodedResult); i++ {
				mySamples[i] = append(mySamples[i], decodedResult[i]...)
			}

			ResultCounter += len(decodedResult[0])

		}
		fmt.Println("Test: Not waiting anymore", ResultCounter)
	}()

	fmt.Println("Test: Waiting...")
	WG.Wait()

}

func TestReadAndDecodeAudio(t *testing.T) {
	//filename := "c:\\temp\\Pencil_1644.wav"
	filename := "C:\\Users\\jonat\\Music\\Resolution\\Pencil_24176.wav"

	fmt.Println("Test: Full running TestReadAndDecodeAudio...", filename)

	var decoder foxWavReader.WavReader

	fmt.Println("Test: running reader sub-routine...")
	inputFile, err := os.Open(filename)
	if err != nil {
		fmt.Println("Test: File Error...", err)
		panic(err)
	}
	defer inputFile.Close()
	decoder.Input = inputFile
	err = decoder.DecodeWavHeader()
	if err != nil {
		println("Test: Header Read Error...", err)
	}
	var WG sync.WaitGroup
	DecodedSamplesChannel := make(chan [][]float64, 10000)
	fmt.Println("Test: Decoding Data...")
	WG.Add(1)
	go func() {
		defer WG.Done()
		decoder.DecodeInput(DecodedSamplesChannel, nil)

	}()

	fmt.Println("Test: Encoding Data...")
	WG.Add(1)
	go func() {

		defer WG.Done()
		var ResultCounter int

		for decodedResult := range DecodedSamplesChannel {
			// Discard decoded results
			ResultCounter += len(decodedResult[0])

		}
		fmt.Println("Test: Not waiting anymore", ResultCounter)
	}()

	fmt.Println("Test: Waiting...")
	WG.Wait()

}

func TestMain(m *testing.M) {
	// Run tests
	fmt.Println("Running Decoder  Test:")
	exitCode := m.Run()

	// Exit with the test result code
	os.Exit(exitCode)
}
