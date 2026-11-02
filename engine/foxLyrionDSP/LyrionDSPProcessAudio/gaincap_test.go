package LyrionDSPProcessAudio

// Our tests (ledger A RESAMPLED IMPULSE GAINS THE RATE RATIO): an impulse resampled by sox is scaled
// back to the gain it was made with, and ReplayGain is capped by its peak as LMS caps it. Each
// requirement has a control that must not move.
//   cd engine/foxLyrionDSP && go test -run 'Scale|Cap|Resolve' ./LyrionDSPProcessAudio/

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/Foxenfurter/foxAudioLib/foxLog"
	"github.com/Foxenfurter/foxLyrionDSP/LyrionClient"
	"github.com/Foxenfurter/foxLyrionDSP/LyrionDSPSettings"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestScaleImpulse(t *testing.T) {
	impulse := [][]float64{{1, 0.5}, {-2, 0}}
	if k := scaleImpulse(impulse, 96000, 384000); !near(k, 0.25) {
		t.Errorf("96k to 384k: factor %v, want 0.25", k)
	}
	if !near(impulse[0][0], 0.25) || !near(impulse[0][1], 0.125) || !near(impulse[1][0], -0.5) {
		t.Errorf("96k to 384k: every channel times 0.25, got %v", impulse)
	}
	down := [][]float64{{1}}
	if k := scaleImpulse(down, 96000, 44100); !near(k, 96000.0/44100.0) || !near(down[0][0], 96000.0/44100.0) {
		t.Errorf("96k to 44.1k: times 96000/44100, got factor %v, sample %v", k, down[0][0])
	}
	// controls: the same rate, or a rate not known, leave the impulse as it is
	for _, c := range [][2]int{{96000, 96000}, {0, 384000}, {96000, 0}} {
		same := [][]float64{{1, -1}}
		if k := scaleImpulse(same, c[0], c[1]); k != 1 || same[0][0] != 1 || same[0][1] != -1 {
			t.Errorf("%d to %d: untouched, got factor %v, %v", c[0], c[1], k, same)
		}
	}
	if k := scaleImpulse(nil, 96000, 384000); !near(k, 0.25) {
		t.Errorf("no impulse (a failed load): no panic, factor %v", k)
	}
}

func f(v float64) *float64 { return &v }

func TestCapByPeak(t *testing.T) {
	// LMS's preventClipping (Slim/Player/ReplayGain.pm:267-277), case by case
	cases := []struct {
		name   string
		gain   float64
		peak   *float64
		want   float64
		capped bool
	}{
		{"U.F.Orb: +4.79 with peak 0.851105 -> +1.40", 4.79, f(0.851105), -20 * math.Log10(0.851105), true},
		{"DSOTM: -6.76 with peak 0.98 stays", -6.76, f(0.98), -6.76, false},
		{"a peak above full scale lowers even a cut", -1, f(1.2), -20 * math.Log10(1.2), true},
		{"no peak: as it is", 4.79, nil, 4.79, false},
		{"peak 0 (no data): as it is", 4.79, f(0), 4.79, false},
		{"a negative peak: as it is", 4.79, f(-0.5), 4.79, false},
		{"just under the cap (+1.00 against 1.01): not capped", 1, f(0.89), 1, false},
	}
	for _, c := range cases {
		got, capped := capByPeak(c.gain, c.peak)
		if math.Abs(got-c.want) > 1e-9 || capped != c.capped {
			t.Errorf("%s: got %.6f capped %v, want %.6f capped %v", c.name, got, capped, c.want, c.capped)
		}
	}
	if got, _ := capByPeak(4.79, f(0.851105)); math.Abs(got-1.4003) > 0.0001 {
		t.Errorf("U.F.Orb cap %.4f dB, want 1.4003", got)
	}
}

func processor(t *testing.T, mode int, data *LyrionClient.TrackGainResult, url string) *AudioProcessor {
	t.Helper()
	logger, err := foxLog.NewLogger(filepath.Join(t.TempDir(), "engine.log"), "test", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(logger.Close)
	cfg := &LyrionDSPSettings.ClientConfig{}
	cfg.ReplayGain = LyrionDSPSettings.ReplayGainConfig{Enabled: true, Mode: mode, FixedGain: -6, SpotifyGain: -4}
	ap := &AudioProcessor{Logger: logger, Config: cfg, Args: &LyrionDSPSettings.Arguments{TrackURL: url}}
	ap.gainCh = make(chan *LyrionClient.TrackGainResult, 1)
	ap.gainCh <- data
	return ap
}

func TestResolveReplayGainCap(t *testing.T) {
	orb := func() *LyrionClient.TrackGainResult {
		return &LyrionClient.TrackGainResult{URL: "file:///orb/2.flac", TrackGain: f(0.4), TrackPeak: f(0.5),
			AlbumGain: f(4.79), AlbumPeak: f(0.851105), AlbumMatch: true, AlbumID: "local:album:2",
			NextAlbumGain: f(4.79), NextAlbumID: "local:album:2"}
	}
	albumCap := -20 * math.Log10(0.851105)

	// mode 2, album in sequence: the album gain, capped by the ALBUM peak
	ap := processor(t, 2, orb(), "file:///orb/2.flac")
	ap.ResolveReplayGain()
	if math.Abs(ap.ReplayGain-albumCap) > 1e-9 {
		t.Errorf("mode 2 album: %.4f, want the album peak's cap %.4f", ap.ReplayGain, albumCap)
	}
	if ap.PreviousAlbumGain == nil || *ap.PreviousAlbumGain != 4.79 {
		t.Errorf("mode 2: PreviousAlbumGain keeps the album's own gain for matching, got %v", ap.PreviousAlbumGain)
	}
	if ap.PreviousTrackGain != ap.ReplayGain {
		t.Errorf("mode 2: a repeat of the track reuses the capped gain, got %.4f vs %.4f", ap.PreviousTrackGain, ap.ReplayGain)
	}

	// mode 2, not in sequence: the track gain, under the TRACK peak (+0.4 against a 6.02 dB cap: stays)
	d := orb()
	d.AlbumMatch = false
	d.AlbumID = ""
	ap = processor(t, 2, d, "file:///orb/2.flac")
	ap.ResolveReplayGain()
	if ap.ReplayGain != 0.4 {
		t.Errorf("mode 2 track: %.4f, want 0.4 (its own peak leaves room)", ap.ReplayGain)
	}
	d = orb()
	d.AlbumMatch = false
	d.AlbumID = ""
	d.TrackGain = f(9)
	ap = processor(t, 2, d, "file:///orb/2.flac")
	ap.ResolveReplayGain()
	if want := -20 * math.Log10(0.5); math.Abs(ap.ReplayGain-want) > 1e-9 {
		t.Errorf("mode 2 track +9: %.4f, want the TRACK peak's cap %.4f", ap.ReplayGain, want)
	}

	// mode 1: track gain, track peak
	d = orb()
	d.TrackGain = f(9)
	ap = processor(t, 1, d, "file:///orb/2.flac")
	ap.ResolveReplayGain()
	if want := -20 * math.Log10(0.5); math.Abs(ap.ReplayGain-want) > 1e-9 {
		t.Errorf("mode 1: %.4f, want the track peak's cap %.4f", ap.ReplayGain, want)
	}

	// mode 3, album matched with the next track: album peak
	ap = processor(t, 3, orb(), "file:///orb/2.flac")
	ap.ResolveReplayGain()
	if math.Abs(ap.ReplayGain-albumCap) > 1e-9 {
		t.Errorf("mode 3 album (matched next): %.4f, want %.4f", ap.ReplayGain, albumCap)
	}
	if ap.PreviousAlbumGain == nil || *ap.PreviousAlbumGain != 4.79 {
		t.Errorf("mode 3: PreviousAlbumGain keeps 4.79 so the next track still matches, got %v", ap.PreviousAlbumGain)
	}

	// an album gain with no album peak: the track's own peak caps it, in every album branch
	noAlbumPeak := func() *LyrionClient.TrackGainResult {
		d := orb()
		d.AlbumPeak = nil
		d.TrackPeak = f(0.85)
		return d
	}
	trackCap := -20 * math.Log10(0.85)
	ap = processor(t, 2, noAlbumPeak(), "file:///orb/2.flac")
	ap.ResolveReplayGain()
	if math.Abs(ap.ReplayGain-trackCap) > 1e-9 {
		t.Errorf("mode 2 album, no album peak: %.4f, want the TRACK peak's cap %.4f", ap.ReplayGain, trackCap)
	}
	ap = processor(t, 3, noAlbumPeak(), "file:///orb/2.flac")
	ap.ResolveReplayGain()
	if math.Abs(ap.ReplayGain-trackCap) > 1e-9 {
		t.Errorf("mode 3 album (matched next), no album peak: %.4f, want %.4f", ap.ReplayGain, trackCap)
	}
	d = noAlbumPeak()
	d.NextAlbumGain = nil
	ap = processor(t, 3, d, "file:///orb/2.flac")
	ap.PreviousAlbumGain = f(4.79)
	ap.ResolveReplayGain()
	if math.Abs(ap.ReplayGain-trackCap) > 1e-9 {
		t.Errorf("mode 3 album (matched previous), no album peak: %.4f, want %.4f", ap.ReplayGain, trackCap)
	}
	if ap.PreviousAlbumGain == nil || *ap.PreviousAlbumGain != 4.79 {
		t.Errorf("mode 3, no album peak: PreviousAlbumGain still the album's own 4.79, got %v", ap.PreviousAlbumGain)
	}
	// controls: an album peak is used even where the track's is lower; no peak at all leaves the gain as LMS does
	d = orb()
	d.TrackPeak = f(0.1)
	ap = processor(t, 2, d, "file:///orb/2.flac")
	ap.ResolveReplayGain()
	if math.Abs(ap.ReplayGain-albumCap) > 1e-9 {
		t.Errorf("control, album peak present and track peak lower: %.4f, want the ALBUM peak's cap %.4f", ap.ReplayGain, albumCap)
	}
	d = noAlbumPeak()
	d.TrackPeak = nil
	ap = processor(t, 2, d, "file:///orb/2.flac")
	ap.ResolveReplayGain()
	if ap.ReplayGain != 4.79 {
		t.Errorf("control, no peak at all: the album gain as it is (LMS), got %.4f", ap.ReplayGain)
	}

	// controls: a cut is never raised, a default or Spotify gain has no peak to answer to
	dsotm := &LyrionClient.TrackGainResult{URL: "file:///d/1.flac", TrackGain: f(-5), TrackPeak: f(0.98),
		AlbumGain: f(-6.76), AlbumPeak: f(0.98), AlbumMatch: true, AlbumID: "local:album:1"}
	ap = processor(t, 2, dsotm, "file:///d/1.flac")
	ap.ResolveReplayGain()
	if ap.ReplayGain != -6.76 {
		t.Errorf("control, DSOTM album -6.76: got %.4f", ap.ReplayGain)
	}
	ap = processor(t, 2, &LyrionClient.TrackGainResult{URL: "file:///x.flac", TrackPeak: f(1.5), AlbumPeak: f(1.5)}, "file:///x.flac")
	ap.ResolveReplayGain()
	if ap.ReplayGain != -6 {
		t.Errorf("control, no gain data: the fixed -6, not capped by a stray peak, got %.4f", ap.ReplayGain)
	}
	ap = processor(t, 2, &LyrionClient.TrackGainResult{URL: "spotify://track:1", TrackGain: f(9), TrackPeak: f(1.5)}, "spotify://track:1")
	ap.ResolveReplayGain()
	if ap.ReplayGain != -4 {
		t.Errorf("control, Spotify: its fixed -4, got %.4f", ap.ReplayGain)
	}
}
