// Package: github.com/Foxenfurter/foxAudioLib/foxAudioDecoder/foxDSDReader
// filename state.go
//
// SqueezeDSP fork (THE GAPLESS ROUND, 2026-11-05): the decode's place in the stream, so the next file's decode goes on
// from it and a gapless join of DSD files is one decode of the files end to end (Simon: "We cant have gaps where
// filters are not applied"). A file that a next one goes on from stops at its last byte (EndHold), its filters keeping
// what they hold; its last frames, as many as the filters reach, come out at the next file's start. Only a whole file
// both ends: a decode from a start or to an end (a seek, a cue sheet's track) starts afresh, as before. A decode made
// otherwise (rates, channels, cutoff, filters) cannot go on from it (SetState refuses).
package foxDSDReader

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
