package foxConvolverPartition

import (
	"math"
	"math/rand"
	"testing"
)

// TestMultiplyAddAVX2Bits: multiplyAddAVX2 = multiplyAddScalar, bit for bit, summed over twelve partitions: the
// convolver's bin counts and short ones (every tail length), magnitudes over 17 decades. It needs an amd64 build with
// GOEXPERIMENT=simd on a CPU with AVX2 (Rosetta has it); elsewhere the two are one and the test skips, saying so.
func TestMultiplyAddAVX2Bits(t *testing.T) {
	if !haveAVX2 {
		t.Skip("no AVX2 path in this build (amd64 with GOEXPERIMENT=simd) or on this CPU: nothing to compare")
	}
	r := rand.New(rand.NewSource(1))
	spectrum := func(n int) []complex128 {
		s := make([]complex128, n)
		for i := range s {
			s[i] = complex(r.NormFloat64()*math.Exp(r.Float64()*20-10), r.NormFloat64()*math.Exp(r.Float64()*20-10))
		}
		return s
	}
	for _, n := range []int{8193, 4097, 2049, 1, 2, 3, 5, 9} {
		ra, rb := make([]complex128, n), make([]complex128, n)
		for p := 0; p < 12; p++ {
			a, h := spectrum(n+p%3), spectrum(n+p%2) // longer than res, as the ring's and the impulse's slots may be
			multiplyAddScalar(ra, a, h)
			multiplyAddAVX2(rb, a, h)
		}
		for i := range ra {
			if math.Float64bits(real(ra[i])) != math.Float64bits(real(rb[i])) || math.Float64bits(imag(ra[i])) != math.Float64bits(imag(rb[i])) {
				t.Fatalf("%d bins: bin %d is %v, multiplyAddScalar's %v", n, i, rb[i], ra[i])
			}
		}
	}
}

// BenchmarkMultiplyAdd: one chunk at 705.6k's partitioning (8193 bins), multiplyAddScalar against multiplyAdd (AVX2
// where the build and CPU have it), over 16 partitions held in cache. Under Rosetta only the two's ratio means anything.
func BenchmarkMultiplyAdd(b *testing.B) {
	r := rand.New(rand.NewSource(1))
	const n, parts = 8193, 16
	a, h := make([][]complex128, parts), make([][]complex128, parts)
	for p := range a {
		a[p], h[p] = make([]complex128, n), make([]complex128, n)
		for i := range a[p] {
			a[p][i], h[p][i] = complex(r.NormFloat64(), r.NormFloat64()), complex(r.NormFloat64(), r.NormFloat64())
		}
	}
	res := make([]complex128, n)
	for _, f := range []struct {
		name string
		mac  func(res, a, h []complex128)
	}{{"scalar", multiplyAddScalar}, {"multiplyAdd", multiplyAdd}} {
		b.Run(f.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				clear(res)
				for p := 0; p < parts; p++ {
					f.mac(res, a[p], h[p])
				}
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*parts*n), "ns/bin")
		})
	}
}
