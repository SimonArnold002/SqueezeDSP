package foxDSDEncoder

// The encoder's spec bench (2026-11-03; Simon: "can we have test bench kept so we dont have to rebuild one each
// time. I want to see each rate via each method in band noise level and out of band levels and where they rise
// up", then "also be good to run a test using one of each of our DSF files to see what it reads for real music via
// the engine"). Skipped unless asked for; `sh engine/encoder-spec.sh` runs it with build.sh's environment:
//
//	go test ./foxAudioEncoder/foxDSDEncoder/ -run TestSpec -v -count=1 -timeout 90m -args -spec \
//	    [-specout=report.md] [-specmusic=a.dsf,b.dsf] [-specstart=30] [-specsecs=10] [-specallrates] [-specwrite=dir]
//
// The path measured is the shipped one: Encoder.Open, EncodeData and Close (the limit, the interpolator, the loop,
// the pair, the DSF packing); the DSF's left channel is read back as +-1. The analysis is in double precision: a
// Kaiser beta 38 window over 2^24 points, a radix-2 FFT with every twiddle from a table computed directly (a
// recurrence drifts to about -180 dB). dB re a full-scale sine; a band's figure is the noise integrated over it. A
// self-check runs first: a pure float64 sine must read under -250 dB in band, white noise of known power within 0.1
// dB of its figure.
//
// Tones: 1 kHz at 0.25 (SACD -6 dB) and 0.5 (SACD 0 dB) from 352.8k PCM, left +, right -, the tone's own bins left
// out of the noise. Stability: the engine's own count of loop resets for steady and starting signals at the limit.
// Music: a DSF decoded by foxDSDReader as the engine decodes for DSD out (352.8k, the 30 kHz low-pass, gain 0) and
// encoded at its own rate (every rate with -specallrates); the noise is the loop's output less its own input (the
// PCM through the same interpolator), so the music cancels and what is left is what the modulator added (Welch, up
// to 8 frames). -specwrite writes each music DSF, to compare with the engine's own output (cmp).

import (
	"crypto/sha1"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Foxenfurter/foxAudioLib/foxAudioDecoder/foxDSDReader"
)

var (
	specRun      = flag.Bool("spec", false, "run the encoder spec bench (TestSpec, TestSpecMusic)")
	specOut      = flag.String("specout", "", "also write the bench's tables to this Markdown file")
	specMusic    = flag.String("specmusic", "", "DSF files, comma separated, for TestSpecMusic")
	specStart    = flag.Float64("specstart", 30, "seconds into each music file")
	specSecs     = flag.Float64("specsecs", 10, "seconds of each music file")
	specAllRates = flag.Bool("specallrates", false, "encode each music file at every DSD rate, not only its own")
	specWrite    = flag.String("specwrite", "", "write each music DSF here (to compare with the engine's)")
)

const (
	specN     = 1 << 24
	specPCM   = 352800
	specBeta  = 38.0
	specFrame = 8 // music: Welch frames at most
)

var (
	specRates = []int{DSD64, 2 * DSD64, 4 * DSD64, 8 * DSD64}
	specKinds = []string{"standard", "clans", "adaptive"}
	// specMethods: each modulator for each source: a DSD source's designs from 352.8k (the DSD path's DSP rate), a
	// PCM source's from 705.6k (the converter's: our upsampler, then the DSP)
	specMethods = []specMethod{
		{"standard", "dsd", 352800}, {"clans", "dsd", 352800}, {"adaptive", "dsd", 352800},
		{"standard", "pcm", 705600}, {"clans", "pcm", 705600}, {"adaptive", "pcm", 705600},
	}
	specLevels = []float64{0.25, 0.5}
	specRise   = []float64{-140, -120, -100, -80, -60, -40}
	specOcts   = []float64{20e3, 40e3, 80e3, 160e3, 320e3, 640e3, 1.28e6, 2.56e6, 5.12e6, 1e9}
	specTitles = []string{"20-40k", "40-80k", "80-160k", "160-320k", "320-640k", "0.64-1.28M", "1.28-2.56M", "2.56-5.12M", "5.12M up"}
	specMD     strings.Builder
	specOpened bool
)

func specName(rate int) string { return fmt.Sprintf("DSD%d", rate/44100) }

type specMethod struct {
	kind, src string
	in        int // the PCM rate in
}

func (m specMethod) String() string {
	if m.src == "pcm" {
		return "pcm " + m.kind
	}
	return m.kind
}

func (m specMethod) short() string {
	if m.src == "pcm" {
		return "p" + m.kind[:3]
	}
	return m.kind[:3]
}

func specHz(f float64) string {
	if f >= 1e6 {
		return fmt.Sprintf("%.2fM", f/1e6)
	}
	return fmt.Sprintf("%.0fk", f/1e3)
}

func specDB(v float64) string {
	if math.IsNaN(v) {
		return "-"
	}
	return fmt.Sprintf("%.1f", v)
}

// specFlush: the section written so far, to stdout and (appended) to -specout
func specFlush(t *testing.T) {
	s := specMD.String()
	specMD.Reset()
	fmt.Print(s)
	if *specOut == "" {
		return
	}
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if !specOpened {
		flags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
		specOpened = true
	}
	f, err := os.OpenFile(*specOut, flags, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
}

// ---- the analysis ----

type specAna struct {
	n   int
	win []float64
	sw2 float64
	tw  []complex128
	a   []complex128
	acc []float64 // one-sided power per bin, summed over the frames added
	nfr int
}

func newSpecAna(n int) *specAna {
	s := &specAna{n: n, win: make([]float64, n), tw: make([]complex128, n/2), a: make([]complex128, n), acc: make([]float64, n/2)}
	d := bessel0(specBeta)
	for i := range s.win {
		r := 2*float64(i)/float64(n-1) - 1
		s.win[i] = bessel0(specBeta*math.Sqrt(math.Max(0, 1-r*r))) / d
		s.sw2 += s.win[i] * s.win[i]
	}
	for k := range s.tw {
		sn, c := math.Sincos(-2 * math.Pi * float64(k) / float64(n))
		s.tw[k] = complex(c, sn)
	}
	return s
}

func (s *specAna) reset() {
	clear(s.acc)
	s.nfr = 0
}

// add: s.n values windowed and transformed, their power added; summed over a band, 4 |X|^2 / (N sum w^2) is the
// band's mean square re a full-scale sine's
func (s *specAna) add(x func(i int) float64) {
	for i := 0; i < s.n; i++ {
		s.a[i] = complex(x(i)*s.win[i], 0)
	}
	n := s.n
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			s.a[i], s.a[j] = s.a[j], s.a[i]
		}
	}
	for l := 2; l <= n; l <<= 1 {
		h, step := l/2, n/l
		for k := 0; k < n; k += l {
			for m := 0; m < h; m++ {
				u, v := s.a[k+m], s.a[k+m+h]*s.tw[m*step]
				s.a[k+m], s.a[k+m+h] = u+v, u-v
			}
		}
	}
	sc := 4 / (float64(s.n) * s.sw2)
	for k := range s.acc {
		re, im := real(s.a[k]), imag(s.a[k])
		s.acc[k] += (re*re + im*im) * sc
	}
	s.nfr++
}

// band: dB over [lo, hi), the tone's lobe left out when tone > 0; NaN for a band past Nyquist
func (s *specAna) band(rate, lo, hi, tone float64) float64 {
	df := rate / float64(s.n)
	if lo >= rate/2 {
		return math.NaN()
	}
	tb, lobe := int(math.Round(tone/df)), int(math.Ceil(specBeta/math.Pi))+4
	sum := 0.0
	for k := max(1, int(math.Ceil(lo/df))); k < len(s.acc) && float64(k)*df < hi; k++ {
		if tone > 0 && k >= tb-lobe && k <= tb+lobe {
			continue
		}
		sum += s.acc[k]
	}
	return 10 * math.Log10(sum/float64(s.nfr)+1e-300)
}

func (s *specAna) tone(rate, f float64) float64 {
	df := rate / float64(s.n)
	tb, lobe := int(math.Round(f/df)), int(math.Ceil(specBeta/math.Pi))+4
	sum := 0.0
	for k := tb - lobe; k <= tb+lobe; k++ {
		sum += s.acc[k]
	}
	return 10 * math.Log10(sum/float64(s.nfr))
}

// thirds: third-octave bands (base 2, centres 1000 * 2^(k/3)) from 20 Hz to Nyquist: lo, centre, hi
func specThirds(rate float64) [][3]float64 {
	var b [][3]float64
	for k := -17; ; k++ {
		c := 1000 * math.Pow(2, float64(k)/3)
		lo, hi := c*math.Pow(2, -1.0/6), c*math.Pow(2, 1.0/6)
		if hi > rate/2 {
			return b
		}
		b = append(b, [3]float64{lo, c, hi})
	}
}

// specResult: one measurement's figures
type specResult struct {
	inband, sub [4]float64 // inband[0] the 20 Hz-20 kHz figure; sub: 20-2k, 2k-10k, 10k-20k
	oct         []float64
	thirds      map[float64]float64 // third-octave centre -> dB
	rise        []string            // per specRise threshold: the first third-octave centre from 20 kHz up that reaches it
}

// result: the figures; asIs: a bitstream read with its music in it, so the rise is looked for from 20 kHz up only
func (s *specAna) result(rate, tone float64, asIs bool) specResult {
	var r specResult
	r.inband[0] = s.band(rate, 20, 20000, tone)
	r.sub[0], r.sub[1], r.sub[2] = s.band(rate, 20, 2000, tone), s.band(rate, 2000, 10000, tone), s.band(rate, 10000, 20000, tone)
	for i := 0; i+1 < len(specOcts); i++ {
		r.oct = append(r.oct, s.band(rate, specOcts[i], math.Min(specOcts[i+1], rate/2), tone))
	}
	r.thirds = map[float64]float64{}
	th := specThirds(rate)
	for _, b := range th {
		r.thirds[b[1]] = s.band(rate, b[0], b[2], tone)
	}
	for _, lim := range specRise {
		got := "-"
		inBand := false
		for _, b := range th {
			if !asIs && b[1] < 20000 && r.thirds[b[1]] >= lim {
				inBand = true
			}
		}
		if inBand {
			got = "in band"
		} else {
			for _, b := range th {
				if b[1] >= 20000 && r.thirds[b[1]] >= lim {
					got = specHz(b[1])
					break
				}
			}
		}
		r.rise = append(r.rise, got)
	}
	return r
}

// ---- the encoder, as shipped ----

// specEncode: x through Encoder; the DSF, and the encoder's counts
func specEncode(t *testing.T, rate int, m specMethod, x [][]float64) ([]byte, *Encoder) {
	e := &Encoder{DSDRate: rate, InRate: m.in, NumChannels: len(x), Frames: int64(len(x[0])), Kind: m.kind, Source: m.src}
	b := encode(t, e, x, 1<<15)
	if string(b[80:84]) != "data" {
		t.Fatalf("no data chunk at 80")
	}
	return b, e
}

// specBit: DSD sample i of the left channel as +-1 (the data from byte 92, 4096-byte blocks per channel in turn,
// the oldest bit in the LSB)
func specBit(b []byte, chans int, i int) float64 {
	per := blockSize * 8
	k := 92 + (i/per)*blockSize*chans + (i%per)/8
	return float64((b[k]>>uint(i%8))&1)*2 - 1
}

func specSha(name string) string {
	b, err := os.ReadFile(name)
	if err != nil {
		return "?"
	}
	return fmt.Sprintf("%x", sha1.Sum(b))[:8]
}

func specHeader(t *testing.T, a *specAna, title string) {
	fmt.Fprintf(&specMD, "# DSD encoder spec bench: %s\n\n%s. Sources: tables.go %s, modulator.go %s, adaptive.go %s, interp.go %s, encoder.go %s.\n\n",
		title, time.Now().Format("2006-01-02 15:04"), specSha("tables.go"), specSha("modulator.go"), specSha("adaptive.go"), specSha("interp.go"), specSha("encoder.go"))
	// self-check: the analysis itself, at DSD256
	rate := float64(4 * DSD64)
	a.reset()
	a.add(func(i int) float64 { return 0.5 * math.Sin(2*math.Pi*1000*float64(i)/rate) })
	pure, lvl := a.band(rate, 20, 20000, 1000), a.tone(rate, 1000)
	rng := rand.New(rand.NewSource(1))
	sigma := 1e-3
	a.reset()
	a.add(func(i int) float64 { return sigma * rng.NormFloat64() })
	white, want := a.band(rate, 20, 20000, 0), 10*math.Log10(sigma*sigma*(20000-20)/(rate/2)/0.5)
	fmt.Fprintf(&specMD, "Self-check (DSD256): a pure float64 sine at 0.5 reads %.3f dB, its in-band noise %.1f dB (must be under -250); white noise reads %.2f dB in band, %.2f expected.\n\n", lvl, pure, white, want)
	if pure > -250 || math.Abs(white-want) > 0.1 || math.Abs(lvl+6.0206) > 0.001 {
		specFlush(t)
		t.Fatal("the analysis fails its self-check")
	}
}

// ---- tones ----

func TestSpec(t *testing.T) {
	if !*specRun {
		t.Skip("the spec bench: sh engine/encoder-spec.sh, or -args -spec")
	}
	a := newSpecAna(specN)
	specHeader(t, a, "tones and stability")
	type key struct {
		rate   int
		method string
		level  float64
	}
	res := map[key]specResult{}
	note := map[key]string{}
	for _, level := range specLevels {
		for _, rate := range specRates {
			settle := rate / 10
			std := map[string][]byte{}
			for _, m := range specMethods {
				L := rate / m.in
				frames := (settle+specN)/L + 256
				x := [][]float64{make([]float64, frames), make([]float64, frames)}
				for i := range x[0] {
					x[0][i] = level * math.Sin(2*math.Pi*1000*float64(i)/float64(m.in))
					x[1][i] = -x[0][i]
				}
				b, e := specEncode(t, rate, m, x)
				k := key{rate, m.String(), level}
				if m.kind == "standard" {
					std[m.src] = b
				}
				if m.kind == "adaptive" && rate != DSD64 {
					if string(b) == string(std[m.src]) {
						note[k] = "= standard, bit for bit"
						res[k] = res[key{rate, specMethod{"standard", m.src, m.in}.String(), level}]
						continue
					}
					note[k] = "DIFFERS from standard"
				}
				a.reset()
				a.add(func(i int) float64 { return specBit(b, 2, settle+i) })
				r := a.result(float64(rate), 1000, false)
				if lv := a.tone(float64(rate), 1000); math.Abs(lv-20*math.Log10(level)) > 0.01 {
					t.Errorf("%s %s at %.2f: the tone reads %.3f dB", specName(rate), m, level, lv)
				}
				res[k] = r
				if m.kind == "adaptive" && rate == DSD64 {
					note[k] = fmt.Sprintf("safe design %.0f%% of samples", 100*float64(e.InSafe)/float64(2*e.count))
				}
				if e.Resets > 0 {
					note[k] += fmt.Sprintf(" %d RESETS", e.Resets)
				}
			}
		}
	}
	fmt.Fprintf(&specMD, "## Tones: in band (20 Hz-20 kHz), dB re full scale\n\n1 kHz, the tone's bins left out: a DSD source's designs from 352.8k PCM, a PCM source's (\"pcm\") from 705.6k. Sub-bands at SACD 0 dB.\n\n")
	fmt.Fprintf(&specMD, "| rate | method | at -6 dB (0.25) | at SACD 0 dB (0.5) | 20 Hz-2 kHz | 2-10 kHz | 10-20 kHz | note |\n|---|---|---|---|---|---|---|---|\n")
	for _, rate := range specRates {
		for _, m := range specMethods {
			lo, hi := res[key{rate, m.String(), 0.25}], res[key{rate, m.String(), 0.5}]
			n := strings.TrimSpace(note[key{rate, m.String(), 0.5}])
			if n2 := strings.TrimSpace(note[key{rate, m.String(), 0.25}]); n2 != n && n2 != "" {
				n = "0.5: " + n + "; 0.25: " + n2
			}
			fmt.Fprintf(&specMD, "| %s | %s | %s | %s | %s | %s | %s | %s |\n", specName(rate), m, specDB(lo.inband[0]), specDB(hi.inband[0]),
				specDB(hi.sub[0]), specDB(hi.sub[1]), specDB(hi.sub[2]), n)
		}
	}
	for _, level := range specLevels {
		fmt.Fprintf(&specMD, "\n## Tones: out of band at %.2f (%s), per octave, dB re full scale\n\n| rate | method | %s |\n|---|---|%s\n",
			level, map[float64]string{0.25: "SACD -6 dB", 0.5: "SACD 0 dB"}[level], strings.Join(specTitles, " | "), strings.Repeat("---|", len(specTitles)))
		for _, rate := range specRates {
			for _, m := range specMethods {
				r := res[key{rate, m.String(), level}]
				cells := []string{}
				for _, v := range r.oct {
					cells = append(cells, specDB(v))
				}
				fmt.Fprintf(&specMD, "| %s | %s | %s |\n", specName(rate), m, strings.Join(cells, " | "))
			}
		}
	}
	fmt.Fprintf(&specMD, "\n## Tones: where the noise rises\n\nThe first third-octave band (centre) from 20 kHz up whose noise reaches each level; \"in band\" when a band under 20 kHz already does.\n\n| rate | method | level | %s |\n|---|---|---|%s\n",
		strings.Join(specRiseTitles(), " | "), strings.Repeat("---|", len(specRise)))
	for _, rate := range specRates {
		for _, m := range specMethods {
			for _, level := range specLevels {
				fmt.Fprintf(&specMD, "| %s | %s | %.2f | %s |\n", specName(rate), m, level, strings.Join(res[key{rate, m.String(), level}].rise, " | "))
			}
		}
	}
	// the whole third-octave profile at SACD 0 dB
	fmt.Fprintf(&specMD, "\n## Tones: third-octave profile at SACD 0 dB (0.5), dB re full scale\n\n| centre |")
	for _, rate := range specRates {
		for _, m := range specMethods {
			fmt.Fprintf(&specMD, " %s %s |", specName(rate), m.short())
		}
	}
	fmt.Fprintf(&specMD, "\n|---|%s\n", strings.Repeat("---|", len(specRates)*len(specMethods)))
	for _, b := range specThirds(float64(8 * DSD64)) {
		fmt.Fprintf(&specMD, "| %s |", specHzFine(b[1]))
		for _, rate := range specRates {
			for _, m := range specMethods {
				v, ok := res[key{rate, m.String(), 0.5}].thirds[b[1]]
				if !ok {
					v = math.NaN()
				}
				fmt.Fprintf(&specMD, " %s |", specDB(v))
			}
		}
		fmt.Fprintf(&specMD, "\n")
	}
	specFlush(t)
	specInterp()
	specStability(t)
	specFlush(t)
}

// specInterp: the interpolator's own passband: float64 sines at each input rate (352.8k, a DSD source's; 705.6k, a
// PCM source's) against the exact sine at the DSD rate (the gain and phase fitted over the middle half). Found
// 2026-11-03: the round trip's whole difference in band above DSD64.
func specInterp() {
	fmt.Fprintf(&specMD, "\n## The interpolator's passband, gain against the exact sine\n\n| in | out | 50 Hz | 1 kHz | 10 kHz | 20 kHz | error at 1 kHz, re the tone |\n|---|---|---|---|---|---|---|\n")
	for _, in := range []int{352800, 705600} {
		for _, rate := range specRates {
			L := rate / in
			cells := []string{}
			e1k := 0.0
			for _, f := range []float64{50, 1000, 10000, 20000} {
				x := make([]float64, in)
				for i := range x {
					x[i] = 0.5 * math.Sin(2*math.Pi*f*float64(i)/float64(in))
				}
				it := newInterpolator(L)
				u := it.flush(it.push(x, nil))
				var ss, sc, cc, us, uc, res float64
				lo, hi := len(u)/4, 3*len(u)/4
				for i := lo; i < hi; i++ {
					sn, c := math.Sincos(2 * math.Pi * f * float64(i) / float64(rate))
					ss, cc, sc, us, uc = ss+sn*sn, cc+c*c, sc+sn*c, us+u[i]*sn, uc+u[i]*c
					e := u[i] - 0.5*sn
					res += e * e
				}
				det := ss*cc - sc*sc
				g := math.Hypot((us*cc-uc*sc)/det, (uc*ss-us*sc)/det) / 0.5
				cells = append(cells, fmt.Sprintf("%+.7f dB", 20*math.Log10(g)))
				if f == 1000 {
					e1k = 10 * math.Log10(res/float64(hi-lo)/0.125)
				}
			}
			fmt.Fprintf(&specMD, "| %.1fk | %s | %s | %.1f dB |\n", float64(in)/1000, specName(rate), strings.Join(cells, " | "), e1k)
		}
	}
}

func specRiseTitles() []string {
	s := []string{}
	for _, v := range specRise {
		s = append(s, fmt.Sprintf("%.0f dB", v))
	}
	return s
}

func specHzFine(f float64) string {
	switch {
	case f >= 1e6:
		return fmt.Sprintf("%.2f MHz", f/1e6)
	case f >= 1e3:
		return fmt.Sprintf("%.1f kHz", f/1e3)
	}
	return fmt.Sprintf("%.0f Hz", f)
}

// specStability: loop resets for steady and starting signals at each source's limit (2 s, both channels, left +,
// right -): a DSD source's at Limit from 352.8k, a PCM source's at LimitPCM from 705.6k
func specStability(t *testing.T) {
	type sig struct {
		name string
		v    func(i int, in, lim float64) float64
	}
	sine := func(f, scale, ph float64) func(int, float64, float64) float64 {
		return func(i int, in, lim float64) float64 { return scale * lim * math.Sin(2*math.Pi*f*float64(i)/in+ph) }
	}
	sigs := []sig{
		{"DC at the limit from silence (a jump)", func(_ int, _, lim float64) float64 { return lim }},
		{"DC at the limit, 10 ms fade-in", func(i int, in, lim float64) float64 { return lim * math.Min(1, float64(i)/(in/100)) }},
		{"20 Hz at the limit", sine(20, 1, 0)},
		{"1 kHz at the limit", sine(1000, 1, 0)},
		{"1 kHz at the limit from its peak (a seek into a loud passage)", sine(1000, 1, math.Pi/2)},
		{"20 kHz at the limit", sine(20000, 1, 0)},
		{"1 kHz at 1.25x the limit (held there)", sine(1000, 1.25, 0)},
	}
	for _, src := range []string{"dsd", "pcm"} {
		in, lim, title := 352800, Limit, "a DSD source (352.8k in"
		if src == "pcm" {
			in, lim, title = 705600, LimitPCM, "a PCM source (705.6k in"
		}
		fmt.Fprintf(&specMD, "\n## Stability, %s, the limit %.2f): loop resets (standard / clans / adaptive), 2 s each, both channels\n\n| signal | %s |\n|---|%s\n",
			title, lim, strings.Join([]string{"DSD64", "DSD128", "DSD256", "DSD512"}, " | "), strings.Repeat("---|", len(specRates)))
		frames := 2 * in
		for _, s := range sigs {
			x := [][]float64{make([]float64, frames), make([]float64, frames)}
			for i := range x[0] {
				x[0][i] = s.v(i, float64(in), lim)
				x[1][i] = -x[0][i]
			}
			cells := []string{}
			for _, rate := range specRates {
				c := []string{}
				for _, m := range specMethods {
					if m.src != src {
						continue
					}
					_, e := specEncode(t, rate, m, x)
					c = append(c, fmt.Sprint(e.Resets))
				}
				cells = append(cells, strings.Join(c, " / "))
			}
			fmt.Fprintf(&specMD, "| %s | %s |\n", s.name, strings.Join(cells, " | "))
		}
	}
}

// ---- music ----

const specPCMN = 1 << 20 // the round trip's null, at 352.8k: 2.97 s a frame

// specOrig: an original DSF's left-channel bits over [from, to) DSD samples, read from the file
type specOrig struct {
	data      []byte
	g0, chans int
}

func specReadOrig(t *testing.T, path string, from, to int) specOrig {
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := make([]byte, 40)
	if _, err := io.ReadFull(f, h); err != nil {
		t.Fatal(err)
	}
	if string(h[0:4]) != "DSD " || string(h[28:32]) != "fmt " {
		t.Fatalf("%s: not a DSF", path)
	}
	at := 28 + int(binary.LittleEndian.Uint64(h[32:40])) // the data chunk, after the fmt chunk
	hd := make([]byte, at+12)
	if _, err := f.ReadAt(hd, 0); err != nil {
		t.Fatal(err)
	}
	if string(hd[at:at+4]) != "data" || binary.LittleEndian.Uint32(hd[72:76]) != blockSize || binary.LittleEndian.Uint32(hd[60:64]) != 1 {
		t.Fatalf("%s: no data chunk after fmt, or not 4096-byte blocks LSB first", path)
	}
	chans := int(binary.LittleEndian.Uint32(hd[52:56]))
	per := blockSize * 8
	g0, g1 := from/per, (to+per-1)/per
	buf := make([]byte, (g1-g0)*blockSize*chans)
	if n, err := f.ReadAt(buf, int64(at+12+g0*blockSize*chans)); n < len(buf) {
		t.Fatalf("%s: %d of %d bytes (%v)", path, n, len(buf), err)
	}
	return specOrig{buf, g0, chans}
}

func (o specOrig) bit(i int) float64 {
	per := blockSize * 8
	k := (i/per-o.g0)*blockSize*o.chans + (i%per)/8
	return float64((o.data[k]>>uint(i%8))&1)*2 - 1
}

// specDecode: a DSF through foxDSDReader as the engine decodes for DSD out (352.8k, the 30 kHz low-pass, gain 0)
func specDecode(t *testing.T, path string, start, end time.Duration) ([][]float64, int) {
	r := &foxDSDReader.Reader{Filename: path, OutRate: specPCM, Cutoff: 30000, StartTime: start, EndTime: end, TargetFrameSize: 8192}
	if err := r.Open(); err != nil {
		t.Fatal(err)
	}
	ch := make(chan [][]float64, 4)
	go func() { r.DecodeInput(ch); close(ch) }()
	pcm := make([][]float64, r.NumChannels)
	for blk := range ch {
		for c := range pcm {
			pcm[c] = append(pcm[c], blk[c]...)
		}
	}
	r.Close()
	return pcm, r.DSDRate
}

// specFrames: up to specFrame windows of n, spread over [settle, count)
func specFrames(count, settle, n int) []int {
	span := count - settle - n
	if span < 0 {
		return nil
	}
	nf := min(specFrame, span/(n/2)+1)
	var s []int
	for j := 0; j < nf; j++ {
		s0 := settle
		if nf > 1 {
			s0 += j * span / (nf - 1)
		}
		s = append(s, s0)
	}
	return s
}

func specOctRow(r specResult) string {
	cells := []string{}
	for _, v := range r.oct {
		cells = append(cells, specDB(v))
	}
	return strings.Join(cells, " | ")
}

// TestSpecMusic: for each DSF, (1) what the modulator added: its output less its own input; (2) the round trip in
// band: our DSF decoded as the original was, less the original decoded (whatever our chain changed, the filters'
// passband ripple included); (3) the original's own bitstream and ours read the same way, out of band.
func TestSpecMusic(t *testing.T) {
	if !*specRun || *specMusic == "" {
		t.Skip("the music half of the spec bench: -args -spec -specmusic=a.dsf,b.dsf")
	}
	a := newSpecAna(specN)
	pa := newSpecAna(specPCMN)
	specHeader(t, a, "music")
	tmp := t.TempDir()
	for _, file := range strings.Split(*specMusic, ",") {
		file = strings.TrimSpace(file)
		start := time.Duration(*specStart * float64(time.Second))
		pcm, src := specDecode(t, file, start, start+time.Duration(*specSecs*float64(time.Second)))
		ms := 0.0
		for _, v := range pcm[0] {
			ms += v * v
		}
		pfr := specFrames(len(pcm[0]), specPCM/10, specPCMN)
		if pfr == nil {
			t.Fatalf("%s: %.1f s is too short", file, *specSecs)
		}
		pa.reset()
		for _, s0 := range pfr {
			pa.add(func(i int) float64 { return pcm[0][s0+i] })
		}
		fmt.Fprintf(&specMD, "## Music: %s (%s), %.1f-%.1f s, left channel %.1f dB RMS re full scale, %.1f dB in band\n\n", filepath.Base(file), specName(src),
			*specStart, *specStart+*specSecs, 10*math.Log10(2*ms/float64(len(pcm[0]))), pa.band(specPCM, 20, 20000, 0))
		// the original's own bitstream, over the same time as ours at its rate
		oL := src / specPCM
		oFrom := int(math.Round(*specStart*specPCM)) * oL
		ofr := specFrames(len(pcm[0])*oL, src/10, specN)
		if ofr == nil {
			t.Fatalf("%s: %.1f s is too short for %s", file, *specSecs, specName(src))
		}
		orig := specReadOrig(t, file, oFrom+ofr[0], oFrom+ofr[len(ofr)-1]+specN)
		a.reset()
		for _, s0 := range ofr {
			a.add(func(i int) float64 { return orig.bit(oFrom + s0 + i) })
		}
		origRes := a.result(float64(src), 0, true)
		rates := []int{src}
		if *specAllRates {
			rates = specRates
		}
		type row struct {
			rate       int
			kind       string
			added, out specResult
			trip       [4]float64 // in band, 20-2k, 2k-10k, 10k-20k
			note       string
		}
		var rows []row
		for _, rate := range rates {
			L := rate / specPCM
			var std []byte
			for _, kind := range specKinds {
				b, e := specEncode(t, rate, specMethod{kind, "dsd", specPCM}, pcm)
				if *specWrite != "" {
					name := fmt.Sprintf("%s_%s_%s.dsf", strings.TrimSuffix(filepath.Base(file), filepath.Ext(file)), specName(rate), kind)
					if err := os.WriteFile(filepath.Join(*specWrite, name), b, 0o644); err != nil {
						t.Fatal(err)
					}
				}
				if kind == "standard" {
					std = b
				}
				note := fmt.Sprintf("peak %.3f, %d limited, %d resets", e.Peak, e.Limited, e.Resets)
				if kind == "adaptive" && rate == DSD64 {
					note += fmt.Sprintf(", safe design %.1f%% of samples", 100*float64(e.InSafe)/float64(int64(len(pcm))*e.count))
				}
				if kind == "adaptive" && rate != DSD64 {
					if string(b) == string(std) {
						w := rows[len(rows)-2]
						w.kind, w.note = kind, "= standard, bit for bit"
						rows = append(rows, w)
						continue
					}
					note += ", DIFFERS from standard"
				}
				frames := specFrames(int(e.count), rate/10, specN)
				if frames == nil {
					t.Fatalf("%s: %.1f s is too short for %s", file, *specSecs, specName(rate))
				}
				// (1) the loop's input, again: the PCM held at Limit through a fresh interpolator per frame (64 frames of margin)
				a.reset()
				for _, s0 := range frames {
					m0 := max(0, s0/L-64)
					m1 := min(len(pcm[0]), (s0+specN)/L+64)
					xs := make([]float64, m1-m0)
					for i := range xs {
						xs[i] = math.Max(-Limit, math.Min(Limit, pcm[0][m0+i]*e.gain))
					}
					it := newInterpolator(L)
					u := it.flush(it.push(xs, nil))
					off := s0 - m0*L
					a.add(func(i int) float64 { return specBit(b, len(pcm), s0+i) - u[off+i] })
				}
				added := a.result(float64(rate), 0, false)
				if added.inband[0] > -100 {
					t.Errorf("%s %s %s: the music did not cancel (in band %.1f dB): the input is not the loop's", file, specName(rate), kind, added.inband[0])
				}
				// (3) ours as it is
				a.reset()
				for _, s0 := range frames {
					a.add(func(i int) float64 { return specBit(b, len(pcm), s0+i) })
				}
				out := a.result(float64(rate), 0, true)
				// (2) the round trip in band
				rt := filepath.Join(tmp, "rt.dsf")
				if err := os.WriteFile(rt, b, 0o644); err != nil {
					t.Fatal(err)
				}
				back, _ := specDecode(t, rt, 0, 0)
				if len(back[0]) != len(pcm[0]) {
					t.Fatalf("%s %s %s: %d frames back, %d in", file, specName(rate), kind, len(back[0]), len(pcm[0]))
				}
				pa.reset()
				for _, s0 := range pfr {
					pa.add(func(i int) float64 { return back[0][s0+i] - pcm[0][s0+i] })
				}
				trip := [4]float64{pa.band(specPCM, 20, 20000, 0), pa.band(specPCM, 20, 2000, 0), pa.band(specPCM, 2000, 10000, 0), pa.band(specPCM, 10000, 20000, 0)}
				rows = append(rows, row{rate, kind, added, out, trip, note + fmt.Sprintf(", %d frames", len(frames))})
			}
		}
		fmt.Fprintf(&specMD, "In band, dB re full scale. *Added*: the modulator's output less its own input. *Round trip*: our DSF decoded as the original was, less the original decoded: all our chain changed (the filters' passband ripple too).\n\n")
		fmt.Fprintf(&specMD, "| out | method | added | round trip | round trip 20 Hz-2 kHz | 2-10 kHz | 10-20 kHz | note |\n|---|---|---|---|---|---|---|---|\n")
		for _, w := range rows {
			fmt.Fprintf(&specMD, "| %s | %s | %s | %s | %s | %s | %s | %s |\n", specName(w.rate), w.kind, specDB(w.added.inband[0]), specDB(w.trip[0]),
				specDB(w.trip[1]), specDB(w.trip[2]), specDB(w.trip[3]), w.note)
		}
		fmt.Fprintf(&specMD, "\nOut of band, each bitstream as it is (the music above 20 kHz included), dB re full scale:\n\n| | method | %s |\n|---|---|%s\n", strings.Join(specTitles, " | "), strings.Repeat("---|", len(specTitles)))
		fmt.Fprintf(&specMD, "| the original %s | | %s |\n", specName(src), specOctRow(origRes))
		for _, w := range rows {
			fmt.Fprintf(&specMD, "| ours %s | %s | %s |\n", specName(w.rate), w.kind, specOctRow(w.out))
		}
		fmt.Fprintf(&specMD, "\nWhere it rises, each bitstream as it is (the first third-octave band from 20 kHz up reaching each level):\n\n| | method | %s |\n|---|---|%s\n", strings.Join(specRiseTitles(), " | "), strings.Repeat("---|", len(specRise)))
		fmt.Fprintf(&specMD, "| the original %s | | %s |\n", specName(src), strings.Join(origRes.rise, " | "))
		for _, w := range rows {
			fmt.Fprintf(&specMD, "| ours %s | %s | %s |\n", specName(w.rate), w.kind, strings.Join(w.out.rise, " | "))
		}
		fmt.Fprintf(&specMD, "\nThird-octave profile from 16 kHz up, each bitstream as it is, dB re full scale:\n\n| centre | the original %s |", specName(src))
		for _, w := range rows {
			fmt.Fprintf(&specMD, " ours %s %s |", specName(w.rate), w.kind)
		}
		fmt.Fprintf(&specMD, "\n|---|---|%s\n", strings.Repeat("---|", len(rows)))
		for _, b := range specThirds(float64(8 * DSD64)) {
			if b[1] < 16000 {
				continue
			}
			v, ok := origRes.thirds[b[1]]
			if !ok {
				v = math.NaN()
			}
			fmt.Fprintf(&specMD, "| %s | %s |", specHzFine(b[1]), specDB(v))
			for _, w := range rows {
				v, ok := w.out.thirds[b[1]]
				if !ok {
					v = math.NaN()
				}
				fmt.Fprintf(&specMD, " %s |", specDB(v))
			}
			fmt.Fprintf(&specMD, "\n")
		}
		fmt.Fprintf(&specMD, "\nThe modulator's own noise out of band (its output less its input):\n\n| out | method | %s |\n|---|---|%s\n", strings.Join(specTitles, " | "), strings.Repeat("---|", len(specTitles)))
		for _, w := range rows {
			fmt.Fprintf(&specMD, "| %s | %s | %s |\n", specName(w.rate), w.kind, specOctRow(w.added))
		}
		fmt.Fprintf(&specMD, "\n")
		specFlush(t)
	}
}
