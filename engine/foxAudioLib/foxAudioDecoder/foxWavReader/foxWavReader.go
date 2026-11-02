// Package: github.com/Foxenfurter/foxAudioLib/foxAudioDecoder/foxWavReader
// file foxWavReader.go
// pkg for encoding a stream into wav format. The package has been specified to allow a header to be written in the initial phase
// the body to be following this. The package is expected to be called as part of an encoder function. which will run asynchronously
package foxWavReader

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"math/bits"

	"sync"
	"unsafe"

	"github.com/google/uuid"
)

// Structure holds basic information about the Samples to be encoded
// this is a type neutral representation of the WavHeader structure
type WavReader struct {
	SampleRate      int
	BitDepth        int
	NumChannels     int
	Size            uint32
	ReaderCursor    int
	LittleEndian    bool
	ByteOrder       binary.ByteOrder
	wordLength      int
	bytesPerSample  int
	Input           io.Reader // Changed from *os.File to io.Reader to *bufio.Reader back to io.Reader
	AudioFormat     WaveFormat
	DebugFunc       func(string)
	TotalSamples    int64
	TargetFrameSize int
	// additional for PCM

	RawPeak          float64
	IgnoreDataLength bool
	Length           uint32
}

// Holds detailed information about the wav Header
type WaveFile struct {
	ok            bool
	pos           int64
	dataOffset    int64
	riff          string
	length        uint32
	wave          string
	format        string
	size          uint32
	audioFormat   WaveFormat
	numChannels   uint16
	sampleRate    uint32
	byteRate      uint32
	blockAlign    uint16
	bitsPerSample uint16
	data          string
	dataSize      uint32
	max           uint32
	channelMask   uint32
	formatEx      WaveFormatEx
	bigEndian     bool
}

const packageName = "FoxWavReader"

//	Getters and Setters
//
// ------------Getters----------------------------------------
// GetSampleRate returns the sample rate of the FoxDecoder.
func (fd *WavReader) GetSampleRate() int {
	return fd.SampleRate
}

// GetBitDepth returns the bit depth of the FoxDecoder.
func (fd *WavReader) GetBitDepth() int {
	return fd.BitDepth
}

// GetNumChannels returns the number of channels in the FoxDecoder.
func (fd *WavReader) GetNumChannels() int {
	return fd.NumChannels
}

// GetSize returns the size of the FoxDecoder.
func (fd *WavReader) GetSize() uint32 {
	return fd.Size
}

// GetReaderCursor returns the reader cursor position of the FoxDecoder.
func (fd *WavReader) GetReaderCursor() int {
	return fd.ReaderCursor
}

// SetEndianess sets the Endianess field of FoxDecoder.
func (fd *WavReader) GetLittleEndian() bool {
	return fd.LittleEndian

}
func (fd *WavReader) GetAudioFormatString() string {
	return fmt.Sprintf("%d", fd.AudioFormat)
}

func (fd *WavReader) GetMaxDataSize() uint32 {
	return maxDataSize
}

func (fd *WavReader) GetAudioFormat() WaveFormat {
	return fd.AudioFormat
}

// --------------Setters--------------------------------
// SetSampleRate sets the SampleRate field of FoxDecoder.
func (fd *WavReader) SetSampleRate(sampleRate int) {
	fd.SampleRate = sampleRate
}

// SetBitDepth sets the BitDepth field of FoxDecoder.
func (fd *WavReader) SetBitDepth(bitDepth int) {
	fd.BitDepth = bitDepth
}

// SetNumChannels sets the NumChannels field of FoxDecoder.
func (fd *WavReader) SetNumChannels(numChannels int) {
	fd.NumChannels = numChannels
}

// SetSize sets the Size field of FoxDecoder.
func (fd *WavReader) SetSize(size uint32) {
	fd.Size = size
}

// SetReaderCursor sets the ReaderCursor field of FoxDecoder.
func (fd *WavReader) SetReaderCursor(readerCursor int) {
	fd.ReaderCursor = readerCursor
}

// SetEndianess sets the Endianess field of FoxDecoder.
func (fd *WavReader) SetLittleEndian(endianNess bool) {
	fd.LittleEndian = endianNess
}

const scale8Bit float64 = 1 / 128.0
const scale16Bit float64 = 1 / 32768.0
const scale24Bit float64 = 1 / 8388608.0
const scale32Bit float64 = 1 / 2147483648.0

const processingBufferMultiple = 3 // hold 3× target to absorb read jitter

const maxDataSize uint32 = math.MaxUint32

func (FD *WavReader) ConvertBytesToFloat64(myBytes []byte) ([][]float64, error) {
	numChannels := FD.NumChannels
	bytesPerSample := (FD.BitDepth / 8) * numChannels
	numSamples := len(myBytes) / bytesPerSample

	samples := make([][]float64, numChannels)
	for c := range samples {
		samples[c] = make([]float64, numSamples)
	}

	var rawPeak float64
	//header := *(*reflect.SliceHeader)(unsafe.Pointer(&myBytes))
	//dataPtr := unsafe.Pointer(header.Data)
	dataPtr := unsafe.Pointer(unsafe.SliceData(myBytes))

	switch FD.BitDepth {
	case 8:
		FD.convert8Bit(dataPtr, samples, numSamples, &rawPeak, len(myBytes))
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

func (FD *WavReader) convert8Bit(dataPtr unsafe.Pointer, samples [][]float64, numSamples int, rawPeak *float64, lenmyBytes int) {
	const bytesPerSample = 1

	step := bytesPerSample * FD.NumChannels
	// Calculate the start address of myBytes for offset checks
	start := uintptr(dataPtr)
	for c := 0; c < FD.NumChannels; c++ {
		chanPtr := unsafe.Pointer(uintptr(dataPtr) + uintptr(c*bytesPerSample))
		channel := samples[c]

		for s := 0; s < numSamples; s++ {
			offset := uintptr(chanPtr) - start
			if int(offset)+1 > lenmyBytes {
				return
			}
			val := int8(*(*uint8)(chanPtr) - 128) // Convert to signed
			converted := float64(val) * scale8Bit
			if abs := math.Abs(converted); abs > *rawPeak {
				*rawPeak = abs
			}
			channel[s] = converted
			chanPtr = unsafe.Pointer(uintptr(chanPtr) + uintptr(step))
		}
	}
}

func (FD *WavReader) convert16Bit(dataPtr unsafe.Pointer, samples [][]float64, numSamples int, rawPeak *float64, lenmyBytes int) {
	const bytesPerSample = 2

	step := bytesPerSample * FD.NumChannels
	// Calculate the start address of myBytes for offset checks
	start := uintptr(dataPtr)
	for c := 0; c < FD.NumChannels; c++ {
		chanPtr := unsafe.Pointer(uintptr(dataPtr) + uintptr(c*bytesPerSample))
		channel := samples[c]

		for s := 0; s < numSamples; s++ {
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

func (FD *WavReader) convert24Bit(dataPtr unsafe.Pointer, samples [][]float64, numSamples int, rawPeak *float64, lenmyBytes int) {
	const bytesPerSample = 3

	step := bytesPerSample * FD.NumChannels
	// Calculate the start address of myBytes for offset checks
	start := uintptr(dataPtr)
	for c := 0; c < FD.NumChannels; c++ {
		chanPtr := unsafe.Pointer(uintptr(dataPtr) + uintptr(c*bytesPerSample))
		channel := samples[c]

		for s := 0; s < numSamples; s++ {
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

func (FD *WavReader) convert32Bit(dataPtr unsafe.Pointer, samples [][]float64, numSamples int, rawPeak *float64, lenmyBytes int) {
	const bytesPerSample = 4
	step := bytesPerSample * FD.NumChannels // bytes per full frame
	start := uintptr(dataPtr)

	// Loop over channels first (like the 64-bit version)
	for c := 0; c < FD.NumChannels; c++ {
		// Start of this channel's first sample
		chanPtr := unsafe.Pointer(uintptr(dataPtr) + uintptr(c*bytesPerSample))
		channel := samples[c] // assumes slices are correctly sized

		for s := 0; s < numSamples; s++ {
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

func (FD *WavReader) convert64Bit(dataPtr unsafe.Pointer, samples [][]float64, numSamples int, rawPeak *float64, lenmyBytes int) {
	const bytesPerSample = 8
	step := bytesPerSample * FD.NumChannels
	start := uintptr(dataPtr)
	for c := 0; c < FD.NumChannels; c++ {
		chanPtr := unsafe.Pointer(uintptr(dataPtr) + uintptr(c*bytesPerSample))
		channel := samples[c]

		for s := 0; s < numSamples; s++ {
			offset := uintptr(chanPtr) - start
			if int(offset)+8 > lenmyBytes {
				return
			}
			if FD.AudioFormat == IEEE_FLOAT || FD.AudioFormat == INTERNAL_DOUBLE {
				val := math.Float64frombits(FD.ByteOrder.Uint64((*[8]byte)(chanPtr)[:]))
				channel[s] = val
			} else {
				// Mimic C#: Return 0 for 64-bit PCM (unsupported)
				channel[s] = 0
			}
			chanPtr = unsafe.Pointer(uintptr(chanPtr) + uintptr(step))
		}
	}
}

func (FD *WavReader) DecodeInput(DecodedSamplesChannel chan [][]float64) error {
	functionName := "DecodeInput"
	MsgHeader := packageName + ":" + functionName + ": "
	var TotalBytes int64
	var TotalFrames int64
	FD.debug(MsgHeader + " entered")
	bytesPerFrame := FD.NumChannels * (FD.BitDepth / 8)
	if bytesPerFrame == 0 {
		return fmt.Errorf("invalid bytesPerFrame (BitDepth=%d)", FD.BitDepth)
	}
	targetFrames := FD.TargetFrameSize
	if targetFrames == 0 {
		targetFrames = FD.SampleRate / 10 // fallback default
	}
	targetBytes := targetFrames * bytesPerFrame

	processingBufferSize := targetBytes * processingBufferMultiple
	processingBufferSize = (processingBufferSize / bytesPerFrame) * bytesPerFrame
	if processingBufferSize < bytesPerFrame {
		processingBufferSize = bytesPerFrame
	}

	bufferedInput, ok := FD.Input.(*bufio.Reader)
	if !ok {
		bufferedInput = bufio.NewReaderSize(FD.Input, 8192)
	}
	processingBuffer := make([]byte, processingBufferSize)
	filledBytes := 0
	// The data chunk's length, where the header gave one: read up to it and no further (a file can
	// carry other chunks after its audio), then process what is held and stop. -1: read to the end of
	// the input (raw PCM, a header of unknown length). The loop used to stop as soon as the end of the
	// data was in the 512 KB read buffer, dropping whatever of it had not been handed out yet.
	dataLeft := int64(-1)
	if !FD.IgnoreDataLength && FD.Size > 0 {
		dataLeft = int64(FD.Size)
	}
	eofReceived := false
	for {
		if !eofReceived {
			maxRead := processingBufferSize - filledBytes
			if dataLeft >= 0 && int64(maxRead) > dataLeft {
				maxRead = int(dataLeft)
			}
			if maxRead > 0 {
				n, err := bufferedInput.Read(processingBuffer[filledBytes : filledBytes+maxRead])
				filledBytes += n
				if dataLeft >= 0 {
					dataLeft -= int64(n)
				}
				if err != nil {
					eofReceived = true
					FD.debug(MsgHeader + "end of input")
				}
			}
			if dataLeft == 0 {
				eofReceived = true
				FD.debug(MsgHeader + "end of the data chunk")
			}
		}

		// Process data
		if filledBytes >= targetBytes || (eofReceived && filledBytes > 0) {
			// Processable bytes is the number of bytes that can be processed in a single frame and needs to be trimmed to a bytes per frame size
			processableBytes := (filledBytes / bytesPerFrame) * bytesPerFrame
			remainingBytes := filledBytes - processableBytes

			if processableBytes > 0 {
				samples, err := FD.ConvertBytesToFloat64(processingBuffer[:processableBytes])
				if err != nil {
					return fmt.Errorf("%s: conversion error: %w", functionName, err)
				}
				DecodedSamplesChannel <- samples
				TotalBytes += int64(processableBytes)
				TotalFrames += int64(len(samples[0]))

				// Preserve remaining bytes safely
				if remainingBytes > 0 {
					copy(
						processingBuffer[:remainingBytes],
						processingBuffer[processableBytes:processableBytes+remainingBytes],
					)
				}
				filledBytes = remainingBytes

			} else if eofReceived {
				filledBytes = 0 // Discard partial frame
			}
		}
		//msgcounter++
		//if msgcounter%100 == 0 {
		//	FD.debug(fmt.Sprintf("Decode ongoing. filled Bytes + buffered: %d, buffered %d, TotalBytes %d, %d Expected Size, EOF %t", filledBytes+bufferedInput.Buffered(), bufferedInput.Buffered(), TotalBytes, FD.Size, eofReceived))
		//}
		// Exit condition: the input or the data chunk has ended and everything read is processed
		if eofReceived && filledBytes == 0 {
			break
		}
	}
	FD.TotalSamples = TotalFrames
	FD.debug(MsgHeader + fmt.Sprintf("Decoded %d frames", TotalFrames))
	return nil
}

// **************Header as per old Inguz c# library************************

type WaveFormatEx struct {
	GUID [16]byte
}

var (
	waveFormatExPCM = NewWaveFormatExString("1f0e8ffb-9db2-4f16-a2ef-47481f5dc9e5")
	//waveFormatExIEEEFloat                = NewWaveFormatExString("d478b09e-bcb9-4038-afe1-def76ec94512")
	waveFormatExIEEEFloat                = NewWaveFormatExString("00000003-0000-0010-8000-00AA00389B71")
	AMBISONIC_B_FORMAT_PCMPCMguid        = NewWaveFormatExString("e8f32218-3fc1-4361-9f41-abf9b3b72260")
	AMBISONIC_B_FORMAT_IEEE_FLOATPCMguid = NewWaveFormatExString("00000003-0721-11d3-8644-C8C1CA000000")
)

// NewWaveFormatExString creates a new WaveFormatEx instance with the specified GUID string.
func NewWaveFormatExString(guid string) WaveFormatEx {
	var waveFormatEx WaveFormatEx
	bytes, err := uuid.Parse(guid)
	if err != nil || len(bytes) != 16 {
		// Handle error appropriately (e.g., return default or panic)

		println(fmt.Sprintf("Invalid GUID: %s length GUID %v len bytes %v", guid, len(guid), len(bytes)))
	}

	copy(waveFormatEx.GUID[:], bytes[:])
	return waveFormatEx
}

// NewWaveFormatExBytes creates a new WaveFormatEx instance with the specified GUID byte array.
func NewWaveFormatExBytes(guid []byte) WaveFormatEx {
	if len(guid) != 16 {
		// Handle error appropriately (e.g., return default or panic)
		println("Invalid GUID byte array length")
	}

	var waveFormatEx WaveFormatEx
	copy(waveFormatEx.GUID[:], guid)
	return waveFormatEx
}

func (wf *WaveFormatEx) Equals(other *WaveFormatEx) bool {
	//return wf.GUID == other.GUID
	return bytes.Equal(wf.GUID[:], other.GUID[:]) // Compare raw bytes, not structs
}

func (wf *WaveFormatEx) GetHashCode() int {
	return hash(wf.GUID[:])
}

func (wf *WaveFormatEx) String() string {
	return fmt.Sprintf("%x", wf.GUID)
}

func hash(bytes []byte) int {
	var result int
	for _, b := range bytes {
		result = (result << 5) - result + int(b)
	}
	return result
}

type WaveFormat uint16

const (
	PCM             WaveFormat = 1
	ADPCM           WaveFormat = 2
	IEEE_FLOAT      WaveFormat = 3
	INTERNAL_DOUBLE WaveFormat = 4
	EXTENSIBLE      WaveFormat = 0xFFFE
)

// Function to map uint16 to WaveFormat
func mapUint16ToWaveFormat(value uint16) WaveFormat {
	switch value {
	case 1:
		return PCM
	case 2:
		return ADPCM
	case 3:
		return IEEE_FLOAT
	case 4:
		return INTERNAL_DOUBLE
	case 0xFFFE:
		return EXTENSIBLE
	default:
		// Handle unknown values, you can return an error or a default value
		return PCM // Change to an appropriate default or handle the error accordingly
	}
}

// Reusable buffer pool for fixed-size reads (4 bytes for 32-bit values, 2 for 16-bit)
var bufferPool4 = sync.Pool{
	New: func() interface{} { return make([]byte, 4) },
}

var bufferPool2 = sync.Pool{
	New: func() interface{} { return make([]byte, 2) },
}

func (w *WaveFile) readBytesFromInput(myReader io.Reader, count int) ([]byte, error) {
	buffer := make([]byte, count)
	if _, err := io.ReadFull(myReader, buffer); err != nil {
		return nil, err
	}
	w.pos += int64(count)
	return buffer, nil
}

func (w *WaveFile) readCharsFromInput(myReader io.Reader, count int) (string, error) {
	buffer := make([]byte, count)
	if _, err := io.ReadFull(myReader, buffer); err != nil {
		return "", err
	}
	w.pos += int64(count)
	return string(buffer), nil
}

func (w *WaveFile) readUInt32FromInput(myReader io.Reader) (uint32, error) {
	buf := bufferPool4.Get().([]byte)
	defer bufferPool4.Put(buf)

	if _, err := io.ReadFull(myReader, buf); err != nil {
		return 0, err
	}
	w.pos += 4
	if w.bigEndian {
		return binary.BigEndian.Uint32(buf), nil
	}
	return binary.LittleEndian.Uint32(buf), nil
}

func (w *WaveFile) readUInt16FromInput(myReader io.Reader) (uint16, error) {
	buf := bufferPool2.Get().([]byte)
	defer bufferPool2.Put(buf)

	if _, err := io.ReadFull(myReader, buf); err != nil {
		return 0, err
	}
	w.pos += 2
	if w.bigEndian {
		return binary.BigEndian.Uint16(buf), nil
	}
	return binary.LittleEndian.Uint16(buf), nil
}

func (w *WaveFile) readInt32FromInput(myReader io.Reader) (int32, error) {
	buf := bufferPool4.Get().([]byte)
	defer bufferPool4.Put(buf)

	if _, err := io.ReadFull(myReader, buf); err != nil {
		return 0, err
	}
	w.pos += 4
	if w.bigEndian {
		return int32(binary.BigEndian.Uint32(buf)), nil
	}
	return int32(binary.LittleEndian.Uint32(buf)), nil
}

// decodeIEEEExtended decodes a 10-byte IEEE 754 extended double-precision float.
// This is the format used for sample rates in AIFF files.
func decodeIEEEExtended(b []byte) (float64, error) {
	if len(b) != 10 {
		return 0, fmt.Errorf("expected 10 bytes for IEEE extended float, got %d", len(b))
	}

	// The first 2 bytes are for the sign and exponent
	exponent := uint16(b[0])<<8 | uint16(b[1])
	sign := 1.0
	// Check the most significant bit for the sign (bit 15)
	if (exponent & 0x8000) != 0 {
		sign = -1.0
	}
	// Clear the sign bit
	exponent = exponent & 0x7FFF

	// The remaining 8 bytes are the mantissa (fractional part)
	mantissa := uint64(b[2])<<56 | uint64(b[3])<<48 | uint64(b[4])<<40 | uint64(b[5])<<32 |
		uint64(b[6])<<24 | uint64(b[7])<<16 | uint64(b[8])<<8 | uint64(b[9])

	// Special cases:
	if exponent == 0 && mantissa == 0 {
		return 0, nil // Zero
	}
	if exponent == 0x7FFF {
		// Infinity or NaN
		return 0, fmt.Errorf("unsupported: infinity or NaN IEEE extended float")
	}

	// Calculate the float value
	// The exponent is biased by 16383.
	// The mantissa is scaled by 2^63
	return sign * float64(mantissa) / math.Pow(2, 63) * math.Pow(2, float64(exponent-16383)), nil
}

// Read Wav File Header
func (fd *WavReader) DecodeWavHeader() error {
	functionName := "DecodeWavHeader"
	var w WaveFile
	var err error
	w.ok = false
	w.pos = 0
	w.dataOffset = 0
	// New counter

	// Read the first 12 bytes (RIFF header + size + WAVE)
	header := make([]byte, 12)
	if _, err := io.ReadFull(fd.Input, header); err != nil {
		return err
	}
	w.riff = string(header[:4])

	w.wave = string(header[8:12])
	// Parse file length with correct endianness
	if w.wave == "AIFF" {
		w.bigEndian = true
		w.length = binary.BigEndian.Uint32(header[4:8]) // AIFF uses big-endian
	} else {
		w.length = binary.LittleEndian.Uint32(header[4:8]) // WAV uses little-endian
	}

	w.pos = 12 // Advance position

	if w.riff != "RIFF" && w.riff != "FORM" {
		if len(w.riff) == 0 {
			return errors.New(packageName + functionName + ": file could not be read: no data")
		}
		x := ""
		for j := 0; j < len(w.riff); j++ {
			x += fmt.Sprintf("%X ", w.riff[j])
		}
		return fmt.Errorf(packageName+functionName+": file is not WAV: no 'RIFF' tag found, instead '%s' ", x)
	}

	if w.wave != "WAVE" && w.wave != "AIFF" {
		return fmt.Errorf(packageName+functionName+": file is not WAV: no 'WAVE' tag found, instead %s", w.wave)
	}

	// Read Format
	w.format, err = w.readCharsFromInput(fd.Input, 4)

	if err != nil {
		return err
	}

	for w.format != "fmt " && w.format != "COMM" {
		chunkSize, err := w.readInt32FromInput(fd.Input)
		if err != nil {
			return err
		}
		//if w.bigEndian {
		//	chunkSizeBytes := make([]byte, 4)
		//	binary.BigEndian.PutUint32(chunkSizeBytes, uint32(chunkSize))
		//	chunkSize = int32(binary.BigEndian.Uint32(chunkSizeBytes))
		//	}

		if w.dataOffset+int64(chunkSize) > int64(w.length) {
			return errors.New(packageName + functionName + ": file could not be read: invalid 'fmt' chunk size")
		}

		_, err = w.readBytesFromInput(fd.Input, int(chunkSize))
		// Skip chunkSize bytes without buffering
		//_, err = io.CopyN(io.Discard, fd.Input, int64(chunkSize))

		//w.pos += int64(chunkSize) // Update position tracking if needed

		if err != nil {
			return err
		}
		w.format, err = w.readCharsFromInput(fd.Input, 4)
		if err != nil {
			return err
		}
	}

	// Continue with the 'fmt' chunk processing...
	if w.format == "fmt " {
		// WAV file-format chunk
		w.size, err = w.readUInt32FromInput(fd.Input)
		if err != nil {
			return err
		}
		if w.size < 16 {
			return errors.New(packageName + functionName + ": file could not be read: don't know how to read 'fmt' size " + fmt.Sprint(w.size))
		}

		// lookup audio format key
		audioFormatKey, err := w.readUInt16FromInput(fd.Input)
		if err != nil {
			return err
		}
		w.audioFormat = mapUint16ToWaveFormat(audioFormatKey)

		if w.audioFormat == PCM || w.audioFormat == ADPCM || w.audioFormat == IEEE_FLOAT ||
			w.audioFormat == INTERNAL_DOUBLE || w.audioFormat == EXTENSIBLE {
			w.numChannels, err = w.readUInt16FromInput(fd.Input)
			if err != nil {
				return err
			}
			w.sampleRate, err = w.readUInt32FromInput(fd.Input)
			if err != nil {
				return err
			}
			w.byteRate, err = w.readUInt32FromInput(fd.Input)
			if err != nil {
				return err
			}
			w.blockAlign, err = w.readUInt16FromInput(fd.Input)
			if err != nil {
				return err
			}
			w.bitsPerSample, err = w.readUInt16FromInput(fd.Input)
			if err != nil {
				return err
			}
			if w.size > 16 {
				skip := uint32(16)
				if w.audioFormat == EXTENSIBLE {
					_, err = w.readUInt16FromInput(fd.Input)

					if err != nil {
						return err
					}
					_, err = w.readUInt16FromInput(fd.Input)

					if err != nil {
						return err
					}
					w.channelMask, err = w.readUInt32FromInput(fd.Input)
					if err != nil {
						return err
					}

					guidBytes, err := w.readBytesFromInput(fd.Input, 16)
					if err != nil {
						return err
					}
					w.formatEx = NewWaveFormatExBytes(guidBytes)
					//		w.dataOffset += 16
					skip = 40

					if w.formatEx == waveFormatExPCM {
						w.audioFormat = PCM

					} else if w.formatEx == waveFormatExIEEEFloat {
						w.audioFormat = IEEE_FLOAT
					}
					if w.formatEx == AMBISONIC_B_FORMAT_PCMPCMguid || w.formatEx == AMBISONIC_B_FORMAT_IEEE_FLOATPCMguid {
						w.bigEndian = true // Ambisonic B-format uses big-endian PCM
					}
				}
				// Read and discard the rest of the 'fmt' structure
				_, err := w.readBytesFromInput(fd.Input, int(w.size-skip))

				if err != nil {
					return err
				}
			}
		} else {
			return errors.New(packageName + functionName + ": file could not be read: don't know how to read audio format " + fmt.Sprint(w.audioFormat))
		}

	} else if w.format == "COMM" {
		// Continue with the 'COMM' chunk processing...
		// AIFF file-format chunk
		w.size, err = w.readUInt32FromInput(fd.Input)
		if err != nil {
			return err
		}
		if w.size < 18 {
			return errors.New(packageName + functionName + ": file could not be read: don't know how to read 'COMM' size " + fmt.Sprint(w.size))
		}

		w.audioFormat = PCM
		w.numChannels, err = w.readUInt16FromInput(fd.Input)
		if err != nil {
			return err
		}
		//numFrames
		_, err := w.readUInt32FromInput(fd.Input)
		if err != nil {
			return err
		}
		w.bitsPerSample, err = w.readUInt16FromInput(fd.Input)
		if err != nil {
			return err
		}

		// SampleRate is 10-byte IEEE_extended format.
		ext, err := w.readBytesFromInput(fd.Input, 10)
		if err != nil {
			return err
		}

		sampleRateFloat, err := decodeIEEEExtended(ext) // You'll need to implement decodeIEEEExtended
		if err != nil {
			return errors.New(packageName + functionName + ": file could not be read: failed to decode IEEE extended sample rate: " + err.Error())
		}
		w.sampleRate = uint32(sampleRateFloat) // Convert float to uint32 for your struct

		w.blockAlign = uint16((w.numChannels * w.bitsPerSample) / 8)
		w.byteRate = uint32(w.blockAlign) * w.sampleRate

		if w.size > 18 {
			// Read and discard the rest of the 'COMM' structure
			_, err := w.readBytesFromInput(fd.Input, int(w.size-18))

			if err != nil {
				return err
			}
		}

	} else {
		return fmt.Errorf(packageName+functionName+": file could not be read: no 'fmt' tag found, instead %s", w.format)
	}

	// Read Data
	w.data, err = w.readCharsFromInput(fd.Input, 4)
	if err != nil {
		return err
	}
	// Seek for data chunk
	for w.data != "data" && w.data != "SSND" {
		miscSize, err := w.readInt32FromInput(fd.Input)
		if err != nil {
			return err
		}
		if w.pos+int64(miscSize) > int64(w.length) {
			return errors.New(packageName + functionName + ": file could not be read: invalid data chunk size")
		}
		//if w.dataOffset+int64(miscSize) > int64(w.length) {
		//	return errors.New(packageName + functionName + ": file could not be read: invalid data chunk size")
		//}
		//if w.bigEndian {
		//	miscSizeBytes := make([]byte, 4)
		//	binary.BigEndian.PutUint32(miscSizeBytes, uint32(miscSize))
		//	miscSize = int32(binary.BigEndian.Uint32(miscSizeBytes))
		//}

		// Skip chunkSize bytes without buffering
		_, err = io.CopyN(io.Discard, fd.Input, int64(miscSize))
		w.pos += int64(miscSize) // Update position tracking if needed
		//_, err = w.readBytesFromInput(fd.Input, int(miscSize))

		if err != nil {
			return err
		}
		w.data, err = w.readCharsFromInput(fd.Input, 4)
		if err != nil {
			return err
		}
		//	w.dataOffset += 4
	}
	if w.data == "data" {
		w.dataSize, err = w.readUInt32FromInput(fd.Input)
		if err != nil {
			return err
		}
	} else if w.data == "SSND" {
		// AIFF: Read SSND chunk size (big-endian)
		w.dataSize, err = w.readUInt32FromInput(fd.Input)
		if err != nil {
			return err
		}

		// Read offset (4 bytes) and blockSize (4 bytes)
		offsetBytes, err := w.readBytesFromInput(fd.Input, 4)
		if err != nil {
			return err
		}
		offset := binary.BigEndian.Uint32(offsetBytes)

		blockSizeBytes, err := w.readBytesFromInput(fd.Input, 4)
		if err != nil {
			return err
		}
		_ = binary.BigEndian.Uint32(blockSizeBytes) // Often 0

		// Skip the offset bytes (critical!)
		_, err = w.readBytesFromInput(fd.Input, int(offset))
		if err != nil {
			return err
		}

		// Adjust dataSize to exclude header (8 bytes) + offset
		w.dataSize = w.dataSize - 8 - offset

		// For AIFF, read until EOF/dataSize

	}
	// Convert dataSize to host order if BigEndian
	//if w.bigEndian {
	//	dataSizeBytes := make([]byte, 4)
	//	binary.BigEndian.PutUint32(dataSizeBytes, w.dataSize)
	//	w.dataSize = binary.BigEndian.Uint32(dataSizeBytes)
	//}

	// See if we can read this
	if w.numChannels > 0 {
		switch w.audioFormat {
		case PCM, EXTENSIBLE:
			switch w.bitsPerSample {
			case 8, 16, 24, 32:
				w.ok = true
			}
		case IEEE_FLOAT:
			switch w.bitsPerSample {
			case 32, 64:
				w.ok = true
			}
		case INTERNAL_DOUBLE:
			switch w.bitsPerSample {
			case 64:
				w.ok = true
			}
		}
	}
	// If dataSize is 0, it means the file size is unknown so let's default to the max
	if w.dataSize == 0 {
		w.dataSize = maxDataSize
	}
	if w.ok {
		w.max = uint32(float64(w.dataSize) / (float64(w.bitsPerSample) / 8) / float64(w.numChannels))
		//w.max = uint32((uint32(w.dataSize) / (uint32(w.bitsPerSample / 8))) / uint32(w.numChannels))
		if w.dataSize == maxDataSize {
			fmt.Println(packageName + functionName + ": Wave file: unknown size from header")
			w.max = math.MaxUint32
		}
		if w.max == 0 {
			fmt.Println(packageName + functionName + ":Wave file: zero size from header")
			w.max = math.MaxUint32
			fd.IgnoreDataLength = true
		}
	}

	//Header has now been read...
	//let set common header values
	fd.NumChannels = int(w.numChannels)
	fd.BitDepth = int(w.bitsPerSample)
	fd.SampleRate = int(w.sampleRate)
	fd.Length = w.length
	if w.bigEndian {
		fd.LittleEndian = false
		fd.ByteOrder = binary.BigEndian
	} else {
		fd.LittleEndian = true
		fd.ByteOrder = binary.LittleEndian
	}
	fd.debug("WavReader DecodeWavHeader: Offset:" + fmt.Sprint(w.dataOffset) + " Pos:" + fmt.Sprint(w.pos))
	fd.ReaderCursor = int(w.pos)
	fd.AudioFormat = w.audioFormat
	//fd.Size = w.length
	if w.dataSize == maxDataSize { //|| w.dataSize == 0xFFFFFFFF {
		w.max = math.MaxUint32     // Treat as "unknown length"
		fd.IgnoreDataLength = true // Add this flag to your WavReader struct
	}
	fd.Size = w.dataSize
	switch fd.BitDepth {
	case 16:
		fd.wordLength = 2
	case 24:
		fd.wordLength = 3
	case 32:
		fd.wordLength = 4
	case 64:
		fd.wordLength = 8
	default:
		fd.wordLength = 2
	}
	fd.bytesPerSample = fd.wordLength * fd.NumChannels

	return nil

}

// Function to handle debug calls, allowing for different logging implementations
func (myDecoder *WavReader) debug(message string) {

	if myDecoder.DebugFunc != nil {
		myDecoder.DebugFunc(message)
	} else { // if no external debug function available just print the message
		println(message)
	}

}
