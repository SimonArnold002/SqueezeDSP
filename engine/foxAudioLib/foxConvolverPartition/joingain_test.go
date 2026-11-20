package foxConvolverPartition

import (
	"math"
	"testing"
)

// TestGaplessJoinGain (SqueezeDSP fork, 2026-11-20, a gain change at a gapless join): tracks played the way engine runs
// play them (gaplessTracks), each one's output multiplied by its own gain (the engine's gain comes after the convolver),
// with ScaleState(last gain / this gain) after DecodeState: every input sample comes out at its OWN track's gain, where
// the filter (a delay) puts it, in whichever track's run that is: inside a partition and over several, a join inside a
// partition and on its edge, a track shorter than what the last one left over, three tracks. The control: with no
// ScaleState the delayed samples of the last track come out at the next one's gain.
func TestGaplessJoinGain(t *testing.T) {
	gains := []float64{0.8, 0.2, 1.3}
	for _, c := range []struct {
		P, c  int
		joins []int
	}{
		{4096, 3000, []int{100000, 180000}},
		{4096, 20000, []int{4096 * 30, 200000}},
		{8192, 20000, []int{100000, 100500}},
		{65536, 100000, []int{30000, 150000}},
	} {
		for _, scale := range []bool{true, false} {
			y, x, track := gaplessTracksGain(t, c.P, c.c, c.joins, 300000, gains, scale)
			worst := 0.0
			for i := range y {
				want := 0.0
				if i >= c.c {
					want = x[i-c.c] * gains[track[i-c.c]]
				}
				worst = math.Max(worst, math.Abs(y[i]-want))
			}
			if scale && worst > 1e-9 {
				t.Errorf("partitions of %d, a delay of %d, joins at %v: scaled, %.3g from each sample at its own track's gain", c.P, c.c, c.joins, worst)
			}
			if !scale && worst < 1e-3 {
				t.Errorf("partitions of %d, a delay of %d, joins at %v: the control (no ScaleState) is %.3g from it: the test cannot see the fault", c.P, c.c, c.joins, worst)
			}
		}
	}
}

// gaplessTracksGain: gaplessTracks with a gain a track (after the convolver), and ScaleState at each join when scale;
// returns the outputs joined, the input, and each input sample's track
func gaplessTracksGain(t *testing.T, P, c int, joins []int, n int, gains []float64, scale bool) (y, x []float64, track []int) {
	t.Helper()
	old := PartitionedChunkSize
	SetPartitionSizes(P)
	defer SetPartitionSizes(old)
	h := make([]float64, c+1)
	h[c] = 1
	x = make([]float64, n)
	track = make([]int, n)
	for i := range x {
		x[i] = math.Sin(float64(i)*0.001) + 0.5*math.Sin(float64(i)*0.0173)
	}
	pc := NewPartitionedConvolver(h, 0)
	cache, err := pc.GobEncode()
	if err != nil {
		t.Fatal(err)
	}
	start := 0
	for i, end := range append(append([]int(nil), joins...), n) {
		if i > 0 {
			state, err := pc.EncodeState()
			if err != nil {
				t.Fatal(err)
			}
			pc = &PartitionedConvolver{}
			if err := pc.GobDecode(cache); err != nil {
				t.Fatal(err)
			}
			if err := pc.DecodeState(state); err != nil {
				t.Fatal(err)
			}
			if scale {
				pc.ScaleState(gains[i-1] / gains[i])
			}
		}
		for k := start; k < end; k++ {
			track[k] = i
		}
		out := runTrack(pc, x[start:end])
		if len(out) != end-start {
			t.Fatalf("partitions of %d, delay %d: track %d gave %d samples for %d", P, c, i+1, len(out), end-start)
		}
		for _, v := range out {
			y = append(y, v*gains[i])
		}
		start = end
	}
	return y, x, track
}
