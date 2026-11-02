// Package: github.com/Foxenfurter/foxAudioLib/foxAudioDecoder/foxAdapterDecoder
// file foxAdapterDecoder.go
// pkg for encoding a stream using an embdeded decoder  format. The package has been specified to allow a header to be written in the initial phase
// the body to be following this. The package is expected to be called as part of an encoder function. which will run asynchronously
package foxAdapterDecoder

import (
	"encoding/binary"
	"os/exec"
	"strconv"
	"time"

	"bytes"
	"fmt"
	"io"
	"math"
	"math/bits"
	"unsafe"
)

// Structure holds basic information about the Samples to be encoded
// this is a type neutral representation of the WavHeader structure
type AdapterDecoder struct {
	SampleRate       int
	BitDepth         int
	NumChannels      int
	Size             uint32
	ReaderCursor     int
	LittleEndian     bool
	ByteOrder        binary.ByteOrder
	wordLength       int
	bytesPerSample   int
	Input            io.Reader
	Output           io.Reader
	AudioFormat      WaveFormat
	DebugFunc        func(string)
	TotalSamples     int64
	TargetFrameSize  int
	RawPeak          float64
	IgnoreDataLength bool
	Length           uint32
	FFmpegPath       string
	AdapterCmd       *exec.Cmd
	stderrBuf        *bytes.Buffer
	stdinPipe        io.WriteCloser

	waitErr  error
	waitDone chan struct{}
}

const packageName = "foxAdapterDecoder"

type WaveFormat uint16

const (
	PCM             WaveFormat = 1
	ADPCM           WaveFormat = 2
	IEEE_FLOAT      WaveFormat = 3
	INTERNAL_DOUBLE WaveFormat = 4
	EXTENSIBLE      WaveFormat = 0xFFFE
)

const scale16Bit float64 = 1 / 32768.0
const scale24Bit float64 = 1 / 8388608.0
const scale32Bit float64 = 1 / 2147483648.0

const processingBufferMultiple = 3 // hold 3× target to absorb read jitter

const maxDataSize uint32 = math.MaxUint32

func SetAdapterArgs(sampleRate, channels int, startTime time.Duration) []string {
	args := []string{}
	if startTime > 0 {
		// -ss before -i for input seek (fast, keyframe-accurate enough for audio)
		args = append(args, "-ss", fmt.Sprintf("%.3f", startTime.Seconds()))
	}

	args = append(args,
		"-nostdin",
		"-loglevel", "warning",
		"-err_detect", "ignore_err",
		"-vn", "-sn", "-dn",
		"-i", "pipe:0",
		"-map", "0:a",
		"-acodec", "pcm_f64le",
		"-f", "f64le",
		"-flush_packets", "1",
		"-ac", strconv.Itoa(channels),
		"-ar", strconv.Itoa(sampleRate),

		"pipe:1", // ← explicit pipe rather than "-"

	)

	return args
}

// StartAdapter initializes the adapter by starting the specified decoder (e.g., ffmpeg) as a subprocess.
// It reads encoded data from the provided io.Reader and outputs float64 samples
// via the existing DecodeInput method.
func (fd *AdapterDecoder) StartAdapter(mySampleRate int, myChannels int, startTime time.Duration) error {
	MsgHeader := packageName + ":StartAdapter: "
	if fd.AdapterCmd != nil {
		return fmt.Errorf("adapter already started")
	}

	args := SetAdapterArgs(mySampleRate, myChannels, startTime)
	cmd := exec.Command(fd.FFmpegPath, args...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdin pipe: %w", err)
	}

	pr, pw := io.Pipe()
	cmd.Stdout = pw
	fd.Output = pr // Set Output to the read end of the pipe for DecodeInput to consume

	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	fd.debug(fmt.Sprintf(MsgHeader+"ffmpeg command: %s %v", fd.FFmpegPath, args))

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	// Feed input
	go func() {
		n, err := io.Copy(stdin, fd.Input)
		fd.debug(fmt.Sprintf(MsgHeader+"input copy done: %d bytes, err=%v", n, err))
		stdin.Close()
		fd.debug(MsgHeader + "FFmpeg stdin closed")
	}()

	// Close the write end of the pipe when FFmpeg exits
	// This is what delivers io.EOF to DecodeInput's reader
	fd.waitDone = make(chan struct{})
	go func() {
		defer func() {
			if r := recover(); r != nil {
				fd.debug(fmt.Sprintf(MsgHeader+"PANIC in wait goroutine: %v", r))
			}
			close(fd.waitDone)
		}()
		fd.waitErr = cmd.Wait()
		stderr := stderrBuf.String()
		exitCode := -1
		if cmd.ProcessState != nil {
			exitCode = cmd.ProcessState.ExitCode()
		}
		fd.debug(fmt.Sprintf(MsgHeader+"FFmpeg exited code=%d err=%v", exitCode, fd.waitErr))
		if stderr != "" {
			fd.debug(MsgHeader + "FFmpeg stderr: " + stderr)
		}
		pw.Close()
		fd.debug(MsgHeader + "pw.Close() called")
	}()

	fd.stdinPipe = stdin
	fd.AdapterCmd = cmd
	fd.SampleRate = mySampleRate
	fd.BitDepth = 64
	fd.NumChannels = myChannels
	fd.LittleEndian = true
	fd.ByteOrder = binary.LittleEndian
	fd.AudioFormat = IEEE_FLOAT
	fd.IgnoreDataLength = true

	if fd.TargetFrameSize == 0 {
		fd.TargetFrameSize = mySampleRate / 10
	}

	//fd.Output = bufio.NewReader(pr)
	return nil
}

func (fd *AdapterDecoder) DecodeInput(DecodedSamplesChannel chan [][]float64) error {
	MsgHeader := packageName + ":DecodeInput: "

	bytesPerSample := 8
	frameSize := fd.NumChannels * bytesPerSample

	targetFrames := fd.TargetFrameSize
	if targetFrames == 0 {
		targetFrames = fd.SampleRate / 10
	}
	batchCap := targetFrames * frameSize

	// Read directly from the pipe — no bufio wrapper
	tmp := make([]byte, batchCap)
	batch := make([]byte, 0, batchCap*2)

	var totalFrames int64

	dispatch := func() {
		aligned := (len(batch) / frameSize) * frameSize
		if aligned == 0 {
			return
		}
		samples, err := fd.ConvertBytesToFloat64(batch[:aligned])
		if err != nil {
			fd.debug(MsgHeader + fmt.Sprintf("convert error: %v", err))
		} else {
			DecodedSamplesChannel <- samples
			totalFrames += int64(len(samples[0]))
			//fd.debug(MsgHeader + fmt.Sprintf("sent %d frames (total %d)", len(samples[0]), totalFrames))
		}
		remainder := len(batch) - aligned
		if remainder > 0 {
			copy(batch, batch[aligned:])
		}
		batch = batch[:remainder]
	}

	for {
		n, err := fd.Output.Read(tmp)
		if n > 0 {
			batch = append(batch, tmp[:n]...)
			if len(batch) >= batchCap {
				dispatch()
			}
		}
		if err != nil {
			dispatch()
			if err != io.EOF {
				fd.debug(MsgHeader + fmt.Sprintf("read error: %v", err))
			} else {
				fd.debug(MsgHeader + "EOF - all frames delivered")
			}
			break
		}
	}

	<-fd.waitDone
	if fd.waitErr != nil {
		fd.debug(MsgHeader + fmt.Sprintf("FFmpeg wait error: %v", fd.waitErr))
	}
	fd.TotalSamples = totalFrames
	fd.debug(MsgHeader + fmt.Sprintf("decoded %d frames total", totalFrames))
	return nil
}

// CloseFFmpeg is optional; DecodeInput already waits. Kept for symmetry.
func (fd *AdapterDecoder) CloseAdapter() error {
	if fd.AdapterCmd != nil {
		return fd.AdapterCmd.Wait()
	}
	return nil
}

// Function to handle debug calls, allowing for different logging implementations
func (myDecoder *AdapterDecoder) debug(message string) {

	if myDecoder.DebugFunc != nil {
		myDecoder.DebugFunc(message)
	} else { // if no external debug function available just print the message
		println(message)
	}

}
func (FD *AdapterDecoder) ConvertBytesToFloat64(myBytes []byte) ([][]float64, error) {
	numChannels := FD.NumChannels
	bytesPerSample := (FD.BitDepth / 8) * numChannels
	numSamples := len(myBytes) / bytesPerSample

	samples := make([][]float64, numChannels)
	for c := range samples {
		samples[c] = make([]float64, numSamples)
	}

	var rawPeak float64

	dataPtr := unsafe.Pointer(unsafe.SliceData(myBytes))

	switch FD.BitDepth {
	case 16:
		FD.convert16Bit(dataPtr, samples, numSamples, &rawPeak, len(myBytes))
	case 24:
		FD.convert24Bit(dataPtr, samples, numSamples, &rawPeak, len(myBytes))
	case 32:
		FD.convert32Bit(dataPtr, samples, numSamples, &rawPeak, len(myBytes))
	case 64:
		FD.convert64Bit(dataPtr, samples, numSamples, &rawPeak, len(myBytes))
	}

	if rawPeak > FD.RawPeak {
		FD.RawPeak = rawPeak
	}

	return samples, nil
}

func (FD *AdapterDecoder) convert16Bit(dataPtr unsafe.Pointer, samples [][]float64, numSamples int, rawPeak *float64, lenmyBytes int) {
	const bytesPerSample = 2

	step := bytesPerSample * FD.NumChannels
	// Calculate the start address of myBytes for offset checks
	start := uintptr(dataPtr)
	for c := 0; c < FD.NumChannels; c++ {
		chanPtr := unsafe.Pointer(uintptr(dataPtr) + uintptr(c*bytesPerSample))
		channel := samples[c]

		for s := range numSamples {
			offset := uintptr(chanPtr) - start
			if int(offset)+2 > lenmyBytes {
				return
			}
			var val int16
			if FD.LittleEndian {
				val = int16(binary.LittleEndian.Uint16((*[2]byte)(chanPtr)[:]))
			} else {
				val = int16(binary.BigEndian.Uint16((*[2]byte)(chanPtr)[:]))
			}

			converted := float64(val) * scale16Bit
			if abs := math.Abs(converted); abs > *rawPeak {
				*rawPeak = abs
			}
			channel[s] = converted
			chanPtr = unsafe.Pointer(uintptr(chanPtr) + uintptr(step))
		}
	}
}

func (FD *AdapterDecoder) convert24Bit(dataPtr unsafe.Pointer, samples [][]float64, numSamples int, rawPeak *float64, lenmyBytes int) {
	const bytesPerSample = 3

	step := bytesPerSample * FD.NumChannels
	// Calculate the start address of myBytes for offset checks
	start := uintptr(dataPtr)
	for c := 0; c < FD.NumChannels; c++ {
		chanPtr := unsafe.Pointer(uintptr(dataPtr) + uintptr(c*bytesPerSample))
		channel := samples[c]

		for s := range numSamples {
			offset := uintptr(chanPtr) - start
			if int(offset)+3 > lenmyBytes {
				return
			}
			b := (*[3]byte)(chanPtr)

			var raw int32
			if FD.LittleEndian {
				// Little-endian: bytes are [LSB, Mid, MSB]

				raw = int32(b[2])<<16 | int32(b[1])<<8 | int32(b[0])
			} else {
				// Big-endian: bytes are [MSB, Mid, LSB]
				raw = int32(b[0])<<16 | int32(b[1])<<8 | int32(b[2])
			}
			// Sign extend 24-bit to 32-bit
			raw = (raw << 8) >> 8 // Equivalent to arithmetic shift

			converted := float64(raw) * scale24Bit
			if abs := math.Abs(converted); abs > *rawPeak {
				*rawPeak = abs
			}
			channel[s] = converted
			chanPtr = unsafe.Pointer(uintptr(chanPtr) + uintptr(step))
		}
	}
}

func (FD *AdapterDecoder) convert32Bit(dataPtr unsafe.Pointer, samples [][]float64, numSamples int, rawPeak *float64, lenmyBytes int) {
	const bytesPerSample = 4
	step := bytesPerSample * FD.NumChannels // bytes per full frame
	start := uintptr(dataPtr)

	// Loop over channels first (like the 64-bit version)
	for c := 0; c < FD.NumChannels; c++ {
		// Start of this channel's first sample
		chanPtr := unsafe.Pointer(uintptr(dataPtr) + uintptr(c*bytesPerSample))
		channel := samples[c] // assumes slices are correctly sized

		for s := range numSamples {
			// Safety: ensure we don't read beyond the buffer
			offset := uintptr(chanPtr) - start
			if int(offset)+bytesPerSample > lenmyBytes {
				return
			}

			var converted float64
			if FD.AudioFormat == PCM {
				// 32-bit signed integer PCM
				raw := binary.LittleEndian.Uint32((*[4]byte)(chanPtr)[:])
				if !FD.LittleEndian {
					raw = bits.ReverseBytes32(raw)
				}
				converted = float64(int32(raw)) * scale32Bit
			} else {
				// 32-bit IEEE float
				raw := binary.LittleEndian.Uint32((*[4]byte)(chanPtr)[:])
				if !FD.LittleEndian {
					raw = bits.ReverseBytes32(raw)
				}
				converted = float64(math.Float32frombits(raw))
			}

			// Update peak
			if abs := math.Abs(converted); abs > *rawPeak {
				*rawPeak = abs
			}

			// Store sample
			channel[s] = converted

			// Move to the next sample of the same channel
			chanPtr = unsafe.Pointer(uintptr(chanPtr) + uintptr(step))
		}
	}
}

func (FD *AdapterDecoder) convert64Bit(dataPtr unsafe.Pointer, samples [][]float64, numSamples int, rawPeak *float64, lenmyBytes int) {
	const bytesPerSample = 8
	step := bytesPerSample * FD.NumChannels
	start := uintptr(dataPtr)
	for c := 0; c < FD.NumChannels; c++ {
		chanPtr := unsafe.Pointer(uintptr(dataPtr) + uintptr(c*bytesPerSample))
		channel := samples[c]

		for s := range numSamples {
			offset := uintptr(chanPtr) - start
			if int(offset)+8 > lenmyBytes {
				return
			}
			if FD.AudioFormat == IEEE_FLOAT || FD.AudioFormat == INTERNAL_DOUBLE {
				val := math.Float64frombits(FD.ByteOrder.Uint64((*[8]byte)(chanPtr)[:]))
				channel[s] = val
				if abs := math.Abs(val); abs > *rawPeak {
					*rawPeak = abs
				}
			} else {
				// Mimic C#: Return 0 for 64-bit PCM (unsupported)
				channel[s] = 0
			}
			chanPtr = unsafe.Pointer(uintptr(chanPtr) + uintptr(step))
		}
	}
}

//	Getters and Setters
//
// ------------Getters----------------------------------------
// GetSampleRate returns the sample rate of the FoxDecoder.
func (fd *AdapterDecoder) GetSampleRate() int {
	return fd.SampleRate
}

// GetBitDepth returns the bit depth of the FoxDecoder.
func (fd *AdapterDecoder) GetBitDepth() int {
	return fd.BitDepth
}

// GetNumChannels returns the number of channels in the FoxDecoder.
func (fd *AdapterDecoder) GetNumChannels() int {
	return fd.NumChannels
}

// GetSize returns the size of the FoxDecoder.
func (fd *AdapterDecoder) GetSize() uint32 {
	return fd.Size
}

// GetReaderCursor returns the reader cursor position of the FoxDecoder.
func (fd *AdapterDecoder) GetReaderCursor() int {
	return fd.ReaderCursor
}

// SetEndianess sets the Endianess field of FoxDecoder.
func (fd *AdapterDecoder) GetLittleEndian() bool {
	return fd.LittleEndian

}
func (fd *AdapterDecoder) GetAudioFormatString() string {
	return fmt.Sprintf("%d", fd.AudioFormat)
}

func (fd *AdapterDecoder) GetMaxDataSize() uint32 {
	return maxDataSize
}

func (fd *AdapterDecoder) GetAudioFormat() WaveFormat {
	return fd.AudioFormat
}

// --------------Setters--------------------------------
// SetSampleRate sets the SampleRate field of FoxDecoder.
func (fd *AdapterDecoder) SetSampleRate(sampleRate int) {
	fd.SampleRate = sampleRate
}

// SetBitDepth sets the BitDepth field of FoxDecoder.
func (fd *AdapterDecoder) SetBitDepth(bitDepth int) {
	fd.BitDepth = bitDepth
}

// SetNumChannels sets the NumChannels field of FoxDecoder.
func (fd *AdapterDecoder) SetNumChannels(numChannels int) {
	fd.NumChannels = numChannels
}

// SetSize sets the Size field of FoxDecoder.
func (fd *AdapterDecoder) SetSize(size uint32) {
	fd.Size = size
}

// SetReaderCursor sets the ReaderCursor field of FoxDecoder.
func (fd *AdapterDecoder) SetReaderCursor(readerCursor int) {
	fd.ReaderCursor = readerCursor
}

// SetEndianess sets the Endianess field of FoxDecoder.
func (fd *AdapterDecoder) SetLittleEndian(endianNess bool) {
	fd.LittleEndian = endianNess
}
