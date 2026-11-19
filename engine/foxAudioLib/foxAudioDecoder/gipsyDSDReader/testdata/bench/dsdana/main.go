// Bench analyser for the DSD decoder comparison: raw stereo PCM in (f32le, or s24le with -s24), the left
// channel analysed. Kaiser beta 30 window over 2^k points after a settle time (as the ledger's earlier
// analyses: scratchpad prec/spec.c). Levels in dBFS where a full-scale sine reads 0.
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"math"
	"math/cmplx"
	"os"
)

func fft(a []complex128) {
	n := len(a)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			a[i], a[j] = a[j], a[i]
		}
	}
	for l := 2; l <= n; l <<= 1 {
		w := cmplx.Exp(complex(0, -2*math.Pi/float64(l)))
		for i := 0; i < n; i += l {
			wk := complex(1, 0)
			for k := 0; k < l/2; k++ {
				u, v := a[i+k], a[i+k+l/2]*wk
				a[i+k], a[i+k+l/2] = u+v, u-v
				wk *= w
			}
		}
	}
}

func bessel0(x float64) float64 {
	sum, term := 1.0, 1.0
	for k := 1; k < 200; k++ {
		term *= (x / (2 * float64(k))) * (x / (2 * float64(k)))
		sum += term
		if term < 1e-17*sum {
			break
		}
	}
	return sum
}

func main() {
	rate := flag.Float64("rate", 352800, "sample rate")
	s24 := flag.Bool("s24", false, "input is s24le")
	tone := flag.Float64("tone", 1000, "test tone, Hz (0: none)")
	settle := flag.Float64("settle", 0.5, "seconds skipped first")
	logn := flag.Int("log2n", 20, "FFT size, log2")
	label := flag.String("label", "", "row label")
	flag.Parse()

	raw, _ := io.ReadAll(os.Stdin)
	var x []float64
	if *s24 {
		for i := 0; i+6 <= len(raw); i += 6 {
			v := int32(uint32(raw[i])|uint32(raw[i+1])<<8|uint32(raw[i+2])<<16) << 8 >> 8
			x = append(x, float64(v)/8388608)
		}
	} else {
		for i := 0; i+8 <= len(raw); i += 8 {
			x = append(x, float64(math.Float32frombits(binary.LittleEndian.Uint32(raw[i:]))))
		}
	}
	peak := 0.0
	for _, v := range x {
		peak = math.Max(peak, math.Abs(v))
	}
	n := 1 << *logn
	s := int(*settle * *rate)
	if s+n > len(x) {
		fmt.Printf("%-34s too short: %d samples, need %d\n", *label, len(x), s+n)
		return
	}
	a := make([]complex128, n)
	beta := 30.0
	wsum := 0.0
	for i := 0; i < n; i++ {
		r := 2*float64(i)/float64(n-1) - 1
		w := bessel0(beta*math.Sqrt(math.Max(0, 1-r*r))) / bessel0(beta)
		wsum += w
		a[i] = complex(x[s+i]*w, 0)
	}
	fft(a)
	df := *rate / float64(n)
	// power per bin, scaled so a full-scale sine's main lobe sums to 0 dB (window's coherent gain)
	p := make([]float64, n/2)
	for i := range p {
		m := cmplx.Abs(a[i]) * 2 / wsum
		p[i] = m * m / 2 // mean-square contribution
	}
	enbw := 0.0 // equivalent noise bandwidth in bins, to turn noise power into RMS correctly
	{
		s1, s2 := 0.0, 0.0
		for i := 0; i < n; i++ {
			r := 2*float64(i)/float64(n-1) - 1
			w := bessel0(beta*math.Sqrt(math.Max(0, 1-r*r))) / bessel0(beta)
			s1 += w
			s2 += w * w
		}
		enbw = float64(n) * s2 / (s1 * s1)
	}
	lobe := int(math.Ceil(beta/math.Pi)) + 2 // main-lobe half width in bins
	tb := int(math.Round(*tone / df))
	band := func(lo, hi float64, skipTone bool) float64 {
		sum := 0.0
		for i := int(lo / df); i <= int(hi/df) && i < len(p); i++ {
			if skipTone && *tone > 0 && i >= tb-lobe && i <= tb+lobe {
				continue
			}
			if i < 2 {
				continue
			}
			sum += p[i]
		}
		return 10 * math.Log10(sum/enbw*2+1e-300) // RMS re a full-scale sine's RMS -> dBFS-sine
	}
	toneDB := math.Inf(-1)
	if *tone > 0 {
		sum := 0.0
		for i := tb - lobe; i <= tb+lobe; i++ {
			sum += p[i]
		}
		toneDB = 10 * math.Log10(sum/enbw*2)
	}
	ny := *rate / 2
	hi50 := math.Min(ny, 1e9)
	fmt.Printf("%-34s tone %8.3f  in-band noise %8.2f  20-50k %8.2f  50k-Nyq %8.2f  peak %7.2f dBFS\n",
		*label, toneDB, band(20, 20000, true), band(20000, math.Min(50000, ny), true), band(50000, hi50, true),
		20*math.Log10(peak+1e-300))
}
