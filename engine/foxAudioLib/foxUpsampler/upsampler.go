// Package: github.com/Foxenfurter/foxAudioLib/foxUpsampler
// filename upsampler.go
//
// SqueezeDSP fork: PCM from any rate to any rate (the converter's 705.6k before the DSP and the DSD modulator, and every
// PCM target) in two stages, designed here from first principles; no code or coefficients from soxr, sox or any other
// resampler.
//
//	A: the filter with the character, at the lower rate's Nyquist, a x2 stage between the lower rate and twice it:
//	   by the player's choice of filter (Family: Kaiser, Gaussian, Half-band, Slow roll-off, Non-ringing; Simon
//	   2026-11-06, ledger THE FILTER FAMILY), length (Kaiser and Gaussian) and phase (linear, intermediate or
//	   minimum), one choice for CD-rate sources (48 kHz and under) and one for hi-res, with the widths of the lower
//	   rate's class (a hi-res source going down to 44.1k or 48k meets a CD-rate band edge). Its ringing sits in its
//	   own transition band.
//	B: the rest of the way, linear phase and gentle: flat to A's stop band edge, stopped from where an image or an
//	   alias would land below it. Rational L/M, so the 48 kHz family reaches 705.6k too (96k to 705.6k is 147/20).
//	   Its ringing sits in A's stop band.
//
// Going up, A then B (B from twice the source to the target, down when the target is under twice the source: 44.1k to
// 48k is 88.2k to 48k); going down, B then A (B from the source to twice the target, A a 2:1 decimator). So the
// character stage always runs, and with Kaiser and Gaussian nothing above the lower Nyquist comes back as an image or
// an alias (until 2026-11-05 a target under twice the source, or under it, went through B alone, flat to 45% of the
// lower rate and stopped at 55%: images and aliases of the band's top 10% at -7 to -15 dB, and no character at all).
// The leaky families let images through on purpose (Simon: "these are for subjective listening"); going down, where
// they would fold what is above the target's Nyquist into the band, they run as Kaiser short in their phase. The pair
// has A's character at a fraction of one filter's cost (ledger STAGE 1 STUDY: 38 against 174 M multiply-adds a second
// a channel at 20k to 22.05k, 219 against 1618 at 99%). A source at the target goes through neither.
//
// The widths, as a share of the lower rate fs (N = fs/2):
//
//	Kaiser    CD short flat to 0.4535 fs (20 kHz at 44.1k), long to 0.99 N, extra-long to 0.995 N; hi-res
//	          short flat to 20 kHz (a slow roll-off, very short ringing), long to 0.9 N, extra-long to 0.99 N;
//	          stopped by N
//	Gaussian  CD short flat to 0.4308 fs (19 kHz at 44.1k), medium to 0.4535 fs, long to 0.99 N; hi-res short to
//	          20 kHz, medium to 0.9 N, long to 0.99 N; stopped by N
//	Half-band N - 0.0238 fs to N + 0.0238 fs (21.0 to 23.1 kHz at 44.1k): what is above 21 kHz comes back
//	Slow      N - 0.1247 fs to N + 0.1247 fs (16.55 to 27.55 kHz): a 20 kHz tone's image -26.5 dB
//	Non-ring. every tap positive, 3 dB down at 0.4535 fs, nothing stopped: a 20 kHz tone's image -4.5 dB
//
// Every filter but Non-ringing is StopDB down from its stop band edge. Intermediate phase: a linear-phase half
// times a minimum-phase half: for Kaiser, each with half the attenuation in dB (the same length as linear, the
// energy before the main peak 28-42 dB below linear's: study -45.5 against -13.4 dB at CD short); for the others the
// square root of the linear one (sqrtHalf), so it keeps the linear one's response.
//
// The output is time-aligned with the input: the chain's delay (at the output rate, rounded) is dropped at
// the start, and Flush ends the stream with silence through the filters, so N input frames give
// round(N*out/in) output frames. Each stream is filtered on its own (as soxr in ffmpeg did per track).
package foxUpsampler

import (
	"fmt"
	"math"
	"strings"
	"sync"
)

// StopDB: every filter's stop band (soxr's precision 28, today's route, is about 169 dB).
const StopDB = 170.0

const (
	gaussPassDB    = 0.001           // a Gaussian's pass band: within this of flat to its edge (FAMILY STUDY)
	gaussMargin    = 2.0             // dB deeper than asked, as kaiserLowpass's 2.5, for the leakage's estimate
	halfMargin     = 2.0             // dB deeper the linear filter is designed before sqrtHalf takes its square root
	halfBandWidth  = 0.0238          // Half-band: its edges this share of fs either side of fs/2
	slowWidth      = 0.1247          // Slow roll-off: the same, wider
	nonRingingEdge = 20000.0 / 44100 // Non-ringing: 3 dB down here, a share of fs (20 kHz at 44.1k)
)

type Phase int

const (
	Linear Phase = iota
	Intermediate
	Minimum
)

// Family: the filter's kind. Kaiser and Gaussian stop everything from the lower Nyquist on; the three after them
// are leaky by design, for listening (Simon 2026-11-06: "these are for subjective listening, we maintain the
// rejection of such images in our own filter").
type Family int

const (
	Kaiser     Family = iota // Kaiser-windowed sinc: short, long, extra-long
	Gaussian                 // Gaussian-windowed sinc: short, medium, long
	HalfBand                 // steep across the lower Nyquist, -6 dB there
	Slow                     // a slow roll-off across the lower Nyquist, ringing about a fifth of Kaiser short's
	NonRinging               // every tap positive: no ringing, no overshoot; one entry, no phase
)

type Length int

const (
	Short Length = iota
	Medium
	Long
	ExtraLong
)

// Choice: a filter's family, its length (Kaiser and Gaussian; Short for the others) and its phase (Linear for
// Non-ringing).
type Choice struct {
	Family Family
	Length Length
	Phase  Phase
}

var (
	DefaultCD    = Choice{Phase: Intermediate} // Kaiser short, intermediate (Simon 2026-11-04)
	DefaultHiRes = Choice{Phase: Linear}       // Kaiser short, linear
)

var (
	phaseNames    = []string{"lin", "int", "min"}
	familyNames   = []string{"", "gauss", "halfband", "slow", "nonring"}
	lengthNames   = []string{"short", "medium", "long", "xlong"}
	familyLengths = map[Family][]Length{Kaiser: {Short, Long, ExtraLong}, Gaussian: {Short, Medium, Long}}
)

// Leaky: a family that lets images through (and, going down, would let aliases in).
func (c Choice) Leaky() bool { return c.Family >= HalfBand }

// String: the engine's --upfilter name. Kaiser's as before 0.1.72.44 (short-int, long-lin, and xlong-min), the
// others named first (gauss-medium-lin, halfband-int, slow-min), Non-ringing alone (nonring).
func (c Choice) String() string {
	var p []string
	if c.Family != Kaiser {
		p = append(p, familyNames[c.Family])
	}
	if _, ok := familyLengths[c.Family]; ok {
		p = append(p, lengthNames[c.Length])
	}
	if c.Family != NonRinging {
		p = append(p, phaseNames[c.Phase])
	}
	return strings.Join(p, "-")
}

// Choices: every filter, in the page's order (Upsample.pm's @FILTERS).
func Choices() []Choice {
	var cs []Choice
	for f := Kaiser; f <= NonRinging; f++ {
		lengths, ok := familyLengths[f]
		if !ok {
			lengths = []Length{Short}
		}
		phases := []Phase{Linear, Intermediate, Minimum}
		if f == NonRinging {
			phases = phases[:1]
		}
		for _, l := range lengths {
			for _, p := range phases {
				cs = append(cs, Choice{Family: f, Length: l, Phase: p})
			}
		}
	}
	return cs
}

// ParseChoice reads a name String gives (short-int, gauss-medium-lin, halfband-min, nonring, ...), in any case.
func ParseChoice(s string) (Choice, error) {
	v := strings.ToLower(strings.TrimSpace(s))
	for _, c := range Choices() {
		if c.String() == v {
			return c, nil
		}
	}
	return Choice{}, fmt.Errorf("upsample filter %q: not one of the filters (short-int, gauss-medium-lin, halfband-min, nonring, ...)", s)
}

// IsHiRes: a source above 48 kHz takes the hi-res choice.
func IsHiRes(rate int) bool { return rate > 48000 }

// widths: the pass band edge and stop band edge of stage A for a lower rate fs (Half-band and Slow: either side of
// fs/2; Non-ringing: where it is 3 dB down, and fs, as it stops nothing).
func widths(fs float64, hiRes bool, c Choice) (fp, fst float64) {
	switch c.Family {
	case HalfBand:
		return fs/2 - halfBandWidth*fs, fs/2 + halfBandWidth*fs
	case Slow:
		return fs/2 - slowWidth*fs, fs/2 + slowWidth*fs
	case NonRinging:
		return nonRingingEdge * fs, fs
	}
	fst = fs / 2
	gauss := c.Family == Gaussian
	switch {
	case !hiRes && gauss && c.Length == Short:
		fp = fs * 19000 / 44100
	case !hiRes && (c.Length == Short || c.Length == Medium):
		fp = fs * 20000 / 44100
	case !hiRes && c.Length == Long:
		fp = 0.99 * fs / 2
	case !hiRes:
		fp = 0.995 * fs / 2
	case c.Length == Short:
		fp = math.Min(20000, 0.45*fs)
	case c.Length == Medium || c.Length == Long && !gauss:
		fp = 0.9 * fs / 2
	default:
		fp = 0.99 * fs / 2
	}
	return fp, fst
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// Upsampler resamples every channel the same way; one per stream.
type Upsampler struct {
	InRate, OutRate, Channels int
	Choice                    Choice    // the one used (by the source's rate)
	Down                      bool      // to a lower rate: B, then A as a 2:1 decimator
	TapsA, TapsB              int       // the prototypes' lengths (0: no stage)
	Delay                     float64   // the chain's delay, output samples (before rounding)
	stages                    [][]*poly // in the order they run, one poly a channel each
	bFp, bFst                 float64   // B's pass band and stop band edges, Hz (the tests read them)
	drop                      int       // output samples still to drop at the start
	in                        int64     // input frames taken
	out                       int64     // output frames given
	scratch                   [][]float64
}

// New designs the chain from inRate to outRate (outRate/inRate a ratio of whole numbers), with cd for a source
// at 48k and under, hiRes for one above.
func New(inRate, outRate, channels int, cd, hiRes Choice) (*Upsampler, error) {
	if inRate <= 0 || outRate <= 0 || channels <= 0 {
		return nil, fmt.Errorf("upsample: rates %d to %d, %d channels", inRate, outRate, channels)
	}
	u := &Upsampler{InRate: inRate, OutRate: outRate, Channels: channels, Choice: cd}
	if IsHiRes(inRate) {
		u.Choice = hiRes
	}
	if inRate == outRate {
		return u, nil
	}
	// A at the lower rate's Nyquist: x2 going up (gain 2: the zeros it stuffs), a 2:1 decimator going down
	lo := min(inRate, outRate)
	u.Down = outRate < inRate
	if u.Down && u.Choice.Leaky() {
		// going down, a leaky filter would fold what is above the target's Nyquist into the band: Kaiser short in its
		// phase instead (Non-ringing's is linear)
		u.Choice = Choice{Phase: u.Choice.Phase}
	}
	fp, fst := widths(float64(lo), IsHiRes(lo), u.Choice)
	gain := 2.0
	if u.Down {
		gain = 1
	}
	protoA := shaped(u.Choice, 2*float64(lo), fp, fst, StopDB, gain)
	u.TapsA = len(protoA)
	// B between twice the lower rate and the other end: flat to A's stop band edge, stopped from where an image or
	// (going down to its output) an alias would land below that edge. A leaky A's edge can sit past what B's lower end
	// can carry: B then flat half way from the lower Nyquist to its own at most (never for Kaiser or Gaussian, whose
	// edge is the lower Nyquist)
	bIn, bOut := 2*lo, outRate
	if u.Down {
		bIn, bOut = inRate, 2*lo
	}
	var protoB []float64
	var L, M int
	if bIn != bOut {
		g := gcd(bIn, bOut)
		L, M = bOut/g, bIn/g
		u.bFp = math.Min(fst, (float64(lo)/2+float64(min(bIn, bOut))/2)/2)
		u.bFst = float64(min(bIn, bOut)) - u.bFp
		protoB = kaiserLowpass(float64(bIn)*float64(L), u.bFp, u.bFst, StopDB, float64(L))
		u.TapsB = len(protoB)
	}
	// the delay at the output rate: A's at twice the lower rate, B's at its own output
	if u.Down {
		d := delayOf(protoA) // at 2*lo, A's input
		if protoB != nil {
			d += delayOf(protoB) / float64(M)
		}
		u.Delay = d / 2
	} else {
		u.Delay = delayOf(protoA) * float64(outRate) / float64(2*lo)
		if protoB != nil {
			u.Delay += delayOf(protoB) / float64(M)
		}
	}
	u.drop = int(math.Round(u.Delay))
	a := make([]*poly, channels)
	var b []*poly
	for ch := range a {
		if u.Down {
			a[ch] = newPoly(protoA, 1, 2)
		} else {
			a[ch] = newPoly(protoA, 2, 1)
		}
		if protoB != nil {
			b = append(b, newPoly(protoB, L, M))
		}
	}
	switch {
	case b == nil:
		u.stages = [][]*poly{a}
	case u.Down:
		u.stages = [][]*poly{b, a}
	default:
		u.stages = [][]*poly{a, b}
	}
	u.scratch = make([][]float64, channels)
	return u, nil
}

// Process resamples one block (frames per channel, the same count in each) and returns the output block.
func (u *Upsampler) Process(in [][]float64) [][]float64 {
	if len(in) == 0 {
		return in
	}
	u.in += int64(len(in[0]))
	if u.stages == nil {
		u.out += int64(len(in[0]))
		return in
	}
	out := u.run(in)
	return u.trim(out, -1)
}

// Flush ends the stream: silence through the filters until round(in*out/inRate) frames have come out in all.
func (u *Upsampler) Flush() [][]float64 {
	target := int64(math.Round(float64(u.in) * float64(u.OutRate) / float64(u.InRate)))
	res := make([][]float64, u.Channels)
	if u.stages == nil {
		return res
	}
	for u.out < target {
		zeros := make([][]float64, u.Channels)
		n := 4096
		for ch := range zeros {
			zeros[ch] = make([]float64, n)
		}
		got := u.trim(u.run(zeros), target)
		for ch := range res {
			res[ch] = append(res[ch], got[ch]...)
		}
	}
	return res
}

// run: every channel through the stages in turn, in parallel. Between stages a channel's scratch buffer; the last
// stage writes a new block, as it travels downstream.
func (u *Upsampler) run(in [][]float64) [][]float64 {
	out := make([][]float64, u.Channels)
	var wg sync.WaitGroup
	last := len(u.stages) - 1
	for ch := 0; ch < u.Channels; ch++ {
		wg.Add(1)
		go func(ch int) {
			defer wg.Done()
			x := in[ch]
			for i, st := range u.stages {
				p := st[ch]
				if i < last {
					u.scratch[ch] = p.push(x, u.scratch[ch][:0])
					x = u.scratch[ch]
					continue
				}
				out[ch] = p.push(x, make([]float64, 0, int(float64(len(x))*float64(p.L)/float64(p.M))+2))
			}
		}(ch)
	}
	wg.Wait()
	return out
}

// trim drops what is left of the start's delay and, with limit >= 0, anything past limit frames in all.
func (u *Upsampler) trim(out [][]float64, limit int64) [][]float64 {
	n := len(out[0])
	start := min(u.drop, n)
	u.drop -= start
	end := n
	if limit >= 0 && u.out+int64(end-start) > limit {
		end = start + int(limit-u.out)
	}
	for ch := range out {
		out[ch] = out[ch][start:end]
	}
	u.out += int64(end - start)
	return out
}
