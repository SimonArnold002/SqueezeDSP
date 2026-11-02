// Package: github.com/Foxenfurter/foxAudioLib/foxAudioDecoder
// filename foxAudioDecoder.go
// package calls a decoder  based upon the supplied format
// and then processes the returned bytestream
// either to standardd out or to the supplied file name
package foxAudioDecoder

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	//	"github.com/Foxenfurter/foxAudioLib/foxAudioDecoder/foxAdapterDecoder"
	"github.com/Foxenfurter/foxAudioLib/foxAudioDecoder/foxDSDReader"
	"github.com/Foxenfurter/foxAudioLib/foxAudioDecoder/foxWavReader"
	"github.com/Foxenfurter/foxAudioLib/foxLog"

	//"github.com/Foxenfurter/foxAudioLib/foxLog"
	"github.com/Foxenfurter/foxAudioLib/foxUtils"
)

const packageName = "foxAudioDecoder"

type AudioDecoder struct {
	SampleRate  int
	BitDepth    int
	NumChannels int
	BigEndian   bool
	Size        int64
	Type        string
	Filename    string
	File        io.Reader
	//FFmpegPath     string // add this
	WavDecoder *foxWavReader.WavReader
	//AdapterDecoder *foxAdapterDecoder.AdapterDecoder
	StartTime time.Duration

	TargetFrameSize int
	TotalSamples    int64
	DebugFunc       func(string)
	RawPeak         float64
	TimeStamp       string

	// DSD (SqueezeDSP fork): a DSF or DSDIFF file decoded to SampleRate here
	DSDReader *foxDSDReader.Reader
	EndTime   time.Duration // where the decode ends, from the file's start; 0 = to the end
	DSDCutoff float64       // Hz; 0 = none
	DSDGain   float64       // dB
}

// NewDecoder creates a new decoder with implicit file opening or Stdin setup
func (myDecoder *AudioDecoder) Initialise() error {
	const functionName = "Initialise"
	// We need to be able to read the header in the filestream before we do any processing
	// we are going to peak this and 1000 bytes should cover almost all formats.
	if myDecoder.DebugFunc != nil {
		myDecoder.debug(packageName + ":" + functionName + ": Initialising...")
	}
	if strings.ToUpper(myDecoder.Type) == "DSD" {
		return myDecoder.initDSD()
	}

	var r io.Reader

	if myDecoder.Filename == "" || myDecoder.Filename == "-" {
		stat, err := os.Stdin.Stat()
		if err != nil {
			return err
		}

		if (stat.Mode() & os.ModeCharDevice) == 0 {
			r = os.Stdin
		} else {
			return errors.New("no file specified and stdin is not a pipe")
		}

	} else {
		f, err := os.Open(filepath.ToSlash(filepath.Clean(myDecoder.Filename)))
		if err != nil {
			return err
		}
		r = f
	}

	// Normalize behavior with buffering
	//myDecoder.File = bufio.NewReader(r)
	myDecoder.File = bufio.NewReaderSize(r, 512*1024)

	// Initialize decoder based on type
	// Decide which encoder to use
	switch strings.ToUpper(myDecoder.Type) {
	case "WAV":
		initWavDecoder(myDecoder)

		//Init the header
		err := myDecoder.WavDecoder.DecodeWavHeader()
		if err != nil {
			return err
		}
		// Read the audio header data into a buffer
		myDecoder.BitDepth = int(myDecoder.WavDecoder.BitDepth)
		myDecoder.SampleRate = int(myDecoder.WavDecoder.SampleRate)
		myDecoder.NumChannels = int(myDecoder.WavDecoder.NumChannels)

		myDecoder.Size = int64(myDecoder.WavDecoder.Size)
		if myDecoder.TargetFrameSize == 0 {
			myDecoder.TargetFrameSize = myDecoder.SampleRate / 10
		}
		myDecoder.WavDecoder.TargetFrameSize = myDecoder.TargetFrameSize

	case "PCM":
		initWavDecoder(myDecoder)

		//Do not Init the header instead we will have received the necessary header information as arguments

		myDecoder.WavDecoder.BitDepth = int(myDecoder.BitDepth)
		myDecoder.WavDecoder.SampleRate = int(myDecoder.SampleRate)
		myDecoder.WavDecoder.NumChannels = int(myDecoder.NumChannels)
		if myDecoder.BigEndian {
			myDecoder.WavDecoder.ByteOrder = binary.BigEndian
			myDecoder.WavDecoder.LittleEndian = false
		} else {
			myDecoder.WavDecoder.ByteOrder = binary.LittleEndian
			myDecoder.WavDecoder.LittleEndian = true
		}
		myDecoder.WavDecoder.IgnoreDataLength = true

		myDecoder.Size = int64(myDecoder.WavDecoder.GetMaxDataSize())
		if myDecoder.TargetFrameSize == 0 {
			myDecoder.TargetFrameSize = myDecoder.SampleRate / 10
		}
		myDecoder.WavDecoder.TargetFrameSize = myDecoder.TargetFrameSize

	default:
		// reverted to wav as the defaul
		initWavDecoder(myDecoder)

		//Init the header
		err := myDecoder.WavDecoder.DecodeWavHeader()
		if err != nil {
			return err
		}
		// Read the audio header data into a buffer
		myDecoder.BitDepth = int(myDecoder.WavDecoder.BitDepth)
		myDecoder.SampleRate = int(myDecoder.WavDecoder.SampleRate)
		myDecoder.NumChannels = int(myDecoder.WavDecoder.NumChannels)

		myDecoder.Size = int64(myDecoder.WavDecoder.Size)
		if myDecoder.TargetFrameSize == 0 {
			myDecoder.TargetFrameSize = myDecoder.SampleRate / 10
		}
		myDecoder.WavDecoder.TargetFrameSize = myDecoder.TargetFrameSize

		/*ffmpegPath := myDecoder.FFmpegPath
		if ffmpegPath == "" {
			ffmpegPath = "ffmpeg"
		}

		adapter := &foxAdapterDecoder.AdapterDecoder{
			DebugFunc:  myDecoder.DebugFunc,
			Input:      r,
			FFmpegPath: ffmpegPath,
		}

		if err := adapter.StartAdapter(myDecoder.SampleRate, myDecoder.NumChannels, myDecoder.StartTime); err != nil {
			return fmt.Errorf("%s:%s:failed to start decoder: %w", packageName, functionName, err)
		}

		myDecoder.AdapterDecoder = adapter
		myDecoder.File = nil // Clear the file reference since the adapter is now handling the input
		// */

	}

	// --skip (LMS's seek): start part way in
	if myDecoder.StartTime > 0 {
		if err := myDecoder.skip(r); err != nil {
			return err
		}
	}

	// It is important that the low level decoder packages support these setter functions

	myDecoder.debug(fmt.Sprintf(packageName+":"+functionName+" SampleRate: [%v] Channels: [%v] BitDepth: [%v] Size [%v] TargetFrameSize [%v]",
		myDecoder.SampleRate, myDecoder.NumChannels, myDecoder.BitDepth, myDecoder.Size, myDecoder.TargetFrameSize))

	return nil
}

// skip passes over the audio before StartTime, in whole frames: a file is sought past it, a pipe has
// it read away. The size left shrinks to match, so the WAV the engine writes states what remains.
func (myDecoder *AudioDecoder) skip(r io.Reader) error {
	wd := myDecoder.WavDecoder
	frameBytes := int64(wd.NumChannels * (wd.BitDepth / 8))
	if frameBytes <= 0 || wd.SampleRate <= 0 {
		return nil
	}
	n := int64(myDecoder.StartTime.Seconds()*float64(wd.SampleRate)) * frameBytes
	if !wd.IgnoreDataLength && n > int64(wd.Size) {
		n = int64(wd.Size) / frameBytes * frameBytes
	}
	if n <= 0 {
		return nil
	}
	br := myDecoder.File.(*bufio.Reader)
	sought := false
	if f, ok := r.(*os.File); ok && n > int64(br.Buffered()) {
		// the file is ahead of what the buffer has handed out by the bytes it holds
		if _, err := f.Seek(n-int64(br.Buffered()), io.SeekCurrent); err == nil {
			br.Reset(f)
			sought = true
		}
	}
	if !sought {
		if _, err := io.CopyN(io.Discard, br, n); err != nil {
			return fmt.Errorf("%s: skip: %w", packageName, err)
		}
	}
	if !wd.IgnoreDataLength {
		wd.Size -= uint32(n)
		myDecoder.Size = int64(wd.Size)
	}
	myDecoder.debug(fmt.Sprintf("%s: skipped %d bytes (%.3f s), sought=%v", packageName, n, myDecoder.StartTime.Seconds(), sought))
	return nil
}

// initDSD opens a DSF or DSDIFF file for decoding to SampleRate, from StartTime to EndTime. It reads the file
// itself (it seeks), so a DSD input is always a file. As 24-bit PCM for the size a WAV header states.
func (myDecoder *AudioDecoder) initDSD() error {
	if myDecoder.Filename == "" || myDecoder.Filename == "-" {
		return errors.New(packageName + ": DSD needs a file, not stdin")
	}
	r := &foxDSDReader.Reader{
		Filename:        filepath.ToSlash(filepath.Clean(myDecoder.Filename)),
		OutRate:         myDecoder.SampleRate,
		Cutoff:          myDecoder.DSDCutoff,
		GainDB:          myDecoder.DSDGain,
		StartTime:       myDecoder.StartTime,
		EndTime:         myDecoder.EndTime,
		TargetFrameSize: myDecoder.TargetFrameSize,
		DebugFunc:       myDecoder.DebugFunc,
	}
	if err := r.Open(); err != nil {
		r.Close()
		return err
	}
	myDecoder.DSDReader = r
	myDecoder.NumChannels = r.NumChannels
	myDecoder.BitDepth = 24
	myDecoder.Size = r.Size()
	if myDecoder.TargetFrameSize == 0 {
		myDecoder.TargetFrameSize = myDecoder.SampleRate / 10
		r.TargetFrameSize = myDecoder.TargetFrameSize
	}
	myDecoder.debug(fmt.Sprintf("%s: DSD %d Hz x%d -> %d Hz, %d frames", packageName, r.DSDRate, r.NumChannels, myDecoder.SampleRate, r.Frames))
	return nil
}

func initWavDecoder(myDecoder *AudioDecoder) {
	myDecoder.WavDecoder = &foxWavReader.WavReader{}
	myDecoder.WavDecoder.Input = myDecoder.File // already 512KB buffered in Initialise
	myDecoder.WavDecoder.DebugFunc = myDecoder.DebugFunc
}

func (myDecoder *AudioDecoder) ConfigureFrameSize(mode foxUtils.ProcessingMode) {
	myDecoder.TargetFrameSize = foxUtils.CalculateChunkSize(myDecoder.SampleRate, mode)
	if myDecoder.WavDecoder != nil {
		myDecoder.WavDecoder.TargetFrameSize = myDecoder.TargetFrameSize
	}
	if myDecoder.DSDReader != nil {
		myDecoder.DSDReader.TargetFrameSize = myDecoder.TargetFrameSize
	}
	/*	if myDecoder.AdapterDecoder != nil {
			myDecoder.AdapterDecoder.TargetFrameSize = myDecoder.TargetFrameSize
		}
	*/
}

func (myDecoder *AudioDecoder) Close() error {
	const functionName = "Close"
	if myDecoder.DSDReader != nil {
		return myDecoder.DSDReader.Close()
	}
	// Adapter case — r is owned by the adapter, nothing to close here
	/*if myDecoder.AdapterDecoder != nil {
		return nil
	}*/
	if myDecoder.File != nil {
		myDecoder.debug(packageName + ":" + functionName + ": Closing file handle for: " + myDecoder.Filename)
		if f, ok := myDecoder.File.(*os.File); ok {
			return f.Close()
		}
	}
	return nil
}

// Should call the lower level function and pass in the Channel to be used for transmitting decoded samples
// Current version of wav loader does not need throttling
func (myDecoder *AudioDecoder) DecodeSamples(DecodedSamplesChannel chan [][]float64) error {
	if myDecoder.DSDReader != nil {
		err := myDecoder.DSDReader.DecodeInput(DecodedSamplesChannel)
		myDecoder.TotalSamples = myDecoder.DSDReader.TotalSamples
		myDecoder.RawPeak = myDecoder.DSDReader.RawPeak
		return err
	}

	err := myDecoder.WavDecoder.DecodeInput(DecodedSamplesChannel)
	myDecoder.TotalSamples = myDecoder.WavDecoder.TotalSamples
	myDecoder.RawPeak = myDecoder.WavDecoder.RawPeak
	return err

	/*switch strings.ToUpper(myDecoder.Type) {
	case "WAV", "PCM":
		err := myDecoder.WavDecoder.DecodeInput(DecodedSamplesChannel)
		myDecoder.TotalSamples = myDecoder.WavDecoder.TotalSamples
		myDecoder.RawPeak = myDecoder.WavDecoder.RawPeak
		return err

	default:
		err := myDecoder.AdapterDecoder.DecodeInput(DecodedSamplesChannel)
		myDecoder.TotalSamples = myDecoder.AdapterDecoder.TotalSamples
		myDecoder.RawPeak = myDecoder.AdapterDecoder.RawPeak
		return err
	}*/
}

// Simple file loader, returns a buffer with samples
func (myInputDecoder *AudioDecoder) LoadFiletoSampleBuffer(inputFile string, fileType string, myLogger *foxLog.Logger) ([][]float64, error) {
	const functionName = "LoadFiletoBuffer"
	const MsgHeader = packageName + ": " + functionName + ": "

	if myInputDecoder.DebugFunc != nil {
		myInputDecoder.debug(MsgHeader + " Loading file...")
	}
	fileInfo, err := os.Stat(inputFile)
	os.IsNotExist(err)
	if err != nil {
		return nil, fmt.Errorf("input file %s does not exist", inputFile)
	}

	modTime := fileInfo.ModTime()

	// Decode the audio file
	myInputDecoder.Type = fileType
	myInputDecoder.Filename = inputFile

	err = myInputDecoder.Initialise()

	if err != nil {
		return nil, fmt.Errorf("%s: decoder init failed: %v", functionName, err)
	}
	myInputDecoder.TimeStamp = modTime.UTC().Format(time.RFC3339)
	if myInputDecoder.DebugFunc != nil {
		myInputDecoder.debug(MsgHeader + fmt.Sprintf("File Decoder initialized: SampleRate=%d, Channels=%d, Type=%s", myInputDecoder.SampleRate, myInputDecoder.NumChannels, myInputDecoder.Type))
	}
	outputSamples := make([][]float64, myInputDecoder.NumChannels)
	var WG sync.WaitGroup
	DecodedSamplesChannel := make(chan [][]float64, 1)
	var decodeErr error
	var errMu sync.Mutex // guards decodeErr if you ever add more writers

	WG.Add(1)
	go func() {
		defer func() {
			close(DecodedSamplesChannel)
			WG.Done()
		}()
		if err := myInputDecoder.DecodeSamples(DecodedSamplesChannel); err != nil {
			errMu.Lock()
			decodeErr = err
			errMu.Unlock()
		}
	}()

	WG.Add(1)
	go func() {
		defer WG.Done()
		for i := range outputSamples {
			outputSamples[i] = make([]float64, 0)
		}
		for samples := range DecodedSamplesChannel {
			for channelIdx, channelData := range samples {
				outputSamples[channelIdx] = append(outputSamples[channelIdx], channelData...)
			}
		}
	}()

	WG.Wait()

	if decodeErr != nil {
		return nil, fmt.Errorf("%s: decode failed: %w", functionName, decodeErr)
	}
	return outputSamples, nil
} // <-- LoadFiletoBuffer ends here

// Some functions for identifying the most common audio formats from the header in the bytestream under 1000 bytes needed.
func identifyFormat(data []byte) string {
	// Define format checks with offset and magic bytes
	type formatCheck struct {
		offset  int
		pattern []byte
		format  string
	}

	checks := []formatCheck{
		// WAV/RIFF
		{0, []byte{0x52, 0x49, 0x46, 0x46}, "WAV"},
		// MP3 with ID3 tag
		{0, []byte{0x49, 0x44, 0x33}, "MP3"},
		// FLAC
		{0, []byte{0x66, 0x4C, 0x61, 0x43}, "FLAC"},
		// AIFF ("FORM" at 0, then "AIFF" at 8)
		{0, []byte{0x46, 0x4F, 0x52, 0x4D}, "AIFF"},
		// Ogg
		{0, []byte{0x4F, 0x67, 0x67, 0x53}, "Ogg"},
		// WMA/ASF GUID (first 16 bytes)
		{0, []byte{0x30, 0x26, 0xB2, 0x75, 0x8E, 0x66, 0xCF, 0x11, 0xA6, 0xD9, 0x00, 0xAA, 0x00, 0x62, 0xCE, 0x6C}, "WMA"},
		// M4A (MP4) - "ftyp" at offset 4
		{4, []byte{0x66, 0x74, 0x79, 0x70}, "ftyp"},
		// APE - "MAC " at start
		{0, []byte{0x4D, 0x41, 0x43, 0x20}, "APE"},
		// MIDI - "MThd" at 0
		{0, []byte{0x4D, 0x54, 0x68, 0x64}, "MIDI"},
		// AMR - "#!AMR"
		{0, []byte{0x23, 0x21, 0x41, 0x4D, 0x52}, "AMR"},
		// AU - ".snd"
		{0, []byte{0x2E, 0x73, 0x6E, 0x64}, "AU"},
	}

	var format string

	for _, check := range checks {
		end := check.offset + len(check.pattern)
		if end > len(data) {
			continue
		}
		if compareBytes(data[check.offset:end], check.pattern) {
			return check.format // Corrected: return the found format immediately
		}
	}

	if format == "ftyp" {
		if len(data) >= 12 {
			brand := data[8:12]
			if compareBytes(brand, []byte{0x6D, 0x70, 0x34, 0x32}) || compareBytes(brand, []byte{0x6D, 0x34, 0x61, 0x20}) {
				return "M4A"
			} else {
				return "MP4"
			}
		}
	}

	// Fallback for MP3 without ID3 tag (check frame sync)
	if len(data) >= 2 && data[0] == 0xFF && (data[1]&0xE0) == 0xE0 {
		return "MP3"
	}

	return "Unknown"
}

// Example compareBytes function (assumed to be correctly implemented)
func compareBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i, v := range a {
		if v != b[i] {
			return false
		}
	}
	return true
}

// Function to handle debug calls, allowing for different logging implementations
func (myDecoder *AudioDecoder) debug(message string) {

	if myDecoder.DebugFunc != nil {
		myDecoder.DebugFunc(message)
	} else { // if no external debug function available just print the message
		println(message)
	}

}
