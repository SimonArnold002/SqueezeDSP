// Package: github.com/Foxenfurter/foxAudioLib/foxAudioEncoder/foxDSDEncoder
// filename state.go
//
// SqueezeDSP fork (THE GAPLESS ROUND, 2026-11-05): the encoder's place in the stream, so the next track's encoder goes on
// from it and a gapless join is one bitstream (Simon: "We cant have gaps where filters are not applied"). A track that
// goes on to the next closes held (CloseHeld): the interpolators keep their last frames (they come out at the next
// track's start, not against silence), the loops their state (an adaptive loop its look-ahead too), and the bits past
// the last whole group stay for the next track's first bytes. The group is 4 bytes a channel: squeezelite plays a DSF
// stream 4 bytes a channel at a time for native DSD (2 for DoP), drops what is left of the last group at a stream's end,
// and strips a last block's zero bytes when the stated count runs past the data (dsd.c _decode_dsf), so a held track
// ends on a whole group whose last byte is not zero in every channel. An encoder made otherwise (rates, channels,
// modulator, source) cannot go on from it (SetState refuses).
package foxDSDEncoder

import (
	"bytes"
	"encoding/gob"
	"fmt"
)

// group: the bytes a channel a held track ends on a whole number of
const group = 4

type encState struct {
	DSDRate, InRate, NumChannels int
	Kind, Source                 string
	Chans                        []chanState
}

type chanState struct {
	Interp []float64  // the interpolator's held frames
	W1, W2 [5]float64 // the loop's (standard or CLANS)
	Ad     *adState   // an adaptive loop's
	Carry  []byte     // whole bytes past the last group
	Cur    byte       // and the bits of the byte in hand
	NBit   uint
}

type adState struct {
	AW1, AW2, BW1, BW2 [4]float64
	Alpha              float64
	Left, Next         int64
	InB                bool
	Buf                []float64
	NL                 []int64
}

// CloseHeld ends a track that the next one goes on from: nothing flushed, no silence, the blocks completed and the last
// one's whole groups (zero after them, as Bounded pads), the rest held for State. Close ends a stream that nothing
// follows.
func (e *Encoder) CloseHeld() ([]byte, error) {
	// the bytes every channel has in its last block, then back to a whole group whose last byte is not zero in every
	// channel (a channel's blocks fill in step: every channel has the same count)
	n := e.chans[0].pos
	for n > 0 {
		n -= n % group
		if n == 0 {
			break
		}
		zero := true
		for _, ch := range e.chans {
			if ch.block[n-1] != 0 {
				zero = false
				break
			}
		}
		if !zero {
			break
		}
		n -= group
	}
	for _, ch := range e.chans {
		ch.carry = append(ch.carry[:0], ch.block[n:ch.pos]...)
		if n > 0 {
			for i := n; i < blockSize; i++ {
				ch.block[i] = 0
			}
			ch.full = append(ch.full, ch.block)
		}
		ch.block = make([]byte, blockSize)
		ch.pos = 0
	}
	out := e.collect()
	e.held = true
	e.debug(fmt.Sprintf("foxDSDEncoder: closed for the next track (%d bytes and %d bits a channel held), peak %.4f, %d samples limited, %d held after the interpolator, %d loop resets",
		len(e.chans[0].carry), e.chans[0].nbit, e.Peak, e.Limited, e.Overshoot, e.Resets))
	return out, nil
}

// State encodes the encoder's place in the stream; after CloseHeld only.
func (e *Encoder) State() ([]byte, error) {
	if !e.held {
		return nil, fmt.Errorf("foxDSDEncoder: State before CloseHeld")
	}
	s := encState{DSDRate: e.DSDRate, InRate: e.InRate, NumChannels: e.NumChannels, Kind: e.Kind, Source: e.Source}
	for _, ch := range e.chans {
		cs := chanState{Interp: append([]float64(nil), ch.it.buf...), Carry: append([]byte(nil), ch.carry...), Cur: ch.cur, NBit: ch.nbit}
		if ch.mod != nil {
			cs.W1, cs.W2 = ch.mod.w1, ch.mod.w2
		}
		if l := ch.ad; l != nil {
			cs.Ad = &adState{AW1: l.aw1, AW2: l.aw2, BW1: l.bw1, BW2: l.bw2, Alpha: l.alpha, Left: l.left, Next: l.next,
				InB: l.inB, Buf: append([]float64(nil), l.buf...), NL: append([]int64(nil), l.nl...)}
		}
		s.Chans = append(s.Chans, cs)
	}
	var b bytes.Buffer
	if err := gob.NewEncoder(&b).Encode(s); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// SetState, after Open: the encoder goes on where State left the last track's; on any error it is as Open left it.
func (e *Encoder) SetState(data []byte) error {
	var s encState
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&s); err != nil {
		return fmt.Errorf("foxDSDEncoder state: %w", err)
	}
	if s.DSDRate != e.DSDRate || s.InRate != e.InRate || s.NumChannels != e.NumChannels || s.Kind != e.Kind ||
		s.Source != e.Source || len(s.Chans) != len(e.chans) {
		return fmt.Errorf("foxDSDEncoder state: DSD %d from %d Hz x%d, %s for %s; this encoder DSD %d from %d x%d, %s for %s",
			s.DSDRate, s.InRate, s.NumChannels, s.Kind, s.Source, e.DSDRate, e.InRate, e.NumChannels, e.Kind, e.Source)
	}
	for k, ch := range e.chans {
		cs := s.Chans[k]
		if len(cs.Interp) < ch.it.K || cs.NBit > 7 || len(cs.Carry) >= blockSize || (cs.Ad == nil) != (ch.ad == nil) ||
			(cs.Ad != nil && len(cs.Ad.Buf) != len(cs.Ad.NL)) {
			return fmt.Errorf("foxDSDEncoder state: channel %d does not fit this encoder", k)
		}
	}
	for k, ch := range e.chans {
		cs := s.Chans[k]
		ch.it.buf = append(ch.it.buf[:0], cs.Interp...)
		if ch.mod != nil {
			ch.mod.w1, ch.mod.w2 = cs.W1, cs.W2
		}
		if l := ch.ad; l != nil {
			a := cs.Ad
			l.aw1, l.aw2, l.bw1, l.bw2, l.alpha, l.left, l.next, l.inB = a.AW1, a.AW2, a.BW1, a.BW2, a.Alpha, a.Left, a.Next, a.InB
			l.buf = append(l.buf[:0], a.Buf...)
			l.nl = append(l.nl[:0], a.NL...)
		}
		for _, b := range cs.Carry {
			ch.put(b)
		}
		ch.cur, ch.nbit = cs.Cur, cs.NBit
		// the carried bits count against this track's stated length as the track's own
		ch.done = int64(len(cs.Carry))*8 + int64(cs.NBit)
	}
	return nil
}
