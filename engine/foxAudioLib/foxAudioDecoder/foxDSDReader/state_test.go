package foxDSDReader

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// decodeOn: Open, SetState when given, DecodeInput, State when held; what it sent and the state
func decodeOn(t *testing.T, r *Reader, state []byte, hold bool) ([][]float64, []byte) {
	t.Helper()
	r.EndHold = func() bool { return hold }
	if err := r.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
	if state != nil {
		if err := r.SetState(state); err != nil {
			t.Fatalf("SetState: %v", err)
		}
	}
	ch := make(chan [][]float64, 4)
	errc := make(chan error, 1)
	go func() { errc <- r.DecodeInput(ch); close(ch) }()
	out := make([][]float64, r.NumChannels)
	for b := range ch {
		for c := range out {
			out[c] = append(out[c], b[c]...)
		}
	}
	if err := <-errc; err != nil {
		t.Fatalf("DecodeInput: %v", err)
	}
	var next []byte
	if hold {
		var err error
		if next, err = r.State(); err != nil {
			t.Fatalf("State: %v", err)
		}
	}
	return out, next
}

// TestStateJoinIsOneDecode: a DSD stream cut into three files (at bytes inside blocks), each decoded by a Reader of its
// own going on from the last one's State (the first two held at their end, the last played out), is the whole stream
// decoded and played out the same way, to the bit: targets by whole numbers (352.8k, 176.4k, 88.2k), through stage 3
// (96k, 48k), with no cutoff and with 30 kHz.
func TestStateJoinIsOneDecode(t *testing.T) {
	dir := t.TempDir()
	fx := makeFixture(dsd64, 0.6, []float64{1000, 1700}, 0.4)
	nb := len(fx.bits[0])
	cuts := []int{0, nb*3/10 + 13, nb*7/10 + 5, nb}
	whole := filepath.Join(dir, "whole.dsf")
	writeDSF(t, whole, dsd64, fx.bits, int64(len(fx.x[0])))
	var parts []string
	for k := 0; k < 3; k++ {
		p := filepath.Join(dir, fmt.Sprintf("t%d.dsf", k+1))
		writeDSF(t, p, dsd64, [][]byte{fx.bits[0][cuts[k]:cuts[k+1]], fx.bits[1][cuts[k]:cuts[k+1]]}, int64(cuts[k+1]-cuts[k])*8)
		parts = append(parts, p)
	}
	for _, tc := range []struct {
		out    int
		cutoff float64
	}{{352800, 0}, {352800, 30000}, {176400, 30000}, {88200, 0}, {96000, 30000}, {48000, 0}} {
		ref, _ := decodeOn(t, &Reader{Filename: whole, OutRate: tc.out, Cutoff: tc.cutoff}, nil, false)
		joined := make([][]float64, 2)
		var state []byte
		for k, p := range parts {
			got, next := decodeOn(t, &Reader{Filename: p, OutRate: tc.out, Cutoff: tc.cutoff}, state, k < 2)
			for c := range got {
				joined[c] = append(joined[c], got[c]...)
			}
			state = next
		}
		for c := range ref {
			if len(joined[c]) != len(ref[c]) {
				t.Fatalf("%d Hz cutoff %.0f: channel %d joined %d frames, whole %d", tc.out, tc.cutoff, c, len(joined[c]), len(ref[c]))
			}
			for i := range ref[c] {
				if joined[c][i] != ref[c][i] {
					t.Fatalf("%d Hz cutoff %.0f: channel %d frame %d: joined %v, whole %v", tc.out, tc.cutoff, c, i, joined[c][i], ref[c][i])
				}
			}
		}
		// played out: the whole decode runs past its last frame by the filters' reach, to silence
		r := &Reader{Filename: whole, OutRate: tc.out, Cutoff: tc.cutoff}
		if err := r.Open(); err != nil {
			t.Fatal(err)
		}
		if int64(len(ref[0])) != r.Frames+r.Span() {
			t.Errorf("%d Hz: played out to %d frames, Frames %d + Span %d", tc.out, len(ref[0]), r.Frames, r.Span())
		}
		r.Close()
	}
}

// TestStateJoinDSD128: at DSD128 stage 1 takes two bytes an output, so a file cut at an odd byte leaves it half way:
// its phase goes on with the next file too.
func TestStateJoinDSD128(t *testing.T) {
	dir := t.TempDir()
	fx := makeFixture(2*dsd64, 0.3, []float64{1000, 1700}, 0.4)
	nb := len(fx.bits[0])
	cuts := []int{0, nb/3 | 1, nb*2/3 | 1, nb}
	whole := filepath.Join(dir, "whole.dsf")
	writeDSF(t, whole, 2*dsd64, fx.bits, int64(len(fx.x[0])))
	ref, _ := decodeOn(t, &Reader{Filename: whole, OutRate: 352800, Cutoff: 30000}, nil, false)
	joined := make([][]float64, 2)
	var state []byte
	for k := 0; k < 3; k++ {
		p := filepath.Join(dir, fmt.Sprintf("t%d.dsf", k+1))
		writeDSF(t, p, 2*dsd64, [][]byte{fx.bits[0][cuts[k]:cuts[k+1]], fx.bits[1][cuts[k]:cuts[k+1]]}, int64(cuts[k+1]-cuts[k])*8)
		got, next := decodeOn(t, &Reader{Filename: p, OutRate: 352800, Cutoff: 30000}, state, k < 2)
		for c := range got {
			joined[c] = append(joined[c], got[c]...)
		}
		state = next
	}
	for c := range ref {
		if len(joined[c]) != len(ref[c]) {
			t.Fatalf("channel %d: joined %d frames, whole %d", c, len(joined[c]), len(ref[c]))
		}
		for i := range ref[c] {
			if joined[c][i] != ref[c][i] {
				t.Fatalf("channel %d frame %d: joined %v, whole %v", c, i, joined[c][i], ref[c][i])
			}
		}
	}
}

// TestStateEachFileAloneBreaksTheJoin: the control: the same files each decoded alone (as before 2026-11-05) are not the
// whole stream's decode.
func TestStateEachFileAloneBreaksTheJoin(t *testing.T) {
	dir := t.TempDir()
	fx := makeFixture(dsd64, 0.3, []float64{1000, 1700}, 0.4)
	nb := len(fx.bits[0])
	whole := filepath.Join(dir, "whole.dsf")
	writeDSF(t, whole, dsd64, fx.bits, int64(len(fx.x[0])))
	a, b := filepath.Join(dir, "a.dsf"), filepath.Join(dir, "b.dsf")
	writeDSF(t, a, dsd64, [][]byte{fx.bits[0][:nb/2], fx.bits[1][:nb/2]}, int64(nb/2)*8)
	writeDSF(t, b, dsd64, [][]byte{fx.bits[0][nb/2:], fx.bits[1][nb/2:]}, int64(nb-nb/2)*8)
	ref := decode(t, &Reader{Filename: whole, OutRate: 352800})
	x := decode(t, &Reader{Filename: a, OutRate: 352800})
	y := decode(t, &Reader{Filename: b, OutRate: 352800})
	j := append(x[0], y[0]...)
	same := len(j) == len(ref[0])
	for i := 0; same && i < len(j); i++ {
		same = j[i] == ref[0][i]
	}
	if same {
		t.Fatal("files decoded alone joined to the whole stream's decode: the control should differ")
	}
}

// TestStateRefused: a state goes only to a decode made the same way, of a whole file, after one that stopped holding.
func TestStateRefused(t *testing.T) {
	dir := t.TempDir()
	fx := makeFixture(dsd64, 0.2, []float64{1000, 1700}, 0.4)
	p := filepath.Join(dir, "a.dsf")
	writeDSF(t, p, dsd64, fx.bits, int64(len(fx.x[0])))
	_, s := decodeOn(t, &Reader{Filename: p, OutRate: 352800, Cutoff: 30000}, nil, true)
	for _, r := range []*Reader{
		{Filename: p, OutRate: 176400, Cutoff: 30000},
		{Filename: p, OutRate: 352800, Cutoff: 0},
		{Filename: p, OutRate: 352800, Cutoff: 30000, StartTime: 50 * time.Millisecond},
		{Filename: p, OutRate: 352800, Cutoff: 30000, EndTime: 150 * time.Millisecond},
	} {
		if err := r.Open(); err != nil {
			t.Fatal(err)
		}
		if err := r.SetState(s); err == nil {
			t.Errorf("%d Hz cutoff %.0f start %v end %v took the state", r.OutRate, r.Cutoff, r.StartTime, r.EndTime)
		}
		r.Close()
	}
	// a decode played out (nothing followed) leaves no state to go on from
	r := &Reader{Filename: p, OutRate: 352800, Cutoff: 30000}
	decodeOn(t, r, nil, false)
	if _, err := r.State(); err == nil {
		t.Error("a played-out decode gave a state")
	}
}

// TestStateOnlyFromAWholeFile: a decode begun at a seek (StartTime) or ending at a cue point (EndTime) hands on no state,
// even when it reaches the file's end holding: it numbers its frames from the file's start but its bytes from where it
// began, so a next file going on from it stopped short by that much (found live 2026-11-05: after a seek 25 s before
// the end of Gimme Shelter, Love in Vain played 13.5 s of its 259). And the requirement itself: whatever the last decode
// handed on, the next file decodes its whole length.
func TestStateOnlyFromAWholeFile(t *testing.T) {
	dir := t.TempDir()
	fx := makeFixture(dsd64, 0.3, []float64{1000, 1700}, 0.4)
	p, q := filepath.Join(dir, "a.dsf"), filepath.Join(dir, "b.dsf")
	writeDSF(t, p, dsd64, fx.bits, int64(len(fx.x[0])))
	writeDSF(t, q, dsd64, fx.bits, int64(len(fx.x[0])))
	whole, _ := decodeOn(t, &Reader{Filename: q, OutRate: 352800, Cutoff: 30000}, nil, false) // fresh and played out
	// the refusal's reason, as the engine logs it ("the next track's DSD decode starts afresh: ..."): live on 0.1.72.42 a
	// seek's refusal said the decode had not stopped at the file's end holding, and it had
	reason := []string{"began at 100ms", "ends at a cue point (10s)"}
	for n, first := range []*Reader{
		{Filename: p, OutRate: 352800, Cutoff: 30000, StartTime: 100 * time.Millisecond}, // a seek
		{Filename: p, OutRate: 352800, Cutoff: 30000, EndTime: 10 * time.Second},         // a cue end at the file's end
	} {
		first.EndHold = func() bool { return true }
		if err := first.Open(); err != nil {
			t.Fatal(err)
		}
		ch := make(chan [][]float64, 4)
		errc := make(chan error, 1)
		go func() { errc <- first.DecodeInput(ch); close(ch) }()
		for range ch {
		}
		if err := <-errc; err != nil {
			t.Fatalf("DecodeInput: %v", err)
		}
		state, err := first.State()
		first.Close()
		if err == nil {
			t.Errorf("start %v end %v: a decode not of the whole file handed on a state", first.StartTime, first.EndTime)
		} else if !strings.Contains(err.Error(), reason[n]) || strings.Contains(err.Error(), "did not stop") {
			t.Errorf("start %v end %v: refused for %q, want the reason %q", first.StartTime, first.EndTime, err, reason[n])
		}
		next, _ := decodeOn(t, &Reader{Filename: q, OutRate: 352800, Cutoff: 30000}, state, false)
		if len(next[0]) != len(whole[0]) {
			t.Errorf("start %v end %v: the next file decoded %d frames, its whole length %d", first.StartTime, first.EndTime, len(next[0]), len(whole[0]))
		}
	}
	// CONTROL: the same file whole, held, hands one on
	if _, s := decodeOn(t, &Reader{Filename: p, OutRate: 352800, Cutoff: 30000}, nil, true); s == nil {
		t.Error("CONTROL: the whole file held handed on no state")
	}
}
