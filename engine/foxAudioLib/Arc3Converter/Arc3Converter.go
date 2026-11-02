package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"sort"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: arc3a-extractor <input.arc3a> [output_directory]")
		return
	}

	inputPath := os.Args[1]
	outputDir := "."
	if len(os.Args) > 2 {
		outputDir = os.Args[2]
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		log.Fatalf("Error creating output directory: %v", err)
	}

	if err := extractARC3a(inputPath, outputDir); err != nil {
		log.Fatalf("Extraction failed: %v", err)
	}

	fmt.Printf("Successfully extracted files from %s to %s\n", inputPath, outputDir)
}

func extractARC3a(inputPath, outputDir string) error {
	data, err := ioutil.ReadFile(inputPath)
	if err != nil {
		return fmt.Errorf("error reading file: %w", err)
	}

	// Verify ARC3a signature
	if len(data) < 16 || !bytes.Equal(data[0:8], []byte{'I', 'K', 'M', 'P', 'A', 'K', 0x03, 0x00}) {
		return fmt.Errorf("invalid ARC3a header signature")
	}

	log.Printf("Scanning for WAV files...")

	// Scan for WAV files
	var wavOffsets []int
	offset := 0
	for offset < len(data)-12 {
		if bytes.Equal(data[offset:offset+4], []byte("RIFF")) {
			if offset+8 >= len(data) {
				break
			}
			chunkSize := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
			totalSize := chunkSize + 8

			if offset+totalSize > len(data) {
				log.Printf("Incomplete WAV at offset %d", offset)
				offset += 4
				continue
			}

			if offset+12 >= len(data) || !bytes.Equal(data[offset+8:offset+12], []byte("WAVE")) {
				log.Printf("Invalid WAVE header at offset %d", offset)
				offset += 4
				continue
			}

			wavOffsets = append(wavOffsets, offset)
			offset += totalSize
		} else {
			offset++
		}
	}

	totalFiles := len(wavOffsets)
	if totalFiles == 0 {
		return fmt.Errorf("no WAV files found")
	}

	log.Printf("Found %d WAV files", totalFiles)

	// Generate sorted filename list (alphanumeric order)
	filenames := generateSortedFilenames()
	if len(filenames) < totalFiles {
		log.Printf("Warning: Generated %d filenames but found %d WAVs", len(filenames), totalFiles)
		// Add sequential names for extra files
		for i := len(filenames); i < totalFiles; i++ {
			filenames = append(filenames, fmt.Sprintf("extra_%d.wav", i))
		}
	}

	// Create all necessary directories
	createDirectories(outputDir, filenames)

	// Extract and save each WAV with proper filename
	for i, offset := range wavOffsets {
		if i >= len(filenames) {
			break
		}

		chunkSize := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		totalSize := chunkSize + 8
		wavData := data[offset : offset+totalSize]

		outPath := filepath.Join(outputDir, filenames[i])
		if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
			log.Printf("Error creating directories for %s: %v", outPath, err)
			continue
		}

		if err := ioutil.WriteFile(outPath, wavData, 0644); err != nil {
			log.Printf("Error writing %s: %v", outPath, err)
		} else {
			log.Printf("Extracted %s", outPath)
		}
	}

	return nil
}

func generateSortedFilenames() []string {
	var filenames []string

	// First two files
	filenames = append(filenames, "ch0.wav", "ch1.wav")

	// Generate filenames for 21 steps (0-20)
	for step := 0; step <= 20; step++ {
		stepStr := fmt.Sprintf("step%02d", step) // Zero-padded step number

		// 4 sweeps (0-3) for 2 channels (0-1)
		for sweep := 0; sweep < 4; sweep++ {
			for channel := 0; channel < 2; channel++ {
				filenames = append(filenames,
					fmt.Sprintf("%s/sweep%dch%d.wav", stepStr, sweep, channel))
			}
		}

		// Tail files for 2 channels
		for channel := 0; channel < 2; channel++ {
			filenames = append(filenames,
				fmt.Sprintf("%s/tailch%d.wav", stepStr, channel))
		}
	}

	// Sort alphabetically (ch0, ch1, step00 files, step01 files, etc.)
	sort.Strings(filenames)
	return filenames
}

func createDirectories(baseDir string, filenames []string) {
	dirs := make(map[string]bool)

	for _, filename := range filenames {
		dir := filepath.Dir(filename)
		if dir != "." && !dirs[dir] {
			dirs[dir] = true
			fullPath := filepath.Join(baseDir, dir)
			if err := os.MkdirAll(fullPath, 0755); err != nil {
				log.Printf("Error creating directory %s: %v", fullPath, err)
			}
		}
	}
}
