// Package: github.com/Foxenfurter/foxAudioLib/foxUpsampler
// filename design.go
//
// The filters, designed when a stream starts: Kaiser-windowed sincs (Kaiser, Half-band, Slow roll-off), Gaussian-
// windowed sincs (Gaussian), a positive kernel (Non-ringing), the minimum phase from the cepstrum, and the
// intermediate phase as a product of a linear-phase and a minimum-phase half. Each family's widths and character:
// ledger FAMILY STUDY (MATLAB, 2026-11-06).
package foxUpsampler

import (
	"math"
	"math/cmplx"
)

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

// kaiserLowpass: a Kaiser-windowed sinc at rate fs, flat to fp and at least stopDB down from fst (Hz), with
// gain g at DC. Odd length (a whole-sample delay, no forced zero at fs/2). Designed for stopDB + 2.5 by
// Kaiser's estimate plus 4%: Kaiser's own formulas fall 1-2 dB short at these depths (stage B at 147/20: -168
// for 170), and this meets stopDB for every filter here (TestStopBands, TestStageB).
func kaiserLowpass(fs, fp, fst, stopDB, g float64) []float64 {
	stopDB += 2.5
	dw := 2 * math.Pi * (fst - fp) / fs
	n := int(math.Ceil((stopDB-7.95)/(2.285*dw)*1.04)) + 1
	if n%2 == 0 {
		n++
	}
	beta := 0.1102 * (stopDB - 8.7)
	fc := (fp + fst) / 2 / fs // cycles per sample
	m := float64(n-1) / 2
	h := make([]float64, n)
	i0 := bessel0(beta)
	for i := range h {
		t := float64(i) - m
		s := 2 * fc
		if t != 0 {
			s = math.Sin(2*math.Pi*fc*t) / (math.Pi * t)
		}
		r := t / m
		h[i] = g * s * bessel0(beta*math.Sqrt(math.Max(0, 1-r*r))) / i0
	}
	return h
}

// minimumPhase: the minimum-phase filter with h's magnitude, by the folded real cepstrum (64x zero padding:
// its time aliasing stays far under the stop band), truncated to h's length.
func minimumPhase(h []float64) []float64 {
	n := nextPow2(64 * len(h))
	x := make([]complex128, n)
	for i, v := range h {
		x[i] = complex(v, 0)
	}
	fft(x, false)
	for i := range x {
		x[i] = complex(math.Log(math.Max(cmplx.Abs(x[i]), 1e-300)), 0)
	}
	fft(x, true)
	x[0] = complex(real(x[0]), 0)
	x[n/2] = complex(real(x[n/2]), 0)
	for i := 1; i < n/2; i++ {
		x[i] = complex(2*real(x[i]), 0)
	}
	for i := n/2 + 1; i < n; i++ {
		x[i] = 0
	}
	fft(x, false)
	for i := range x {
		x[i] = cmplx.Exp(x[i])
	}
	fft(x, true)
	out := make([]float64, len(h))
	for i := range out {
		out[i] = real(x[i])
	}
	return out
}

func convolve(a, b []float64) []float64 {
	out := make([]float64, len(a)+len(b)-1)
	for i, x := range a {
		for j, y := range b {
			out[i+j] += x * y
		}
	}
	return out
}

// gaussLowpass: a Gaussian-windowed sinc at rate fs, within gaussPassDB of flat to fp and stopDB down from fst, with
// gain g at DC. The window's spectrum is a Gaussian, so across the cutoff the response is an error function: the
// narrowest one whose two tails, gaussPassDB at fp and stopDB (+ gaussMargin) at fst, fit between the edges sets the
// window's width and the cutoff, with no search. The window ends where its truncation's leakage at fst, about w(T) /
// (2 pi T (fst - fc)), is as deep as the stop band. Odd length. Within 1-4% of the shortest MATLAB's search finds
// (FAMILY STUDY); TestStopBands holds every rate to stopDB.
func gaussLowpass(fs, fp, fst, stopDB, g float64) []float64 {
	ds := math.Pow(10, -(stopDB+gaussMargin)/20)
	dp := 1 - math.Pow(10, -gaussPassDB/20)
	xs, xp := math.Erfcinv(2*ds), math.Erfcinv(2*dp)
	sf := (fst - fp) / fs / (math.Sqrt2 * (xs + xp)) // the window's spectrum's sigma, cycles a sample
	fc := fst/fs - math.Sqrt2*sf*xs
	st := 1 / (2 * math.Pi * sf) // the window's sigma, samples
	T := 6 * st
	for i := 0; i < 4; i++ {
		T = st * math.Sqrt(2*math.Log(1/(ds*2*math.Pi*T*(fst/fs-fc))))
	}
	m := int(math.Ceil(T))
	h := make([]float64, 2*m+1)
	for i := range h {
		t := float64(i - m)
		s := 2 * fc
		if t != 0 {
			s = math.Sin(2*math.Pi*fc*t) / (math.Pi * t)
		}
		h[i] = g * s * math.Exp(-t*t/(2*st*st))
	}
	return h
}

// sqrtHalf: the linear-phase filter whose magnitude is the square root of h's (h linear phase, odd length, designed
// halfMargin dB deeper than stopDB): times its own minimum phase it has h's magnitude, so an intermediate phase built
// on it keeps the linear one's response exactly (a half designed on its own at half the depth, as Kaiser's are, sits
// differently across the transition band: FAMILY STUDY, Gaussian short at 20 kHz -7.7 dB against linear's -3.0). Cut
// to the fewest taps that keep the half stopDB/2 (+ 0.5) down from fst (a share of the rate), found by halving with
// the stop band measured at each try: the square root of the stop band's sign-changing lobes has long faint tails, so
// a bound on the tails cut (tried first) kept nearly all of them (Gaussian long: 154,269 taps for its linear's 7,003).
func sqrtHalf(h []float64, fst, stopDB float64) []float64 {
	m := (len(h) - 1) / 2
	n := nextPow2(16 * len(h))
	x := make([]complex128, n)
	for i := 0; i <= m; i++ {
		x[i] = complex(h[m+i], 0)
		if i > 0 {
			x[n-i] = complex(h[m-i], 0)
		}
	}
	fft(x, false)
	for i := range x {
		x[i] = complex(math.Sqrt(math.Max(real(x[i]), 0)), 0)
	}
	fft(x, true)
	cut := func(k int) []float64 {
		half := make([]float64, 2*k+1)
		for i := range half {
			half[i] = real(x[(i-k+n)%n])
		}
		return half
	}
	limit := math.Pow(10, -(stopDB/2+0.5)/20)
	stops := func(half []float64) bool {
		y := make([]complex128, nextPow2(16*len(half)))
		var dc float64
		for i, v := range half {
			y[i] = complex(v, 0)
			dc += v
		}
		fft(y, false)
		for i := int(math.Ceil(fst * float64(len(y)))); i <= len(y)/2; i++ {
			if cmplx.Abs(y[i]) > limit*math.Abs(dc) {
				return false
			}
		}
		return true
	}
	lo, hi := 0, min(n/2, 4*m)
	for hi-lo > 1 {
		if k := (lo + hi) / 2; stops(cut(k)) {
			hi = k
		} else {
			lo = k
		}
	}
	return cut(hi)
}

// nonRinging: every tap positive, so nothing rings and the step does not overshoot: a sampled Gaussian times a two-tap
// average (its zero at fs/2 nulls the images of the bass), as wide as puts f3 3 dB down, with gain g at DC. It stops
// nothing: the top octave's images come through a few dB down (FAMILY STUDY: a 20 kHz tone's at -4.5 dB from 44.1k).
func nonRinging(fs, f3, g float64) []float64 {
	kernel := func(s float64) []float64 {
		k := int(math.Ceil(6.5*s)) + 1
		w := make([]float64, 2*k+1)
		for i := range w {
			t := float64(i - k)
			w[i] = math.Exp(-t * t / (2 * s * s))
		}
		h := convolve(w, []float64{0.5, 0.5})
		lo, hi := 0, len(h) // the taps under 1e-12 of the peak go, alike at both ends
		for h[lo] < 1e-12*h[k] {
			lo++
		}
		for h[hi-1] < 1e-12*h[k] {
			hi--
		}
		return h[lo:hi]
	}
	at := func(h []float64) float64 {
		var re, im, sum float64
		for i, v := range h {
			s, c := math.Sincos(2 * math.Pi * f3 / fs * float64(i))
			re, im, sum = re+v*c, im-v*s, sum+v
		}
		return math.Hypot(re, im) / sum
	}
	want := math.Pow(10, -3.0/20)
	lo, hi := 0.01, 20.0
	for i := 0; i < 60; i++ {
		if s := (lo + hi) / 2; at(kernel(s)) < want {
			hi = s
		} else {
			lo = s
		}
	}
	h := kernel(lo)
	var sum float64
	for _, v := range h {
		sum += v
	}
	for i := range h {
		h[i] *= g / sum
	}
	return h
}

// shaped: stage A's prototype for a choice, at rate fs (twice the lower rate), between its edges, gain g.
func shaped(c Choice, fs, fp, fst, stopDB, g float64) []float64 {
	switch c.Family {
	case NonRinging:
		return nonRinging(fs, fp, g)
	case Gaussian:
		return phased(c.Phase, func(sdb, g float64) []float64 { return gaussLowpass(fs, fp, fst, sdb, g) }, fst/fs, stopDB, g)
	case HalfBand, Slow:
		return phased(c.Phase, func(sdb, g float64) []float64 { return kaiserLowpass(fs, fp, fst, sdb, g) }, fst/fs, stopDB, g)
	}
	return kaiserShaped(c.Phase, fs, fp, fst, stopDB, g)
}

// phased: lowpass(stopDB + halfMargin, gain) in the phase asked for; intermediate as its square-root half (sqrtHalf)
// times that half's minimum phase. One design under all three, so the three have one magnitude. fst: the stop band's
// edge, a share of the rate.
func phased(p Phase, lowpass func(stopDB, g float64) []float64, fst, stopDB, g float64) []float64 {
	switch p {
	case Minimum:
		return minimumPhase(lowpass(stopDB+halfMargin, g))
	case Intermediate:
		half := sqrtHalf(lowpass(stopDB+halfMargin, 1), fst, stopDB)
		h := convolve(half, minimumPhase(half))
		for i := range h {
			h[i] *= g
		}
		return h
	}
	return lowpass(stopDB+halfMargin, g)
}

// kaiserShaped: Kaiser's lowpass in the phase asked for, as since 0.1.72.33. Intermediate: a linear-phase half times
// the minimum phase of another, each with half the attenuation in dB, so the product meets stopDB, at about the
// linear one's length (darker than linear across the transition band: FAMILY STUDY, CD short at 21 kHz -11.0 dB
// against -5.3; kept, Simon's call).
func kaiserShaped(p Phase, fs, fp, fst, stopDB, g float64) []float64 {
	switch p {
	case Minimum:
		return minimumPhase(kaiserLowpass(fs, fp, fst, stopDB, g))
	case Intermediate:
		half := kaiserLowpass(fs, fp, fst, stopDB/2, 1)
		h := convolve(half, minimumPhase(half))
		for i := range h {
			h[i] *= g
		}
		return h
	}
	return kaiserLowpass(fs, fp, fst, stopDB, g)
}

// Lowpass (the native DSD round, 2026-11-07: foxDSDReader's integrator at the DSD rate): Kaiser's lowpass at rate fs in
// the phase asked for, as the converter's own (kaiserShaped), and its delay at DC in samples (delayOf).
func Lowpass(p Phase, fs, fp, fst, stopDB, g float64) ([]float64, float64) {
	h := kaiserShaped(p, fs, fp, fst, stopDB, g)
	return h, delayOf(h)
}

// delayOf: the group delay at DC, in samples: the impulse's centre, sum(n h[n]) / sum(h[n]). For a linear phase
// filter of length N, (N-1)/2.
func delayOf(h []float64) float64 {
	var s, sn float64
	for i, v := range h {
		s += v
		sn += float64(i) * v
	}
	return sn / s
}
