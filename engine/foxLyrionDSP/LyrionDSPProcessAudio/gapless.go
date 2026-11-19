// SqueezeDSP fork (THE GAPLESS ROUND, 2026-11-05): every stage with a memory runs across a gapless join as one stream,
// and when nothing follows every stage plays out what it holds (Simon: "We cant have gaps where filters are not
// applied. It works for HQPlayer gapless and Roon so this needs to.").
//
// LMS runs an engine a track, and an engine cannot see what follows it, so the plugin says, in a note per player that it
// writes whole and renames into place at each stream's start and at every change to the playlist (gapless_<client> in
// the temp folder): "continues=1" when LMS streams this track gaplessly after the last one (its queue holds both, and
// this one is no seek), with "prev=" the track it continues as LMS gave that track's engine; "follows=1" while a track
// follows this one; "this=" and "next=" each track's rate, kind (pcm or dsd) and channels, as LMS knows them (0 or
// empty: not known).
//
// At its start the engine takes the last track's state (the residual) only when the note says this stream continues,
// and the residual says its track held its state for it; the residual is then removed, so no later stream (a seek, a
// restart) takes it again. At the input's end the engine reads the note again (a few seconds before the track stops
// playing: the player's buffer) and decides once for every stage (endHold): a track follows at the same rate, kind and
// channels, so every stage keeps what it holds and the next track's engine goes on from it; or nothing follows (or the
// next is another kind of stream), so silence goes in after the input (tailStage) until every filter has played out its
// last, and the stream ends that much later. With no note (an engine run by hand, or a plugin before this round) the
// engine does as before.
//
// THE EXACT LENGTH ROUND, step 4 (docs/exact-length-plan.md part B): a player that needs a stream's true length before
// its first byte (UPnP Direct's renderers; its plugin declares it, the plugin's note says "exact=1") cannot have a stream
// end where the next track's arrival decides, so for it the end is decided at the stream's START, from the note as it is
// then, and the header states that stream's frames exactly (length.go). A next track removed after the start leaves the
// held tail in the residual: an engine run with --tail plays it out as a stream of its own, for the renderer to queue.
//
// Each decision goes to the log at Info (0.1.72.40; Debug before, which the rig never runs), a few lines a track, so the
// plugin's log page shows them over HTTP (Page.pm, /squeezedsp/log).
package LyrionDSPProcessAudio

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// gaplessNote is the plugin's note for a player.
type gaplessNote struct {
	ok        bool // the note was there: the plugin speaks for this stream
	continues bool
	prev      string // the track this stream continues, as LMS gave its engine (--trackURL); empty: not said
	follows   bool
	this      trackKind
	next      trackKind
	exact     bool // the player needs the true length before the first byte: the end is decided at the start
}

// trackKind: a track's rate, kind ("pcm" or "dsd") and channels, as LMS knows them; zero values: not known
type trackKind struct {
	rate     int
	kind     string
	channels int
}

func (a trackKind) sameAs(b trackKind) bool {
	return (a.rate == 0 || b.rate == 0 || a.rate == b.rate) &&
		(a.kind == "" || b.kind == "" || a.kind == b.kind) &&
		(a.channels == 0 || b.channels == 0 || a.channels == b.channels)
}

func parseKind(s string) trackKind {
	var k trackKind
	f := strings.Split(strings.TrimSpace(s), ":")
	if len(f) > 0 {
		k.rate, _ = strconv.Atoi(f[0])
	}
	if len(f) > 1 && (f[1] == "pcm" || f[1] == "dsd") {
		k.kind = f[1]
	}
	if len(f) > 2 {
		k.channels, _ = strconv.Atoi(f[2])
	}
	return k
}

func readGaplessNote(path string) gaplessNote {
	b, err := os.ReadFile(path)
	if err != nil {
		return gaplessNote{}
	}
	n := gaplessNote{ok: true}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch k {
		case "continues":
			n.continues = v == "1"
		case "prev":
			n.prev = v
		case "follows":
			n.follows = v == "1"
		case "this":
			n.this = parseKind(v)
		case "next":
			n.next = parseKind(v)
		case "exact":
			n.exact = v == "1"
		}
	}
	return n
}

// gaplessState: the engine's part, on the processor
type gaplessState struct {
	note    gaplessNote // as read at the start
	path    string
	res     *DSPResidualsCache // the last track's residual, read at the start (note mode)
	carry   bool               // the last track's state is taken: the note says this stream continues and it held
	endOnce sync.Once
	hold    bool  // at the end: every stage keeps what it holds for the next track
	tailOut int64 // frames at ProcRate the stages' tails come to at most (the silence the end plays them out with),
	// and what the last track's held state brings into this one's start at most
	tailFrame int64 // the same at the decoder's rate, the silence tailStage sends
}

// noteMode: the plugin's note governs this stream
func (ap *AudioProcessor) noteMode() bool { return ap.gapless.note.ok }

// endHold: decided once, when the first stage reaches the input's end, from the note as it is then; for a player that
// needs the true length first (the note's exact), at the stream's start from the note as it was then (lengthStart asks).
func (ap *AudioProcessor) endHold() bool {
	ap.gapless.endOnce.Do(func() {
		if !ap.noteMode() {
			ap.gapless.hold = true // as before: the convolvers keep their tails for a next track
			return
		}
		when, n := "the input's end", ap.gapless.note
		if !n.exact {
			n = readGaplessNote(ap.gapless.path)
			if !n.ok {
				n.this = ap.gapless.note.this
			}
		} else {
			when = "the stream's start (the player needs the true length first)"
		}
		// a bypassed stream (the DSP off, our rule still in place for a moment) carries nothing (SaveDSPResiduals), so
		// it never holds: its stages play out as before; nor does a held tail played out on its own (--tail)
		ap.gapless.hold = !ap.Config.Bypass && !ap.Args.Tail && n.ok && n.follows && n.next.sameAs(n.this)
		ap.Logger.Info(fmt.Sprintf("%s: %s: %s (note: follows %v, this %v, next %v)", packageName, when,
			map[bool]string{true: "a track follows, every stage keeps what it holds", false: "nothing follows: every stage plays out"}[ap.gapless.hold],
			n.follows, n.this, n.next))
	})
	return ap.gapless.hold
}

// gaplessStart (Initialize, once the decoder and any upsampler exist): reads the note, and in note mode the last track's
// residual, which it removes; whether this stream takes the last one's state.
func (ap *AudioProcessor) gaplessStart() {
	g := &ap.gapless
	g.path = filepath.Join(ap.AppSettings.TempDataFolder, "gapless_"+ap.Args.CleanClientID)
	g.note = readGaplessNote(g.path)
	if ap.Args.Tail {
		// a held tail on its own (a next track removed after a stream that held for it): it continues the residual's
		// track whatever the note says now, nothing follows it, and its length is stated
		g.note = gaplessNote{ok: true, continues: true, exact: true}
	}
	if !g.note.ok {
		return
	}
	res, err := ap.readResiduals()
	if err == nil {
		g.res = res
	} else if !os.IsNotExist(err) {
		ap.Logger.Info(packageName + ": the last track's residual not taken: " + err.Error())
	}
	os.Remove(ap.cachePath) // whatever it held is this stream's or no one's
	// the last track's state, only for the stream that continues that track: a residual another track left (one the
	// next did not take, its stream not ours) goes to no one
	g.carry = g.note.continues && g.res != nil && g.res.Held && (g.note.prev == "" || g.note.prev == g.res.TrackURL) &&
		(!ap.Args.Tail || g.res.Gate)
	ap.Logger.Info(fmt.Sprintf("%s: gapless note: continues %v, the last track %s", packageName, g.note.continues,
		map[bool]string{true: "held its state for this one: taken", false: "left nothing for this one (or this one starts afresh)"}[g.carry]))
	if g.carry && ap.Upsampler != nil && g.res.Upsampler != nil {
		if err := ap.Upsampler.SetState(g.res.Upsampler); err != nil {
			ap.Logger.Warn(packageName + ": the upsampler starts afresh: " + err.Error())
		} else {
			ap.Logger.Info(packageName + ": the upsampler goes on from the last track")
		}
	}
	// a DSD source: its decode stops at the file's end holding for a next file, and goes on from the last file's; the
	// frames it sends are then numbered over the stream, so the length the header states comes from it again
	if rd := ap.Decoder.DSDReader; rd != nil {
		rd.EndHold = ap.endHold
		if g.carry && g.res.Reader != nil {
			if err := rd.SetState(g.res.Reader); err != nil {
				ap.Logger.Info(packageName + ": the DSD decode starts afresh: " + err.Error())
			} else {
				ap.Decoder.Size = rd.Size()
				ap.Logger.Info(packageName + ": the DSD decode goes on from the last file")
			}
		}
	}
}

// gaplessTail (Initialize, once the filters are known): the most the stages' tails come to, the silence a stream that
// nothing follows ends with: the convolvers' filter, the upsampler's whole span, the channel delay, a DSD decode's
// reach (which it plays out itself, with DSD silence), and 20 ms for the crossfeed's decay and to spare.
func (ap *AudioProcessor) gaplessTail() {
	if !ap.noteMode() {
		return
	}
	out := int64(ap.ProcRate / 50)
	if len(ap.Convolvers) > 0 {
		out += int64(len(ap.Convolvers[0].FilterImpulse))
	}
	if u := ap.Upsampler; u != nil {
		out += int64(u.TapsA+u.TapsB) * int64(math.Ceil(float64(u.OutRate)/float64(u.InRate)))
	}
	if ap.Delay != nil {
		d := 0
		for _, v := range ap.Delay.Delays {
			d = max(d, v)
		}
		out += int64(d)
	}
	if rd := ap.Decoder.DSDReader; rd != nil { // its own reach, at its rate
		out += int64(math.Ceil(float64(rd.Span()) * float64(ap.ProcRate) / float64(ap.Decoder.SampleRate)))
	}
	ap.gapless.tailOut = out
	ap.gapless.tailFrame = int64(math.Ceil(float64(out)*float64(ap.Decoder.SampleRate)/float64(ap.ProcRate))) + 1
}

// tailStage: between the decoder and the rest; at the input's end, when nothing follows, the silence that plays every
// stage's tail out (note mode only).
func (ap *AudioProcessor) tailStage(in chan [][]float64, buffer int, wg *sync.WaitGroup) chan [][]float64 {
	if !ap.noteMode() {
		return in
	}
	out := make(chan [][]float64, buffer)
	wg.Add(1)
	go func() {
		defer func() {
			close(out)
			wg.Done()
		}()
		ch := ap.Decoder.NumChannels
		for blk := range in {
			if len(blk) > 0 {
				ch = len(blk)
			}
			out <- blk
		}
		if ap.endHold() {
			return
		}
		left := ap.gapless.tailFrame
		bs := int64(4096)
		for left > 0 {
			n := min(bs, left)
			z := make([][]float64, ch)
			for c := range z {
				z[c] = make([]float64, n)
			}
			out <- z
			left -= n
		}
		ap.Logger.Info(fmt.Sprintf("%s: %d frames of silence after the input: every stage played out", packageName, ap.gapless.tailFrame))
	}()
	return out
}

// readResiduals: the residual file, checked as LoadDSPResiduals checks it
func (ap *AudioProcessor) readResiduals() (*DSPResidualsCache, error) {
	cache, err := ap.loadCacheFromFile(ap.cachePath)
	if err != nil {
		return nil, err
	}
	if cache.Version != CACHE_VERSION {
		return nil, fmt.Errorf("cache version %d, want %d", cache.Version, CACHE_VERSION)
	}
	if cache.SampleRate != ap.ProcRate || cache.NumChannels != ap.Decoder.NumChannels {
		return nil, fmt.Errorf("cache at %d Hz, %d channels; this stream %d, %d", cache.SampleRate, cache.NumChannels, ap.ProcRate, ap.Decoder.NumChannels)
	}
	return cache, nil
}

// saveResiduals writes the cache whole and renames it into place, so the next engine never reads half of one.
func (ap *AudioProcessor) saveResiduals(cache *DSPResidualsCache) error {
	var b bytes.Buffer
	if err := gob.NewEncoder(&b).Encode(cache); err != nil {
		return err
	}
	tmp := ap.cachePath + "." + strconv.Itoa(os.Getpid())
	if err := os.WriteFile(tmp, b.Bytes(), 0644); err != nil {
		return err
	}
	return os.Rename(tmp, ap.cachePath)
}
