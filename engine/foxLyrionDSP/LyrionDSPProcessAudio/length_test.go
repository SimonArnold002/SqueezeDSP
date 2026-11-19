package LyrionDSPProcessAudio

import (
	"path/filepath"
	"sync"
	"testing"
)

// gate runs lengthStage over blocks of the given sizes, the frames numbered from first (both channels the frame's
// number), with in input frames counted; it returns the frames sent.
func gate(t *testing.T, ap *AudioProcessor, first int, sizes []int, in int64) []float64 {
	t.Helper()
	ap.length.in.Store(in)
	ch := make(chan [][]float64, len(sizes))
	n := first
	for _, sz := range sizes {
		b := [][]float64{make([]float64, sz), make([]float64, sz)}
		for i := 0; i < sz; i++ {
			b[0][i], b[1][i] = float64(n), float64(n)
			n++
		}
		ch <- b
	}
	close(ch)
	var wg sync.WaitGroup
	var got []float64
	for b := range ap.lengthStage(ch, &wg) {
		if len(b) != 2 || len(b[0]) != len(b[1]) {
			t.Fatalf("a block of %d channels", len(b))
		}
		got = append(got, b[0]...)
	}
	wg.Wait()
	return got
}

func lengthProc(t *testing.T, follows bool) *AudioProcessor {
	note := "continues=0\nprev=\nthis=44100:pcm:2\nfollows=0\nnext=\n"
	if follows {
		note = "continues=0\nprev=\nthis=44100:pcm:2\nfollows=1\nnext=44100:pcm:2\n"
	}
	ap, dir := noteProc(t, note)
	ap.Decoder.SampleRate = ap.ProcRate // 1:1, so positions are frames
	ap.gapless.path = filepath.Join(dir, "gapless_bench")
	ap.gapless.note = readGaplessNote(ap.gapless.path)
	ap.length.on = true
	return ap
}

func numbered(t *testing.T, got []float64, first, n int, label string) {
	t.Helper()
	if len(got) < n {
		t.Fatalf("%s: %d frames, want at least %d", label, len(got), n)
	}
	for i := 0; i < n; i++ {
		if got[i] != float64(first+i) {
			t.Fatalf("%s: frame %d is %v, want %d", label, i, got[i], first+i)
		}
	}
}

// TestLengthGateHeldAndPending: a held stream is cut holdBack frames before its input's end, whatever the blocks, and
// the frames the stages sent past the cut are kept; the next stream starts with them, at the cut.
func TestLengthGateHeldAndPending(t *testing.T) {
	for _, sizes := range [][]int{{997}, {1, 500, 496}, {300, 300, 300, 97}, {990, 7}} {
		ap := lengthProc(t, true)
		ap.length.holdBack = 5
		// 1000 input frames; the stages hold 3 of them, so 997 came out
		got := gate(t, ap, 0, sizes, 1000)
		g := &ap.length
		if len(got) != 995 || g.sent != 995 || g.cut != 995 || !g.held || g.cumIn != 1000 {
			t.Fatalf("blocks %v: %d frames sent (sent %d, cut %d, held %v, in %d), want 995, held", sizes, len(got), g.sent, g.cut, g.held, g.cumIn)
		}
		numbered(t, got, 0, 995, "the held stream")
		if len(g.keep) != 2 || len(g.keep[0]) != 2 || g.keep[0][0] != 995 || g.keep[1][1] != 996 {
			t.Fatalf("blocks %v: kept %v, want frames 995 and 996", sizes, g.keep)
		}
		// the next track: 400 more input frames, nothing follows, a tail of 10: the stages send the 3 they held, their
		// 400 and 12 of silence (whole input frames, a few more than the tail)
		next := lengthProc(t, false)
		next.length.holdBack, next.length.tail = 5, 10
		next.length.prevIn, next.length.prevCut, next.length.pending = g.cumIn, g.cut, g.keep
		got = gate(t, next, 997, []int{200, 215}, 400)
		if len(got) != 415 || next.length.sent != 415 || next.length.cut != 1410 || next.length.held {
			t.Fatalf("the next stream: %d frames (sent %d, cut %d), want 415 to frame 1410", len(got), next.length.sent, next.length.cut)
		}
		numbered(t, got, 995, 415, "the next stream")
	}
}

// TestLengthGateShortTrack: a track shorter than the hold-back sends nothing of its own and keeps it all.
func TestLengthGateShortTrack(t *testing.T) {
	ap := lengthProc(t, true)
	ap.length.holdBack = 50
	ap.length.prevIn, ap.length.prevCut = 1000, 960
	ap.length.pending = [][]float64{{960, 961}, {960, 961}}
	got := gate(t, ap, 962, []int{20}, 20) // the stages now at 982 of 1020
	g := &ap.length
	if len(got) != 10 || g.cut != 970 || len(g.keep[0]) != 12 || g.keep[0][0] != 970 {
		t.Fatalf("%d frames sent to %d, %d kept from %v; want 10 to frame 970 (1020 less 50), 12 kept from 970", len(got), g.cut, len(g.keep[0]), g.keep[0])
	}
	numbered(t, got, 960, 10, "the short stream")
}

// TestLengthGatePlaysOutExactly: nothing follows: the stream runs to the input's end and the tail, to the frame: what
// the stages send past it is dropped, what they fall short is silence.
func TestLengthGatePlaysOutExactly(t *testing.T) {
	for _, v := range []struct {
		sent int // frames the stages send
		zero int // of the gate's own silence at the end
	}{{112, 0}, {110, 0}, {104, 6}} {
		ap := lengthProc(t, false)
		ap.length.holdBack, ap.length.tail = 3, 10
		ap.length.exact, ap.length.statedIn, ap.length.stated = true, 100, 110
		got := gate(t, ap, 0, []int{v.sent}, 100)
		if len(got) != 110 || ap.length.sent != 110 {
			t.Fatalf("the stages sent %d: the stream has %d frames, want 110", v.sent, len(got))
		}
		numbered(t, got, 0, 110-v.zero, "the stream")
		for i := 110 - v.zero; i < 110; i++ {
			if got[i] != 0 {
				t.Fatalf("the stages sent %d: frame %d is %v, want silence", v.sent, i, got[i])
			}
		}
	}
}

// TestLengthGateFollowsTheInput: the cut comes from the frames the input carried, not from what it stated (a
// decoder's header can be wrong: faad's, measured 2026-11-15): a longer input loses nothing, a shorter one gets no
// silence up to the stated count.
func TestLengthGateFollowsTheInput(t *testing.T) {
	for _, in := range []int{130, 70} {
		ap := lengthProc(t, false)
		ap.length.tail = 10
		ap.length.exact, ap.length.statedIn, ap.length.stated = true, 100, 110
		got := gate(t, ap, 0, []int{in + 12}, int64(in))
		if len(got) != in+10 || ap.length.sent != int64(in+10) {
			t.Fatalf("the input carried %d of a stated 100: the stream has %d frames, want %d", in, len(got), in+10)
		}
		numbered(t, got, 0, in+10, "the stream")
	}
}
