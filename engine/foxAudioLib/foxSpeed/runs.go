// Package: github.com/Foxenfurter/foxAudioLib/foxSpeed
// filename runs.go
//
// SqueezeDSP fork (0.1.72.46): the engine's speed while it plays, not only at a stream's start (ledger THE SPEED SHOWN
// IS ONE START'S FILL). Once the reader (LMS, and so the player) paces a stream nothing ran flat out again, so the fill
// stood for the whole stream, and a gapless change had none of its own.
//
// Lead keeps the engine's output ahead of the reader. Once it holds its high mark, a write waits until the reader has
// taken it down to LeadLow, and the engine then refills it flat out, in a run. Runs times each run where the audio
// ENTERS the pipeline. A run starts with every stage held (the output waited, the stages before it filled their
// channels), so the pipeline takes in a block each time its slowest stage takes one, from the run's first block on,
// whichever stage that is; at the output a run would read high, as the stages after the slowest hand on what they hold
// at once. For the same reason a run must be longer than all the pipeline holds: its high mark, set as it starts, is
// LeadLow, plus LeadRun, plus the audio in the pipeline then (taken in, not yet out: what the stages before the output
// filled while it waited), up to LeadMax. Else a run where the slowest stage is an early one is met from what the stages
// after it hold, and takes in a block or none (M3 2026-11-06, 48k to 768k with the upsampler slowest: no run measured
// with a fixed 2 s, nor with a mark that fell as the run wrote out that backlog). A stream's first run starts with nothing held: the
// fill's (Fill), measured at the output, where it is exact then. M3, 2026-11-06 (read like a player, against the same input read flat out):
// DSD256 from 44.1k 8.2-8.6x against 8.4-8.5, a FIR-bound 705.6k 33.4-36.9 against 35.5-35.7, DSD512 4.1-4.2 against
// 3.9-4.3; a load on every core showed from the next run, and at the limit the runs read 1.61-1.65 against 1.70.
package foxSpeed

import (
	"io"
	"sync"
	"time"
)

const (
	LeadLow  = 1.0              // seconds of output a run starts from
	LeadRun  = 2.0              // the seconds a run fills beyond what the pipeline holds
	LeadMax  = 15.0             // the most the lead ever holds, seconds
	RunsKeep = 10 * time.Second // the figure: the runs' windows closed within this of the latest
)

// Lead: output to w, up to its high mark held ahead of w's reader. A write waits only once the mark is reached, until
// the reader has taken the output down to the low one. w's first error ends it: every write after returns it. For one
// writer.
type Lead struct {
	w        io.Writer
	bps      float64 // bytes a second of output
	low, max int     // bytes
	run      float64 // seconds
	runs     *Runs
	written  int64 // bytes written to the lead in all
	mark     int   // this run's high mark, bytes

	mu     sync.Mutex
	cond   *sync.Cond
	queue  [][]byte
	held   int  // bytes queued
	full   bool // the high mark reached: writes wait until the low one
	closed bool
	err    error
	done   chan struct{}
}

// NewLead: a lead of output at bytesPerSecond, its runs from low seconds to low + run + what the pipeline holds (runs'
// Entered less what the lead has had), up to max; each run's end and start told to runs (nil: none, the pipeline then
// taken as holding nothing).
func NewLead(w io.Writer, bytesPerSecond, low, run, max float64, runs *Runs) *Lead {
	l := &Lead{w: w, bps: bytesPerSecond, low: int(low * bytesPerSecond), run: run, max: int(max * bytesPerSecond), runs: runs,
		done: make(chan struct{})}
	l.mark = l.high()
	l.cond = sync.NewCond(&l.mu)
	go l.drain()
	return l
}

// high: the high mark for a run starting now, in bytes.
func (l *Lead) high() int {
	in := l.runs.Entered() - float64(l.written)/l.bps
	return min(l.low+int((l.run+max(0, in))*l.bps), l.max)
}

// Write queues a copy of p, waiting first while the lead is full.
func (l *Lead) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.full && l.err == nil {
		l.runs.Hold()
		for l.full && l.err == nil {
			l.cond.Wait()
		}
		l.runs.Free()
		l.mark = l.high()
	}
	if l.err != nil {
		return 0, l.err
	}
	l.queue = append(l.queue, append([]byte(nil), p...))
	l.held += len(p)
	l.written += int64(len(p))
	if l.held >= l.mark {
		l.full = true
	}
	l.cond.Broadcast()
	return len(p), nil
}

// drain writes the queue to w as its reader takes it.
func (l *Lead) drain() {
	defer close(l.done)
	l.mu.Lock()
	defer l.mu.Unlock()
	for {
		for len(l.queue) == 0 && !l.closed {
			l.cond.Wait()
		}
		if len(l.queue) == 0 {
			return
		}
		b := l.queue[0]
		l.queue[0] = nil
		l.queue = l.queue[1:]
		l.mu.Unlock()
		_, err := l.w.Write(b)
		l.mu.Lock()
		l.held -= len(b)
		if err != nil {
			l.err = err
		}
		if l.full && (l.held <= l.low || l.err != nil) {
			l.full = false
		}
		l.cond.Broadcast()
		if l.err != nil {
			return
		}
	}
}

// Close: the output is over; waits until the reader has taken it all. w's error, if it had one.
func (l *Lead) Close() error {
	l.mu.Lock()
	l.closed = true
	l.cond.Broadcast()
	l.mu.Unlock()
	<-l.done
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.err
}

// Runs: the speed of the runs a Lead makes, from the blocks that enter the pipeline (In) and the output's waits (Hold,
// Free). A window opens at a run's first block and closes at FillWindow seconds of audio or at the run's end; its audio
// is that of the blocks after its first, its time the first one's to the last one's. Only runs after the output's first
// wait count. Safe from several goroutines; a nil *Runs counts nothing.
type Runs struct {
	Now func() time.Time // the clock; nil: time.Now

	mu       sync.Mutex
	entered  float64   // seconds of audio told in all
	warm     bool      // the output has waited: every run since starts with the stages held
	held     bool      // the output waits now
	ended    bool      // the input is over
	freed    time.Time // when this run started
	open     bool      // the window has its first block
	t0, last time.Time
	audio    float64
	runWins  int // windows this run closed
	runs     int // runs that closed one
	wins     []runWindow
}

type runWindow struct {
	end         time.Time
	audio, secs float64
}

func (r *Runs) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// In: a block of audio seconds went into the pipeline at at. A block from before this run, or one while the output
// waits, is not the run's; every block counts as entered.
func (r *Runs) In(audio float64, at time.Time) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entered += audio
	if !r.warm || r.held || r.ended || at.Before(r.freed) {
		return
	}
	if !r.open {
		r.open, r.t0, r.last, r.audio = true, at, at, 0
		return
	}
	r.audio += audio
	r.last = at
	if r.audio >= FillWindow {
		r.closeWindow()
		r.open, r.t0 = true, at // the next window from this block on
	}
}

// closeWindow: the window joins the figure if a block came after its first.
func (r *Runs) closeWindow() {
	if r.open && r.audio > 0 && r.last.After(r.t0) {
		r.wins = append(r.wins, runWindow{r.last, r.audio, r.last.Sub(r.t0).Seconds()})
		r.runWins++
		i := 0
		for i < len(r.wins) && r.last.Sub(r.wins[i].end) > RunsKeep {
			i++
		}
		r.wins = r.wins[i:]
	}
	r.open, r.audio = false, 0
}

// Hold: the output waits; the run is over.
func (r *Runs) Hold() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.held {
		return
	}
	r.closeWindow()
	if r.runWins > 0 {
		r.runs++
	}
	r.held, r.warm, r.runWins = true, true, 0
}

// Free: the output goes on; a run starts.
func (r *Runs) Free() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.held, r.open, r.audio, r.freed = false, false, 0, r.now()
}

// End: the input is over. The window open then is dropped: the pipeline only empties from there.
func (r *Runs) End() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.open, r.audio, r.ended = false, 0, true
}

// Entered: the seconds of audio told in all (In).
func (r *Runs) Entered() float64 {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.entered
}

// Figures: the audio (seconds) and the time of the windows closed within RunsKeep of the latest, and the runs measured.
func (r *Runs) Figures() (audio, secs float64, runs int) {
	if r == nil {
		return 0, 0, 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, w := range r.wins {
		audio += w.audio
		secs += w.secs
	}
	return audio, secs, r.runs
}
