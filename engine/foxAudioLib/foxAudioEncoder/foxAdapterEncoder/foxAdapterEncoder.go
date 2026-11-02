// package foxAdapterEncoder provides an EncoderInterface implementation that uses an external process (like FFmpeg or SoX) to encode audio.
// I was originally going to use FFmpeg, but SoX is more widely available on LMS and can handle FLAC/MP3 encoding without needing a separate installation, so I've switched to SoX for now. The code is designed to be flexible enough to support either, and the choice can be made via the AdapterType field.
package foxAdapterEncoder

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Foxenfurter/foxAudioLib/foxAudioEncoder/foxWavEncoder"
)

const packageName = "foxAdapterEncoder"

type EncoderAdapter struct {
	SampleRate          int
	NumChannels         int
	BitDepth            int
	TargetFormat        string
	AdapterPath         string // path to the converter binary (SoX, ffmpeg, or LAME)
	AdapterType         string // "sox", "ffmpeg", or "lame"
	ExtraArgs           []string
	Peak                float64
	FormatType          foxWavEncoder.FormatType
	Output              io.Writer
	PlayerMaxSampleRate int

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser

	mu       sync.Mutex
	started  bool
	finished bool

	drainErr  error
	drainDone chan struct{}
	jobHandle uintptr
	pcmBuf    []byte

	DebugFunc func(string)
}

func (e *EncoderAdapter) SetFormatType(ft foxWavEncoder.FormatType) { e.FormatType = ft }
func (e *EncoderAdapter) GetPeak() float64                          { return e.Peak }
func (e *EncoderAdapter) EncodeHeader() ([]byte, error)             { return nil, nil }
func (e *EncoderAdapter) assignJobObject() error                    { return nil }

// StartEncoder launches the subprocess for the configured adapter type.
func (e *EncoderAdapter) StartEncoder() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.started {
		return nil
	}
	if e.Output == nil {
		return errors.New("foxAdapterEncoder: Output writer not set")
	}

	var binaryPath string
	var args []string

	switch e.AdapterType {
	case "ffmpeg":
		if e.AdapterPath == "" {
			return errors.New("foxAdapterEncoder: AdapterPath not set for ffmpeg")
		}
		binaryPath = e.AdapterPath
		args = e.buildFfmpegArgs()

	case "sox":
		if e.AdapterPath == "" {
			return errors.New("foxAdapterEncoder: AdapterPath not set for sox")
		}
		binaryPath = e.AdapterPath
		args = e.buildSoxArgs()

	case "lame":
		if e.AdapterPath == "" {
			return errors.New("foxAdapterEncoder: AdapterPath not set for lame")
		}
		binaryPath = e.AdapterPath
		args = e.buildLameArgs()

	default:
		return fmt.Errorf("foxAdapterEncoder: unknown AdapterType %q", e.AdapterType)
	}

	if e.TargetFormat == "" {
		return errors.New("foxAdapterEncoder: TargetFormat not set")
	}

	e.debug("AdapterEncoder command: " + binaryPath + " " + strings.Join(args, " "))

	cmd := exec.Command(binaryPath, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		return err
	}
	if err := e.assignJobObject(); err != nil {
		e.debug("AdapterEncoder: job object failed (non-fatal): " + err.Error())
	}

	e.cmd = cmd
	e.stdin = stdin
	e.stdout = stdout
	e.drainDone = make(chan struct{})
	e.started = true

	go func() {
		defer close(e.drainDone)
		_, e.drainErr = io.Copy(e.Output, e.stdout)
		e.debug(fmt.Sprintf("AdapterEncoder drain: exited err=%v", e.drainErr))
		if e.cmd != nil && e.cmd.Process != nil {
			_ = e.cmd.Process.Kill()
		}
	}()

	e.debug(fmt.Sprintf("AdapterEncoder: StartEncoder complete, jobHandle=%d", e.jobHandle))
	return nil
}

func (e *EncoderAdapter) buildFfmpegArgs() []string {
	var extraArgs []string
	switch strings.ToUpper(e.TargetFormat) {
	case "FLC":
		bitDepth := e.BitDepth
		if bitDepth == 0 {
			bitDepth = 24
		}
		extraArgs = append(extraArgs, "-compression_level", "1")
		switch bitDepth {
		case 16:
			extraArgs = append(extraArgs, "-sample_fmt", "s16")
		case 24:
			extraArgs = append(extraArgs, "-sample_fmt", "s32", "-bits_per_raw_sample", "24")
		case 32:
			extraArgs = append(extraArgs, "-sample_fmt", "s32")
		default:
			extraArgs = append(extraArgs, "-sample_fmt", "s32", "-bits_per_raw_sample", "24")
		}
	case "MP3":
		extraArgs = []string{"-b:a", "320k"}
	case "AAC":
		extraArgs = []string{"-b:a", "256k"}
	}

	args := []string{
		"-loglevel", "error",
		"-f", "f64le",
		"-ar", strconv.Itoa(e.SampleRate),
		"-ac", strconv.Itoa(e.NumChannels),
		"-i", "pipe:0",
	}
	args = append(args, extraArgs...)
	args = append(args, "-f", e.TargetFormat, "-y", "pipe:1")
	return args
}

func (e *EncoderAdapter) buildSoxArgs() []string {
	outputRate := e.SampleRate
	var extraOutputArgs []string

	switch strings.ToUpper(e.TargetFormat) {
	case "FLAC":
		if outputRate > 384000 {
			outputRate = 384000
		}
		if e.PlayerMaxSampleRate > 0 && outputRate > e.PlayerMaxSampleRate {
			outputRate = e.PlayerMaxSampleRate
		}
		bitDepth := e.BitDepth
		if bitDepth == 0 {
			bitDepth = 24
		}
		extraOutputArgs = append(extraOutputArgs, "--compression", "2")
		switch bitDepth {
		case 16:
			extraOutputArgs = append(extraOutputArgs, "-b", "16")
		case 24:
			extraOutputArgs = append(extraOutputArgs, "-b", "24")
		case 32:
			extraOutputArgs = append(extraOutputArgs, "-b", "32")
		default:
			extraOutputArgs = append(extraOutputArgs, "-b", "24")
		}
	}

	args := []string{
		"-V1",
		"-t", "raw", "-e", "signed-integer", "-b", "32", "-L",
		"-r", strconv.Itoa(e.SampleRate),
		"-c", strconv.Itoa(e.NumChannels),
		"-",
		"-t", strings.ToLower(e.TargetFormat),
	}
	args = append(args, extraOutputArgs...)
	args = append(args, "-")

	if outputRate != e.SampleRate {
		args = append(args, "rate", "-v", "-s", "-a", strconv.Itoa(outputRate))
		e.SampleRate = outputRate
	}
	return args
}

// buildLameArgs builds a LAME command that reads raw signed 32-bit LE PCM
// from stdin, resamples if needed, and writes MP3 to stdout.
func (e *EncoderAdapter) buildLameArgs() []string {
	outputRate := e.SampleRate
	if e.PlayerMaxSampleRate > 0 && outputRate > e.PlayerMaxSampleRate {
		outputRate = e.PlayerMaxSampleRate
	}
	if outputRate > 48000 {
		outputRate = 48000
	}

	mode := "s" // stereo
	if e.NumChannels == 1 {
		mode = "m"
	}

	args := []string{
		"-r",                                                              // raw PCM input
		"--signed",                                                        // signed samples
		"--little-endian",                                                 // LE byte order
		"-s", strconv.FormatFloat(float64(e.SampleRate)/1000, 'f', 3, 64), // input rate in kHz
		"--bitwidth", "32", // 32-bit input samples
		"-m", mode,
		"--preset", "insane", // 320 kbps CBR
	}

	// Ask LAME to resample only when necessary.
	if outputRate != e.SampleRate {
		args = append(args, "--resample", strconv.FormatFloat(float64(outputRate)/1000, 'f', 3, 64))
		e.SampleRate = outputRate
	}

	args = append(args,
		"-", // stdin
		"-", // stdout
	)
	return args
}

// EncodeData writes PCM samples to the subprocess stdin.
func (e *EncoderAdapter) EncodeData(samples [][]float64) ([]byte, error) {
	e.mu.Lock()
	if e.finished {
		e.mu.Unlock()
		return nil, errors.New("encoder already finished")
	}
	if !e.started {
		e.mu.Unlock()
		return nil, errors.New("must call StartEncoder first")
	}
	e.mu.Unlock()

	raw, err := e.float64SamplesToInt32PCM(samples)
	if err != nil {
		return nil, err
	}
	if _, err := e.stdin.Write(raw); err != nil {
		return nil, fmt.Errorf("write to %s stdin: %w", e.AdapterType, err)
	}
	return nil, nil
}

// Finish closes stdin and waits for all encoded data to drain to e.Output.
func (e *EncoderAdapter) Finish() error {
	e.mu.Lock()
	if e.finished {
		e.mu.Unlock()
		return nil
	}
	if !e.started {
		e.mu.Unlock()
		return errors.New("encoder not started")
	}
	e.finished = true
	e.mu.Unlock()

	e.debug("Finish: closing stdin")
	e.stdin.Close()
	e.debug("Finish: waiting for drain")
	<-e.drainDone
	e.debug("Finish: drain complete")
	if e.cmd != nil {
		e.cmd.Wait()
	}
	if e.drainErr != nil {
		return fmt.Errorf("reading %s stdout: %w", e.AdapterType, e.drainErr)
	}
	return nil
}

// Kill forcibly terminates the subprocess.
func (e *EncoderAdapter) Kill() {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.debug(fmt.Sprintf("AdapterEncoder Kill: started=%v finished=%v", e.started, e.finished))

	if e.stdin != nil {
		err := e.stdin.Close()
		e.debug(fmt.Sprintf("AdapterEncoder Kill: stdin.Close() err=%v", err))
	}
	if e.cmd != nil && e.cmd.Process != nil {
		e.debug(fmt.Sprintf("AdapterEncoder Kill: killing PID %d", e.cmd.Process.Pid))
		err := e.cmd.Process.Kill()
		e.debug(fmt.Sprintf("AdapterEncoder Kill: Process.Kill() err=%v", err))
		done := make(chan struct{})
		go func() {
			e.cmd.Wait()
			close(done)
		}()
		select {
		case <-done:
			e.debug("AdapterEncoder Kill: process exited")
		case <-time.After(500 * time.Millisecond):
			e.debug("AdapterEncoder Kill: timed out waiting for exit")
		}
	}
}

func (e *EncoderAdapter) debug(msg string) {
	if e.DebugFunc != nil {
		e.DebugFunc(msg)
	}
}

// float64SamplesToInt32PCM converts [][]float64 to interleaved 32-bit signed
// integer little-endian PCM.
func (e *EncoderAdapter) float64SamplesToInt32PCM(samples [][]float64) ([]byte, error) {
	if len(samples) != e.NumChannels {
		return nil, fmt.Errorf("channel count mismatch: got %d, want %d", len(samples), e.NumChannels)
	}
	numSamples := len(samples[0])
	for _, ch := range samples {
		if len(ch) != numSamples {
			return nil, errors.New("inconsistent sample count across channels")
		}
	}
	needed := numSamples * e.NumChannels * 4
	if cap(e.pcmBuf) < needed {
		e.pcmBuf = make([]byte, needed)
	} else {
		e.pcmBuf = e.pcmBuf[:needed]
	}
	out := e.pcmBuf
	idx := 0
	peak := e.Peak
	for i := 0; i < numSamples; i++ {
		for ch := 0; ch < e.NumChannels; ch++ {
			val := samples[ch][i]
			abs := val
			if abs < 0 {
				abs = -abs
			}
			if abs > peak {
				peak = abs
			}
			if val > 1.0 {
				val = 1.0
			} else if val < -1.0 {
				val = -1.0
			}
			v := uint32(int32(val * 0x7fffffff))
			out[idx] = byte(v)
			out[idx+1] = byte(v >> 8)
			out[idx+2] = byte(v >> 16)
			out[idx+3] = byte(v >> 24)
			idx += 4
		}
	}
	e.Peak = peak
	return out, nil
}
