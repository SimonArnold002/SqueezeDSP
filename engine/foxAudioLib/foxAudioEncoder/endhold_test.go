package foxAudioEncoder

import (
	"path/filepath"
	"testing"
)

// TestEndHoldClosesHeld (SqueezeDSP fork, the gapless round): DSF out asks EndHold at the input's end; true closes held
// (a state to go on from), false or none closes the stream.
func TestEndHoldClosesHeld(t *testing.T) {
	for _, v := range []struct {
		hold func() bool
		held bool
	}{{func() bool { return true }, true}, {func() bool { return false }, false}, {nil, false}} {
		e := &AudioEncoder{Type: "DSF", SampleRate: 352800, BitDepth: 24, NumChannels: 2, DSDRate: 2822400, DSDSource: "pcm",
			Frames: 20000, Bounded: true, Filename: filepath.Join(t.TempDir(), "out.dsf"), EndHold: v.hold}
		if err := e.Initialise(); err != nil {
			t.Fatal(err)
		}
		ch := make(chan [][]float64, 2)
		x := make([]float64, 10000)
		ch <- [][]float64{x, x}
		close(ch)
		if err := e.EncodeSamplesChannel(ch); err != nil {
			t.Fatal(err)
		}
		e.Close()
		_, err := e.DSDEncoder.State()
		if (err == nil) != v.held {
			t.Errorf("EndHold %v: a state to go on from %v, want %v", v.hold != nil, err == nil, v.held)
		}
	}
}
