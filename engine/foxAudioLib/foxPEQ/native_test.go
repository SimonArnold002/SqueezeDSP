// SqueezeDSP fork (native DSD, 0.1.72.48): the PEQ designed at a DSD rate, where native DSD runs the DSP. Live on
// 0.1.72.47 a high shelf on a DSD64 track was dropped (the rate check stopped at 800 kHz): these pin that every DSD rate
// takes the filters, that each one there is the filter it is at the DXD route's 352.8 kHz, and that the impulse the
// engine convolves is that filter.
package foxPEQ

import (
	"math"
	"math/cmplx"
	"testing"
)

var dsdRates = []int{2822400, 5644800, 11289600, 22579200}

// response: the cascade's gain in dB at f Hz
func response(p *PEQFilter, f float64) float64 {
	z := cmplx.Exp(complex(0, -2*math.Pi*f/float64(p.SampleRate))) // z^-1
	h := complex(1, 0)
	for _, c := range p.FilterCoefficients {
		num := complex(c.B[0], 0) + complex(c.B[1], 0)*z + complex(c.B[2], 0)*z*z
		den := complex(c.A[0], 0) + complex(c.A[1], 0)*z + complex(c.A[2], 0)*z*z
		h *= num / den
	}
	return 20 * math.Log10(cmplx.Abs(h))
}

// impulseResponse: the impulse's gain in dB at f Hz, a direct DFT at one frequency
func impulseResponse(p *PEQFilter, f float64) float64 {
	var h complex128
	w := -2 * math.Pi * f / float64(p.SampleRate)
	for n, x := range p.Impulse {
		h += complex(x, 0) * cmplx.Exp(complex(0, w*float64(n)))
	}
	return 20 * math.Log10(cmplx.Abs(h))
}

type peqFilter struct {
	kind              string
	freq, gain, width float64
	slope             string
}

// Simon's filter (2026-11-07, live), and one of each kind the page offers
var filters = []peqFilter{
	{"highshelf", 2960, -4, 0.3, "slope"},
	{"peak", 30, 6, 5, "Q"},
	{"lowshelf", 100, 4, 0.7, "Q"},
	{"peak", 1000, -3, 1, "octave"},
	{"highpass", 20, 0, 0.707, "Q"},
	{"lowpass", 20000, 0, 0.707, "Q"},
	{"notch", 60, 0, 10, "Q"},
}

var freqs = []float64{20, 30, 60, 100, 1000, 2960, 10000, 20000}

func build(t *testing.T, rate int, f peqFilter) *PEQFilter {
	t.Helper()
	p := NewPEQFilter(rate, 15)
	if err := p.CalcBiquadFilter(f.kind, f.freq, f.gain, f.width, f.slope); err != nil {
		t.Fatalf("%d Hz, %v: %v", rate, f, err)
	}
	return &p
}

func TestNativePEQAtEveryDSDRate(t *testing.T) {
	for _, f := range filters {
		dxd := build(t, 352800, f)
		for _, rate := range dsdRates {
			p := build(t, rate, f)
			for _, hz := range freqs {
				a, b := response(dxd, hz), response(p, hz)
				// at the notch itself both are a deep null, different depths
				if f.kind == "notch" && hz == f.freq {
					if b > -60 {
						t.Errorf("%d Hz, %v: %.1f dB at the notch", rate, f, b)
					}
					continue
				}
				// the DXD design's own warping near 20 kHz: its lowpass is set at 20 kHz itself
				tol := 0.01
				if f.kind == "lowpass" && hz >= 10000 {
					tol = 0.05
				}
				if math.Abs(a-b) > tol {
					t.Errorf("%d Hz, %v at %.0f Hz: %.4f dB, at 352.8 kHz %.4f", rate, f, hz, b, a)
				}
			}
		}
	}
}

func TestNativePEQImpulseIsTheFilter(t *testing.T) {
	for _, f := range filters[:4] {
		p := build(t, 2822400, f)
		p.UpdateFilterLength()
		p.GenerateFilterImpulse()
		if len(p.Impulse) != p.FilterLength || p.FilterLength < 1024 {
			t.Fatalf("%v: impulse %d, length %d", f, len(p.Impulse), p.FilterLength)
		}
		for _, hz := range freqs {
			if d := math.Abs(impulseResponse(p, hz) - response(p, hz)); d > 0.01 {
				t.Errorf("%v at %.0f Hz: the impulse %.4f dB, the filter %.4f", f, hz, impulseResponse(p, hz), response(p, hz))
			}
		}
	}
}

func TestNativePEQLoudness(t *testing.T) {
	l := NewLoudness()
	l.PlaybackPhon = 60
	prof, err := l.DifferentialSPL(1.0)
	if err != nil {
		t.Fatal(err)
	}
	dxd := NewPEQFilter(352800, 15)
	if err := dxd.GenerateEQLoudnessFilter(prof); err != nil {
		t.Fatal(err)
	}
	p := NewPEQFilter(2822400, 15)
	if err := p.GenerateEQLoudnessFilter(prof); err != nil {
		t.Fatalf("loudness at DSD64: %v", err)
	}
	// 352.8 kHz's own warping near 20 kHz: 0.014 dB there (DSD64 within 0.0002 dB of DSD512, 96k 0.2 dB off)
	for _, hz := range freqs {
		tol := 0.01
		if hz >= 10000 {
			tol = 0.02
		}
		if d := math.Abs(response(&p, hz) - response(&dxd, hz)); d > tol {
			t.Errorf("loudness at %.0f Hz: %.4f dB, at 352.8 kHz %.4f", hz, response(&p, hz), response(&dxd, hz))
		}
	}
}

// Fox's check stands for every rate that is not a DSD rate
func TestNativePEQRefusesOtherRates(t *testing.T) {
	for _, rate := range []int{5000, 900000, 1411200, 3072000, 6144000} {
		p := NewPEQFilter(rate, 15)
		if err := p.CalcBiquadFilter("highshelf", 2960, -4, 0.3, "slope"); err == nil {
			t.Errorf("%d Hz taken", rate)
		}
	}
	for _, rate := range []int{44100, 352800, 768000} {
		p := NewPEQFilter(rate, 15)
		if err := p.CalcBiquadFilter("highshelf", 2960, -4, 0.3, "slope"); err != nil {
			t.Errorf("%d Hz refused: %v", rate, err)
		}
	}
}
