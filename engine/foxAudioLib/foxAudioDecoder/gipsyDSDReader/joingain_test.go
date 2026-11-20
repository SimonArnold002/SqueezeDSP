package gipsyDSDReader

import (
	"fmt"
	"math"
	"path/filepath"
	"testing"
)

// TestScaleHeldJoinAtEachFilesGain (2026-11-20, a gain change at a gapless join): three files decoded each by a Reader
// going on from the last one's State, its output multiplied by its own gain (the engine's gain comes after the decode, a
// gain a channel), with ScaleHeld(last gain / this gain) after SetState: each file's bits come out at its own gain,
// exactly. Bits cannot be scaled, so the reference is the decode's own linearity: the same three files at gain 1, as
// they are (S1) and with one file's bits flipped (its +-1 turned over: S2 the second, S3 the third, S4 the first); a
// file's share is half the difference, and what no flip turns over (the DSD silence a decode starts with and plays out
// with) is at the first file's gain at the start and the last one's at the end. Through stage 1 alone (352.8k), and
// through stage 3 (96k, 48k), DSD64 and DSD128 (two bytes an output: a file cut at an odd byte). The control: with no
// ScaleHeld it is not so.
func TestScaleHeldJoinAtEachFilesGain(t *testing.T) {
	gains := [][]float64{{0.7, 0.6}, {0.25, 0.3}, {1.1, 0.9}}
	for _, rate := range []int{dsd64, dsd128} {
		dir := t.TempDir()
		fx := makeFixture(rate, 0.4, []float64{1000, 1700}, 0.4)
		nb := len(fx.bits[0])
		cuts := []int{0, nb*3/10 + 13, nb*7/10 + 5, nb}
		files := make([][2]string, 3) // as they are, flipped
		for k := 0; k < 3; k++ {
			for v, flip := range []bool{false, true} {
				p := filepath.Join(dir, fmt.Sprintf("t%d_%v.dsf", k+1, flip))
				ch := [][]byte{append([]byte(nil), fx.bits[0][cuts[k]:cuts[k+1]]...), append([]byte(nil), fx.bits[1][cuts[k]:cuts[k+1]]...)}
				if flip {
					for c := range ch {
						for i := range ch[c] {
							ch[c][i] ^= 0xFF
						}
					}
				}
				writeDSF(t, p, rate, ch, int64(cuts[k+1]-cuts[k])*8)
				files[k][v] = p
			}
		}
		for _, out := range []int{352800, 96000, 48000} {
			// joined: inv the file flipped (-1 none); g: each file's gain (nil: 1, and no ScaleHeld)
			joined := func(inv int, g [][]float64, scale bool) [][]float64 {
				res := make([][]float64, 2)
				var state []byte
				for k := 0; k < 3; k++ {
					v := 0
					if k == inv {
						v = 1
					}
					r := &Reader{Filename: files[k][v], OutRate: out}
					r.EndHold = func() bool { return k < 2 }
					if err := r.Open(); err != nil {
						t.Fatal(err)
					}
					if state != nil {
						if err := r.SetState(state); err != nil {
							t.Fatal(err)
						}
						if scale {
							r.ScaleHeld([]float64{g[k-1][0] / g[k][0], g[k-1][1] / g[k][1]})
						}
					}
					chn := make(chan [][]float64, 4)
					errc := make(chan error, 1)
					go func() { errc <- r.DecodeInput(chn); close(chn) }()
					for b := range chn {
						for c := range res {
							for _, s := range b[c] {
								if g != nil {
									s *= g[k][c]
								}
								res[c] = append(res[c], s)
							}
						}
					}
					if err := <-errc; err != nil {
						t.Fatal(err)
					}
					if k < 2 {
						var err error
						if state, err = r.State(); err != nil {
							t.Fatal(err)
						}
					}
					r.Close()
				}
				return res
			}
			s1, s2, s3, s4 := joined(-1, nil, false), joined(1, nil, false), joined(2, nil, false), joined(0, nil, false)
			for _, scale := range []bool{true, false} {
				j := joined(-1, gains, scale)
				worst := 0.0
				for c := range j {
					if len(j[c]) != len(s1[c]) || len(s2[c]) != len(s1[c]) || len(s3[c]) != len(s1[c]) || len(s4[c]) != len(s1[c]) {
						t.Fatalf("DSD %d to %d: lengths %d %d %d %d %d", rate, out, len(j[c]), len(s1[c]), len(s2[c]), len(s3[c]), len(s4[c]))
					}
					half := len(j[c]) / 2
					for i := range j[c] {
						pa, pb, pc := (s1[c][i]-s4[c][i])/2, (s1[c][i]-s2[c][i])/2, (s1[c][i]-s3[c][i])/2
						rest := s1[c][i] - pa - pb - pc
						gr := gains[0][c]
						if i >= half {
							gr = gains[2][c]
						}
						want := gains[0][c]*pa + gains[1][c]*pb + gains[2][c]*pc + gr*rest
						worst = math.Max(worst, math.Abs(j[c][i]-want))
					}
				}
				if scale && worst > 1e-9 {
					t.Errorf("DSD %d to %d: scaled, %.3g from each file's bits at its own gain", rate, out, worst)
				}
				if !scale && worst < 1e-3 {
					t.Errorf("DSD %d to %d: the control (no ScaleHeld) is %.3g from it: the test cannot see the fault", rate, out, worst)
				}
			}
		}
	}
}
