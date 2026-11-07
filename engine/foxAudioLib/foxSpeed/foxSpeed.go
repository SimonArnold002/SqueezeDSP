// Package: github.com/Foxenfurter/foxAudioLib/foxSpeed
// filename foxSpeed.go
//
// SqueezeDSP fork: the engine's own speed for the SqueezeDSP page.
//
// Fill: how fast the engine runs flat out, measured at a stream's start while the reader (LMS, and so the player)
// takes everything it writes, until the player's buffer is full. Exact by construction: the audio written over the
// time taken, nothing estimated. Nothing else is: once the reader paces the stream the stages before the output work
// while its writes wait, and the engine's stages, each on its own goroutine, also wait on each other and for a core,
// which no stage's own working time shows (ledger THE PAGE'S SPEED WAS PER CPU-SECOND: M3, DSD64 with a 1 s FIR,
// 3.97x flat out, the slowest stage's own time 5.43x; over the fill 3.78-4.05x). Since 0.1.72.46 the engine also runs
// flat out while it plays, in runs (runs.go: Lead, Runs), so the fill is a stream's first run's figure, no longer its
// only one.
//
// Meter: each stage (the decoder, the upsampler, each channel's convolver, the merger, the encoder) counts the audio it
// hands on and the time of its own arithmetic, its waits left out. Their slowest names the busiest stage; its figure
// is an upper bound on the engine's speed, not the speed.
package foxSpeed

import (
	"sync"
	"sync/atomic"
	"time"
)

const (
	FillWindow   = 2.0                   // seconds of audio a window of the fill holds
	fillWaitFrac = 0.25                  // a window whose writes waited for the reader more than this share of its time
	fillWaitMin  = 50 * time.Millisecond // and more than this (LMS's own pauses are shorter) ends the fill
)

// Fill measures a stream's rate while nothing holds it back, window by window of FillWindow seconds of audio written.
// A window in which the writes waited for the reader more than a quarter of its time ends the fill: the figures stay
// those of the windows before it, as from then on the reader paces the stream. Safe to read while it is written.
type Fill struct {
	Now func() time.Time // the clock; nil: time.Now

	mu          sync.Mutex
	begun, over bool
	winStart    time.Time
	winAudio    float64
	winWait     time.Duration
	audio, busy float64 // the clean windows: audio written, and their time less the waits
}

func (f *Fill) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now()
}

// Wrote counts one write: the seconds of audio it carried, when the work that made it began (the first write's begins
// the clock) and how long the write waited for the reader.
func (f *Fill) Wrote(audio float64, began time.Time, wait time.Duration) {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.over {
		return
	}
	if !f.begun {
		f.begun, f.winStart = true, began
	}
	f.winAudio += audio
	f.winWait += wait
	if f.winAudio >= FillWindow {
		f.closeWindow()
	}
}

// closeWindow ends the window now: clean, it joins the fill; held back by the reader, the fill is over.
func (f *Fill) closeWindow() {
	end := f.now()
	span := end.Sub(f.winStart)
	if f.winWait > fillWaitMin && float64(f.winWait) > fillWaitFrac*float64(span) {
		f.over = true
		return
	}
	f.audio += f.winAudio
	f.busy += (span - f.winWait).Seconds()
	f.winStart, f.winAudio, f.winWait = end, 0, 0
}

// End: the stream is over; a part window counts if it was clean.
func (f *Fill) End() {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.begun && !f.over && f.winAudio > 0 {
		f.closeWindow()
	}
	f.over = true
}

// Figures: the fill's audio (seconds) and its time with the waits left out; over once it can grow no more.
func (f *Fill) Figures() (audio, busy float64, over bool) {
	if f == nil {
		return 0, 0, false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.audio, f.busy, f.over
}

// Meter is one stage's count: the frames it has handed on, at Rate frames a second, and the time it worked on
// them. Safe to read from another goroutine while the stage runs. A nil *Meter counts nothing, so a stage
// without one runs as before.
type Meter struct {
	Name   string
	Rate   int
	frames atomic.Int64
	busy   atomic.Int64 // ns
}

// Add counts frames handed on and d of work. A stage that only passes its input along adds its frames with no
// work: it has counted, and it is never the slowest.
func (m *Meter) Add(frames int, d time.Duration) {
	if m == nil {
		return
	}
	m.frames.Add(int64(frames))
	m.busy.Add(int64(d))
}

// Figures: the audio handed on (seconds) and the time worked (seconds).
func (m *Meter) Figures() (audio, busy float64) {
	if m == nil || m.Rate <= 0 {
		return 0, 0
	}
	return float64(m.frames.Load()) / float64(m.Rate), time.Duration(m.busy.Load()).Seconds()
}

// Slowest: the stage whose own work runs fewest times real time (the busiest), its name and figures. Not ok until
// every meter has counted some audio (a stage not yet started could be the busiest) and one of them some work.
func Slowest(ms ...*Meter) (name string, audio, busy float64, ok bool) {
	best := 0.0
	for _, m := range ms {
		if m == nil {
			continue
		}
		a, b := m.Figures()
		if a <= 0 {
			return "", 0, 0, false
		}
		if b <= 0 {
			continue
		}
		if s := a / b; !ok || s < best {
			name, audio, busy, best, ok = m.Name, a, b, s, true
		}
	}
	return name, audio, busy, ok
}
