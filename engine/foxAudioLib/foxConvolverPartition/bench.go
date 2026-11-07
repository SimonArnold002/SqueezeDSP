// Package: github.com/Foxenfurter/foxAudioLib/foxConvolverPartition
// filename bench.go
//
// (SqueezeDSP fork) Bench: processChunk's multiply-add timed on this machine for the engine's --bench (the plugin's
// /squeezedsp/bench, Simon 2026-11-06), multiplyAddScalar against multiplyAdd (AVX2 where it runs), over a 1 s FIR at
// 705.6k's partitioning: 87 partitions of 8193 bins, 22.8 MB, past the caches as it is live.
package foxConvolverPartition

import (
	"fmt"
	"io"
	"math"
	"math/rand"
	"time"
)

func Bench(w io.Writer, each time.Duration) {
	const parts, n = 87, 8193
	r := rand.New(rand.NewSource(1))
	a, h := make([][]complex128, parts), make([][]complex128, parts)
	for p := range a {
		a[p], h[p] = make([]complex128, n), make([]complex128, n)
		for i := range a[p] {
			a[p][i], h[p][i] = complex(r.NormFloat64(), r.NormFloat64()), complex(r.NormFloat64(), r.NormFloat64())
		}
	}
	res := make([]complex128, n)
	timeMAC := func(mac func(res, a, h []complex128)) float64 {
		best := math.Inf(1)
		for rep := 0; rep < 3; rep++ {
			chunks, t0 := 0, time.Now()
			for time.Since(t0) < each/3 {
				clear(res)
				for p := 0; p < parts; p++ {
					mac(res, a[p], h[p])
				}
				chunks++
			}
			best = math.Min(best, float64(time.Since(t0).Nanoseconds())/float64(chunks*parts*n))
		}
		return best
	}
	scalar, fast := timeMAC(multiplyAddScalar), timeMAC(multiplyAdd)
	fmt.Fprintf(w, "FIR multiply-add, %d partitions x %d bins: %.2f ns a bin, AVX2 %t: %.2f (%.2fx)\n", parts, n, scalar, haveAVX2, fast, scalar/fast)
}
