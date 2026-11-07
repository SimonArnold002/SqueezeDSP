// Package: github.com/Foxenfurter/foxAudioLib/foxUpsampler
// filename fft.go
//
// A plain radix-2 complex FFT, for designing filters only (the minimum phase's cepstrum), never on the audio
// path. Iterative, in place; the inverse divides by n.
package foxUpsampler

import (
	"math"
	"math/bits"
)

// fft transforms x in place; len(x) must be a power of two.
func fft(x []complex128, inverse bool) {
	n := len(x)
	if n < 2 {
		return
	}
	shift := 64 - uint(bits.TrailingZeros(uint(n)))
	for i := 0; i < n; i++ {
		if j := int(bits.Reverse64(uint64(i)) >> shift); j > i {
			x[i], x[j] = x[j], x[i]
		}
	}
	sign := -1.0
	if inverse {
		sign = 1
	}
	for size := 2; size <= n; size <<= 1 {
		half := size / 2
		step := sign * 2 * math.Pi / float64(size)
		for k := 0; k < half; k++ {
			s, c := math.Sincos(step * float64(k))
			w := complex(c, s)
			for i := k; i < n; i += size {
				t := w * x[i+half]
				x[i+half] = x[i] - t
				x[i] += t
			}
		}
	}
	if inverse {
		inv := 1 / float64(n)
		for i := range x {
			x[i] = complex(real(x[i])*inv, imag(x[i])*inv)
		}
	}
}

func nextPow2(n int) int {
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}
