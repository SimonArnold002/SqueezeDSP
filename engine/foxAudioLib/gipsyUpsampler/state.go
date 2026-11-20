// Package: github.com/Foxenfurter/foxAudioLib/gipsyUpsampler
// filename state.go
//
// SqueezeDSP fork (THE GAPLESS ROUND, 2026-11-05): the chain's place in the stream, so the next track's chain goes on from
// it and a gapless join is one continuous resample (Simon: "We cant have gaps where filters are not applied"). Each
// stage keeps the inputs its next outputs still need, the next output's phase and newest input; the chain keeps the
// frames taken and given since the stream's start (Flush ends the stream at round(in*out/inRate) from there) and what is
// left of the start's delay to drop. A track that goes on to the next ends without Flush; its last outputs, as many as
// the chain's delay, come out at the start of the next track. A chain designed otherwise (rates, channels, filter)
// cannot go on from it (SetState refuses).
package gipsyUpsampler

import (
	"bytes"
	"encoding/gob"
	"fmt"
)

type chainState struct {
	InRate, OutRate, Channels int
	Choice                    Choice
	TapsA, TapsB              int
	Stages                    [][]polyState // [stage][channel]
	In, Out                   int64
	Drop                      int
}

type polyState struct {
	Buf  []float64
	Base int64
	I    int64
	P    int
}

// State encodes the chain's place in the stream.
func (u *Upsampler) State() ([]byte, error) {
	s := chainState{InRate: u.InRate, OutRate: u.OutRate, Channels: u.Channels, Choice: u.Choice,
		TapsA: u.TapsA, TapsB: u.TapsB, In: u.in, Out: u.out, Drop: u.drop}
	for _, st := range u.stages {
		ps := make([]polyState, len(st))
		for ch, p := range st {
			ps[ch] = polyState{Buf: append([]float64(nil), p.buf...), Base: p.base, I: p.i, P: p.p}
		}
		s.Stages = append(s.Stages, ps)
	}
	var b bytes.Buffer
	if err := gob.NewEncoder(&b).Encode(s); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// Scale (2026-11-20, a gain change at a gapless join): multiplies what each stage holds of channel ch's input so far by
// f[ch] (SetState's, the last track's): the engine's gain comes after the chain, so the next track's engine scales the
// last track's held input by the last track's gain over its own. Linear, so exact. A missing or 1 factor leaves the
// channel as it is.
func (u *Upsampler) Scale(f []float64) {
	for _, st := range u.stages {
		for ch, p := range st {
			if ch >= len(f) || f[ch] == 1 {
				continue
			}
			for i := range p.buf {
				p.buf[i] *= f[ch]
			}
		}
	}
}

// FromState (2026-11-20, a source-rate change at a gapless join): the chain that wrote the state, designed again from
// what the state records (its rates, channels and the filter it ran, the one New chose), and put where it left off, so
// the next track's engine, whose own chain cannot go on from it, can play its last outputs out.
func FromState(data []byte) (*Upsampler, error) {
	var s chainState
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&s); err != nil {
		return nil, fmt.Errorf("upsample state: %w", err)
	}
	u, err := New(s.InRate, s.OutRate, s.Channels, s.Choice, s.Choice)
	if err != nil {
		return nil, err
	}
	if err := u.SetState(data); err != nil {
		return nil, err
	}
	return u, nil
}

// SetState puts a chain made the same way where State left the last one; on any error the chain is as New made it.
func (u *Upsampler) SetState(data []byte) error {
	var s chainState
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&s); err != nil {
		return fmt.Errorf("upsample state: %w", err)
	}
	if s.InRate != u.InRate || s.OutRate != u.OutRate || s.Channels != u.Channels || s.Choice != u.Choice ||
		s.TapsA != u.TapsA || s.TapsB != u.TapsB || len(s.Stages) != len(u.stages) {
		return fmt.Errorf("upsample state: from %d to %d Hz, %d channels, %s, taps %d/%d; this chain %d to %d, %d, %s, %d/%d",
			s.InRate, s.OutRate, s.Channels, s.Choice, s.TapsA, s.TapsB, u.InRate, u.OutRate, u.Channels, u.Choice, u.TapsA, u.TapsB)
	}
	for i, st := range u.stages {
		if len(s.Stages[i]) != len(st) {
			return fmt.Errorf("upsample state: stage %d has %d channels, not %d", i, len(s.Stages[i]), len(st))
		}
		for ch, p := range st {
			ps := s.Stages[i][ch]
			// the next output's window starts at or after the first input held (push keeps it so), its phase one of L
			if ps.I-int64(p.K-1) < ps.Base || ps.P < 0 || ps.P >= p.L {
				return fmt.Errorf("upsample state: stage %d channel %d does not fit this chain", i, ch)
			}
		}
	}
	for i, st := range u.stages {
		for ch, p := range st {
			ps := s.Stages[i][ch]
			p.buf = append(p.buf[:0], ps.Buf...)
			p.base, p.i, p.p = ps.Base, ps.I, ps.P
		}
	}
	u.in, u.out, u.drop = s.In, s.Out, s.Drop
	return nil
}
