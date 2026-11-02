package foxConvolverPartition

// Our tests (ledger A RESAMPLED IMPULSE GAINS THE RATE RATIO): RMSGain is measured over the audio
// band, so the same filter reads the same at every rate; without a rate it is the L2 norm, as before.
//   cd engine/foxAudioLib && go test -run 'RMSGain' ./foxConvolverPartition/

import (
	"bytes"
	"encoding/gob"
	"math"
	"testing"
)

// lowpass is a Blackman-windowed sinc, unit DC gain: flat to cutoff Hz, silent above it.
func lowpass(rate int, cutoff float64, taps int) []float64 {
	h := make([]float64, taps)
	sum := 0.0
	m := float64(taps - 1)
	for i := range h {
		x := float64(i) - m/2
		s := 2 * cutoff / float64(rate)
		if x != 0 {
			s = math.Sin(2*math.Pi*cutoff/float64(rate)*x) / (math.Pi * x)
		}
		w := 0.42 - 0.5*math.Cos(2*math.Pi*float64(i)/m) + 0.08*math.Cos(4*math.Pi*float64(i)/m)
		h[i] = s * w
		sum += h[i]
	}
	for i := range h {
		h[i] /= sum
	}
	return h
}

func db(x float64) float64 { return 20 * math.Log10(x) }

func TestRMSGainInBand(t *testing.T) {
	// The requirement: one filter (flat to 23 kHz) made at 96k and at 384k reads ~0 dB at both.
	// Its L2 norm, the old figure, falls with the rate: that is what made the gain depend on it.
	for _, rate := range []int{44100, 96000, 352800, 384000} {
		cut := math.Min(23000, float64(rate)/2*0.9)
		h := lowpass(rate, cut, rate/20+1)
		inBand := RMSGainInBand(h, rate, 20, 20000)
		if math.Abs(db(inBand)) > 0.05 {
			t.Errorf("%d Hz: in-band RMS %.3f dB, want ~0", rate, db(inBand))
		}
		l2 := (&PartitionedConvolver{FilterImpulse: h}).rmsGainFromImpulse()
		if rate == 384000 && db(l2) > -5 {
			t.Errorf("384k: the L2 norm should read far below 0 dB (it measured the silent band), got %.2f dB", db(l2))
		}
	}
	// a 6 dB gain everywhere reads 6 dB; a unit impulse reads 1; empty or silent reads 1 (as before)
	h := lowpass(96000, 23000, 4801)
	for i := range h {
		h[i] *= 2
	}
	if got := db(RMSGainInBand(h, 96000, 20, 20000)); math.Abs(got-6.02) > 0.05 {
		t.Errorf("+6 dB filter: %.3f dB", got)
	}
	if got := RMSGainInBand([]float64{1}, 384000, 20, 20000); math.Abs(got-1) > 1e-9 {
		t.Errorf("unit impulse: %v, want 1", got)
	}
	if RMSGainInBand(nil, 96000, 20, 20000) != 1 || RMSGainInBand(make([]float64, 64), 96000, 20, 20000) != 1 {
		t.Errorf("empty or silent impulse: want 1")
	}
	// a cut below 20 Hz is outside the band; the band stops at Nyquist on a low rate
	if got := RMSGainInBand([]float64{1}, 8000, 20, 20000); math.Abs(got-1) > 1e-9 {
		t.Errorf("8 kHz rate (band capped at 4 kHz): %v, want 1", got)
	}
}

func TestRMSGainCarriers(t *testing.T) {
	h := lowpass(384000, 23000, 19201)
	// the constructor knows the rate: RMSGain in band
	pc := NewPartitionedConvolver(h, 384000)
	if math.Abs(db(pc.RMSGain)) > 0.05 {
		t.Errorf("NewPartitionedConvolver at 384k: RMSGain %.3f dB, want ~0 (in band)", db(pc.RMSGain))
	}
	// control: no rate, the L2 norm exactly as before
	pc0 := NewPartitionedConvolver(h, 0)
	if want := pc0.rmsGainFromImpulse(); pc0.RMSGain != want {
		t.Errorf("no rate: RMSGain %v, want the L2 norm %v", pc0.RMSGain, want)
	}
	// the filter cache (gob) carries the figure across tracks unchanged
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode([]*PartitionedConvolver{pc}); err != nil {
		t.Fatal(err)
	}
	var back []*PartitionedConvolver
	if err := gob.NewDecoder(&buf).Decode(&back); err != nil {
		t.Fatal(err)
	}
	if back[0].RMSGain != pc.RMSGain || back[0].MaxGain != pc.MaxGain {
		t.Errorf("cache round trip: RMSGain %v MaxGain %v, want %v %v", back[0].RMSGain, back[0].MaxGain, pc.RMSGain, pc.MaxGain)
	}
	// streaming start on a cached filter does not re-derive it (no rate there)
	back[0].InitForStreaming()
	if back[0].RMSGain != pc.RMSGain {
		t.Errorf("InitForStreaming on a cached filter changed RMSGain: %v -> %v", pc.RMSGain, back[0].RMSGain)
	}
	// AmendFilterImpulse on a filter that knows its rate stays in band
	pc.AmendFilterImpulse(lowpass(384000, 23000, 9601))
	if math.Abs(db(pc.RMSGain)) > 0.05 {
		t.Errorf("AmendFilterImpulse: RMSGain %.3f dB, want ~0 (in band)", db(pc.RMSGain))
	}
}
