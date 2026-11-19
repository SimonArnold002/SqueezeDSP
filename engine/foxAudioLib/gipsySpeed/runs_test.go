package gipsySpeed

import (
	"bytes"
	"errors"
	"sync"
	"testing"
	"time"
)

// pipeReader: a reader that takes what it is let take (allow), at most per bytes a write, recording what it took.
type pipeReader struct {
	mu      sync.Mutex
	cond    *sync.Cond
	allowed int
	got     bytes.Buffer
	err     error
}

func newPipeReader() *pipeReader {
	p := &pipeReader{}
	p.cond = sync.NewCond(&p.mu)
	return p
}

func (p *pipeReader) allow(n int) {
	p.mu.Lock()
	p.allowed += n
	p.cond.Broadcast()
	p.mu.Unlock()
}

func (p *pipeReader) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return 0, p.err
	}
	for p.allowed < len(b) {
		p.cond.Wait()
	}
	p.allowed -= len(b)
	p.got.Write(b)
	return len(b), nil
}

func (p *pipeReader) taken() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.got.Len()
}

// isHeld: whether the output waits now, read as the lead's goroutines write it.
func (r *Runs) isHeld() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.held
}

// The lead: every byte reaches the reader, in order, the writer's own buffer reused; a write never waits until the high
// mark is held, then waits (one Hold) until the reader has taken it down to the low mark (one Free); Close waits until
// the reader has taken it all.
func TestLead(t *testing.T) {
	rd := newPipeReader()
	runs := &Runs{}
	l := NewLead(rd, 100, 1, 2, 10, runs) // 100 bytes a second, nothing in the pipeline: from 100 to 300 bytes
	var want bytes.Buffer
	buf := make([]byte, 50)
	write := func(i int) {
		for k := range buf {
			buf[k] = byte(i*50 + k)
		}
		want.Write(buf)
		if n, err := l.Write(buf); n != 50 || err != nil {
			t.Errorf("write %d: %d, %v", i, n, err)
		}
	}
	for i := range 6 { // 300 bytes: the high mark, and nothing taken yet
		write(i)
	}
	if runs.isHeld() || rd.taken() != 0 {
		t.Fatalf("waited before the high mark: held %v, taken %d", runs.isHeld(), rd.taken())
	}
	done := make(chan struct{})
	go func() { // the 7th write waits until the reader has taken it down to 100 bytes
		write(6)
		close(done)
	}()
	time.Sleep(20 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("a write past the high mark did not wait")
	default:
	}
	rd.allow(150) // down to 150: still waiting
	time.Sleep(20 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("the write went on above the low mark")
	default:
	}
	if !runs.isHeld() {
		t.Fatal("the run's end was not told")
	}
	rd.allow(50) // down to 100: it goes on
	<-done
	if runs.isHeld() {
		t.Fatal("the next run's start was not told")
	}
	for i := 7; i < 10; i++ {
		write(i)
	}
	rd.allow(1 << 20)
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rd.got.Bytes(), want.Bytes()) {
		t.Fatalf("the reader took %d bytes, not the %d written, in order", rd.got.Len(), want.Len())
	}
}

// A write error on the output ends the lead: a waiting write returns it, as does every write after and Close.
func TestLeadWriteError(t *testing.T) {
	rd := newPipeReader()
	l := NewLead(rd, 100, 1, 1, 10, nil)
	for range 4 { // 200 bytes: full
		l.Write(make([]byte, 50))
	}
	errc := make(chan error, 1)
	go func() {
		_, err := l.Write(make([]byte, 50))
		errc <- err
	}()
	time.Sleep(20 * time.Millisecond)
	broken := errors.New("broken pipe")
	rd.mu.Lock()
	rd.err = broken
	rd.mu.Unlock()
	rd.allow(1 << 20)
	if err := <-errc; !errors.Is(err, broken) {
		t.Fatalf("the waiting write returned %v, want the output's error", err)
	}
	if _, err := l.Write(make([]byte, 1)); !errors.Is(err, broken) {
		t.Fatalf("a later write returned %v", err)
	}
	if err := l.Close(); !errors.Is(err, broken) {
		t.Fatalf("Close returned %v", err)
	}
}

// The high mark: the low one, the run, and what the pipeline holds (told in, not yet written to the lead), up to the
// most: a run outlasts what the stages after the slowest hold.
func TestLeadHigh(t *testing.T) {
	runs := &Runs{}
	l := NewLead(newPipeReader(), 100, 1, 2, 10, runs)
	defer func() { l.mu.Lock(); l.closed = true; l.cond.Broadcast(); l.mu.Unlock() }()
	if h := l.high(); h != 300 {
		t.Fatalf("nothing held: %d bytes, want 300", h)
	}
	runs.In(4, time.Now()) // 4 s in the pipeline
	if h := l.high(); h != 700 {
		t.Fatalf("4 s in the pipeline: %d bytes, want 700", h)
	}
	l.Write(make([]byte, 150)) // 1.5 s of it out
	if h := l.high(); h != 550 {
		t.Fatalf("2.5 s still in it: %d bytes, want 550", h)
	}
	runs.In(20, time.Now())
	if h := l.high(); h != 1000 {
		t.Fatalf("22.5 s in it: %d bytes, want the most, 1000", h)
	}
	if e := runs.Entered(); e != 24 {
		t.Fatalf("entered %v, want 24", e)
	}
}

// A run's high mark is set as it starts and kept: writing out what the pipeline held does not lower it (the run would
// end before the slowest stage, an early one, took in a block).
func TestLeadRunMark(t *testing.T) {
	rd := newPipeReader()
	runs := &Runs{}
	l := NewLead(rd, 100, 1, 2, 10, runs)
	for range 6 { // 300 bytes: the first run's mark (nothing in the pipeline at the start: 3 s)
		l.Write(make([]byte, 50))
	}
	runs.In(6, time.Now()) // the pipeline took in 6 s while the output waited: 3 s of it not out yet
	done := make(chan struct{})
	go func() { // waits until the reader has taken the lead down to 1 s
		l.Write(make([]byte, 50))
		close(done)
	}()
	rd.allow(200)
	<-done
	full := func() bool { l.mu.Lock(); defer l.mu.Unlock(); return l.full }
	for i := range 8 { // the run's mark: 1 s + 2 s + the 3 s then in the pipeline, 600 bytes; 150 held, 550 after these
		if full() {
			t.Fatalf("full after %d more writes, %d bytes held: the run's mark fell as its backlog went out", i, 150+50*i)
		}
		l.Write(make([]byte, 50))
	}
	l.Write(make([]byte, 50)) // 600
	if !full() {
		t.Fatal("not full at the run's mark")
	}
	rd.allow(1 << 20)
	l.Close()
}

// Runs: nothing before the output's first wait (the stream's first run, the fill's); then each run's blocks, each
// window from its first block to its last, closing at FillWindow s of audio and at the run's end; a block from before
// the run or while the output waits left out; the figure the windows of the last RunsKeep; End drops the open window.
func TestRuns(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	r := &Runs{Now: c.now}
	in := func(audio float64, d time.Duration) {
		c.add(d)
		r.In(audio, c.t)
	}
	for range 10 { // the first run, cold: not counted
		in(0.5, 50*time.Millisecond)
	}
	if a, s, n := r.Figures(); a != 0 || s != 0 || n != 0 {
		t.Fatalf("the cold run counted: %v s in %v s, %d runs", a, s, n)
	}
	r.Hold()
	before := c.t
	c.add(2 * time.Second)
	r.In(0.5, c.t) // while the output waits: not the run's
	r.Free()
	r.In(0.5, before) // a block from before the run, told late: left out
	for range 7 {     // the run: 0.5 s every 0.1 s (5x); its first block opens the window, 4 more close it at 2 s
		in(0.5, 100*time.Millisecond)
	}
	if a, s, _ := r.Figures(); a != 2 || !near(s, 0.4) {
		t.Fatalf("the first window: %v s of audio in %v s, want 2 in 0.4", a, s)
	}
	r.Hold() // the next window (from the 5th block: 2 blocks after it) closes with the run
	if a, s, n := r.Figures(); a != 3 || !near(s, 0.6) || n != 1 {
		t.Fatalf("after the run: %v s in %v s, %d runs, want 3 in 0.6, 1 run", a, s, n)
	}
	c.add(20 * time.Second) // a later run, 2x: the windows more than RunsKeep before its last left out
	r.Free()
	for range 6 {
		in(0.5, 250*time.Millisecond)
	}
	r.Hold()
	if a, s, n := r.Figures(); a != 2.5 || !near(s, 1.25) || n != 2 {
		t.Fatalf("the latest run alone: %v s in %v s, %d runs, want 2.5 in 1.25, 2 runs", a, s, n)
	}
	r.Free()
	for range 3 {
		in(0.5, 250*time.Millisecond)
	}
	r.End() // the input's end: the open window dropped, nothing after counts
	in(0.5, 250*time.Millisecond)
	r.Hold()
	if a, s, n := r.Figures(); a != 2.5 || !near(s, 1.25) || n != 2 {
		t.Fatalf("after the end: %v s in %v s, %d runs, want the last run's 2.5 in 1.25", a, s, n)
	}
}

// A nil *Runs counts nothing, and a lead without one runs.
func TestRunsNil(t *testing.T) {
	var r *Runs
	r.In(1, time.Now())
	r.Hold()
	r.Free()
	r.End()
	if a, s, n := r.Figures(); a != 0 || s != 0 || n != 0 {
		t.Fatal("a nil Runs counted")
	}
}
