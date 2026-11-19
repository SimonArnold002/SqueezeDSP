package LyrionDSPSettings

import (
	"os"
	"testing"
)

// TestInSamplesArg (the exact-length round): --insamples=N the input's frames; --insamples=header a decoder's WAV header
// on the pipe vouched for by the plugin; anything else refused (an engine that cannot read the count must not run on).
func TestInSamplesArg(t *testing.T) {
	saved := os.Args
	defer func() { os.Args = saved }()
	for _, v := range []struct {
		arg    string
		n      int64
		header bool
		bad    bool
	}{
		{"--insamples=15235080", 15235080, false, false},
		{"--insamples=0", 0, false, false},
		{"--insamples=header", 0, true, false},
		{"--insamples=HEADER", 0, true, false}, // the parser lowercases values (Fox's)
		{"--insamples=-5", 0, false, true},
		{"--insamples=12.5", 0, false, true},
		{"--insamples=", 0, false, true},
	} {
		os.Args = []string{"SqueezeDSP", "--Clientid=cd-ef-e3-a9-15-31", v.arg}
		var a Arguments
		err := parseArgs(&a)
		if (err != nil) != v.bad {
			t.Errorf("%s: error %v, want an error %v", v.arg, err, v.bad)
			continue
		}
		if !v.bad && (a.InSamples != v.n || a.InHeader != v.header) {
			t.Errorf("%s: InSamples %d, InHeader %v; want %d, %v", v.arg, a.InSamples, a.InHeader, v.n, v.header)
		}
	}
}

// TestFlacSeekAndTailArgs (step 4): --flacseek=OFFSET a remote FLAC's byte LMS starts at, only with an http(s)
// --trackurl (the engine reads the count from it); --tail takes no value and does not swallow the next flag (values
// lowercased: Fox's parser).
func TestFlacSeekAndTailArgs(t *testing.T) {
	saved := os.Args
	defer func() { os.Args = saved }()
	for _, v := range []struct {
		args []string
		seek int64
		set  bool
		tail bool
		bad  bool
	}{
		{[]string{"--flacseek=20000000", "--trackurl=https://streaming-qobuz-std.akamaized.net/file?uid=1&fmt=7"}, 20000000, true, false, false},
		{[]string{"--flacseek=0", "--trackurl=http://h/x.flac"}, 0, true, false, false},
		{[]string{"--flacseek=20000000", "--trackurl=qobuz://123.flac"}, 0, false, false, true},
		{[]string{"--flacseek=20000000"}, 0, false, false, true},
		{[]string{"--flacseek=-1", "--trackurl=http://h/x.flac"}, 0, false, false, true},
		{[]string{"--tail", "--formatout=WAV"}, 0, false, true, false},
		{[]string{"--formatout=WAV", "--tail"}, 0, false, true, false},
	} {
		os.Args = append([]string{"SqueezeDSP", "--Clientid=cd-ef-e3-a9-15-31"}, v.args...)
		a, err := ReadArgs()
		if (err != nil) != v.bad {
			t.Errorf("%v: error %v, want an error %v", v.args, err, v.bad)
			continue
		}
		if !v.bad && (a.FlacSeek != v.seek || a.FlacSeekSet != v.set || a.Tail != v.tail || (v.tail && a.OutputFormat != "wav")) {
			t.Errorf("%v: seek %d set %v tail %v out %q", v.args, a.FlacSeek, a.FlacSeekSet, a.Tail, a.OutputFormat)
		}
	}
}
