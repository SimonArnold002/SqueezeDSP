package LyrionDSPProcessAudio

import (
	"bytes"
	"context"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Foxenfurter/foxAudioLib/foxAudioDecoder"
	"github.com/Foxenfurter/foxAudioLib/foxLog"
	"github.com/Foxenfurter/foxLyrionDSP/LyrionDSPSettings"
)

// THE EXACT LENGTH ROUND, step 4: the exact-length contract (a player that needs the true length before the first
// byte), the held tail on its own, and a remote FLAC's count read from its URL.

func exactProc(t *testing.T, follows bool) *AudioProcessor {
	note := "continues=0\nprev=\nthis=44100:pcm:2\nfollows=0\nnext=\nexact=1\n"
	if follows {
		note = "continues=0\nprev=\nthis=44100:pcm:2\nfollows=1\nnext=44100:pcm:2\nexact=1\n"
	}
	ap, dir := noteProc(t, note)
	ap.Decoder.SampleRate = ap.ProcRate
	ap.gapless.path = filepath.Join(dir, "gapless_bench")
	ap.gapless.note = readGaplessNote(ap.gapless.path)
	return ap
}

// TestExactDecidedAtStart: under the exact contract the end is decided from the note as it is at the start: a next
// track removed (or added) afterwards does not change it; under LMS's the note at the input's end decides (CONTROL).
func TestExactDecidedAtStart(t *testing.T) {
	if n := readGaplessNote(""); n.exact {
		t.Fatal("no note: not exact")
	}
	for _, follows := range []bool{true, false} {
		ap := exactProc(t, follows)
		if !ap.gapless.note.exact {
			t.Fatal("the note's exact=1 not read")
		}
		// the queue changes after the start
		later := "follows=1\nthis=44100:pcm:2\nnext=44100:pcm:2\n"
		if follows {
			later = "follows=0\nthis=44100:pcm:2\nnext=\n"
		}
		os.WriteFile(ap.gapless.path, []byte(later), 0644)
		if got := ap.endHold(); got != follows {
			t.Fatalf("exact, follows %v at the start: endHold %v after the note changed", follows, got)
		}
		lms, dir := noteProc(t, "follows="+map[bool]string{true: "1", false: "0"}[follows]+"\nthis=44100:pcm:2\nnext=44100:pcm:2\n")
		lms.gapless.path = filepath.Join(dir, "gapless_bench")
		lms.gapless.note = readGaplessNote(lms.gapless.path)
		os.WriteFile(lms.gapless.path, []byte(later), 0644)
		if got := lms.endHold(); got == follows {
			t.Fatalf("CONTROL, LMS's contract: endHold %v did not follow the note at the end", got)
		}
	}
}

// TestExactLengthStart: the header's count under the exact contract is the stream's own: a held stream's ends holdBack
// before its input's end (LMS's contract states the played-out count for it, CONTROL), a stream nothing follows plays
// out; a held tail on its own (--tail) states holdBack + tail with no input.
func TestExactLengthStart(t *testing.T) {
	for _, v := range []struct {
		follows, tail, exact bool
		want                 int64
		label                string
	}{
		{true, false, true, 1000 - 7, "exact, held"},
		{false, false, true, 1000 + 20, "exact, played out"},
		{true, false, false, 1000 + 20, "CONTROL: LMS's contract states the played-out count for a held stream"},
		{false, true, true, 7 + 20, "--tail"},
	} {
		ap := exactProc(t, v.follows)
		ap.gapless.note.exact = v.exact
		ap.Args.InSamples = 1000
		if v.tail {
			ap.Args.Tail, ap.Args.InSamples = true, 0
			ap.gapless.note = gaplessNote{ok: true, continues: true, exact: true}
			ap.gapless.res = &DSPResidualsCache{Held: true, Gate: true, GateIn: 1000, GateCut: 1000 - 7}
			ap.gapless.carry = true
		}
		ap.length.holdBack = 7 // what the stages would set (none here)
		ap.gapless.tailOut = 20
		ap.lengthStart()
		g := &ap.length
		if !g.exact || g.stated != v.want || g.fixed != v.exact {
			t.Fatalf("%s: stated %d (exact %v, fixed %v), want %d", v.label, g.stated, g.exact, g.fixed, v.want)
		}
		if v.exact && ap.gapless.hold != v.follows {
			t.Fatalf("%s: decided hold %v at the start", v.label, ap.gapless.hold)
		}
	}
}

// exactGate: a 1:1 gate under the exact contract, the count known (statedIn), positions as given
func exactGate(t *testing.T, follows bool, prevIn, prevCut, statedIn, holdBack, tail int64, pending [][]float64) *AudioProcessor {
	ap := exactProc(t, follows)
	ap.length.on = true
	g := &ap.length
	g.prevIn, g.prevCut, g.pending, g.holdBack, g.tail, g.statedIn = prevIn, prevCut, pending, holdBack, tail, statedIn
	g.exact, g.fixed, g.contract = true, true, "exact"
	end := prevIn + statedIn
	g.stated = end + tail - prevCut
	if ap.endHold() {
		g.stated = max(end-holdBack, prevCut) - prevCut
	}
	return ap
}

// TestExactGateIsTheCount: the stream is the count stated at the start, whatever the stages send and whatever the
// input carried: a held stream cut holdBack before the stated end, the rest kept for the next; a played-out one to the
// end and its tail, past it dropped, short of it silence.
func TestExactGateIsTheCount(t *testing.T) {
	for _, v := range []struct {
		follows  bool
		in, sent int // input frames carried, frames the stages send
		want     int64
		keep     int
		zeroFrom int // the stream is silence from here (the stages fell short)
		label    string
	}{
		{true, 1000, 997, 995, 2, 995, "held, the stages hold 3 of a hold-back of 5"},
		{true, 1000, 990, 995, 0, 990, "held, the stages hold more than the hold-back: silence to the count"},
		{true, 1200, 1197, 995, 202, 995, "held, the input carried more than stated: the rest goes to the next"},
		{false, 1000, 1012, 1010, 0, 1010, "played out, the stages' silence past the tail dropped"},
		{false, 900, 912, 1010, 0, 912, "played out, the input carried less: silence to the count"},
	} {
		for _, sizes := range [][]int{{v.sent}, {1, v.sent - 1}, {v.sent / 3, v.sent / 3, v.sent - 2*(v.sent/3)}} {
			ap := exactGate(t, v.follows, 0, 0, 1000, 5, 10, nil)
			got := gate(t, ap, 0, sizes, int64(v.in))
			g := &ap.length
			if int64(len(got)) != v.want || g.sent != v.want || g.held != v.follows {
				t.Fatalf("%s %v: %d frames (sent %d, held %v), want %d", v.label, sizes, len(got), g.sent, g.held, v.want)
			}
			numbered(t, got, 0, v.zeroFrom, v.label)
			for i := v.zeroFrom; i < len(got); i++ {
				if got[i] != 0 {
					t.Fatalf("%s: frame %d is %v, want silence", v.label, i, got[i])
				}
			}
			if v.follows {
				if len(g.keep[0]) != v.keep || (v.keep > 0 && g.keep[0][0] != float64(v.want)) {
					t.Fatalf("%s: kept %d frames from %v, want %d from %d", v.label, len(g.keep[0]), g.keep[0], v.keep, v.want)
				}
				if g.cumIn != int64(v.in) || g.cut != v.want || g.tailFrames != int64(v.in)+10-v.want {
					t.Fatalf("%s: place %d / cut %d / tail %d", v.label, g.cumIn, g.cut, g.tailFrames)
				}
			}
		}
	}
}

// TestExactHeldThenTail: a held stream, then its tail on its own (no input, the held state taken, nothing follows): the
// two together are the track played out, frame for frame, and each is the count its header stated at its start.
func TestExactHeldThenTail(t *testing.T) {
	whole := gate(t, exactGate(t, false, 0, 0, 1000, 5, 10, nil), 0, []int{1012}, 1000)
	held := exactGate(t, true, 0, 0, 1000, 5, 10, nil)
	a := gate(t, held, 0, []int{997}, 1000)
	h := &held.length
	tail := exactGate(t, false, h.cumIn, h.cut, 0, 5, 10, h.keep)
	tail.Args.Tail = true
	if tail.length.stated != h.tailFrames || tail.length.stated != 15 {
		t.Fatalf("the tail states %d, the held stream said %d, want 15", tail.length.stated, h.tailFrames)
	}
	b := gate(t, tail, 997, []int{3 + 12}, 0) // the stages: the 3 they held, then the silence that plays them out
	if int64(len(a)) != h.stated || int64(len(b)) != tail.length.stated {
		t.Fatalf("held %d of %d stated, tail %d of %d", len(a), h.stated, len(b), tail.length.stated)
	}
	joined := append(append([]float64(nil), a...), b...)
	if len(joined) != len(whole) {
		t.Fatalf("held + tail %d frames, the track played out %d", len(joined), len(whole))
	}
	for i := range whole {
		if joined[i] != whole[i] {
			t.Fatalf("frame %d: held + tail %v, played out %v", i, joined[i], whole[i])
		}
	}
}

// ---- the remote FLAC's count ----

// frameHeader: a FLAC frame header (fixed or variable blocking), its coded number and CRC-8; bs and sr its block-size
// and sample-rate codes (their extra bytes as zeros)
func frameHeader(number int64, variable bool, bs, sr byte) []byte {
	h := []byte{0xff, 0xf8, bs<<4 | sr, 0x18} // stereo independent, 24-bit
	if variable {
		h[1] = 0xf9
	}
	h = append(h, coded(number)...)
	switch bs {
	case 6:
		h = append(h, 0)
	case 7:
		h = append(h, 0, 0)
	}
	switch sr {
	case 12:
		h = append(h, 0)
	case 13, 14:
		h = append(h, 0, 0)
	}
	return append(h, crc8(h))
}

func coded(v int64) []byte {
	if v < 0x80 {
		return []byte{byte(v)}
	}
	n := 2
	for v >= 1<<(5*n+1) {
		n++
	}
	b := make([]byte, n)
	for k := n - 1; k > 0; k-- {
		b[k] = 0x80 | byte(v&0x3f)
		v >>= 6
	}
	b[0] = byte(0xff<<(8-n)) | byte(v)
	return b
}

func flacHead(total int64, block int) []byte {
	b := []byte("fLaC")
	b = append(b, 0x80, 0, 0, 34) // the last block: STREAMINFO, 34 bytes
	m := make([]byte, 34)
	binary.BigEndian.PutUint16(m[0:], uint16(block))
	binary.BigEndian.PutUint16(m[2:], uint16(block))
	x := uint64(44100)<<44 | uint64(1)<<41 | uint64(23)<<36 | uint64(total)
	binary.BigEndian.PutUint64(m[10:], x)
	return append(b, m...)
}

// TestCodedNumber: FLAC's coded numbers, every length, round trip
func TestCodedNumber(t *testing.T) {
	for _, v := range []int64{0, 1, 0x7f, 0x80, 0x7ff, 0x800, 0xffff, 0x10000, 1<<21 - 1, 1 << 21, 1<<31 - 1, 1 << 31, 1<<36 - 1} {
		if got, ok := codedNumber(coded(v)); !ok || got != v {
			t.Fatalf("%d: %d %v", v, got, ok)
		}
	}
	if _, ok := codedNumber([]byte{0xc2, 0x02}); ok {
		t.Fatal("a bad continuation byte taken")
	}
}

// TestAlignedFrameAsLMS: the frame LMS's frameAlign starts at: the first header with good fields and a good CRC-8,
// false syncs passed by; for block-size codes 6 and 7 LMS looks for the CRC a byte off, so it passes those frames by
// too, and so must the count (else it names a frame flac never sees).
func TestAlignedFrameAsLMS(t *testing.T) {
	noise := bytes.Repeat([]byte{0x12, 0xff, 0xf8, 0xff, 0x00, 0x7e}, 7) // syncs with bad fields or CRC
	pad := bytes.Repeat([]byte{0x55}, 60)
	for _, v := range []struct {
		stream []byte
		at     int
		number int64
		label  string
	}{
		{append(append(append([]byte{}, noise...), frameHeader(1055, false, 5, 9)...), pad...), len(noise), 1055, "after false syncs"},
		{append(append(frameHeader(77, false, 6, 9), frameHeader(78, false, 5, 9)...), pad...), 7, 78, "block-size code 6 passed by, as LMS does"},
		{append(append(frameHeader(77, false, 7, 9), frameHeader(78, false, 5, 12)...), pad...), 8, 78, "block-size code 7 passed by"},
		{append(frameHeader(3_000_000, true, 5, 13), pad...), 0, 3_000_000, "variable blocking, 16-bit rate code"},
		{append([]byte("fLaC"), pad...), 0, 0, "the stream from its start"},
	} {
		at, n, _, err := alignedFrame(v.stream)
		if err != nil || at != v.at || n != v.number {
			t.Fatalf("%s: at %d number %d (%v), want at %d number %d", v.label, at, n, err, v.at, v.number)
		}
	}
	if _, _, _, err := alignedFrame(append(noise, pad...)); err == nil {
		t.Fatal("no good header: found one")
	}
}

// TestRemoteFlacCount: over HTTP with ranges, as the engine reads a Qobuz URL: the count from a seek's offset is
// STREAMINFO's total less the first frame's first sample (fixed blocking: its number x the block size; variable: its
// number); from offset 0, the total; a server without ranges, a STREAMINFO of no total, no frame: not known.
func TestRemoteFlacCount(t *testing.T) {
	const total, block = 11170089, 4608
	file := flacHead(total, block)
	file = append(file, bytes.Repeat([]byte{0x33}, 20000000-len(file))...)
	file = append(file, bytes.Repeat([]byte{0x44}, 517)...) // the seek lands mid-frame
	off := int64(20000000)
	file = append(file, frameHeader(1055, false, 5, 9)...)
	file = append(file, bytes.Repeat([]byte{0x33}, 300000)...)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "a.flac", time.Time{}, bytes.NewReader(file))
	}))
	defer srv.Close()
	ctx := context.Background()
	if n, err := remoteFlacCount(ctx, srv.Client(), srv.URL, off); err != nil || n != total-1055*block {
		t.Fatalf("from the seek: %d (%v), want %d", n, err, total-1055*block)
	}
	if n, err := remoteFlacCount(ctx, srv.Client(), srv.URL, 0); err != nil || n != total {
		t.Fatalf("from the start: %d (%v), want %d", n, err, total)
	}
	noRange := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(file[:70000]) }))
	defer noRange.Close()
	if _, err := remoteFlacCount(ctx, noRange.Client(), noRange.URL, off); err == nil {
		t.Fatal("a server that ignores Range: a count")
	}
	zero := append(flacHead(0, block), bytes.Repeat([]byte{0x33}, 1000)...)
	zs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "z.flac", time.Time{}, bytes.NewReader(zero))
	}))
	defer zs.Close()
	if _, err := remoteFlacCount(ctx, zs.Client(), zs.URL, 0); err == nil {
		t.Fatal("STREAMINFO of no total: a count")
	}
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	}))
	defer slow.Close()
	tctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	t0 := time.Now()
	if _, err := remoteFlacCount(tctx, slow.Client(), slow.URL, off); err == nil || time.Since(t0) > time.Second {
		t.Fatalf("a server that never answers: %v after %v", err, time.Since(t0))
	}
}

// TestRawFormatFromURL: raw PCM whose rate, channels or bits came as 0 (LMS knew no more of a Qobuz track yet; live
// 2026-11-16, the engine died "upsample: rates 0 to 48000, 0 channels") takes the remote FLAC's own from its
// STREAMINFO; a value LMS gave is kept; no http(s) URL, no FLAC, a server that never answers: as given.
func TestRawFormatFromURL(t *testing.T) {
	head := func(rate, ch, bps int) []byte {
		b := []byte("fLaC")
		b = append(b, 0x80, 0, 0, 34)
		m := make([]byte, 34)
		binary.BigEndian.PutUint16(m[0:], 4608)
		binary.BigEndian.PutUint16(m[2:], 4608)
		x := uint64(rate)<<44 | uint64(ch-1)<<41 | uint64(bps-1)<<36 | uint64(1000000)
		binary.BigEndian.PutUint64(m[10:], x)
		return append(append(b, m...), bytes.Repeat([]byte{0x33}, 70000)...)
	}
	asked := 0
	serve := func(file []byte) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			asked++
			http.ServeContent(w, r, "a.flac", time.Time{}, bytes.NewReader(file))
		}))
	}
	run := func(url string, rate, ch, bits int) (int, int, int) {
		logger, err := foxLog.NewLogger(filepath.Join(t.TempDir(), "engine.log"), "test", true)
		if err != nil {
			t.Fatal(err)
		}
		ap := &AudioProcessor{Logger: logger, Args: &LyrionDSPSettings.Arguments{TrackURL: url}}
		ap.Decoder = &foxAudioDecoder.AudioDecoder{SampleRate: rate, NumChannels: ch, BitDepth: bits}
		ap.rawFormatFromURL()
		return ap.Decoder.SampleRate, ap.Decoder.NumChannels, ap.Decoder.BitDepth
	}
	srv := serve(head(96000, 2, 24))
	defer srv.Close()
	if r, c, b := run(srv.URL, 0, 0, 0); r != 96000 || c != 2 || b != 24 {
		t.Fatalf("all three 0: %d %d %d, want the FLAC's 96000 2 24", r, c, b)
	}
	if r, c, b := run(srv.URL, 0, 1, 16); r != 96000 || c != 1 || b != 16 {
		t.Fatalf("only the rate 0: %d %d %d, want 96000 and LMS's 1 16 kept (the FLAC says 2 24)", r, c, b)
	}
	asked = 0
	if r, c, b := run(srv.URL, 44100, 2, 16); r != 44100 || c != 2 || b != 16 || asked != 0 {
		t.Fatalf("CONTROL: all given: %d %d %d after %d reads, want them as given and no read", r, c, b, asked)
	}
	if r, _, _ := run("qobuz://193171337.flac", 0, 0, 0); r != 0 {
		t.Fatalf("not an http(s) URL: %d, want 0 as given", r)
	}
	notFlac := serve(bytes.Repeat([]byte{0x49}, 70000))
	defer notFlac.Close()
	if r, _, _ := run(notFlac.URL, 0, 0, 0); r != 0 {
		t.Fatalf("not a FLAC: %d, want 0 as given", r)
	}
}
