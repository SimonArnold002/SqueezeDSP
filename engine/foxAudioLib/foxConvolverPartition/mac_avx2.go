//go:build goexperiment.simd && amd64

// Package: github.com/Foxenfurter/foxAudioLib/foxConvolverPartition
// filename mac_avx2.go
//
// (SqueezeDSP fork) processChunk's multiply-add in AVX2 (Go's simd/archsimd, GOEXPERIMENT=simd, as engine/build.sh
// builds): two bins a Float64x4, [re0 im0 re1 im1]. Each bin is multiplyAddScalar's to the bit: re = ar*hr - ai*hi
// by VADDSUBPD, im = ai*hr + ar*hi (Go's ar*hi + ai*hr in the other order: an IEEE sum does not depend on it), then
// res + that, every step rounded as Go rounds it on amd64, where it never fuses (TestMultiplyAddAVX2Bits,
// TestChunkBitsUnchanged). The loop walks the bins by pointer and clears the registers' upper halves before the SSE2
// code after it (ledger GO DOES NOT CLEAR THE AVX UPPER HALVES).
package foxConvolverPartition

import (
	"simd/archsimd"
	"unsafe"
)

// haveAVX2: the CPU has AVX2 (plex's Tiger Lake; Rosetta too, so the Mac's amd64 tests run this path).
var haveAVX2 = archsimd.X86.AVX2()

func multiplyAddAVX2(res, audioFFT, impulseFFT []complex128) {
	n := len(res)
	audioFFT, impulseFFT = audioFFT[:n], impulseFFT[:n]
	m := n &^ 1 // the bins the vectors take, two at a time
	if m > 0 {
		r, a, h := unsafe.Pointer(&res[0]), unsafe.Pointer(&audioFFT[0]), unsafe.Pointer(&impulseFFT[0])
		for off, end := uintptr(0), uintptr(m)*16; off < end; off += 32 {
			av, hv := at4(a, off), at4(h, off)
			hr := hv.ConcatPermuteScalarsGrouped(0, 2, hv) // [hr0 hr0 hr1 hr1]
			hi := hv.ConcatPermuteScalarsGrouped(1, 3, hv) // [hi0 hi0 hi1 hi1]
			as := av.ConcatPermuteScalarsGrouped(1, 2, av) // [ai0 ar0 ai1 ar1]
			at4(r, off).Add(av.Mul(hr).AddOddSubEven(as.Mul(hi))).StoreArray((*[4]float64)(unsafe.Add(r, off)))
		}
		archsimd.ClearAVXUpperBits()
	}
	for k := m; k < n; k++ {
		res[k] += audioFFT[k] * impulseFFT[k]
	}
}

// at4: the four float64s off bytes on from p (two complex128 bins).
func at4(p unsafe.Pointer, off uintptr) archsimd.Float64x4 {
	return archsimd.LoadFloat64x4Array((*[4]float64)(unsafe.Add(p, off)))
}
