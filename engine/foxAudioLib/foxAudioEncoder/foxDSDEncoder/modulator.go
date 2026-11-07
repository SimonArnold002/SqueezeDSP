// Package: github.com/Foxenfurter/foxAudioLib/foxAudioEncoder/foxDSDEncoder
// filename modulator.go
//
// The 1-bit loop: error feedback through the NTF, built as its sections. With q = y - v the quantizer's
// error, v = u + (H-1)q, so y = u + Hq: the signal passes at unity and the error is shaped by H. Each
// section's output less its input depends only on its own past, so (H-1)q is known before the decision.
//
// Order 9 only (a first-order section and four biquads), unrolled: written so that everything but one add
// per section is computed before the decision, which is the loop's critical path.
package foxDSDEncoder

// guard: a loop whose decision input passes this has run away (it stays under about 2 in use); its state
// is cleared. It should never happen below Limit.
const guard = 20.0

type modulator struct {
	d1, d2, a1, a2 [5]float64 // per section: b1-a1, b2-a2, a1, a2
	w1, w2         [5]float64 // per section, direct form II states
	resets         int64
}

func newModulator(s [5]section) *modulator {
	m := &modulator{}
	for k, c := range s {
		m.d1[k], m.d2[k], m.a1[k], m.a2[k] = c.b1-c.a1, c.b2-c.a2, c.a1, c.a2
	}
	return m
}

// run modulates u (already within Limit) into bits, one per byte (1 = +1), the oldest first.
func (m *modulator) run(u []float64, bits []byte) {
	bits = bits[:len(u)]
	d1a, d1b, d1c, d1d, d1e := m.d1[0], m.d1[1], m.d1[2], m.d1[3], m.d1[4]
	d2b, d2c, d2d, d2e := m.d2[1], m.d2[2], m.d2[3], m.d2[4]
	a1a, a1b, a1c, a1d, a1e := m.a1[0], m.a1[1], m.a1[2], m.a1[3], m.a1[4]
	a2b, a2c, a2d, a2e := m.a2[1], m.a2[2], m.a2[3], m.a2[4]
	wa := m.w1[0]
	wb1, wc1, wd1, we1 := m.w1[1], m.w1[2], m.w1[3], m.w1[4]
	wb2, wc2, wd2, we2 := m.w2[1], m.w2[2], m.w2[3], m.w2[4]
	for i, x := range u {
		// c_k = the section's output less its input, from its past alone
		ca := d1a * wa
		cb := d2b*wb2 + d1b*wb1
		cc := d2c*wc2 + d1c*wc1
		cd := d2d*wd2 + d1d*wd1
		ce := d2e*we2 + d1e*we1
		v := (x + ca) + ((cb + cc) + (cd + ce))
		if v > guard || v < -guard {
			wa, wb1, wc1, wd1, we1 = 0, 0, 0, 0, 0
			wb2, wc2, wd2, we2 = 0, 0, 0, 0
			ca, cb, cc, cd, ce = 0, 0, 0, 0, 0
			v = x
			m.resets++
		}
		// section k's input is q plus the c's before it: q + (sum of all c) - (c's from k on) = y - x - S_k
		se := ce
		sd := cd + se
		sc := cc + sd
		sb := cb + sc
		sa := ca + sb
		pa := -sa - a1a*wa
		pb := -sb - a1b*wb1 - a2b*wb2
		pc := -sc - a1c*wc1 - a2c*wc2
		pd := -sd - a1d*wd1 - a2d*wd2
		pe := -se - a1e*we1 - a2e*we2
		y := -1.0
		var bit byte
		if v >= 0 {
			y = 1
			bit = 1
		}
		bits[i] = bit
		e := y - x
		wa = e + pa
		wb2, wb1 = wb1, e+pb
		wc2, wc1 = wc1, e+pc
		wd2, wd1 = wd1, e+pd
		we2, we1 = we1, e+pe
	}
	m.w1[0] = wa
	m.w1[1], m.w1[2], m.w1[3], m.w1[4] = wb1, wc1, wd1, we1
	m.w2[1], m.w2[2], m.w2[3], m.w2[4] = wb2, wc2, wd2, we2
}

// runPair: two channels with the same design through one loop, interleaved. The loop is bound by its own
// chain of dependent steps, not by arithmetic, so a second independent chain costs little: about half the
// CPU time of two runs. It is run's arithmetic, so run's bits wherever Go keeps each multiply and add apart:
// amd64 as built (plex's), no fused instruction in either loop (checked with -S, 2026-11-04). On arm64 Go
// fuses multiply-adds (42 in the two loops), not in the same places in both, so there the rounding drifts
// apart and, the loop being chaotic, in time the bits too, both equally valid (ledger PAIR AND SINGLE RUNS
// ROUND APART ON ARM64: a 96k track at DSD512 parted after 2.1 s). The encoder runs pairs only.
func runPair(ma, mb *modulator, ua, ub []float64, ba, bb []byte) {
	ub, ba, bb = ub[:len(ua)], ba[:len(ua)], bb[:len(ua)]
	d1a, d1b, d1c, d1d, d1e := ma.d1[0], ma.d1[1], ma.d1[2], ma.d1[3], ma.d1[4]
	d2b, d2c, d2d, d2e := ma.d2[1], ma.d2[2], ma.d2[3], ma.d2[4]
	a1a, a1b, a1c, a1d, a1e := ma.a1[0], ma.a1[1], ma.a1[2], ma.a1[3], ma.a1[4]
	a2b, a2c, a2d, a2e := ma.a2[1], ma.a2[2], ma.a2[3], ma.a2[4]
	// channel A's states, then B's
	xa := ma.w1[0]
	xb1, xc1, xd1, xe1 := ma.w1[1], ma.w1[2], ma.w1[3], ma.w1[4]
	xb2, xc2, xd2, xe2 := ma.w2[1], ma.w2[2], ma.w2[3], ma.w2[4]
	ya := mb.w1[0]
	yb1, yc1, yd1, ye1 := mb.w1[1], mb.w1[2], mb.w1[3], mb.w1[4]
	yb2, yc2, yd2, ye2 := mb.w2[1], mb.w2[2], mb.w2[3], mb.w2[4]
	for i, x := range ua {
		z := ub[i]
		// A
		ca := d1a * xa
		cb := d2b*xb2 + d1b*xb1
		cc := d2c*xc2 + d1c*xc1
		cd := d2d*xd2 + d1d*xd1
		ce := d2e*xe2 + d1e*xe1
		v := (x + ca) + ((cb + cc) + (cd + ce))
		// B
		ga := d1a * ya
		gb := d2b*yb2 + d1b*yb1
		gc := d2c*yc2 + d1c*yc1
		gd := d2d*yd2 + d1d*yd1
		ge := d2e*ye2 + d1e*ye1
		vb := (z + ga) + ((gb + gc) + (gd + ge))
		if v > guard || v < -guard {
			xa, xb1, xc1, xd1, xe1 = 0, 0, 0, 0, 0
			xb2, xc2, xd2, xe2 = 0, 0, 0, 0
			ca, cb, cc, cd, ce = 0, 0, 0, 0, 0
			v = x
			ma.resets++
		}
		if vb > guard || vb < -guard {
			ya, yb1, yc1, yd1, ye1 = 0, 0, 0, 0, 0
			yb2, yc2, yd2, ye2 = 0, 0, 0, 0
			ga, gb, gc, gd, ge = 0, 0, 0, 0, 0
			vb = z
			mb.resets++
		}
		se := ce
		sd := cd + se
		sc := cc + sd
		sb := cb + sc
		sa := ca + sb
		pa := -sa - a1a*xa
		pb := -sb - a1b*xb1 - a2b*xb2
		pc := -sc - a1c*xc1 - a2c*xc2
		pd := -sd - a1d*xd1 - a2d*xd2
		pe := -se - a1e*xe1 - a2e*xe2
		te := ge
		td := gd + te
		tc := gc + td
		tb := gb + tc
		ta := ga + tb
		qa := -ta - a1a*ya
		qb := -tb - a1b*yb1 - a2b*yb2
		qc := -tc - a1c*yc1 - a2c*yc2
		qd := -td - a1d*yd1 - a2d*yd2
		qe := -te - a1e*ye1 - a2e*ye2
		y := -1.0
		var bit byte
		if v >= 0 {
			y = 1
			bit = 1
		}
		ba[i] = bit
		yy := -1.0
		var bitb byte
		if vb >= 0 {
			yy = 1
			bitb = 1
		}
		bb[i] = bitb
		e := y - x
		xa = e + pa
		xb2, xb1 = xb1, e+pb
		xc2, xc1 = xc1, e+pc
		xd2, xd1 = xd1, e+pd
		xe2, xe1 = xe1, e+pe
		f := yy - z
		ya = f + qa
		yb2, yb1 = yb1, f+qb
		yc2, yc1 = yc1, f+qc
		yd2, yd1 = yd1, f+qd
		ye2, ye1 = ye1, f+qe
	}
	ma.w1[0] = xa
	ma.w1[1], ma.w1[2], ma.w1[3], ma.w1[4] = xb1, xc1, xd1, xe1
	ma.w2[1], ma.w2[2], ma.w2[3], ma.w2[4] = xb2, xc2, xd2, xe2
	mb.w1[0] = ya
	mb.w1[1], mb.w1[2], mb.w1[3], mb.w1[4] = yb1, yc1, yd1, ye1
	mb.w2[1], mb.w2[2], mb.w2[3], mb.w2[4] = yb2, yc2, yd2, ye2
}
