// Package: github.com/Foxenfurter/foxAudioLib/foxAudioEncoder/gipsyDSDEncoder
// filename encoder.go
//
// SqueezeDSP fork: PCM to a native DSD stream (DSF), for players that take DSD. Each channel goes up to the
// DSD rate (interp.go) and through a 1-bit modulator (modulator.go, designs in tables.go), then into DSF
// (Sony DSF 1.01): 4096-byte blocks per channel, channels interleaved block by block, the oldest bit in
// each byte's LSB.
//
// Level: unity, the inverse of gipsyDSDReader's: PCM full scale is DSD's full swing, so a DSD file decoded at
// gain 0 and written back keeps its modulation. The designs go by the source (Source): a DSD source's
// (designs) are held at Limit, just above SACD's +3.1 dB peak (71%), and stay stable to at least 75%; a PCM
// source's (designsPCM), which comes in at SACD's 0 dB (-6.02 dB, peaks 0.5), are held at LimitPCM, 2 dB over
// that for inter-sample overs, and stay stable to at least 65% (ledger ORDER 7 SWEEP, Simon 2026-11-04: by
// source type, both kinds offered).
//
// DSF states its length before the data, so the caller gives Frames, the PCM frames the stream will carry:
// the header states Frames*L DSD samples, exactly that many are written (silence after a short input, the
// rest of a long one dropped), and the last block is padded with DSD silence (0x69). The spec says zero;
// a reader that honours the count never plays either, and one that does not plays silence, not a DC step.
package gipsyDSDEncoder

import (
	"encoding/binary"
	"fmt"
	"math"
	"sync"

	foxWavEncoder "github.com/Foxenfurter/foxAudioLib/foxAudioEncoder/foxWavEncoder"
)

const (
	DSD64 = 2822400
	// Limit: the largest modulation fed to a DSD source's loop; LimitPCM, a PCM source's.
	Limit     = 0.72
	LimitPCM  = 0.63
	blockSize = 4096
	silence   = 0x69
	subFrames = 1024 // PCM frames per pass through interpolator and loop (keeps the scratch in cache)
	pipeDepth = 3    // sub-chunks a channel's interpolation may run ahead of the loop (process)
)

type Encoder struct {
	DSDRate     int   // 2822400 (DSD64), 5644800, 11289600 or 22579200
	InRate      int   // the PCM rate in; DSDRate must be a whole multiple of it (itself: a native decode, L 1)
	NumChannels int   //
	Frames      int64 // PCM frames the stream carries
	// Bounded: Frames is an upper bound, not the exact length (a PCM stream whose length was stated ahead with a
	// margin, the engine's --wavlength). Close then stops where the input ended instead of filling the stated count
	// with silence, and the last block's rest is zero. The header still states Frames: a reader plays a stream that
	// ends before its count to its end, and squeezelite strips a last block's zero tail when the count is longer than
	// the data left (dsd.c _decode_dsf, "Remove zero padding from last block in case of inaccurate sample count").
	Bounded bool
	// Count (the exact-length round): the DSD samples a channel the stream carries, when the caller knows them to the
	// sample (what the last track's encoder held for this one included); 0: Frames*L
	Count int64
	// HeldCount (the exact-length round, step 4: a player that needs the true length before the first byte): the stream
	// closes held at exactly Count samples, decided at its start; the bits the encoder makes past Count are the next
	// track's first (CloseHeld), so packing does not stop at Count. Count is then HeldBack's whole groups.
	HeldCount bool
	Kind      string  // "standard" (default), "clans" or "adaptive" (adaptive.go)
	Source    string  // "dsd" (default) or "pcm": which designs, and the limit
	GainDB    float64 // on the PCM before the loop
	DebugFunc func(string)

	Peak      float64 // the largest |sample| after the gain, before the limit (1 = full swing)
	Limited   int64   // samples held at the limit
	Overshoot int64   // DSD-rate samples of the interpolator's overshoot held at the limit (hold)
	Resets    int64   // loop runaways cleared (never expected below the limit)
	InSafe    int64   // adaptive: samples with any of the safe design in the blend

	L     int
	limit float64 // Limit or LimitPCM
	count int64   // DSD samples per channel stated
	gain  float64
	chans []*channel
	held  bool // closed for a next track to go on from (CloseHeld, state.go)
}

type channel struct {
	it      *interpolator
	mod     *modulator    // nil when ad runs
	ad      *adaptiveLoop // adaptive at DSD64
	in      []float64
	ring    [pipeDepth][]float64 // the interpolated sub-chunks in flight between interpolation and loop
	bits    []byte
	done    int64 // DSD samples packed
	cur     byte
	nbit    uint
	block   []byte
	pos     int
	full    [][]byte
	peak    float64
	limited int64
	over    int64  // the interpolator's overshoot held (hold)
	carry   []byte // CloseHeld: the bytes past the last whole group, the next track's first (state.go)
}

func (e *Encoder) debug(s string) {
	if e.DebugFunc != nil {
		e.DebugFunc(s)
	}
}

// Open checks the settings and sets up each channel.
func (e *Encoder) Open() error {
	switch e.DSDRate {
	case DSD64, 2 * DSD64, 4 * DSD64, 8 * DSD64:
	default:
		return fmt.Errorf("gipsyDSDEncoder: DSD rate %d is not DSD64, 128, 256 or 512", e.DSDRate)
	}
	// the DSD rate itself (L 1): a native DSD decode (gipsyDSDReader Native), the loop taking it as it comes
	if e.InRate <= 0 || e.DSDRate%e.InRate != 0 {
		return fmt.Errorf("gipsyDSDEncoder: the PCM rate %d does not go into DSD rate %d", e.InRate, e.DSDRate)
	}
	// the interpolator is flat to a quarter of its input rate: from 88.2k that is past 22 kHz (measured flat within
	// 0.001 dB); from 44.1k it would take 2.5 dB off 20 kHz
	if e.InRate < 88200 {
		return fmt.Errorf("gipsyDSDEncoder: the PCM rate %d is under 88.2 kHz: upsample it first", e.InRate)
	}
	if e.NumChannels < 1 || e.NumChannels > 6 {
		return fmt.Errorf("gipsyDSDEncoder: %d channels", e.NumChannels)
	}
	if e.Frames <= 0 && e.Count <= 0 && !(e.HeldCount && e.Count == 0) { // a held stream may carry nothing: its count
		return fmt.Errorf("gipsyDSDEncoder: DSF states its length first, and it is not known")
	}
	if e.Kind == "" {
		e.Kind = "standard"
	}
	family := 1
	if e.DSDRate == DSD64 {
		family = 0
	}
	kind := e.Kind
	if kind == "adaptive" {
		kind = "standard" // DSD128 and up; DSD64 runs adaptive.go
	}
	table, limit := designs, Limit
	switch e.Source {
	case "", "dsd":
		e.Source = "dsd"
	case "pcm":
		table, limit = designsPCM, LimitPCM
	default:
		return fmt.Errorf("gipsyDSDEncoder: no source %q (dsd or pcm)", e.Source)
	}
	d, ok := table[kind]
	if !ok {
		return fmt.Errorf("gipsyDSDEncoder: no modulator %q (standard, clans or adaptive)", e.Kind)
	}
	e.limit = limit
	e.L = e.DSDRate / e.InRate
	e.count = e.Frames * int64(e.L)
	if e.Count > 0 {
		e.count = e.Count
	}
	e.gain = math.Pow(10, e.GainDB/20)
	e.chans = make([]*channel, e.NumChannels)
	for c := range e.chans {
		ch := &channel{it: newInterpolator(e.L), block: make([]byte, blockSize)}
		if e.Kind == "adaptive" && family == 0 {
			ch.ad = newAdaptiveLoop(adaptiveDesigns[0], adaptiveDesigns[1], e.DSDRate)
		} else {
			ch.mod = newModulator(d[family])
		}
		e.chans[c] = ch
	}
	e.debug(fmt.Sprintf("gipsyDSDEncoder: %d Hz x%d -> DSD %d (x%d, %d taps per phase), %s for a %s source (limit %.2f), gain %.2f dB, %d samples per channel",
		e.InRate, e.NumChannels, e.DSDRate, e.L, 2*e.chans[0].it.K+1, e.Kind, e.Source, e.limit, e.GainDB, e.count))
	return nil
}

// HeldBack: the DSD samples a channel an encoder of these settings keeps back when it closes held, past what it has
// packed: the frames its interpolator has not sent (K, once K frames are in) and an adaptive loop's look-ahead. Fixed
// by the settings, so a stream's held count is known at its start: what the last track's encoder held for it, plus its
// frames x L, less this, down to a whole group (exact-length round, step 4).
func HeldBack(dsdRate, inRate int, kind string) int64 {
	if inRate <= 0 || dsdRate%inRate != 0 {
		return 0
	}
	L := dsdRate / inRate
	n := int64(newInterpolator(L).K) * int64(L)
	if kind == "adaptive" && dsdRate == DSD64 {
		n += newAdaptiveLoop(adaptiveDesigns[0], adaptiveDesigns[1], dsdRate).ahead
	}
	return n
}

// Group: the DSD samples a channel a stream ends on a whole number of (4 bytes a channel: squeezelite's native DSD)
const Group = group * 8

func channelType(n int) uint32 {
	switch n {
	case 1, 2, 3:
		return uint32(n)
	case 4:
		return 4 // quad
	case 5:
		return 6
	default:
		return 7 // 5.1
	}
}

// EncodeHeader: the DSD, fmt and data chunk headers, 92 bytes.
func (e *Encoder) EncodeHeader() ([]byte, error) {
	le := binary.LittleEndian
	blocks := ((e.count+7)/8 + blockSize - 1) / blockSize
	data := blocks * blockSize * int64(e.NumChannels)
	h := make([]byte, 0, 92)
	h = append(h, "DSD "...)
	h = le.AppendUint64(h, 28)
	h = le.AppendUint64(h, uint64(92+data))
	h = le.AppendUint64(h, 0) // no metadata chunk
	h = append(h, "fmt "...)
	h = le.AppendUint64(h, 52)
	h = le.AppendUint32(h, 1) // format version
	h = le.AppendUint32(h, 0) // DSD raw
	h = le.AppendUint32(h, channelType(e.NumChannels))
	h = le.AppendUint32(h, uint32(e.NumChannels))
	h = le.AppendUint32(h, uint32(e.DSDRate))
	h = le.AppendUint32(h, 1) // bits per sample 1: LSB first
	h = le.AppendUint64(h, uint64(e.count))
	h = le.AppendUint32(h, blockSize)
	h = le.AppendUint32(h, 0)
	h = append(h, "data"...)
	h = le.AppendUint64(h, uint64(12+data))
	return h, nil
}

// EncodeData takes PCM frames (one slice per channel) and returns the DSF blocks completed.
func (e *Encoder) EncodeData(samples [][]float64) ([]byte, error) {
	if len(samples) != e.NumChannels {
		return nil, fmt.Errorf("gipsyDSDEncoder: %d channels given, %d expected", len(samples), e.NumChannels)
	}
	e.groups(func(g []*channel, first int) { e.process(g, samples[first:first+len(g)], false) })
	return e.collect(), nil
}

// Close flushes the interpolators, fills the stated length with the loop's silence if the input ran short (not when
// Bounded: there the stream ends with the input), pads the last block and returns what is left.
func (e *Encoder) Close() ([]byte, error) {
	pad := byte(silence)
	if e.Bounded {
		pad = 0
	}
	e.groups(func(g []*channel, first int) {
		e.process(g, nil, true)
		e.drain(g)
		zero := make([]float64, subFrames*e.L)
		for !e.Bounded && g[0].done < e.count {
			n := int(min(int64(len(zero)), e.count-g[0].done))
			e.modulate(g, [][]float64{zero[:n], zero[:n]}[:len(g)])
			e.drain(g)
		}
		for _, ch := range g {
			if ch.nbit > 0 {
				ch.cur |= silence &^ (1<<ch.nbit - 1) // the rest of the byte: silence's own bits
				ch.block[ch.pos] = ch.cur
				ch.pos++
				ch.nbit, ch.cur = 0, 0
			}
			if ch.pos > 0 {
				for i := ch.pos; i < blockSize; i++ {
					ch.block[i] = pad
				}
				ch.full = append(ch.full, ch.block)
				ch.block = make([]byte, blockSize)
				ch.pos = 0
			}
		}
	})
	out := e.collect()
	e.debug(fmt.Sprintf("gipsyDSDEncoder: closed, peak %.4f, %d samples limited, %d held after the interpolator, %d loop resets, %d samples on the safe design",
		e.Peak, e.Limited, e.Overshoot, e.Resets, e.InSafe))
	return out, nil
}

// groups runs f on each pair of channels (and a last odd one alone) at the same time.
func (e *Encoder) groups(f func(g []*channel, first int)) {
	var wg sync.WaitGroup
	for c := 0; c < len(e.chans); c += 2 {
		g := e.chans[c:min(c+2, len(e.chans))]
		wg.Add(1)
		go func() {
			defer wg.Done()
			f(g, c)
		}()
	}
	wg.Wait()
	e.Limited, e.Overshoot, e.Resets, e.InSafe = 0, 0, 0, 0
	for _, ch := range e.chans {
		e.Peak = math.Max(e.Peak, ch.peak)
		e.Limited += ch.limited
		e.Overshoot += ch.over
		if ch.ad != nil {
			e.Resets += ch.ad.resets
			e.InSafe += ch.ad.samplesInB
		} else {
			e.Resets += ch.mod.resets
		}
	}
}

func grow(b []byte, n int) []byte {
	if cap(b) < n {
		return make([]byte, n)
	}
	return b[:n]
}

// process: gain, limit, up to the DSD rate, the loop, packing; a sub-chunk at a time. Each channel's interpolation
// (gain, limit, the interpolator, hold) runs on a goroutine of its own up to pipeDepth sub-chunks ahead of the loop and
// the packing, which take the group together (runPair): the same arithmetic per sample as one after the other, so the
// same bits, but the parts overlap. At DSD512 the encoder is the engine's slowest stage, and done in turn its parts
// set the speed (2026-11-04 bench, ledger THE ENCODER RAN ITS PARTS IN TURN).
func (e *Encoder) process(g []*channel, x [][]float64, flush bool) {
	n := 0
	if len(x) > 0 {
		n = len(x[0])
	}
	chunks := (n + subFrames - 1) / subFrames
	if flush {
		chunks++ // the interpolators' last frames, against silence after the end
	}
	if chunks == 0 {
		return
	}
	ready := make([]chan []float64, len(g))
	free := make([]chan []float64, len(g))
	for k, ch := range g {
		ready[k] = make(chan []float64, pipeDepth)
		free[k] = make(chan []float64, pipeDepth)
		for _, b := range ch.ring {
			free[k] <- b
		}
		go func() {
			defer close(ready[k])
			for i := 0; i < chunks; i++ {
				buf := (<-free[k])[:0]
				if lo := i * subFrames; lo < n {
					buf = e.interpolate(ch, x[k][lo:min(lo+subFrames, n)], false, buf)
				} else {
					buf = e.interpolate(ch, nil, true, buf)
				}
				ready[k] <- buf
			}
		}()
	}
	ups := make([][]float64, len(g))
	for i := 0; i < chunks; i++ {
		for k := range g {
			ups[k] = <-ready[k]
		}
		e.modulate(g, ups)
		for k := range g {
			free[k] <- ups[k]
		}
	}
	for k, ch := range g {
		for j := range ch.ring {
			ch.ring[j] = <-free[k] // every buffer is back once the loop has had the last: kept for the next call
		}
	}
}

// interpolate: one sub-chunk of a channel (flush: the interpolator's last frames instead) through the gain, the
// limit, the interpolator and hold, appended to buf. Only the channel's interpolation goroutine runs it.
func (e *Encoder) interpolate(ch *channel, x []float64, flush bool, buf []float64) []float64 {
	// the largest |input| any output of this pass can see: the window reaches back over the interpolator's history
	reach := 0.0
	for _, v := range ch.it.buf {
		reach = math.Max(reach, math.Abs(v))
	}
	if flush {
		buf = ch.it.flush(buf)
		e.hold(ch, buf, reach)
		return buf
	}
	ch.in = ch.in[:0]
	for _, v := range x {
		v *= e.gain
		a := math.Abs(v)
		if a > ch.peak {
			ch.peak = a
		}
		if v > e.limit {
			v = e.limit
			ch.limited++
		} else if v < -e.limit {
			v = -e.limit
			ch.limited++
		}
		reach = math.Max(reach, math.Min(a, e.limit))
		ch.in = append(ch.in, v)
	}
	buf = ch.it.push(ch.in, buf)
	e.hold(ch, buf, reach)
	return buf
}

// hold: the interpolator's overshoot past the limit held at the limit too, so the loop never sees more than the limit,
// which every design is stable past (a DSD source's to 0.75, a PCM source's to 0.65). A sinc rings on a step: a jump to
// the limit, or a seek into a loud passage, overshoots it by up to about 9% for a few samples, and on the 2026-11-04
// bench order 7 standard at DSD512 ran away twice on each without this (ledger THE STEP OVERSHOOT).
// reach is the largest |input| this pass's outputs can see; times the interpolator's largest gain (bound: the sum of
// a phase's |taps|) it bounds every output, so a pass that cannot reach the limit is not scanned (music sits far
// under it). The margin covers the sums' rounding; skipping changes no output.
func (e *Encoder) hold(ch *channel, up []float64, reach float64) {
	lim := e.limit
	if reach*ch.it.bound*(1+1e-9) <= lim {
		return
	}
	for i, v := range up {
		if v > lim {
			up[i] = lim
			ch.over++
		} else if v < -lim {
			up[i] = -lim
			ch.over++
		}
	}
}

// drain: an adaptive loop holds its look-ahead back; at the end it is modulated too.
func (e *Encoder) drain(g []*channel) {
	for _, ch := range g {
		if ch.ad != nil {
			ch.bits = ch.ad.drain(ch.bits[:0])
			e.pack(ch, ch.bits)
		}
	}
}

// modulate: the loop (a pair through runPair) and the packing.
func (e *Encoder) modulate(g []*channel, ups [][]float64) {
	if g[0].ad != nil {
		for k, ch := range g {
			ch.bits = ch.ad.run(ups[k], ch.bits[:0])
			e.pack(ch, ch.bits)
		}
		return
	}
	for k, ch := range g {
		ch.bits = grow(ch.bits, len(ups[k]))
	}
	if len(g) == 2 {
		runPair(g[0].mod, g[1].mod, ups[0], ups[1], g[0].bits, g[1].bits)
	} else {
		g[0].mod.run(ups[0], g[0].bits)
	}
	for _, ch := range g {
		e.pack(ch, ch.bits)
	}
}

// pack: bits into bytes (oldest in the LSB) and blocks, to the stated count; a whole byte's eight at once.
func (e *Encoder) pack(ch *channel, bits []byte) {
	if left := e.count - ch.done; !e.HeldCount && int64(len(bits)) > left {
		bits = bits[:left]
	}
	ch.done += int64(len(bits))
	for len(bits) > 0 {
		if ch.nbit == 0 && len(bits) >= 8 {
			b := bits[:8:8]
			ch.put(b[0] | b[1]<<1 | b[2]<<2 | b[3]<<3 | b[4]<<4 | b[5]<<5 | b[6]<<6 | b[7]<<7)
			bits = bits[8:]
			continue
		}
		ch.cur |= bits[0] << ch.nbit
		ch.nbit++
		bits = bits[1:]
		if ch.nbit == 8 {
			ch.put(ch.cur)
			ch.cur, ch.nbit = 0, 0
		}
	}
}

// put: one byte into the channel's block, a full block onto its list.
func (ch *channel) put(b byte) {
	ch.block[ch.pos] = b
	ch.pos++
	if ch.pos == blockSize {
		ch.full = append(ch.full, ch.block)
		ch.block = make([]byte, blockSize)
		ch.pos = 0
	}
}

// collect: the blocks every channel has completed, interleaved.
func (e *Encoder) collect() []byte {
	n := len(e.chans[0].full)
	for _, ch := range e.chans {
		n = min(n, len(ch.full))
	}
	out := make([]byte, 0, n*blockSize*len(e.chans))
	for k := 0; k < n; k++ {
		for _, ch := range e.chans {
			out = append(out, ch.full[k]...)
		}
	}
	for _, ch := range e.chans {
		ch.full = ch.full[n:]
	}
	return out
}

func (e *Encoder) GetPeak() float64 { return e.Peak }

// SetFormatType: DSF has one format.
func (e *Encoder) SetFormatType(foxWavEncoder.FormatType) {}
