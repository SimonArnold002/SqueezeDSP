//go:build !goexperiment.simd || !amd64

// Package: github.com/Foxenfurter/foxAudioLib/foxConvolverPartition
// filename mac_noavx2.go
//
// (SqueezeDSP fork) A build without AVX2 (not amd64, or no GOEXPERIMENT=simd): multiplyAdd is multiplyAddScalar.
// haveAVX2 is a constant here, so the compiler drops multiplyAdd's other branch.
package foxConvolverPartition

const haveAVX2 = false

func multiplyAddAVX2(res, audioFFT, impulseFFT []complex128) { multiplyAddScalar(res, audioFFT, impulseFFT) }
