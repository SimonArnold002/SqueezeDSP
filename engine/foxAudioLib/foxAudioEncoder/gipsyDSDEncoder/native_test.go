package gipsyDSDEncoder

import (
	"math"
	"testing"
)

// TestNativeRateIsTheLoopAlone (the native DSD round, 2026-11-07): input already at the DSD rate (a native decode) opens
// with L 1, and every bit written is the loop's on the input as it came (the gain and the limit only, no
// interpolation), each modulator at DSD64 and standard and CLANS at DSD128; the stated length is the input's.
func TestNativeRateIsTheLoopAlone(t *testing.T) {
	for _, tc := range []struct {
		rate int
		kind string
	}{{DSD64, "standard"}, {DSD64, "clans"}, {DSD64, "adaptive"}, {2 * DSD64, "standard"}, {2 * DSD64, "clans"}} {
		n := 150000
		x := stereoTone(n, float64(tc.rate), 1000, 0.6)
		e := &Encoder{DSDRate: tc.rate, InRate: tc.rate, NumChannels: 2, Frames: int64(n), Kind: tc.kind, GainDB: 1.5}
		out := encode(t, e, x, 7777)
		if e.L != 1 || e.count != int64(n) {
			t.Fatalf("%d %s: L %d, %d samples stated; want 1, %d", tc.rate, tc.kind, e.L, e.count, n)
		}
		g := math.Pow(10, 1.5/20)
		fam := 0
		if tc.rate != DSD64 {
			fam = 1
		}
		for c := range x {
			u := make([]float64, n)
			for i, v := range x[c] {
				u[i] = math.Max(-Limit, math.Min(Limit, v*g))
			}
			var bits []byte
			if tc.kind == "adaptive" {
				l := newAdaptiveLoop(adaptiveDesigns[0], adaptiveDesigns[1], tc.rate)
				bits = l.run(u, nil)
				bits = l.drain(bits)
			} else {
				bits = make([]byte, n)
				newModulator(designs[tc.kind][fam]).run(u, bits)
			}
			for i := 0; i < n; i++ {
				// DSF: the header's 92 bytes, then 4096-byte blocks a channel in turn, each byte's oldest bit its LSB
				k := 92 + (i/(blockSize*8))*blockSize*2 + c*blockSize + (i%(blockSize*8))/8
				if got := float64((out[k]>>uint(i%8))&1)*2 - 1; got != float64(bits[i])*2-1 {
					t.Fatalf("%d %s channel %d: bit %d is %v, the loop's %d", tc.rate, tc.kind, c, i, got, bits[i])
				}
			}
		}
	}
}
