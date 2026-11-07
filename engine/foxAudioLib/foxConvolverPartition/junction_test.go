package foxConvolverPartition

import (
	"math"
	"testing"
)

// runTrack streams x through pc as ConvolveChannel does in the engine (blocks of 1000 samples) and returns the output.
func runTrack(pc *PartitionedConvolver, x []float64) []float64 {
	in := make(chan []float64, 4)
	out := make(chan []float64, 4)
	go func() {
		for i := 0; i < len(x); i += 1000 {
			in <- append([]float64(nil), x[i:min(i+1000, len(x))]...)
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

// gaplessTracks plays n samples as tracks split at joins the way engine runs do: the first builds the filter (a delay
// of c samples, partitions of P) and each saves its state at its end (SaveDSPResiduals: EncodeState); each next one
// loads the filter from the cache (GobEncode / GobDecode) and that state (LoadDSPResiduals: DecodeState). Returns the
// outputs joined, and the input.
func gaplessTracks(t *testing.T, P, c int, joins []int, n int) (y, x []float64) {
	t.Helper()
	old := PartitionedChunkSize
	SetPartitionSizes(P)
	defer SetPartitionSizes(old)
	h := make([]float64, c+1)
	h[c] = 1
	x = make([]float64, n)
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
		}
		out := runTrack(pc, x[start:end])
		if len(out) != end-start {
			t.Fatalf("partitions of %d, delay %d: track %d gave %d samples for %d", P, c, i+1, len(out), end-start)
		}
		y = append(y, out...)
		start = end
	}
	return y, x
}

// TestGaplessJoin (SqueezeDSP fork): tracks played the way engine runs play them come out as one track would, x
// delayed by the filter's delay, within rounding: a delay of none, under a partition and over several; partitions of
// 4096 to 65,536; the join inside a partition and on its edge; a track shorter than what the last one left over; three
// tracks; a first track shorter than a partition. Fox's hand-over went on from the zero-padded partition with the
// tail of the one before: as many outputs as the delay replayed and then skipped up to a partition.
func TestGaplessJoin(t *testing.T) {
	for _, c := range []struct {
		P, c  int
		joins []int
	}{
		{4096, 0, []int{100000}},
		{4096, 3000, []int{100000}},
		{4096, 20000, []int{100000}},
		{4096, 20000, []int{4096 * 30}},
		{8192, 20000, []int{100000, 100500}},
		{65536, 20000, []int{100000, 200000}},
		{65536, 100000, []int{30000}},
	} {
		y, x := gaplessTracks(t, c.P, c.c, c.joins, 300000)
		for i := range y {
			want := 0.0
			if i >= c.c {
				want = x[i-c.c]
			}
			if math.Abs(y[i]-want) > 1e-9 {
				t.Fatalf("partitions of %d, a delay of %d, joins at %v: sample %d is %.6f, want %.6f", c.P, c.c, c.joins, i, y[i], want)
			}
		}
	}
}
