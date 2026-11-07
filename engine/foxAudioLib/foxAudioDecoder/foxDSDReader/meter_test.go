package foxDSDReader

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Foxenfurter/foxAudioLib/foxSpeed"
)

// The meter (SqueezeDSP fork) counts the decode, not the sends: 1 s of DSD64 to 88.2k (stage 2 picks the frames) and
// to 96k (stage 3), in blocks of 10000 frames and a last part block, sent to a stage that takes one every 50 ms, take
// 0.4 s or more while decoding takes a fraction of that; every frame sent is counted, the last part's too.
func TestMeterLeavesOutWaits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.dsf")
	fx := makeFixture(dsd64, 1.0, []float64{1000, 3000}, 0.4)
	writeDSF(t, path, dsd64, fx.bits, int64(len(fx.x[0])))
	for _, rate := range []int{88200, 96000} {
		r := &Reader{Filename: path, OutRate: rate, Cutoff: 30000, TargetFrameSize: 10000,
			Meter: &foxSpeed.Meter{Name: "decoder", Rate: rate}}
		if err := r.Open(); err != nil {
			t.Fatal(err)
		}
		out := make(chan [][]float64)
		errc := make(chan error, 1)
		go func() {
			errc <- r.DecodeInput(out)
			close(out)
		}()
		t0 := time.Now()
		frames := int64(0)
		for b := range out {
			frames += int64(len(b[0]))
			time.Sleep(50 * time.Millisecond)
		}
		wall := time.Since(t0)
		if err := <-errc; err != nil {
			t.Fatal(err)
		}
		r.Close()
		audio, busy := r.Meter.Figures()
		if frames != r.Frames || r.Frames%10000 == 0 || audio != float64(r.Frames)/float64(rate) || wall < 400*time.Millisecond ||
			busy <= 0 || busy > 0.2 {
			t.Fatalf("%d Hz: %d frames of %d, audio %.4f s, busy %.4f s over %v: want every frame and the decode alone",
				rate, frames, r.Frames, audio, busy, wall)
		}
	}
}
