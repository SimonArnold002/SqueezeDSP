// Package: github.com/Foxenfurter/foxAudioLib/foxAudioDecoder/gipsyDSDReader
// filename gipsyDSDReader.go
//
// SqueezeDSP fork: decodes DSD (DSF, and DSDIFF that is not DST-compressed) to PCM for the engine, in
// place of ffmpeg, DSDPlayer's dsdplay or sox. Written from the DSF 1.01 and DSDIFF 1.5 specifications
// and a Kaiser-windowed sinc; no code from ffmpeg, dsd2pcm, sox or dsdplay.
//
// Two linear-phase FIR stages per channel:
//  1. the DSD rate down to `mid` (352.8k, or 705.6k for a 705.6k target): evaluated a byte at a time from
//     256-entry tables, one per 8 taps. Flat to 80 kHz, -160 dB from mid-80 kHz, the band whose aliases
//     would land below 80 kHz. 120 dB measured too shallow: at DSD128 and DSD256 the noise up there came
//     back about 20 dB above ffmpeg's in-band floor.
//  2. at mid: the cutoff and the gain as one FIR (-150 dB; the narrowest band, 44.1k's 18.5-22.05 kHz guard,
//     reaches -146.5 at its edge, where Kaiser's length estimate runs short), by overlap-save FFT with two
//     channels packed as one complex signal, then decimated to the target. The transition sits symmetric
//     about the cutoff, at most 20 kHz wide, so every cutoff is flat to 20 kHz.
//
// A target that 352.8k does not divide by a whole number (the 48 kHz family, or 705.6k from DSD64) takes a
// third stage after stage 2, which then only band-limits:
//  3. a polyphase interpolator by up/down: frame k is stage 2's output at k*down/up, from a few samples
//     either side. Flat to stage 2's stop edge and -150 dB from mid less that edge, where the first image
//     starts; stage 2 has left nothing between, so a handful of taps per frame does it.
//
// Both filters' centres are placed so the delay is whole output frames: it is removed, and the tail is
// flushed with DSD silence, so output frame k is the input at time k exactly. Seeks, --until and the
// pre-roll are one calculation on those frame numbers, and a seek gives the full decode's samples.
//
// Native (the native DSD round, 2026-11-07; Simon: "an option for those with high spec systems, default is still our DXD
// route"): the decode stays at the DSD rate. Stage 1 has no filter, each bit as +-1; stage 2 at the DSD rate is the
// integrator, the low-pass of the same band as the DXD route's (Cutoff; none is stage 1's 80 kHz) in the shape asked
// for (Integrator): linear, intermediate or minimum phase, or non-ringing, a Gaussian with no ringing at all, which
// nothing decimating to 352.8k could use (the source's noise would fold into the band). No stage 3. A seek, an end,
// Span and the gapless state work as on the DXD route, a frame being a DSD sample.
//
// Level: unity, as ffmpeg, sox and dsdplay (DSD's full swing at full scale, so DSD's 0 dB, 50%
// modulation, reads -6 dBFS). GainDB 6 puts DSD's 0 dB at full scale.
package gipsyDSDReader

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"sync"
	"time"

	"github.com/Foxenfurter/foxAudioLib/foxFFT"
	"github.com/Foxenfurter/foxAudioLib/gipsySpeed"
	"github.com/Foxenfurter/foxAudioLib/gipsyUpsampler"
	"scientificgo.org/fft"
)

const packageName = "gipsyDSDReader"

// DXD is stage 1's usual output rate: DSD64 / 8.
const DXD = 352800

// silence is DSD's idle pattern; it pads the history before a file's start and the flush after its end.
const silence = 0x69

type Reader struct {
	Filename        string
	OutRate         int           // the PCM rate out, 44.1 to 768 kHz: 44.1k family to 352.8k (or 705.6k) by whole numbers, others by stage 3
	Cutoff          float64       // Hz, above 20 to 60 kHz (stage 1 is flat to 80); 0 = none (stage 1's 80 kHz band only)
	GainDB          float64       // dB on top of unity
	StartTime       time.Duration // from the file's start
	EndTime         time.Duration // from the file's start; 0 = to the end
	TargetFrameSize int           // frames per block sent
	DebugFunc       func(string)
	// Native: decode at the DSD rate itself (Open sets OutRate to DSDRate), through Integrator: "lin" (or ""), "int",
	// "min" or "nonring"
	Native     bool
	Integrator string

	DSDRate      int   // bits per second per channel
	NumChannels  int   //
	Frames       int64 // frames DecodeInput sends
	TotalSamples int64 // frames sent
	RawPeak      float64

	// Meter (SqueezeDSP fork): the decode's own time, for the engine's speed (gipsySpeed); the file's reads and the
	// sends, which wait for the next stage, left out
	Meter *gipsySpeed.Meter

	f          *os.File
	src        *source
	h1, h2     []float64
	every, dec int   // bytes per stage 1 output (0: native, eight outputs a byte); stage 1 outputs per frame
	up, down   int   // stage 3: frame k is stage 2's output at k*down/up; 0 = no stage 3
	ki         int   // stage 3 reaches ki of stage 2's outputs either side
	ph         [][]float64
	pass, stop float64 // native: the integrator's band
	delay      int64   // stage 1 outputs of delay, both stages together
	kStart     int64
	kEnd       int64

	// EndHold (SqueezeDSP fork, the gapless round): asked at the input's end. True: a next file goes on from this
	// decode (state.go), so it stops at the last byte, its filters keeping what they hold; false: nothing follows, so
	// DSD silence plays the filters out, past the last frame by their reach (Span). nil: as before, the last frame.
	EndHold func() bool
	resume  *session // SetState: the decode goes on from the last file's
	sess    *session // this decode's, for State
}

// session (SqueezeDSP fork, the gapless round): the decode's place in the stream, kept across a gapless join so the next
// file goes on from it (state.go): stage 1's last bytes and phase, stage 2's inputs not yet through an FFT block (its
// block grid runs on), stage 3's history, the numbers of the next stage 1 and stage 2 outputs and of the next frame,
// counted over the stream, and the stream's bytes a channel before this file and in it.
type session struct {
	s1    []*stage1
	s2    []*stage2
	hist  [][]float64 // stage 3: stage 2's outputs from number hb on
	hb    int64
	j, j1 int64 // the next stage 2 output's number, the next stage 1 output's
	k     int64 // the next frame's
	base  int64 // the stream's bytes a channel before this file's first
	read  int64 // this file's bytes a channel read
	whole bool  // the decode stopped at the file's end with every stage holding (a next file can go on from it)
}

// Open reads the header and designs the filters. The decode starts at StartTime.
func (r *Reader) Open() error {
	f, err := os.Open(r.Filename)
	if err != nil {
		return err
	}
	r.f = f
	var magic [4]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil {
		return fmt.Errorf("%s: %w", packageName, err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	switch string(magic[:]) {
	case "DSD ":
		r.src, err = openDSF(f)
	case "FRM8":
		r.src, err = openDFF(f)
	default:
		err = errors.New("neither DSF nor DSDIFF")
	}
	if err != nil {
		return fmt.Errorf("%s: %w", packageName, err)
	}
	r.DSDRate, r.NumChannels = r.src.rate, r.src.channels
	if r.DSDRate%(DXD*8) != 0 {
		return fmt.Errorf("%s: DSD rate %d is not in the 44.1 kHz family", packageName, r.DSDRate)
	}
	if r.Native {
		if err := r.openNative(); err != nil {
			return err
		}
		r.frames()
		r.debug(fmt.Sprintf("%s: %s %d Hz x%d native: the integrator %s, %d taps, pass %.0f stop %.0f, gain %.2f dB, delay %d, frames %d-%d",
			packageName, r.src.name, r.DSDRate, r.NumChannels, r.integrator(), len(r.h2), r.pass, r.stop, r.GainDB, r.delay, r.kStart, r.kEnd))
		return nil
	}
	mid := DXD
	if r.OutRate == 2*DXD && r.DSDRate >= 16*DXD {
		mid = 2 * DXD
	}
	r.dec = 1
	switch {
	case r.OutRate > 0 && mid%r.OutRate == 0 && mid/r.OutRate <= 8:
		r.dec = mid / r.OutRate
	case r.OutRate >= 44100 && r.OutRate <= 768000:
		g := gcd(r.OutRate, mid)
		r.up, r.down = r.OutRate/g, mid/g
		if r.up > 1024 { // the 48k family is 20-320 phases; a rate needing more is no real one
			return fmt.Errorf("%s: no %d Hz from DSD (%d/%d of 352.8 kHz)", packageName, r.OutRate, r.up, r.down)
		}
	default:
		return fmt.Errorf("%s: no %d Hz from DSD at %d Hz (44.1-768 kHz)", packageName, r.OutRate, r.DSDRate)
	}
	if r.Cutoff != 0 && (r.Cutoff <= 20000 || r.Cutoff > 60000) { // above 60k its band would reach past stage 1's flat 80k
		return fmt.Errorf("%s: cutoff %.0f Hz: 0, or above 20 kHz to 60 kHz", packageName, r.Cutoff)
	}
	r.every = r.DSDRate / mid / 8

	// stage 1: centre c1 = 8*every*d1 - 1 bits, so output j is the input at stage 1 sample j+1-d1 exactly
	d1 := halfLength(float64(r.DSDRate), 80000, float64(mid)-80000, 160)/(8*r.every) + 1
	c1 := 8*r.every*d1 - 1
	r.h1 = make([]float64, 2*c1+2) // 2*c1+1 taps, and a zero, for whole bytes
	copy(r.h1, design(float64(r.DSDRate), 80000, float64(mid)-80000, 160, c1))

	// stage 2: the cutoff (or, decimating, the target's own guard band) and the gain
	pass, stop := 0.42*float64(r.OutRate), float64(r.OutRate)/2
	if r.up > 0 && stop > 100000 { // stage 3 needs room under mid/2: stage 1's flat 80 kHz, gone by 100 kHz
		pass, stop = 80000, 100000
	}
	if r.Cutoff > 0 {
		tw := math.Min(2*(r.Cutoff-20000), 20000)
		if r.Cutoff+tw/2 <= stop { // the target's Nyquist wins: nothing may alias
			pass, stop = r.Cutoff-tw/2, r.Cutoff+tw/2
		}
	}
	g := math.Pow(10, r.GainDB/20)
	c2 := 0
	if r.Cutoff > 0 || r.dec > 1 || r.up > 0 {
		c2 = halfLength(float64(mid), pass, stop, 150)
		r.h2 = design(float64(mid), pass, stop, 150, c2)
	} else {
		r.h2 = []float64{1}
	}
	for i := range r.h2 {
		r.h2[i] *= g
	}
	r.delay = int64(d1 - 1 + c2)

	// stage 3: a low-pass at up*mid, flat to stage 2's stop edge, -150 dB from mid less it, centred on a
	// stage 2 output; phase p holds taps up*t-p, so frame k takes phase k*down mod up over the 2*ki+1
	// stage 2 outputs from k*down/up - ki. Designed for 155 dB: at these lengths Kaiser's estimate runs up
	// to 5 dB short (145.3 measured when designed for 150), and 155 measures 152 at worst.
	if r.up > 0 {
		L := r.up
		fs := float64(L * mid)
		r.ki = (halfLength(fs, stop, float64(mid)-stop, 155) + L - 1) / L
		h := design(fs, stop, float64(mid)-stop, 155, L*r.ki)
		r.ph = make([][]float64, L)
		for p := range r.ph {
			r.ph[p] = make([]float64, 2*r.ki+1)
			for t := range r.ph[p] {
				if i := L*t - p; i >= 0 {
					r.ph[p][t] = float64(L) * h[i]
				}
			}
		}
		if r.delay < int64(r.ki) { // frame 0 would reach before stage 2's first output
			return fmt.Errorf("%s: stage 3 reaches %d back, the delay is %d", packageName, r.ki, r.delay)
		}
	}

	r.frames()
	s3 := "none"
	if r.up > 0 {
		s3 = fmt.Sprintf("x%d/%d, %d taps", r.up, r.down, 2*r.ki+1)
	}
	r.debug(fmt.Sprintf("%s: %s %d Hz x%d -> %d Hz: stage 1 %d taps to %d Hz, stage 2 %d taps / %d, pass %.0f stop %.0f, stage 3 %s, gain %.2f dB, delay %d, frames %d-%d",
		packageName, r.src.name, r.DSDRate, r.NumChannels, r.OutRate, len(r.h1), mid, len(r.h2), r.dec, pass, stop, s3, r.GainDB, r.delay, r.kStart, r.kEnd))
	return nil
}

// frames: the frames to send, from StartTime to EndTime or the last whole frame
func (r *Reader) frames() {
	last := r.src.bytes * 8 * int64(r.OutRate) / int64(r.DSDRate)
	r.kStart = int64(math.Round(r.StartTime.Seconds() * float64(r.OutRate))) // as kEnd: a cut track and the next meet
	r.kEnd = last
	if r.EndTime > 0 {
		if e := int64(math.Round(r.EndTime.Seconds() * float64(r.OutRate))); e < r.kEnd {
			r.kEnd = e
		}
	}
	if r.kStart > r.kEnd {
		r.kStart = r.kEnd
	}
	r.Frames = r.kEnd - r.kStart
}

// openNative: the native decode's filter. Stage 1 none (every 0: each bit as +-1); stage 2 the integrator at the DSD
// rate, with the gain, its delay (rounded to a sample) removed. Its band is the DXD route's for the same Cutoff: a
// transition symmetric about it, at most 20 kHz wide; none, stage 1's own (flat to 80 kHz, stopped by 100 kHz).
func (r *Reader) openNative() error {
	if c := r.Cutoff; c != 0 && (c <= 20000 || c > 60000) { // the DXD route's range
		return fmt.Errorf("%s: cutoff %.0f Hz: 0, or above 20 kHz to 60 kHz", packageName, c)
	}
	if r.OutRate != 0 && r.OutRate != r.DSDRate {
		return fmt.Errorf("%s: a native decode is at the DSD rate (%d Hz), not %d Hz", packageName, r.DSDRate, r.OutRate)
	}
	r.OutRate, r.every, r.dec = r.DSDRate, 0, 1
	fs := float64(r.DSDRate)
	r.pass, r.stop = 80000, 100000
	if r.Cutoff > 0 {
		tw := math.Min(2*(r.Cutoff-20000), 20000)
		r.pass, r.stop = r.Cutoff-tw/2, r.Cutoff+tw/2
	}
	// the three Kaiser shapes are the converter's own designs, as there (gipsyUpsampler, with its margins: 150 dB met at the
	// DSD rate, where Kaiser's estimate alone runs short, -145.8 measured; intermediate's two halves' ripple adds, within
	// 0.003 dB); non-ringing is unity at DC
	var d float64
	phase := map[string]gipsyUpsampler.Phase{"lin": gipsyUpsampler.Linear, "int": gipsyUpsampler.Intermediate, "min": gipsyUpsampler.Minimum}
	switch shape := r.integrator(); shape {
	case "lin", "int", "min":
		r.h2, d = gipsyUpsampler.Lowpass(phase[shape], fs, r.pass, r.stop, 150, 1)
	case "nonring":
		r.h2, d = gaussian(fs, (r.pass+r.stop)/2)
	default:
		return fmt.Errorf("%s: integrator %q: lin, int, min or nonring", packageName, r.Integrator)
	}
	gain := math.Pow(10, r.GainDB/20)
	for i := range r.h2 {
		r.h2[i] *= gain
	}
	r.h1 = nil
	r.delay = int64(math.Round(d))
	return nil
}

// integrator: the native decode's shape, "lin" when none was given
func (r *Reader) integrator() string {
	if r.Integrator == "" {
		return "lin"
	}
	return r.Integrator
}

// gaussian: the native integrator's non-ringing shape (the converter's Non-ringing, gipsyUpsampler, without its two-tap
// average, which nulls images the integrator has none of): a sampled Gaussian, every tap positive, so nothing rings
// and a step never overshoots; 3 dB down at f3, unity at DC, cut where its taps fall under 1e-12 of the centre. Odd
// length, so its delay is a whole sample. The width is found on the sampled response itself.
func gaussian(fs, f3 float64) ([]float64, float64) {
	kernel := func(s float64) []float64 {
		k := int(math.Ceil(s*math.Sqrt(2*math.Log(1e12)))) + 1
		h := make([]float64, 2*k+1)
		sum := 0.0
		for i := range h {
			t := float64(i - k)
			h[i] = math.Exp(-t * t / (2 * s * s))
			sum += h[i]
		}
		for i := range h {
			h[i] /= sum
		}
		return h
	}
	at := func(h []float64) float64 { // the response at f3 (symmetric: real)
		k := (len(h) - 1) / 2
		v := 0.0
		for i, x := range h {
			v += x * math.Cos(2*math.Pi*f3/fs*float64(i-k))
		}
		return v
	}
	want := math.Sqrt(0.5)
	s := fs * math.Sqrt(math.Ln2) / (2 * math.Pi * f3) // the continuous Gaussian's width: the start of the search
	lo, hi := s/2, 2*s
	for i := 0; i < 60; i++ {
		if m := (lo + hi) / 2; at(kernel(m)) < want {
			hi = m
		} else {
			lo = m
		}
	}
	h := kernel(lo)
	return h, float64((len(h) - 1) / 2)
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// outputsIn: stage 1's outputs from this many bytes a channel (native: eight a byte)
func (r *Reader) outputsIn(bytes int64) int64 {
	if r.every == 0 {
		return bytes * 8
	}
	return bytes / int64(r.every)
}

// byteLimit: the bytes a channel (over the stream) within which the frame made from stage 2's output lastJ is out:
// stage 2 hands it back at most one FFT block (step outputs) late; 8192 covers every DXD route's block
func (r *Reader) byteLimit(lastJ, step int64) int64 {
	if r.every == 0 {
		return (lastJ+step+int64(len(r.h2)))/8 + 1
	}
	return (lastJ+int64(8192+len(r.h2)))*int64(r.every) + int64(len(r.h1))
}

// span is the first and last of stage 2's outputs frame k is made from
func (r *Reader) span(k int64) (int64, int64) {
	if r.up == 0 {
		j := k*int64(r.dec) + r.delay
		return j, j
	}
	q := floorDiv(k*int64(r.down), int64(r.up))
	return r.delay + q - int64(r.ki), r.delay + q + int64(r.ki)
}

// FileFrames: this file's own share of the stream's frames, from its start (or StartTime) to its end (or EndTime):
// what Frames is for a decode that starts afresh. A decode that goes on from the last file's (SetState) also sends the
// frames that file held back, so its Frames is more; the files' FileFrames still add up to the stream's.
func (r *Reader) FileFrames() int64 {
	if r.resume != nil {
		return r.kEnd - r.resume.base*8*int64(r.OutRate)/int64(r.DSDRate)
	}
	return r.kEnd - r.kStart
}

// Size is the bytes the decode comes to as 24-bit PCM, for the length a WAV header states.
func (r *Reader) Size() int64 { return r.Frames * int64(r.NumChannels) * 3 }

func (r *Reader) Close() error {
	if r.f != nil {
		return r.f.Close()
	}
	return nil
}

// DecodeInput sends the PCM, TargetFrameSize frames at a time, [channel][frame].
func (r *Reader) DecodeInput(out chan [][]float64) error {
	ch := r.NumChannels
	s := r.resume
	var j0, from int64
	if s == nil {
		s = &session{s1: make([]*stage1, ch), s2: make([]*stage2, (ch+1)/2), hist: make([][]float64, ch)}
		for p := range s.s2 {
			s.s2[p] = r.newStage2For()
		}
		// Stage 2 starts on the full decode's FFT block grid, one whole block before the first stage 1 output
		// the first frame needs: every block a seek uses then holds exactly what the full decode's does, so a
		// seek's samples are the full decode's to the bit (off the grid the FFT rounds differently, and a
		// 24-bit sample can come out 1 LSB apart). The bytes start where stage 1's filter reaches back to.
		grid := s.s2[0].step()
		first, _ := r.span(r.kStart)
		jFirst := first - int64(len(r.h2)-1)
		j0 = (floorDiv(jFirst, grid) - 1) * grid
		if j0 < 0 {
			j0 = 0
		}
		if r.every == 0 { // native: stage 1 has no history, and output j is bit j
			from = j0 / 8
		} else {
			from = j0*int64(r.every) - int64(len(r.h1)/8)
			from -= from % int64(r.every)
		}
		if from < 0 {
			from = 0
		}
		if from > 0 {
			if err := r.src.seek(from); err != nil {
				return fmt.Errorf("%s: seek: %w", packageName, err)
			}
		}
		s.j1 = r.outputsIn(from) // the number of stage 1's next output; those before j0 only fill its history
		s.j = j0                     // the number of the next output stage 2 hands back
		s.hb = j0
		s.k = r.kStart
		for c := range s.s1 {
			s.s1[c] = newStage1(r.h1, r.every)
		}
	} else {
		// a gapless continuation (SetState): this file's first byte follows the last file's last, and the outputs
		// and frames are numbered over the stream; no output is history only
		j0 = s.j1
	}
	r.sess = s
	s1, s2, hist := s.s1, s.s2, s.hist
	// stage 2 hands back at most one FFT block late, so the last frame is out within these bytes (over the stream)
	kEnd := r.kEnd
	_, lastJ := r.span(kEnd)
	limit := r.byteLimit(lastJ, s2[0].step())
	pos := s.base + from
	target := r.TargetFrameSize
	if target <= 0 {
		target = r.OutRate / 10
	}
	chunk := int64(target) * int64(r.DSDRate/8) / int64(r.OutRate)
	if chunk > 1<<20 {
		chunk = 1 << 20
	}
	if chunk < int64(r.every) {
		chunk = int64(r.every)
	}
	if chunk < 1 { // native, a target under a byte's eight frames
		chunk = 1
	}
	bufs := make([][]byte, ch)
	for c := range bufs {
		bufs[c] = make([]byte, chunk)
	}
	mid := make([][]float64, ch)
	full := make([][]float64, ch+1)
	block := newBlock(ch, target)
	hb, j, j1, k := s.hb, s.j, s.j1, s.k
	eof, decided, hold := false, false, false
	var wg sync.WaitGroup
	for k < kEnd {
		if pos > limit {
			return fmt.Errorf("%s: frame %d of %d not reached by byte %d", packageName, k, kEnd, pos)
		}
		want := chunk
		n := int64(0)
		if !eof {
			got, err := r.src.read(bufs, int(want))
			n = int64(got)
			s.read += n
			if err != nil {
				eof = true
			}
		}
		if eof && !decided && r.EndHold != nil {
			// the input's end: a next file goes on from here (stop at the last byte), or the filters play out
			decided = true
			if hold = r.EndHold(); !hold {
				kEnd += r.Span()
				_, lastJ = r.span(kEnd)
				limit = r.byteLimit(lastJ, s2[0].step())
			}
		}
		if eof && !hold { // past the file's end: DSD silence flushes the filters
			for ; n < want; n++ {
				for c := range bufs {
					bufs[c][n] = silence
				}
			}
		}
		pos += n
		t0 := time.Now() // the decode's work from here, to a send and on from it (Meter)
		for c := 0; c < ch; c++ {
			wg.Add(1)
			go func(c int) {
				defer wg.Done()
				mid[c] = s1[c].run(bufs[c][:n], mid[c][:0])
			}(c)
		}
		wg.Wait()
		skip := int64(0) // stage 1 outputs before j0: history only
		if j1 < j0 {
			skip = j0 - j1
			if skip > int64(len(mid[0])) {
				skip = int64(len(mid[0]))
			}
		}
		j1 += int64(len(mid[0]))
		for p := range s2 {
			var b []float64
			if 2*p+1 < ch {
				b = mid[2*p+1][skip:]
			}
			full[2*p], full[2*p+1] = s2[p].run(mid[2*p][skip:], b, full[2*p][:0], full[2*p+1][:0])
		}
		if r.up == 0 {
			// stage 2's outputs come back in order, one per stage 1 output: number j+i is frame (j+i - delay) / dec
			// when that divides
			for i := range full[0] {
				jj := j + int64(i)
				if jj < r.delay || (jj-r.delay)%int64(r.dec) != 0 {
					continue
				}
				if kk := (jj - r.delay) / int64(r.dec); kk >= k && kk < kEnd {
					for c := 0; c < ch; c++ {
						v := full[c][i]
						block.s[c] = append(block.s[c], v)
						if a := math.Abs(v); a > r.RawPeak {
							r.RawPeak = a
						}
					}
					k = kk + 1
					if len(block.s[0]) >= target {
						r.Meter.Add(len(block.s[0]), time.Since(t0))
						out <- block.s
						t0 = time.Now()
						r.TotalSamples += int64(len(block.s[0]))
						block = newBlock(ch, target)
					}
				}
			}
		} else {
			// stage 3: every frame whose outputs are all in hand, then drop those the next frame is past
			for c := 0; c < ch; c++ {
				hist[c] = append(hist[c], full[c]...)
			}
			for k < kEnd {
				lo, hi := r.span(k)
				if hi >= hb+int64(len(hist[0])) {
					break
				}
				taps := r.ph[k*int64(r.down)%int64(r.up)]
				for c := 0; c < ch; c++ {
					x := hist[c][lo-hb : lo-hb+int64(len(taps))]
					x = x[:len(taps)] // its length the taps', as the compiler can see: no bounds check per tap
					v := 0.0
					for t, h := range taps {
						v += h * x[t]
					}
					block.s[c] = append(block.s[c], v)
					if a := math.Abs(v); a > r.RawPeak {
						r.RawPeak = a
					}
				}
				k++
				if len(block.s[0]) >= target {
					r.Meter.Add(len(block.s[0]), time.Since(t0))
					out <- block.s
					t0 = time.Now()
					r.TotalSamples += int64(len(block.s[0]))
					block = newBlock(ch, target)
				}
			}
			lo, _ := r.span(k)
			if n := min(lo-hb, int64(len(hist[0]))); n > 0 {
				for c := range hist {
					hist[c] = append(hist[c][:0], hist[c][n:]...)
				}
				hb += n
			}
		}
		j += int64(len(full[0]))
		r.Meter.Add(0, time.Since(t0))
		if eof && hold {
			break // every byte in: the next file goes on from here
		}
	}
	if len(block.s[0]) > 0 {
		r.Meter.Add(len(block.s[0]), 0) // its work is counted above
		out <- block.s
		r.TotalSamples += int64(len(block.s[0]))
	}
	s.hb, s.j, s.j1, s.k = hb, j, j1, k
	// a whole file both ends: a decode begun at a seek or a cue point numbers its frames from the file's start but its
	// bytes from where it began, so a next file going on from it stops short by that much (found live 2026-11-05: a
	// seek 25 s before a track's end cut the next track to its length less the seek's offset)
	s.whole = eof && hold && r.StartTime == 0 && r.EndTime == 0
	r.debug(fmt.Sprintf("%s: decoded %d frames of %d%s", packageName, r.TotalSamples, r.Frames,
		map[bool]string{true: ", the rest held for the next file", false: ""}[s.whole]))
	return nil
}

type blockT struct{ s [][]float64 }

func newBlock(ch, n int) blockT {
	b := blockT{s: make([][]float64, ch)}
	for c := range b.s {
		b.s[c] = make([]float64, 0, n)
	}
	return b
}

func (r *Reader) debug(s string) {
	if r.DebugFunc != nil {
		r.DebugFunc(s)
	}
}

// ---- filter design: Kaiser-windowed sinc ----

func bessel0(x float64) float64 {
	sum, term := 1.0, 1.0
	for k := 1; k < 100; k++ {
		term *= (x / (2 * float64(k))) * (x / (2 * float64(k)))
		sum += term
		if term < 1e-17*sum {
			break
		}
	}
	return sum
}

// halfLength is the centre index a Kaiser low-pass needs for this transition and attenuation.
func halfLength(fs, pass, stop, atten float64) int {
	n := (atten - 7.95) / (14.36 * (stop - pass) / fs)
	return int(math.Ceil(n / 2))
}

// design returns 2*c+1 taps of a linear-phase low-pass centred on c, unity at DC.
func design(fs, pass, stop, atten float64, c int) []float64 {
	n := 2*c + 1
	beta := 0.1102 * (atten - 8.7)
	fc := (pass + stop) / 2 / fs
	h := make([]float64, n)
	sum := 0.0
	for i := range h {
		x := float64(i - c)
		s := 2 * fc
		if x != 0 {
			s = math.Sin(2*math.Pi*fc*x) / (math.Pi * x)
		}
		q := x / float64(c)
		h[i] = s * bessel0(beta*math.Sqrt(math.Max(0, 1-q*q))) / bessel0(beta)
		sum += h[i]
	}
	for i := range h {
		h[i] /= sum
	}
	return h
}

// ---- stage 1: bytes to `mid`, a block at a time ----

type stage1 struct {
	tab   [][256]float64 // tab[i][b]: byte b's contribution at window position i, oldest first
	buf   []byte         // the last k-1 bytes, then the block
	k     int
	every int
	cnt   int
}

// newStage1: every 0 (native) is no filter: run hands back each bit as +-1, the oldest first
func newStage1(h []float64, every int) *stage1 {
	if every == 0 {
		return &stage1{}
	}
	k := len(h) / 8
	s := &stage1{tab: make([][256]float64, k), k: k, every: every, buf: make([]byte, k-1)}
	for j := 0; j < k; j++ { // j bytes back from the newest
		for b := 0; b < 256; b++ {
			v := 0.0
			for t := 0; t < 8; t++ { // t = 0 is the byte's earliest bit, its MSB
				v += h[8*j+7-t] * (float64((b>>(7-t))&1)*2 - 1)
			}
			s.tab[k-1-j][b] = v
		}
	}
	for i := range s.buf {
		s.buf[i] = silence
	}
	return s
}

func (s *stage1) run(in []byte, out []float64) []float64 {
	if s.every == 0 {
		for _, b := range in {
			out = append(out, bitsPM[b][:]...)
		}
		return out
	}
	k := s.k
	s.buf = append(s.buf[:k-1], in...)
	tab := s.tab
	for i := range in {
		if s.cnt++; s.cnt < s.every {
			continue
		}
		s.cnt = 0
		w := s.buf[i : i+k]
		w = w[:len(tab)]
		var a0, a1, a2, a3 float64
		j := 0
		for ; j+3 < len(w); j += 4 {
			t4, w4 := tab[j:j+4:j+4], w[j:j+4:j+4] // one bounds check for each four bytes, not four
			a0 += t4[0][w4[0]]
			a1 += t4[1][w4[1]]
			a2 += t4[2][w4[2]]
			a3 += t4[3][w4[3]]
		}
		for ; j < len(w); j++ {
			a0 += tab[j][w[j]]
		}
		out = append(out, (a0+a1)+(a2+a3))
	}
	copy(s.buf, s.buf[len(s.buf)-(k-1):])
	s.buf = s.buf[:k-1]
	return out
}

// ---- stage 2: an FIR at `mid` by overlap-save FFT, two channels as one complex signal ----

type stage2 struct {
	gain float64      // a single tap: a multiply, no FFT
	m, n int          // taps, FFT size
	hf   []complex128 // the filter's FFT
	x    []complex128 // m-1 samples of history, then new ones
	fill int
	fox  bool         // native: through foxFFT, in place, into w (newStage2Native)
	w    []complex128
	// outputs not yet handed back: an FFT block runs only when full
	pl, pr []float64
}

func newStage2(h []float64) *stage2 {
	s := &stage2{m: len(h)}
	if len(h) == 1 {
		s.gain = h[0]
		return s
	}
	s.n = 4096
	for s.n < 4*s.m {
		s.n *= 2
	}
	hc := make([]complex128, s.n)
	for i, v := range h {
		hc[i] = complex(v, 0)
	}
	s.hf = fft.Fft(hc, false)
	s.x = make([]complex128, s.n)
	s.fill = s.m - 1
	return s
}

// newStage2Native (the native round): the same FIR by overlap-save, through foxFFT, the convolver's FFT, in place: no
// buffer a block (scientificgo's hands back new ones, 512 KB a block at 8192 points, about 215 MB a second at DSD64,
// most of the decode's time in the garbage collector) and 1.9x the speed at 8192 points (M3). The DXD route keeps
// scientificgo's, so its bits stay as they were.
func newStage2Native(h []float64) *stage2 {
	s := &stage2{m: len(h), fox: true}
	if len(h) == 1 {
		s.gain = h[0]
		return s
	}
	s.n = 4096
	for s.n < 4*s.m {
		s.n *= 2
	}
	s.hf = make([]complex128, s.n)
	for i, v := range h {
		s.hf[i] = complex(v, 0)
	}
	foxFFT.FftUnchecked(s.hf, false)
	s.x = make([]complex128, s.n)
	s.w = make([]complex128, s.n)
	s.fill = s.m - 1
	return s
}

// newStage2For: this decode's stage 2, native's through foxFFT, the DXD route's as before
func (r *Reader) newStage2For() *stage2 {
	if r.every == 0 {
		return newStage2Native(r.h2)
	}
	return newStage2(r.h2)
}

// step is the inputs per FFT block (1 for a single tap): blocks start every step inputs from the first
func (s *stage2) step() int64 {
	if s.hf == nil {
		return 1
	}
	return int64(s.n - s.m + 1)
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// run filters a and b (b nil: one channel) and appends one output per input to l and r. The FFT path
// hands back outputs a block behind, so the caller's frame numbering counts what comes back.
func (s *stage2) run(a, b []float64, l, r []float64) ([]float64, []float64) {
	if s.fox {
		return s.runNative(a, b, l, r)
	}
	if s.hf == nil {
		for i := range a {
			l = append(l, a[i]*s.gain)
			if b != nil {
				r = append(r, b[i]*s.gain)
			} else {
				r = append(r, 0)
			}
		}
		return l, r
	}
	for i := range a {
		v := 0.0
		if b != nil {
			v = b[i]
		}
		s.x[s.fill] = complex(a[i], v)
		if s.fill++; s.fill == s.n {
			y := fft.Fft(s.x, false)
			for i := range y {
				y[i] *= s.hf[i]
			}
			y = fft.Fft(y, true)
			for i := s.m - 1; i < s.n; i++ {
				l = append(l, real(y[i]))
				r = append(r, imag(y[i]))
			}
			copy(s.x, s.x[s.n-(s.m-1):])
			s.fill = s.m - 1
		}
	}
	return l, r
}

// runNative: run for a native decode (newStage2Native), the same overlap-save through foxFFT in place. A method of its
// own: as a branch in run's block the DXD route's multiply compiled differently (fused), moving its last bits (found
// 2026-11-07: 7 of 18 DXD routes off 0.1.72.46's bitstream, the decode 1 ULP apart); run stays as it was.
func (s *stage2) runNative(a, b []float64, l, r []float64) ([]float64, []float64) {
	if s.hf == nil {
		for i := range a {
			l = append(l, a[i]*s.gain)
			if b != nil {
				r = append(r, b[i]*s.gain)
			} else {
				r = append(r, 0)
			}
		}
		return l, r
	}
	for i := range a {
		v := 0.0
		if b != nil {
			v = b[i]
		}
		s.x[s.fill] = complex(a[i], v)
		if s.fill++; s.fill == s.n {
			y := s.w
			copy(y, s.x)
			foxFFT.FftUnchecked(y, false)
			for i := range y {
				y[i] *= s.hf[i]
			}
			foxFFT.FftUnchecked(y, true)
			for i := s.m - 1; i < s.n; i++ {
				l = append(l, real(y[i]))
				r = append(r, imag(y[i]))
			}
			copy(s.x, s.x[s.n-(s.m-1):])
			s.fill = s.m - 1
		}
	}
	return l, r
}

// ---- DSF: 4096-byte blocks per channel, interleaved by block, LSB first ----

type source struct {
	rate     int
	channels int
	bytes    int64 // per channel
	name     string
	read     func(out [][]byte, n int) (int, error) // up to n bytes per channel, MSB first
	seek     func(byteInChannel int64) error
}

var rev [256]byte

// bitsPM: a byte's eight bits as +-1, the oldest (its MSB, as the sources hand bytes over) first: the native stage 1
var bitsPM [256][8]float64

func init() {
	for b := range bitsPM {
		for t := 0; t < 8; t++ {
			bitsPM[b][t] = float64((b>>(7-t))&1)*2 - 1
		}
	}
	for i := range rev {
		b, v := byte(i), byte(0)
		for k := 0; k < 8; k++ {
			v = v<<1 | b&1
			b >>= 1
		}
		rev[i] = v
	}
}

func openDSF(f *os.File) (*source, error) {
	le := binary.LittleEndian
	var hdr [28]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil || string(hdr[:4]) != "DSD " {
		return nil, errors.New("DSF: no DSD chunk")
	}
	var fm [12]byte
	if _, err := io.ReadFull(f, fm[:]); err != nil || string(fm[:4]) != "fmt " {
		return nil, errors.New("DSF: no fmt chunk")
	}
	body := make([]byte, int64(le.Uint64(fm[4:]))-12)
	if len(body) < 40 {
		return nil, errors.New("DSF: fmt chunk too short")
	}
	if _, err := io.ReadFull(f, body); err != nil {
		return nil, errors.New("DSF: fmt chunk cut short")
	}
	if le.Uint32(body[4:]) != 0 {
		return nil, errors.New("DSF: not raw DSD")
	}
	ch := int(le.Uint32(body[12:]))
	rate := int(le.Uint32(body[16:]))
	bps := le.Uint32(body[20:])
	count := int64(le.Uint64(body[24:]))
	block := int64(le.Uint32(body[32:]))
	if bps != 1 || block != 4096 || ch < 1 || ch > 6 || rate <= 0 {
		return nil, fmt.Errorf("DSF: unsupported: %d bits, block %d, %d channels, %d Hz", bps, block, ch, rate)
	}
	var dataLen int64
	for {
		var c [12]byte
		if _, err := io.ReadFull(f, c[:]); err != nil {
			return nil, errors.New("DSF: no data chunk")
		}
		if string(c[:4]) == "data" {
			dataLen = int64(le.Uint64(c[4:])) - 12
			break
		}
		if _, err := f.Seek(int64(le.Uint64(c[4:]))-12, io.SeekCurrent); err != nil {
			return nil, err
		}
	}
	dataStart, _ := f.Seek(0, io.SeekCurrent)
	if st, err := f.Stat(); err == nil && dataStart+dataLen > st.Size() { // cut short: what is there plays
		dataLen = st.Size() - dataStart
	}
	total := (count + 7) / 8 // the header's length, within the whole blocks the data chunk holds
	if blocks := dataLen / (block * int64(ch)); total > blocks*block {
		total = blocks * block
	}
	br := bufio.NewReaderSize(f, 1<<20)
	buf := make([]byte, block*int64(ch))
	var pos, have, off int64
	s := &source{rate: rate, channels: ch, bytes: total, name: "DSF"}
	s.seek = func(b int64) error {
		blk := b / block
		if _, err := f.Seek(dataStart+blk*block*int64(ch), io.SeekStart); err != nil {
			return err
		}
		br.Reset(f)
		pos, have, off = b, 0, b-blk*block
		return nil
	}
	s.read = func(out [][]byte, n int) (int, error) {
		got := 0
		for got < n && pos < total {
			if have == 0 {
				if _, err := io.ReadFull(br, buf); err != nil { // within total, so a read error, not the end
					total = pos
					return got, io.EOF
				}
				have = block - off
			}
			i := block - have
			for c := 0; c < ch; c++ {
				out[c][got] = rev[buf[int64(c)*block+i]]
			}
			have--
			off = 0
			pos++
			got++
		}
		if got < n {
			return got, io.EOF
		}
		return got, nil
	}
	return s, nil
}

// ---- DSDIFF: big-endian chunks, channels interleaved by byte, MSB first ----

func openDFF(f *os.File) (*source, error) {
	be := binary.BigEndian
	var h [16]byte
	if _, err := io.ReadFull(f, h[:]); err != nil || string(h[:4]) != "FRM8" || string(h[12:16]) != "DSD " {
		return nil, errors.New("DSDIFF: no FRM8 DSD form")
	}
	var rate, ch int
	var dataStart, dataLen int64
	for dataStart == 0 {
		var c [12]byte
		if _, err := io.ReadFull(f, c[:]); err != nil {
			return nil, errors.New("DSDIFF: no DSD chunk")
		}
		id, size := string(c[:4]), int64(be.Uint64(c[4:]))
		switch id {
		case "PROP":
			body := make([]byte, size)
			if _, err := io.ReadFull(f, body); err != nil {
				return nil, errors.New("DSDIFF: PROP chunk cut short")
			}
			if size&1 == 1 {
				f.Seek(1, io.SeekCurrent)
			}
			for i := 4; i+12 <= len(body); { // after "SND "
				sid, ss := string(body[i:i+4]), int(be.Uint64(body[i+4:]))
				if i+12+ss > len(body) {
					break
				}
				d := body[i+12 : i+12+ss]
				switch sid {
				case "FS  ":
					if ss >= 4 {
						rate = int(be.Uint32(d))
					}
				case "CHNL":
					if ss >= 2 {
						ch = int(be.Uint16(d))
					}
				case "CMPR":
					if ss >= 4 && string(d[:4]) != "DSD " {
						return nil, fmt.Errorf("DSDIFF: compressed (%q): ffmpeg decodes it", string(d[:4]))
					}
				}
				i += 12 + ss + ss&1
			}
		case "DSD ":
			dataStart, _ = f.Seek(0, io.SeekCurrent)
			dataLen = size
		case "DST ":
			return nil, errors.New("DSDIFF: DST-compressed: ffmpeg decodes it")
		default:
			if _, err := f.Seek(size+size&1, io.SeekCurrent); err != nil {
				return nil, err
			}
		}
	}
	if rate <= 0 || ch < 1 || ch > 6 {
		return nil, fmt.Errorf("DSDIFF: %d Hz, %d channels", rate, ch)
	}
	if st, err := f.Stat(); err == nil && dataStart+dataLen > st.Size() { // cut short: what is there plays
		dataLen = st.Size() - dataStart
	}
	total := dataLen / int64(ch)
	br := bufio.NewReaderSize(f, 1<<20)
	var pos int64
	frame := make([]byte, ch)
	s := &source{rate: rate, channels: ch, bytes: total, name: "DSDIFF"}
	s.seek = func(b int64) error {
		if _, err := f.Seek(dataStart+b*int64(ch), io.SeekStart); err != nil {
			return err
		}
		br.Reset(f)
		pos = b
		return nil
	}
	s.read = func(out [][]byte, n int) (int, error) {
		got := 0
		for got < n && pos < total {
			if _, err := io.ReadFull(br, frame); err != nil {
				return got, io.EOF
			}
			for c := 0; c < ch; c++ {
				out[c][got] = frame[c]
			}
			pos++
			got++
		}
		if got < n {
			return got, io.EOF
		}
		return got, nil
	}
	return s, nil
}
