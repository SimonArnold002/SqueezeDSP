package LyrionDSPProcessAudio

import (
	"math"
	"sync"
	"testing"

	"github.com/Foxenfurter/foxAudioLib/gipsyUpsampler"
)

// upsampleStage (0.1.72.33): with an upsampler, every block goes up to its rate and the filters' last output follows
// the decoder's end, so N frames in give round(N*out/in) out, in order; without one, the decoder's own channel.
func TestUpsampleStage(t *testing.T) {
	ap := &AudioProcessor{}
	in := make(chan [][]float64, 2)
	var wg sync.WaitGroup
	if got := ap.upsampleStage(in, 1, &wg); got != in {
		t.Fatal("no upsampler: not the decoder's own channel")
	}
	u, err := gipsyUpsampler.New(44100, 705600, 2, gipsyUpsampler.DefaultCD, gipsyUpsampler.DefaultHiRes)
	if err != nil {
		t.Fatal(err)
	}
	ap.Upsampler = u
	out := ap.upsampleStage(in, 1, &wg)
	go func() {
		for b := 0; b < 5; b++ {
			blk := [][]float64{make([]float64, 4410), make([]float64, 4410)}
			for i := range blk[0] {
				v := 0.5 * math.Sin(2*math.Pi*1000*float64(b*4410+i)/44100)
				blk[0][i], blk[1][i] = v, -v
			}
			in <- blk
		}
		close(in)
	}()
	var l, r []float64
	for blk := range out {
		l, r = append(l, blk[0]...), append(r, blk[1]...)
	}
	wg.Wait()
	if len(l) != 5*4410*16 || len(r) != len(l) {
		t.Fatalf("%d and %d frames out for %d in, want %d", len(l), len(r), 5*4410, 5*4410*16)
	}
	// in order and in time: the middle against the sine at the output rate (the default filter is intermediate phase:
	// in time at low frequencies within a fraction of a sample)
	worst := 0.0
	for i := len(l) / 4; i < 3*len(l)/4; i++ {
		ideal := 0.5 * math.Sin(2*math.Pi*1000*float64(i)/705600)
		worst = math.Max(worst, math.Max(math.Abs(l[i]-ideal), math.Abs(r[i]+ideal)))
	}
	if worst > 1e-3 {
		t.Fatalf("the middle off the sine by %.2g", worst)
	}
}
