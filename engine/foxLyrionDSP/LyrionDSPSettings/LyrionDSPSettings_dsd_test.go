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

// The DSF output's flags: the DSD rates, the three modulators, the gain's range; DSF needs a rate, and the DSD out
// flags are refused (not ignored) without DSF.
func TestDSFArgs(t *testing.T) {
	saved := os.Args
	defer func() { os.Args = saved }()
	read := func(a ...string) (*Arguments, error) {
		os.Args = append([]string{"SqueezeDSP", "--clientid=bench"}, a...)
		return ReadArgs()
	}
	dsd := []string{"--formatin=DSD", "--input", "/x.dsf", "--samplerate=352800", "--formatout=DSF"}
	for _, tc := range []struct {
		args []string
		want string // "" = accepted
	}{
		{append(dsd, "--dsdrate=2822400"), ""},
		{append(dsd, "--dsdrate=22579200"), ""},
		{append(dsd, "--dsdrate=3072000"), "--dsdrate"},
		{append(dsd, "--dsdrate=45158400"), "--dsdrate"},
		{dsd, ""}, // the rate from the player's settings, else the source's
		{append(dsd, "--dsdrate=source"), ""},
		{append(dsd, "--dsdrate=Source", "--dsdmax=11289600"), ""},
		{append(dsd, "--dsdmax=3072000"), "--dsdmax"},
		{[]string{"--formatin=DSD", "--input", "/x.dsf", "--samplerate=352800", "--dsdmax=2822400"}, "need --formatout=DSF"},
		{[]string{"--formatin=DSD", "--input", "/x.dsf", "--samplerate=352800", "--dsdrate=source"}, "need --formatout=DSF"},
		{append(dsd, "--dsdrate=2822400", "--dsdmod=standard"), ""},
		{append(dsd, "--dsdrate=2822400", "--dsdmod=CLANS"), ""},
		{append(dsd, "--dsdrate=2822400", "--dsdmod=adaptive"), ""},
		{append(dsd, "--dsdrate=2822400", "--dsdmod=sdm"), "--dsdmod"},
		{append(dsd, "--dsdrate=2822400", "--dsdoutgain=-24"), ""},
		{append(dsd, "--dsdrate=2822400", "--dsdoutgain=6.5"), "--dsdoutgain"},
		{[]string{"--formatin=DSD", "--input", "/x.dsf", "--samplerate=352800", "--formatout=WAV", "--dsdrate=2822400"}, "need --formatout=DSF"},
		{[]string{"--formatin=DSD", "--input", "/x.dsf", "--samplerate=352800", "--dsdmod=clans"}, "need --formatout=DSF"},
	} {
		a, err := read(tc.args...)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%v: refused: %v", tc.args, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%v: got %v, want an error with %q", tc.args, err, tc.want)
		case tc.want == "" && strings.Contains(strings.Join(tc.args, " "), "--dsdmod=CLANS") && a.DSDMod != "clans":
			t.Errorf("%v: DSDMod %q, want clans", tc.args, a.DSDMod)
		case tc.want == "" && strings.Contains(strings.Join(tc.args, " "), "--dsdrate=Source") && (!a.DSDRateSource || a.DSDRate != 0 || a.DSDMax != 11289600):
			t.Errorf("%v: source %v rate %d max %d", tc.args, a.DSDRateSource, a.DSDRate, a.DSDMax)
		}
	}
}

// The player's DSF output settings (DSDOutRate, DSDOutMax, DSDOutMod), stored by setval as a number or its string:
// a valid value is kept, anything else reads as not set (the engine then takes the source's rate, no max, standard).
func TestDSFClientConfig(t *testing.T) {
	for _, tc := range []struct {
		json string
		rate string
		max  int
		mod  string
	}{
		{`{"Client":{"DSDOutRate":"source","DSDOutMax":11289600,"DSDOutMod":"adaptive"}}`, "source", 11289600, "adaptive"},
		{`{"Client":{"DSDOutRate":5644800,"DSDOutMax":"22579200","DSDOutMod":"CLANS"}}`, "5644800", 22579200, "clans"},
		{`{"Client":{"DSDOutRate":"2822400"}}`, "2822400", 0, ""},
		{`{"Client":{"DSDOutRate":3072000,"DSDOutMax":44100,"DSDOutMod":"sdm"}}`, "", 0, ""},
		{`{"Client":{"DSDOutRate":"fast","DSDOutMax":"x"}}`, "", 0, ""},
		{`{"Client":{}}`, "", 0, ""},
	} {
		c, err := buildConfig([]byte(tc.json))
		if err != nil {
			t.Fatalf("%s: %v", tc.json, err)
		}
		if c.DSDOutRate != tc.rate || c.DSDOutMax != tc.max || c.DSDOutMod != tc.mod {
			t.Errorf("%s: rate %q max %d mod %q, want %q %d %q", tc.json, c.DSDOutRate, c.DSDOutMax, c.DSDOutMod, tc.rate, tc.max, tc.mod)
		}
	}
}

// The converter's flags (0.1.72.33): --upsample (a PCM input's rate before the DSP), --upfilter (its two filters,
// CD rates first; one value: both) and --dsdsource (which modulator designs). Refused, never ignored.
func TestUpsampleArgs(t *testing.T) {
	saved := os.Args
	defer func() { os.Args = saved }()
	read := func(a ...string) (*Arguments, error) {
		os.Args = append([]string{"SqueezeDSP", "--clientid=bench"}, a...)
		return ReadArgs()
	}
	pcm := []string{"--formatin=PCM", "--bitsin=24", "--samplerate=44100", "--channels=2", "--formatout=DSF"}
	for _, tc := range []struct {
		args       []string
		want       string // "" = accepted
		rate       int
		cd, hi, ds string
	}{
		{pcm, "", 0, "short-int", "short-lin", ""},
		{append(pcm, "--upsample=705600"), "", 705600, "short-int", "short-lin", ""},
		{append(pcm, "--upsample=705600", "--upfilter=long-min,short-int"), "", 705600, "long-min", "short-int", ""},
		{append(pcm, "--upsample=705600", "--upfilter=LONG-LIN"), "", 705600, "long-lin", "long-lin", ""},
		{append(pcm, "--dsdsource=pcm"), "", 0, "short-int", "short-lin", "pcm"},
		{append(pcm, "--dsdsource=DSD"), "", 0, "short-int", "short-lin", "dsd"},
		{append(pcm, "--upsample=44100"), "", 44100, "short-int", "short-lin", ""}, // a PCM target down to 44.1k
		{append(pcm, "--upsample=44099"), "--upsample", 0, "", "", ""},
		// a PCM target, WAV out, below the source (down)
		{[]string{"--formatin=PCM", "--bitsin=24", "--samplerate=96000", "--channels=2", "--formatout=WAV", "--upsample=48000", "--upfilter=long-min,long-lin"}, "", 48000, "long-min", "long-lin", ""},
		{append(pcm, "--upsample=1536001"), "--upsample", 0, "", "", ""},
		{append(pcm, "--upsample=fast"), "--upsample", 0, "", "", ""},
		{append(pcm, "--upsample=705600", "--upfilter=medium-lin"), "--upfilter", 0, "", "", ""},
		{append(pcm, "--upsample=705600", "--upfilter=short-int,long"), "--upfilter", 0, "", "", ""},
		{append(pcm, "--upfilter=short-lin"), "--upfilter needs --upsample", 0, "", "", ""},
		{append(pcm, "--dsdsource=sacd"), "--dsdsource", 0, "", "", ""},
		{[]string{"--formatin=PCM", "--bitsin=24", "--samplerate=44100", "--channels=2", "--formatout=WAV", "--dsdsource=pcm"}, "need --formatout=DSF", 0, "", "", ""},
		{[]string{"--formatin=DSD", "--input", "/x.dsf", "--samplerate=352800", "--formatout=DSF", "--upsample=705600"}, "--upsample needs a PCM input", 0, "", "", ""},
	} {
		a, err := read(tc.args...)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%v: refused: %v", tc.args, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%v: got %v, want an error with %q", tc.args, err, tc.want)
		case tc.want == "" && (a.UpsampleRate != tc.rate || a.UpFilterCD.String() != tc.cd || a.UpFilterHiRes.String() != tc.hi || a.DSDSource != tc.ds):
			t.Errorf("%v: upsample %d, filters %s and %s, source %q; want %d, %s and %s, %q", tc.args, a.UpsampleRate,
				a.UpFilterCD, a.UpFilterHiRes, a.DSDSource, tc.rate, tc.cd, tc.hi, tc.ds)
		}
	}
}

// The native round (2026-11-07): --samplerate=native (any case) decodes a DSD input at its own DSD rate, for DSF out
// only; anywhere else it is refused, not ignored.
func TestNativeArgs(t *testing.T) {
	saved := os.Args
	defer func() { os.Args = saved }()
	read := func(a ...string) (*Arguments, error) {
		os.Args = append([]string{"SqueezeDSP", "--clientid=bench"}, a...)
		return ReadArgs()
	}
	for _, tc := range []struct {
		args []string
		want string // "" = accepted
	}{
		{[]string{"--formatin=DSD", "--input", "/x.dsf", "--samplerate=native", "--formatout=DSF", "--dsdrate=source"}, ""},
		{[]string{"--formatin=DSD", "--input", "/x.dsf", "--samplerate=Native", "--formatout=DSF"}, ""},
		{[]string{"--formatin=DSD", "--input", "/x.dsf", "--samplerate=native", "--formatout=WAV"}, "--samplerate=native needs"},
		{[]string{"--formatin=PCM", "--bitsin=24", "--channels=2", "--samplerate=native", "--formatout=DSF"}, "--samplerate=native needs"},
		{[]string{"--formatin=DSD", "--input", "/x.dsf", "--samplerate=natives", "--formatout=DSF"}, "--samplerate"},
	} {
		a, err := read(tc.args...)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%v: refused: %v", tc.args, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%v: got %v, want an error with %q", tc.args, err, tc.want)
		case tc.want == "" && (!a.DSDNative || a.InputSampleRate != 0):
			t.Errorf("%v: native %v, rate %d; want native, no rate", tc.args, a.DSDNative, a.InputSampleRate)
		}
	}
	if a, err := read("--formatin=DSD", "--input", "/x.dsf", "--samplerate=352800", "--formatout=DSF"); err != nil || a.DSDNative {
		t.Errorf("--samplerate=352800: native %v (%v)", a != nil && a.DSDNative, err)
	}
}

// The player's DSDIntegrator: lin, int, min or nonring, any case and quoted; anything else is "" (lin).
func TestNativeClientConfig(t *testing.T) {
	for _, tc := range []struct{ json, want string }{
		{`{"Client":{"DSDIntegrator":"nonring"}}`, "nonring"},
		{`{"Client":{"DSDIntegrator":" MIN"}}`, "min"},
		{`{"Client":{"DSDIntegrator":"int"}}`, "int"},
		{`{"Client":{"DSDIntegrator":"lin"}}`, "lin"},
		{`{"Client":{"DSDIntegrator":"slow"}}`, ""},
		{`{"Client":{"DSDIntegrator":3}}`, ""},
		{`{"Client":{}}`, ""},
	} {
		c, err := buildConfig([]byte(tc.json))
		if err != nil {
			t.Fatalf("%s: %v", tc.json, err)
		}
		if c.DSDIntegrator != tc.want {
			t.Errorf("%s: %q, want %q", tc.json, c.DSDIntegrator, tc.want)
		}
	}
}
