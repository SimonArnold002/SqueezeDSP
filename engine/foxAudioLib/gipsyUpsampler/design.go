// Package: github.com/Foxenfurter/foxAudioLib/gipsyUpsampler
// filename design.go
//
// The filters, designed when a stream starts: Kaiser-windowed sincs (Kaiser, Half-band, Slow roll-off), Gaussian-
// windowed sincs (Gaussian), a positive kernel (Non-ringing), the minimum phase from the cepstrum, and the
// intermediate phase as a product of a linear-phase and a minimum-phase half. Each family's widths and character:
// ledger FAMILY STUDY (MATLAB, 2026-11-06).
package gipsyUpsampler

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
// on it keeps the linear one's response exactly (a half designed on its own at half the depth, as Kaiser's DSD
// integrator's are, sits differently across the transition band: FAMILY STUDY, Gaussian short at 20 kHz -7.7 dB
// against linear's -3.0). powHalf with b 0.5, its limit stopDB/2 + 0.5 (85.5 dB), no floor: as before powHalf.
func sqrtHalf(h []float64, fst, stopDB float64) []float64 {
	return powHalf(h, 0.5, fst, 0.5*(stopDB+1), 0)
}

// powHalf: the linear-phase filter whose magnitude is h's to the power b (0 < b < 1; h as sqrtHalf's), cut to the
// fewest taps that keep it limitDB down from fst (a share of the rate), found by halving with the stop band measured
// at each try: a power of the stop band's sign-changing lobes has long faint tails, so a bound on the tails cut (tried
// first, for the square root) kept nearly all of them (Gaussian long: 154,269 taps for its linear's 7,003). The square
// root takes math.Sqrt, as it did before powHalf. floorDB > 0: the magnitude never under floorDB below the limit (the
// minimum part of a linear-leaning split: zero across each negative stop band lobe, a 20% part rang long once in
// minimum phase, Gaussian long int80 80 ms after the peak above -100 dB, its intermediate 28; with the floor 22).
func powHalf(h []float64, b, fst, limitDB, floorDB float64) []float64 {
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
	low := 0.0
	if floorDB > 0 {
		low = math.Pow(10, -(limitDB+floorDB)/20) * math.Pow(math.Max(real(x[0]), 0), b)
	}
	for i := range x {
		if b == 0.5 && floorDB == 0 {
			x[i] = complex(math.Sqrt(math.Max(real(x[i]), 0)), 0)
		} else {
			x[i] = complex(math.Max(math.Pow(math.Max(real(x[i]), 0), b), low), 0)
		}
	}
	fft(x, true)
	limit := math.Pow(10, -limitDB/20)
	return cutTo(x, fst, min(n/2, 4*m), func(y []complex128, f0 int, dc float64) bool {
		for i := f0; i <= len(y)/2; i++ {
			if cmplx.Abs(y[i]) > limit*math.Abs(dc) {
				return false
			}
		}
		return true
	})
}

// cutTo: the fewest taps 2k+1 (k up to most) of the zero-phase filter x (a circular impulse, centred on 0) that pass
// ok, given the cut's spectrum on a grid of at least 16 points a tap, the first bin at or past fst and the taps' sum
// (summed in order, as sqrtHalf's cut always has); found by halving.
func cutTo(x []complex128, fst float64, most int, ok func(y []complex128, f0 int, dc float64) bool) []float64 {
	n := len(x)
	cut := func(k int) []float64 {
		half := make([]float64, 2*k+1)
		for i := range half {
			half[i] = real(x[(i-k+n)%n])
		}
		return half
	}
	passes := func(k int) bool {
		half := cut(k)
		y := make([]complex128, nextPow2(16*len(half)))
		var dc float64
		for i, v := range half {
			y[i] = complex(v, 0)
			dc += v
		}
		fft(y, false)
		return ok(y, int(math.Ceil(fst*float64(len(y)))), dc)
	}
	lo, hi := 0, most
	for hi-lo > 1 {
		if k := (lo + hi) / 2; passes(k) {
			hi = k
		} else {
			lo = k
		}
	}
	return cut(hi)
}

// leanLinear: the linear part of a linear-leaning split, given its minimum part r (the minimum phase of powHalf's
// floored part 1 - b): the linear-phase filter whose magnitude is h's over r's, so the product has h's magnitude
// whatever r's own cut left in it (a part of 20% or 30% cut to its 34-51 dB stop band alone carries 0.001 to 0.012
// dB of ripple; held to its full response instead, its kinks at the floor ran it to 4x h's length, Kaiser short int80
// 2,823 taps: ledger `A LINEAR-LEANING INTERMEDIATE`). Its phase is linear, so the split's phase is r's. Cut to the
// fewest taps that keep the product stopDB + 0.5 down from fst, r's response read on the same grid. r's magnitude is
// read no lower than floor (against its gain at DC): r's cut, once in minimum phase, dips under its own floor in the
// stop band, and h over a dip blew the part up (Half-band int70: 8,457 taps, still 165.2 dB); where it binds the
// product is under h, and it never binds in the pass or transition bands (h would be under -190 dB there).
func leanLinear(h, r []float64, fst, stopDB, floor float64) []float64 {
	m := (len(h) - 1) / 2
	n := nextPow2(16 * max(len(h), len(r)))
	x := make([]complex128, n)
	for i := 0; i <= m; i++ {
		x[i] = complex(h[m+i], 0)
		if i > 0 {
			x[n-i] = complex(h[m-i], 0)
		}
	}
	fft(x, false)
	rn := spectrum(r, n)
	least := floor * cmplx.Abs(rn[0])
	for i := range x {
		x[i] = complex(math.Max(real(x[i]), 0)/math.Max(cmplx.Abs(rn[i]), least), 0)
	}
	fft(x, true)
	limit := math.Pow(10, -(stopDB+0.5)/20)
	return cutTo(x, fst, n/2-1, func(y []complex128, f0 int, dc float64) bool {
		ry := spectrum(r, len(y))
		at := math.Abs(dc) * cmplx.Abs(ry[0])
		for i := f0; i <= len(y)/2; i++ {
			if cmplx.Abs(y[i])*cmplx.Abs(ry[i]) > limit*at {
				return false
			}
		}
		return true
	})
}

// spectrum: r's response on an n-point grid (r longer than n folds, as a circular response does).
func spectrum(r []float64, n int) []complex128 {
	y := make([]complex128, n)
	for i, v := range r {
		y[i%n] += complex(v, 0)
	}
	fft(y, false)
	return y
}

// floorUnder: how far (dB) under its stop band limit the minimum part's floor sits: room for the cut, which a floor at
// the limit itself left none of (Kaiser long int70: 20,863 taps at 0.5 dB over it; 9,175 here, 3 dB 9,671).
const floorUnder = 6.0

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

// shaped: stage A's prototype for a choice, at rate fs (twice the lower rate), between its edges, gain g. Kaiser's
// linear and minimum as since 0.1.72.33 (kaiserShaped); its intermediates, as every other family's, by phased.
func shaped(c Choice, fs, fp, fst, stopDB, g float64) []float64 {
	switch {
	case c.Family == NonRinging:
		return nonRinging(fs, fp, g)
	case c.Family == Gaussian:
		return phased(c.Phase, func(sdb, g float64) []float64 { return gaussLowpass(fs, fp, fst, sdb, g) }, fst/fs, stopDB, halfMargin, g)
	case c.Family != Kaiser:
		return phased(c.Phase, func(sdb, g float64) []float64 { return kaiserLowpass(fs, fp, fst, sdb, g) }, fst/fs, stopDB, halfMargin, g)
	case c.Phase.linShare() > 0:
		// split from Kaiser's own linear design (kaiserShaped's), so it keeps that one's magnitude: kaiserLowpass's own
		// 2.5 dB margin (171.9 dB measured) leaves each part room; from a design halfMargin deeper they sat 0.1 to 1.9 dB
		// off Kaiser Linear across the transition band
		return phased(c.Phase, func(sdb, g float64) []float64 { return kaiserLowpass(fs, fp, fst, sdb, g) }, fst/fs, stopDB, 0, g)
	}
	return kaiserShaped(c.Phase, fs, fp, fst, stopDB, g)
}

// phased: lowpass(stopDB + margin, gain) in the phase asked for; an intermediate as a linear-phase part times a
// minimum-phase part sharing the linear filter's magnitude: Intermediate the square-root halves (sqrtHalf, as since
// 0.1.72.44), Int80 and Int70 the minimum phase of the floored part 1 - b (powHalf) and the linear part that makes up
// the rest of the magnitude exactly (leanLinear). One design under all of them, so they have one magnitude. fst: the
// stop band's edge, a share of the rate. margin: halfMargin for the families after Kaiser (their phases all from it),
// 0 for Kaiser's intermediates (its linear and minimum are kaiserShaped's).
func phased(p Phase, lowpass func(stopDB, g float64) []float64, fst, stopDB, margin, g float64) []float64 {
	if b := p.linShare(); b > 0 {
		h0 := lowpass(stopDB+margin, 1)
		// Intermediate: the square root's halves, as before. Otherwise the minimum part powHalf's floored 1 - b, cut to its
		// share of stopDB + 1 (as the halves are, stopDB/2 + 0.5 each), and the linear part h's magnitude over that one's
		// (leanLinear), so the product is h's magnitude and r's phase.
		if b != 0.5 {
			restDB := (1 - b) * (stopDB + 1)
			r := minimumPhase(powHalf(h0, 1-b, fst, restDB, floorUnder))
			h := convolve(leanLinear(h0, r, fst, stopDB, math.Pow(10, -(restDB+floorUnder)/20)), r)
			for i := range h {
				h[i] *= g
			}
			return h
		}
		lin := sqrtHalf(h0, fst, stopDB)
		rest := lin
		h := convolve(lin, minimumPhase(rest))
		for i := range h {
			h[i] *= g
		}
		return h
	}
	if p == Minimum {
		return minimumPhase(lowpass(stopDB+margin, g))
	}
	return lowpass(stopDB+margin, g)
}

// kaiserShaped: Kaiser's lowpass in the phase asked for, as since 0.1.72.33: the upsampler's linear and minimum, and
// the DSD integrator's three (Lowpass). Intermediate: a linear-phase half times the minimum phase of another, each
// with half the attenuation in dB, so the product meets stopDB, at about the linear one's length (darker than linear
// across the transition band: FAMILY STUDY, CD short at 21 kHz -11.0 dB against -5.3). The upsampler's Kaiser
// intermediate left it for phased's square-root halves on 2026-11-15 (Simon: "Rebuild on even split"); the integrator
// keeps it (not asked).
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

// Lowpass (the native DSD round, 2026-11-07: gipsyDSDReader's integrator at the DSD rate): Kaiser's lowpass at rate fs in
// the phase asked for (Linear, Intermediate or Minimum), as kaiserShaped designs it (the converter's own until
// 2026-11-15; its intermediate since then phased's), and its delay at DC in samples (delayOf).
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
