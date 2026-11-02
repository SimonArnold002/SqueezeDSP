package foxDSDReader

import (
	"encoding/binary"
	"math"
	"math/cmplx"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---- fixtures: a 2nd-order sigma-delta modulator, written as DSF and DSDIFF ----

// modulate returns 1-bit DSD (1 = +1) for x at the DSD rate, MSB-first bytes, its first bit the earliest.
func modulate(x []float64) []byte {
	out := make([]byte, (len(x)+7)/8)
	var u1, u2 float64
	y := 1.0
	for n, v := range x {
		u1 += v - y
		u2 += u1 - y
		if u2 >= 0 {
			y = 1
			out[n/8] |= 0x80 >> uint(n%8)
		} else {
			y = -1
		}
	}
	return out
}

// tone is amp*sin(2 pi f t) at the DSD rate for secs, starting at phase 0 at t = 0
func tone(rate int, secs, f, amp float64) []float64 {
	x := make([]float64, int(secs*float64(rate)))
	for n := range x {
		x[n] = amp * math.Sin(2*math.Pi*f*float64(n)/float64(rate))
	}
	return x
}

func writeDSF(t *testing.T, path string, rate int, chans [][]byte, bits int64) {
	t.Helper()
	le := binary.LittleEndian
	ch := len(chans)
	blocks := (len(chans[0]) + 4095) / 4096
	data := make([]byte, blocks*4096*ch)
	for b := 0; b < blocks; b++ {
		for c := 0; c < ch; c++ {
			for i := 0; i < 4096; i++ {
				if k := b*4096 + i; k < len(chans[c]) {
					data[(b*ch+c)*4096+i] = rev[chans[c][k]] // DSF is LSB first
				}
			}
		}
	}
	var h []byte
	h = append(h, "DSD "...)
	h = le.AppendUint64(h, 28)
	h = le.AppendUint64(h, uint64(28+52+12+len(data)))
	h = le.AppendUint64(h, 0)
	h = append(h, "fmt "...)
	h = le.AppendUint64(h, 52)
	h = le.AppendUint32(h, 1) // version
	h = le.AppendUint32(h, 0) // raw DSD
	h = le.AppendUint32(h, 2) // stereo (channel type; not read)
	h = le.AppendUint32(h, uint32(ch))
	h = le.AppendUint32(h, uint32(rate))
	h = le.AppendUint32(h, 1)
	h = le.AppendUint64(h, uint64(bits))
	h = le.AppendUint32(h, 4096)
	h = le.AppendUint32(h, 0)
	h = append(h, "data"...)
	h = le.AppendUint64(h, uint64(12+len(data)))
	if err := os.WriteFile(path, append(h, data...), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeDFF(t *testing.T, path string, rate int, chans [][]byte, cmpr string) {
	t.Helper()
	id := "DSD "
	if cmpr == "DST " {
		id = "DST "
	}
	writeDFFChunk(t, path, rate, chans, cmpr, id)
}

// writeDFFChunk names the CMPR property and the audio chunk separately: a DSDIFF can say DST in either
func writeDFFChunk(t *testing.T, path string, rate int, chans [][]byte, cmpr, id string) {
	t.Helper()
	be := binary.BigEndian
	ch := len(chans)
	chunk := func(id string, body []byte) []byte {
		b := append([]byte(id), be.AppendUint64(nil, uint64(len(body)))...)
		b = append(b, body...)
		if len(body)%2 == 1 {
			b = append(b, 0)
		}
		return b
	}
	chnl := be.AppendUint16(nil, uint16(ch))
	for c := 0; c < ch; c++ {
		chnl = append(chnl, []string{"SLFT", "SRGT", "C   ", "LFE ", "LS  ", "RS  "}[c]...)
	}
	cm := append([]byte(cmpr), 14)
	cm = append(cm, "not compressed"...)
	prop := []byte("SND ")
	prop = append(prop, chunk("FS  ", be.AppendUint32(nil, uint32(rate)))...)
	prop = append(prop, chunk("CHNL", chnl)...)
	prop = append(prop, chunk("CMPR", cm)...)
	data := make([]byte, len(chans[0])*ch)
	for i := range chans[0] {
		for c := 0; c < ch; c++ {
			data[i*ch+c] = chans[c][i]
		}
	}
	form := []byte("DSD ")
	form = append(form, chunk("FVER", []byte{1, 5, 0, 0})...)
	form = append(form, chunk("PROP", prop)...)
	form = append(form, chunk(id, data)...)
	if err := os.WriteFile(path, chunk("FRM8", form), 0o644); err != nil {
		t.Fatal(err)
	}
}

type fixture struct {
	rate int
	x    [][]float64 // what was modulated, per channel, at the DSD rate
	bits [][]byte
}

func makeFixture(rate int, secs float64, freqs []float64, amp float64) fixture {
	fx := fixture{rate: rate}
	for _, f := range freqs {
		var x []float64
		if f > 0 {
			x = tone(rate, secs, f, amp)
		} else {
			x = make([]float64, int(secs*float64(rate)))
		}
		fx.x = append(fx.x, x)
		fx.bits = append(fx.bits, modulate(x))
	}
	return fx
}

func decode(t *testing.T, r *Reader) [][]float64 {
	t.Helper()
	if err := r.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
	ch := make(chan [][]float64, 4)
	errc := make(chan error, 1)
	go func() { errc <- r.DecodeInput(ch); close(ch) }()
	out := make([][]float64, r.NumChannels)
	for b := range ch {
		for c := range out {
			out[c] = append(out[c], b[c]...)
		}
	}
	if err := <-errc; err != nil {
		t.Fatalf("DecodeInput: %v", err)
	}
	if int64(len(out[0])) != r.Frames || r.TotalSamples != r.Frames {
		t.Fatalf("sent %d frames (counted %d), Frames says %d", len(out[0]), r.TotalSamples, r.Frames)
	}
	return out
}

// levelDB is the level of the f Hz component in x (rate), in dBFS of a sine's peak, over whole cycles
// after skipping `skip` frames.
func levelDB(x []float64, rate int, f float64, skip int) float64 {
	return 20 * math.Log10(2*cmplx.Abs(dft(x, rate, f, skip)))
}

// dft is x's f Hz component (amplitude/2 and phase), over whole periods after skipping `skip` frames
func dft(x []float64, rate int, f float64, skip int) complex128 {
	p := period(rate, f) // frames in a whole number of cycles
	n := (len(x) - skip) / p * p
	var s complex128
	for i := 0; i < n; i++ {
		s += complex(x[skip+i], 0) * cmplx.Exp(complex(0, -2*math.Pi*f*float64(i)/float64(rate)))
	}
	return s / complex(float64(n), 0)
}

func maxDiff(a, b []float64) float64 {
	d := 0.0
	for i := range a {
		d = math.Max(d, math.Abs(a[i]-b[i]))
	}
	return d
}

const dsd64, dsd128 = 2822400, 5644800

// ---- the requirements ----

// The level is unity (a 0.5 modulation sine decodes at -6.02 dBFS, as ffmpeg, sox and dsdplay decode), and
// flat: at 1 kHz and 20 kHz the decode reads what the bitstream itself holds at that frequency over the
// same span (an ideal decoder), within 0.002 dB, on every cutoff and output rate. Against the bitstream,
// not 20 kHz against 1 kHz: this fixture's 2nd-order modulator puts +0.013 dB into its own 20 kHz.
func TestLevelAndFlatness(t *testing.T) {
	dir := t.TempDir()
	lo, hi := makeFixture(dsd64, 1.0, []float64{1000, 1000}, 0.5), makeFixture(dsd64, 1.0, []float64{20000, 20000}, 0.5)
	writeDSF(t, filepath.Join(dir, "lo.dsf"), dsd64, lo.bits, int64(len(lo.x[0])))
	writeDSF(t, filepath.Join(dir, "hi.dsf"), dsd64, hi.bits, int64(len(hi.x[0])))
	for _, rate := range []int{352800, 176400, 88200, 44100, 48000, 96000, 192000, 384000, 705600, 768000} {
		for _, cut := range []float64{0, 24000, 30000, 50000} {
			for _, fx := range []struct {
				name string
				f    float64
				bits []byte
			}{{"lo.dsf", 1000, lo.bits[0]}, {"hi.dsf", 20000, hi.bits[0]}} {
				if fx.f > 0.42*float64(rate) { // 44.1k's guard band starts at 18.5 kHz
					continue
				}
				a := decode(t, &Reader{Filename: filepath.Join(dir, fx.name), OutRate: rate, Cutoff: cut})
				skip := rate / 20
				// whole cycles that are also whole bits (a 48k-family frame is not a whole number of bits)
				p := period(rate, fx.f)
				p *= rate / gcd(rate, dsd64) / gcd(p, rate/gcd(rate, dsd64))
				n := (len(a[0]) - skip) / p * p
				got := dft(a[0][:skip+n], rate, fx.f, skip)
				want := bitsDFT(fx.bits, dsd64, fx.f, skip*dsd64/rate, (skip+n)*dsd64/rate)
				d := 20 * math.Log10(cmplx.Abs(got)/cmplx.Abs(want))
				if math.Abs(d) > 0.002 {
					t.Errorf("%d Hz cutoff %.0f: %.0f Hz decodes %.4f dB off the bitstream's own", rate, cut, fx.f, d)
				}
				if l := 20 * math.Log10(2*cmplx.Abs(got)); fx.f == 1000 && math.Abs(l+6.0206) > 0.02 {
					t.Errorf("%d Hz cutoff %.0f: 1 kHz at %.4f dBFS, want -6.02", rate, cut, l)
				}
			}
		}
	}
}

func period(rate int, f float64) int {
	a, b := rate, int(f)
	for b != 0 {
		a, b = b, a%b
	}
	return rate / a
}

// bitsDFT is the +-1 bitstream's f Hz component over bits [from, to): what an ideal decoder outputs
func bitsDFT(bits []byte, rate int, f float64, from, to int) complex128 {
	var s complex128
	w := cmplx.Exp(complex(0, -2*math.Pi*f/float64(rate)))
	z := cmplx.Exp(complex(0, -2*math.Pi*f/float64(rate)*float64(from)))
	for n := from; n < to; n++ {
		v := -1.0
		if bits[n/8]&(0x80>>uint(n%8)) != 0 {
			v = 1
		}
		s += complex(v, 0) * z
		z *= w
	}
	return s / complex(float64(to-from), 0)
}

// The filters' delay is removed: frame k is the input at time k/rate. The fixture's modulator passes its
// input with no delay (STF 1), so the decoded 1 kHz has the input sine's phase: within 1/100 frame.
func TestAlignment(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		rate, out int
		cut       float64
	}{{dsd64, 352800, 30000}, {dsd64, 88200, 0}, {dsd128, 352800, 24000}, {dsd128, 705600, 50000},
		{dsd64, 96000, 30000}, {dsd64, 705600, 0}, {dsd128, 384000, 24000}, {dsd64, 48000, 50000}, {dsd128, 768000, 0}} {
		fx := makeFixture(tc.rate, 0.5, []float64{1000, 1000}, 0.5)
		path := filepath.Join(dir, "a.dsf")
		writeDSF(t, path, tc.rate, fx.bits, int64(len(fx.x[0])))
		got := decode(t, &Reader{Filename: path, OutRate: tc.out, Cutoff: tc.cut})
		skip := tc.out / 20
		ref := make([]float64, len(got[0]))
		for k := range ref {
			ref[k] = 0.5 * math.Sin(2*math.Pi*1000*float64(k)/float64(tc.out))
		}
		dphi := cmplx.Phase(dft(got[0], tc.out, 1000, skip) / dft(ref, tc.out, 1000, skip))
		if off := dphi / (2 * math.Pi * 1000) * float64(tc.out); math.Abs(off) > 0.01 {
			t.Errorf("DSD %d -> %d cutoff %.0f: %.4f frames off the input's time", tc.rate, tc.out, tc.cut, off)
		}
		if want := int64(len(fx.bits[0])) * 8 * int64(tc.out) / int64(tc.rate); int64(len(got[0])) != want {
			t.Errorf("DSD %d -> %d: %d frames, want %d (one per whole frame of input)", tc.rate, tc.out, len(got[0]), want)
		}
	}
}

// Stage 3 adds nothing: a frame is the 352.8k decode's signal (the same stage 2) at that frame's own time,
// within 1e-6 (-120 dB re the tone). The reference is an independent Kaiser-windowed sinc over 129 of the
// 352.8k samples, exact to about 1e-7 on a signal stage 2 has left under 60 kHz. Timing jitter (a phase
// off by one) keeps the mean phase and the level, so only this sees it.
func TestStage3IsTheSignalAtEachFrame(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		rate, out int
		cut       float64
	}{{dsd64, 96000, 30000}, {dsd64, 192000, 24000}, {dsd128, 384000, 50000}, {dsd64, 705600, 30000}, {dsd64, 768000, 24000}} {
		fx := makeFixture(tc.rate, 0.3, []float64{1000, 19000}, 0.5)
		path := filepath.Join(dir, "i.dsf")
		writeDSF(t, path, tc.rate, fx.bits, int64(len(fx.x[0])))
		ref := decode(t, &Reader{Filename: path, OutRate: DXD, Cutoff: tc.cut})
		got := decode(t, &Reader{Filename: path, OutRate: tc.out, Cutoff: tc.cut})
		const w, beta = 64, 14.47 // half width in 352.8k samples; beta for 140 dB
		g := gcd(tc.out, DXD)
		up, down := tc.out/g, DXD/g
		weights := map[int][]float64{} // by phase: the fraction of a 352.8k sample past n
		worst, compared := 0.0, 0
		for k := range got[0] {
			n, p := k*down/up, k*down%up
			if n-w < 0 || n+w+1 >= len(ref[0]) {
				continue
			}
			compared++
			wt, ok := weights[p]
			if !ok {
				d := float64(p) / float64(up)
				for i := -w; i <= w+1; i++ {
					x := float64(i) - d
					s := 1.0
					if x != 0 {
						s = math.Sin(math.Pi*x) / (math.Pi * x)
					}
					q := x / (w + 1)
					wt = append(wt, s*bessel0(beta*math.Sqrt(math.Max(0, 1-q*q)))/bessel0(beta))
				}
				weights[p] = wt
			}
			for c := range got {
				v := 0.0
				for i, h := range wt {
					v += h * ref[c][n-w+i]
				}
				worst = math.Max(worst, math.Abs(v-got[c][k]))
			}
		}
		t.Logf("DSD %d -> %d cutoff %.0f: %d of %d frames, worst %.2g", tc.rate, tc.out, tc.cut, compared, len(got[0]), worst)
		if compared < len(got[0])*9/10 || len(weights) != up {
			t.Errorf("DSD %d -> %d: compared %d of %d frames over %d of %d phases", tc.rate, tc.out, compared, len(got[0]), len(weights), up)
		}
		if worst > 1e-6 {
			t.Errorf("DSD %d -> %d cutoff %.0f: a frame is %.2g (%.1f dB) off the 352.8k signal at its time", tc.rate, tc.out, tc.cut, worst, 20*math.Log10(worst))
		}
	}
}

// DSF and DSDIFF holding the same bits decode to the same samples.
func TestDSFEqualsDFF(t *testing.T) {
	dir := t.TempDir()
	fx := makeFixture(dsd64, 0.7, []float64{1000, 3000}, 0.4)
	writeDSF(t, filepath.Join(dir, "a.dsf"), dsd64, fx.bits, int64(len(fx.x[0])))
	writeDFF(t, filepath.Join(dir, "a.dff"), dsd64, fx.bits, "DSD ")
	a := decode(t, &Reader{Filename: filepath.Join(dir, "a.dsf"), OutRate: 352800, Cutoff: 30000})
	b := decode(t, &Reader{Filename: filepath.Join(dir, "a.dff"), OutRate: 352800, Cutoff: 30000})
	if len(a[0]) != len(b[0]) {
		t.Fatalf("DSF %d frames, DFF %d", len(a[0]), len(b[0]))
	}
	for c := range a {
		if d := maxDiff(a[c], b[c]); d != 0 {
			t.Errorf("channel %d: DSF and DFF differ by %g", c, d)
		}
	}
}

// A seek gives the full decode's samples from that frame on, to the bit (it runs on the full decode's FFT
// block grid), and a track cut by --until followed by one from the same point is the full decode. Several
// seek points, so one that happens to fall on a block boundary cannot pass for all.
func TestSeekAndUntil(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		rate, out int
		cut       float64
		ext       string
	}{{dsd64, 352800, 30000, "dsf"}, {dsd64, 88200, 24000, "dff"}, {dsd128, 352800, 0, "dsf"}, {dsd128, 176400, 50000, "dff"},
		{dsd64, 96000, 30000, "dsf"}, {dsd128, 384000, 0, "dff"}, {dsd64, 705600, 24000, "dsf"}, {dsd64, 48000, 0, "dff"}} {
		fx := makeFixture(tc.rate, 1.5, []float64{1000, 2500}, 0.5)
		path := filepath.Join(dir, "s."+tc.ext)
		if tc.ext == "dsf" {
			writeDSF(t, path, tc.rate, fx.bits, int64(len(fx.x[0])))
		} else {
			writeDFF(t, path, tc.rate, fx.bits, "DSD ")
		}
		full := decode(t, &Reader{Filename: path, OutRate: tc.out, Cutoff: tc.cut})
		for _, ms := range []int{3, 617, 1010, 1370} { // not on a DSF block or an FFT block, and one inside the first
			at := time.Duration(ms) * time.Millisecond
			k := int(math.Round(at.Seconds() * float64(tc.out)))
			head := decode(t, &Reader{Filename: path, OutRate: tc.out, Cutoff: tc.cut, EndTime: at})
			tail := decode(t, &Reader{Filename: path, OutRate: tc.out, Cutoff: tc.cut, StartTime: at})
			if len(head[0]) != k || len(head[0])+len(tail[0]) != len(full[0]) {
				t.Fatalf("%s %d -> %d at %v: head %d (want %d) + tail %d != full %d", tc.ext, tc.rate, tc.out, at, len(head[0]), k, len(tail[0]), len(full[0]))
			}
			for c := range full {
				if d := maxDiff(head[c], full[c][:k]); d != 0 {
					t.Errorf("%s %d -> %d at %v channel %d: --until's samples differ from the full decode by %g", tc.ext, tc.rate, tc.out, at, c, d)
				}
				if d := maxDiff(tail[c], full[c][k:]); d != 0 {
					t.Errorf("%s %d -> %d at %v channel %d: the seek's samples differ from the full decode by %g", tc.ext, tc.rate, tc.out, at, c, d)
				}
			}
		}
	}
}

// The designed filters: stage 2 flat to 20 kHz within 0.001 dB and -149 dB past its stop edge; stage 1
// flat to 80 kHz and -159 dB from mid-80 kHz to the DSD Nyquist.
func TestFilterResponse(t *testing.T) {
	dir := t.TempDir()
	fx := makeFixture(dsd128, 0.05, []float64{0, 0}, 0)
	path := filepath.Join(dir, "z.dsf")
	writeDSF(t, path, dsd128, fx.bits, int64(len(fx.x[0])))
	resp := func(h []float64, fs, f float64) float64 {
		var s complex128
		for n, v := range h {
			s += complex(v, 0) * cmplx.Exp(complex(0, -2*math.Pi*f/fs*float64(n)))
		}
		return 20 * math.Log10(cmplx.Abs(s)+1e-300)
	}
	for _, cut := range []float64{24000, 30000, 50000} {
		r := &Reader{Filename: path, OutRate: 352800, Cutoff: cut}
		if err := r.Open(); err != nil {
			t.Fatal(err)
		}
		r.Close()
		tw := math.Min(2*(cut-20000), 20000)
		for f := 0.0; f <= 20000; f += 500 {
			if g := resp(r.h2, DXD, f); math.Abs(g) > 0.001 {
				t.Errorf("cutoff %.0f: stage 2 at %.0f Hz %.5f dB", cut, f, g)
			}
		}
		for f := cut + tw/2; f <= DXD/2; f += 997 {
			if g := resp(r.h2, DXD, f); g > -149 {
				t.Errorf("cutoff %.0f: stage 2 at %.0f Hz %.1f dB", cut, f, g)
			}
		}
		if cut == 30000 {
			for f := 0.0; f <= 80000; f += 2000 {
				if g := resp(r.h1, dsd128, f); math.Abs(g) > 0.001 {
					t.Errorf("stage 1 at %.0f Hz %.5f dB", f, g)
				}
			}
			for f := float64(DXD - 80000); f <= dsd128/2; f += 9973 {
				if g := resp(r.h1, dsd128, f); g > -159 {
					t.Errorf("stage 1 at %.0f Hz %.1f dB", f, g)
				}
			}
		}
	}

	// Stage 3, at up*352.8k: flat within 0.001 dB to stage 2's stop edge (the band stage 2 leaves), -150 dB from
	// 352.8k less that edge, where the first image starts, to its Nyquist. And stage 2 ends at that edge: the
	// target's guard band, a cutoff, or (above 192k) 100 kHz past stage 1's flat 80k. Stage 2's depth there is
	// the 44.1k family's own design, which reaches -146.5 dB at 44.1k's edge (ledger): -146 here pins the edge.
	for _, tc := range []struct {
		dsd, out  int
		cut, edge float64
	}{{dsd64, 96000, 30000, 40000}, {dsd64, 48000, 0, 24000}, {dsd64, 192000, 0, 96000}, {dsd64, 384000, 0, 100000},
		{dsd128, 384000, 50000, 60000}, {dsd64, 768000, 24000, 28000}, {dsd128, 768000, 0, 100000}, {dsd64, 705600, 0, 100000}} {
		fx := makeFixture(tc.dsd, 0.05, []float64{0, 0}, 0)
		p := filepath.Join(dir, "s3.dsf")
		writeDSF(t, p, tc.dsd, fx.bits, int64(len(fx.x[0])))
		r := &Reader{Filename: p, OutRate: tc.out, Cutoff: tc.cut}
		if err := r.Open(); err != nil {
			t.Fatal(err)
		}
		r.Close()
		if r.up == 0 {
			t.Errorf("%d from DSD %d: no stage 3", tc.out, tc.dsd)
			continue
		}
		for f := 0.0; f <= 20000; f += 500 {
			if g := resp(r.h2, DXD, f); math.Abs(g) > 0.001 {
				t.Errorf("%d cutoff %.0f: stage 2 at %.0f Hz %.5f dB", tc.out, tc.cut, f, g)
			}
		}
		for f := tc.edge; f <= DXD/2; f += 997 {
			if g := resp(r.h2, DXD, f); g > -146 {
				t.Errorf("%d cutoff %.0f: stage 2 at %.0f Hz %.1f dB", tc.out, tc.cut, f, g)
			}
		}
		L := r.up
		proto := make([]float64, 2*L*r.ki+1) // tap up*t-p is phase p's t
		for ph := range r.ph {
			for k, v := range r.ph[ph] {
				if i := L*k - ph; i >= 0 {
					proto[i] = v / float64(L)
				}
			}
		}
		fs := float64(L * DXD)
		for f := 0.0; f <= tc.edge; f += tc.edge / 40 {
			if g := resp(proto, fs, f); math.Abs(g) > 0.001 {
				t.Errorf("%d cutoff %.0f: stage 3 at %.0f Hz %.5f dB", tc.out, tc.cut, f, g)
			}
		}
		for f, n := DXD-tc.edge, 0; f <= fs/2; n++ { // close steps near the edge, where a Kaiser's ripple peaks
			if g := resp(proto, fs, f); g > -150 {
				t.Errorf("%d cutoff %.0f: stage 3 at %.0f Hz %.1f dB", tc.out, tc.cut, f, g)
			}
			if n < 100 {
				f += 997
			} else {
				f += math.Max(997, (fs/2-DXD)/1500)
			}
		}
	}
}

func TestGain(t *testing.T) {
	dir := t.TempDir()
	fx := makeFixture(dsd64, 0.5, []float64{1000, 1000}, 0.25)
	path := filepath.Join(dir, "g.dsf")
	writeDSF(t, path, dsd64, fx.bits, int64(len(fx.x[0])))
	a := decode(t, &Reader{Filename: path, OutRate: 352800, Cutoff: 30000})
	b := decode(t, &Reader{Filename: path, OutRate: 352800, Cutoff: 30000, GainDB: 6})
	la, lb := levelDB(a[0], 352800, 1000, 17640), levelDB(b[0], 352800, 1000, 17640)
	if math.Abs(lb-la-6) > 0.001 {
		t.Errorf("gain 6: %.4f -> %.4f dB", la, lb)
	}
}

// Three channels: the odd one rides alone in its complex pair, and no pair leaks into its partner.
func TestThreeChannels(t *testing.T) {
	dir := t.TempDir()
	fx := makeFixture(dsd64, 0.5, []float64{1000, 0, 2000}, 0.5)
	path := filepath.Join(dir, "m.dsf")
	writeDSF(t, path, dsd64, fx.bits, int64(len(fx.x[0])))
	for _, rate := range []int{176400, 96000} { // stage 3 keeps each channel's own history
		out := decode(t, &Reader{Filename: path, OutRate: rate, Cutoff: 24000})
		if len(out) != 3 {
			t.Fatalf("%d channels", len(out))
		}
		if l := levelDB(out[0], rate, 1000, rate/20); math.Abs(l+6.0206) > 0.02 {
			t.Errorf("%d channel 0: 1 kHz at %.3f", rate, l)
		}
		if l := levelDB(out[2], rate, 2000, rate/20); math.Abs(l+6.0206) > 0.02 {
			t.Errorf("%d channel 2: 2 kHz at %.3f", rate, l)
		}
		if l := levelDB(out[1], rate, 1000, rate/20); l > -100 {
			t.Errorf("%d channel 1 (silent) shows channel 0's 1 kHz at %.1f dB", rate, l)
		}
	}
}

// A file cut short plays what it holds: a DSF its whole blocks, a DSDIFF its bytes.
func TestCutShort(t *testing.T) {
	dir := t.TempDir()
	fx := makeFixture(dsd64, 1.0, []float64{1000, 1000}, 0.5)
	dsf, dff := filepath.Join(dir, "c.dsf"), filepath.Join(dir, "c.dff")
	writeDSF(t, dsf, dsd64, fx.bits, int64(len(fx.x[0])))
	writeDFF(t, dff, dsd64, fx.bits, "DSD ")
	for _, p := range []string{dsf, dff} {
		st, _ := os.Stat(p)
		if err := os.Truncate(p, st.Size()-50000); err != nil {
			t.Fatal(err)
		}
	}
	a := decode(t, &Reader{Filename: dsf, OutRate: 352800})
	b := decode(t, &Reader{Filename: dff, OutRate: 352800})
	full := int64(len(fx.bits[0]))
	if want := (full*2 - 50000) / (2 * 4096) * 4096; int64(len(a[0])) != want {
		t.Errorf("cut DSF: %d frames, want %d (its whole blocks)", len(a[0]), want)
	}
	if want := (full*2 - 50000) / 2; int64(len(b[0])) != want {
		t.Errorf("cut DFF: %d frames, want %d", len(b[0]), want)
	}
}

// What is not ours is refused with a reason: DST (ffmpeg decodes it), a 48 kHz-family DSD rate, a target
// under 44.1k, over 768k or no real rate, a cutoff at or under 20 kHz.
func TestRefused(t *testing.T) {
	dir := t.TempDir()
	fx := makeFixture(dsd64, 0.05, []float64{1000, 1000}, 0.5)
	dst, d48, ok := filepath.Join(dir, "x.dff"), filepath.Join(dir, "x48.dsf"), filepath.Join(dir, "ok.dsf")
	dstProp, dstChunk := filepath.Join(dir, "prop.dff"), filepath.Join(dir, "chunk.dff")
	writeDFF(t, dst, dsd64, fx.bits, "DST ")
	writeDFFChunk(t, dstProp, dsd64, fx.bits, "DST ", "DSD ")  // only CMPR says DST
	writeDFFChunk(t, dstChunk, dsd64, fx.bits, "DSD ", "DST ") // only the chunk does
	writeDSF(t, d48, 3072000, fx.bits, int64(len(fx.x[0])))
	writeDSF(t, ok, dsd64, fx.bits, int64(len(fx.x[0])))
	for _, tc := range []struct {
		r    *Reader
		want string
	}{
		{&Reader{Filename: dst, OutRate: 352800}, "ffmpeg decodes it"},
		{&Reader{Filename: dstProp, OutRate: 352800}, "ffmpeg decodes it"},
		{&Reader{Filename: dstChunk, OutRate: 352800}, "ffmpeg decodes it"},
		{&Reader{Filename: d48, OutRate: 352800}, "44.1 kHz family"},
		{&Reader{Filename: ok, OutRate: 22050}, "no 22050 Hz"},
		{&Reader{Filename: ok, OutRate: 32000}, "no 32000 Hz"},
		{&Reader{Filename: ok, OutRate: 800000}, "no 800000 Hz"},
		{&Reader{Filename: ok, OutRate: 44101}, "no 44101 Hz"},
		{&Reader{Filename: ok, OutRate: 352800, Cutoff: 20000}, "cutoff"},
		{&Reader{Filename: ok, OutRate: 352800, Cutoff: 60001}, "cutoff"},
	} {
		err := tc.r.Open()
		tc.r.Close()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s at %d cutoff %.0f: got %v, want an error with %q", filepath.Base(tc.r.Filename), tc.r.OutRate, tc.r.Cutoff, err, tc.want)
		}
	}
	// controls: the same file at a target it can reach opens, at the cutoff range's top too, and the targets
	// once refused (the 48k family, 705.6k from DSD64) are taken
	if r := (&Reader{Filename: ok, OutRate: 352800, Cutoff: 60000}); r.Open() != nil {
		t.Errorf("control: cutoff 60000 refused")
	} else {
		r.Close()
	}
	for _, rate := range []int{352800, 44100, 48000, 96000, 705600, 768000} {
		r := &Reader{Filename: ok, OutRate: rate}
		if err := r.Open(); err != nil {
			t.Errorf("control: %v", err)
		}
		r.Close()
	}
}
