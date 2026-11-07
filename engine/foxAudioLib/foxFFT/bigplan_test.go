package foxFFT

// SqueezeDSP fork: plans and pooled work buffers above 8192 points give the same bits as Fox's paths did, which
// are kept here verbatim (only their plan lookups renamed to the fixed sizes he built) to compare against.

import (
	"math"
	"math/rand"
	"sync"
	"testing"
)

// foxGetPlan, foxGetRealPlan: Fox's lookups, plans up to 8192 points only.
func foxGetPlan(N int) *fftPlan {
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

func foxGetRealPlan(N int) *realFFTPlan {
	switch N {
	case RealFFTSize256:
		return &realPlan256
	case RealFFTSize512:
		return &realPlan512
	case RealFFTSize1024:
		return &realPlan1024
	case RealFFTSize2048:
		return &realPlan2048
	case RealFFTSize4096:
		return &realPlan4096
	case RealFFTSize8192:
		return &realPlan8192
	default:
		return nil
	}
}

// foxFft128 is Fox's fft128.
func foxFft128(x []complex128) {
	N := len(x)
	if N == 1 {
		return
	}
	if N == 2 {
		x[0], x[1] = x[0]+x[1], x[0]-x[1]
		return
	}
	permute(x)
	for i := 0; i < N; i += 4 {
		a, b, c, d := x[i], x[i+1], x[i+2], x[i+3]
		f := complex(imag(c)-imag(d), real(d)-real(c))
		x[i] = a + b + c + d
		x[i+1] = a - b + f
		x[i+2] = a - c + b - d
		x[i+3] = a - b - f
	}
	if N < 8 {
		return
	}
	for i := 0; i < N; i += 8 {
		u, v := x[i], x[i+4]
		x[i], x[i+4] = u+v, u-v
		u = x[i+1]
		r5, m5 := real(x[i+5]), imag(x[i+5])
		v = complex((r5+m5)*invsqrt2, (m5-r5)*invsqrt2)
		x[i+1], x[i+5] = u+v, u-v
		u = x[i+2]
		v = complex(imag(x[i+6]), -real(x[i+6]))
		x[i+2], x[i+6] = u+v, u-v
		u = x[i+3]
		r7, m7 := real(x[i+7]), imag(x[i+7])
		v = complex((m7-r7)*invsqrt2, (-r7-m7)*invsqrt2)
		x[i+3], x[i+7] = u+v, u-v
	}
	if N <= 8 {
		return
	}
	plan := foxGetPlan(N)
	if plan != nil {
		for si, tw := range plan.stageTwiddles {
			n := 8 << uint(si)
			m := n << 1
			for o := 0; o < N; o += m {
				xo := x[o : o+m]
				for k := 0; k < n; k++ {
					f := tw[k] * xo[k+n]
					xo[k], xo[k+n] = xo[k]+f, xo[k]-f
				}
			}
		}
	} else {
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

// foxIfft128 is Fox's ifft128 over foxFft128.
func foxIfft128(x []complex128) {
	for i := range x {
		x[i] = complex(real(x[i]), -imag(x[i]))
	}
	foxFft128(x)
	invN := 1.0 / float64(len(x))
	for i := range x {
		x[i] = complex(real(x[i])*invN, -imag(x[i])*invN)
	}
}

// foxRealFft, foxRealIfft are Fox's RealFftUnchecked and RealIfftUnchecked (their work buffer allocated: it is
// written in full before it is read either way).
func foxRealFft(in []float64, out []complex128) {
	N := len(in)
	M := N / 2
	plan := foxGetRealPlan(N)
	work := make([]complex128, M)
	for k := 0; k < M; k++ {
		work[k] = complex(in[2*k], in[2*k+1])
	}
	foxFft128(work)
	if plan != nil {
		realPostProcess(work, out, plan)
	} else {
		realPostProcessGeneric(work, out, N)
	}
}

func foxRealIfft(in []complex128, out []float64) {
	M := len(in) - 1
	N := M * 2
	plan := foxGetRealPlan(N)
	work := make([]complex128, M)
	if plan != nil {
		realPreProcessInverse(in, work, plan)
	} else {
		realPreProcessInverseGeneric(in, work, N)
	}
	foxIfft128(work)
	for k := 0; k < M; k++ {
		out[2*k] = real(work[k])
		out[2*k+1] = imag(work[k])
	}
}

func sameBits(a, b complex128) bool {
	return math.Float64bits(real(a)) == math.Float64bits(real(b)) && math.Float64bits(imag(a)) == math.Float64bits(imag(b))
}

// Plans built on first use from several goroutines at once (the engine's convolvers run one per channel): each
// gets the same bits as Fox's path (run with -race; first in this file, so the 65,536-point plan is not built yet).
func TestBigPlansConcurrent(t *testing.T) {
	in := make([]float64, 65536)
	for i := range in {
		in[i] = math.Cos(float64(i) * 0.37)
	}
	want := make([]complex128, len(in)/2+1)
	foxRealFft(in, want)
	var wg sync.WaitGroup
	bad := make(chan int, 8)
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got := make([]complex128, len(want))
			RealFftUnchecked(in, got)
			for k := range got {
				if !sameBits(got[k], want[k]) {
					bad <- g
					return
				}
			}
		}()
	}
	wg.Wait()
	close(bad)
	for g := range bad {
		t.Fatalf("goroutine %d: not Fox's bits", g)
	}
}

// TestBigPlansSameBits: real FFTs of 256 to 262,144 points and complex ones of 128 to 131,072, forward and back, the
// same bits as Fox's paths, twice each (the second time on a pooled buffer the first left dirty).
func TestBigPlansSameBits(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	for N := 256; N <= bigRealPlanMax; N <<= 1 {
		for round := range 2 {
			in := make([]float64, N)
			for i := range in {
				in[i] = rng.Float64()*2 - 1
			}
			got, want := make([]complex128, N/2+1), make([]complex128, N/2+1)
			RealFftUnchecked(in, got)
			foxRealFft(in, want)
			for k := range got {
				if !sameBits(got[k], want[k]) {
					t.Fatalf("real FFT of %d points (round %d): bin %d is %v, Fox's path %v", N, round, k, got[k], want[k])
				}
			}
			back, foxBack := make([]float64, N), make([]float64, N)
			RealIfftUnchecked(got, back)
			foxRealIfft(want, foxBack)
			for i := range back {
				if math.Float64bits(back[i]) != math.Float64bits(foxBack[i]) {
					t.Fatalf("real inverse FFT of %d points (round %d): sample %d is %v, Fox's path %v", N, round, i, back[i], foxBack[i])
				}
			}
		}
	}
	for N := 128; N <= bigPlanMax; N <<= 1 {
		x := make([]complex128, N)
		for i := range x {
			x[i] = complex(rng.Float64()*2-1, rng.Float64()*2-1)
		}
		got, want := append([]complex128(nil), x...), append([]complex128(nil), x...)
		FftUnchecked(got, false)
		foxFft128(want)
		for k := range got {
			if !sameBits(got[k], want[k]) {
				t.Fatalf("complex FFT of %d points: bin %d is %v, Fox's path %v", N, k, got[k], want[k])
			}
		}
		FftUnchecked(got, true)
		foxIfft128(want)
		for k := range got {
			if !sameBits(got[k], want[k]) {
				t.Fatalf("complex inverse FFT of %d points: sample %d is %v, Fox's path %v", N, k, got[k], want[k])
			}
		}
	}
}

// The plans exist from 16,384 points up to their caps and not beyond (a plan lost costs only time, so the bits
// above cannot tell): complex to 131,072, real to 262,144, each of its own size.
func TestBigPlansExist(t *testing.T) {
	for N := 16384; N <= 131072; N <<= 1 {
		if p := getPlan(N); p == nil || p.N != N {
			t.Fatalf("complex FFT of %d points: plan %v", N, p)
		}
	}
	for N := 16384; N <= 262144; N <<= 1 {
		if p := getRealPlan(N); p == nil || p.N != N || len(p.postTw) != N/2+1 {
			t.Fatalf("real FFT of %d points: no plan of its size", N)
		}
	}
	if getPlan(262144) != nil || getRealPlan(524288) != nil || getPlan(3*8192) != nil {
		t.Fatal("a plan beyond the caps, or for a length that is not a power of two")
	}
}

// The convolver's FFT at 176.4k and up (16,384 points, both ways) takes its work buffers from the pool: Fox's path
// allocated one per call.
func TestBigFFTDoesNotAllocate(t *testing.T) {
	in := make([]float64, 16384)
	out := make([]complex128, 8193)
	for i := range in {
		in[i] = math.Sin(float64(i))
	}
	if a := testing.AllocsPerRun(50, func() { RealFftUnchecked(in, out); RealIfftUnchecked(out, in) }); a >= 1 {
		t.Fatalf("%.1f allocations per forward and inverse FFT of 16,384 points: want the pooled buffers", a)
	}
}
