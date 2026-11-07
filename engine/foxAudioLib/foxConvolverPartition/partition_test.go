package foxConvolverPartition

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"math"
	"math/cmplx"
	"math/rand"
	"testing"

	fft "github.com/Foxenfurter/foxAudioLib/foxFFT"
	"github.com/Foxenfurter/foxAudioLib/foxUtils"
)

// convolveAt runs signal through a convolver with impulse h, its partitions P samples (the package's sizes set for it,
// then put back), and returns the output.
func convolveAt(P int, h, signal []float64) []float64 {
	old := PartitionedChunkSize
	SetPartitionSizes(P)
	defer SetPartitionSizes(old)
	pc := NewPartitionedConvolver(h, 0) // rate 0: the sizes as set, not chosen by rate
	pc.InitForStreaming()
	in := make(chan []float64, 4)
	out := make(chan []float64, 4)
	go func() {
		for i := 0; i < len(signal); i += 8192 {
			in <- signal[i:min(i+8192, len(signal))]
		}
		close(in)
	}()
	go func() {
		pc.ConvolveChannel(in, out)
		close(out)
	}()
	var y []float64
	for b := range out {
		y = append(y, b...)
	}
	return y
}

// TestPartitionSizesSameConvolution (SqueezeDSP fork): partitions of 4096 to 262,144 samples compute the same
// convolution as today's 8192, to rounding: a 200,000-tap impulse, 300,000 samples of noise.
func TestPartitionSizesSameConvolution(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	h := make([]float64, 200000)
	for i := range h {
		h[i] = rng.NormFloat64() * math.Exp(-float64(i)/40000) / 50
	}
	x := make([]float64, 300000)
	for i := range x {
		x[i] = rng.Float64()*2 - 1
	}
	ref := convolveAt(8192, h, x)
	peak := 0.0
	for _, v := range ref {
		peak = math.Max(peak, math.Abs(v))
	}
	for _, P := range []int{4096, 16384, 32768, 65536, 131072, 262144} {
		y := convolveAt(P, h, x)
		if len(y) != len(ref) {
			t.Fatalf("partitions of %d: %d samples out, 8192's %d", P, len(y), len(ref))
		}
		worst := 0.0
		for i := range y {
			worst = math.Max(worst, math.Abs(y[i]-ref[i]))
		}
		if db := 20 * math.Log10(worst/peak+1e-300); db > -250 {
			t.Fatalf("partitions of %d: %.1f dB from 8192's (peak %.3g)", P, db, peak)
		} else {
			t.Logf("partitions of %d: %.1f dB from 8192's", P, db)
		}
	}
}

// TestPartitionSize: Fox's sizes below 176.4k, outside streaming and for short impulses; at 176.4k and up his 8192
// doubled until the impulse needs at most 16 partitions, up to 65,536.
func TestPartitionSize(t *testing.T) {
	S, R := foxUtils.ModeStreaming, foxUtils.ModeRealtime
	for _, c := range []struct {
		rate, taps int
		mode       foxUtils.ProcessingMode
		want       int
	}{
		{44100, 2000000, S, 4096}, // below 176.4k: Fox's, however long
		{96000, 96000, S, 4096},
		{705600, 0, S, 8192},       // no impulse
		{705600, 105840, S, 8192},  // the PEQ alone (3 x rate / 20 Hz): 13 partitions of 8192
		{705600, 131072, S, 8192},  // 16 of them
		{705600, 131073, S, 16384}, // 17: doubled
		{705600, 333534, S, 32768}, // half a second: 11
		{705600, 705600, S, 65536}, // a whole second: 11
		{705600, 4000000, S, 65536},
		{352800, 352800, S, 32768},
		{176400, 176400, S, 16384},
		{705600, 705600, R, 512}, // realtime: Fox's
	} {
		if got := partitionSize(c.rate, c.taps, c.mode); got != c.want {
			t.Errorf("rate %d, %d taps, mode %d: partitions of %d, want %d", c.rate, c.taps, c.mode, got, c.want)
		}
	}
}

// fineGain: |H| of h at its largest, on an FFT grid of 16 points a tap (the reference peakGain is checked against).
func fineGain(h []float64) float64 {
	n := NextPowerOf2(16 * len(h))
	in := make([]float64, n)
	out := make([]complex128, n/2+1)
	copy(in, h)
	if err := fft.RealFft(in, out); err != nil {
		panic(err)
	}
	peak := 0.0
	for _, v := range out {
		peak = math.Max(peak, cmplx.Abs(v))
	}
	return peak
}

// MaxGain (the level's headroom, PrepareChannelGains) is the whole filter's peak gain: within 0.01 dB of a grid 16 points
// a tap fine, never below the coarse FFT's largest bin, and the same at every partition size, bit for bit. Fox's measure
// (the largest bin of any one partition, maxGainFromPartitions) read otherwise, and moved with the partitions (ledger
// PER-PARTITION PEAK READS THE FILTER WRONG). A new impulse (AmendFilterImpulse) is measured alike.
func TestMaxGainIsTheFiltersPeak(t *testing.T) {
	old := PartitionedChunkSize
	defer SetPartitionSizes(old)
	h := decaying(333534, 9)
	SetPartitionSizes(8192)
	at8192 := NewPartitionedConvolver(h, 0)
	fox := at8192.maxGainFromPartitions()
	pc := NewPartitionedConvolver(h, 705600)
	if PartitionedChunkSize <= 8192 {
		t.Fatalf("partitions of %d at 705.6k for %d taps: the test wants larger ones", PartitionedChunkSize, len(h))
	}
	if math.Float64bits(pc.MaxGain) != math.Float64bits(at8192.MaxGain) {
		t.Fatalf("MaxGain %v over partitions of %d, %v over 8192: it moved with the partitions", pc.MaxGain, PartitionedChunkSize, at8192.MaxGain)
	}
	ref := fineGain(h)
	if db := 20 * math.Log10(pc.MaxGain/ref); math.Abs(db) > 0.01 {
		t.Fatalf("MaxGain %v, the filter's peak %v: %.4f dB off", pc.MaxGain, ref, db)
	}
	if fox == pc.MaxGain {
		t.Fatalf("Fox's per-partition measure reads %v too: the test cannot tell the two apart", fox)
	}
	t.Logf("MaxGain %.6f, the 16x grid %.6f, Fox's per partition %.6f (%.2f dB)", pc.MaxGain, ref, fox, 20*math.Log10(fox/ref))
	h2 := decaying(300000, 10)
	pc.AmendFilterImpulse(h2)
	if want := fineGain(h2); math.Abs(20*math.Log10(pc.MaxGain/want)) > 0.01 {
		t.Fatalf("after AmendFilterImpulse MaxGain %v, the filter's peak %v", pc.MaxGain, want)
	}
}

// A filter whose peak is one narrow resonance halfway between two bins of the FFT peakGain takes: the largest bin
// reads it low; the refinement between bins brings MaxGain to the peak a grid 16 points a tap fine finds.
func TestMaxGainBetweenBins(t *testing.T) {
	const taps = 50000
	n := NextPowerOf2(max(4*taps, 1<<16))
	w0 := 2 * math.Pi * (6000.5) / float64(n) // halfway between bins 6000 and 6001 of peakGain's grid
	h := make([]float64, taps)
	for i := range h {
		h[i] = math.Exp(-float64(i)/15000) * math.Cos(w0*float64(i))
	}
	in := make([]float64, n)
	out := make([]complex128, n/2+1)
	copy(in, h)
	if err := fft.RealFft(in, out); err != nil {
		t.Fatal(err)
	}
	bin := 0.0
	for _, v := range out {
		bin = math.Max(bin, cmplx.Abs(v))
	}
	ref := fineGain(h)
	got := peakGain(h)
	t.Logf("the largest bin %.3f dB, MaxGain %.4f dB, against the fine grid's peak", 20*math.Log10(bin/ref), 20*math.Log10(got/ref))
	if 20*math.Log10(bin/ref) > -0.05 {
		t.Fatalf("the largest bin only %.3f dB under the peak: the case does not need the refinement", 20*math.Log10(bin/ref))
	}
	if db := 20 * math.Log10(got/ref); math.Abs(db) > 0.01 {
		t.Fatalf("MaxGain %.4f dB from the peak between bins", db)
	}
	// and beside it a broad peak 0.04 dB lower, whose bin is then the largest: the narrow one, refined, is the peak
	broad := make([]float64, 64)
	w1 := 2 * math.Pi * 0.1
	sum := 0.0
	for i := range broad {
		win := 0.5 - 0.5*math.Cos(2*math.Pi*(float64(i)+0.5)/64)
		broad[i] = win * math.Cos(w1*float64(i))
		sum += win
	}
	scale := ref * math.Pow(10, -0.04/20) / (sum / 2)
	h2 := append([]float64(nil), h...)
	for i, v := range broad {
		h2[i] += scale * v
	}
	copy(in, h2)
	clear(in[len(h2):])
	if err := fft.RealFft(in, out); err != nil {
		t.Fatal(err)
	}
	at, top := 0, 0.0
	for i, v := range out {
		if m := cmplx.Abs(v); m > top {
			at, top = i, m
		}
	}
	if at < 12000 { // the broad peak sits at bin n/10, the narrow one at 6000
		t.Fatalf("the largest bin is %d, the narrow peak's: the case does not test the other candidates", at)
	}
	ref2 := fineGain(h2)
	if db := 20 * math.Log10(peakGain(h2)/ref2); math.Abs(db) > 0.01 {
		t.Fatalf("beside a broad peak with the largest bin: MaxGain %.4f dB from the peak", db)
	}
}

// FIR Strength under 100 at 176.4k and up (the ledger's case): half the impulse a dry pass at sample 0, half a
// lowpass a partition or more later. Where the two add (low frequencies) the filter's gain is about 1, so MaxGain must
// be about 1, and a tone above the lowpass (the dry half alone, 0.5) comes out 6 dB down after 1/MaxGain. Fox's measure
// saw each half in its own partition, about 0.5, so 1/MaxGain doubled the level: that tone at about 0 dB, the halves
// together about 6 dB over full scale.
func TestMaxGainStrengthBlend(t *testing.T) {
	old := PartitionedChunkSize
	defer SetPartitionSizes(old)
	const rate = 384000
	// a linear-phase lowpass at 20 kHz, 25 ms of delay (9600 samples), windowed
	n := 19201
	lp := make([]float64, n)
	m := float64(n-1) / 2
	fc := 20000.0 / rate
	for i := range lp {
		x := float64(i) - m
		s := 2 * fc
		if x != 0 {
			s = math.Sin(2*math.Pi*fc*x) / (math.Pi * x)
		}
		lp[i] = s * (0.42 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n-1)) + 0.08*math.Cos(4*math.Pi*float64(i)/float64(n-1)))
	}
	h := make([]float64, n)
	h[0] = 0.5
	for i, v := range lp {
		h[i] += 0.5 * v
	}
	pc := NewPartitionedConvolver(h, rate)
	SetPartitionSizes(8192)
	fox := NewPartitionedConvolver(h, 0).maxGainFromPartitions()
	if math.Abs(pc.MaxGain-1) > 0.02 {
		t.Fatalf("MaxGain %v for halves that add to about 1 at low frequencies", pc.MaxGain)
	}
	if fox > 0.75 {
		t.Fatalf("Fox's measure %v: the case does not show what it is for", fox)
	}
	// a 30 kHz tone, above the lowpass: the dry half alone
	var re, im float64
	for i, v := range h {
		s, c := math.Sincos(2 * math.Pi * 30000 * float64(i) / rate)
		re, im = re+v*c, im-v*s
	}
	out := math.Hypot(re, im) / pc.MaxGain
	if db := 20 * math.Log10(out); math.Abs(db+6.02) > 0.1 {
		t.Fatalf("a 30 kHz tone at %.2f dB after 1/MaxGain, want -6.02", db)
	}
	t.Logf("MaxGain %.4f (Fox's per partition %.4f): a 30 kHz tone at %.2f dB, Fox's level %.2f dB", pc.MaxGain, fox,
		20*math.Log10(out), 20*math.Log10(math.Hypot(re, im)/fox))
}

// A filter cache an earlier engine wrote (no version first) is refused, so the filter, and its MaxGain, is built again;
// the version this engine writes decodes.
func TestFilterCacheVersion(t *testing.T) {
	old := PartitionedChunkSize
	defer SetPartitionSizes(old)
	pc := NewPartitionedConvolver(decaying(100000, 14), 352800)
	P := PartitionedChunkSize
	data, err := pc.GobEncode()
	if err != nil {
		t.Fatal(err)
	}
	if err := (&PartitionedConvolver{}).GobDecode(data); err != nil {
		t.Fatalf("this engine's cache refused: %v", err)
	}
	// as 0.1.72.36 wrote it: the sizes first
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	for _, v := range []any{PartitionedChunkSize, PartitionedFFTSize, PartitionedHalfFFTSize, PartitionedImpulseChunkSize,
		PartitionedOverlapSize, pc.FilterImpulse, pc.impulseLength, pc.outputLength, pc.numPartitions, pc.ringSize,
		pc.impulsePartitions, pc.MaxGain, pc.RMSGain} {
		if err := enc.Encode(v); err != nil {
			t.Fatal(err)
		}
	}
	SetPartitionSizes(4096)
	if err := (&PartitionedConvolver{}).GobDecode(buf.Bytes()); err == nil {
		t.Fatal("an earlier engine's cache decoded: its MaxGain would stand")
	}
	if PartitionedChunkSize != 4096 {
		t.Fatalf("a refused cache set the package's sizes (partitions of %d)", PartitionedChunkSize)
	}
	// a cache laid out as this engine's but another version (the next change to what it holds): refused too
	var other bytes.Buffer
	enc = gob.NewEncoder(&other)
	for _, v := range []any{filterCacheVersion - 1, P, PartitionedFFTSize, PartitionedHalfFFTSize, PartitionedImpulseChunkSize,
		PartitionedOverlapSize, pc.FilterImpulse, pc.impulseLength, pc.outputLength, pc.numPartitions, pc.ringSize,
		pc.impulsePartitions, pc.MaxGain, pc.RMSGain} {
		if err := enc.Encode(v); err != nil {
			t.Fatal(err)
		}
	}
	if err := (&PartitionedConvolver{}).GobDecode(other.Bytes()); err == nil {
		t.Fatal("a cache of another version decoded")
	}
}

// The filter cache (filter_<client>_<rate>.gob) keeps the larger partitions: decoding one built at 705.6k puts its
// sizes back whatever the package held, and the decoded filter convolves with the same bits as the one encoded.
func TestCacheKeepsPartitionSize(t *testing.T) {
	old := PartitionedChunkSize
	defer SetPartitionSizes(old)
	h := decaying(333534, 12)
	pc := NewPartitionedConvolver(h, 705600)
	P := PartitionedChunkSize
	data, err := pc.GobEncode()
	if err != nil {
		t.Fatal(err)
	}
	SetPartitionSizes(8192)
	back := &PartitionedConvolver{}
	if err := back.GobDecode(data); err != nil {
		t.Fatal(err)
	}
	if PartitionedChunkSize != P || back.MaxGain != pc.MaxGain {
		t.Fatalf("decoded: partitions of %d (encoded %d), MaxGain %v (encoded %v)", PartitionedChunkSize, P, back.MaxGain, pc.MaxGain)
	}
	pc.InitForStreaming()
	back.InitForStreaming()
	rng := rand.New(rand.NewSource(13))
	for c := 0; c < 3; c++ {
		chunk := make([]float64, P)
		for i := range chunk {
			chunk[i] = rng.Float64()*2 - 1
		}
		a := append([]float64(nil), pc.processChunk(chunk, true)...)
		b := back.processChunk(chunk, true)
		for i := range a {
			if math.Float64bits(a[i]) != math.Float64bits(b[i]) {
				t.Fatalf("chunk %d sample %d: %v, the decoded filter %v", c, i, a[i], b[i])
			}
		}
	}
}

// decaying is a reproducible impulse: noise under an exponential decay.
func decaying(taps int, seed int64) []float64 {
	rng := rand.New(rand.NewSource(seed))
	h := make([]float64, taps)
	for i := range h {
		h[i] = rng.NormFloat64() * math.Exp(-float64(i)/30000) / 20
	}
	return h
}

// benchPartition: one second of audio at rate through an impulse of taps, partitions of P, the ring full as in steady
// playback; an op is that second.
func benchPartition(b *testing.B, rate, taps, P int) {
	rng := rand.New(rand.NewSource(5))
	h := make([]float64, taps)
	for i := range h {
		h[i] = rng.NormFloat64() * math.Exp(-float64(i)/float64(taps/7+1)) / 100
	}
	old := PartitionedChunkSize
	SetPartitionSizes(P)
	defer SetPartitionSizes(old)
	pc := NewPartitionedConvolver(h, 0)
	pc.InitForStreaming()
	chunk := make([]float64, P)
	for i := range chunk {
		chunk[i] = rng.Float64()*2 - 1
	}
	for range pc.ringSize {
		pc.processChunk(chunk, true)
	}
	per := (rate + P - 1) / P // chunks a second
	b.ResetTimer()
	for range b.N {
		for range per {
			pc.processChunk(chunk, true)
		}
	}
}

// Impulses of several lengths at the engine's rates (the converter's 705.6k: the PEQ alone, 3 x rate / 20 Hz, up to a
// whole second; the speed bench's FIR is 667,067 taps there once CleanUpImpulse trims it; DXD's 352.8k; 176.4k; 96k)
// through partitions of 4096 to 131,072: go test -run XXX -bench Partition
func BenchmarkPartition(b *testing.B) {
	for _, r := range []struct{ rate, taps int }{
		{705600, 105840}, {705600, 240000}, {705600, 333534}, {705600, 480000}, {705600, 705600},
		{352800, 120000}, {352800, 176400}, {352800, 352800},
		{176400, 176400},
		{96000, 96000},
	} {
		for _, P := range []int{4096, 8192, 16384, 32768, 65536, 131072} {
			b.Run(fmt.Sprintf("r=%d/taps=%d/P=%d", r.rate, r.taps, P), func(b *testing.B) { benchPartition(b, r.rate, r.taps, P) })
		}
	}
}
