package foxSpeed

import (
	"math"
	"sync"
	"testing"
	"time"
)

// clock is a time the test moves.
type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// The fill: windows of 2 s of audio, each joining it while the reader takes everything; the first window the reader
// holds back ends it and the figures stay those before it; later writes change nothing.
func TestFill(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	f := &Fill{Now: c.now}
	began := c.t
	for range 8 { // flat out: 0.5 s of audio every 0.1 s, each write waiting 1 ms: 4 s in two windows
		c.add(100 * time.Millisecond)
		f.Wrote(0.5, began, time.Millisecond)
	}
	if a, b, over := f.Figures(); a != 4 || !near(b, 0.8-0.008) || over {
		t.Fatalf("flat out: audio %v busy %v over %v, want 4 s in 0.792 s, not over", a, b, over)
	}
	for range 4 { // the player's buffer full: each write waits 0.4 of its 0.5 s
		c.add(500 * time.Millisecond)
		f.Wrote(0.5, began, 400*time.Millisecond)
	}
	if a, b, over := f.Figures(); a != 4 || !near(b, 0.792) || !over {
		t.Fatalf("held back: audio %v busy %v over %v, want the 4 s and 0.792 s before it, over", a, b, over)
	}
	c.add(10 * time.Second) // long after, a clean write: still nothing (the held window's waits now a small share)
	f.Wrote(2, began, 0)
	if a, _, _ := f.Figures(); a != 4 {
		t.Fatalf("a write after the fill counted: %v", a)
	}
}

// A wait the reader's own pauses explain (50 ms or less a window) or a small share of a window ends nothing.
func TestFillShortWaitsDoNotEndIt(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	f := &Fill{Now: c.now}
	began := c.t
	for range 4 { // 40 ms of waits in a 100 ms window: 40% of it, but under 50 ms
		c.add(25 * time.Millisecond)
		f.Wrote(0.5, began, 10*time.Millisecond)
	}
	for range 4 { // 60 ms of waits in 1 s: over 50 ms, but 6%
		c.add(250 * time.Millisecond)
		f.Wrote(0.5, began, 15*time.Millisecond)
	}
	if a, b, over := f.Figures(); a != 4 || !near(b, 1.1-0.1) || over {
		t.Fatalf("audio %v busy %v over %v, want 4 s in 1.0 s, not over", a, b, over)
	}
	for range 4 { // 300 ms of 1 s: the reader holds it back
		c.add(250 * time.Millisecond)
		f.Wrote(0.5, began, 75*time.Millisecond)
	}
	if _, _, over := f.Figures(); !over {
		t.Fatal("a window held back 30% of its time did not end the fill")
	}
}

// A stream that ends inside a window: its part counts if clean, not if held back; nothing written, nothing counted.
func TestFillEnd(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	f := &Fill{Now: c.now}
	for range 6 { // 3 s: one window and half another
		c.add(100 * time.Millisecond)
		f.Wrote(0.5, c.t.Add(-100*time.Millisecond), 0)
	}
	f.End()
	if a, b, over := f.Figures(); a != 3 || !near(b, 0.6) || !over {
		t.Fatalf("ended: audio %v busy %v over %v, want 3 s in 0.6 s, over", a, b, over)
	}
	g := &Fill{Now: c.now}
	for range 4 {
		c.add(100 * time.Millisecond)
		g.Wrote(0.5, c.t.Add(-100*time.Millisecond), 0)
	}
	c.add(time.Second)
	g.Wrote(0.5, c.t, 900*time.Millisecond) // the last part held back
	g.End()
	if a, _, _ := g.Figures(); a != 2 {
		t.Fatalf("a held-back last part counted: %v", a)
	}
	h := &Fill{Now: c.now}
	h.End()
	if a, b, over := h.Figures(); a != 0 || b != 0 || !over {
		t.Fatalf("nothing written: %v %v %v", a, b, over)
	}
	var none *Fill
	none.Wrote(1, c.t, 0)
	none.End()
	if a, _, _ := none.Figures(); a != 0 {
		t.Fatal("a nil fill counted")
	}
}

func TestMeterFigures(t *testing.T) {
	m := &Meter{Name: "x", Rate: 1000}
	m.Add(1500, 300*time.Millisecond)
	m.Add(500, 200*time.Millisecond)
	if a, b := m.Figures(); a != 2 || b != 0.5 {
		t.Fatalf("2000 frames at 1000 a second in 0.5 s: audio %v busy %v", a, b)
	}
	var nilM *Meter
	nilM.Add(10, time.Second) // counts nothing, no panic
	if a, b := nilM.Figures(); a != 0 || b != 0 {
		t.Fatalf("a nil meter: %v %v", a, b)
	}
	if a, b := (&Meter{}).Figures(); a != 0 || b != 0 {
		t.Fatalf("no rate: %v %v", a, b)
	}
}

func TestMeterConcurrent(t *testing.T) {
	m := &Meter{Rate: 100}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 1000 {
				m.Add(1, time.Millisecond)
				m.Figures()
			}
		}()
	}
	wg.Wait()
	if a, b := m.Figures(); a != 80 || b != 8 {
		t.Fatalf("8000 adds: audio %v busy %v", a, b)
	}
}

func TestSlowest(t *testing.T) {
	dec := &Meter{Name: "decoder", Rate: 1000}
	conv := &Meter{Name: "convolver 1", Rate: 1000}
	enc := &Meter{Name: "encoder", Rate: 1000}
	dec.Add(10000, 100*time.Millisecond) // 100x
	conv.Add(8000, 2*time.Second)        // 4x: the slowest
	if _, _, _, ok := Slowest(dec, conv, enc); ok {
		t.Fatal("a stage that has counted nothing yet could be the slow one: no figure")
	}
	enc.Add(6000, time.Second) // 6x
	name, a, b, ok := Slowest(dec, conv, enc)
	if !ok || name != "convolver 1" || a != 8 || b != 2 {
		t.Fatalf("the slowest is the convolver, 8 s of audio in 2 s: %q %v %v %v", name, a, b, ok)
	}
	// a stage that only passes its input along (no work) is never the slowest
	pass := &Meter{Name: "pass", Rate: 1000}
	pass.Add(9000, 0)
	if name, _, _, _ := Slowest(pass, dec, conv, enc); name != "convolver 1" {
		t.Fatalf("a stage with no work taken as the slowest: %q", name)
	}
	if _, _, _, ok := Slowest(pass); ok {
		t.Fatal("no work counted anywhere: no figure")
	}
	// a nil meter (a stage the route does not have) is left out
	if name, _, _, ok := Slowest(nil, enc, nil); !ok || name != "encoder" {
		t.Fatalf("nil meters: %q %v", name, ok)
	}
	if _, _, _, ok := Slowest(); ok {
		t.Fatal("no meters: no figure")
	}
}
