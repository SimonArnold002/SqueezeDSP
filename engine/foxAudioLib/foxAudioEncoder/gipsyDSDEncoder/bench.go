// Package: github.com/Foxenfurter/foxAudioLib/foxAudioEncoder/gipsyDSDEncoder
// filename bench.go
//
// Bench: the encoder's own work timed on this machine for the engine's --bench (the plugin's /squeezedsp/bench, Simon
// 2026-11-06): the modulator pair's loop (the busiest stage live: ledger THE ENCODER'S LOOP IS A RECURSION WITH A
// BRANCH) on a PCM source's standard design at DSD128 up, and the interpolator 16x (705.6k to DSD256), as they run.
package gipsyDSDEncoder

import (
	"fmt"
	"io"
	"math"
	"time"
)

func Bench(w io.Writer, each time.Duration) {
	const n = 1 << 16
	u, v := make([]float64, n), make([]float64, n)
	for i := range u {
		u[i] = 0.3*math.Sin(float64(i)*0.00223) + 0.01*math.Sin(float64(i)*1.3)
		v[i] = -0.25*math.Sin(float64(i)*0.00311) + 0.01*math.Sin(float64(i)*0.7)
	}
	ba, bb := make([]byte, n), make([]byte, n)
	ma, mb := newModulator(designsPCM["standard"][1]), newModulator(designsPCM["standard"][1])
	pair := best(each, func() int { runPair(ma, mb, u, v, ba, bb); return n })
	fmt.Fprintf(w, "encoder loop, the pair: %.2f ns a sample (both channels), %.0f ms a second of DSD256\n", pair, pair*11289600/1e6)
	it := newInterpolator(16)
	x := make([]float64, 1024)
	for i := range x {
		x[i] = 0.3 * math.Sin(float64(i)*0.031)
	}
	out := make([]float64, 0, 16*1024)
	frame := best(each, func() int { out = it.push(x, out[:0]); return len(x) })
	fmt.Fprintf(w, "encoder interpolator 16x: %.1f ns a frame (a channel), %.0f ms a second of 705.6k\n", frame, frame*705600/1e6)
}

// best: the best of three runs of a third of each, in ns a unit (f does some work and says how many units).
func best(each time.Duration, f func() int) float64 {
	b := math.Inf(1)
	for rep := 0; rep < 3; rep++ {
		units, t0 := 0, time.Now()
		for time.Since(t0) < each/3 {
			units += f()
		}
		b = math.Min(b, float64(time.Since(t0).Nanoseconds())/float64(units))
	}
	return b
}
