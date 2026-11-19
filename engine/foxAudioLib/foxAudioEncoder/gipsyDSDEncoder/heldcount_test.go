package gipsyDSDEncoder

import (
	"bytes"
	"encoding/binary"
	"math"
	"math/rand"
	"testing"
)

// The exact-length round, step 4: a player that needs the true length before the first byte gets a held stream whose
// header states, at its start, exactly the samples it carries (HeldCount); the next track's stream goes on from it.

// channels: a DSF stream's data, each channel's bytes, block by block
func channels(t *testing.T, data []byte, ch int) [][]byte {
	t.Helper()
	if len(data)%(blockSize*ch) != 0 {
		t.Fatalf("data of %d bytes is not whole blocks", len(data))
	}
	out := make([][]byte, ch)
	for k := 0; k < len(data)/(blockSize*ch); k++ {
		for c := 0; c < ch; c++ {
			o := (k*ch + c) * blockSize
			out[c] = append(out[c], data[o:o+blockSize]...)
		}
	}
	return out
}

// encodeExact: one track's stream, its count stated at the start: held (a next track goes on from it) at
// HeldBack's count, or played out to the count rounded up to a group. Returns each channel's bytes up to the count,
// the count, the rest of the stream past it, and the state and bits held for the next.
func encodeExact(t *testing.T, kind string, rate, in int, x [][]float64, state []byte, heldIn int64, last bool) ([][]byte, int64, [][]byte, []byte, int64) {
	t.Helper()
	L := int64(rate / in)
	n := int64(len(x[0]))
	var count int64
	if last {
		count = (heldIn + n*L + Group - 1) / Group * Group
	} else {
		// a stream shorter than what the encoder keeps back (an adaptive loop's 20 ms look-ahead) carries nothing
		count = max(heldIn+n*L-HeldBack(rate, in, kind), 0) / Group * Group
	}
	e := &Encoder{DSDRate: rate, InRate: in, NumChannels: len(x), Count: count, Kind: kind, Source: "pcm", HeldCount: !last}
	if err := e.Open(); err != nil {
		t.Fatal(err)
	}
	h, err := e.EncodeHeader()
	if err != nil {
		t.Fatal(err)
	}
	if got := int64(binary.LittleEndian.Uint64(h[28+12+24:])); got != count {
		t.Fatalf("the header states %d samples, the count is %d", got, count)
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
	if last {
		tail, err = e.Close()
	} else {
		tail, err = e.CloseHeld()
	}
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, tail...)
	if len(h) != 92 {
		t.Fatalf("header of %d bytes", len(h))
	}
	if dataLen := int64(binary.LittleEndian.Uint64(h[84:])) - 12; dataLen != int64(len(data)) {
		t.Fatalf("the header's data chunk states %d bytes, the stream carries %d", dataLen, len(data))
	}
	got := channels(t, data, len(x))
	var rest [][]byte
	for c := range got {
		rest = append(rest, got[c][count/8:])
		got[c] = got[c][:count/8]
	}
	var next []byte
	var bits int64
	if !last {
		if next, err = e.State(); err != nil {
			t.Fatal(err)
		}
		bits = e.HeldBits()
	}
	return got, count, rest, next, bits
}

func signal(in, n int, seed int64) [][]float64 {
	rng := rand.New(rand.NewSource(seed))
	x := [][]float64{make([]float64, n), make([]float64, n)}
	for i := 0; i < n; i++ {
		s := float64(i) / float64(in)
		env := 0.2 + 0.4*math.Max(0, math.Sin(2*math.Pi*3*s)) // swells past the adaptive threshold and back
		x[0][i] = env*math.Sin(2*math.Pi*1000*s) + 0.01*(rng.Float64()*2-1)
		x[1][i] = env*math.Sin(2*math.Pi*1700*s) + 0.01*(rng.Float64()*2-1)
	}
	return x
}

// TestHeldCountIsExact: three tracks, each through an encoder of its own going on from the last's State, each stream's
// count stated before its first byte (held at HeldBack's count, the last played out): every stream carries exactly
// its count (the rest of its last block silence), and the streams' samples one after the other are the whole run's
// through one encoder, at DSD64 and DSD256 with every modulator, at cut points that land anywhere in a block.
func TestHeldCountIsExact(t *testing.T) {
	for _, tc := range []struct {
		kind     string
		rate, in int
	}{{"standard", DSD64, 352800}, {"clans", DSD64, 352800}, {"adaptive", DSD64, 352800}, {"standard", 4 * DSD64, 705600},
		{"clans", 4 * DSD64, 705600}, {"adaptive", 2 * DSD64, 352800}, {"standard", DSD64, DSD64}} {
		n := tc.in/3 + 1234
		x := signal(tc.in, n, int64(tc.rate+len(tc.kind)))
		whole, wcount, _, _, _ := encodeExact(t, tc.kind, tc.rate, tc.in, x, nil, 0, true)
		for _, cuts := range [][]int{{0, n*3/10 + 7, n*7/10 + 3, n}, {0, 4096, 4097 + 517, n}, {0, n / 2, n/2 + 1, n}} {
			joined := [][]byte{nil, nil}
			var state []byte
			var held, total int64
			for k := 0; k < 3; k++ {
				part := [][]float64{x[0][cuts[k]:cuts[k+1]], x[1][cuts[k]:cuts[k+1]]}
				got, count, rest, next, bits := encodeExact(t, tc.kind, tc.rate, tc.in, part, state, held, k == 2)
				if k < 2 {
					for c := range rest {
						for i, b := range rest[c] {
							if b != silence {
								t.Fatalf("%s DSD %d track %d: byte %d past the count is %#x, not silence", tc.kind, tc.rate, k+1, i, b)
							}
						}
					}
				}
				for c := range got {
					joined[c] = append(joined[c], got[c]...)
				}
				total += count
				state, held = next, bits
			}
			if total != wcount {
				t.Fatalf("%s DSD %d cuts %v: the streams state %d samples together, the whole run %d", tc.kind, tc.rate, cuts, total, wcount)
			}
			for c := range whole {
				if !bytes.Equal(joined[c], whole[c]) {
					i := 0
					for i < min(len(joined[c]), len(whole[c])) && joined[c][i] == whole[c][i] {
						i++
					}
					t.Fatalf("%s DSD %d cuts %v channel %d: joined %d bytes, whole %d, first difference at byte %d",
						tc.kind, tc.rate, cuts, c, len(joined[c]), len(whole[c]), i)
				}
			}
		}
	}
}

// TestHeldCountShortFills: a count past what the encoder has (a caller's wrong count) ends the stream at the count
// anyway, the missing bytes silence: the header is never more than the stream.
func TestHeldCountShortFills(t *testing.T) {
	x := signal(352800, 20000, 7)
	count := (int64(20000)*8 - HeldBack(DSD64, 352800, "standard")) / Group * Group
	count += 3 * Group
	e := &Encoder{DSDRate: DSD64, InRate: 352800, NumChannels: 2, Count: count, Source: "pcm", HeldCount: true}
	if err := e.Open(); err != nil {
		t.Fatal(err)
	}
	data, err := e.EncodeData(x)
	if err != nil {
		t.Fatal(err)
	}
	tail, err := e.CloseHeld()
	if err != nil {
		t.Fatal(err)
	}
	got := channels(t, append(data, tail...), 2)
	for c := range got {
		if int64(len(got[c])) < count/8 {
			t.Fatalf("channel %d: %d bytes, the count needs %d", c, len(got[c]), count/8)
		}
		for i := count/8 - 12; i < count/8; i++ {
			if got[c][i] != silence {
				t.Fatalf("channel %d byte %d: %#x, not the silence that fills to the count", c, i, got[c][i])
			}
		}
	}
	if len(e.chans[0].carry) != 0 || e.chans[0].nbit != 0 {
		t.Fatalf("a short stream holds nothing past its count: %d bytes, %d bits", len(e.chans[0].carry), e.chans[0].nbit)
	}
}

// TestHeldBackIsWhatCloseHeldKeeps: HeldBack, known at the start, is what HeldBits says the encoder kept back past its
// packed bits after a held close (the interpolator's frames and an adaptive loop's look-ahead), for every factor.
func TestHeldBackIsWhatCloseHeldKeeps(t *testing.T) {
	for _, tc := range []struct {
		kind     string
		rate, in int
	}{{"standard", DSD64, 352800}, {"adaptive", DSD64, 352800}, {"adaptive", 2 * DSD64, 352800}, {"standard", 8 * DSD64, 705600},
		{"clans", 4 * DSD64, 88200 * 4}, {"standard", DSD64, DSD64}} {
		x := signal(tc.in, 30000, 3)
		e := &Encoder{DSDRate: tc.rate, InRate: tc.in, NumChannels: 2, Frames: 40000, Bounded: true, Kind: tc.kind, Source: "pcm"}
		if err := e.Open(); err != nil {
			t.Fatal(err)
		}
		if _, err := e.EncodeData(x); err != nil {
			t.Fatal(err)
		}
		if _, err := e.CloseHeld(); err != nil {
			t.Fatal(err)
		}
		ch := e.chans[0]
		kept := e.HeldBits() - int64(len(ch.carry))*8 - int64(ch.nbit)
		if hb := HeldBack(tc.rate, tc.in, tc.kind); kept != hb {
			t.Fatalf("%s DSD %d from %d: kept back %d, HeldBack says %d", tc.kind, tc.rate, tc.in, kept, hb)
		}
		if packed := ch.done; packed+kept != 30000*int64(tc.rate/tc.in) {
			t.Fatalf("%s DSD %d from %d: packed %d + kept %d is not the %d in", tc.kind, tc.rate, tc.in, packed, kept, 30000*(tc.rate/tc.in))
		}
	}
}
