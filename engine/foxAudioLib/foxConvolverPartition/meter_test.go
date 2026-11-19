package foxConvolverPartition

import (
	"testing"
	"time"

	"github.com/Foxenfurter/foxAudioLib/gipsySpeed"
)

// The meter (SqueezeDSP fork) counts the convolution, not the waits either side: 8 blocks of 4096 frames through a
// 4801-tap filter, sent on to a stage that takes one every 50 ms, take 0.35 s or more while the work takes a few ms;
// every frame taken in is counted. With nothing to convolve the frames count and the work is none.
func TestMeterLeavesOutWaits(t *testing.T) {
	run := func(pc *PartitionedConvolver, blocks int) (audio, busy float64, wall time.Duration) {
		in := make(chan []float64, blocks)
		for range blocks {
			in <- make([]float64, 4096)
		}
		close(in)
		out := make(chan []float64)
		go func() {
			pc.ConvolveChannel(in, out)
			close(out)
		}()
		t0 := time.Now()
		for range out {
			time.Sleep(50 * time.Millisecond)
		}
		audio, busy = pc.Meter.Figures()
		return audio, busy, time.Since(t0)
	}
	pc := NewPartitionedConvolver(lowpass(96000, 23000, 4801), 96000)
	pc.Meter = &gipsySpeed.Meter{Name: "convolver 1", Rate: 96000}
	if audio, busy, wall := run(pc, 8); audio != 8*4096/96000.0 || wall < 350*time.Millisecond || busy <= 0 || busy > 0.1 {
		t.Fatalf("audio %.4f s, busy %.4f s over %v: want every frame and the work alone", audio, busy, wall)
	}
	none := &PartitionedConvolver{Meter: &gipsySpeed.Meter{Name: "convolver 1", Rate: 96000}}
	if audio, busy, _ := run(none, 2); audio != 2*4096/96000.0 || busy != 0 {
		t.Fatalf("nothing to convolve: audio %.4f s, busy %.4f s, want the frames and no work", audio, busy)
	}
}
