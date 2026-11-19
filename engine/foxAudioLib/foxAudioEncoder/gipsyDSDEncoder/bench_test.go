package gipsyDSDEncoder

import (
	"math"
	"testing"
)

// per DSD64 second of one channel: the interpolator (x8 from 352.8k), the loop, the packing
func BenchmarkInterpolatorX8(b *testing.B) {
	x := make([]float64, 352800)
	for i := range x {
		x[i] = 0.5 * math.Sin(float64(i)*0.0178)
	}
	out := make([]float64, 0, 1024*8)
	for b.Loop() {
		it := newInterpolator(8)
		for i := 0; i < len(x); i += 1024 {
			out = it.push(x[i:min(i+1024, len(x))], out[:0])
		}
	}
}

func BenchmarkLoopDSD64(b *testing.B) {
	u := make([]float64, 2822400)
	for i := range u {
		u[i] = 0.5 * math.Sin(float64(i)*0.00223)
	}
	bits := make([]byte, 8192)
	for b.Loop() {
		m := newModulator(designs["standard"][0])
		for i := 0; i < len(u); i += 8192 {
			m.run(u[i:min(i+8192, len(u))], bits)
		}
	}
}

func BenchmarkPackDSD64(b *testing.B) {
	bits := make([]byte, 2822400)
	for i := range bits {
		bits[i] = byte(i*7919>>3) & 1
	}
	for b.Loop() {
		e := &Encoder{count: int64(len(bits))}
		ch := &channel{block: make([]byte, blockSize)}
		e.pack(ch, bits)
	}
}

func BenchmarkLoopPairDSD64(b *testing.B) {
	u := make([]float64, 2822400)
	v := make([]float64, 2822400)
	for i := range u {
		u[i] = 0.5 * math.Sin(float64(i)*0.00223)
		v[i] = -0.3 * math.Sin(float64(i)*0.00223)
	}
	ba := make([]byte, 8192)
	bb := make([]byte, 8192)
	for b.Loop() {
		ma := newModulator(designs["standard"][0])
		mb := newModulator(designs["standard"][0])
		for i := 0; i < len(u); i += 8192 {
			n := min(i+8192, len(u))
			runPair(ma, mb, u[i:n], v[i:n], ba, bb)
		}
	}
}
