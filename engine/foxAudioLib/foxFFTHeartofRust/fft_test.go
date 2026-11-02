package foxFFTHeartofRust

import (
	"runtime"
	"testing"
)

func TestEnvironment(t *testing.T) {
	t.Logf("Running tests on %s/%s", runtime.GOOS, runtime.GOARCH)
	t.Logf("Go version: %s", runtime.Version())

	// Verify we're actually running on the expected platform
	expectedOS := "linux" // Since we're testing in Docker
	if runtime.GOOS != expectedOS {
		t.Logf("NOTE: Running on %s, but testing Linux compatibility", runtime.GOOS)
	} else {
		t.Logf("✓ Running on correct platform: %s", runtime.GOOS)
	}
}

func TestFFT(t *testing.T) {
	t.Logf("Testing FFT on %s/%s", runtime.GOOS, runtime.GOARCH)

	input := []complex128{1 + 0i, 1 + 0i, 1 + 0i, 1 + 0i}

	// Test forward FFT
	forward := Fft(input, false)
	if len(forward) != len(input) {
		t.Errorf("Forward FFT changed length: got %d, want %d", len(forward), len(input))
	}

	// Test inverse FFT
	inverse := Fft(forward, true)
	if len(inverse) != len(forward) {
		t.Errorf("Inverse FFT changed length: got %d, want %d", len(inverse), len(forward))
	}

	// For a 4-point FFT of [1,1,1,1], the DC component should be 4
	if real(forward[0]) != 4.0 {
		t.Errorf("Expected DC component to be 4, got %f", real(forward[0]))
	}

	t.Logf("FFT test completed successfully on %s", runtime.GOOS)
}
