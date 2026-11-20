package gipsyUpsampler

import (
	"math"
	"math/rand"
	"testing"
)

// TestScaleJoinAtEachTracksGain (2026-11-20, a gain change at a gapless join): a stream cut into three tracks, each
// through a chain of its own going on from the last one's State, its output multiplied by its own gain (the engine's
// gain comes after the chain, a gain a channel), with Scale(last gain / this gain) after SetState: is the whole stream
// with each track's input at its own gain through one chain, within rounding, at every kind of pair. The control: with
// no Scale it is not (the last track's held input comes out at the next track's gain).
func TestScaleJoinAtEachTracksGain(t *testing.T) {
	gains := [][]float64{{0.7, 0.6}, {0.25, 0.3}, {1.1, 0.9}}
	pairs := [][2]int{{44100, 352800}, {96000, 384000}, {44100, 48000}, {48000, 44100}, {192000, 96000}}
	for _, pr := range pairs {
		for _, c := range []Choice{{Phase: Intermediate}, {Length: Long, Phase: Linear}, {Phase: Minimum}, {Family: NonRinging}} {
			rng := rand.New(rand.NewSource(int64(pr[0] + pr[1])))
			n := pr[0]/2 + 777
			x := [][]float64{make([]float64, n), make([]float64, n)}
			for i := 0; i < n; i++ {
				x[0][i], x[1][i] = rng.Float64()*2-1, rng.Float64()*2-1
			}
			cuts := []int{0, n * 3 / 10, n*7/10 + 1, n}
			xg := [][]float64{make([]float64, n), make([]float64, n)}
			for k := 0; k < 3; k++ {
				for ch := range x {
					for i := cuts[k]; i < cuts[k+1]; i++ {
						xg[ch][i] = x[ch][i] * gains[k][ch]
					}
				}
			}
			whole := run1(t, pr[0], pr[1], c, xg, 4096)
			for _, scale := range []bool{true, false} {
				joined := [][]float64{nil, nil}
				var state []byte
				for k := 0; k < 3; k++ {
					u, err := New(pr[0], pr[1], 2, c, c)
					if err != nil {
						t.Fatal(err)
					}
					if state != nil {
						if err := u.SetState(state); err != nil {
							t.Fatal(err)
						}
						if scale {
							u.Scale([]float64{gains[k-1][0] / gains[k][0], gains[k-1][1] / gains[k][1]})
						}
					}
					got := feed(u, [][]float64{x[0][cuts[k]:cuts[k+1]], x[1][cuts[k]:cuts[k+1]]}, 1000+333*k, k == 2)
					for ch := range got {
						for _, v := range got[ch] {
							joined[ch] = append(joined[ch], v*gains[k][ch])
						}
					}
					if k < 2 {
						if state, err = u.State(); err != nil {
							t.Fatal(err)
						}
					}
				}
				worst := 0.0
				for ch := range whole {
					if len(joined[ch]) != len(whole[ch]) {
						t.Fatalf("%d to %d %s: channel %d joined %d frames, whole %d", pr[0], pr[1], c, ch, len(joined[ch]), len(whole[ch]))
					}
					for i := range whole[ch] {
						worst = math.Max(worst, math.Abs(joined[ch][i]-whole[ch][i]))
					}
				}
				if scale && worst > 1e-12 {
					t.Errorf("%d to %d %s: scaled, %.3g from the whole stream at each track's gain", pr[0], pr[1], c, worst)
				}
				if !scale && worst < 1e-3 {
					t.Errorf("%d to %d %s: the control (no Scale) is %.3g from it: the test cannot see the fault", pr[0], pr[1], c, worst)
				}
			}
		}
	}
}
