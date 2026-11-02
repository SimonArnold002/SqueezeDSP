package LyrionDSPSettings

import (
	"os"
	"strings"
	"testing"
	"time"
)

// The DSD flags (SqueezeDSP fork): what ReadArgs accepts and refuses. Fox's other tests in this package
// read Windows paths, so run this one alone: go test -run TestDSDArgs ./LyrionDSPSettings/
func TestDSDArgs(t *testing.T) {
	saved := os.Args
	defer func() { os.Args = saved }()
	read := func(a ...string) (*Arguments, error) {
		os.Args = append([]string{"SqueezeDSP", "--clientid=bench"}, a...)
		return ReadArgs()
	}
	dsd := []string{"--formatin=DSD", "--input", "/x.dsf", "--samplerate=352800"}
	for _, tc := range []struct {
		args []string
		want string // "" = accepted
	}{
		{dsd, ""},
		{append(dsd, "--dsdcutoff=60000"), ""},
		{append(dsd, "--dsdcutoff=0"), ""},
		{append(dsd, "--dsdcutoff=60001"), "--dsdcutoff"},
		{append(dsd, "--dsdcutoff=20000"), "--dsdcutoff"},
		{append(dsd, "--dsdgain=6"), ""},
		{append(dsd, "--dsdgain=13"), "--dsdgain"},
		{append(dsd, "--until=1:02.50"), ""},
		{append(dsd, "--until=x"), "--until"},
		{[]string{"--formatin=PCM", "--bitsin=24", "--samplerate=96000", "--channels=2", "--until=1:02.50"}, "--until needs --formatin=DSD"},
		{[]string{"--formatin=DSD", "--samplerate=352800"}, "--input"},
		{[]string{"--formatin=DSD", "--input", "/x.dsf"}, "--samplerate"},
	} {
		a, err := read(tc.args...)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%v: refused: %v", tc.args, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%v: got %v, want an error with %q", tc.args, err, tc.want)
		case tc.want == "" && strings.Contains(strings.Join(tc.args, " "), "--until=1:02.50") && a.EndTime != 62500*time.Millisecond:
			t.Errorf("%v: EndTime %v, want 1m2.5s", tc.args, a.EndTime)
		}
	}
	if a, _ := read(dsd...); a == nil || a.DSDCutoff != 30000 || a.DSDGain != 0 {
		t.Errorf("defaults: %+v, want cutoff 30000, gain 0", a)
	}
}

// The player's DSDCutoff and DSDGain (its settings JSON, as the pages store them through setval: a number or
// its string). An out-of-range or unreadable value is nil, so the engine keeps its own; so is a missing one.
func TestDSDClientConfig(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	for _, tc := range []struct {
		json      string
		cut, gain *float64
	}{
		{`{"Client":{"DSDCutoff":24000,"DSDGain":6}}`, f(24000), f(6)},
		{`{"Client":{"DSDCutoff":"50000","DSDGain":"0"}}`, f(50000), f(0)},
		{`{"Client":{"DSDCutoff":0}}`, f(0), nil},
		{`{"Client":{"DSDCutoff":70000,"DSDGain":13}}`, nil, nil},
		{`{"Client":{"DSDCutoff":"x","DSDGain":"6dB"}}`, nil, nil},
		{`{"Client":{}}`, nil, nil},
	} {
		c, err := buildConfig([]byte(tc.json))
		if err != nil {
			t.Fatalf("%s: %v", tc.json, err)
		}
		same := func(a, b *float64) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }
		if !same(c.DSDCutoff, tc.cut) || !same(c.DSDGain, tc.gain) {
			t.Errorf("%s: cutoff %v gain %v, want %v %v", tc.json, c.DSDCutoff, c.DSDGain, tc.cut, tc.gain)
		}
	}
	// a command-line value is marked as given, so it wins over the player's
	saved := os.Args
	defer func() { os.Args = saved }()
	os.Args = []string{"SqueezeDSP", "--clientid=bench", "--formatin=DSD", "--input", "/x.dsf", "--samplerate=352800", "--dsdgain=6"}
	a, err := ReadArgs()
	if err != nil || !a.DSDGainSet || a.DSDCutoffSet {
		t.Errorf("--dsdgain given, --dsdcutoff not: GainSet %v CutoffSet %v (%v)", a != nil && a.DSDGainSet, a != nil && a.DSDCutoffSet, err)
	}
}
