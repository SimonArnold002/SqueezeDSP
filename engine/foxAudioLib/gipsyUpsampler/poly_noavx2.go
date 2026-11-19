//go:build !goexperiment.simd || !amd64

// Package: github.com/Foxenfurter/foxAudioLib/gipsyUpsampler
// filename poly_noavx2.go
//
// A build without AVX2 (not amd64, or no GOEXPERIMENT=simd): push is pushScalar. haveAVX2 is a constant here, so
// the compiler drops push's other branch.
package gipsyUpsampler

const haveAVX2 = false

func (s *poly) pushAVX2(x []float64, out []float64) []float64 { return s.pushScalar(x, out) }
