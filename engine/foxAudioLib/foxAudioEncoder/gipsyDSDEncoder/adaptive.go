// Package: github.com/Foxenfurter/foxAudioLib/foxAudioEncoder/gipsyDSDEncoder
// filename adaptive.go
//
// The "adaptive" modulator, DSD64 only: a quiet design (adaptiveDesigns[0], stable to about 0.52) runs while the
// input stays low; ahead of a louder passage (|u| over adaptThreshold within the next adaptAheadSec) the loop fades
// over adaptRampSec to a safe design (adaptiveDesigns[1], stable to 0.75) and fades back adaptHoldSec after it. Both
// run on the same quantizer error all the time and the loop takes their blend, so a change of design never jumps:
// a hard switch, even with the new design's state rebuilt from the error history, put a click in band at -55 dB.
// The level stays unity throughout. At DSD128 and up the plain designs keep full headroom with little noise above
// 20 kHz, so "adaptive" is the standard modulator there.
//
// Removal: this file, adaptiveDesigns (tables.go, TestGenerateTables) and the "adaptive" branches in encoder.go.
package gipsyDSDEncoder

import "math"

const (
	adaptThreshold = 0.45  // the quiet design holds to about 0.52
	adaptAheadSec  = 0.020 // the look-ahead: longer than the ramp, so the safe design is in before a loud sample
	adaptHoldSec   = 0.050
	adaptRampSec   = 0.005
	noLoud         = math.MaxInt64
)

// adaptiveLoop: two designs of order 7 (a first-order section and three biquads), A quiet and B safe.
type adaptiveLoop struct {
	ad1, ad2, aa1, aa2 [4]float64 // A: b1-a1, b2-a2, a1, a2 per section
	bd1, bd2, ba1, ba2 [4]float64 // B
	aw1, aw2, bw1, bw2 [4]float64 // states
	alpha, step        float64
	ahead, hold, left  int64
	inB                bool
	buf                []float64 // samples not yet modulated
	nl                 []int64   // per pending sample: the index of the next loud sample at or after it (noLoud: none yet)
	next               int64     // the index of buf[0]
	resets             int64
	samplesInB         int64 // samples with any of B in the blend
}

func newAdaptiveLoop(a, b [4]section, rate int) *adaptiveLoop {
	l := &adaptiveLoop{
		ahead: int64(adaptAheadSec * float64(rate)),
		hold:  int64(adaptHoldSec * float64(rate)),
		step:  1 / (adaptRampSec * float64(rate)),
	}
	for k := 0; k < 4; k++ {
		l.ad1[k], l.ad2[k], l.aa1[k], l.aa2[k] = a[k].b1-a[k].a1, a[k].b2-a[k].a2, a[k].a1, a[k].a2
		l.bd1[k], l.bd2[k], l.ba1[k], l.ba2[k] = b[k].b1-b[k].a1, b[k].b2-b[k].a2, b[k].a1, b[k].a2
	}
	return l
}

// run takes samples at the DSD rate and appends the bits of every sample whose look-ahead window is now known.
func (l *adaptiveLoop) run(u []float64, out []byte) []byte {
	l.receive(u)
	n := int64(len(l.buf)) - l.ahead
	if n <= 0 {
		return out
	}
	return l.modulate(n, out)
}

// drain modulates everything pending: nothing comes after it.
func (l *adaptiveLoop) drain(out []byte) []byte {
	return l.modulate(int64(len(l.buf)), out)
}

// receive appends u and keeps nl: each sample's next loud index changes at most once, from noLoud.
func (l *adaptiveLoop) receive(u []float64) {
	old := len(l.buf)
	base := l.next + int64(old)
	l.buf = append(l.buf, u...)
	for range u {
		l.nl = append(l.nl, noLoud)
	}
	cur := int64(noLoud)
	for i := len(u) - 1; i >= 0; i-- {
		if math.Abs(u[i]) > adaptThreshold {
			cur = base + int64(i)
		}
		l.nl[old+i] = cur
	}
	for j := old - 1; j >= 0 && l.nl[j] == noLoud && cur != noLoud; j-- {
		l.nl[j] = cur
	}
}

func (l *adaptiveLoop) modulate(n int64, out []byte) []byte {
	d1a, d1b, d1c, d1d := l.ad1[0], l.ad1[1], l.ad1[2], l.ad1[3]
	d2b, d2c, d2d := l.ad2[1], l.ad2[2], l.ad2[3]
	a1a, a1b, a1c, a1d := l.aa1[0], l.aa1[1], l.aa1[2], l.aa1[3]
	a2b, a2c, a2d := l.aa2[1], l.aa2[2], l.aa2[3]
	e1a, e1b, e1c, e1d := l.bd1[0], l.bd1[1], l.bd1[2], l.bd1[3]
	e2b, e2c, e2d := l.bd2[1], l.bd2[2], l.bd2[3]
	b1a, b1b, b1c, b1d := l.ba1[0], l.ba1[1], l.ba1[2], l.ba1[3]
	b2b, b2c, b2d := l.ba2[1], l.ba2[2], l.ba2[3]
	xa := l.aw1[0]
	xb1, xc1, xd1 := l.aw1[1], l.aw1[2], l.aw1[3]
	xb2, xc2, xd2 := l.aw2[1], l.aw2[2], l.aw2[3]
	ya := l.bw1[0]
	yb1, yc1, yd1 := l.bw1[1], l.bw1[2], l.bw1[3]
	yb2, yc2, yd2 := l.bw2[1], l.bw2[2], l.bw2[3]
	alpha, step := l.alpha, l.step
	if l.next == 0 && n > 0 && l.nl[0] <= l.ahead {
		alpha = 1 // loud from the very start (a track or a seek): no time to fade, so begin on the safe design
	}
	for k := int64(0); k < n; k++ {
		idx := l.next + k
		// B while a loud sample is within the look-ahead, and for hold after it
		if l.nl[k]-idx <= l.ahead {
			l.inB, l.left = true, l.hold
		} else if l.inB {
			if l.left > 0 {
				l.left--
			} else {
				l.inB = false
			}
		}
		if l.inB {
			alpha = math.Min(1, alpha+step)
		} else {
			alpha = math.Max(0, alpha-step)
		}
		if alpha > 0 {
			l.samplesInB++
		}
		x := l.buf[k]
		// each design's output less its input, from its own past
		ca := d1a * xa
		cb := d2b*xb2 + d1b*xb1
		cc := d2c*xc2 + d1c*xc1
		cd := d2d*xd2 + d1d*xd1
		ga := e1a * ya
		gb := e2b*yb2 + e1b*yb1
		gc := e2c*yc2 + e1c*yc1
		gd := e2d*yd2 + e1d*yd1
		CA := (ca + cb) + (cc + cd)
		CB := (ga + gb) + (gc + gd)
		v := x + CA + alpha*(CB-CA)
		if v > guard || v < -guard {
			xa, xb1, xc1, xd1, xb2, xc2, xd2 = 0, 0, 0, 0, 0, 0, 0
			ya, yb1, yc1, yd1, yb2, yc2, yd2 = 0, 0, 0, 0, 0, 0, 0
			ca, cb, cc, cd, ga, gb, gc, gd, CA, CB = 0, 0, 0, 0, 0, 0, 0, 0, 0, 0
			v = x
			l.resets++
		}
		// both cascades filter the same error q = y - v; section k's input is q plus the c's before it
		sd := cd
		sc := cc + sd
		sb := cb + sc
		pa := (CA - v) - CA - a1a*xa
		pb := (CA - v) - sb - a1b*xb1 - a2b*xb2
		pc := (CA - v) - sc - a1c*xc1 - a2c*xc2
		pd := (CA - v) - sd - a1d*xd1 - a2d*xd2
		td := gd
		tc := gc + td
		tb := gb + tc
		qa := (CB - v) - CB - b1a*ya
		qb := (CB - v) - tb - b1b*yb1 - b2b*yb2
		qc := (CB - v) - tc - b1c*yc1 - b2c*yc2
		qd := (CB - v) - td - b1d*yd1 - b2d*yd2
		y := -1.0
		var bit byte
		if v >= 0 {
			y = 1
			bit = 1
		}
		out = append(out, bit)
		xa = y + pa
		xb2, xb1 = xb1, y+pb
		xc2, xc1 = xc1, y+pc
		xd2, xd1 = xd1, y+pd
		ya = y + qa
		yb2, yb1 = yb1, y+qb
		yc2, yc1 = yc1, y+qc
		yd2, yd1 = yd1, y+qd
	}
	l.alpha = alpha
	l.aw1[0] = xa
	l.aw1[1], l.aw1[2], l.aw1[3] = xb1, xc1, xd1
	l.aw2[1], l.aw2[2], l.aw2[3] = xb2, xc2, xd2
	l.bw1[0] = ya
	l.bw1[1], l.bw1[2], l.bw1[3] = yb1, yc1, yd1
	l.bw2[1], l.bw2[2], l.bw2[3] = yb2, yc2, yd2
	m := copy(l.buf, l.buf[n:])
	l.buf = l.buf[:m]
	copy(l.nl, l.nl[n:])
	l.nl = l.nl[:m]
	l.next += n
	return out
}
