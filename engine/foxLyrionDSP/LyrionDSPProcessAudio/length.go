// SqueezeDSP fork (THE EXACT LENGTH ROUND, 2026-11-15; docs/exact-length-plan.md): a stream's header states the
// frames the stream carries, to the frame, where the input's own count is known at the start (a DSD file, a WAV
// input's header, --insamples). Until now the header stated an upper bound: LMS's duration with a margin, or the
// input's count plus room for two tails.
//
// A stream is a stretch of ONE continuous stream, the album's: the engine keeps its place in it across a gapless join
// (the residual: the input frames so far and the output frame the last stream was cut at), so the cut points come from
// the continuous stream's positions, round(input frames so far x ratio), and a rational ratio never drifts.
//
// A held stream (a track follows) cannot end AT that point: its stages hold frames back that only the next track's
// input brings out (the upsampler its delay, a DSD decode its reach; measured 2026-11-15, 1.6 to 27 ms). So a held
// stream is cut holdBack frames before it, a bound on what the stages hold, known at the start; what the chain yields
// past the cut stays in the residual (pending) and opens the next track's stream. A stream nothing follows runs to the
// point plus the stages' tails (gaplessState.tailOut), to the frame: zeros after the chain's last, nothing past it.
//
// The header states the play-out count, the longer of the two; a held stream ends before it, by holdBack + tail
// exactly (LMS's players stop at the stream's end). lengthStage is the last stage before the encoder; it also runs
// when the input's count is not known (the header then as before), so the next stream's place is right either way.
//
// Step 4, the exact-length contract (a player that needs the true length before the first byte; the note's exact): the
// end is decided at the start (gapless.go endHold), so the header states the stream's own count, held or played out,
// and the stream carries exactly that (fixed): what the chain yields past it is the next stream's (held) or dropped
// (played out), and a chain that falls short of it (an input that carried less than it stated) is filled with silence.
// The tail a held stream keeps (holdBack + tail, to the frame) plays out with --tail when no next track takes it.
package LyrionDSPProcessAudio

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
)

type lengthState struct {
	on       bool        // note mode: the gate runs
	exact    bool        // the input's count is known at the start: the header states the stream's frames
	prevIn   int64       // the continuous stream's input frames before this track (the decoder's rate)
	prevCut  int64       // the output frame this stream starts at (ProcRate)
	pending  [][]float64 // the last stream's frames past its cut: this stream's first
	statedIn int64       // this track's input frames, as known at the start
	holdBack int64       // a held stream is cut this many frames before its input's end
	tail     int64       // a stream nothing follows runs this many frames past it
	stated   int64       // the frames the header states: the stream when nothing follows (exact contract: the stream)
	fixed    bool        // the exact contract with the count known: the stream is exactly stated, cut at the start
	contract string      // "lms" or "exact" (the report's)
	in       atomic.Int64
	// at the end
	cumIn, cut, sent int64
	held             bool
	tailFrames       int64 // a held stream: what --tail plays out if no next track takes it
	keep             [][]float64
}

// lengthPos: the continuous stream's output frame at this many input frames
func (ap *AudioProcessor) lengthPos(in int64) int64 {
	if ap.ProcRate == ap.Decoder.SampleRate {
		return in
	}
	return int64(math.Round(float64(in) * float64(ap.ProcRate) / float64(ap.Decoder.SampleRate)))
}

// lengthStart (Initialize, after gaplessTail, before the encoder is made): this stream's place, and its count.
func (ap *AudioProcessor) lengthStart() {
	g := &ap.length
	if !ap.noteMode() {
		return
	}
	g.on = true
	g.contract = "lms"
	exactContract := ap.gapless.note.exact
	if exactContract {
		g.contract = "exact"
		ap.endHold() // decided now, from the note as it is at the start
	}
	if res := ap.gapless.res; ap.gapless.carry && res != nil && res.Gate {
		g.prevIn, g.prevCut = res.GateIn, res.GateCut
		if len(res.Pending) == ap.Decoder.NumChannels {
			g.pending = res.Pending
		}
	}
	ratio := float64(ap.ProcRate) / float64(ap.Decoder.SampleRate)
	if u := ap.Upsampler; u != nil {
		// what a held upsampler has not sent: its delay, and a step of its two stages
		g.holdBack += int64(math.Ceil(u.Delay)) + 2*int64(math.Ceil(float64(u.OutRate)/float64(u.InRate))) + 2
	}
	rd := ap.Decoder.DSDReader
	if rd != nil {
		reach := int64(math.Ceil(float64(rd.Span())*ratio)) + 1
		g.holdBack += int64(math.Ceil(float64(rd.HoldMax())*ratio)) + 1
		if ap.Config.Bypass {
			g.tail = reach // the decode plays its own reach out
		}
	}
	if !ap.Config.Bypass {
		g.tail = ap.gapless.tailOut
	}
	known := false
	switch {
	case ap.Args.Tail:
		known = true // a held tail on its own: no input frames
	case ap.Args.InSamples > 0:
		g.statedIn = ap.Args.InSamples
	case ap.Args.FlacSeekSet:
		g.statedIn = ap.flacSeekCount() // a remote FLAC's, from the stream URL (flacseek.go); 0: not read
	case rd != nil:
		g.statedIn = rd.FileFrames()
	case (ap.Args.InPath != "" || ap.Args.InHeader) && ap.Decoder.Size > 0 && ap.Decoder.Size < 0xFFFFFFF0 &&
		ap.Decoder.BitDepth > 0 && ap.Decoder.NumChannels > 0:
		// a WAV or AIFF the engine reads itself (its own --skip taken off), or a decoder's header the plugin vouches for
		// (--insamples=header); any other pipe's header is not taken as the count (faad states the whole file after a
		// seek, and an MP4 AAC's padded length: 144,384 for 143,360, measured 2026-11-15)
		g.statedIn = ap.Decoder.Size / int64(ap.Decoder.NumChannels*ap.Decoder.BitDepth/8)
	}
	if g.statedIn > 0 || known {
		g.exact = true
		end := ap.lengthPos(g.prevIn + g.statedIn)
		g.stated = end + g.tail - g.prevCut
		if exactContract {
			g.fixed = true
			if ap.endHold() { // cut holdBack before the input's end, as a held stream under LMS's contract ends
				g.stated = max(end-g.holdBack, g.prevCut) - g.prevCut
			}
		}
	}
	ap.Logger.Info(fmt.Sprintf("%s: stream length: %s; starts at frame %d of the continuous stream (%d input frames before it, %d frames from the last track), held back %d, tail %d, %s contract",
		packageName, map[bool]string{true: fmt.Sprintf("%d frames at %d Hz, exactly (%d in)", g.stated, ap.ProcRate, g.statedIn), false: "not known at the start"}[g.exact],
		g.prevCut, g.prevIn, pendingLen(g.pending), g.holdBack, g.tail, g.contract))
}

func pendingLen(p [][]float64) int {
	if len(p) == 0 {
		return 0
	}
	return len(p[0])
}

// countStage: the decoder's frames counted on their way (the gate's cut comes from what the input really carried)
func (ap *AudioProcessor) countStage(in chan [][]float64, buffer int, wg *sync.WaitGroup) chan [][]float64 {
	if !ap.length.on {
		return in
	}
	out := make(chan [][]float64, buffer)
	wg.Add(1)
	go func() {
		defer func() {
			close(out)
			wg.Done()
		}()
		for blk := range in {
			if len(blk) > 0 {
				ap.length.in.Add(int64(len(blk[0])))
			}
			out <- blk
		}
	}()
	return out
}

// lengthStage: the last stage before the encoder (see the file's head).
func (ap *AudioProcessor) lengthStage(in chan [][]float64, wg *sync.WaitGroup) chan [][]float64 {
	g := &ap.length
	if !g.on {
		return in
	}
	out := make(chan [][]float64, 1)
	wg.Add(1)
	go func() {
		defer func() {
			close(out)
			wg.Done()
		}()
		ch := ap.Decoder.NumChannels
		buf := make([][]float64, ch)
		pos := g.prevCut // the continuous stream's frame the next one sent is
		// the frames kept in hand until the end is known: the hold-back, and the frame or few by which the silence
		// that plays the tails out (whole input frames, tailStage) runs past the tail
		window := int(g.holdBack) + int(math.Ceil(float64(ap.ProcRate)/float64(ap.Decoder.SampleRate))) + 2
		excess := window // what may come past a played-out end without a word: tailStage's whole frames of silence
		cutAt := g.prevCut + g.stated
		send := func(n int) {
			if n <= 0 {
				return
			}
			blk := make([][]float64, ch)
			for c := range blk {
				blk[c] = buf[c][:n:n]
				buf[c] = buf[c][n:]
			}
			out <- blk
			pos += int64(n)
		}
		take := func(blk [][]float64) {
			if len(blk) != ch || len(blk[0]) == 0 {
				return
			}
			for c := range buf {
				buf[c] = append(buf[c], blk[c]...)
			}
			if g.fixed { // the cut is known: up to it at once, the rest kept
				send(int(min(int64(len(buf[0])), cutAt-pos)))
				return
			}
			send(len(buf[0]) - window)
		}
		take(g.pending)
		for blk := range in {
			take(blk)
		}
		// the input's end: every stage before this one is done, so the end is decided and the input counted
		in := g.in.Load()
		if rd := ap.Decoder.DSDReader; rd != nil {
			in = rd.FileFrames()
		}
		g.cumIn = g.prevIn + in
		end := ap.lengthPos(g.cumIn)
		have := pos + int64(len(buf[0]))
		if g.fixed {
			// the exact contract: the stream is the count its header stated at the start, whatever came
			if in != g.statedIn {
				ap.Logger.Warn(fmt.Sprintf("%s: stream length: the input carried %d frames, not the %d stated: the stream keeps its stated %d",
					packageName, in, g.statedIn, g.stated))
			}
			send(int(min(cutAt, have) - pos))
			if short := cutAt - pos; short > 0 {
				ap.Logger.Warn(fmt.Sprintf("%s: stream length: the chain fell %d frames short of the stated count: silence to it", packageName, short))
				for short > 0 {
					n := min(short, 4096)
					z := make([][]float64, ch)
					for c := range z {
						z[c] = make([]float64, n)
					}
					out <- z
					pos += n
					short -= n
				}
			}
			if ap.endHold() {
				g.keep = make([][]float64, ch)
				for c := range buf {
					g.keep[c] = append([]float64(nil), buf[c]...)
				}
				g.held = true
				g.tailFrames = max(end+g.tail-pos, 0)
			} else if left := len(buf[0]); left > excess {
				ap.Logger.Warn(fmt.Sprintf("%s: stream length: %d frames past the stated count dropped", packageName, left))
			}
		} else if ap.endHold() {
			cut := max(end-g.holdBack, g.prevCut)
			switch {
			case have < cut:
				ap.Logger.Warn(fmt.Sprintf("%s: stream length: the stages held %d frames more than the hold-back %d: the stream ends short of its cut by them",
					packageName, cut-have, g.holdBack))
				cut = have
			case pos > cut:
				ap.Logger.Warn(fmt.Sprintf("%s: stream length: %d frames past the cut were sent before the input ended", packageName, pos-cut))
				cut = pos
			}
			send(int(cut - pos))
			g.keep = make([][]float64, ch)
			for c := range buf {
				g.keep[c] = append([]float64(nil), buf[c]...)
			}
			g.held = true
			g.tailFrames = max(end+g.tail-cut, 0)
		} else {
			cut := end + g.tail
			if pos > cut {
				ap.Logger.Warn(fmt.Sprintf("%s: stream length: %d frames past the end were sent before the input ended", packageName, pos-cut))
				cut = pos
			}
			send(int(min(cut, have) - pos))
			for left := cut - pos; left > 0; {
				n := min(left, 4096)
				z := make([][]float64, ch)
				for c := range z {
					z[c] = make([]float64, n)
				}
				out <- z
				pos += n
				left -= n
			}
		}
		g.cut, g.sent = pos, pos-g.prevCut
		if g.exact && !g.held && g.sent != g.stated {
			// the input did not carry what it stated (a decoder's header): the stream ends where its audio does
			ap.Logger.Warn(fmt.Sprintf("%s: stream length: the input carried %d frames, not the %d stated: the stream has %d frames, its header %d",
				packageName, in, g.statedIn, g.sent, g.stated))
			if e := ap.Encoder; e != nil && e.DSDEncoder != nil {
				e.DSDEncoder.Bounded = true // no silence up to the stated count
			}
		}
		ap.Logger.Info(fmt.Sprintf("%s: stream length: %d frames sent, to frame %d of the continuous stream (%s)", packageName, g.sent, g.cut,
			map[bool]string{true: fmt.Sprintf("held: %d frames kept for the next track", pendingLen(g.keep)), false: "played out"}[g.held]))
		ap.lengthReport(true)
	}()
	return out
}

// lengthReport: the stream's count where a server can read it (length_<client> in the temp folder, whole and renamed
// into place): at the start what the header states, at the end what was sent.
func (ap *AudioProcessor) lengthReport(ended bool) {
	g := &ap.length
	if !g.on {
		return
	}
	r := map[string]any{"pid": os.Getpid(), "rate": ap.ProcRate, "exact": g.exact, "ended": ended, "contract": g.contract}
	if g.exact {
		r["frames"] = g.stated
		if g.fixed {
			r["hold"] = ap.endHold() // decided at the start: frames is this stream, held or not
		} else {
			r["heldFrames"] = max(g.stated-g.holdBack-g.tail, 0)
		}
	}
	if e := ap.Encoder; e != nil && e.DSDCount > 0 {
		r["dsdRate"], r["dsdSamples"] = e.DSDRate, e.DSDCount
	}
	if ended {
		r["sent"], r["held"] = g.sent, g.held
		if g.held {
			r["tailFrames"] = g.tailFrames
		}
	}
	b, err := json.Marshal(r)
	if err != nil {
		return
	}
	path := filepath.Join(ap.AppSettings.TempDataFolder, "length_"+ap.Args.CleanClientID)
	tmp := path + "." + strconv.Itoa(os.Getpid())
	if os.WriteFile(tmp, b, 0644) == nil {
		os.Rename(tmp, path)
	}
}
