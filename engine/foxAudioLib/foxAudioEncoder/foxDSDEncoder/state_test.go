package foxDSDEncoder

import (
	"bytes"
	"math"
	"math/rand"
	"testing"
)

// played: a DSF stream's data (after its header) as squeezelite plays it (dsd.c _decode_dsf): each channel's bytes block
// by block, the last block's zero tail stripped (the count is an upper bound in Bounded mode), then whole groups of 4.
func played(t *testing.T, data []byte, channels int) [][]byte {
	t.Helper()
	if len(data)%(blockSize*channels) != 0 {
		t.Fatalf("data of %d bytes is not whole blocks", len(data))
	}
	ch := make([][]byte, channels)
	nb := len(data) / (blockSize * channels)
	for k := 0; k < nb; k++ {
		for c := 0; c < channels; c++ {
			o := (k*channels + c) * blockSize
			blk := data[o : o+blockSize]
			if k == nb-1 {
				n := blockSize
				for n > 1 {
					zero := true
					for cc := 0; cc < channels; cc++ {
						if data[(k*channels+cc)*blockSize+n-1] != 0 {
							zero = false
						}
					}
					if !zero {
						break
					}
					n--
				}
				blk = blk[:n]
			}
			ch[c] = append(ch[c], blk...)
		}
	}
	for c := range ch {
		ch[c] = ch[c][:len(ch[c])/4*4]
	}
	return ch
}

func encodeTrack(t *testing.T, kind string, rate, in int, x [][]float64, state []byte, last bool) ([][]byte, []byte) {
	t.Helper()
	e := &Encoder{DSDRate: rate, InRate: in, NumChannels: len(x), Frames: int64(len(x[0])) + int64(in), Bounded: true, Kind: kind, Source: "pcm"}
	if err := e.Open(); err != nil {
		t.Fatal(err)
	}
	if state != nil {
		if err := e.SetState(state); err != nil {
			t.Fatal(err)
		}
	}
	var data []byte
	for o := 0; o < len(x[0]); o += 3001 {
		blk := make([][]float64, len(x))
		for c := range x {
			blk[c] = x[c][o:min(o+3001, len(x[c]))]
		}
		b, err := e.EncodeData(blk)
		if err != nil {
			t.Fatal(err)
		}
		data = append(data, b...)
	}
	var tail []byte
	var err error
	if last {
		tail, err = e.Close()
	} else {
		tail, err = e.CloseHeld()
	}
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, tail...)
	var next []byte
	if !last {
		if next, err = e.State(); err != nil {
			t.Fatal(err)
		}
	}
	return played(t, data, len(x)), next
}

// TestStateJoinIsOneBitstream: a stream cut into three tracks, each through an encoder of its own going on from the
// last one's State (held closes, the last a Close), played as squeezelite plays them, is the whole stream's bits through
// one encoder, at DSD64 and DSD256 with every modulator; the signal loud enough that the adaptive loop changes design.
func TestStateJoinIsOneBitstream(t *testing.T) {
	for _, tc := range []struct {
		kind     string
		rate, in int
	}{{"standard", DSD64, 352800}, {"clans", DSD64, 352800}, {"adaptive", DSD64, 352800}, {"standard", 4 * DSD64, 705600}, {"clans", 4 * DSD64, 705600}} {
		rng := rand.New(rand.NewSource(int64(tc.rate)))
		n := tc.in/3 + 1234
		x := [][]float64{make([]float64, n), make([]float64, n)}
		for i := 0; i < n; i++ {
			s := float64(i) / float64(tc.in)
			env := 0.2 + 0.4*math.Max(0, math.Sin(2*math.Pi*3*s)) // swells past the adaptive threshold and back
			x[0][i] = env*math.Sin(2*math.Pi*1000*s) + 0.01*(rng.Float64()*2-1)
			x[1][i] = env*math.Sin(2*math.Pi*1700*s) + 0.01*(rng.Float64()*2-1)
		}
		whole, _ := encodeTrack(t, tc.kind, tc.rate, tc.in, x, nil, true)
		cuts := []int{0, n*3/10 + 7, n*7/10 + 3, n}
		joined := [][]byte{nil, nil}
		var state []byte
		for k := 0; k < 3; k++ {
			part := [][]float64{x[0][cuts[k]:cuts[k+1]], x[1][cuts[k]:cuts[k+1]]}
			got, next := encodeTrack(t, tc.kind, tc.rate, tc.in, part, state, k == 2)
			for c := range got {
				if k < 2 && len(got[c])%4 != 0 {
					t.Fatalf("%s DSD %d: track %d ends on %d bytes, not whole groups", tc.kind, tc.rate, k+1, len(got[c]))
				}
				joined[c] = append(joined[c], got[c]...)
			}
			state = next
		}
		for c := range whole {
			if !bytes.Equal(joined[c], whole[c]) {
				i := 0
				for i < min(len(joined[c]), len(whole[c])) && joined[c][i] == whole[c][i] {
					i++
				}
				t.Fatalf("%s DSD %d channel %d: joined %d bytes, whole %d, first difference at byte %d (cuts at %d, %d frames)",
					tc.kind, tc.rate, c, len(joined[c]), len(whole[c]), i, cuts[1], cuts[2])
			}
		}
	}
}

// TestStateRefusesAnotherEncoder: a state goes only to an encoder made the same way, and only after a held close.
func TestStateRefusesAnotherEncoder(t *testing.T) {
	x := [][]float64{make([]float64, 5000), make([]float64, 5000)}
	_, s := encodeTrack(t, "standard", DSD64, 352800, x, nil, false)
	for _, v := range []struct {
		kind, src string
		rate, in  int
	}{{"clans", "pcm", DSD64, 352800}, {"standard", "dsd", DSD64, 352800}, {"standard", "pcm", 2 * DSD64, 352800}, {"standard", "pcm", 4 * DSD64, 705600}} {
		e := &Encoder{DSDRate: v.rate, InRate: v.in, NumChannels: 2, Frames: 10000, Bounded: true, Kind: v.kind, Source: v.src}
		if err := e.Open(); err != nil {
			t.Fatal(err)
		}
		if err := e.SetState(s); err == nil {
			t.Errorf("%s for %s, DSD %d from %d took a standard DSD64 PCM state", v.kind, v.src, v.rate, v.in)
		}
	}
	e := &Encoder{DSDRate: DSD64, InRate: 352800, NumChannels: 2, Frames: 10000, Bounded: true, Kind: "standard", Source: "pcm"}
	if err := e.Open(); err != nil {
		t.Fatal(err)
	}
	if _, err := e.State(); err == nil {
		t.Error("State before a held close")
	}
}

// TestStateEachTrackAloneBreaksTheBitstream: the control: the same tracks each through a fresh encoder closed at its own
// end (as before 2026-11-05) are not the whole stream's bits.
func TestStateEachTrackAloneBreaksTheBitstream(t *testing.T) {
	n := 352800/3 + 1234
	x := [][]float64{make([]float64, n), make([]float64, n)}
	for i := 0; i < n; i++ {
		x[0][i] = 0.3 * math.Sin(2*math.Pi*1000*float64(i)/352800)
		x[1][i] = x[0][i]
	}
	whole, _ := encodeTrack(t, "standard", DSD64, 352800, x, nil, true)
	a, _ := encodeTrack(t, "standard", DSD64, 352800, [][]float64{x[0][:n/2], x[1][:n/2]}, nil, true)
	b, _ := encodeTrack(t, "standard", DSD64, 352800, [][]float64{x[0][n/2:], x[1][n/2:]}, nil, true)
	if bytes.Equal(append(a[0], b[0]...), whole[0]) {
		t.Fatal("tracks encoded alone joined to the whole stream's bits: the control should differ")
	}
}

// TestCloseHeldEndsOnAGroupNotZero: a held close emits whole groups of 4 bytes a channel, and backs off a group whose last
// byte is zero in every channel (squeezelite would strip it as padding); what it backs off is carried, bits in hand too.
func TestCloseHeldEndsOnAGroupNotZero(t *testing.T) {
	for _, v := range []struct {
		pos   int
		zero  []int // bytes zero in both channels
		emits int
	}{
		{10, nil, 8},         // 10 bytes: 2 carried
		{12, nil, 12},        // whole groups: none carried
		{12, []int{11}, 8},   // the last byte zero in both: back a group
		{12, []int{7, 11}, 4}, // and the one before: back two
		{3, nil, 0},          // under a group: all carried
	} {
		e := &Encoder{DSDRate: DSD64, InRate: 352800, NumChannels: 2, Frames: 1000, Bounded: true, Source: "pcm"}
		if err := e.Open(); err != nil {
			t.Fatal(err)
		}
		for _, ch := range e.chans {
			for i := 0; i < v.pos; i++ {
				ch.block[i] = byte(i + 1)
			}
			for _, z := range v.zero {
				ch.block[z] = 0
			}
			ch.pos, ch.cur, ch.nbit = v.pos, 0x5, 3
		}
		out, err := e.CloseHeld()
		if err != nil {
			t.Fatal(err)
		}
		got := played(t, out, 2)
		if v.emits == 0 && len(out) != 0 || v.emits > 0 && len(got[0]) != v.emits {
			t.Errorf("%d bytes, zero %v: emitted %d bytes a channel (%d data), want %d", v.pos, v.zero, len(got[0]), len(out), v.emits)
		}
		for _, ch := range e.chans {
			if len(ch.carry) != v.pos-v.emits || ch.nbit != 3 || ch.cur != 0x5 {
				t.Errorf("%d bytes, zero %v: carried %d bytes and %d bits, want %d and 3", v.pos, v.zero, len(ch.carry), ch.nbit, v.pos-v.emits)
			}
		}
	}
}

// TestStateCarriesTheBytesAndBits: what a held close carries, the bytes past the last group and the bits in hand, is
// the next encoder's start: its first bytes, its byte in hand (every L is a whole number of bytes at a frame's end, so
// only a direct state shows the bits).
func TestStateCarriesTheBytesAndBits(t *testing.T) {
	e := &Encoder{DSDRate: DSD64, InRate: 352800, NumChannels: 2, Frames: 1000, Bounded: true, Source: "pcm"}
	if err := e.Open(); err != nil {
		t.Fatal(err)
	}
	for _, ch := range e.chans {
		for i := 0; i < 10; i++ {
			ch.block[i] = byte(0x11 * (i + 1))
		}
		ch.pos, ch.cur, ch.nbit = 10, 0x5, 3
	}
	if _, err := e.CloseHeld(); err != nil {
		t.Fatal(err)
	}
	s, err := e.State()
	if err != nil {
		t.Fatal(err)
	}
	f := &Encoder{DSDRate: DSD64, InRate: 352800, NumChannels: 2, Frames: 1000, Bounded: true, Source: "pcm"}
	if err := f.Open(); err != nil {
		t.Fatal(err)
	}
	if err := f.SetState(s); err != nil {
		t.Fatal(err)
	}
	for _, ch := range f.chans {
		if ch.pos != 2 || ch.block[0] != 0x99 || ch.block[1] != 0xAA || ch.cur != 0x5 || ch.nbit != 3 || ch.done != 2*8+3 {
			t.Fatalf("the next encoder starts with %d bytes %x %x, byte in hand %x of %d bits, %d done; want 2 bytes 99 aa, 5 of 3, 19",
				ch.pos, ch.block[0], ch.block[1], ch.cur, ch.nbit, ch.done)
		}
	}
}
