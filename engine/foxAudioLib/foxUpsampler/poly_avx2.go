//go:build goexperiment.simd && amd64

// Package: github.com/Foxenfurter/foxAudioLib/foxUpsampler
// filename poly_avx2.go
//
// push in AVX2 (Go's simd/archsimd, GOEXPERIMENT=simd, as engine/build.sh builds): four outputs at a time, each
// output's four sums in one Float64x4. pushScalar waits on its own adds (each of its four sums on its last), so
// four outputs at once keep sixteen sums in flight. Every output is pushScalar's to the bit: the same products and
// sums in the same order, Mul then Add, unfused, as Go compiles pushScalar on amd64 (ledger §A3 GOAMD64=V3 FUSES OUR
// MULTIPLY-ADDS), the taps past the last four into the first sum, then (a0+a1)+(a2+a3) (TestPushAVX2Bits). The vector
// loop walks the taps by pointer, and clears the registers' upper halves before the SSE2 code after it: Go proves no
// bound of eight slices (a check and a reload a slice a step diluted the first version) and emits no VZEROUPPER
// itself (ledger GO DOES NOT CLEAR THE AVX UPPER HALVES). Plex's cost per filter: ledger PLEX'S COST PER FILTER.
package foxUpsampler

import (
	"simd/archsimd"
	"slices"
	"unsafe"
)

// haveAVX2: the CPU has AVX2 (plex's Tiger Lake; Rosetta too, so the Mac's amd64 tests run this path).
var haveAVX2 = archsimd.X86.AVX2()

// pushAVX2: push with every output's window and phase gathered first, their dot products four at a time.
func (s *poly) pushAVX2(x []float64, out []float64) []float64 {
	s.buf = append(s.buf, x...)
	K, L, M := s.K, s.L, s.M
	last := s.base + int64(len(s.buf)) - 1
	at, ph := s.at[:0], s.ph[:0]
	for s.i <= last {
		at = append(at, int(s.i-s.base)-(K-1))
		ph = append(ph, s.p)
		s.p += M
		for s.p >= L {
			s.p -= L
			s.i++
		}
	}
	s.at, s.ph = at, ph
	n := len(out)
	out = slices.Grow(out, len(at))[:n+len(at)]
	dotsAVX2(s.buf, s.h, at, ph, K, out[n:])
	s.trim()
	return out
}

// dotsAVX2: out[k] = phase ph[k]'s taps on the K inputs from buf[at[k]], summed as pushScalar sums them: the first
// K &^ 3 taps in the vectors (sums4, sums1), the rest in finish.
func dotsAVX2(buf []float64, h [][]float64, at, ph []int, K int, out []float64) {
	n4 := K &^ 3
	k := 0
	for ; k+4 <= len(out); k += 4 {
		w0, w1, w2, w3 := buf[at[k]:at[k]+K], buf[at[k+1]:at[k+1]+K], buf[at[k+2]:at[k+2]+K], buf[at[k+3]:at[k+3]+K]
		h0, h1, h2, h3 := h[ph[k]][:K], h[ph[k+1]][:K], h[ph[k+2]][:K], h[ph[k+3]][:K]
		var s0, s1, s2, s3 [4]float64
		if n4 > 0 {
			sums4(&s0, &s1, &s2, &s3, h0, h1, h2, h3, w0, w1, w2, w3, n4)
		}
		out[k], out[k+1], out[k+2], out[k+3] = finish(&s0, h0, w0, n4), finish(&s1, h1, w1, n4), finish(&s2, h2, w2, n4), finish(&s3, h3, w3, n4)
	}
	for ; k < len(out); k++ {
		w, hh := buf[at[k]:at[k]+K], h[ph[k]][:K]
		var s [4]float64
		if n4 > 0 {
			sums1(&s, hh, w, n4)
		}
		out[k] = finish(&s, hh, w, n4)
	}
}

// sums4: four outputs' four sums over their first n4 taps (a multiple of 4, at most each slice's length), each
// output's in one Float64x4, stored to s0..s3; then the upper halves cleared.
func sums4(s0, s1, s2, s3 *[4]float64, h0, h1, h2, h3, w0, w1, w2, w3 []float64, n4 int) {
	q0, q1, q2, q3 := unsafe.Pointer(&h0[0]), unsafe.Pointer(&h1[0]), unsafe.Pointer(&h2[0]), unsafe.Pointer(&h3[0])
	p0, p1, p2, p3 := unsafe.Pointer(&w0[0]), unsafe.Pointer(&w1[0]), unsafe.Pointer(&w2[0]), unsafe.Pointer(&w3[0])
	var c0, c1, c2, c3 archsimd.Float64x4
	for off, end := uintptr(0), uintptr(n4)*8; off < end; off += 32 {
		c0 = c0.Add(at4(q0, off).Mul(at4(p0, off)))
		c1 = c1.Add(at4(q1, off).Mul(at4(p1, off)))
		c2 = c2.Add(at4(q2, off).Mul(at4(p2, off)))
		c3 = c3.Add(at4(q3, off).Mul(at4(p3, off)))
	}
	c0.StoreArray(s0)
	c1.StoreArray(s1)
	c2.StoreArray(s2)
	c3.StoreArray(s3)
	archsimd.ClearAVXUpperBits()
}

// sums1: sums4 for one output.
func sums1(s *[4]float64, h, w []float64, n4 int) {
	q, p := unsafe.Pointer(&h[0]), unsafe.Pointer(&w[0])
	var c archsimd.Float64x4
	for off, end := uintptr(0), uintptr(n4)*8; off < end; off += 32 {
		c = c.Add(at4(q, off).Mul(at4(p, off)))
	}
	c.StoreArray(s)
	archsimd.ClearAVXUpperBits()
}

// at4: the four float64s off bytes on from p.
func at4(p unsafe.Pointer, off uintptr) archsimd.Float64x4 {
	return archsimd.LoadFloat64x4Array((*[4]float64)(unsafe.Add(p, off)))
}

// finish: pushScalar's ending from the four sums in s: the taps from j on into the first, then (a0+a1)+(a2+a3).
func finish(s *[4]float64, h, w []float64, j int) float64 {
	a0 := s[0]
	for ; j < len(w); j++ {
		a0 += h[j] * w[j]
	}
	return (a0 + s[1]) + (s[2] + s[3])
}
