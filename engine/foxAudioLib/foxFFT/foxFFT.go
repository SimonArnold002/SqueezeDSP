// Package foxFFT provides a Cooley-Tukey FFT optimised for use inside a
// partitioned convolver whose block size is fixed at either 4096 (44.1/48 kHz)
// or 8192 (88.2/96 kHz) complex samples.
//
// Hot-path design decisions
//   - Both plans are fully built at init() — zero allocation during FFT calls.
//   - Per-stage twiddle slices are indexed sequentially (tw[k]) so the inner
//     loop streams through memory rather than striding with k*step.
//   - Stages 1+2 are fused into a single radix-4 pass (no twiddle lookups).
//   - Stage 3 is unrolled with hard-coded roots of unity (no twiddle lookups).
//   - IFFT uses the conj/FFT/conj identity — two sequential passes instead of
//     the reversal loop, which is cache-hostile at these sizes.
//   - The fallback path for arbitrary power-of-two sizes is retained but uses
//     sync.Once + a fixed array instead of a RWMutex + map.
package foxFFT

import (
	"fmt"
	"math"
	"math/bits"
	"sync"
)

const packageName = "foxFFT"

// Supported convolver FFT sizes.
const (
    FFTSize128   = 128
    FFTSize256   = 256
    FFTSize512   = 512
    FFTSize1024  = 1024
    FFTSize2048  = 2048
    FFTSize4096  = 4096  // FFTSize44k
    FFTSize8192  = 8192  // FFTSize192k
)
// invsqrt2 = 1/√2, used in the hard-coded 8-point butterfly stage.
const invsqrt2 = 0.7071067811865476

// ─── Plan ────────────────────────────────────────────────────────────────────

// fftPlan holds pre-computed twiddle factors for one fixed FFT size.
//
// Stages covered by the plan:
//
//	n=1,2  → fused radix-4 block   (no twiddles needed)
//	n=4    → hard-coded 8-pt block (no twiddles needed)
//	n=8…N/2→ stageTwiddles[i], where i=0 ↔ n=8
//
// Each stageTwiddles[i] has length n and is indexed sequentially: tw[k] is the
// twiddle for butterfly index k at that stage. This replaces the original
// strided access twiddles[k*step] and keeps the inner loop cache-friendly.
type fftPlan struct {
	N             int
	stageTwiddles [][]complex128
}

// Plans are constructed once at package init — never written to again.

var (
    plan128  = buildPlan(FFTSize128)
    plan256  = buildPlan(FFTSize256)
    plan512  = buildPlan(FFTSize512)
    plan1024 = buildPlan(FFTSize1024)
    plan2048 = buildPlan(FFTSize2048)
    plan4096 = buildPlan(FFTSize4096)
    plan8192 = buildPlan(FFTSize8192)
)


// buildPlan pre-computes sequential per-stage twiddle slices for size N.
// The first three stages (n=1,2,4) are handled inline, so we start at n=8.
func buildPlan(N int) fftPlan {
	// Total general stages: log2(N) − 1 butterfly stages, minus 3 inlined.
	// n iterates 8, 16, 32 … N/2  →  log2(N)−4 slices.
	stages := make([][]complex128, 0, bits.Len64(uint64(N))-4)
	for n := 8; n < N; n <<= 1 {
		m := n << 1
		tw := make([]complex128, n)
		for k := 0; k < n; k++ {
			s, c := math.Sincos(-2 * math.Pi * float64(k) / float64(m))
			tw[k] = complex(c, s)
		}
		stages = append(stages, tw)
	}
	return fftPlan{N: N, stageTwiddles: stages}
}

// getPlan returns the pre-built plan for the two supported sizes, or nil.
func getPlan(N int) *fftPlan {
    switch N {
    case FFTSize128:
        return &plan128
    case FFTSize256:
        return &plan256
    case FFTSize512:
        return &plan512
    case FFTSize1024:
        return &plan1024
    case FFTSize2048:
        return &plan2048
    case FFTSize4096:
        return &plan4096
    case FFTSize8192:
        return &plan8192
    default:
        return nil
    }
}

// ─── Public API ───────────────────────────────────────────────────────────────

// Fft performs an in-place FFT (forward or inverse) on x.
// len(x) must be a power of two.
func Fft(x []complex128, inverse bool) error {
	if err := checkLength("FFT input", len(x)); err != nil {
		return err
	}
	if inverse {
		ifft128(x)
	} else {
		fft128(x)
	}
	return nil
}

// FftUnchecked is identical to Fft but skips the length check.
// Use in the convolver inner loop where the size is guaranteed correct.
func FftUnchecked(x []complex128, inverse bool) error {
	if inverse {
		ifft128(x)
	} else {
		fft128(x)
	}
	return nil
}

// ─── Bit-reversal permutation ────────────────────────────────────────────────

func permute(x []complex128) {
	N := len(x)
	switch N {
	case 1, 2:
		return
	case 4:
		x[1], x[2] = x[2], x[1]
		return
	case 8:
		x[1], x[4] = x[4], x[1]
		x[3], x[6] = x[6], x[3]
		return
	}
	shift := 64 - uint64(bits.Len64(uint64(N-1)))
	N2 := N >> 1
	for i := 0; i < N; i += 2 {
		ind := int(bits.Reverse64(uint64(i)) >> shift)
		if ind > i {
			x[i], x[ind] = x[ind], x[i]
		}
		ind |= N2
		if ind > i+1 {
			x[i+1], x[ind] = x[ind], x[i+1]
		}
	}
}

// ─── Forward FFT ─────────────────────────────────────────────────────────────

func fft128(x []complex128) {
	N := len(x)

	if N == 1 {
		return // single sample — FFT is the identity, nothing to do
	}
	if N == 2 {
		x[0], x[1] = x[0]+x[1], x[0]-x[1] // length-2 butterfly
		return
	}

	permute(x)

	// ── Stage 1 + 2 fused: radix-4 butterfly ─────────────────────────────
	// Combines the length-2 and length-4 butterfly passes.
	// The twiddle for the length-4 pass at k=1 is e^{-iπ/2} = −i, so the
	// multiply becomes a 90° rotation: (a+bi)*(−i) = (b − ai).
	for i := 0; i < N; i += 4 {
		a, b, c, d := x[i], x[i+1], x[i+2], x[i+3]
		// f = (c − d) * (−i)  →  swap re/im and negate new imaginary part
		f := complex(imag(c)-imag(d), real(d)-real(c))
		x[i] = a + b + c + d
		x[i+1] = a - b + f
		x[i+2] = a - c + b - d
		x[i+3] = a - b - f
	}

	if N < 8 {
		return
	}

	// ── Stage 3: hard-coded 8-point butterfly ─────────────────────────────
	// Twiddles for k = 0..3 at half-size n=4 (full-size m=8):
	//   k=0: w = 1
	//   k=1: w = (1−i)/√2
	//   k=2: w = −i
	//   k=3: w = (−1−i)/√2
	for i := 0; i < N; i += 8 {
		// k=0: multiply by 1 — just a butterfly
		u, v := x[i], x[i+4]
		x[i], x[i+4] = u+v, u-v

		// k=1: multiply x[i+5] by (1−i)/√2
		//   (a+bi)(1−i)/√2 = ((a+b) + (b−a)i) / √2
		u = x[i+1]
		r5, m5 := real(x[i+5]), imag(x[i+5])
		v = complex((r5+m5)*invsqrt2, (m5-r5)*invsqrt2)
		x[i+1], x[i+5] = u+v, u-v

		// k=2: multiply x[i+6] by −i
		//   (a+bi)(−i) = b − ai
		u = x[i+2]
		v = complex(imag(x[i+6]), -real(x[i+6]))
		x[i+2], x[i+6] = u+v, u-v

		// k=3: multiply x[i+7] by (−1−i)/√2
		//   (a+bi)(−1−i)/√2 = ((b−a) + (−a−b)i) / √2
		u = x[i+3]
		r7, m7 := real(x[i+7]), imag(x[i+7])
		v = complex((m7-r7)*invsqrt2, (-r7-m7)*invsqrt2)
		x[i+3], x[i+7] = u+v, u-v
	}

	if N <= 8 {
		return
	}

	// ── General butterfly stages: n = 8, 16, … N/2 ───────────────────────
	plan := getPlan(N)
	if plan != nil {
		// Fast path: sequential per-stage twiddles, no map/lock overhead.
		// tw[k] is the exact twiddle for butterfly index k at this stage.
		for si, tw := range plan.stageTwiddles {
			n := 8 << uint(si)
			m := n << 1
			for o := 0; o < N; o += m {
				xo := x[o : o+m] // sub-slice avoids repeated base+offset arithmetic
				for k := 0; k < n; k++ {
					f := tw[k] * xo[k+n]
					xo[k], xo[k+n] = xo[k]+f, xo[k]-f
				}
			}
		}
	} else {
		// Fallback: arbitrary power-of-two size.
		// Uses the original strided-twiddle approach via the fallback cache.
		twiddles := getTwiddlesFallback(N)
		for n := 8; n < N; n <<= 1 {
			m := n << 1
			step := N / m
			for o := 0; o < N; o += m {
				for k := 0; k < n; k++ {
					i := k + o
					f := twiddles[k*step] * x[i+n]
					x[i], x[i+n] = x[i]+f, x[i]-f
				}
			}
		}
	}
}

// ─── Inverse FFT ─────────────────────────────────────────────────────────────

// ifft128 uses the identity  IFFT(x) = conj(FFT(conj(x))) / N.
//
// This replaces the original reversal loop, which touched N/2 non-sequential
// element pairs and was cache-hostile at 4096/8192 elements. Two sequential
// passes (conjugate in, conjugate+scale out) are strictly more cache-friendly.
func ifft128(x []complex128) {
	// Pass 1: conjugate input
	for i := range x {
		x[i] = complex(real(x[i]), -imag(x[i]))
	}

	fft128(x)

	// Pass 2: conjugate output and scale
	invN := 1.0 / float64(len(x))
	for i := range x {
		x[i] = complex(real(x[i])*invN, -imag(x[i])*invN)
	}
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// IsPow2 reports whether N is a positive power of two.
func IsPow2(N int) bool {
	return N > 0 && (uint64(N)&uint64(N-1)) == 0
}

func checkLength(ctx string, N int) error {
	if !IsPow2(N) {
		return fmt.Errorf("%s: length must be a power of 2, got %d", ctx, N)
	}
	return nil
}

// ─── Fallback twiddle cache (non-convolver sizes) ─────────────────────────────
//
// Indexed by log2(N) so lookup is O(1) with no hashing.
// sync.Once guarantees each entry is computed at most once without holding a
// lock on every read — unlike the original RWMutex + map approach.

var (
	twiddleFallback     [33][]complex128
	twiddleFallbackOnce [33]sync.Once
)

func getTwiddlesFallback(N int) []complex128 {
	idx := bits.Len64(uint64(N)) - 1 // log2(N)
	twiddleFallbackOnce[idx].Do(func() {
		tw := make([]complex128, N)
		for k := 0; k < N; k++ {
			s, c := math.Sincos(-2 * math.Pi * float64(k) / float64(N))
			tw[k] = complex(c, s)
		}
		twiddleFallback[idx] = tw
	})
	return twiddleFallback[idx]
}
