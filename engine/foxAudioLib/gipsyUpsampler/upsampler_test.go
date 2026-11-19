package gipsyUpsampler

import (
	"math"
	"math/cmplx"
	"math/rand"
	"strings"
	"testing"
)

// every filter the page offers (Choices: Upsample.pm's @FILTERS)
var allChoices = Choices()

var (
	kaiserShortLin = Choice{Phase: Linear}
	kaiserLongLin  = Choice{Length: Long, Phase: Linear}
	kaiserLongMin  = Choice{Length: Long, Phase: Minimum}
)

// response: |H| in dB against gain g on an FFT grid of at least 64 points per tap, up to fs/2.
func response(h []float64, g float64) []float64 { return responseN(h, g, 64) }

// responseN: the same with perTap points a tap (16 finds a Kaiser stop band's lobes to about 0.2 dB).
func responseN(h []float64, g float64, perTap int) []float64 {
	n := nextPow2(perTap * len(h))
	if n < 1<<16 {
		n = 1 << 16
	}
	x := make([]complex128, n)
	for i, v := range h {
		x[i] = complex(v, 0)
	}
	fft(x, false)
	db := make([]float64, n/2+1)
	for i := range db {
		db[i] = 20 * math.Log10(math.Max(cmplx.Abs(x[i])/g, 1e-300))
	}
	return db
}

// gainAt: |H| of h at f (rate fs) against gain g, dB.
func gainAt(h []float64, fs, f, g float64) float64 {
	var re, im float64
	for i, v := range h {
		s, co := math.Sincos(2 * math.Pi * f * float64(i) / fs)
		re, im = re+v*co, im-v*s
	}
	return 20 * math.Log10(math.Hypot(re, im)/g)
}

// pcmRates: every rate a PCM source or target takes (the converter's 705.6k among them).
var pcmRates = []int{44100, 48000, 88200, 96000, 176400, 192000, 352800, 384000, 705600, 768000}

// protoOf: a stage's prototype, rebuilt from its phases.
func protoOf(b *poly) []float64 {
	proto := make([]float64, 0, b.K*b.L)
	for t0 := 0; t0 < b.K*b.L; t0++ {
		p, j := t0%b.L, b.K-1-t0/b.L
		proto = append(proto, b.h[p][j])
	}
	return proto
}

// stage A of every filter but Non-ringing (which stops nothing: TestNonRinging) at every lower rate (a source going
// up, a target going down): flat in its pass band, StopDB down from its stop band edge.
func TestStopBands(t *testing.T) {
	for _, fs := range []float64{44100, 48000, 88200, 96000, 176400, 192000, 352800, 384000, 705600} {
		for _, c := range allChoices {
			if c.Family == NonRinging {
				continue
			}
			fp, fst := widths(fs, IsHiRes(int(fs)), c)
			h := shaped(c, 2*fs, fp, fst, StopDB, 2)
			db := response(h, 2)
			bin := 2 * fs / float64(2*(len(db)-1))
			ripple, stop := 0.0, -1000.0
			for i, v := range db {
				f := float64(i) * bin
				if f <= fp {
					ripple = math.Max(ripple, math.Abs(v))
				} else if f >= fst {
					stop = math.Max(stop, v)
				}
			}
			want := 1e-5
			split := c.Phase.linShare() > 0
			switch {
			case c.Family == Gaussian && split:
				want = gaussPassDB + 6e-4 // its edge, and the parts cut as their stop bands allow (0.0015 dB)
			case c.Family == Gaussian:
				want = gaussPassDB + 1e-4 // its pass band edge is where it is gaussPassDB down
			case split:
				want = 3e-4 // the parts cut as their stop bands allow (0.00012 dB)
			case c.Phase == Minimum:
				want = 2e-4
			}
			if ripple > want || stop > -StopDB+0.5 {
				t.Errorf("%.1fk %s: %d taps, pass band ripple %.2g dB (want under %.2g), stop band %.1f dB (want under -%.1f)",
					fs/1000, c, len(h), ripple, want, stop, StopDB-0.5)
			}
		}
	}
}

// Every pair of rates: the stages in order (going up A, x2 from the source, then B; going down B, then A, 2:1 to the
// target), B wherever the other end is not twice the lower rate, flat to A's stop band edge (the lower Nyquist; for a
// leaky family going up, its own edge, at most half way to B's lower Nyquist) and StopDB down from where an image or
// an alias would land below it.
func TestStageB(t *testing.T) {
	for _, c := range []Choice{DefaultCD, {Family: Slow}, {Family: HalfBand, Phase: Minimum}, {Family: NonRinging}} {
		for _, in := range pcmRates {
			for _, out := range pcmRates {
				if in == out {
					continue
				}
				u, err := New(in, out, 1, c, c)
				if err != nil {
					t.Fatal(err)
				}
				lo, hi := min(in, out), max(in, out)
				if u.Down != (out < in) {
					t.Fatalf("%d to %d: Down %v", in, out, u.Down)
				}
				a := u.stages[0][0]
				if u.Down {
					a = u.stages[len(u.stages)-1][0]
				}
				if u.Down && (a.L != 1 || a.M != 2) || !u.Down && (a.L != 2 || a.M != 1) {
					t.Fatalf("%d to %d: A is %d/%d, want %s", in, out, a.L, a.M, map[bool]string{false: "2/1 first", true: "1/2 last"}[u.Down])
				}
				if wantB := hi != 2*lo; (len(u.stages) == 2) != wantB || (u.TapsB > 0) != wantB {
					t.Fatalf("%d to %d: %d stages, B %d taps", in, out, len(u.stages), u.TapsB)
				}
				if len(u.stages) == 1 {
					continue
				}
				b := u.stages[1][0]
				bIn, bOut := 2*lo, out
				if u.Down {
					b, bIn, bOut = u.stages[0][0], in, 2*lo
				}
				if b.L*bIn != b.M*bOut {
					t.Fatalf("%d to %d: B %d/%d from %d", in, out, b.L, b.M, bIn)
				}
				fp := float64(lo) / 2
				if !u.Down && c.Leaky() {
					_, edge := widths(float64(lo), IsHiRes(lo), c)
					fp = math.Min(edge, (float64(lo)/2+float64(min(bIn, bOut))/2)/2)
				}
				fst := float64(min(bIn, bOut)) - fp
				if u.bFp != fp || u.bFst != fst {
					t.Fatalf("%d to %d %s: B flat to %.0f and stopped from %.0f, want %.0f and %.0f", in, out, c, u.bFp, u.bFst, fp, fst)
				}
				db := responseN(protoOf(b), float64(b.L), 16)
				bin := float64(bIn) * float64(b.L) / float64(2*(len(db)-1))
				ripple, stop := 0.0, -1000.0
				for i, v := range db {
					f := float64(i) * bin
					if f <= fp {
						ripple = math.Max(ripple, math.Abs(v))
					} else if f >= fst {
						stop = math.Max(stop, v)
					}
				}
				if ripple > 1e-5 || stop > -StopDB+0.5 {
					t.Errorf("%d to %d %s: B %d/%d, %d taps: ripple %.2g dB, stop band %.1f dB", in, out, c, b.L, b.M, u.TapsB, ripple, stop)
				}
			}
		}
	}
}

// energyBefore: the share (dB) of the impulse's energy before its main lobe (up to the last sign change before
// the peak).
func energyBefore(h []float64) float64 {
	p := 0
	for i, v := range h {
		if math.Abs(v) > math.Abs(h[p]) {
			p = i
		}
	}
	z := -1
	for i := p - 1; i >= 0; i-- {
		if (h[i] > 0) != (h[p] > 0) {
			z = i
			break
		}
	}
	var pre, all float64
	for i, v := range h {
		all += v * v
		if i <= z {
			pre += v * v
		}
	}
	return 10 * math.Log10(math.Max(pre/all, 1e-300))
}

// Every family with phases, CD widths: linear symmetric with about -13 dB of its energy before the main lobe,
// intermediate 25 dB under that, minimum none.
func TestPhaseShapes(t *testing.T) {
	for _, base := range []Choice{{}, {Length: Long}, {Length: ExtraLong}, {Family: Gaussian}, {Family: Gaussian, Length: Medium},
		{Family: Gaussian, Length: Long}, {Family: HalfBand}, {Family: Slow}} {
		at := func(p Phase) []float64 {
			c := base
			c.Phase = p
			fp, fst := widths(44100, false, c)
			return shaped(c, 88200, fp, fst, StopDB, 2)
		}
		lin := at(Linear)
		for i := range lin {
			if lin[i] != lin[len(lin)-1-i] {
				t.Fatalf("%s: not symmetric at %d", base, i)
			}
		}
		el, ei, em := energyBefore(lin), energyBefore(at(Intermediate)), energyBefore(at(Minimum))
		t.Logf("%s: energy before the main lobe: linear %.1f, intermediate %.1f, minimum %.1f dB", base, el, ei, em)
		if el > -10 || el < -16 {
			t.Errorf("%s: linear %.1f dB before the main lobe, want about -13", base, el)
		}
		if ei > el-25 {
			t.Errorf("%s: intermediate %.1f dB, want 25 dB under linear's %.1f", base, ei, el)
		}
		if em > -120 {
			t.Errorf("%s: minimum %.1f dB before the main lobe, want none (under -120)", base, em)
		}
	}
}

// Every intermediate phase (half and half, 80% and 70% linear) of every family with phases, Kaiser's included since
// 2026-11-15, has its linear one's magnitude (powHalf): within 0.01 dB from DC down to -20 dB, within 0.1 down to -60
// (the parts cut as short as their stop bands allow: Gaussian 0.035 dB at -60, 4e-6 of the gain), at CD and hi-res
// widths. Kaiser's half-depth halves (kaiserShaped, the DSD integrator's) do not (the control: 5.7 dB at 21 kHz).
func TestIntermediateKeepsMagnitude(t *testing.T) {
	worstOf := func(c Choice, fs, floor float64, design func(Choice, float64, float64, float64) []float64) float64 {
		fp, fst := widths(fs, IsHiRes(int(fs)), c)
		p := c.Phase
		c.Phase = Linear
		lin := response(shaped(c, 2*fs, fp, fst, StopDB, 2), 2)
		c.Phase = p
		h := design(c, 2*fs, fp, fst)
		n := nextPow2(64 * len(h))
		if 2*(len(lin)-1) > n {
			n = 2 * (len(lin) - 1)
		}
		x := make([]complex128, n)
		for i, v := range h {
			x[i] = complex(v, 0)
		}
		fft(x, false)
		d := 0.0
		for i := range lin {
			k := i * n / (2 * (len(lin) - 1))
			if lin[i] < floor {
				break
			}
			d = math.Max(d, math.Abs(20*math.Log10(cmplx.Abs(x[k])/2)-lin[i]))
		}
		return d
	}
	upsampler := func(c Choice, fs, fp, fst float64) []float64 { return shaped(c, fs, fp, fst, StopDB, 2) }
	for _, fs := range []float64{44100, 96000} {
		for _, base := range phasedBases {
			for _, p := range []Phase{Intermediate, Int70, Int80} {
				c := base
				c.Phase = p
				if d := worstOf(c, fs, -20, upsampler); d > 0.01 {
					t.Errorf("%.1fk %s: differs from linear by %.3f dB above -20 dB, want under 0.01", fs/1000, c, d)
				}
				if d := worstOf(c, fs, -60, upsampler); d > 0.1 {
					t.Errorf("%.1fk %s: differs from linear by %.3f dB above -60 dB, want under 0.1", fs/1000, c, d)
				}
			}
		}
	}
	halfDepth := func(c Choice, fs, fp, fst float64) []float64 { return kaiserShaped(Intermediate, fs, fp, fst, StopDB, 2) }
	if d := worstOf(Choice{Phase: Intermediate}, 44100, -20, halfDepth); d < 1 {
		t.Errorf("Kaiser short's half-depth intermediate within %.3f dB of its linear: the control should differ (FAMILY STUDY: 5.7 dB at 21 kHz)", d)
	}
}

// The filters the linear-leaning round (2026-11-15) left as they were, pinned by their lengths at CD widths (44.1k,
// stage A at 88.2k): Kaiser's linear and minimum (kaiserShaped), and the other families' square-root intermediates
// (sqrtHalf, its cut through powHalf and cutTo since): the lengths the FAMILY STUDY's cross-check measured on
// 0.1.72.44. All 204 of the old filters at every rate came out bit for bit the same as before the round (scratchpad
// bits/, a one-off); this keeps the cut from moving unseen.
func TestUnchangedLengths(t *testing.T) {
	for _, tc := range []struct {
		c    Choice
		taps int
	}{
		{kaiserShortLin, 515}, {Choice{Phase: Minimum}, 515}, {kaiserLongLin, 4769}, {kaiserLongMin, 4769},
		{Choice{Length: ExtraLong}, 9537}, {Choice{Family: Gaussian, Phase: Intermediate}, 469},
		{Choice{Family: Gaussian, Length: Medium, Phase: Intermediate}, 693}, {Choice{Family: Gaussian, Length: Long, Phase: Intermediate}, 6469},
		{Choice{Family: HalfBand, Phase: Intermediate}, 805}, {Choice{Family: Slow, Phase: Intermediate}, 157},
	} {
		fp, fst := widths(44100, false, tc.c)
		if n := len(shaped(tc.c, 88200, fp, fst, StopDB, 2)); n != tc.taps {
			t.Errorf("%s: %d taps, want %d", tc.c, n, tc.taps)
		}
	}
}

// phasedBases: every family and length with phases.
var phasedBases = []Choice{{}, {Length: Long}, {Length: ExtraLong}, {Family: Gaussian}, {Family: Gaussian, Length: Medium},
	{Family: Gaussian, Length: Long}, {Family: HalfBand}, {Family: Slow}}

// groupDelay: h's group delay at f (rate fs) in samples, by the phase step between f and f + 1 Hz.
func groupDelay(h []float64, fs, f float64) float64 {
	at := func(f float64) complex128 {
		var re, im float64
		for i, v := range h {
			s, c := math.Sincos(2 * math.Pi * f * float64(i) / fs)
			re, im = re+v*c, im-v*s
		}
		return complex(re, im)
	}
	return -cmplx.Phase(at(f+1)/at(f)) / (2 * math.Pi / fs)
}

// ringing: how long (s) h stays above db (dB, under 0) of its peak before and after the peak.
func ringing(h []float64, fs, db float64) (pre, post float64) {
	p := 0
	for i, v := range h {
		if math.Abs(v) > math.Abs(h[p]) {
			p = i
		}
	}
	first, last := p, p
	for i, v := range h {
		if math.Abs(v) > math.Abs(h[p])*math.Pow(10, db/20) {
			first, last = min(first, i), max(last, i)
		}
	}
	return float64(p-first) / fs, float64(last-p) / fs
}

// The linear-leaning intermediates (Simon 2026-11-15: "keep both filters for now then I can decide which works best"),
// every family with phases, CD widths (44.1k, stage A at 88.2k), what they are for: the top of the band arrives later
// than 1 kHz by the share of minimum phase's delay that is minimum (80%: 0.2 of it, 70%: 0.3, half and half: 0.5,
// each within 0.06), so in order linear < 80% < 70% < intermediate < minimum; the pre-ringing's energy (before the
// main lobe) in the order intermediate < 70% < 80% < linear, and how long before the peak it stays above -80 dB between
// intermediate's and linear's (at -100 dB Kaiser short's 80% reaches 0.09 ms further back than linear: its linear part,
// h over the minimum part, is the longer; between 70% and 80% Slow's 99-tap filters differ by two samples either way);
// and none rings longer after the peak (above -100 dB) than the half-and-half one (powHalf's floor under the minimum
// part: without it Gaussian long's 80% rang 80 ms after, its intermediate 28).
func TestLinearLeaning(t *testing.T) {
	const fs, f = 88200.0, 20000.0
	for _, base := range phasedBases {
		at := func(p Phase) []float64 {
			c := base
			c.Phase = p
			fp, fst := widths(44100, false, c)
			return shaped(c, fs, fp, fst, StopDB, 2)
		}
		delay := func(h []float64) float64 { return groupDelay(h, fs, f) - groupDelay(h, fs, 1000) }
		hs := map[Phase][]float64{}
		for _, p := range phaseOrder {
			hs[p] = at(p)
		}
		dMin := delay(hs[Minimum])
		if dMin < 0.05*fs/1000 { // under 0.05 ms: nothing to share out
			t.Fatalf("%s: minimum phase's top-octave delay %.3f ms", base, dMin/fs*1000)
		}
		last := -1.0
		for _, p := range phaseOrder {
			d := delay(hs[p])
			if d <= last {
				t.Errorf("%s %s: %.3f ms at 20 kHz, not after the phase before it (%.3f)", base, phaseNames[p], d/fs*1000, last/fs*1000)
			}
			last = d
			if b := p.linShare(); b > 0 && math.Abs(d/dMin-(1-b)) > 0.06 {
				t.Errorf("%s %s: %.2f of minimum's delay at 20 kHz, want %.2f", base, phaseNames[p], d/dMin, 1-b)
			}
		}
		eI, e70, e80, eL := energyBefore(hs[Intermediate]), energyBefore(hs[Int70]), energyBefore(hs[Int80]), energyBefore(hs[Linear])
		if !(eI < e70 && e70 < e80 && e80 < eL) {
			t.Errorf("%s: energy before the main lobe intermediate %.1f, 70%% %.1f, 80%% %.1f, linear %.1f dB: want in that order",
				base, eI, e70, e80, eL)
		}
		preI, _ := ringing(hs[Intermediate], fs, -80)
		pre70, _ := ringing(hs[Int70], fs, -80)
		pre80, _ := ringing(hs[Int80], fs, -80)
		preL, _ := ringing(hs[Linear], fs, -80)
		if !(preI <= pre70 && preI <= pre80 && pre70 < preL && pre80 < preL) {
			t.Errorf("%s: above -80 dB before the peak intermediate %.2f, 70%% %.2f, 80%% %.2f, linear %.2f ms: want 70%% and 80%% between",
				base, preI*1000, pre70*1000, pre80*1000, preL*1000)
		}
		_, postI := ringing(hs[Intermediate], fs, -100)
		_, post70 := ringing(hs[Int70], fs, -100)
		_, post80 := ringing(hs[Int80], fs, -100)
		if post70 > postI || post80 > postI {
			t.Errorf("%s: ringing after the peak 70%% %.2f, 80%% %.2f ms, longer than intermediate's %.2f", base, post70*1000, post80*1000, postI*1000)
		}
	}
}

// Non-ringing: every tap positive (no ringing, a step with no overshoot), symmetric, 3 dB down at its edge, at every
// lower rate.
func TestNonRinging(t *testing.T) {
	c := Choice{Family: NonRinging}
	for _, fs := range []float64{44100, 48000, 96000, 192000, 705600} {
		fp, _ := widths(fs, IsHiRes(int(fs)), c)
		h := shaped(c, 2*fs, fp, 0, StopDB, 2)
		for i, v := range h {
			if v <= 0 || v != h[len(h)-1-i] {
				t.Fatalf("%.1fk: tap %d of %d is %g (or not its mirror's)", fs/1000, i, len(h), v)
			}
		}
		if g := gainAt(h, 2*fs, fp, 2); math.Abs(g+3) > 0.01 {
			t.Errorf("%.1fk: %.3f dB at %.0f Hz, want -3", fs/1000, g, fp)
		}
		if g := gainAt(h, 2*fs, fs, 2); g > -150 {
			t.Errorf("%.1fk: %.1f dB at fs (the bass's images), want nulled", fs/1000, g)
		}
	}
}

func noise(n int, seed int64) []float64 {
	r := rand.New(rand.NewSource(seed))
	x := make([]float64, n)
	for i := range x {
		x[i] = r.Float64()*2 - 1
	}
	return x
}

func runAll(t *testing.T, u *Upsampler, in [][]float64, blocks []int) [][]float64 {
	out := make([][]float64, len(in))
	at := 0
	for _, b := range blocks {
		blk := make([][]float64, len(in))
		for ch := range in {
			blk[ch] = in[ch][at : at+b]
		}
		at += b
		got := u.Process(blk)
		for ch := range out {
			out[ch] = append(out[ch], got[ch]...)
		}
	}
	if at != len(in[0]) {
		t.Fatalf("blocks cover %d of %d", at, len(in[0]))
	}
	got := u.Flush()
	for ch := range out {
		out[ch] = append(out[ch], got[ch]...)
	}
	return out
}

// Blocks of any size give the same samples, bit for bit, and N frames in give round(N*out/in) out.
func TestBlocksAndLength(t *testing.T) {
	for _, r := range [][2]int{{44100, 705600}, {48000, 705600}, {96000, 705600}, {192000, 705600}, {384000, 705600},
		{44100, 48000}, {176400, 192000}, {96000, 48000}, {192000, 44100}, {88200, 48000}, {384000, 352800}} {
		fs, to := r[0], r[1]
		for _, c := range []Choice{{Phase: Intermediate}, kaiserLongMin, {Family: Gaussian, Length: Medium, Phase: Intermediate},
			{Family: Slow, Phase: Minimum}, {Family: NonRinging}, {Phase: Int80}, {Family: HalfBand, Phase: Int70}} {
			n := fs/3 + 17
			in := [][]float64{noise(n, 1), noise(n, 2)}
			one, _ := New(fs, to, 2, c, c)
			whole := runAll(t, one, in, []int{n})
			r := rand.New(rand.NewSource(int64(fs)))
			var blocks []int
			for left := n; left > 0; {
				b := min(left, 1+r.Intn(5000))
				blocks = append(blocks, b)
				left -= b
			}
			many, _ := New(fs, to, 2, c, c)
			parts := runAll(t, many, in, blocks)
			want := int(math.Round(float64(n) * float64(to) / float64(fs)))
			for ch := range whole {
				if len(whole[ch]) != want || len(parts[ch]) != want {
					t.Fatalf("%d to %d %s: %d and %d frames out for %d in, want %d", fs, to, c, len(whole[ch]), len(parts[ch]), n, want)
				}
				for i := range whole[ch] {
					if whole[ch][i] != parts[ch][i] {
						t.Fatalf("%d to %d %s ch %d: frame %d differs in blocks (%g against %g)", fs, to, c, ch, i, parts[ch][i], whole[ch][i])
					}
				}
			}
		}
	}
}

// Linear phase: a sine comes out as the same sine at the output rate, in time with the input (the delay left after
// rounding is a fraction of a sample, allowed for), within -150 dB: every pair of rates, up and down.
func TestSineExact(t *testing.T) {
	for _, fs := range pcmRates {
		for _, to := range pcmRates {
			if fs == to {
				continue
			}
			for _, c := range []Choice{kaiserShortLin, kaiserLongLin, {Family: Gaussian, Length: Medium}} {
				u, _ := New(fs, to, 1, c, c)
				n := fs / 4
				f := 997.0
				x := make([]float64, n)
				for i := range x {
					x[i] = 0.5 * math.Sin(2*math.Pi*f*float64(i)/float64(fs))
				}
				y := runAll(t, u, [][]float64{x}, []int{n})[0]
				shift := u.Delay - math.Round(u.Delay) // the delay's fraction, still in the output
				var e, s float64
				skip := to / 20 // the ends: the filters see silence beyond them
				for i := skip; i < len(y)-skip; i++ {
					ideal := 0.5 * math.Sin(2*math.Pi*f*(float64(i)-shift)/float64(to))
					e += (y[i] - ideal) * (y[i] - ideal)
					s += ideal * ideal
				}
				if db := 10 * math.Log10(e/s); db > -150 {
					t.Errorf("%d to %d %s: error %.1f dB against the ideal sine (A %d taps, B %d, delay %.3f), want under -150",
						fs, to, c, db, u.TapsA, u.TapsB, u.Delay)
				}
			}
		}
	}
}

// A click comes out where it went in: minimum and intermediate phase too, within the delay's rounding and
// the impulse's own spread; up, and down.
func TestAligned(t *testing.T) {
	for _, r := range [][2]int{{44100, 705600}, {48000, 705600}, {96000, 705600}, {44100, 48000}, {96000, 48000},
		{192000, 44100}, {88200, 48000}, {176400, 96000}} {
		fs, to := r[0], r[1]
		for _, c := range allChoices {
			u, _ := New(fs, to, 1, c, c)
			n := fs / 4
			x := make([]float64, n)
			k := n / 2
			x[k] = 1
			y := runAll(t, u, [][]float64{x}, []int{n})[0]
			var s, sn float64
			for i, v := range y {
				s += v
				sn += float64(i) * v
			}
			want := float64(k) * float64(to) / float64(fs)
			if got := sn / s; math.Abs(got-want) > 1 {
				t.Errorf("%d to %d %s: the click's centre at %.2f, want %.2f", fs, to, c, got, want)
			}
		}
	}
}

// kaiserWindow: beta 30, side lobes far under -200 dB, for reading the chain's output spectrum.
func kaiserWindow(n int) []float64 {
	w := make([]float64, n)
	i0 := bessel0(30)
	m := float64(n-1) / 2
	for i := range w {
		r := (float64(i) - m) / m
		w[i] = bessel0(30*math.Sqrt(math.Max(0, 1-r*r))) / i0
	}
	return w
}

// toneThrough: a tone at f (0.5) through in to out, 0.4 s, the steady middle of the output through a Kaiser window
// and its spectrum (N points), the window's sum.
func toneThrough(in, out int, c Choice, f float64) ([]complex128, float64) {
	u, _ := New(in, out, 1, c, c)
	n := 2 * in / 5
	x := make([]float64, n)
	for i := range x {
		x[i] = 0.5 * math.Sin(2*math.Pi*f*float64(i)/float64(in))
	}
	var y []float64
	for at := 0; at < n; at += 8192 {
		y = append(y, u.Process([][]float64{x[at:min(at+8192, n)]})[0]...)
	}
	y = append(y, u.Flush()[0]...)
	mid := y[out/10 : len(y)-out/10]
	N := 1
	for N*2 <= len(mid) {
		N *= 2
	}
	w := kaiserWindow(N)
	X := make([]complex128, N)
	var sw float64
	for i := range X {
		X[i] = complex(mid[i]*w[i], 0)
		sw += w[i]
	}
	fft(X, false)
	return X, sw
}

// spurious: the largest component in the output's band other than the tone itself, dB against the tone that went
// in. Nothing but the tone should be there: neither an image of it nor (above the target's Nyquist) an alias.
func spurious(in, out int, c Choice, f float64) float64 {
	X, sw := toneThrough(in, out, c, f)
	N := len(X)
	k0, worst := int(math.Round(f*float64(N)/float64(out))), 0.0
	for k := 1; k < N/2; k++ {
		if f < float64(out)/2 && k >= k0-14 && k <= k0+14 {
			continue
		}
		worst = math.Max(worst, cmplx.Abs(X[k])*2/sw)
	}
	return 20 * math.Log10(math.Max(worst, 1e-300)/0.5)
}

// imageOf: the level of a tone's image at in - f (going up), dB against the tone that went in.
func imageOf(in, out int, c Choice, f float64) float64 {
	X, sw := toneThrough(in, out, c, f)
	N := len(X)
	k0, peak := int(math.Round((float64(in)-f)*float64(N)/float64(out))), 0.0
	for k := k0 - 3; k <= k0+3; k++ {
		peak = math.Max(peak, cmplx.Abs(X[k])*2/sw)
	}
	return 20 * math.Log10(math.Max(peak, 1e-300)/0.5)
}

// toneLevel: a tone at f through in to out (0.4 s, its steady middle read through a Kaiser window), its level at the
// output against the input's, dB.
func toneLevel(in, out int, c Choice, f float64) float64 {
	u, _ := New(in, out, 1, c, c)
	n := 2 * in / 5
	x := make([]float64, n)
	for i := range x {
		x[i] = 0.5 * math.Sin(2*math.Pi*f*float64(i)/float64(in))
	}
	var y []float64
	for at := 0; at < n; at += 8192 {
		y = append(y, u.Process([][]float64{x[at:min(at+8192, n)]})[0]...)
	}
	y = append(y, u.Flush()[0]...)
	mid := y[out/10 : len(y)-out/10]
	w := kaiserWindow(len(mid))
	var re, im, sw float64
	for i, v := range mid {
		s, c := math.Sincos(2 * math.Pi * f * float64(i) / float64(out))
		re, im, sw = re+v*w[i]*c, im-v*w[i]*s, sw+w[i]
	}
	return 20 * math.Log10(math.Hypot(re, im)*2/sw/0.5)
}

// Going down, A sits at the target's Nyquist with the widths of the target's class: a hi-res track down to 48k meets
// the CD-rate edges (short flat to 21.77 kHz there, long to 23.76 kHz), as a 48k track going up does; down within
// hi-res, the hi-res ones (to 96k: long flat to 43.2 kHz). The hi-res widths at 48k would roll these tones off.
func TestDownWidths(t *testing.T) {
	for _, tc := range []struct {
		in, out int
		c       Choice
		f       float64
	}{
		{96000, 48000, kaiserShortLin, 21500},
		{96000, 48000, kaiserLongMin, 23500},
		{192000, 44100, kaiserLongLin, 21700},
		{192000, 96000, kaiserLongLin, 43000},
	} {
		if db := toneLevel(tc.in, tc.out, tc.c, tc.f); math.Abs(db) > 0.01 {
			t.Errorf("%d to %d %s: a %.0f Hz tone at %.4f dB, want flat", tc.in, tc.out, tc.c, tc.f, db)
		}
	}
}

// Tones at the top of the lower rate's band, and (going down) above the target's Nyquist, leave nothing else in the
// output: every way through the chain (up by 2 or more, up by less than 2, down by 2, by more, by less), Kaiser's and
// Gaussian's ways across. Until 2026-11-05 the last four went through B alone: images and aliases at -7 to -15 dB.
// The leaky families going down run as Kaiser short, so they leave nothing either.
func TestNoImagesOrAliases(t *testing.T) {
	for _, r := range [][2]int{{44100, 352800}, {96000, 705600}, {44100, 48000}, {176400, 192000}, {352800, 384000},
		{384000, 705600}, {96000, 48000}, {192000, 44100}, {88200, 48000}, {384000, 352800}} {
		in, out := r[0], r[1]
		lo := float64(min(in, out))
		tones := []float64{0.47 * lo, 0.49 * lo, 0.499 * lo}
		cs := []Choice{kaiserShortLin, kaiserLongMin, {Family: Gaussian, Phase: Intermediate}, {Family: Gaussian, Length: Long, Phase: Minimum},
			{Phase: Intermediate}, {Phase: Int70}, {Family: Gaussian, Length: Medium, Phase: Int80}}
		if out < in {
			for _, k := range []float64{0.51, 0.55, 0.75, 0.95} {
				if f := k * float64(out); f < float64(in)/2 {
					tones = append(tones, f)
				}
			}
			cs = append(cs, Choice{Family: Slow, Phase: Intermediate}, Choice{Family: NonRinging})
		}
		for _, c := range cs {
			for _, f := range tones {
				if db := spurious(in, out, c, f); db > -165 {
					t.Errorf("%d to %d %s: a tone at %.0f Hz leaves %.1f dB of something else, want under -165", in, out, c, f, db)
				}
			}
		}
	}
}

// The leaky families going up let through what the FAMILY STUDY measured (44.1k to 88.2k, A alone; 44.1k to 705.6k,
// B after it); going down they run as Kaiser short in their phase.
func TestLeaky(t *testing.T) {
	for _, tc := range []struct {
		c      Choice
		f      float64
		lo, hi float64 // the image's level, dB
		what   string
	}{
		{Choice{Family: Slow}, 20000, -27.5, -25.5, "a 20 kHz tone's image (study -26.5)"},
		{Choice{Family: Slow, Phase: Minimum}, 20000, -27.5, -25.5, "... minimum phase, the same magnitude"},
		{Choice{Family: NonRinging}, 20000, -5, -4, "a 20 kHz tone's image (study -4.5)"},
		{Choice{Family: HalfBand}, 21950, -12, -6, "a 21.95 kHz tone's image (22.15k), just past its -6 dB point"},
		{Choice{Family: HalfBand}, 21500, -45, -38, "a 21.5 kHz tone's image (22.6k), most of the way down its edge"},
		{Choice{Family: HalfBand}, 20000, -400, -165, "a 20 kHz tone's image, past its band: none"},
	} {
		for _, out := range []int{88200, 705600} {
			if db := imageOf(44100, out, tc.c, tc.f); db < tc.lo || db > tc.hi {
				t.Errorf("44.1k to %d %s: %s at %.1f dB, want %.1f to %.1f", out, tc.c, tc.what, db, tc.lo, tc.hi)
			}
		}
	}
	for _, c := range []Choice{{Family: HalfBand, Phase: Minimum}, {Family: Slow, Phase: Intermediate}, {Family: NonRinging},
		{Family: HalfBand, Phase: Int80}, {Family: Slow, Phase: Int70}} {
		u, _ := New(96000, 48000, 1, c, c)
		if want := (Choice{Phase: c.Phase}); u.Choice != want {
			t.Errorf("96k to 48k %s: ran as %s, want %s", c, u.Choice, want)
		}
		if u, _ := New(48000, 96000, 1, c, c); u.Choice != c {
			t.Errorf("48k to 96k %s: ran as %s", c, u.Choice)
		}
	}
}

func BenchmarkCDShortInt(b *testing.B) { bench(b, 44100, Choice{Phase: Intermediate}) }
func BenchmarkCDLongLin(b *testing.B)  { bench(b, 44100, kaiserLongLin) }
func BenchmarkCD48ShortInt(b *testing.B) {
	bench(b, 48000, Choice{Phase: Intermediate})
}
func BenchmarkHiRes96ShortLin(b *testing.B) { bench(b, 96000, kaiserShortLin) }
func BenchmarkCDGaussLongLin(b *testing.B)  { bench(b, 44100, Choice{Family: Gaussian, Length: Long}) }

// one second of stereo at fs to 705.6k, in the engine's block size
func bench(b *testing.B, fs int, c Choice) {
	in := [][]float64{noise(fs, 1), noise(fs, 2)}
	b.ResetTimer()
	for it := 0; it < b.N; it++ {
		u, _ := New(fs, 705600, 2, c, c)
		for at := 0; at < fs; at += 8192 {
			e := min(at+8192, fs)
			u.Process([][]float64{in[0][at:e], in[1][at:e]})
		}
	}
}

// The widths (upsampler.go): pinned from outside widths(), so a long that is a short, or the hi-res line moved,
// shows. Gain against the gain at DC, dB, at frequencies each side of each edge.
func TestWidths(t *testing.T) {
	if IsHiRes(48000) || !IsHiRes(88200) {
		t.Fatal("48 kHz a hi-res source, or 88.2 kHz not")
	}
	at := func(fs float64, c Choice, f float64) float64 {
		fp, fst := widths(fs, IsHiRes(int(fs)), c)
		return gainAt(shaped(c, 2*fs, fp, fst, StopDB, 2), 2*fs, f, 2)
	}
	g := func(l Length) Choice { return Choice{Family: Gaussian, Length: l} }
	for _, tc := range []struct {
		fs   float64
		c    Choice
		flat float64 // still within 0.001 dB here (a Gaussian: gaussPassDB, its edge)
		down float64 // at least 1 dB down here
	}{
		{44100, kaiserShortLin, 20000, 21300},            // short: flat to 20 kHz
		{44100, kaiserLongLin, 21800, 22000},             // long: flat to 99% (21.83 kHz)
		{44100, Choice{Length: ExtraLong}, 21930, 22030}, // extra-long: flat to 99.5% (21.94 kHz)
		{48000, kaiserShortLin, 21700, 23100},            // the same shares at 48k
		{88200, kaiserShortLin, 20000, 30000},            // hi-res short: flat to 20 kHz, rolling off
		{88200, kaiserLongLin, 39600, 43000},             // hi-res long: flat to 90% (39.7 kHz)
		{88200, Choice{Length: ExtraLong}, 43600, 44050}, // hi-res extra-long: flat to 99% (43.66 kHz)
		{192000, kaiserShortLin, 20000, 60000},           // hi-res short at 192k: still 20 kHz
		{44100, g(Short), 19000, 20000},                  // Gaussian short: flat to 19 kHz (study: 20k -3.0)
		{44100, g(Medium), 20000, 21000},                 // medium: to 20 kHz (21k -16)
		{44100, g(Long), 21800, 22000},                   // long: to 99%
		{96000, g(Medium), 43100, 47000},                 // hi-res medium: to 90%
		{44100, Choice{Family: HalfBand}, 21000, 22500},  // Half-band: flat to 21.0 kHz
		{44100, Choice{Family: Slow}, 16500, 21000},      // Slow: flat to 16.55 kHz (study: 20k -0.42)
	} {
		tol := 0.001
		if tc.c.Family == Gaussian {
			tol = gaussPassDB + 1e-5
		}
		if g := at(tc.fs, tc.c, tc.flat); math.Abs(g) > tol {
			t.Errorf("%.1fk %s: %.4f dB at %.2f kHz, want flat", tc.fs/1000, tc.c, g, tc.flat/1000)
		}
		if g := at(tc.fs, tc.c, tc.down); g > -1 {
			t.Errorf("%.1fk %s: %.4f dB at %.2f kHz, want at least 1 dB down", tc.fs/1000, tc.c, g, tc.down/1000)
		}
	}
}

func TestParseChoice(t *testing.T) {
	if n := len(Choices()); n != 41 {
		t.Errorf("%d filters, want 41 (Kaiser 15, Gaussian 15, Half-band 5, Slow 5, Non-ringing 1)", n)
	}
	// each length's phases in the page's order, linear to minimum
	var names []string
	for _, c := range Choices()[:5] {
		names = append(names, c.String())
	}
	if got := strings.Join(names, " "); got != "short-lin short-int80 short-int70 short-int short-min" {
		t.Errorf("Kaiser short's phases in the order %s", got)
	}
	seen := map[string]bool{}
	for _, c := range Choices() {
		s := c.String()
		if seen[s] {
			t.Errorf("%q named twice", s)
		}
		seen[s] = true
		if got, err := ParseChoice(" " + strings.ToUpper(s) + " "); err != nil || got != c {
			t.Errorf("%q: %v %v", s, got, err)
		}
	}
	// Kaiser keeps the names stored before 0.1.72.44, so a player's setting carries over
	for s, c := range map[string]Choice{"short-lin": {}, "short-int": DefaultCD, "long-min": kaiserLongMin, "xlong-int": {Length: ExtraLong, Phase: Intermediate},
		"gauss-short-int": {Family: Gaussian, Phase: Intermediate}, "halfband-min": {Family: HalfBand, Phase: Minimum}, "nonring": {Family: NonRinging},
		"short-int80": {Phase: Int80}, "xlong-int70": {Length: ExtraLong, Phase: Int70}, "gauss-long-int80": {Family: Gaussian, Length: Long, Phase: Int80},
		"slow-int70": {Family: Slow, Phase: Int70}} {
		if got, _ := ParseChoice(s); got != c {
			t.Errorf("%q read as %s", s, got)
		}
	}
	// the values a gapless state stored before the linear-leaning phases came
	if Linear != 0 || Intermediate != 1 || Minimum != 2 {
		t.Errorf("phase values moved: linear %d, intermediate %d, minimum %d", Linear, Intermediate, Minimum)
	}
	for _, s := range []string{"", "short", "lin", "medium-lin", "short-linear", "short_lin", "short-int,long-lin", "gauss-xlong-lin",
		"gauss-lin", "halfband-short-lin", "nonring-lin", "kaiser-short-lin", "slow", "short-int60", "short-int-80", "int80",
		"nonring-int80", "short-int 80", "short-int8"} {
		if _, err := ParseChoice(s); err == nil {
			t.Errorf("%q: taken", s)
		}
	}
}
