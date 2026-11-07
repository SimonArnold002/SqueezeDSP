// Package: github.com/Foxenfurter/foxAudioLib/foxUpsampler
// filename bench.go
//
// Bench: stage A's loop timed on this machine for the engine's --bench (the plugin's /squeezedsp/bench: plex's own
// figures with no ssh and no play test, Simon 2026-11-06), pushScalar against push (AVX2 where the build and the CPU
// have it): Kaiser short, Gaussian long and Kaiser extra-long's CD lengths at 2/1. Random taps: the time is the count's.
package foxUpsampler

import (
	"fmt"
	"io"
	"math"
	"math/rand"
	"time"
)

func Bench(w io.Writer, each time.Duration) {
	for _, n := range []int{515, 7003, 9537} {
		r := rand.New(rand.NewSource(1))
		proto := make([]float64, n)
		for i := range proto {
			proto[i] = r.Float64()*2 - 1
		}
		x := make([]float64, 4096)
		for i := range x {
			x[i] = r.Float64()*2 - 1
		}
		scalar := timePush(each, proto, x, (*poly).pushScalar)
		fast := timePush(each, proto, x, (*poly).push)
		fmt.Fprintf(w, "upsampler stage A 2/1, %d taps: %.1f ns an output, AVX2 %t: %.1f (%.2fx)\n", n, scalar, haveAVX2, fast, scalar/fast)
	}
}

// timePush: the best of three runs of a third of each, in ns an output.
func timePush(each time.Duration, proto, x []float64, push func(s *poly, x, out []float64) []float64) float64 {
	s := newPoly(proto, 2, 1)
	out := push(s, x, make([]float64, 0, 2*len(x)+8))
	best := math.Inf(1)
	for rep := 0; rep < 3; rep++ {
		n, t0 := 0, time.Now()
		for time.Since(t0) < each/3 {
			out = push(s, x, out[:0])
			n += len(out)
		}
		best = math.Min(best, float64(time.Since(t0).Nanoseconds())/float64(n))
	}
	return best
}
