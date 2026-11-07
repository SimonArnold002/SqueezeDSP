package foxAudioEncoder

import (
	"io"
	"math"
	"os"
	"testing"
	"time"

	"github.com/Foxenfurter/foxAudioLib/foxSpeed"
)

// encodeToPipe encodes frames of silence (PCM, 24-bit stereo at 1 kHz) in the blocks given, stdout a pipe whose reader
// starts after delay; the meter's figures and the wall time taken.
func encodeToPipe(t *testing.T, delay time.Duration, blocks ...int) (audio, busy, wall float64) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = old }()
	drained := make(chan struct{})
	go func() {
		time.Sleep(delay)
		io.Copy(io.Discard, r)
		close(drained)
	}()
	e := &AudioEncoder{Type: "PCM", SampleRate: 1000, BitDepth: 24, NumChannels: 2, Meter: &foxSpeed.Meter{Name: "encoder", Rate: 1000}}
	if err := e.Initialise(); err != nil {
		t.Fatal(err)
	}
	in := make(chan [][]float64, len(blocks))
	for _, n := range blocks {
		in <- [][]float64{make([]float64, n), make([]float64, n)}
	}
	close(in)
	t0 := time.Now()
	if err := e.EncodeSamplesChannel(in); err != nil {
		t.Fatal(err)
	}
	wall = time.Since(t0).Seconds()
	w.Close()
	<-drained
	audio, busy = e.Meter.Figures()
	return audio, busy, wall
}

// The meter counts the encoding, not the writes the reader holds back: 1.2 MB of PCM, more than a pipe holds, its
// reader asleep for 300 ms, takes that long while the encoding itself takes a few ms.
func TestMeterLeavesOutWrites(t *testing.T) {
	audio, busy, wall := encodeToPipe(t, 300*time.Millisecond, 100000, 100000)
	if audio != 200 || wall < 0.29 || busy <= 0 || busy > 0.1 {
		t.Fatalf("audio %.3f s, busy %.3f s over %.3f s: the wait for the reader should not count", audio, busy, wall)
	}
}

// The meter's audio is every frame the encoder was handed, the batches and the last part flushed at the end: 2500
// frames (one 1365-frame batch, then the rest) at 1 kHz read back as 2.5 s.
func TestMeterCountsEveryFrame(t *testing.T) {
	if audio, busy, _ := encodeToPipe(t, 0, 700, 900, 900); audio != 2.5 || busy <= 0 {
		t.Fatalf("audio %.3f s, busy %.6f s: want every frame handed over, 2.5 s, and some work", audio, busy)
	}
}

// fillThroughPipe encodes secs of silence (PCM, 24-bit stereo at 48 kHz) with a fill, stdout a pipe that read drains
// first (the rest is then drained freely); the fill's figures.
func fillThroughPipe(t *testing.T, secs float64, read func(r *os.File)) (audio, busy float64, over bool) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = old }()
	drained := make(chan struct{})
	go func() {
		read(r)
		io.Copy(io.Discard, r)
		close(drained)
	}()
	e := &AudioEncoder{Type: "PCM", SampleRate: 48000, BitDepth: 24, NumChannels: 2, Fill: &foxSpeed.Fill{}}
	if err := e.Initialise(); err != nil {
		t.Fatal(err)
	}
	in := make(chan [][]float64, 8)
	go func() {
		for n := int(secs * 48000); n > 0; n -= 4800 {
			k := min(n, 4800)
			in <- [][]float64{make([]float64, k), make([]float64, k)}
		}
		close(in)
	}()
	if err := e.EncodeSamplesChannel(in); err != nil {
		t.Fatal(err)
	}
	w.Close()
	<-drained
	return e.Fill.Figures()
}

// The fill stops at the first window the reader holds back: 10 s of audio, the reader taking three windows' bytes (6 s)
// at once and then pausing for a second: the fill is the three windows before the pause (6.06 s: a window closes on
// the write that takes it to 2 s), over, and took a fraction of the pause.
func TestFillStopsWhenTheReaderHoldsBack(t *testing.T) {
	audio, busy, over := fillThroughPipe(t, 10, func(r *os.File) {
		io.CopyN(io.Discard, r, 3*2*48000*6)
		time.Sleep(time.Second)
	})
	if audio < 6 || audio > 6.1 || !over || busy <= 0 || busy > 0.5 {
		t.Fatalf("audio %.3f s, busy %.3f s, over %v: want the 6 s before the pause, over", audio, busy, over)
	}
}

// A stream that ends inside the fill counts to its last part: 3 s, the reader taking everything.
func TestFillCountsTheLastPart(t *testing.T) {
	if audio, busy, over := fillThroughPipe(t, 3, func(*os.File) {}); math.Abs(audio-3) > 1e-9 || !over || busy <= 0 {
		t.Fatalf("audio %.3f s, busy %.3f s, over %v: want all 3 s, over", audio, busy, over)
	}
}
