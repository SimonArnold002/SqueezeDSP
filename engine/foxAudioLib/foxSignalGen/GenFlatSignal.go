package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
)

const (
	sampleRate = 48000
	duration   = 1.0 // 1 second
	amp        = 0.8 // -2dB to avoid clipping
)

func main() {
	dirac := generateDiracImpulse()
	flatSignal := generateFlatSignal()

	err := writeWavFile("testimpulse.wav", dirac)
	if err != nil {
		fmt.Println("Error writing WAV file:", err)
		return
	}
	err = writeWavFile("testflat.wav", flatSignal)
	if err != nil {
		fmt.Println("Error writing WAV file:", err)
		return
	}
	fmt.Println("Generated 32-bit float WAV files successfully")
}

func generateDiracImpulse() []float32 {
	data := make([]float32, int(sampleRate*duration))
	for i := range data {
		data[i] = 0.0 // Zero amplitude throughout
	}
	data[0] = 1.0 * amp // Dirac impulse at sample 0
	return data
}

func generateFlatSignal() []float32 {
	data := make([]float32, int(sampleRate*duration))
	for i := range data {
		data[i] = amp // Constant amplitude throughout
	}
	return data
}

func writeWavFile(path string, data []float32) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	numSamples := len(data)
	const (
		headerSize = 44
		byteDepth  = 4 // 32-bit = 4 bytes
	)

	// Calculate sizes
	dataSize := numSamples * byteDepth
	fileSize := headerSize - 8 + dataSize // Total RIFF chunk size

	// Create header buffer
	header := new(bytes.Buffer)

	// RIFF chunk descriptor
	header.Write([]byte("RIFF"))
	binary.Write(header, binary.LittleEndian, uint32(fileSize))
	header.Write([]byte("WAVE"))

	// Format sub-chunk
	header.Write([]byte("fmt "))
	binary.Write(header, binary.LittleEndian, uint32(16))           // Subchunk size
	binary.Write(header, binary.LittleEndian, uint16(3))            // Audio format (3 = float)
	binary.Write(header, binary.LittleEndian, uint16(1))            // Channels
	binary.Write(header, binary.LittleEndian, uint32(sampleRate))   // Sample rate
	binary.Write(header, binary.LittleEndian, uint32(sampleRate*4)) // Byte rate (sampleRate * bytes/sample)
	binary.Write(header, binary.LittleEndian, uint16(4))            // Block align (channels * bytes/sample)
	binary.Write(header, binary.LittleEndian, uint16(32))           // Bits per sample
	header.Write([]byte("data"))                                    // Data header
	binary.Write(header, binary.LittleEndian, uint32(numSamples*4)) // Data size

	// Write header to file
	if _, err := file.Write(header.Bytes()); err != nil {
		return err
	}

	// Write audio data (float32 samples)
	for _, sample := range data {
		err := binary.Write(file, binary.LittleEndian, sample)
		if err != nil {
			return err
		}
	}

	return nil
}
