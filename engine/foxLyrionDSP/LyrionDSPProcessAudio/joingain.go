// SqueezeDSP fork (2026-11-20, A GAIN CHANGE AT A GAPLESS JOIN): the engine's gain (ReplayGain, the filter's headroom,
// the balance: mergeChannels) comes AFTER the decode, the upsampler, the delay and the convolvers, and each of those
// carries the last track's input across a gapless join (Fox's residual: the convolvers' state and the delay; ours: the
// upsampler, the DSD decode, a played-out end). So the last track's end (its filter decay, about the filter's length;
// what the front stage held, a few ms) came out of the next track's engine at the NEXT track's gain: at every join where
// the gain changes (a playlist with track gain, album to album, a track with no gain data). Simon: "a playlist needs to
// carry track gain".
//
// The next engine now scales what it took by the last track's channel gains over its own (the residual's Gains): every
// stage is linear, so the last track's end plays at the last track's gain and this track's audio at its own, exactly as
// each played alone and summed. Equal gains (an album) scale nothing: the stream is as before, byte for byte. What the
// stages hold after the gain (the frames already past the cut, DSF out's modulator, the crossfeed) is the last track's
// at its own gain already.
package LyrionDSPProcessAudio

import (
	"fmt"
	"math"
	"strings"
)

// joinGain (Initialize, once this track's channel gains are known, before any frame is processed): what this stream took
// of the last track's input, scaled to play at the last track's gain.
func (ap *AudioProcessor) joinGain() {
	g := &ap.gapless
	if !g.dspTaken && !g.upTaken && !g.rdTaken && len(g.lead) == 0 {
		return
	}
	f, ok := joinFactors(g.gains, ap.ChannelGains)
	if !ok {
		ap.Logger.Info(packageName + ": the last track's gains are not known (a residual from before them): what it held plays at this track's gain")
		return
	}
	if f == nil {
		return // the same gains: nothing to scale
	}
	if g.dspTaken {
		for i, c := range ap.Convolvers {
			if i < len(f) && f[i] != 1 {
				c.ScaleState(f[i])
			}
		}
		if ap.Delay != nil {
			for i, b := range ap.Delay.Buffers {
				if i < len(f) && f[i] != 1 {
					for j := range b {
						b[j] *= f[i]
					}
				}
			}
		}
	}
	if g.upTaken && ap.Upsampler != nil {
		ap.Upsampler.Scale(f)
	}
	if g.rdTaken && ap.Decoder.DSDReader != nil {
		ap.Decoder.DSDReader.ScaleHeld(f)
	}
	for i, c := range g.lead {
		if i < len(f) && f[i] != 1 {
			for j := range c {
				c[j] *= f[i]
			}
		}
	}
	db := make([]string, len(f))
	for i, v := range f {
		db[i] = fmt.Sprintf("%+.2f dB", 20*math.Log10(v))
	}
	ap.Logger.Info(fmt.Sprintf("%s: the last track's gain differs from this one's: what this stream took of it plays at its own gain (scaled %s a channel)",
		packageName, strings.Join(db, ", ")))
}

// joinFactors: each channel's last gain over its gain now; nil when every channel's is the same (nothing to scale), and
// false when the last gains are not known or do not fit. A channel at 0 now plays nothing of either: left as it is.
func joinFactors(last, now []float64) ([]float64, bool) {
	if len(last) == 0 || len(last) != len(now) {
		return nil, false
	}
	var f []float64
	for i := range now {
		if last[i] == now[i] || now[i] == 0 {
			continue
		}
		if f == nil {
			f = make([]float64, len(now))
			for j := range f {
				f[j] = 1
			}
		}
		f[i] = last[i] / now[i]
	}
	return f, true
}
