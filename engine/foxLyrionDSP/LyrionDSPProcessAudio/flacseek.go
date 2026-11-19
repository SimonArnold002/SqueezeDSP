// SqueezeDSP fork (THE EXACT LENGTH ROUND, step 4; docs/exact-length-plan.md part C): a remote FLAC after a seek or a
// resume (Qobuz) reaches flac as frames only: LMS asks the server for the bytes from the seek's offset and drops what
// comes before the first frame header it trusts (Slim::Formats::FLAC::frameAlign, LMS 9.2), with no STREAMINFO, so
// nothing in the pipe says how many frames are left. The stream URL itself does (measured 2026-11-15 on a signed Qobuz
// URL, t/length/flachead.py and flacframe.py): its head holds STREAMINFO's total, and the frame LMS starts at holds its
// own number. --flacseek=OFFSET (the byte LMS's request starts at) has the engine read both, two small ranged reads of
// --trackurl at its start, and states total less that frame's first sample: the frames flac will hand on, exactly.
// Any failure (no answer in time, no STREAMINFO, a total of 0) leaves the count unknown, as before.
package LyrionDSPProcessAudio

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// flacSeekWait: the most the stream's start waits for the two reads (they run beside the filters' set-up)
const flacSeekWait = 4 * time.Second

// headSpan: the bytes the head read asks for (STREAMINFO follows fLaC, a few hundred bytes in); frameSpan: the read at
// the offset, which must reach the next frame header: a frame is at most its block uncompressed (4608 frames of 24-bit
// stereo, 27 KB; 16384, the largest a streaming encoder uses, 96 KB)
const (
	headSpan  = 65536
	frameSpan = 262144
)

type streamInfo struct {
	total               int64 // samples a channel; 0: not known
	minBlock, maxBlock  int
	rate, channels, bps int
}

// rangeRead: up to n bytes of url from byte from (a 206, or a 200 read from its start)
func rangeRead(ctx context.Context, client *http.Client, url string, from, n int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", from, from+n-1))
	req.Header.Set("User-Agent", "SqueezeDSP")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusPartialContent:
	case resp.StatusCode == http.StatusOK && from == 0:
	default:
		return nil, fmt.Errorf("%s: HTTP %d for bytes %d-", url, resp.StatusCode, from)
	}
	return io.ReadAll(io.LimitReader(resp.Body, n))
}

// parseStreamInfo: a FLAC file's head (an ID3v2 tag before it allowed) to its STREAMINFO
func parseStreamInfo(b []byte) (streamInfo, error) {
	var si streamInfo
	if len(b) >= 10 && string(b[:3]) == "ID3" {
		size := int(b[6]&0x7f)<<21 | int(b[7]&0x7f)<<14 | int(b[8]&0x7f)<<7 | int(b[9]&0x7f)
		skip := 10 + size
		if b[5]&0x10 != 0 { // a footer
			skip += 10
		}
		if skip > len(b) {
			return si, errors.New("FLAC: an ID3v2 tag longer than the head read")
		}
		b = b[skip:]
	}
	if len(b) < 4+4+34 || string(b[:4]) != "fLaC" {
		return si, errors.New("FLAC: no fLaC at the head")
	}
	if b[4]&0x7f != 0 { // the first block is STREAMINFO
		return si, errors.New("FLAC: the first metadata block is not STREAMINFO")
	}
	m := b[8:]
	si.minBlock = int(binary.BigEndian.Uint16(m[0:]))
	si.maxBlock = int(binary.BigEndian.Uint16(m[2:]))
	x := binary.BigEndian.Uint64(m[10:])
	si.rate = int(x >> 44)
	si.channels = int(x>>41&7) + 1
	si.bps = int(x>>36&31) + 1
	si.total = int64(x & (1<<36 - 1))
	return si, nil
}

var crc8Table = func() [256]byte {
	var t [256]byte
	for i := range t {
		c := byte(i)
		for k := 0; k < 8; k++ {
			if c&0x80 != 0 {
				c = c<<1 ^ 0x07
			} else {
				c <<= 1
			}
		}
		t[i] = c
	}
	return t
}()

func crc8(b []byte) byte {
	var c byte
	for _, v := range b {
		c = crc8Table[c^v]
	}
	return c
}

// alignedFrame: where LMS's frameAlign starts the stream in b (the bytes from the seek's offset), and that frame
// header's coded number and blocking strategy. It is LMS's own search, byte for byte: the sync and reserved bit, the
// four field checks, and a CRC-8 found where frameAlign looks for it, which for block-size codes 6 and 7 is not where
// the spec puts it (frameAlign adds 2 bytes for code 6 and 1 for 7, the spec's 1 and 2), so LMS passes those frames by
// and starts at a later one; so does this. It needs 33 bytes from a candidate on (frameAlign's "length > 32").
func alignedFrame(b []byte) (at int, number int64, variable bool, err error) {
	if len(b) >= 4 && string(b[:4]) == "fLaC" {
		return 0, 0, false, nil // frameAlign: "found STREAMINFO header", the stream from its start
	}
	for i := 0; i+33 <= len(b); i++ {
		if b[i] != 0xff {
			continue
		}
		tag := binary.BigEndian.Uint32(b[i:])
		if tag&0xfff80000 != 0xfff80000 {
			continue
		}
		if tag>>12&0x0f == 0 || tag>>8&0x0f == 0x0f || tag>>4&0x0f >= 11 || tag>>1&0x07 == 0x03 || tag>>1&0x07 == 0x07 {
			continue
		}
		// frameAlign's offsets count from the byte after the 0xff (its buffer has dropped it)
		in := b[i+1:]
		off := 4
		for v := in[3]; v&0x80 != 0; off++ {
			v <<= 1
		}
		if off > 4 {
			off--
		}
		switch tag >> 12 & 0x0f {
		case 6:
			off += 2
		case 7:
			off += 1
		}
		switch sr := tag >> 8 & 0x0f; {
		case sr == 12:
			off += 1
		case sr > 12 && sr < 15:
			off += 2
		}
		if off >= len(in) {
			continue
		}
		if crc8(append(binary.BigEndian.AppendUint32(nil, tag), in[3:off]...)) != in[off] {
			continue
		}
		// the coded number (FLAC's UTF-8-like form) from the header's fifth byte
		n, ok := codedNumber(b[i+4:])
		if !ok {
			return 0, 0, false, fmt.Errorf("FLAC: the frame header at byte %d has a bad coded number", i)
		}
		return i, n, tag>>16&1 == 1, nil
	}
	return 0, 0, false, errors.New("FLAC: no frame header LMS would start at in the bytes read")
}

// codedNumber: FLAC's frame or sample number (UTF-8's scheme, to 36 bits)
func codedNumber(b []byte) (int64, bool) {
	if len(b) == 0 {
		return 0, false
	}
	c := b[0]
	n := 0
	for c&0x80 != 0 {
		n++
		c <<= 1
	}
	switch {
	case n == 0:
		return int64(b[0]), true
	case n == 1 || n > 7 || len(b) < n:
		return 0, false
	}
	v := int64(b[0] & (0xff >> (n + 1)))
	for k := 1; k < n; k++ {
		if b[k]&0xc0 != 0x80 {
			return 0, false
		}
		v = v<<6 | int64(b[k]&0x3f)
	}
	return v, true
}

// remoteFlacCount: the frames a remote FLAC's stream carries from byte offset (LMS's request's first byte) to its end.
func remoteFlacCount(ctx context.Context, client *http.Client, url string, offset int64) (int64, error) {
	type res struct {
		b   []byte
		err error
	}
	head, at := make(chan res, 1), make(chan res, 1)
	go func() { b, err := rangeRead(ctx, client, url, 0, headSpan); head <- res{b, err} }()
	if offset > 0 {
		go func() { b, err := rangeRead(ctx, client, url, offset, frameSpan); at <- res{b, err} }()
	}
	h := <-head
	if h.err != nil {
		return 0, h.err
	}
	si, err := parseStreamInfo(h.b)
	if err != nil {
		return 0, err
	}
	if si.total <= 0 {
		return 0, errors.New("FLAC: STREAMINFO states no total")
	}
	if offset == 0 {
		return si.total, nil
	}
	a := <-at
	if a.err != nil {
		return 0, a.err
	}
	_, number, variable, err := alignedFrame(a.b)
	if err != nil {
		return 0, err
	}
	pos := number
	if !variable {
		if si.minBlock != si.maxBlock && si.minBlock != 0 { // a fixed-blocksize stream states one size
			return 0, fmt.Errorf("FLAC: a fixed-blocksize stream whose STREAMINFO states blocks %d to %d", si.minBlock, si.maxBlock)
		}
		pos = number * int64(si.maxBlock)
	}
	if pos > si.total {
		return 0, fmt.Errorf("FLAC: the first frame starts at sample %d, past the total %d", pos, si.total)
	}
	return si.total - pos, nil
}

// remoteFlacFormat: a remote FLAC's own rate, channels and bits, from the STREAMINFO at its head.
func remoteFlacFormat(ctx context.Context, client *http.Client, url string) (streamInfo, error) {
	b, err := rangeRead(ctx, client, url, 0, headSpan)
	if err != nil {
		return streamInfo{}, err
	}
	si, err := parseStreamInfo(b)
	if err != nil {
		return streamInfo{}, err
	}
	if si.rate <= 0 || si.channels <= 0 || si.bps <= 0 {
		return streamInfo{}, fmt.Errorf("FLAC: STREAMINFO states %d Hz, %d channels, %d bits", si.rate, si.channels, si.bps)
	}
	return si, nil
}

// THE RAW INPUT'S FORMAT FROM THE REMOTE FLAC ITSELF (2026-11-16, found live twice: the A8 13:20, the Sonos Arc 14:43).
// The rules hand raw PCM from flac with --samplerate/--channels/--bitsin from LMS's $SAMPLERATE$ $CHANNELS$
// $SAMPLESIZE$, and LMS knows a Qobuz track's only a few seconds after its playback starts: a next track streamed
// sooner (a first track made in a second, a resume) came with 0s and the engine died ("upsample: rates 0 to 48000, 0
// channels"), an empty track. That raw stream is flac's decode of the FLAC at --trackurl, so its STREAMINFO states the
// three exactly: read from the URL's head (http or https; the read --flacseek makes). Only where a value came as 0;
// a value LMS gave is never overruled. Not read: the input as given, the error as before.
func (ap *AudioProcessor) rawFormatFromURL() {
	d := ap.Decoder
	if d.SampleRate > 0 && d.NumChannels > 0 && d.BitDepth > 0 {
		return
	}
	url := ap.Args.TrackURL
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), flacSeekWait)
	defer cancel()
	t0 := time.Now()
	si, err := remoteFlacFormat(ctx, http.DefaultClient, url)
	if err != nil {
		ap.Logger.Warn(fmt.Sprintf("%s: the raw input came as %d Hz, %d channels, %d bits; the stream's own format not read (%v, %d ms)",
			packageName, d.SampleRate, d.NumChannels, d.BitDepth, err, time.Since(t0).Milliseconds()))
		return
	}
	ap.Logger.Info(fmt.Sprintf("%s: the raw input came as %d Hz, %d channels, %d bits (LMS knew no more yet): the remote FLAC's own, %d Hz, %d channels, %d bits (read in %d ms)",
		packageName, d.SampleRate, d.NumChannels, d.BitDepth, si.rate, si.channels, si.bps, time.Since(t0).Milliseconds()))
	if d.SampleRate <= 0 {
		d.SampleRate = si.rate
	}
	if d.NumChannels <= 0 {
		d.NumChannels = si.channels
	}
	if d.BitDepth <= 0 {
		d.BitDepth = si.bps
	}
}

// flacSeekStart (Initialize, at once): the two reads, beside the rest of the set-up; flacSeekCount waits for them.
func (ap *AudioProcessor) flacSeekStart() {
	if !ap.Args.FlacSeekSet || ap.Args.TrackURL == "" {
		return
	}
	ch := make(chan flacSeekResult, 1)
	ap.flacSeek = ch
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), flacSeekWait)
		defer cancel()
		t0 := time.Now()
		n, err := remoteFlacCount(ctx, http.DefaultClient, ap.Args.TrackURL, ap.Args.FlacSeek)
		ch <- flacSeekResult{n, err, time.Since(t0)}
	}()
}

type flacSeekResult struct {
	frames int64
	err    error
	took   time.Duration
}

// flacSeekCount: the frames the remote FLAC carries from LMS's offset, or 0 (not known)
func (ap *AudioProcessor) flacSeekCount() int64 {
	if ap.flacSeek == nil {
		return 0
	}
	r := <-ap.flacSeek
	ap.flacSeek = nil
	if r.err != nil {
		ap.Logger.Info(fmt.Sprintf("%s: stream length: the remote FLAC's count not read (%v, %d ms): not known", packageName, r.err, r.took.Milliseconds()))
		return 0
	}
	ap.Logger.Info(fmt.Sprintf("%s: stream length: the remote FLAC carries %d frames from byte %d (read in %d ms)", packageName, r.frames, ap.Args.FlacSeek, r.took.Milliseconds()))
	return r.frames
}
