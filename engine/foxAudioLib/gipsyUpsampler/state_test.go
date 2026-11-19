package gipsyUpsampler

import (
	"math/rand"
	"testing"
)

// run1: one chain over the whole stream, in blocks of bs, then Flush
func run1(t *testing.T, in, out int, c Choice, x [][]float64, bs int) [][]float64 {
	t.Helper()
	u, err := New(in, out, len(x), c, c)
	if err != nil {
		t.Fatal(err)
	}
	return feed(u, x, bs, true)
}

func feed(u *Upsampler, x [][]float64, bs int, flush bool) [][]float64 {
	res := make([][]float64, len(x))
	for o := 0; o < len(x[0]); o += bs {
		blk := make([][]float64, len(x))
		for ch := range x {
			blk[ch] = x[ch][o:min(o+bs, len(x[ch]))]
		}
		for ch, v := range u.Process(blk) {
			res[ch] = append(res[ch], v...)
		}
	}
	if flush {
		for ch, v := range u.Flush() {
			res[ch] = append(res[ch], v...)
		}
	}
	return res
}

// TestStateJoinIsOneStream: a stream cut into three tracks, each through a chain of its own that goes on from the last
// one's State (as the engine runs one a track), the last ending with Flush, is the whole stream through one chain, to
// the bit, at every kind of pair (x2, rational up, under 2x, down), Kaiser's lengths and the other families; the
// blocks differ on purpose.
func TestStateJoinIsOneStream(t *testing.T) {
	pairs := [][2]int{{44100, 352800}, {44100, 705600}, {96000, 384000}, {96000, 705600}, {44100, 48000},
		{48000, 44100}, {192000, 96000}, {176400, 352800}}
	for _, pr := range pairs {
		for _, c := range []Choice{{Phase: Intermediate}, {Length: Long, Phase: Linear}, {Phase: Minimum},
			{Family: Gaussian, Length: Medium, Phase: Intermediate}, {Family: Slow}, {Family: NonRinging}, {Phase: Int80},
			{Family: Gaussian, Phase: Int70}} {
			rng := rand.New(rand.NewSource(int64(pr[0] + pr[1])))
			n := pr[0]/2 + 777
			x := [][]float64{make([]float64, n), make([]float64, n)}
			for i := 0; i < n; i++ {
				x[0][i], x[1][i] = rng.Float64()*2-1, rng.Float64()*2-1
			}
			whole := run1(t, pr[0], pr[1], c, x, 4096)
			cuts := []int{0, n * 3 / 10, n*7/10 + 1, n}
			var joined [][]float64 = [][]float64{nil, nil}
			var state []byte
			for k := 0; k < 3; k++ {
				u, err := New(pr[0], pr[1], 2, c, c)
				if err != nil {
					t.Fatal(err)
				}
				if state != nil {
					if err := u.SetState(state); err != nil {
						t.Fatalf("%d to %d %s: %v", pr[0], pr[1], c, err)
					}
				}
				part := [][]float64{x[0][cuts[k]:cuts[k+1]], x[1][cuts[k]:cuts[k+1]]}
				got := feed(u, part, 1000+333*k, k == 2)
				for ch := range got {
					joined[ch] = append(joined[ch], got[ch]...)
				}
				if k < 2 {
					if state, err = u.State(); err != nil {
						t.Fatal(err)
					}
				}
			}
			for ch := range whole {
				if len(joined[ch]) != len(whole[ch]) {
					t.Fatalf("%d to %d %s: channel %d joined %d frames, whole %d", pr[0], pr[1], c, ch, len(joined[ch]), len(whole[ch]))
				}
				for i := range whole[ch] {
					if joined[ch][i] != whole[ch][i] {
						t.Fatalf("%d to %d %s: channel %d frame %d: joined %v, whole %v", pr[0], pr[1], c, ch, i, joined[ch][i], whole[ch][i])
					}
				}
			}
		}
	}
}

// TestStateEachTrackAloneBreaksTheJoin: the control: the same tracks each through a fresh chain of their own (Flush at
// each end, as before 2026-11-05) are NOT the whole stream (the join the round fixes).
func TestStateEachTrackAloneBreaksTheJoin(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	n := 30000
	x := [][]float64{make([]float64, n)}
	for i := range x[0] {
		x[0][i] = rng.Float64()*2 - 1
	}
	c := Choice{Phase: Intermediate}
	whole := run1(t, 44100, 352800, c, x, 4096)
	a := run1(t, 44100, 352800, c, [][]float64{x[0][:n/2]}, 4096)
	b := run1(t, 44100, 352800, c, [][]float64{x[0][n/2:]}, 4096)
	j := append(a[0], b[0]...)
	same := len(j) == len(whole[0])
	for i := 0; same && i < len(j); i++ {
		same = j[i] == whole[0][i]
	}
	if same {
		t.Fatal("tracks resampled alone joined exactly: the control should differ")
	}
}

// TestStateRefusesAnotherChain: a state goes only to a chain made the same way.
func TestStateRefusesAnotherChain(t *testing.T) {
	c := Choice{Phase: Intermediate}
	u, _ := New(44100, 352800, 2, c, c)
	feed(u, [][]float64{make([]float64, 5000), make([]float64, 5000)}, 4096, false)
	s, err := u.State()
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []struct {
		in, out, ch int
		c           Choice
	}{{44100, 352800, 2, Choice{Length: Long, Phase: Linear}}, {44100, 352800, 2, Choice{Family: Gaussian, Phase: Intermediate}},
		{48000, 384000, 2, c}, {44100, 705600, 2, c}, {44100, 352800, 1, c}} {
		w, _ := New(v.in, v.out, v.ch, v.c, v.c)
		if err := w.SetState(s); err == nil {
			t.Errorf("%d to %d, %d channels, %s took a 44.1k to 352.8k stereo short-int state", v.in, v.out, v.ch, v.c)
		}
		if w.drop != int(w.Delay+0.5) && w.stages != nil {
			t.Errorf("%d to %d: a refused state changed the chain (drop %d)", v.in, v.out, w.drop)
		}
	}
	w, _ := New(44100, 352800, 2, c, c)
	if err := w.SetState(s); err != nil {
		t.Errorf("the same chain refused its state: %v", err)
	}
	if err := w.SetState([]byte("not gob")); err == nil {
		t.Error("a broken state was taken")
	}
}
