package gipsyDSDReader

import (
	"fmt"
	"math"
	"math/cmplx"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The native decode (the native DSD round, 2026-11-07): the bits as +-1 at the DSD rate through the integrator, its
// delay removed, nothing decimated.

var nativeShapes = []string{"lin", "int", "min", "nonring"}

// TestNativeIsTheIntegratorOnTheBits: every frame is the integrator (the taps Open designed) run over the bits as +-1,
// frame k the output at bit k + delay, the integrator's history before the file's start zero (DSD silence's mean); one
// frame a bit. Every shape, with no cutoff (stage 1's band), 30 and 50 kHz, at DSD64 and DSD128.
func TestNativeIsTheIntegratorOnTheBits(t *testing.T) {
	dir := t.TempDir()
	for _, rate := range []int{dsd64, dsd128} {
		fx := makeFixture(rate, 0.05, []float64{1000, 2500}, 0.5)
		path := filepath.Join(dir, fmt.Sprintf("n%d.dsf", rate))
		writeDSF(t, path, rate, fx.bits, int64(len(fx.x[0])))
		for _, shape := range nativeShapes {
			for _, cut := range []float64{0, 30000, 50000} {
				r := &Reader{Filename: path, Native: true, Integrator: shape, Cutoff: cut}
				got := decode(t, r)
				if r.OutRate != rate || r.Frames != int64(len(fx.x[0])) {
					t.Fatalf("%d %s %.0f: OutRate %d, Frames %d; want %d, %d", rate, shape, cut, r.OutRate, r.Frames, rate, len(fx.x[0]))
				}
				// the same frames whatever the reads: a byte at a time (TargetFrameSize 8)
				if cut == 30000 {
					one := decode(t, &Reader{Filename: path, Native: true, Integrator: shape, Cutoff: cut, TargetFrameSize: 8})
					for c := range got {
						if d := maxDiff(one[c], got[c]); len(one[c]) != len(got[c]) || d != 0 {
							t.Fatalf("%d %s: read a byte at a time, channel %d: %d frames, %g off", rate, shape, c, len(one[c]), d)
						}
					}
				}
				h, d := r.h2, int(r.delay)
				for c := range got {
					x := make([]float64, len(h)) // the history before the start
					lead := len(x)
					for _, b := range fx.bits[c] { // the bits as +-1, the oldest (the MSB) first: worked out here, not bitsPM
						for t := 7; t >= 0; t-- {
							x = append(x, float64(int(b>>uint(t)&1)*2-1))
						}
					}
					worst := 0.0
					for k := 0; k < len(got[c]) && k+d < len(fx.x[c]); k++ {
						v := 0.0
						for i, tap := range h {
							v += tap * x[lead+k+d-i]
						}
						worst = math.Max(worst, math.Abs(v-got[c][k]))
					}
					if worst > 1e-9 {
						t.Errorf("DSD %d %s cutoff %.0f channel %d: frames differ from the integrator on the bits by %g", rate, shape, cut, c, worst)
					}
				}
			}
		}
	}
}

// TestNativeLevelAndBand: a 0.5 modulation sine reads -6.02 dBFS at 1 kHz on every shape (unity), the Kaiser shapes are
// flat at 20 kHz against what the bitstream holds there (within 0.002 dB, as the DXD route), and non-ringing is 3 dB
// down at its cutoff (the Gaussian's own response at 20 kHz below it).
func TestNativeLevelAndBand(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lv.dsf")
	fx := makeFixture(dsd64, 0.25, []float64{1000, 20000}, 0.5)
	writeDSF(t, path, dsd64, fx.bits, int64(len(fx.x[0])))
	skip := dsd64 / 20
	for _, shape := range nativeShapes {
		for _, cut := range []float64{30000, 50000} {
			got := decode(t, &Reader{Filename: path, Native: true, Integrator: shape, Cutoff: cut})
			// non-ringing: less the Gaussian's own response, exp(-ln(sqrt 2) (f/cut)^2), at f
			gauss := func(f float64) float64 {
				if shape != "nonring" {
					return 0
				}
				return 20 * math.Log10(math.Exp(-math.Log(math.Sqrt2)*(f/cut)*(f/cut)))
			}
			if l := levelDB(got[0], dsd64, 1000, skip); math.Abs(l-(-6.0206+gauss(1000))) > 0.002 {
				t.Errorf("%s %.0f: 1 kHz at %.4f dBFS, want %.4f", shape, cut, l, -6.0206+gauss(1000))
			}
			// over whole cycles, frames and bits alike (a frame is a bit)
			n := (len(got[1]) - skip) / period(dsd64, 20000) * period(dsd64, 20000)
			want := 20 * math.Log10(2*abs(bitsDFT(fx.bits[1], dsd64, 20000, skip, skip+n)))
			at20 := 20 * math.Log10(2*abs(dft(got[1][:skip+n], dsd64, 20000, skip)))
			if shape == "nonring" {
				want += gauss(20000)
				if math.Abs(at20-want) > 0.01 {
					t.Errorf("nonring %.0f: 20 kHz at %.4f dB, the Gaussian's %.4f", cut, at20, want)
				}
				continue
			}
			if math.Abs(at20-want) > 0.002 {
				t.Errorf("%s %.0f: 20 kHz at %.4f dB, the bitstream holds %.4f", shape, cut, at20, want)
			}
		}
	}
}

func abs(c complex128) float64 { return math.Hypot(real(c), imag(c)) }

// TestGaussianIsNonRinging: every tap positive and symmetric (no ringing, a step never overshoots, a whole-sample delay),
// unity at DC, 3 dB down at f3 within 0.001 dB, at each DSD rate and cutoff the pages offer.
func TestGaussianIsNonRinging(t *testing.T) {
	for _, fs := range []float64{dsd64, dsd128, 4 * dsd64, 8 * dsd64} {
		for _, f3 := range []float64{24000, 30000, 50000, 90000} {
			h, d := gaussian(fs, f3)
			if len(h)%2 != 1 || d != float64((len(h)-1)/2) {
				t.Fatalf("%.0f/%.0f: %d taps, delay %v", fs, f3, len(h), d)
			}
			sum, re := 0.0, 0.0
			k := (len(h) - 1) / 2
			for i, v := range h {
				if v <= 0 || v != h[len(h)-1-i] {
					t.Fatalf("%.0f/%.0f: tap %d is %g (its mirror %g)", fs, f3, i, v, h[len(h)-1-i])
				}
				sum += v
				re += v * math.Cos(2*math.Pi*f3/fs*float64(i-k))
			}
			if math.Abs(sum-1) > 1e-12 {
				t.Errorf("%.0f/%.0f: DC gain %v", fs, f3, sum)
			}
			if db := 20 * math.Log10(re); math.Abs(db+3.0103) > 0.001 {
				t.Errorf("%.0f/%.0f: %.4f dB at f3", fs, f3, db)
			}
		}
	}
}

// TestNativeSeekAndUntil: as TestSeekAndUntil on the native decode: a seek's frames and --until's are the full
// decode's to the bit, head and tail make up the whole, every shape, at DSD64 and DSD128, DSF and DSDIFF.
func TestNativeSeekAndUntil(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		rate  int
		cut   float64
		shape string
		ext   string
	}{{dsd64, 30000, "lin", "dsf"}, {dsd64, 0, "min", "dff"}, {dsd128, 50000, "int", "dsf"}, {dsd64, 24000, "nonring", "dsf"}, {dsd128, 0, "nonring", "dff"}} {
		fx := makeFixture(tc.rate, 0.3, []float64{1000, 2500}, 0.5)
		path := filepath.Join(dir, "s."+tc.ext)
		if tc.ext == "dsf" {
			writeDSF(t, path, tc.rate, fx.bits, int64(len(fx.x[0])))
		} else {
			writeDFF(t, path, tc.rate, fx.bits, "DSD ")
		}
		full := decode(t, &Reader{Filename: path, Native: true, Integrator: tc.shape, Cutoff: tc.cut})
		for _, us := range []int{3, 61700, 151030, 270011} { // not on a byte, a DSF block or an FFT block
			at := time.Duration(us) * time.Microsecond
			k := int(math.Round(at.Seconds() * float64(tc.rate)))
			head := decode(t, &Reader{Filename: path, Native: true, Integrator: tc.shape, Cutoff: tc.cut, EndTime: at})
			tail := decode(t, &Reader{Filename: path, Native: true, Integrator: tc.shape, Cutoff: tc.cut, StartTime: at})
			if len(head[0]) != k || len(head[0])+len(tail[0]) != len(full[0]) {
				t.Fatalf("%s %d %s at %v: head %d (want %d) + tail %d != full %d", tc.ext, tc.rate, tc.shape, at, len(head[0]), k, len(tail[0]), len(full[0]))
			}
			for c := range full {
				if d := maxDiff(head[c], full[c][:k]); d != 0 {
					t.Errorf("%s %d %s at %v channel %d: --until's frames differ from the full decode by %g", tc.ext, tc.rate, tc.shape, at, c, d)
				}
				if d := maxDiff(tail[c], full[c][k:]); d != 0 {
					t.Errorf("%s %d %s at %v channel %d: the seek's frames differ from the full decode by %g", tc.ext, tc.rate, tc.shape, at, c, d)
				}
			}
		}
	}
}

// TestNativeStateJoinIsOneDecode: as TestStateJoinIsOneDecode on the native decode: three files cut inside bytes'
// blocks, each going on from the last one's State, are the whole stream decoded and played out the same way, to the
// bit; played out, Frames + Span.
func TestNativeStateJoinIsOneDecode(t *testing.T) {
	dir := t.TempDir()
	fx := makeFixture(dsd64, 0.3, []float64{1000, 1700}, 0.4)
	nb := len(fx.bits[0])
	cuts := []int{0, nb*3/10 + 13, nb*7/10 + 5, nb}
	whole := filepath.Join(dir, "whole.dsf")
	writeDSF(t, whole, dsd64, fx.bits, int64(len(fx.x[0])))
	var parts []string
	for k := 0; k < 3; k++ {
		p := filepath.Join(dir, fmt.Sprintf("t%d.dsf", k+1))
		writeDSF(t, p, dsd64, [][]byte{fx.bits[0][cuts[k]:cuts[k+1]], fx.bits[1][cuts[k]:cuts[k+1]]}, int64(cuts[k+1]-cuts[k])*8)
		parts = append(parts, p)
	}
	for _, shape := range nativeShapes {
		for _, cut := range []float64{0, 30000} {
			ref, _ := decodeOn(t, &Reader{Filename: whole, Native: true, Integrator: shape, Cutoff: cut}, nil, false)
			joined := make([][]float64, 2)
			var state []byte
			for k, p := range parts {
				got, next := decodeOn(t, &Reader{Filename: p, Native: true, Integrator: shape, Cutoff: cut}, state, k < 2)
				for c := range got {
					joined[c] = append(joined[c], got[c]...)
				}
				state = next
			}
			for c := range ref {
				if len(joined[c]) != len(ref[c]) {
					t.Fatalf("%s %.0f: channel %d joined %d frames, whole %d", shape, cut, c, len(joined[c]), len(ref[c]))
				}
				if d := maxDiff(joined[c], ref[c]); d != 0 {
					t.Fatalf("%s %.0f: channel %d joined differs from the whole by %g", shape, cut, c, d)
				}
			}
			r := &Reader{Filename: whole, Native: true, Integrator: shape, Cutoff: cut}
			if err := r.Open(); err != nil {
				t.Fatal(err)
			}
			if int64(len(ref[0])) != r.Frames+r.Span() {
				t.Errorf("%s %.0f: played out to %d frames, Frames %d + Span %d", shape, cut, len(ref[0]), r.Frames, r.Span())
			}
			// the play-out reaches the integrator's whole tail
			if r.Span() < int64(len(r.h2))-r.delay {
				t.Errorf("%s %.0f: Span %d short of the integrator's reach past the end, %d", shape, cut, r.Span(), int64(len(r.h2))-r.delay)
			}
			r.Close()
		}
	}
}

// TestNativeStateRefused: a native decode never goes on from a DXD one or from another integrator's, nor a DXD one
// from a native one: each names what differs.
func TestNativeStateRefused(t *testing.T) {
	dir := t.TempDir()
	fx := makeFixture(dsd64, 0.1, []float64{1000, 1700}, 0.4)
	a, b := filepath.Join(dir, "a.dsf"), filepath.Join(dir, "b.dsf")
	writeDSF(t, a, dsd64, fx.bits, int64(len(fx.x[0])))
	writeDSF(t, b, dsd64, fx.bits, int64(len(fx.x[0])))
	_, native := decodeOn(t, &Reader{Filename: a, Native: true, Integrator: "min"}, nil, true)
	_, dxd := decodeOn(t, &Reader{Filename: a, OutRate: 352800}, nil, true)
	for _, tc := range []struct {
		name  string
		r     *Reader
		state []byte
		want  string
	}{
		{"native from DXD", &Reader{Filename: b, Native: true, Integrator: "min"}, dxd, "DSD 2822400 to 352800"},
		{"DXD from native", &Reader{Filename: b, OutRate: 352800}, native, "to 2822400"},
		{"another integrator", &Reader{Filename: b, Native: true, Integrator: "lin"}, native, `integrator "min"`},
	} {
		if err := tc.r.Open(); err != nil {
			t.Fatal(err)
		}
		err := tc.r.SetState(tc.state)
		tc.r.Close()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want a refusal naming %q", tc.name, err, tc.want)
		}
	}
}

// TestNativeRefused: a cutoff out of range, a rate other than the DSD rate, an integrator not offered.
func TestNativeRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "r.dsf")
	fx := makeFixture(dsd64, 0.02, []float64{1000}, 0.5)
	writeDSF(t, path, dsd64, fx.bits, int64(len(fx.x[0])))
	for _, tc := range []struct {
		r    *Reader
		want string
	}{
		{&Reader{Filename: path, Native: true, Cutoff: 15000}, "cutoff 15000"},
		{&Reader{Filename: path, Native: true, Cutoff: 70000}, "cutoff 70000"},
		{&Reader{Filename: path, Native: true, OutRate: 352800}, "at the DSD rate"},
		{&Reader{Filename: path, Native: true, Integrator: "slow"}, `integrator "slow"`},
	} {
		err := tc.r.Open()
		tc.r.Close()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: %v, want an error naming %q", tc.r, err, tc.want)
		}
	}
	// the rate the decode is at, given: taken
	r := &Reader{Filename: path, Native: true, OutRate: dsd64}
	if err := r.Open(); err != nil {
		t.Errorf("OutRate at the DSD rate refused: %v", err)
	}
	r.Close()
}

// TestNativeIntegratorResponse: the integrator Open designs, every shape at DSD64 and DSD256 and every cutoff the pages
// offer. Linear, intermediate and minimum: flat to 20 kHz within 0.001 dB (intermediate 0.003, as the converter's) and
// 149 dB down from the stop edge to the Nyquist (as the DXD route's stage 2), the stop edge the DXD route's for the
// cutoff (none: 100 kHz). Non-ringing: 3 dB
// down at the cutoff (none: 90 kHz), within 0.01 dB of the Gaussian's own response at 20 kHz, every tap positive.
func TestNativeIntegratorResponse(t *testing.T) {
	dir := t.TempDir()
	for _, rate := range []int{dsd64, 4 * dsd64} {
		path := filepath.Join(dir, fmt.Sprintf("r%d.dsf", rate))
		fx := makeFixture(rate, 0.01, []float64{1000}, 0.5)
		writeDSF(t, path, rate, fx.bits, int64(len(fx.x[0])))
		for _, shape := range nativeShapes {
			for _, cut := range []float64{24000, 30000, 50000, 0} {
				r := &Reader{Filename: path, Native: true, Integrator: shape, Cutoff: cut}
				if err := r.Open(); err != nil {
					t.Fatal(err)
				}
				r.Close()
				h := r.h2
				at := func(f float64) float64 { // the magnitude at f, dB
					var re, im float64
					for i, v := range h {
						s, c := math.Sincos(2 * math.Pi * f / float64(rate) * float64(i))
						re, im = re+v*c, im-v*s
					}
					return 20 * math.Log10(math.Hypot(re, im))
				}
				stop, f3 := 100000.0, 90000.0
				if cut > 0 {
					stop, f3 = cut+math.Min(2*(cut-20000), 20000)/2, cut
				}
				if shape == "nonring" {
					want := 20 * math.Log10(math.Exp(-math.Log(math.Sqrt2)*(20000/f3)*(20000/f3)))
					if d := at(f3); math.Abs(d+3.0103) > 0.001 {
						t.Errorf("%d nonring %.0f: %.4f dB at %.0f Hz, want -3.01", rate, cut, d, f3)
					}
					if d := at(20000); math.Abs(d-want) > 0.01 {
						t.Errorf("%d nonring %.0f: %.4f dB at 20 kHz, the Gaussian's %.4f", rate, cut, d, want)
					}
					for i, v := range h {
						if v <= 0 {
							t.Fatalf("%d nonring %.0f: tap %d is %g", rate, cut, i, v)
						}
					}
					continue
				}
				flat := 0.001
				if shape == "int" {
					flat = 0.003 // its two halves' ripple adds, as the converter's intermediate filters (TestStopBands)
				}
				for f := 0.0; f <= 20000; f += 500 {
					if d := at(f); math.Abs(d) > flat {
						t.Errorf("%d %s %.0f: %.5f dB at %.0f Hz, not flat", rate, shape, cut, d, f)
						break
					}
				}
				worst := -999.0
				for f := stop; f <= float64(rate)/2; f += float64(rate) / 2 / 4000 {
					worst = math.Max(worst, at(f))
				}
				if worst > -149 {
					t.Errorf("%d %s %.0f: %.1f dB from %.0f Hz, want -149", rate, shape, cut, worst, stop)
				}
			}
		}
	}
}

// TestNativeAlignment: frame k is the input at time k: the fixture's modulator passes its input with no delay (STF 1),
// so the decoded 1 kHz has the input sine's phase, within half a DSD sample for every shape (the linear ones exactly
// on a sample, the minimum and intermediate ones their delay at DC rounded to one).
func TestNativeAlignment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "al.dsf")
	fx := makeFixture(dsd64, 0.2, []float64{1000, 1000}, 0.5)
	writeDSF(t, path, dsd64, fx.bits, int64(len(fx.x[0])))
	for _, shape := range nativeShapes {
		got := decode(t, &Reader{Filename: path, Native: true, Integrator: shape, Cutoff: 30000})
		skip := dsd64 / 20
		dp := cmplx.Phase(dft(got[0], dsd64, 1000, skip)) - cmplx.Phase(dft(fx.x[0], dsd64, 1000, skip))
		dp = math.Remainder(dp, 2*math.Pi)
		if samples := dp / (2 * math.Pi * 1000) * dsd64; math.Abs(samples) > 0.5 {
			t.Errorf("%s: the decode is %.2f DSD samples off the input", shape, samples)
		}
	}
}

// TestNativeGain: the decode's gain is GainDB on top of unity, every shape (DSD to PCM's +6 dB; DSD out decodes at 0)
func TestNativeGain(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "g.dsf")
	fx := makeFixture(dsd64, 0.1, []float64{1000}, 0.25)
	writeDSF(t, path, dsd64, fx.bits, int64(len(fx.x[0])))
	for _, shape := range nativeShapes {
		a := decode(t, &Reader{Filename: path, Native: true, Integrator: shape})
		b := decode(t, &Reader{Filename: path, Native: true, Integrator: shape, GainDB: 6})
		if d := levelDB(b[0], dsd64, 1000, dsd64/20) - levelDB(a[0], dsd64, 1000, dsd64/20); math.Abs(d-6) > 1e-6 {
			t.Errorf("%s: GainDB 6 is %+.6f dB", shape, d)
		}
	}
}
