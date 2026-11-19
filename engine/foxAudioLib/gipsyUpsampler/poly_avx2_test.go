package gipsyUpsampler

import (
	"math"
	"math/rand"
	"strconv"
	"testing"
)

// TestPushAVX2Bits: pushAVX2's outputs and state are pushScalar's, bit for bit: stage A (2/1) and stage B's rational
// pairs, tap counts that leave every tail length, blocks of odd sizes. It needs an amd64 build with GOEXPERIMENT=simd
// on a CPU with AVX2 (Rosetta has it); elsewhere pushAVX2 is pushScalar and the test skips, saying so.
func TestPushAVX2Bits(t *testing.T) {
	if !haveAVX2 {
		t.Skip("no AVX2 path in this build (amd64 with GOEXPERIMENT=simd) or on this CPU: nothing to compare")
	}
	for _, lm := range [][2]int{{2, 1}, {147, 160}, {160, 147}, {320, 147}, {147, 80}, {3, 2}} {
		for _, n := range []int{515, 517, 4769, 9537, 9539, 99, 6, 1} {
			r := rand.New(rand.NewSource(int64(n + lm[0])))
			proto := make([]float64, n*lm[0]/2+lm[0])
			for i := range proto {
				proto[i] = (r.Float64()*2 - 1) / math.Sqrt(float64(len(proto)))
			}
			a, b := newPoly(proto, lm[0], lm[1]), newPoly(proto, lm[0], lm[1])
			var oa, ob []float64
			for _, blk := range []int{4096, 1, 7, 333, 4096, 2, 1000} {
				x := make([]float64, blk)
				for i := range x {
					x[i] = r.Float64()*2 - 1
				}
				oa = a.pushScalar(x, oa[:0])
				ob = b.pushAVX2(x, ob[:0])
				if len(oa) != len(ob) {
					t.Fatalf("%v, %d taps: %d outputs, pushScalar %d", lm, len(proto), len(ob), len(oa))
				}
				for i := range oa {
					if math.Float64bits(oa[i]) != math.Float64bits(ob[i]) {
						t.Fatalf("%v, %d taps: output %d is %v, pushScalar's %v", lm, len(proto), i, ob[i], oa[i])
					}
				}
				if a.base != b.base || a.i != b.i || a.p != b.p || !slicesEqualBits(a.buf, b.buf) {
					t.Fatalf("%v, %d taps: state base %d i %d p %d (%d held), pushScalar's %d %d %d (%d)", lm, len(proto),
						b.base, b.i, b.p, len(b.buf), a.base, a.i, a.p, len(a.buf))
				}
			}
		}
	}
}

func slicesEqualBits(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if math.Float64bits(a[i]) != math.Float64bits(b[i]) {
			return false
		}
	}
	return true
}

// BenchmarkPush: stage A (2/1) at CD short, long and extra-long, a block of 4096 inputs, pushScalar against push (AVX2
// where the build and CPU have it). Under Rosetta only the two's ratio means anything, never against plex.
func BenchmarkPush(b *testing.B) {
	for _, n := range []int{515, 4769, 9537} {
		for _, f := range []struct {
			name string
			push func(s *poly, x, out []float64) []float64
		}{{"scalar", (*poly).pushScalar}, {"push", (*poly).push}} {
			b.Run(f.name+"/"+strconv.Itoa(n), func(b *testing.B) {
				r := rand.New(rand.NewSource(1))
				proto := make([]float64, n)
				for i := range proto {
					proto[i] = r.Float64()*2 - 1
				}
				s := newPoly(proto, 2, 1)
				x := make([]float64, 4096)
				for i := range x {
					x[i] = r.Float64()*2 - 1
				}
				out := f.push(s, x, make([]float64, 0, 8200))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					out = f.push(s, x, out[:0])
				}
				b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*len(out)), "ns/out")
			})
		}
	}
}
