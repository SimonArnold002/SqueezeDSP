// Package: github.com/Foxenfurter/foxAudioLib/foxPEQ
// filename foxPEQ.go
// Package allows filters to be added and co-efficients generated
// The coefficients can be combined by cascading them
// Finally an impulse can be generated that is then used for convolution
package foxPEQ

import (
	"fmt"
	"math"
	"strings"
)

const packageName = "foxPEQ"

type PEQFilter struct {
	SampleRate         int
	FilterLength       int
	FilterCoefficients CoefficientsSlice
	Impulse            []float64 // Impulse response
	DebugFunc          func(string)
	WarningFunc        func(string)
}

// creates new PEQ Filter Object
// Optimal size for filter is sampleRate / lowest frequency.
// expects parameters sampleRate int, lowestFrequency int and returns a PEQFilter
func NewPEQFilter(sampleRate int, lowestFrequency int) PEQFilter {

	if lowestFrequency < 15 {
		lowestFrequency = 15
	}

	myPEQFilter := PEQFilter{
		SampleRate: sampleRate,
		// change to 3*sampleRate to give more accurate results for low frequencies, but will increase processing time and memory usage
		FilterLength: int(float64(3*sampleRate) / float64(lowestFrequency)),
	}
	return myPEQFilter
}

type FilterProfile []FreqGain

func NewFilterProfiles(sampleRate int) FilterProfile {
	return make(FilterProfile, 0)
}

func (fp FilterProfile) Copy() FilterProfile {
	copiedProfile := make(FilterProfile, len(fp))
	copy(copiedProfile, fp)
	return copiedProfile
}

type FreqGain struct {
	Freq float64
	Gain float64
}

func NewFreqGain(freq, gain float64) FreqGain {
	return FreqGain{Freq: freq, Gain: gain}
}

// coefficients a0,a1,a2,b0,b1,b2
type Coefficients struct {
	A [3]float64
	B [3]float64
}

// CoefficientsSlice represents a slice of Coefficients.
type CoefficientsSlice []Coefficients

// Add method adds a new Coefficients element to the slice.
func (c *CoefficientsSlice) Add(coeff Coefficients) {
	*c = append(*c, coeff)
}

// logTarget80 is the logarithm of the target amplitude corresponding to -80 dB, used for filter length calculation.
const logTarget80 = -9.210340371976184 // math.Log(10.0^(-80/20))

func (PEQ *PEQFilter) UpdateFilterLength() {
	if len(PEQ.FilterCoefficients) == 0 {
		return
	}

	maxN := 0
	for _, coeff := range PEQ.FilterCoefficients {
		a2 := coeff.A[2]
		if a2 <= 0 || a2 >= 1 {
			continue
		}
		r := math.Sqrt(a2)
		// n = ceil(logTarget80 / log(r))
		n := int(math.Ceil(logTarget80 / math.Log(r)))
		if n > maxN {
			maxN = n
		}
	}

	if maxN == 0 {
		// Fallback to heuristic: 32 periods of lowest frequency (assume 20 Hz)
		maxN = int(32.0 * float64(PEQ.SampleRate) / 20.0)
	}

	margin := int(float64(maxN) * 0.1)
	if margin < 1024 {
		margin = 1024
	}
	newLen := maxN + margin

	// Cap at 5 seconds if desired
	if newLen > 5*PEQ.SampleRate {
		newLen = 5 * PEQ.SampleRate
	}

	PEQ.FilterLength = newLen
}

func IIRFilter(input []float64, b [3]float64, a [3]float64, output []float64) {
	N := len(input)
	M := len(b)
	//L := len(a)
	y := make([]float64, N)
	w := make([]float64, M)

	for n := range N {
		y[n] = b[0]*input[n] + w[0]
		for i := 1; i < M; i++ {
			w[i-1] = b[i]*input[n] + w[i] - a[i]*y[n]
		}
		output[n] = y[n]
	}
}

// Take the filter coefficients and generate an impulse from them.
func (PEQ *PEQFilter) GenerateFilterImpulse() {

	maxLength := len(PEQ.FilterCoefficients)
	if maxLength == 0 {
		fmt.Printf("No coefficients\n")
		return
	}
	var a, b [3]float64
	signal := make([]float64, PEQ.FilterLength)
	impulse := make([]float64, PEQ.FilterLength)

	impulse[0] = 1

	for i := range maxLength {
		a = PEQ.FilterCoefficients[i].A
		b = PEQ.FilterCoefficients[i].B

		IIRFilter(impulse, b, a, signal)

		impulse = make([]float64, len(signal))
		copy(impulse, signal)
	}
	PEQ.Impulse = signal

}

// Take a set of filter coefficients and generate a FIR filter Impulse Response from them
func GenerateImpulseResponse(coeff Coefficients) []float64 {
	//testing shows that 4000 works for 48000 sampling rate and below, but longer filter needed
	// for higher sampling rate
	FilterLength := 8192
	// Create an array of zeros of length 'FilterLength'
	impulse := make([]float64, FilterLength)

	// Set the first element of the impulse array to 1
	impulse[0] = 1

	// Create a signal array of length 'FilterLength'
	signal := make([]float64, FilterLength)

	// Apply the filter coefficients to the impulse array using the IIRFilter function
	IIRFilter(impulse, coeff.B, coeff.A, signal)

	return signal
}

// Calculate the biquad filter coefficients and add to PEQFilter coefficientsSlice
func (PEQ *PEQFilter) CalcBiquadFilter(filterType string, frequency, peakGain, width float64, slopeType string) error {
	const errorPrefix = packageName + ":" + "CalcBiquadFilter "
	// Nyquist frequency approximation
	Nyquist := 0.445
	filterType = strings.ToLower(filterType)
	//slopeType = strings.ToLower(slopeType)
	if frequency < 10 || frequency > 25000 {

		return fmt.Errorf(errorPrefix+" %s frequency should be between 10 and 25000, got: %v ", filterType, frequency)
	}
	if PEQ.SampleRate < 10000 || PEQ.SampleRate > 800000 {
		return fmt.Errorf(errorPrefix+" %s sampleRate should be a recognized sample rate, got: %d ", filterType, PEQ.SampleRate)
	}
	if peakGain < -30 || peakGain > 20 {
		return fmt.Errorf(errorPrefix+" %s peakGain should be between -30 and +20, got: %f ", filterType, peakGain)
	}

	// Adjust frequency if it exceeds Nyquist frequency
	if float64(frequency)/float64(PEQ.SampleRate) > Nyquist {
		// Need to add in Error Logger here.
		frequency = (float64(PEQ.SampleRate) * Nyquist)
	}

	b0, b1, b2, a0, a1, a2, norm := 1.0, 0.0, 0.0, 1.0, 0.0, 0.0, 0.0

	ampl := math.Pow(10, math.Abs(peakGain)/40)
	if peakGain < 0 {
		ampl = 1 / ampl
	}

	SQRTA := math.Sqrt(ampl)
	var alpha, beta float64

	if filterType == "lowpass" || filterType == "highcut" {
		// Correct for the low pass filter rolling off too early
		desiredLevel := -1.0
		x := math.Pow(10, desiredLevel/20.0)
		desiredStartingPoint := float64(frequency) + float64(frequency) - (float64(frequency) * x)

		frequency = (desiredStartingPoint)
		filterType = "lowpass"
	}

	if filterType == "highpass" || filterType == "lowcut" {
		filterType = "highpass"
		desiredLevel := 2.0
		desiredStartingPoint := float64(frequency) * math.Sqrt(math.Pow(10, desiredLevel/10.0)-1)
		frequency = (desiredStartingPoint)
	}

	omega := 2 * math.Pi * float64(frequency) / float64(PEQ.SampleRate)
	coso := math.Cos(omega)
	sino := math.Sin(omega)

	switch slopeType {
	case "slope":
		alpha = sino / 2 * math.Sqrt((ampl+1/ampl)*(1/width-1)+2)
	case "Q":
		alpha = sino / (2 * width)
	case "octave":
		alpha = sino * math.Sinh(math.Log(2)/2*width*omega/sino)
	default:
		return fmt.Errorf(errorPrefix+" slopeType %s was not recognized [slope, Q, octave]", slopeType)
	}

	switch filterType {

	case "lowpass":
		norm = 1 / (1.0 + alpha)

		b0 = norm * ((1.0 - coso) / 2.0)
		b1 = norm * (1.0 - coso)
		b2 = norm * ((1.0 - coso) / 2.0)

		a0 = 1.0
		a1 = norm * (-2.0 * coso)
		a2 = norm * (1.0 - alpha)

	case "highpass":
		norm = 1 / (1.0 + alpha)

		b0 = norm * ((1.0 + coso) / 2.0)
		b1 = norm * (-(1.0 + coso))
		b2 = b0

		a0 = 1.0
		a1 = norm * (-2.0 * coso)
		a2 = norm * (1.0 - alpha)

	case "bandpass":
		norm = 1 / (1.0 + alpha)

		b0 = norm * (alpha)
		b1 = 0.0
		b2 = norm * (-alpha)

		a0 = 1.0
		a1 = norm * (-2.0 * coso)
		a2 = norm * (1.0 - alpha)

	case "notch":
		norm = 1 / (1.0 + alpha)

		b0 = norm
		b1 = norm * -2.0 * coso
		b2 = b0

		a0 = 1.0
		a1 = norm * -2.0 * coso
		a2 = norm * (1.0 - alpha)

	case "peak":
		norm = 1 / (1 + alpha/ampl)

		b0 = norm * (1 + alpha*ampl)
		b1 = norm * (-2 * coso)
		b2 = norm * (1 - alpha*ampl)

		a0 = 1
		a1 = norm * (-2 * coso)
		a2 = norm * (1 - alpha/ampl)

	case "lowshelf":
		beta = 2.0 * SQRTA * alpha
		norm = 1 / (ampl + 1 + (ampl-1)*coso + 2*SQRTA*alpha)

		b0 = norm * (ampl * (ampl + 1 - (ampl-1)*coso + beta))
		b1 = norm * (2 * ampl * (ampl - 1 - (ampl+1)*coso))
		b2 = norm * (ampl * (ampl + 1 - (ampl-1)*coso - beta))

		a0 = 1
		a1 = norm * (-2 * (ampl - 1 + (ampl+1)*coso))
		a2 = norm * (ampl + 1 + (ampl-1)*coso - beta)

	case "highshelf":
		norm = 1 / ((ampl + 1) - (ampl-1)*coso + 2*SQRTA*alpha)
		b0 = norm * (ampl * ((ampl + 1) + (ampl-1)*coso + 2*SQRTA*alpha))
		b1 = norm * (-2 * ampl * ((ampl - 1) + (ampl+1)*coso))
		b2 = norm * (ampl * ((ampl + 1) + (ampl-1)*coso - 2*SQRTA*alpha))
		a0 = 1.0
		a1 = norm * (2 * ((ampl - 1) - (ampl+1)*coso))
		a2 = norm * ((ampl + 1) - (ampl-1)*coso - 2*SQRTA*alpha)

	default:
		return fmt.Errorf(errorPrefix+" filterType %s was not set to a recognized filter type ", filterType)
	}

	myCoefficients := Coefficients{
		A: [3]float64{a0, a1, a2},
		B: [3]float64{b0, b1, b2},
	}
	PEQ.FilterCoefficients.Add(myCoefficients)
	return nil
}

// EOF ex[ero,emta;]

// Usage:
/*
myPEQFilter := foxPEQ.PEQFilter
foxPEQ.SampleRate = mySampleRate
myLoudness := foxPEQ.NewLoudness()
myLoudness.testPhon = 72; // this value should be set by user input
if math.IsNaN(myLoudness.testPhon ) || myLoudness.testPhon  == 0 {
    myLoudness.testPhon  = 70
}
//get the filter profile
fpl = myLoudness.DifferentialSPL( 1.0);

myLoudnessFilter := myPEQFilter.GenerateEQLoudnessFilter(fpl);
*/

// Loudness , calculates an Equal Loudness filter
type Loudness struct {
	f             []float64
	af            []float64
	Lu            []float64
	Tf            []float64
	ReferencePhon float64
	PlaybackPhon  float64
}

// Initiate A new loudness profile
func NewLoudness() *Loudness {
	official := false
	if official {
		return &Loudness{

			/* Official Loudness curve - using my Differential Phon gives wildy exaggerated results */
			f:             []float64{20, 25, 31.5, 40.0, 50.0, 63.0, 80.0, 100.0, 125.0, 160.0, 200.0, 250.0, 315.0, 400.0, 500.0, 630.0, 800.0, 1000, 1250, 1600, 2000, 2500, 3150, 4000, 5000, 6300, 8000, 10000, 12500},
			af:            []float64{0.532, 0.506, 0.480, 0.455, 0.432, 0.409, 0.387, 0.367, 0.349, 0.330, 0.315, 0.301, 0.288, 0.276, 0.267, 0.259, 0.253, 0.250, 0.246, 0.244, 0.243, 0.243, 0.243, 0.242, 0.242, 0.245, 0.254, 0.271, 0.301},
			Lu:            []float64{-31.6, -27.2, -23.0, -19.1, -15.9, -13.0, -10.3, -8.1, -6.2, -4.5, -3.1, -2.0, -1.1, -0.4, 0.0, 0.3, 0.5, 0.0, -2.7, -4.1, -1.0, 1.7, 2.5, 1.2, -2.1, -7.1, -11.2, -10.7, -3.1},
			Tf:            []float64{78.5, 68.7, 59.5, 51.1, 44.0, 37.5, 31.5, 26.5, 22.1, 17.9, 14.4, 11.4, 8.6, 6.2, 4.4, 3.0, 2.2, 2.4, 3.5, 1.7, -1.3, -4.2, -6.0, -5.4, -1.5, 6.0, 12.6, 13.9, 12.3},
			ReferencePhon: 82.5,
		}
	} else {
		return &Loudness{
			/* approximation for Loudness curve that gives good results for simplified automated calculation*/
			f:             []float64{40, 70, 1000, 1200, 2400, 6300, 12500},
			af:            []float64{0.455, 0.309, 0.25, 0.246, 0.243, 0.245, 0.301},
			Lu:            []float64{-19.1, -13.0, 0, -2.7, 1.7, -7.1, -3.1},
			Tf:            []float64{51.1, 31.5, 2.4, 3.5, -4.2, 6, 12.3},
			ReferencePhon: 82.5,
		}
	}
}

// Returns filter coefficients from a loudness filter profile
func (PEQ *PEQFilter) GenerateEQLoudnessFilter(myFilter FilterProfile) error {
	//const errorPrefix = packageName + ":" + "GenerateEQLoudnessFilter "
	mySize := len(myFilter)
	lastfilter := mySize - 1

	for i := 0; i < mySize; i++ {
		var filterType string
		var Q float64
		if i >= 1 && i != lastfilter {
			filterType = "peak"
			Q = 0.41
		} else {
			if i == 0 {
				filterType = "lowshelf"
			} else {
				filterType = "highshelf"
			}
			Q = 0.51
		}
		// calculate the Loudness coefficients for the generated filterprofile, will automatically
		// be stored in the PEQ coefficientsSlice
		err := PEQ.CalcBiquadFilter(filterType, myFilter[i].Freq, myFilter[i].Gain, Q, "Q")
		if err != nil {
			return err
		}

	}
	return nil
}

// / Create a list of dB SPL equal-loudness values for a given 'phon' loudness
// / (from zero, threshold, to 90)
// / </summary>
// / <param name="phon"></param>
// / <returns>list of {frequency Hz, dB SPL}</returns>
func (l *Loudness) spl(phon float64) (FilterProfile, error) {
	const errorPrefix = packageName + ":" + "spl: "
	if phon < 0 || phon > 120 {
		return nil, fmt.Errorf(errorPrefix+"Phon value out of bounds!, got: %v", phon)

	}
	// Setup user-defined values for equation
	lfg := make(FilterProfile, len(l.f))
	Ln := phon

	for j := 0; j < len(l.f); j++ {
		// Deriving sound pressure level from loudness level (iso226 sect 4.1)
		// see function here       https://uk.mathworks.com/matlabcentral/fileexchange/7028-iso-226-equal-loudness-level-contour-signal
		Af := 4.47e-3*(math.Pow(10.0, 0.025*Ln)-1.15) + math.Pow(0.4*math.Pow(10.0, ((l.Tf[j]+l.Lu[j])/10.0)-9.0), l.af[j])
		Lp := (10.0/l.af[j])*math.Log10(Af) - l.Lu[j] + 94.0

		fg := NewFreqGain(l.f[j], Lp)
		lfg[j] = fg
	}
	return lfg, nil
}

// take the reference phon at 1000 hz and diff this against the playback phon (measured sound pressure at listening position)
// we will then add this difference to all the test phon values
// and calculate the new SPL values by subtracting the reference phon from the playback phon
func (l *Loudness) DifferentialSPL(scale float64) (FilterProfile, error) {
	//const errorPrefix = packageName + ":" + "spl: "
	spl0, err := l.spl(l.ReferencePhon)
	if err != nil {
		return nil, err
	}
	spl1, err := l.spl(l.PlaybackPhon)
	if err != nil {
		return nil, err
	}

	var refLevel float64
	for j := 0; j < len(spl0); j++ {
		if spl0[j].Freq == 1000 && spl1[j].Freq == 1000 {
			refLevel = spl0[j].Gain - spl1[j].Gain
		}
	}

	spl := make(FilterProfile, len(spl1))
	for j := 0; j < len(spl1); j++ {
		fg := spl1[j]
		if refLevel != 0 {
			fg.Gain = (fg.Gain + refLevel) - spl0[j].Gain
		} else {
			fg.Gain = (scale * (spl0[j].Gain - fg.Gain))
		}
		spl[j] = fg
	}
	return spl, nil
}
