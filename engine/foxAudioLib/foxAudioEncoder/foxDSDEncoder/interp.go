// Package: github.com/Foxenfurter/foxAudioLib/foxAudioEncoder/foxDSDEncoder
// filename interp.go
//
// PCM up to the DSD rate by L: a Kaiser-windowed sinc cut at the input's Nyquist, so DSD sample k*L is
// PCM frame k exactly (phase 0 is a copy), flat to a quarter of the input rate (88.2 kHz from 352.8k) and
// -120 dB from three quarters of it, where the first image starts. The modulator's own noise covers what
// is left: it is far above -120 dB there. K input frames either side: 8 for every L.
//
// The delay is removed: the input before frame 0 is silence and the end is flushed with silence, so the
// output is exactly L samples per frame, and frame k comes out at DSD sample k*L.
package foxDSDEncoder

import (
	"math"
	"slices"
)

const (
	interpStopDB   = 120.0
	interpPassFrac = 0.25 // flat to this share of the input rate; the stop band starts at 1 less it
)

type interpolator struct {
	L, K  int
	h     [][]float64 // [l][t]: the tap on window sample t (oldest first) for output phase l > 0
	bound float64     // the largest gain any output can have: the largest sum of a phase's |taps| (phase 0, a copy: 1)
	buf   []float64   // the inputs not yet past: 2K of history, then new frames
}

func bessel0(x float64) float64 {
	sum, term := 1.0, 1.0
	for k := 1; k < 500; k++ {
		term *= (x / (2 * float64(k))) * (x / (2 * float64(k)))
		sum += term
		if term < 1e-17*sum {
			break
		}
	}
	return sum
}

func newInterpolator(L int) *interpolator {
	// Kaiser: length (A - 8) / (2.285 * 2 pi * width), width in cycles per output sample
	width := (1 - 2*interpPassFrac) / float64(L)
	n := (interpStopDB - 8) / (2.285 * 2 * math.Pi * width)
	K := int(math.Ceil(n / float64(2*L)))
	beta := 0.1102 * (interpStopDB - 8.7)
	half := float64(K * L)
	it := &interpolator{L: L, K: K, h: make([][]float64, L), buf: make([]float64, K)}
	for l := 1; l < L; l++ {
		it.h[l] = make([]float64, 2*K+1)
		for t := 0; t <= 2*K; t++ {
			m := float64((K-t)*L + l) // distance in output samples from window sample t to the output
			if math.Abs(m) >= half {
				continue
			}
			x := m / float64(L)
			r := m / half
			it.h[l][t] = math.Sin(math.Pi*x) / (math.Pi * x) * bessel0(beta*math.Sqrt(1-r*r)) / bessel0(beta)
		}
	}
	it.bound = 1
	for l := 1; l < L; l++ {
		sum := 0.0
		for _, v := range it.h[l] {
			sum += math.Abs(v)
		}
		it.bound = math.Max(it.bound, sum)
	}
	return it
}

// push takes PCM frames and appends the DSD-rate samples of every frame whose window is complete.
func (it *interpolator) push(x []float64, out []float64) []float64 {
	it.buf = append(it.buf, x...)
	K := it.K
	frames := len(it.buf) - 2*K // frames j whose window buf[j .. j+2K] is in
	n := 2*K + 1
	L := it.L
	if frames > 0 {
		base := len(out)
		out = slices.Grow(out, frames*L)[:base+frames*L]
		o := out[base:]
		for j := 0; j < frames; j++ {
			w := it.buf[j : j+n]
			oj := o[j*L : j*L+L]
			oj[0] = w[K]
			if K == 8 {
				// every L has K = 8 (17 taps): unrolled on fixed-length arrays, so no bounds checks
				wa := (*[17]float64)(w)
				for l := 1; l < L; l++ {
					h := (*[17]float64)(it.h[l])
					s0 := wa[0]*h[0] + wa[4]*h[4] + wa[8]*h[8] + wa[12]*h[12] + wa[16]*h[16]
					s1 := wa[1]*h[1] + wa[5]*h[5] + wa[9]*h[9] + wa[13]*h[13]
					s2 := wa[2]*h[2] + wa[6]*h[6] + wa[10]*h[10] + wa[14]*h[14]
					s3 := wa[3]*h[3] + wa[7]*h[7] + wa[11]*h[11] + wa[15]*h[15]
					oj[l] = (s0 + s1) + (s2 + s3)
				}
				continue
			}
			for l := 1; l < L; l++ {
				h := it.h[l][:n]
				// four sums side by side: one long chain of multiply-adds is the slow part
				var s0, s1, s2, s3 float64
				t := 0
				for ; t+4 <= n; t += 4 {
					s0 += w[t] * h[t]
					s1 += w[t+1] * h[t+1]
					s2 += w[t+2] * h[t+2]
					s3 += w[t+3] * h[t+3]
				}
				for ; t < n; t++ {
					s0 += w[t] * h[t]
				}
				oj[l] = (s0 + s1) + (s2 + s3)
			}
		}
	}
	if frames > 0 {
		n := copy(it.buf, it.buf[frames:])
		it.buf = it.buf[:n]
	}
	return out
}

// flush: the last K frames, against silence after the end.
func (it *interpolator) flush(out []float64) []float64 {
	return it.push(make([]float64, it.K), out)
}
