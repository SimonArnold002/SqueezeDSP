package LyrionDSPProcessAudio

import (
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Foxenfurter/foxAudioLib/foxAudioDecoder"
	"github.com/Foxenfurter/foxAudioLib/foxAudioEncoder"
	"github.com/Foxenfurter/foxAudioLib/foxFilters"
	"github.com/Foxenfurter/foxAudioLib/foxLog"
	"github.com/Foxenfurter/foxAudioLib/foxUpsampler"
	"github.com/Foxenfurter/foxLyrionDSP/LyrionDSPSettings"
)

func TestGaplessNote(t *testing.T) {
	p := filepath.Join(t.TempDir(), "gapless_x")
	if n := readGaplessNote(p); n.ok {
		t.Fatal("no note read as one")
	}
	os.WriteFile(p, []byte("continues=1\nprev=file:///m/a=b.flac\nthis=44100:pcm:2\nfollows=1\nnext=2822400:dsd:2\n"), 0644)
	n := readGaplessNote(p)
	if !n.ok || !n.continues || !n.follows || n.prev != "file:///m/a=b.flac" {
		t.Fatalf("note %+v (a URL keeps its own '=')", n)
	}
	if n.this != (trackKind{44100, "pcm", 2}) || n.next != (trackKind{2822400, "dsd", 2}) {
		t.Fatalf("this %+v next %+v", n.this, n.next)
	}
	os.WriteFile(p, []byte("continues=0\nprev=\nthis=0::0\nfollows=0\nnext=\n"), 0644)
	if n := readGaplessNote(p); !n.ok || n.continues || n.follows || n.prev != "" || n.this != (trackKind{}) || n.next != (trackKind{}) {
		t.Fatalf("an empty note %+v", n)
	}
}

func TestTrackKindSameAs(t *testing.T) {
	a := trackKind{44100, "pcm", 2}
	for _, v := range []struct {
		b    trackKind
		same bool
	}{
		{trackKind{44100, "pcm", 2}, true},
		{trackKind{0, "", 0}, true}, // nothing known: taken as the same
		{trackKind{48000, "pcm", 2}, false},
		{trackKind{44100, "dsd", 2}, false},
		{trackKind{44100, "pcm", 1}, false},
		{trackKind{0, "pcm", 0}, true},
	} {
		if got := v.b.sameAs(a); got != v.same {
			t.Errorf("%+v against %+v: %v, want %v", v.b, a, got, v.same)
		}
	}
}

// a processor in note mode with its note at path
func noteProc(t *testing.T, note string) (*AudioProcessor, string) {
	t.Helper()
	dir := t.TempDir()
	logger, err := foxLog.NewLogger(filepath.Join(dir, "engine.log"), "test", true)
	if err != nil {
		t.Fatal(err)
	}
	ap := &AudioProcessor{Logger: logger, Config: &LyrionDSPSettings.ClientConfig{},
		AppSettings: &LyrionDSPSettings.AppSettings{TempDataFolder: dir},
		Args:        &LyrionDSPSettings.Arguments{CleanClientID: "bench", TrackURL: "file:///m/b.flac"},
		Decoder:     &foxAudioDecoder.AudioDecoder{SampleRate: 44100, NumChannels: 2}, ProcRate: 352800}
	ap.cachePath = filepath.Join(dir, "dsp_residuals_bench.gob")
	if note != "" {
		os.WriteFile(filepath.Join(dir, "gapless_bench"), []byte(note), 0644)
	}
	return ap, dir
}

// TestEndHold: at the input's end every stage holds only when the note, read then, says a track follows of the same
// rate, kind and channels; no note: as before (the convolvers keep their tails).
func TestEndHold(t *testing.T) {
	for _, v := range []struct {
		end  string // the note at the end ("" none)
		hold bool
	}{
		{"follows=1\nthis=44100:pcm:2\nnext=44100:pcm:2\n", true},
		{"follows=1\nthis=44100:pcm:2\nnext=0::0\n", true},
		{"follows=0\nthis=44100:pcm:2\nnext=\n", false},
		{"follows=1\nthis=44100:pcm:2\nnext=96000:pcm:2\n", false},
		{"follows=1\nthis=2822400:dsd:2\nnext=44100:pcm:2\n", false},
		{"follows=1\nthis=44100:pcm:2\nnext=44100:pcm:6\n", false},
	} {
		ap, dir := noteProc(t, "continues=0\n")
		ap.gaplessStart()
		// the plugin rewrites the note before the end (a playlist change)
		os.WriteFile(filepath.Join(dir, "gapless_bench"), []byte(v.end), 0644)
		if got := ap.endHold(); got != v.hold {
			t.Errorf("note at the end %q: hold %v, want %v", v.end, got, v.hold)
		}
		os.WriteFile(filepath.Join(dir, "gapless_bench"), []byte("follows=0\n"), 0644)
		if got := ap.endHold(); got != v.hold {
			t.Errorf("%q: decided again (%v): endHold decides once", v.end, got)
		}
	}
	ap, _ := noteProc(t, "")
	ap.gaplessStart()
	if ap.noteMode() || !ap.endHold() {
		t.Error("no note: not note mode, and holding as before")
	}
	// bypassed: nothing is saved for a next track, so nothing is held back from this one
	ap, _ = noteProc(t, "continues=0\nfollows=1\nthis=44100:pcm:2\nnext=44100:pcm:2\n")
	ap.Config.Bypass = true
	ap.gaplessStart()
	if ap.endHold() {
		t.Error("a bypassed stream held its stages for a next track")
	}
}

// TestGaplessStartGate: the last track's state only for the stream that continues the track which held it; the
// residual is removed once read; the album's gain carries whatever happens.
func TestGaplessStartGate(t *testing.T) {
	gain := -3.5
	for _, v := range []struct {
		name  string
		note  string
		held  bool
		url   string
		carry bool
	}{
		{"continues the track that held", "continues=1\nprev=file:///m/a.flac\n", true, "file:///m/a.flac", true},
		{"no prev said (an engine run by hand)", "continues=1\n", true, "file:///m/a.flac", true},
		{"continues another track", "continues=1\nprev=file:///m/z.flac\n", true, "file:///m/a.flac", false},
		{"a fresh start", "continues=0\nprev=\n", true, "file:///m/a.flac", false},
		{"the last track played out", "continues=1\nprev=file:///m/a.flac\n", false, "file:///m/a.flac", false},
	} {
		ap, _ := noteProc(t, v.note)
		res := &DSPResidualsCache{Version: CACHE_VERSION, SampleRate: 352800, NumChannels: 2, Held: v.held, TrackURL: v.url, PreviousAlbumGain: &gain, PreviousAlbumID: "album"}
		if err := ap.saveResiduals(res); err != nil {
			t.Fatal(err)
		}
		ap.gaplessStart()
		if ap.gapless.carry != v.carry {
			t.Errorf("%s: carry %v, want %v", v.name, ap.gapless.carry, v.carry)
		}
		if _, err := os.Stat(ap.cachePath); !os.IsNotExist(err) {
			t.Errorf("%s: the residual is still there after the start read it", v.name)
		}
		ap.LoadDSPResiduals()
		if ap.PreviousAlbumGain == nil || *ap.PreviousAlbumGain != gain || ap.PreviousAlbumID != "album" {
			t.Errorf("%s: the album's gain did not carry", v.name)
		}
	}
	// a residual at another rate or for other channels is no one's
	ap, _ := noteProc(t, "continues=1\nprev=file:///m/a.flac\n")
	ap.saveResiduals(&DSPResidualsCache{Version: CACHE_VERSION, SampleRate: 384000, NumChannels: 2, Held: true, TrackURL: "file:///m/a.flac"})
	ap.gaplessStart()
	if ap.gapless.carry {
		t.Error("a residual at 384k taken by a 352.8k stream")
	}
}

// TestSaveResidualsWhole: written whole and renamed into place, nothing left beside it.
func TestSaveResidualsWhole(t *testing.T) {
	ap, dir := noteProc(t, "continues=0\n")
	if err := ap.saveResiduals(&DSPResidualsCache{Version: CACHE_VERSION, SampleRate: 352800, NumChannels: 2, Held: true}); err != nil {
		t.Fatal(err)
	}
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if e.Name() != "dsp_residuals_bench.gob" && e.Name() != "gapless_bench" && e.Name() != "engine.log" {
			t.Errorf("left beside it: %s", e.Name())
		}
	}
	if c, err := ap.readResiduals(); err != nil || !c.Held {
		t.Errorf("read back: %v %+v", err, c)
	}
}

// run a stage: blocks of n frames in, everything out
func runStage(t *testing.T, stage func(chan [][]float64, int, *sync.WaitGroup) chan [][]float64, blocks, n int) [][]float64 {
	t.Helper()
	in := make(chan [][]float64, 2)
	var wg sync.WaitGroup
	out := stage(in, 1, &wg)
	go func() {
		for b := 0; b < blocks; b++ {
			blk := [][]float64{make([]float64, n), make([]float64, n)}
			for i := range blk[0] {
				blk[0][i], blk[1][i] = 0.5, -0.5
			}
			in <- blk
		}
		close(in)
	}()
	res := make([][]float64, 2)
	for blk := range out {
		res[0], res[1] = append(res[0], blk[0]...), append(res[1], blk[1]...)
	}
	wg.Wait()
	return res
}

// TestTailStage: in note mode, when nothing follows, the silence that plays every stage out goes in after the input;
// when a track follows, nothing; with no note, no stage at all.
func TestTailStage(t *testing.T) {
	ap, _ := noteProc(t, "")
	in := make(chan [][]float64)
	var wg sync.WaitGroup
	if ap.gaplessStart(); ap.tailStage(in, 1, &wg) != in {
		t.Error("no note: not the decoder's own channel")
	}
	for _, v := range []struct {
		note string
		tail int
	}{{"follows=0\n", 1000}, {"follows=1\nthis=44100:pcm:2\nnext=44100:pcm:2\n", 0}} {
		ap, _ := noteProc(t, v.note)
		ap.gaplessStart()
		ap.gapless.tailFrame = 1000
		got := runStage(t, ap.tailStage, 3, 500)
		if len(got[0]) != 1500+v.tail || len(got[1]) != len(got[0]) {
			t.Fatalf("%q: %d frames out for 1500 in, want %d", v.note, len(got[0]), 1500+v.tail)
		}
		for i := 1500; i < len(got[0]); i++ {
			if got[0][i] != 0 || got[1][i] != 0 {
				t.Fatalf("%q: frame %d after the input is not silence", v.note, i)
			}
		}
	}
}

// TestUpsampleStageHolds: in note mode a track that a next one goes on from ends without the upsampler's Flush (its last
// outputs are the next track's first); one that nothing follows, and any stream with no note, flushes.
func TestUpsampleStageHolds(t *testing.T) {
	for _, v := range []struct {
		note  string
		flush bool
	}{{"follows=1\nthis=44100:pcm:2\nnext=44100:pcm:2\n", false}, {"follows=0\n", true}, {"", true}} {
		ap, _ := noteProc(t, v.note)
		ap.gaplessStart()
		u, err := foxUpsampler.New(44100, 352800, 2, foxUpsampler.DefaultCD, foxUpsampler.DefaultHiRes)
		if err != nil {
			t.Fatal(err)
		}
		ap.Upsampler = u
		got := runStage(t, ap.upsampleStage, 4, 4410)
		full := 4 * 4410 * 8
		if v.flush && len(got[0]) != full || !v.flush && len(got[0]) != full-int(math.Round(u.Delay)) {
			t.Errorf("note %q: %d frames out, want %d (flush %v; the delay %.1f)", v.note, len(got[0]), full, v.flush, u.Delay)
		}
	}
}

// TestSaveDSPResidualsHeld: in note mode the stages' state is saved only for a track that a next one goes on from, and
// only when the DSP ran; the track's own URL always.
func TestSaveDSPResidualsHeld(t *testing.T) {
	for _, v := range []struct {
		note   string
		bypass bool
		held   bool
	}{{"follows=1\nthis=44100:pcm:2\nnext=44100:pcm:2\n", false, true}, {"follows=0\n", false, false},
		{"follows=1\nthis=44100:pcm:2\nnext=44100:pcm:2\n", true, false}} {
		ap, _ := noteProc(t, v.note)
		ap.gaplessStart()
		ap.Config.Bypass = v.bypass
		ap.Delay = foxFilters.NewDelay(2, 352800)
		ap.Encoder = &foxAudioEncoder.AudioEncoder{}
		u, _ := foxUpsampler.New(44100, 352800, 2, foxUpsampler.DefaultCD, foxUpsampler.DefaultHiRes)
		ap.Upsampler = u
		if err := ap.SaveDSPResiduals(10, 1); err != nil {
			t.Fatal(err)
		}
		c, err := ap.readResiduals()
		if err != nil {
			t.Fatal(err)
		}
		if c.Held != v.held || (c.Upsampler != nil) != v.held || (c.DelayBuffers != nil) != v.held || c.TrackURL != "file:///m/b.flac" {
			t.Errorf("note %q bypass %v: held %v, upsampler %v, delay %v, url %q; want held %v", v.note, v.bypass, c.Held,
				c.Upsampler != nil, c.DelayBuffers != nil, c.TrackURL, v.held)
		}
	}
}
