// Package: github.com/Foxenfurter/foxAudioLib/foxUpsampler
// filename poly.go
//
// One rational stage, L/M: the input zero-stuffed by L, filtered by the prototype h (at L times the input
// rate, gain L), every Mth sample kept. Polyphase: output n is prototype sample n*M, so phase p = n*M mod L of
// the filter on the input around frame n*M div L, K taps a phase. x2 (A) is 2/1.
package foxUpsampler

type poly struct {
	L, M, K int
	h       [][]float64 // [p][j]: phase p's taps, oldest input first (h[p + (K-1-j)*L])
	buf     []float64   // inputs held: K-1 of history, then the new ones
	base    int64       // the input index of buf[0]
	i       int64       // the newest input the next output uses
	p       int         // the next output's phase
	at, ph  []int       // pushAVX2's scratch: each output's window start in buf and its phase
}

func newPoly(proto []float64, L, M int) *poly {
	K := (len(proto) + L - 1) / L
	s := &poly{L: L, M: M, K: K, h: make([][]float64, L)}
	for p := 0; p < L; p++ {
		s.h[p] = make([]float64, K)
		for j := 0; j < K; j++ {
			if t := p + (K-1-j)*L; t < len(proto) {
				s.h[p][j] = proto[t]
			}
		}
	}
	// silence before the first input: K-1 zeros of history
	s.buf = make([]float64, K-1, K-1+4096)
	s.base = -int64(K - 1)
	return s
}

// push takes inputs and appends every output whose newest input is in: four outputs at a time in AVX2 where the
// engine is built with it and the CPU has it (poly_avx2.go), else pushScalar; the same bits either way.
func (s *poly) push(x []float64, out []float64) []float64 {
	if haveAVX2 {
		return s.pushAVX2(x, out)
	}
	return s.pushScalar(x, out)
}

// AVX2: whether push runs in AVX2 here; the engine logs it at a stream's start, as the bits are the same either way.
func AVX2() bool { return haveAVX2 }

// pushScalar: push one output at a time, the reference (TestPushAVX2Bits).
func (s *poly) pushScalar(x []float64, out []float64) []float64 {
	s.buf = append(s.buf, x...)
	K, L, M := s.K, s.L, s.M
	last := s.base + int64(len(s.buf)) - 1
	for s.i <= last {
		w := s.buf[s.i-s.base-int64(K-1) : s.i-s.base+1]
		h := s.h[s.p][:len(w)] // the same length as w, so the loops below need no bounds checks
		var a0, a1, a2, a3 float64
		j := 0
		for ; j+4 <= len(w); j += 4 {
			ww, hh := w[j:j+4:j+4], h[j:j+4:j+4] // one check for each four taps, not eight
			a0 += hh[0] * ww[0]
			a1 += hh[1] * ww[1]
			a2 += hh[2] * ww[2]
			a3 += hh[3] * ww[3]
		}
		for ; j < len(w); j++ {
			a0 += h[j] * w[j]
		}
		out = append(out, (a0+a1)+(a2+a3))
		s.p += M
		for s.p >= L {
			s.p -= L
			s.i++
		}
	}
	s.trim()
	return out
}

// trim keeps K-1 inputs before the next output's newest.
func (s *poly) trim() {
	keep := s.i - int64(s.K-1)
	if drop := keep - s.base; drop > 0 {
		if drop > int64(len(s.buf)) {
			drop = int64(len(s.buf))
		}
		n := copy(s.buf, s.buf[drop:])
		s.buf = s.buf[:n]
		s.base += drop
	}
}
