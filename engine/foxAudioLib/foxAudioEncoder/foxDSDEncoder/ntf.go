// Package: github.com/Foxenfurter/foxAudioLib/foxAudioEncoder/foxDSDEncoder
// filename ntf.go
//
// SqueezeDSP fork: the noise transfer functions (NTFs) of the 1-bit modulators. Designed here from first
// principles and the published methods; no code or coefficients from sox's sdm.c or any other modulator.
//
// Both kinds share the zeros: the roots of the Legendre polynomial of the order, scaled to the band edge,
// on the unit circle. For zeros near z=1 the in-band noise is about the integral over the band of the
// square of the monic polynomial with those roots, and the Legendre polynomial's is the least.
//   - standard: the poles of a Butterworth high-pass (bilinear transform) whose cutoff makes the gain at
//     half the sample rate the out-of-band gain asked for.
//   - CLANS (Kenney & Carley 1993, closed-loop analysis of noise shapers): the poles that leave the least
//     in-band noise while the sum of the NTF impulse response's absolute values stays at most a cap. Found
//     by a Nelder-Mead search from the standard poles.
//
// The engine runs the designs baked into tables.go: a CLANS search takes about a second. TestGenerateTables
// remakes that file from this code and checks each design in the modulator first (go test -run
// TestGenerateTables -args -update).
package foxDSDEncoder

import (
	"math"
	"math/cmplx"
	"sort"
)

// section is (1 + b1 z^-1 + b2 z^-2) / (1 + a1 z^-1 + a2 z^-2); the first of an odd order is first-order.
type section struct {
	b1, b2, a1, a2 float64
}

type ntf struct {
	order int
	wB    float64 // band edge, radians per sample
	zeros []complex128
	poles []complex128
}

func legendreRoots(n int) []float64 {
	roots := make([]float64, 0, n)
	for i := 1; i <= n; i++ {
		x := math.Cos(math.Pi * (float64(i) - 0.25) / (float64(n) + 0.5))
		for it := 0; it < 100; it++ {
			p0, p1 := 1.0, x
			for k := 2; k <= n; k++ {
				p0, p1 = p1, ((2*float64(k)-1)*x*p1-(float64(k)-1)*p0)/float64(k)
			}
			dx := p1 / (float64(n) * (x*p1 - p0) / (x*x - 1))
			x -= dx
			if math.Abs(dx) < 1e-16 {
				break
			}
		}
		if math.Abs(x) < 1e-15 {
			x = 0
		}
		roots = append(roots, x)
	}
	sort.Float64s(roots)
	return roots
}

func butterworthHighPassPoles(n int, oc float64) []complex128 {
	p := make([]complex128, n)
	for k := 1; k <= n; k++ {
		s := cmplx.Exp(complex(0, math.Pi*float64(2*k+n-1)/float64(2*n))) * complex(oc, 0)
		p[k-1] = (1 + s) / (1 - s)
	}
	return p
}

func gainAt(z, p []complex128, w float64) float64 {
	e := cmplx.Exp(complex(0, w))
	g := 1.0
	for i := range z {
		g *= cmplx.Abs(e-z[i]) / cmplx.Abs(e-p[i])
	}
	return g
}

// maxGain: the largest |H| over a 4096-point grid of [0, pi].
func (f ntf) maxGain() float64 {
	m := 0.0
	for i := 0; i <= 4096; i++ {
		m = math.Max(m, gainAt(f.zeros, f.poles, math.Pi*float64(i)/4096))
	}
	return m
}

// inBand: the share of white noise the NTF leaves in the band, (1/pi) * integral over [0, wB] of |H|^2.
func (f ntf) inBand() float64 {
	const m = 400
	s := 0.0
	for i := 0; i < m; i++ {
		w := f.wB * (float64(i) + 0.5) / m
		g := gainAt(f.zeros, f.poles, w)
		s += g * g
	}
	return s * f.wB / m / math.Pi
}

func legendreZeros(n int, wB float64) []complex128 {
	z := make([]complex128, n)
	for i, r := range legendreRoots(n) {
		z[i] = cmplx.Exp(complex(0, r*wB))
	}
	return z
}

// designStandard: the cutoff by bisection on the gain at z = -1, which rises with it (the largest gain
// over all frequencies does not: poles close to z=1 put a peak in the band).
func designStandard(n int, wB, obg float64) ntf {
	z := legendreZeros(n, wB)
	lo, hi := 1e-6, 50.0
	for it := 0; it < 200; it++ {
		mid := math.Sqrt(lo * hi)
		if gainAt(z, butterworthHighPassPoles(n, mid), math.Pi) > obg {
			hi = mid
		} else {
			lo = mid
		}
	}
	return ntf{order: n, wB: wB, zeros: z, poles: butterworthHighPassPoles(n, lo)}
}

// sections pairs each zero pair (by angle) with a pole pair (by angle); an odd order's zero at z=1 goes
// with the real pole.
func (f ntf) sections() []section {
	var zs []float64
	for _, z := range f.zeros {
		if a := cmplx.Phase(z); a >= 0 {
			zs = append(zs, a)
		}
	}
	sort.Float64s(zs)
	// the real pole of an odd order is the one nearest the axis; the rest pair as conjugates by count, not by a
	// threshold on the imaginary part (a pair close to the axis would otherwise read as two real poles)
	rest := append([]complex128(nil), f.poles...)
	var re []float64
	if f.order%2 == 1 {
		ri := 0
		for i, p := range rest {
			if math.Abs(imag(p)) < math.Abs(imag(rest[ri])) {
				ri = i
			}
		}
		re = append(re, real(rest[ri]))
		rest = append(rest[:ri], rest[ri+1:]...)
	}
	sort.Slice(rest, func(i, j int) bool { return imag(rest[i]) < imag(rest[j]) })
	up := append([]complex128(nil), rest[len(rest)/2:]...)
	sort.Slice(up, func(i, j int) bool { return cmplx.Phase(up[i]) < cmplx.Phase(up[j]) })
	var out []section
	zi := 0
	if f.order%2 == 1 {
		out = append(out, section{b1: -1, a1: -re[0]})
		zi = 1
	}
	for i, p := range up {
		th := zs[zi+i]
		out = append(out, section{b1: -2 * math.Cos(th), b2: 1, a1: -2 * real(p), a2: real(p)*real(p) + imag(p)*imag(p)})
	}
	return out
}

// l1: the sum of |h(n)| over the first N samples of the NTF's impulse response (h(0) = 1).
func l1(secs []section, N int) float64 {
	w1 := make([]float64, len(secs))
	w2 := make([]float64, len(secs))
	s := 0.0
	for n := 0; n < N; n++ {
		in := 0.0
		if n == 0 {
			in = 1
		}
		for k, c := range secs {
			w0 := in - c.a1*w1[k] - c.a2*w2[k]
			out := w0 + c.b1*w1[k] + c.b2*w2[k]
			w2[k], w1[k] = w1[k], w0
			in = out
		}
		s += math.Abs(in)
	}
	return s
}

func sigmoid(x float64) float64 { return 1 / (1 + math.Exp(-x)) }
func logit(y float64) float64   { return math.Log(y / (1 - y)) }

// the CLANS search's parameters: per complex pole pair a radius (logit of r/rmax) and an angle (logit
// over [0, pi/2]); an odd order's real pole as atanh(p/rmax).
func polesFromParams(x []float64, n int, rmax float64) []complex128 {
	var p []complex128
	j := 0
	for i := 0; i < n/2; i++ {
		c := cmplx.Rect(rmax*sigmoid(x[j]), math.Pi/2*sigmoid(x[j+1]))
		j += 2
		p = append(p, c, cmplx.Conj(c))
	}
	if n%2 == 1 {
		p = append(p, complex(rmax*math.Tanh(x[j]), 0))
	}
	return p
}

func paramsFromPoles(p []complex128, n int, rmax float64) []float64 {
	var up []complex128
	var re []float64
	for _, c := range p {
		if math.Abs(imag(c)) < 1e-12 {
			re = append(re, real(c))
		} else if imag(c) > 0 {
			up = append(up, c)
		}
	}
	sort.Slice(up, func(i, j int) bool { return cmplx.Phase(up[i]) < cmplx.Phase(up[j]) })
	var x []float64
	for _, c := range up {
		x = append(x, logit(math.Min(cmplx.Abs(c)/rmax, 0.999999)), logit(math.Min(cmplx.Phase(c)/(math.Pi/2), 0.999999)))
	}
	if n%2 == 1 {
		x = append(x, math.Atanh(math.Max(-0.999999, math.Min(0.999999, re[0]/rmax))))
	}
	return x
}

func nelderMead(f func([]float64) float64, x0 []float64, step float64, iters int) []float64 {
	n := len(x0)
	pts := make([][]float64, n+1)
	vals := make([]float64, n+1)
	for i := range pts {
		pts[i] = append([]float64(nil), x0...)
		if i > 0 {
			pts[i][i-1] += step
		}
		vals[i] = f(pts[i])
	}
	idx := make([]int, n+1)
	for it := 0; it < iters; it++ {
		for i := range idx {
			idx[i] = i
		}
		sort.SliceStable(idx, func(a, b int) bool { return vals[idx[a]] < vals[idx[b]] })
		np, nv := make([][]float64, n+1), make([]float64, n+1)
		for i, k := range idx {
			np[i], nv[i] = pts[k], vals[k]
		}
		pts, vals = np, nv
		if math.Abs(vals[n]-vals[0]) < 1e-13*(1+math.Abs(vals[0])) {
			break
		}
		c := make([]float64, n)
		for i := 0; i < n; i++ {
			for d := 0; d < n; d++ {
				c[d] += pts[i][d] / float64(n)
			}
		}
		at := func(t float64) []float64 {
			y := make([]float64, n)
			for d := range y {
				y[d] = c[d] + t*(pts[n][d]-c[d])
			}
			return y
		}
		xr := at(-1)
		fr := f(xr)
		switch {
		case fr < vals[0]:
			xe := at(-2)
			if fe := f(xe); fe < fr {
				pts[n], vals[n] = xe, fe
			} else {
				pts[n], vals[n] = xr, fr
			}
		case fr < vals[n-1]:
			pts[n], vals[n] = xr, fr
		default:
			xc := at(0.5)
			if fr < vals[n] {
				xc = at(-0.5)
			}
			if fc := f(xc); fc < math.Min(fr, vals[n]) {
				pts[n], vals[n] = xc, fc
			} else {
				for i := 1; i <= n; i++ {
					for d := range pts[i] {
						pts[i][d] = pts[0][d] + 0.5*(pts[i][d]-pts[0][d])
					}
					vals[i] = f(pts[i])
				}
			}
		}
	}
	best := 0
	for i := range vals {
		if vals[i] < vals[best] {
			best = i
		}
	}
	return pts[best]
}

// designCLANS: the least in-band noise with l1(h) <= cap and pole radius <= rmax, from the standard poles
// at an out-of-band gain of 1.3.
func designCLANS(n int, wB, cap, rmax float64) ntf {
	z := legendreZeros(n, wB)
	obj := func(x []float64) float64 {
		f := ntf{order: n, wB: wB, zeros: z, poles: polesFromParams(x, n, rmax)}
		e := math.Log10(f.inBand())
		if v := l1(f.sections(), 3000) - cap; v > 0 {
			e += 1e4 * v * v
		}
		return e
	}
	x := paramsFromPoles(designStandard(n, wB, 1.3).poles, n, rmax)
	for r := 0; r < 6; r++ {
		x = nelderMead(obj, x, 0.3/float64(r+1), 20000)
	}
	return ntf{order: n, wB: wB, zeros: z, poles: polesFromParams(x, n, rmax)}
}
