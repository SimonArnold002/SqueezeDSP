// foxFFTHeartofRust/fft.go
package foxFFTHeartofRust

/*
#cgo windows LDFLAGS: -L${SRCDIR}/libs/windows_amd64 -lrustfft -lws2_32 -luserenv -lbcrypt -lntdll
#cgo linux,amd64 LDFLAGS: -L${SRCDIR}/libs/linux_amd64 -lrustfft -lm -ldl
#cgo darwin,amd64 LDFLAGS: -L${SRCDIR}/libs/darwin_amd64 -lrustfft
#cgo linux,arm64 LDFLAGS: -L${SRCDIR}/libs/linux_arm64 -lrustfft -lm -ldl
#cgo darwin,arm64 LDFLAGS: -L${SRCDIR}/libs/darwin_arm64 -lrustfft

#include <stdlib.h>

// Using the ORIGINAL function names that work
extern void fft_forward(const double* input_real, const double* input_imag, double* output_real, double* output_imag, size_t n);
extern void fft_inverse(const double* input_real, const double* input_imag, double* output_real, double* output_imag, size_t n);
extern void fft_verify_implementation();
*/
import "C"
import (
	"unsafe"
)

// Fft performs forward or inverse FFT on complex data
// If inverse is false, performs forward FFT
// If inverse is true, performs inverse FFT
var isFirstTime = true

func Fft(data []complex128, inverse bool) []complex128 {
	if len(data) == 0 {
		return data
	}

	//result := make([]complex128, len(data))
	//copy(result, data)

	if inverse {
		fftInverse(data)
	} else {
		fftForward(data)
	}

	return data
}

// fftForward performs a forward FFT using the original function signature
func fftForward(data []complex128) {
	n := len(data)
	if n == 0 {
		return
	}

	inputReal := make([]float64, n)
	inputImag := make([]float64, n)
	for i, c := range data {
		inputReal[i] = real(c)
		inputImag[i] = imag(c)
	}

	outputReal := make([]float64, n)
	outputImag := make([]float64, n)

	C.fft_forward(
		(*C.double)(unsafe.Pointer(&inputReal[0])),
		(*C.double)(unsafe.Pointer(&inputImag[0])),
		(*C.double)(unsafe.Pointer(&outputReal[0])),
		(*C.double)(unsafe.Pointer(&outputImag[0])),
		C.size_t(n),
	)

	for i := 0; i < n; i++ {
		data[i] = complex(outputReal[i], outputImag[i])
	}
}

// fftInverse performs an inverse FFT using the original function signature
func fftInverse(data []complex128) {
	n := len(data)
	if n == 0 {
		return
	}

	inputReal := make([]float64, n)
	inputImag := make([]float64, n)
	for i, c := range data {
		inputReal[i] = real(c)
		inputImag[i] = imag(c)
	}

	outputReal := make([]float64, n)
	outputImag := make([]float64, n)

	C.fft_inverse(
		(*C.double)(unsafe.Pointer(&inputReal[0])),
		(*C.double)(unsafe.Pointer(&inputImag[0])),
		(*C.double)(unsafe.Pointer(&outputReal[0])),
		(*C.double)(unsafe.Pointer(&outputImag[0])),
		C.size_t(n),
	)

	for i := 0; i < n; i++ {
		data[i] = complex(outputReal[i], outputImag[i])
	}
}

// VerifyImplementation calls the Rust verification function
func VerifyImplementation() {
	C.fft_verify_implementation()
}
