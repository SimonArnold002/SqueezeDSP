// Package: github.com/Foxenfurter/foxAudioLib/foxAudioDecoder/gipsyDSDReader
// filename state.go
//
// SqueezeDSP fork (THE GAPLESS ROUND, 2026-11-05): the decode's place in the stream, so the next file's decode goes on
// from it and a gapless join of DSD files is one decode of the files end to end (Simon: "We cant have gaps where
// filters are not applied"). A file that a next one goes on from stops at its last byte (EndHold), its filters keeping
// what they hold; its last frames, as many as the filters reach, come out at the next file's start. Only a whole file
// both ends: a decode from a start or to an end (a seek, a cue sheet's track) starts afresh, as before. A decode made
// otherwise (rates, channels, cutoff, filters) cannot go on from it (SetState refuses).
package gipsyDSDReader

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"math"
)

type readerState struct {
	DSDRate, OutRate, NumChannels int
	Cutoff                        float64
	Native                        bool   // the native round: a native decode goes on only from a native one
	Integrator                    string // with the same integrator
	H1, H2, Up, Down, Ki          int
	S1                            []stage1State
	S2                            []stage2State
	Hist                          [][]float64
	HB, J, J1, K, Base, Read      int64
}

type stage1State struct {
	Buf []byte
	Cnt int
}

type stage2State struct {
	X    []complex128
	Fill int
}

// Span: the frames the filters reach past the input's last byte: what a decode that nothing follows plays out after its
// last frame, and at most what a held decode brings into the next file's start.
func (r *Reader) Span() int64 {
	mid := float64(r.DSDRate) // stage 1's output rate: the DSD rate when native (every 0)
	if r.every > 0 {
		mid /= float64(8 * r.every)
	}
	sec := float64(len(r.h1))/float64(r.DSDRate) + float64(len(r.h2))/mid
	if r.up > 0 {
		sec += float64(2*r.ki+1) / mid
	}
	return int64(math.Ceil(sec*float64(r.OutRate))) + 2
}

// HoldMax: the most frames a decode that stops at its file's end holding has not sent: stage 2 hands its outputs back a
// whole FFT block at a time, so up to a block of them, and the filters' reach (Span).
func (r *Reader) HoldMax() int64 {
	mid := float64(r.DSDRate)
	if r.every > 0 {
		mid /= float64(8 * r.every)
	}
	step := r.newStage2For().step()
	return int64(math.Ceil(float64(step)/mid*float64(r.OutRate))) + r.Span() + 2
}

// nativeShape: the integrator a native decode runs, "" for a DXD one
func (r *Reader) nativeShape() string {
	if !r.Native {
		return ""
	}
	return r.integrator()
}

// State encodes the decode's place in the stream, once DecodeInput has decoded a whole file, from its start, and stopped
// at its end holding. The refusal names the reason: a seek's decode reaches the file's end holding too (live 0.1.72.42,
// 2026-11-05: the log said it had not).
func (r *Reader) State() ([]byte, error) {
	s := r.sess
	switch {
	case s != nil && s.whole:
	case r.StartTime != 0:
		return nil, fmt.Errorf("%s: the decode began at %v (a seek or a cue point), not the file's start", packageName, r.StartTime)
	case r.EndTime != 0:
		return nil, fmt.Errorf("%s: the decode ends at a cue point (%v), not the file's end", packageName, r.EndTime)
	default:
		return nil, fmt.Errorf("%s: the decode did not stop at the file's end holding", packageName)
	}
	st := readerState{DSDRate: r.DSDRate, OutRate: r.OutRate, NumChannels: r.NumChannels, Cutoff: r.Cutoff,
		Native: r.Native, Integrator: r.nativeShape(),
		H1: len(r.h1), H2: len(r.h2), Up: r.up, Down: r.down, Ki: r.ki,
		HB: s.hb, J: s.j, J1: s.j1, K: s.k, Base: s.base, Read: s.read}
	for _, a := range s.s1 {
		st.S1 = append(st.S1, stage1State{Buf: append([]byte(nil), a.buf...), Cnt: a.cnt})
	}
	for _, b := range s.s2 {
		st.S2 = append(st.S2, stage2State{X: append([]complex128(nil), b.x...), Fill: b.fill})
	}
	for _, h := range s.hist {
		st.Hist = append(st.Hist, append([]float64(nil), h...))
	}
	var b bytes.Buffer
	if err := gob.NewEncoder(&b).Encode(st); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// PlayOut (2026-11-20, a source-rate change at a gapless join; the processor's gapless.go): the decode that wrote the state
// played out on its own, for a next track that cannot go on from it (PCM, or DSD at another rate). The decode is made
// again from what the state records (its DSD rate, the rate out, channels, cutoff, native and integrator) on empty, an
// empty DSF of its DSD rate (as --tail's input), put where it left off, and run with nothing following: the frames it
// held, then its filters' reach played out with DSD silence, exactly what that decode would have sent had nothing
// followed it. gainDB is the gain the decode ran at (the state does not record it).
func PlayOut(data []byte, empty string, gainDB float64, frameSize int, debug func(string)) ([][]float64, error) {
	var st readerState
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&st); err != nil {
		return nil, fmt.Errorf("%s state: %w", packageName, err)
	}
	r := &Reader{Filename: empty, OutRate: st.OutRate, Cutoff: st.Cutoff, GainDB: gainDB, Native: st.Native,
		Integrator: st.Integrator, TargetFrameSize: frameSize, DebugFunc: debug}
	if err := r.Open(); err != nil {
		r.Close()
		return nil, err
	}
	defer r.Close()
	if r.DSDRate != st.DSDRate {
		return nil, fmt.Errorf("%s: the empty input is DSD %d, the state's %d", packageName, r.DSDRate, st.DSDRate)
	}
	if err := r.SetState(data); err != nil {
		return nil, err
	}
	r.EndHold = func() bool { return false }
	out := make(chan [][]float64, 4)
	done := make(chan error, 1)
	go func() { // DecodeInput leaves the channel open (its caller closes it)
		err := r.DecodeInput(out)
		close(out)
		done <- err
	}()
	res := make([][]float64, r.NumChannels)
	for blk := range out {
		for c := range res {
			if c < len(blk) {
				res[c] = append(res[c], blk[c]...)
			}
		}
	}
	if err := <-done; err != nil {
		return nil, err
	}
	return res, nil
}

// SetState, after Open: the decode goes on where State left the last file's, this file's first byte after its last;
// frames are numbered over the stream, so Frames is what this file sends. On any error the decode is as Open left it.
func (r *Reader) SetState(data []byte) error {
	if r.StartTime != 0 || r.EndTime != 0 {
		return fmt.Errorf("%s: a decode from a start or to an end (a seek, a cue sheet's track) starts afresh", packageName)
	}
	var st readerState
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&st); err != nil {
		return fmt.Errorf("%s state: %w", packageName, err)
	}
	if st.Native && r.Native && st.Integrator != r.nativeShape() {
		return fmt.Errorf("%s state: integrator %q; this decode's %q", packageName, st.Integrator, r.nativeShape())
	}
	if st.DSDRate != r.DSDRate || st.OutRate != r.OutRate || st.NumChannels != r.NumChannels || st.Cutoff != r.Cutoff ||
		st.H1 != len(r.h1) || st.H2 != len(r.h2) || st.Up != r.up || st.Down != r.down || st.Ki != r.ki ||
		st.Native != r.Native || st.Integrator != r.nativeShape() {
		return fmt.Errorf("%s state: DSD %d to %d Hz x%d, cutoff %.0f, taps %d/%d; this decode DSD %d to %d x%d, cutoff %.0f, taps %d/%d",
			packageName, st.DSDRate, st.OutRate, st.NumChannels, st.Cutoff, st.H1, st.H2, r.DSDRate, r.OutRate, r.NumChannels, r.Cutoff, len(r.h1), len(r.h2))
	}
	ch := r.NumChannels
	if len(st.S1) != ch || len(st.S2) != (ch+1)/2 || len(st.Hist) != ch {
		return fmt.Errorf("%s state: %d channels' stages, not %d", packageName, len(st.S1), ch)
	}
	s := &session{s1: make([]*stage1, ch), s2: make([]*stage2, (ch+1)/2), hist: make([][]float64, ch),
		hb: st.HB, j: st.J, j1: st.J1, k: st.K, base: st.Base + st.Read}
	for c := range s.s1 {
		a := newStage1(r.h1, r.every)
		if len(st.S1[c].Buf) != len(a.buf) || st.S1[c].Cnt < 0 || st.S1[c].Cnt >= max(r.every, 1) {
			return fmt.Errorf("%s state: stage 1 of channel %d does not fit", packageName, c)
		}
		copy(a.buf, st.S1[c].Buf)
		a.cnt = st.S1[c].Cnt
		s.s1[c] = a
	}
	for p := range s.s2 {
		b := r.newStage2For()
		if b.hf != nil {
			if len(st.S2[p].X) != len(b.x) || st.S2[p].Fill < b.m-1 || st.S2[p].Fill >= b.n {
				return fmt.Errorf("%s state: stage 2 of pair %d does not fit", packageName, p)
			}
			copy(b.x, st.S2[p].X)
			b.fill = st.S2[p].Fill
		}
		s.s2[p] = b
	}
	for c := range s.hist {
		s.hist[c] = append([]float64(nil), st.Hist[c]...)
	}
	r.kStart = st.K
	r.kEnd = (s.base + r.src.bytes) * 8 * int64(r.OutRate) / int64(r.DSDRate)
	if r.kEnd < r.kStart {
		r.kEnd = r.kStart
	}
	r.Frames = r.kEnd - r.kStart
	r.resume = s
	return nil
}

// ScaleHeld (2026-11-20, a gain change at a gapless join): what the decode took from the last file (SetState) plays at
// f[c] times, channel by channel: the last track's gain over this one's, since the engine's gain comes after the decode
// (Simon: "a playlist needs to carry track gain"). Stages 2 and 3 hold samples, scaled where they are (stage 2 packs a
// pair of channels as one complex signal, the first real); stage 1 holds the last file's bytes, which cannot be scaled,
// so the outputs whose window reaches back over them split their sum there (stage1.runOld). Linear, so exact. A missing
// or 1 factor leaves the channel as it is; no state taken, nothing.
func (r *Reader) ScaleHeld(f []float64) {
	s := r.resume
	if s == nil {
		return
	}
	fc := func(c int) float64 {
		if c < len(f) {
			return f[c]
		}
		return 1
	}
	for c, a := range s.s1 {
		if a != nil && a.every > 0 && fc(c) != 1 {
			a.old, a.f = len(a.buf), fc(c)
		}
	}
	for p, b := range s.s2 {
		fl, fr := fc(2*p), fc(2*p+1)
		if b == nil || (fl == 1 && fr == 1) {
			continue
		}
		for i, v := range b.x {
			b.x[i] = complex(real(v)*fl, imag(v)*fr)
		}
	}
	for c, h := range s.hist {
		if g := fc(c); g != 1 {
			for i := range h {
				h[i] *= g
			}
		}
	}
}
