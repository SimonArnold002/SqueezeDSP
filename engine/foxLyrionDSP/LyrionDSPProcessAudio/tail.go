// SqueezeDSP fork (THE EXACT LENGTH ROUND, step 4; docs/exact-length-plan.md part B): a held tail played out on its own.
//
// Under the exact-length contract a stream's end is decided at its start, so a stream that held for a next track ends
// holdBack before its input's end, the rest in the residual: the frames the stages keep (the upsampler's delay, a DSD
// decode's reach) and their tails (the FIR, the crossfeed). When that next track goes before its stream starts (the
// queue edited), nothing takes them, and the renderer has already been told the held stream's length. --tail plays them
// out: the engine, run with the held stream's own arguments and --tail, opens an empty input of the held track's kind
// (the residual says which), takes every stage's state as the next track would, and plays out with nothing after it:
// exactly the frames a stream that nothing followed would have carried past the held one's cut (holdBack + tail), its
// header stating them. The held stream and its tail, one after the other, are the track played out.
package LyrionDSPProcessAudio

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Foxenfurter/foxAudioLib/foxAudioEncoder/gipsyDSDEncoder"
)

// tailInput (Initialize, before the decoder, with --tail): the held track's residual says what its input was; an empty
// one of that kind replaces the arguments' input. No held residual: an error, no stream.
func (ap *AudioProcessor) tailInput() error {
	path := filepath.Join(ap.AppSettings.TempDataFolder, "dsp_residuals_"+ap.Args.CleanClientID+".gob")
	res, err := ap.loadCacheFromFile(path)
	switch {
	case err != nil:
		return fmt.Errorf("--tail: no residual: %w", err)
	case res.Version != CACHE_VERSION:
		return fmt.Errorf("--tail: residual version %d, want %d", res.Version, CACHE_VERSION)
	case !res.Held || !res.Gate:
		return fmt.Errorf("--tail: the last track held nothing for a next one")
	case res.InKind == "" || res.NumChannels <= 0:
		return fmt.Errorf("--tail: the residual does not say what the last track's input was")
	}
	var data []byte
	name := "tail_input_" + ap.Args.CleanClientID
	switch res.InKind {
	case "dsd":
		e := &gipsyDSDEncoder.Encoder{DSDRate: res.InDSDRate, InRate: res.InDSDRate, NumChannels: res.NumChannels, HeldCount: true}
		if err := e.Open(); err != nil {
			return fmt.Errorf("--tail: %w", err)
		}
		if data, err = e.EncodeHeader(); err != nil {
			return fmt.Errorf("--tail: %w", err)
		}
		name += ".dsf"
		ap.Args.InputFormat = "DSD"
	default:
		data = emptyWAV(res.InRate, res.NumChannels)
		name += ".wav"
		ap.Args.InputFormat = "WAV"
	}
	in := filepath.Join(ap.AppSettings.TempDataFolder, name)
	if err := os.WriteFile(in, data, 0644); err != nil {
		return fmt.Errorf("--tail: %w", err)
	}
	// the held stream's input arguments go: no start, no end, no stated count, no read of a URL
	ap.Args.InPath, ap.Args.StartTime, ap.Args.EndTime = in, 0, 0
	ap.Args.InSamples, ap.Args.InHeader, ap.Args.WavLength, ap.Args.FlacSeekSet = 0, false, 0, false
	ap.flacSeek = nil
	ap.Logger.Info(fmt.Sprintf("%s: --tail: the last track's held tail played out on its own (its input %s at %d Hz x%d)",
		packageName, res.InKind, res.InRate, res.NumChannels))
	return nil
}

// emptyWAV: a 24-bit WAV header stating no data
func emptyWAV(rate, channels int) []byte {
	le := binary.LittleEndian
	var b bytes.Buffer
	b.WriteString("RIFF")
	binary.Write(&b, le, uint32(36))
	b.WriteString("WAVEfmt ")
	binary.Write(&b, le, uint32(16))
	binary.Write(&b, le, uint16(1))
	binary.Write(&b, le, uint16(channels))
	binary.Write(&b, le, uint32(rate))
	binary.Write(&b, le, uint32(rate*channels*3))
	binary.Write(&b, le, uint16(channels*3))
	binary.Write(&b, le, uint16(24))
	b.WriteString("data")
	binary.Write(&b, le, uint32(0))
	return b.Bytes()
}
