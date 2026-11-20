package LyrionDSPProcessAudio

import "testing"

// TestJoinFactors (2026-11-20, joingain.go): each channel's last gain over its gain now; nothing to scale when every
// channel's is the same (an album: the stream as before, byte for byte); the last gains not known (a residual from
// before them) or for another channel count: not scaled; a channel at 0 now plays nothing: left as it is.
func TestJoinFactors(t *testing.T) {
	for _, c := range []struct {
		last, now, want []float64
		ok              bool
	}{
		{[]float64{0.5, 0.5}, []float64{0.25, 0.5}, []float64{2, 1}, true},
		{[]float64{0.2, 0.3}, []float64{0.4, 0.6}, []float64{0.5, 0.5}, true},
		{[]float64{0.5, 0.5}, []float64{0.5, 0.5}, nil, true},
		{nil, []float64{0.5, 0.5}, nil, false},
		{[]float64{0.5}, []float64{0.5, 0.5}, nil, false},
		{[]float64{0.5, 0.5}, []float64{0, 0.25}, []float64{1, 2}, true},
	} {
		f, ok := joinFactors(c.last, c.now)
		if ok != c.ok || len(f) != len(c.want) {
			t.Fatalf("last %v, now %v: %v %v, want %v %v", c.last, c.now, f, ok, c.want, c.ok)
		}
		for i := range f {
			if f[i] != c.want[i] {
				t.Fatalf("last %v, now %v: %v, want %v", c.last, c.now, f, c.want)
			}
		}
	}
}
