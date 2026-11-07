package LyrionDSPProcessAudio

import (
	"fmt"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Foxenfurter/foxAudioLib/foxAudioDecoder"
	"github.com/Foxenfurter/foxAudioLib/foxAudioDecoder/foxDSDReader"
	"github.com/Foxenfurter/foxAudioLib/foxAudioDecoder/foxWavReader"
	"github.com/Foxenfurter/foxAudioLib/foxAudioEncoder"
	foxConvolver "github.com/Foxenfurter/foxAudioLib/foxConvolverPartition"
	"github.com/Foxenfurter/foxAudioLib/foxLog"
	"github.com/Foxenfurter/foxAudioLib/foxSpeed"
	"github.com/Foxenfurter/foxAudioLib/foxUpsampler"
	"github.com/Foxenfurter/foxLyrionDSP/LyrionDSPSettings"
)

type speedFile struct {
	Pid         int
	Stage       string
	Audio, Busy float64
	Over        bool
	Runs        int
	Stages      []stageTime
}

func readSpeed(t *testing.T, path string) (speedFile, bool) {
	t.Helper()
	var got speedFile
	b, err := os.ReadFile(path)
	if err != nil {
		return got, false
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("a torn speed file: %s", b)
	}
	return got, true
}

// The speed file (Status.pm reads it) while the engine runs: written at once with the pid alone (a server with no /proc
// knows the engine runs by its age), then each period with the busiest stage, the latest figures, whether the fill is
// over and the runs measured once there are any (ok); written whole (no temporary file left behind).
func TestReportSpeedTo(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "speed_aa_bb.json")
	var n atomic.Int64
	stop := reportSpeedTo(path, func() (speedFigures, bool) {
		k := n.Add(1)
		return speedFigures{"convolver 2", float64(k), 0.5 * float64(k), k > 4, int(k)}, k > 2
	}, nil, 10*time.Millisecond)
	defer stop()
	if got, ok := readSpeed(t, path); !ok || got.Pid != os.Getpid() || got.Stage != "" || got.Audio != 0 {
		t.Fatalf("at once the file holds %+v (ok %v), want the pid alone", got, ok)
	}
	deadline := time.Now().Add(2 * time.Second)
	for n.Load() < 6 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	got, ok := readSpeed(t, path)
	if !ok || got.Pid != os.Getpid() || got.Stage != "convolver 2" || got.Audio < 5 || got.Busy != 0.5*got.Audio || !got.Over ||
		got.Runs != int(got.Audio) {
		t.Fatalf("while running the file holds %+v (ok %v), want this pid, the stage and figures from a later period", got, ok)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 1 {
		t.Fatalf("%d files in the folder, want the speed file alone", len(ents))
	}
}

// At stop the last figures are written once more, so a run shorter than a period still leaves its speed.
func TestReportSpeedToWritesAtStop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "speed_aa_bb.json")
	var ready atomic.Bool
	stop := reportSpeedTo(path, func() (speedFigures, bool) { return speedFigures{"encoder", 42, 7, true, 3}, ready.Load() }, nil, time.Hour)
	if got, ok := readSpeed(t, path); !ok || got.Audio != 0 || got.Stage != "" {
		t.Fatalf("at once the file holds %+v (ok %v), want the pid alone", got, ok)
	}
	ready.Store(true)
	stop()
	if got, ok := readSpeed(t, path); !ok || got.Audio != 42 || got.Busy != 7 || got.Pid != os.Getpid() || got.Stage != "encoder" || !got.Over || got.Runs != 3 {
		t.Fatalf("after stop the file holds %+v (ok %v), want the last figures", got, ok)
	}
}

// Every stage of the route gets its meter, at the rate it runs at: the decoder at its own, the rest at the DSP's.
func TestAttachMeters(t *testing.T) {
	u, err := foxUpsampler.New(44100, 705600, 2, foxUpsampler.DefaultCD, foxUpsampler.DefaultHiRes)
	if err != nil {
		t.Fatal(err)
	}
	ap := &AudioProcessor{
		Decoder:    &foxAudioDecoder.AudioDecoder{SampleRate: 44100, WavDecoder: &foxWavReader.WavReader{}},
		Encoder:    &foxAudioEncoder.AudioEncoder{SampleRate: 705600},
		Convolvers: []*foxConvolver.PartitionedConvolver{{}, {}},
		Upsampler:  u,
		ProcRate:   705600,
	}
	ms := ap.attachMeters(true)
	type nr struct {
		name string
		rate int
	}
	want := []nr{{"decoder", 44100}, {"upsampler", 705600}, {"convolver 1", 705600}, {"convolver 2", 705600}, {"merger", 705600}, {"encoder", 705600}}
	if len(ms) != len(want) {
		t.Fatalf("%d meters, want %d", len(ms), len(want))
	}
	for i, m := range ms {
		if m == nil || m.Name != want[i].name || m.Rate != want[i].rate {
			t.Fatalf("meter %d: %+v, want %v", i, m, want[i])
		}
	}
	if ap.Decoder.WavDecoder.Meter != ms[0] || ap.upMeter != ms[1] || ap.Convolvers[0].Meter != ms[2] || ap.Convolvers[1].Meter != ms[3] ||
		ap.mergeMeter != ms[4] || ap.Encoder.Meter != ms[5] {
		t.Fatal("a stage does not hold the meter reported for it")
	}
	if ap.Encoder.Fill == nil {
		t.Fatal("the encoder has no fill: the engine would report no speed")
	}
	// bypass: no convolvers, no merger
	ap.Convolvers[0].Meter, ap.Convolvers[1].Meter = nil, nil
	ms = ap.attachMeters(false)
	if len(ms) != 3 || ms[0].Name != "decoder" || ms[1].Name != "upsampler" || ms[2].Name != "encoder" || ap.mergeMeter != nil ||
		ap.Convolvers[0].Meter != nil {
		t.Fatalf("bypass: %d meters, the merger's %v", len(ms), ap.mergeMeter)
	}
	// no upsampler: none for it
	ap.Upsampler = nil
	if ms = ap.attachMeters(true); len(ms) != 5 || ms[1].Name != "convolver 1" || ap.upMeter != nil {
		t.Fatalf("no upsampler: %d meters, the upsampler's %v", len(ms), ap.upMeter)
	}
	// a DSD source: our reader decodes (DecodeSamples takes it first), so the meter is its
	ap.Decoder = &foxAudioDecoder.AudioDecoder{SampleRate: 352800, DSDReader: &foxDSDReader.Reader{}, WavDecoder: &foxWavReader.WavReader{}}
	if ms = ap.attachMeters(true); ap.Decoder.DSDReader.Meter != ms[0] || ap.Decoder.WavDecoder.Meter != nil || ms[0].Rate != 352800 {
		t.Fatalf("DSD: the reader's meter %p, the WAV reader's %p, reported %p", ap.Decoder.DSDReader.Meter, ap.Decoder.WavDecoder.Meter, ms[0])
	}
}

// slowReader hands out per bytes a read, each after delay: a process feeding the engine slowly.
type slowReader struct {
	data  []byte
	per   int
	delay time.Duration
}

func (r *slowReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	time.Sleep(r.delay)
	n := copy(p[:min(len(p), r.per)], r.data)
	r.data = r.data[n:]
	return n, nil
}

// The WAV reader's meter (Fox's foxWavReader; its own test package does not build, so the test is here) counts the
// conversion, not the waits: 4000 frames of 24-bit stereo read 1000 at a time, each read after 50 ms, and sent to a
// stage that takes a block every 100 ms (slower than the reads, so each send waits), take 0.3 s or more while
// converting them takes well under a millisecond.
func TestWavReaderMeterLeavesOutWaits(t *testing.T) {
	fd := &foxWavReader.WavReader{SampleRate: 1000, BitDepth: 24, NumChannels: 2, TargetFrameSize: 1000,
		Input: &slowReader{data: make([]byte, 4000*6), per: 6000, delay: 50 * time.Millisecond}}
	ap := &AudioProcessor{Decoder: &foxAudioDecoder.AudioDecoder{SampleRate: 1000, WavDecoder: fd},
		Encoder: &foxAudioEncoder.AudioEncoder{SampleRate: 1000}, ProcRate: 1000}
	ms := ap.attachMeters(false)
	out := make(chan [][]float64)
	errc := make(chan error, 1)
	go func() {
		errc <- ap.Decoder.DecodeSamples(out)
		close(out)
	}()
	t0 := time.Now()
	frames := 0
	for b := range out {
		frames += len(b[0])
		time.Sleep(100 * time.Millisecond)
	}
	wall := time.Since(t0)
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	audio, busy := ms[0].Figures()
	if frames != 4000 || audio != 4 || wall < 300*time.Millisecond || busy <= 0 || busy > 0.05 {
		t.Fatalf("%d frames, audio %.3f s, busy %.4f s over %v: want every frame and the conversion alone", frames, audio, busy, wall)
	}
}

// The upsampler's and the merger's meters count their arithmetic, not their waits: each sends to a stage that takes a
// block every 100 ms, so the run takes 0.4 s or more while the work takes a few ms; and they count every frame.
func TestStageMetersLeaveOutWaits(t *testing.T) {
	slow := func(recv func() bool) time.Duration { // takes a block every 100 ms until the channel closes
		t0 := time.Now()
		for {
			time.Sleep(100 * time.Millisecond)
			if !recv() {
				return time.Since(t0)
			}
		}
	}
	// the upsampler, its output unbuffered
	u, err := foxUpsampler.New(44100, 705600, 2, foxUpsampler.DefaultCD, foxUpsampler.DefaultHiRes)
	if err != nil {
		t.Fatal(err)
	}
	ap := &AudioProcessor{Decoder: &foxAudioDecoder.AudioDecoder{SampleRate: 44100}, Encoder: &foxAudioEncoder.AudioEncoder{SampleRate: 705600},
		Upsampler: u, ProcRate: 705600}
	ap.attachMeters(false)
	in := make(chan [][]float64, 4)
	for range 4 {
		in <- [][]float64{make([]float64, 4410), make([]float64, 4410)}
	}
	close(in)
	var wg sync.WaitGroup
	out := ap.upsampleStage(in, 0, &wg)
	frames := 0
	wall := slow(func() bool {
		blk, ok := <-out
		if ok {
			frames += len(blk[0])
		}
		return ok
	})
	wg.Wait()
	audio, busy := ap.upMeter.Figures()
	if frames != 4*4410*16 || audio != float64(frames)/705600 || wall < 400*time.Millisecond || busy <= 0 || busy > 0.1 {
		t.Fatalf("upsampler: %d frames out, audio %.3f s, busy %.3f s over %v", frames, audio, busy, wall)
	}

	// the merger, two channels in, its output unbuffered
	logger, err := foxLog.NewLogger(filepath.Join(t.TempDir(), "engine.log"), "test", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(logger.Close)
	ap = &AudioProcessor{Logger: logger, Config: &LyrionDSPSettings.ClientConfig{}, ChannelGains: []float64{0.5, 0.5},
		Decoder: &foxAudioDecoder.AudioDecoder{SampleRate: 1000, NumChannels: 2}, Encoder: &foxAudioEncoder.AudioEncoder{SampleRate: 1000},
		Convolvers: []*foxConvolver.PartitionedConvolver{{}, {}}, ProcRate: 1000}
	ap.attachMeters(true)
	ins := []chan []float64{make(chan []float64, 4), make(chan []float64, 4)}
	for range 4 {
		for _, c := range ins {
			c <- make([]float64, 4096)
		}
	}
	for _, c := range ins {
		close(c)
	}
	merged := make(chan [][]float64)
	go func() {
		ap.mergeChannels(ins, merged)
		close(merged)
	}()
	frames = 0
	wall = slow(func() bool {
		blk, ok := <-merged
		if ok {
			frames += len(blk[0])
		}
		return ok
	})
	audio, busy = ap.mergeMeter.Figures()
	if frames != 4*4096 || audio != float64(frames)/1000 || wall < 400*time.Millisecond || busy <= 0 || busy > 0.1 {
		t.Fatalf("merger: %d frames out, audio %.3f s, busy %.3f s over %v", frames, audio, busy, wall)
	}
}

// reportSpeed (the engine's report): the fill's figures and whether it is over, and the busiest stage's name.
func TestReportSpeed(t *testing.T) {
	dir := t.TempDir()
	ap := &AudioProcessor{AppSettings: &LyrionDSPSettings.AppSettings{TempDataFolder: dir},
		Args: &LyrionDSPSettings.Arguments{CleanClientID: "aa_bb"}, Encoder: &foxAudioEncoder.AudioEncoder{Fill: &foxSpeed.Fill{}}}
	fast, slow := &foxSpeed.Meter{Name: "decoder", Rate: 1000}, &foxSpeed.Meter{Name: "convolver 2", Rate: 1000}
	fast.Add(4000, 10*time.Millisecond)
	slow.Add(4000, time.Second)
	ap.Encoder.Fill.Wrote(4, time.Now().Add(-2*time.Second), 0) // one window: 4 s of audio in about 2 s
	ap.Encoder.Fill.End()
	ap.reportSpeed([]*foxSpeed.Meter{fast, slow})()
	got, ok := readSpeed(t, filepath.Join(dir, "speed_aa_bb.json"))
	if !ok || got.Stage != "convolver 2" || got.Audio != 4 || got.Busy < 1.9 || got.Busy > 3 || !got.Over || got.Pid != os.Getpid() {
		t.Fatalf("the report holds %+v (ok %v), want the fill (4 s in about 2 s, over) and the busiest stage", got, ok)
	}
	// (SqueezeDSP fork) and each stage's own time, ms of work a second of audio, the route's order (Status.pm's stage_ms)
	if fmt.Sprint(got.Stages) != "[{decoder 2.5} {convolver 2 250}]" {
		t.Fatalf("the stages' own times %v, want decoder 2.5 ms and convolver 2 250 ms a second of audio", got.Stages)
	}
	// (0.1.72.46) once the runs have a figure it is the speed: one run, 2 s of audio in 0.25 s
	ap.runs = &foxSpeed.Runs{}
	ap.runs.Hold()
	ap.runs.Free()
	t0 := time.Now()
	for i := range 5 {
		ap.runs.In(0.5, t0.Add(time.Duration(i)*62500*time.Microsecond))
	}
	ap.runs.Hold()
	ap.reportSpeed([]*foxSpeed.Meter{fast, slow})()
	got, ok = readSpeed(t, filepath.Join(dir, "speed_aa_bb.json"))
	if !ok || got.Audio != 2 || got.Busy != 0.25 || got.Runs != 1 || !got.Over || got.Stage != "convolver 2" {
		t.Fatalf("with a run the report holds %+v (ok %v), want the run's 2 s in 0.25 s, 1 run", got, ok)
	}
	if rs := ap.RunsSpeed(); rs != "8.0x real time, 1 runs" {
		t.Fatalf("the end line's speed %q", rs)
	}
}

// The entry stage passes every block on, in order, and tells the runs each one but the input's last, with the time it
// went in; then the input's end, which drops the window open then.
func TestEntryStage(t *testing.T) {
	run := func(sizes []int) *foxSpeed.Runs {
		ap := &AudioProcessor{Decoder: &foxAudioDecoder.AudioDecoder{SampleRate: 1000}, runs: &foxSpeed.Runs{}}
		ap.runs.Hold() // the stream's first run is the fill's: the runs count from the output's first wait
		ap.runs.Free()
		in := make(chan [][]float64)
		var wg sync.WaitGroup
		out := ap.entryStage(in, &wg)
		go func() {
			for _, n := range sizes {
				in <- [][]float64{make([]float64, n)}
			}
			close(in)
		}()
		var got []int
		for blk := range out {
			got = append(got, len(blk[0]))
			time.Sleep(20 * time.Millisecond) // the pipeline takes a block every 20 ms
		}
		wg.Wait()
		if fmt.Sprint(got) != fmt.Sprint(sizes) {
			t.Fatalf("passed on %v, want %v", got, sizes)
		}
		ap.runs.Hold()
		return ap.runs
	}
	// told, the last block would close the window (2.1 s after the first): never told, and the open window dropped
	if a, s, n := run([]int{500, 500, 500, 500, 600}).Figures(); a != 0 || s != 0 || n != 0 {
		t.Fatalf("the input's last block counted: %v s in %v s, %d runs", a, s, n)
	}
	// six 0.5 s blocks and a short last: one window, the 2nd to the 5th block over the 1st's time to the 5th's (4 x 20 ms)
	if a, s, n := run([]int{500, 500, 500, 500, 500, 500, 100}).Figures(); a != 2 || s < 0.07 || s > 0.4 || n != 1 {
		t.Fatalf("%v s in %v s, %d runs: want one window of 2 s in about 0.08 s", a, s, n)
	}
}

// The lead goes on an encoder writing stdout itself (WAV at its bytes a second, DSF at the DSD rate's), never on one
// writing a file.
func TestAttachLead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no lead on Windows")
	}
	ap := &AudioProcessor{Encoder: &foxAudioEncoder.AudioEncoder{SampleRate: 705600, NumChannels: 2, BitDepth: 24}, runs: &foxSpeed.Runs{}}
	l := ap.attachLead()
	if l == nil || ap.Encoder.Out != l {
		t.Fatal("no lead on a WAV encoder writing stdout")
	}
	l.Close()
	ap.Encoder = &foxAudioEncoder.AudioEncoder{SampleRate: 705600, NumChannels: 2, BitDepth: 24, Filename: filepath.Join(t.TempDir(), "x.wav")}
	if ap.attachLead() != nil || ap.Encoder.Out != nil {
		t.Fatal("a lead on an encoder writing a file")
	}
}

// StageTimes (the end line, SqueezeDSP fork): each metered stage's own time in the route's order, one not yet started
// left out, none at all an empty string.
func TestStageTimes(t *testing.T) {
	ap := &AudioProcessor{}
	if got := ap.StageTimes(); got != "" {
		t.Fatalf("no meters: %q, want nothing", got)
	}
	dec, up, enc := &foxSpeed.Meter{Name: "decoder", Rate: 44100}, &foxSpeed.Meter{Name: "upsampler", Rate: 705600}, &foxSpeed.Meter{Name: "encoder", Rate: 705600}
	dec.Add(441000, 12*time.Millisecond)
	enc.Add(7056000, 2502*time.Millisecond)
	ap.Meters = []*foxSpeed.Meter{dec, up, enc}
	if got := ap.StageTimes(); got != "decoder 1.2 ms, encoder 250.2 ms" {
		t.Fatalf("StageTimes %q, want the decoder's 1.2 ms and the encoder's 250.2 ms a second of audio, the upsampler (no audio yet) left out", got)
	}
}
