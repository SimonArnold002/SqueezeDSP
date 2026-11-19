package LyrionDSPProcessAudio

import (
	"context"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Foxenfurter/foxAudioLib/foxAudioDecoder"
	"github.com/Foxenfurter/foxAudioLib/foxAudioDecoder/gipsyDSDReader"
	"github.com/Foxenfurter/foxAudioLib/foxAudioEncoder"
	"github.com/Foxenfurter/foxAudioLib/foxAudioEncoder/gipsyDSDEncoder"
	foxConvolver "github.com/Foxenfurter/foxAudioLib/foxConvolverPartition"
	"github.com/Foxenfurter/foxAudioLib/foxLog"
	"github.com/Foxenfurter/foxAudioLib/foxResampler"
	"github.com/Foxenfurter/foxAudioLib/gipsySpeed"
	"github.com/Foxenfurter/foxAudioLib/gipsyUpsampler"

	"github.com/Foxenfurter/foxAudioLib/foxFilters"
	"github.com/Foxenfurter/foxAudioLib/foxUtils"
	"github.com/Foxenfurter/foxLyrionDSP/LyrionClient"
	"github.com/Foxenfurter/foxLyrionDSP/LyrionDSPSettings"
)

const packageName = "LyrionDSPProcessAudio"

type AudioProcessor struct {
	Decoder           *foxAudioDecoder.AudioDecoder
	Encoder           *foxAudioEncoder.AudioEncoder
	Logger            *foxLog.Logger
	Config            *LyrionDSPSettings.ClientConfig
	Convolvers        []*foxConvolver.PartitionedConvolver
	TempFilterPath    string
	cachePath         string
	previousTrackPath string
	AppSettings       *LyrionDSPSettings.AppSettings
	Args              *LyrionDSPSettings.Arguments
	Impulse           [][]float64
	SaveImpulse       bool
	PEQImpulse        []float64
	Delay             *foxFilters.Delay
	ReuseFilter       bool
	ProcessingMode    foxUtils.ProcessingMode
	gainCh            chan *LyrionClient.TrackGainResult
	gainCancel        context.CancelFunc // cancels any in-flight gain request
	ReplayGainData    *LyrionClient.TrackGainResult
	ReplayGain        float64 // resolved gain to apply
	DefaultReplayGain float64 // default gain if no LMS data available
	PreviousTrackURL  string
	PreviousTrackGain float64
	PreviousAlbumGain *float64 // persisted across tracks for smart gain
	PreviousAlbumID   string   // album ID of the previous track, for remote album detection
	Crossfeed         *foxFilters.CrossfeedProcessor
	crossfeedState    *foxFilters.CrossfeedState
	CombinedGain      float64
	//FIRStrengthNorm   float64
	ChannelGains []float64
	// the converter (SqueezeDSP fork): a PCM source upsampled here, before the DSP, to --upsample (gipsyUpsampler).
	// ProcRate is the rate the DSP and the encoder run at: the decoder's, or --upsample's.
	Upsampler *gipsyUpsampler.Upsampler
	ProcRate  int
	// the stages' meters for the engine's own speed (attachMeters), those this package's stages keep
	upMeter, mergeMeter *gipsySpeed.Meter
	// (SqueezeDSP fork) every stage's meter, the route's order (attachMeters): each stage's own time, live in the speed
	// file and at the end in the log (StageTimes)
	Meters []*gipsySpeed.Meter
	// (0.1.72.46) the runs the engine's lead makes while it plays (attachMeters, attachLead): its speed after the fill
	runs *gipsySpeed.Runs
	// the plugin's gapless note and what the engine decides from it (gapless.go)
	gapless gaplessState
	length  lengthState // the stream's place in the continuous stream, and its exact count (length.go)
	// flacSeek: a remote FLAC's count, read from its URL beside the set-up (flacseek.go)
	flacSeek chan flacSeekResult
}

const CACHE_VERSION = 9 // 9: the input's kind, rate and DSD rate, for --tail (tail.go); 8: the stream's place and the frames past its cut, length.go; Increment when changing convolver type (6, SqueezeDSP fork: the convolver's state carries the
// input a track ended with short of a partition, for an exact gapless join; 7: whether the track held its state for
// the next, and the upsampler's, gapless.go)
type DSPResidualsCache struct {
	Version           int
	SampleRate        int
	NumChannels       int
	ExpiryTime        int64             // When this cache expires
	DelayBuffers      *foxFilters.Delay // Full delay buffers for all channels
	ConvolverStates   [][]byte          // Full encoded state
	PreviousAlbumGain *float64          //
	PreviousAlbumID   string            // *** NEW *** Album ID of the previous track, for remote album detection
	CrossfeedState    *foxFilters.CrossfeedState
	// (SqueezeDSP fork, gapless.go) in note mode: Held, the track kept its stages' state for the next one (a track that
	// nothing follows played it out); the stages' own states besides Fox's
	Held      bool
	Upsampler []byte
	Encoder   []byte // DSF out's
	Reader    []byte // a DSD source's decode (gipsyDSDReader), when it stopped at its file's end holding
	TrackURL  string // the track that wrote it (--trackURL): only the stream that continues that track takes it
	// (length.go) the continuous stream's place where this track's stream was cut, and the frames the stages sent past
	// the cut, the next stream's first; EncBits: the DSD samples a channel DSF out held (they count in the next header)
	Gate            bool
	GateIn, GateCut int64
	Pending         [][]float64
	EncBits         int64
	// (tail.go) the held track's input, so --tail can open an empty one of the same kind: "pcm" or "dsd", the rate the
	// decoder handed on (a DSD source's decode rate), a DSD source's own DSD rate
	InKind    string
	InRate    int
	InDSDRate int
}

// PreviousTrackCache persists only the track identity and gain resolved at the
// START of the previous invocation (immediately after PrepareChannelGains).
// Written unconditionally so skipped tracks are captured.
type PreviousTrackCache struct {
	TrackURL  string
	TrackGain float64
}

// StartGainRetrieval fires the async LMS gain query - call before Initialize()
func (ap *AudioProcessor) StartGainRetrieval() {
	const functionName = "StartGainRetrieval"
	errorText := fmt.Sprintf("%s:%s: ", packageName, functionName)

	// Cancel any previous in-flight request and drain its channel so
	// the old goroutine can unblock and exit cleanly.
	if ap.gainCancel != nil {
		ap.gainCancel()
		ap.gainCancel = nil
	}
	if ap.gainCh != nil {
		// Non-blocking drain — goroutine may already have sent or may be mid-send
		select {
		case <-ap.gainCh:
		default:
		}
		ap.gainCh = nil
	}

	port := 9000
	if ap.AppSettings.LMSPort != "" {
		fmt.Sscan(ap.AppSettings.LMSPort, &port)
	}
	host := "localhost"
	playerID := ap.Args.ClientID

	ap.gainCh = make(chan *LyrionClient.TrackGainResult, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	ap.gainCancel = cancel // stored so next call can cancel this one

	go func() {
		defer cancel() // also self-cancel when done, safe to call multiple times
		ap.Logger.Debug(errorText + "Starting track gain retrieval for player: " + playerID)

		result, err := LyrionClient.New(host, port).GetCurrentTrackGain(ctx, playerID)
		if err != nil {
			if ctx.Err() != nil {
				ap.Logger.Debug(errorText + "Gain retrieval cancelled or timed out")
			} else {
				ap.Logger.Error(errorText + "Error retrieving track gain: " + err.Error())
			}
			// Non-blocking send — if gainCh was already replaced (another skip
			// happened), this channel has no reader and we must not block.
			select {
			case ap.gainCh <- nil:
			default:
			}
			return
		}
		ap.Logger.Debug(errorText + "url: " + result.URL +
			" track_gain: " + fmtGain(result.TrackGain) +
			" album_gain: " + fmtGain(result.AlbumGain))

		select {
		case ap.gainCh <- result:
		default:
			// Channel replaced by a newer skip; result is stale, discard it
			ap.Logger.Debug(errorText + "Gain result discarded (superseded by newer request)")
		}
	}()
}

// ResolveGain blocks until the gain result is available then stores it on the processor
// call after Initialize() and before ProcessAudio()
func (ap *AudioProcessor) ResolveReplayGain() {
	const functionName = "ResolveReplayGain"
	errorText := fmt.Sprintf("%s:%s: ", packageName, functionName)
	ap.DefaultReplayGain = ap.Config.ReplayGain.FixedGain

	if ap.gainCh == nil {
		ap.Logger.Debug(errorText + "No gain retrieval in progress - defaulting to DefaultReplayGain")
		ap.ReplayGain = ap.DefaultReplayGain
		return
	}

	ap.ReplayGainData = <-ap.gainCh

	//
	if ap.PreviousTrackURL == ap.Args.TrackURL &&
		ap.PreviousTrackURL != "" &&
		ap.PreviousTrackGain != 0 {
		ap.ReplayGain = ap.PreviousTrackGain
		// Drain the in-flight request so the goroutine exits cleanly

		ap.Logger.Debug(errorText + fmt.Sprintf("Resolved ReplayGain: %.2f dB (same track repeated)", ap.ReplayGain))
		// the values in the cache remain unchanged.
		return
	}

	if ap.ReplayGainData == nil {
		ap.Logger.Debug(errorText + "No gain result available - defaulting to DefaultReplayGain")
		ap.ReplayGain = ap.DefaultReplayGain
		ap.PreviousAlbumGain = nil
		ap.PreviousTrackURL = ap.Args.TrackURL
		ap.PreviousTrackGain = ap.ReplayGain
		return
	}

	ap.Logger.Debug(errorText +
		" track_gain: " + fmtGain(ap.ReplayGainData.TrackGain) +
		" track_peak: " + fmtGain(ap.ReplayGainData.TrackPeak) +
		" album_gain: " + fmtGain(ap.ReplayGainData.AlbumGain) +
		" album_peak: " + fmtGain(ap.ReplayGainData.AlbumPeak) +
		" album_match: " + fmt.Sprintf("%v", ap.ReplayGainData.AlbumMatch) +
		" next_track_gain: " + fmtGain(ap.ReplayGainData.NextTrackGain) +
		" next_album_gain: " + fmtGain(ap.ReplayGainData.NextAlbumGain))

	gainSource := "default"

	// Spotify normalisation is baked in pre-LMS - apply fixed offset and return
	if strings.HasPrefix(ap.ReplayGainData.URL, "spotify://") {
		ap.ReplayGain = ap.Config.ReplayGain.SpotifyGain
		gainSource = "spotify"
		ap.PreviousTrackURL = ap.Args.TrackURL
		ap.PreviousTrackGain = ap.ReplayGain
		ap.Logger.Debug(errorText + fmt.Sprintf("Resolved ReplayGain: %.2f dB (%s)", ap.ReplayGain, gainSource))
		return
	}

	mode := ap.Config.ReplayGain.Mode
	if mode == 0 {
		mode = 3 // treat unconfigured as smart gain
	}

	currentAlbumGain := ap.ReplayGainData.AlbumGain
	nextAlbumGain := ap.ReplayGainData.NextAlbumGain
	// *** NEW HELPER (local to the switch) ***
	// albumIDMatch is true when the current track carries an album identifier and
	// that identifier is shared with either the previous or next track.
	// Used to detect album context for remote tracks that lack embedded album gain.
	albumIDMatch := ap.ReplayGainData.AlbumID != "" &&
		(ap.ReplayGainData.AlbumID == ap.PreviousAlbumID ||
			ap.ReplayGainData.AlbumID == ap.ReplayGainData.NextAlbumID)
	ap.Logger.Debug(errorText + fmt.Sprintf("Album ID match: %v (current '%s', previous '%s', next '%s')", albumIDMatch, ap.ReplayGainData.AlbumID, ap.PreviousAlbumID, ap.ReplayGainData.NextAlbumID))
	var peak *float64 // the peak measured with the gain chosen; nil for a default gain
	switch mode {
	case 1:
		// Track gain only - ignore album gain entirely
		if ap.ReplayGainData.TrackGain != nil {
			ap.ReplayGain = *ap.ReplayGainData.TrackGain
			peak = ap.ReplayGainData.TrackPeak
			gainSource = "track"
		} else {
			ap.ReplayGain = ap.DefaultReplayGain
			gainSource = "default (no track gain)"
		}
		ap.PreviousAlbumGain = nil
		ap.PreviousAlbumID = "" // add this

	case 2:
		if currentAlbumGain != nil && ap.ReplayGainData.AlbumMatch {
			ap.ReplayGain = *currentAlbumGain
			peak = albumPeak(ap.ReplayGainData)
			gainSource = "album (strict match)"
			ap.PreviousAlbumGain = currentAlbumGain
		} else if albumIDMatch { // *** MISSING - needs to be added here ***
			ap.ReplayGain = ap.DefaultReplayGain
			gainSource = "default (album context via ID, no gain data)"
			ap.PreviousAlbumGain = nil
		} else if ap.ReplayGainData.TrackGain != nil {
			ap.ReplayGain = *ap.ReplayGainData.TrackGain
			peak = ap.ReplayGainData.TrackPeak
			gainSource = "track (no album match)"
			ap.PreviousAlbumGain = nil
		} else {
			ap.ReplayGain = ap.DefaultReplayGain
			gainSource = "default (no gain data)"
			ap.PreviousAlbumGain = nil
		}
		ap.PreviousAlbumID = ap.ReplayGainData.AlbumID
	default:
		// Mode 3: smart gain - use album gain if available from either neighbour,
		// no strict sequence check, neighbour album gain comparison is sufficient
		if currentAlbumGain != nil {
			if ap.PreviousAlbumGain != nil && *ap.PreviousAlbumGain == *currentAlbumGain {
				ap.ReplayGain = *currentAlbumGain
				peak = albumPeak(ap.ReplayGainData)
				gainSource = "album (matched previous)"
			} else if nextAlbumGain != nil && *nextAlbumGain == *currentAlbumGain {
				ap.ReplayGain = *currentAlbumGain
				peak = albumPeak(ap.ReplayGainData)
				gainSource = "album (matched next)"
			} else if ap.ReplayGainData.TrackGain != nil {
				ap.ReplayGain = *ap.ReplayGainData.TrackGain
				peak = ap.ReplayGainData.TrackPeak
				gainSource = "track"
			} else {
				ap.ReplayGain = ap.DefaultReplayGain
				gainSource = "default (no track gain)"
			}
			ap.PreviousAlbumGain = currentAlbumGain
			ap.PreviousAlbumID = ap.ReplayGainData.AlbumID //
		} else {
			// currentAlbumGain == nil
			// *** NEW: if album ID links this track to a neighbour, use default gain
			//         so the whole album gets the same attenuation ***
			if albumIDMatch {
				ap.ReplayGain = ap.DefaultReplayGain
				gainSource = "default (album context via ID, no gain data)"
			} else if ap.ReplayGainData.TrackGain != nil {
				ap.ReplayGain = *ap.ReplayGainData.TrackGain
				peak = ap.ReplayGainData.TrackPeak
				gainSource = "track (no album gain)"
			} else {
				ap.ReplayGain = ap.DefaultReplayGain
				gainSource = "default (no gain data)"
			}
			ap.PreviousAlbumGain = nil
			// *** NEW ***
			ap.PreviousAlbumID = ap.ReplayGainData.AlbumID
		}
	}
	// As LMS does (Slim::Player::ReplayGain::preventClipping), no gain may take the peak it was
	// measured with past full scale. PreviousAlbumGain keeps the album's own gain for matching.
	if capped, ok := capByPeak(ap.ReplayGain, peak); ok {
		gainSource += fmt.Sprintf(", capped from %.2f dB by peak %.6f", ap.ReplayGain, *peak)
		ap.ReplayGain = capped
	}
	ap.PreviousTrackURL = ap.Args.TrackURL
	ap.PreviousTrackGain = ap.ReplayGain

	ap.Logger.Debug(errorText + fmt.Sprintf("Resolved ReplayGain: %.2f dB (%s)", ap.ReplayGain, gainSource))
}

// capByPeak is LMS's preventClipping: the gain, or -20 log10(peak) when that is lower. A missing or
// non-positive peak leaves the gain as it is.
func capByPeak(gain float64, peak *float64) (float64, bool) {
	if peak == nil || *peak <= 0 {
		return gain, false
	}
	if noclip := -20 * math.Log10(*peak); noclip < gain {
		return noclip, true
	}
	return gain, false
}

// albumPeak is the peak an album gain answers to: the album's, or with none the track's own. LMS leaves an
// album gain with no album peak uncapped; the track's peak is the exact bound for the track playing, and an
// album peak is never below it. A Qobuz album can carry its gain without its peak (the Qobuz plugin,
// API/Common.pm, copies a cached album's replay_gain alone), as can a file tagged without REPLAYGAIN_ALBUM_PEAK.
func albumPeak(d *LyrionClient.TrackGainResult) *float64 {
	if d.AlbumPeak != nil {
		return d.AlbumPeak
	}
	return d.TrackPeak
}

func fmtGain(f *float64) string {
	if f == nil {
		return "n/a"
	}
	return fmt.Sprintf("%.2f", *f)
}

// PrepareChannelGains resolves ReplayGain and computes the per-channel gain scalars.
// Call at the end of Initialize(), after convolvers are populated.
// mergeChannels reads ap.ChannelGains directly.
const targetPeakLevel = 0.891 // -1 dBfs ceiling

func (ap *AudioProcessor) PrepareChannelGains() {
	const functionName = "PrepareChannelGains"
	errorText := fmt.Sprintf("%s:%s", packageName, functionName)

	numChannels := ap.Decoder.NumChannels
	channelGains := foxFilters.GetChannelsGain(&ap.Config.PlayerConfig, numChannels, ap.Logger)

	convolverGain := 0.0
	convolverRMSGain := 0.0
	for i := range numChannels {
		convolverGain = math.Max(ap.Convolvers[i].MaxGain, convolverGain)
		convolverRMSGain = math.Max(ap.Convolvers[i].RMSGain, convolverRMSGain)
	}
	if convolverGain == 0 {
		convolverGain = 1.0
	}
	if convolverRMSGain == 0 {
		convolverRMSGain = 1.0
	}

	combinedGain := 1.0 / convolverGain // safe conservative fallback

	if ap.Config.ReplayGain.Enabled {
		ap.ResolveReplayGain()
		replayGainLinear := math.Pow(10, ap.ReplayGain/20.0)

		// Weighted average of RMSGain and FFTMaxGain — weights 2 and 4
		// naturally conservative for boosting filters, recovery for attenuating ones
		weightedGain := (2*convolverRMSGain + 2*convolverGain) / 4.0
		combinedGain = (1.0 / weightedGain) * replayGainLinear

		ap.Logger.Debug(errorText + fmt.Sprintf(
			" : convolverGain %.4f, convolverRMSGain %.4f (%.2f dB), weightedGain %.4f, replayGain %.2f dB (linear %.4f), combined %.4f",
			convolverGain, convolverRMSGain, 20*math.Log10(convolverRMSGain),
			weightedGain, ap.ReplayGain, replayGainLinear, combinedGain))
	} else {
		ap.Logger.Debug(errorText + fmt.Sprintf(
			" : convolverGain %.4f, convolverRMSGain %.4f, replayGain disabled, combined %.4f",
			convolverGain, convolverRMSGain, combinedGain))
	}
	ap.CombinedGain = linearToDBFS(combinedGain)

	for i := range numChannels {
		channelGains[i] *= combinedGain
	}
	if numChannels > 1 {
		ap.Logger.Debug(errorText + fmt.Sprintf(
			" : channel gain left %.6f, right %.6f", channelGains[0], channelGains[1]))
	}
	ap.ChannelGains = channelGains
	go ap.SavePreviousTrack()
}

// scaleImpulse multiplies an impulse resampled from fromRate to toRate by fromRate / toRate, which
// undoes the gain the resample gave it, and returns that factor (1 when the rates match).
func scaleImpulse(impulse [][]float64, fromRate, toRate int) float64 {
	if fromRate <= 0 || toRate <= 0 || fromRate == toRate {
		return 1
	}
	k := float64(fromRate) / float64(toRate)
	for _, channel := range impulse {
		for i := range channel {
			channel[i] *= k
		}
	}
	return k
}

func linearToDBFS(linear float64) float64 {
	if linear <= 0 {
		return -200.0 // or math.Inf(-1)
	}
	return 20 * math.Log10(linear)
}

func requiresAdapter(format string) bool {
	switch strings.ToUpper(format) {
	case "WAV", "PCM": // Most formats need the adapter, but WAV/PCM can be handled natively by the encoder without Adapter
		return false
	}
	return true
}

// Initialize Audio Headers and create the convolvers and populate any tails data from previous runs
// The signal block length is calculated in DSP Filters combine filters function

func (ap *AudioProcessor) Initialize() error {
	const functionName = "Initialize"
	errorText := fmt.Sprintf("%s:%s ", packageName, functionName)
	// a remote FLAC's count from its URL, beside the set-up (flacseek.go); a held tail's empty input (tail.go)
	ap.flacSeekStart()
	if ap.Args.Tail {
		if err := ap.tailInput(); err != nil {
			ap.Logger.Error(errorText + err.Error())
			ap.Encoder = &foxAudioEncoder.AudioEncoder{}
			return err
		}
	}
	// initialise decoder
	ap.Decoder = &foxAudioDecoder.AudioDecoder{
		DebugFunc: ap.Logger.Debug,
		Type:      "WAV",
	}
	ap.ProcessingMode = foxUtils.ModeStreaming
	// If no input file is specified, use stdin by default
	if ap.Args.InPath != "" {
		ap.Decoder.Filename = ap.Args.InPath
		ap.Logger.Debug(errorText + "Input File: " + ap.Decoder.Filename)
	}

	if ap.Args.StartTime > 0 {
		ap.Decoder.StartTime = ap.Args.StartTime
		ap.Logger.Debug(errorText + fmt.Sprintf("Start Time: %.3f seconds", ap.Decoder.StartTime.Seconds()))

	}

	inputFormat := strings.ToUpper(ap.Args.InputFormat)
	if inputFormat == "" {
		inputFormat = "WAV"
	}
	ap.Logger.Debug(errorText + "Input Format: " + inputFormat)

	switch inputFormat {
	case "PCM":
		ap.Decoder.Type = "PCM"
		ap.Decoder.SampleRate = ap.Args.InputSampleRate
		ap.Decoder.BitDepth = ap.Args.PCMBits
		ap.Decoder.NumChannels = ap.Args.PCMChannels
		ap.Decoder.BigEndian = ap.Args.BigEndian
		// a 0 from LMS (it knew no more of a remote track yet): the remote FLAC's own (flacseek.go)
		ap.rawFormatFromURL()
	case "DSD":
		// a DSF or DSDIFF file, decoded by the engine itself to --samplerate (gipsyDSDReader)
		ap.Decoder.Type = "DSD"
		ap.Decoder.SampleRate = ap.Args.InputSampleRate
		// the player's settings, unless the command line gives its own
		ap.Decoder.DSDCutoff, ap.Decoder.DSDGain = ap.Args.DSDCutoff, ap.Args.DSDGain
		if c := ap.Config.DSDCutoff; c != nil && !ap.Args.DSDCutoffSet {
			ap.Decoder.DSDCutoff = *c
		}
		if g := ap.Config.DSDGain; g != nil && !ap.Args.DSDGainSet {
			ap.Decoder.DSDGain = *g
		}
		// DSD back out (DSF): unity end to end; the player's DSDGain is for DSD to PCM, only a flag moves it
		if strings.ToUpper(ap.Args.OutputFormat) == "DSF" && !ap.Args.DSDGainSet {
			ap.Decoder.DSDGain = 0
		}
		// native (--samplerate=native, the native round): the decode stays at the DSD rate, the DSP there, through the
		// player's integrator
		ap.Decoder.DSDNative, ap.Decoder.DSDIntegrator = ap.Args.DSDNative, ap.Config.DSDIntegrator
		ap.Logger.Debug(errorText + fmt.Sprintf("DSD decode: cutoff %.0f Hz, gain %.2f dB, native %v (integrator %q)",
			ap.Decoder.DSDCutoff, ap.Decoder.DSDGain, ap.Decoder.DSDNative, ap.Decoder.DSDIntegrator))
		ap.Decoder.EndTime = ap.Args.EndTime
	case "WAV":
		ap.Decoder.Type = "WAV"
		/*default:
		// All other formats (FLAC, MP3, AAC, OGG, etc.) go through FFmpeg adapter
		// FFmpeg detects the format automatically from the stream
		ap.Decoder.Type = inputFormat
		ap.Decoder.SampleRate = ap.Args.InputSampleRate
		ap.Decoder.NumChannels = ap.Args.PCMChannels
		if requiresAdapter(inputFormat) && ap.AppSettings.FFmpegExe == "" {
			return fmt.Errorf("input format %s requires FFmpegExe to be set", inputFormat)
		}*/
	}

	// Validate before Initialise
	/*if requiresAdapter(inputFormat) && ap.AppSettings.FFmpegExe == "" {
		return fmt.Errorf("input format %s requires FFmpegExe to be set", inputFormat)
	}
	*/
	err := ap.Decoder.Initialise()
	// native, with DSF out at a rate under the source's (the converter's fixed rate below a DSD256 file's): nothing
	// goes down a DSD rate, so this track decodes at DXD, as with native off
	if err == nil && ap.Decoder.DSDNative && strings.ToUpper(ap.Args.OutputFormat) == "DSF" {
		if rate, rerr := ap.dsfRate(); rerr == nil && rate < ap.Decoder.DSDReader.DSDRate {
			ap.Logger.Info(errorText + fmt.Sprintf("native: DSD out at %d Hz is under the source's %d Hz: this track at DXD",
				rate, ap.Decoder.DSDReader.DSDRate))
			ap.Decoder.Close()
			ap.Decoder.DSDReader = nil
			ap.Decoder.DSDNative, ap.Decoder.SampleRate = false, gipsyDSDReader.DXD
			err = ap.Decoder.Initialise()
		}
	}
	if err != nil {
		// initialise an empty encode purely for error handling
		ap.Logger.Error(errorText + "Error Initialising Audio Decoder: " + err.Error())
		ap.Encoder = &foxAudioEncoder.AudioEncoder{}
		return err
	}
	// For adapter-decoded formats, read back the actual stream properties
	// FFmpeg confirms these; copy them up to the parent decoder
	/*if ap.Decoder.AdapterDecoder != nil {
		ap.Decoder.SampleRate = ap.Decoder.AdapterDecoder.SampleRate
		ap.Decoder.NumChannels = ap.Decoder.AdapterDecoder.NumChannels
		ap.Decoder.BitDepth = ap.Decoder.AdapterDecoder.BitDepth
	}*/
	ap.Logger.Debug(errorText + fmt.Sprintf("Decoder initialised: SampleRate=%d, NumChannels=%d, BitDepth=%d", ap.Decoder.SampleRate, ap.Decoder.NumChannels, ap.Decoder.BitDepth))
	// the converter: a PCM source goes up to --upsample here, before the DSP, by our own two-stage filters
	// (gipsyUpsampler; the player's choice for a CD-rate source, another for hi-res); from here on everything runs at
	// ProcRate. A source already at that rate passes as it is.
	ap.ProcRate = ap.Decoder.SampleRate
	if ap.Args.UpsampleRate > 0 && ap.Args.UpsampleRate != ap.Decoder.SampleRate {
		u, err := gipsyUpsampler.New(ap.Decoder.SampleRate, ap.Args.UpsampleRate, ap.Decoder.NumChannels, ap.Args.UpFilterCD, ap.Args.UpFilterHiRes)
		if err != nil {
			ap.Encoder = &foxAudioEncoder.AudioEncoder{}
			return err
		}
		ap.Upsampler, ap.ProcRate = u, ap.Args.UpsampleRate
		ap.Logger.Debug(errorText + fmt.Sprintf("Upsample: %d to %d Hz, %s (%s source), stage A %d taps, stage B %d, delay %.2f samples taken off",
			u.InRate, u.OutRate, u.Choice, map[bool]string{false: "CD-rate", true: "hi-res"}[gipsyUpsampler.IsHiRes(u.InRate)], u.TapsA, u.TapsB, u.Delay))
	}
	targetSampleRate := ap.ProcRate
	// we use the processing Mode and the sample rate to create appropriate buffer sizes using a util function.
	ap.Decoder.ConfigureFrameSize(ap.ProcessingMode)
	foxConvolver.ProcessingMode = ap.ProcessingMode

	// Reader used to load pre-computer filter, delay tails and filter tails from previously played track
	ap.ReuseFilter = false
	ap.TempFilterPath = filepath.Join(ap.AppSettings.TempDataFolder, "filter_"+ap.Args.CleanClientID+"_"+fmt.Sprintf("%d", targetSampleRate)+".gob")

	// initialise delay
	myTempReader := new(foxAudioDecoder.AudioDecoder)
	ap.cachePath = filepath.Join(ap.AppSettings.TempDataFolder, "dsp_residuals_"+ap.Args.CleanClientID+".gob")
	ap.previousTrackPath = filepath.Join(ap.AppSettings.TempDataFolder, "previous_track_"+ap.Args.CleanClientID+".gob")
	// the plugin's gapless note: in note mode, whether this stream takes the last track's state (gapless.go)
	ap.gaplessStart()
	ap.Delay = foxFilters.NewDelay(ap.Decoder.NumChannels, float64(targetSampleRate))
	//var myBuffer [][]float64
	myDelayChannel := 0
	delayMS := 0.0

	switch delayMS = ap.Config.Delay.Value; {

	case delayMS < 0:
		myDelayChannel = 0
		ap.Logger.Debug(errorText + fmt.Sprintf(": Delay left channel %f ms", -delayMS))
		delayMS = -delayMS
		//ap.DelayPath = filepath.Join(ap.AppSettings.TempDataFolder, "delay_"+ap.Args.CleanClientID+"_left.wav")

	case delayMS > 0:
		myDelayChannel = 1
		ap.Logger.Debug(errorText + fmt.Sprintf(": Delay right channel %f ms", delayMS))
		//ap.DelayPath = filepath.Join(ap.AppSettings.TempDataFolder, "delay_"+ap.Args.CleanClientID+"_right.wav")

	}
	// look for delay file if there is a delay
	if delayMS != 0 {
		// add delay
		ap.Delay.AddDelay(myDelayChannel, delayMS)

	}
	// load any gain from previous track
	ap.LoadPreviousTrack()
	// Initialise Convolvers

	//used for normalization - may need to add this as a configurable item in the future
	// Approx -0.5 dBFS
	//targetLevel := 1.0
	saveImpulse := false
	myTempFirFilter := ""
	baseFileName := ""

	// Load any Convolver from previous track
	ap.Logger.Debug(errorText + ": " + "Attemptng to load cached convolvers from " + ap.TempFilterPath)
	err = ap.LoadConvolvers(ap.TempFilterPath, ap.Config.TimeStamp)
	if err != nil {
		ap.Logger.Debug(errorText + ": " + err.Error())
		ap.ReuseFilter = false
	} else {
		ap.ReuseFilter = true
		ap.Logger.Debug(errorText + ": " + "Convolver loaded from cache")
		//ap.UseTail = true
		ap.LoadDSPResiduals()

	}
	if !ap.ReuseFilter {

		var wg sync.WaitGroup

		wg.Add(2)
		go func() {
			defer wg.Done()

			baseFileName = strings.TrimSuffix(filepath.Base(ap.Config.FIRWavFile), filepath.Ext(ap.Config.FIRWavFile))
			// no impulse
			//myLogger.Debug("Trying to load impulse: " + baseFileName)
			if baseFileName == "." {
				ap.Logger.Debug(errorText + ": No impulse specified")
				ap.Impulse = make([][]float64, 0)
			} else {

				//baseFileName = baseFileName + "_" + fmt.Sprintf("%d", targetSampleRate) + ".wav"
				baseFileName = baseFileName + "_" + ap.Args.CleanClientID + "_" + fmt.Sprintf("%d", targetSampleRate) + ".wav"
				myTempFirFilter = filepath.Join(ap.AppSettings.TempDataFolder, baseFileName)

				//inputFile string, outputFile string, targetSampleRate int, targetBitDepth int, myLogger *foxLog.Logger
				// try and load resampled file first
				ap.Logger.Debug(errorText + "Trying resampled Impulse First : " + myTempFirFilter)
				ap.Impulse, err = myTempReader.LoadFiletoSampleBuffer(myTempFirFilter, "WAV", ap.Logger)
				if err != nil {
					// if that fails then try with original file
					if strings.Contains(err.Error(), "does not exist") {
						// File not found case
						ap.Logger.Debug(errorText + "Resampled Impulse does not exist, trying original: " + ap.Config.FIRWavFile)

						myResampler := foxResampler.NewResampler()
						myResampler.ToSampleRate = targetSampleRate
						myResampler.SOXPath = ap.AppSettings.SoxExe

						ap.Impulse, err = myResampler.ReadnResampleFile2Buffer(ap.Config.FIRWavFile)
						if err != nil {
							ap.Logger.Warn(errorText + "Error loading impulse: " + err.Error())
						}
						// sox resamples the impulse as it would audio, keeping its sample values, so at R
						// times its own rate the filter has R times its gain (+12 dB from 96k to 384k).
						// Scaled back, it keeps the response it was made with at any rate. The copy saved
						// below is the scaled one; the plugin deletes the temp folder at every LMS start.
						if myResampler.FromSampleRate > 0 {
							k := scaleImpulse(ap.Impulse, myResampler.FromSampleRate, targetSampleRate)
							ap.Logger.Debug(errorText + fmt.Sprintf("Impulse made at %d Hz, run at %d Hz: scaled by %.6f (%.2f dB)",
								myResampler.FromSampleRate, targetSampleRate, k, 20*math.Log10(k)))
						} else {
							ap.Logger.Warn(errorText + "Impulse's own rate unknown: not scaled to " + fmt.Sprintf("%d", targetSampleRate) + " Hz")
						}
						// remove silence and background noise from impulse

						ap.Impulse, err = foxFilters.CleanUpImpulse(ap.Impulse, targetSampleRate, -70.0, ap.Logger)
						if err != nil {
							ap.Logger.Warn(errorText + "Error cleaning impulse: " + err.Error())
						}
						saveImpulse = true

					} else {
						ap.Logger.Warn(errorText + "Error loading impulse: " + err.Error()) // Other errors
					}
				}
			}
		}()

		myPEQ := foxFilters.NewPEQFilter(targetSampleRate, ap.Logger)
		go func() {
			defer wg.Done()
			ap.Logger.Debug("Create PEQ Filter")
			var err error = nil
			myPEQ, err = foxFilters.BuildPEQFilter(&ap.Config.PlayerConfig, targetSampleRate, ap.Logger)
			if err != nil {
				ap.Logger.FatalError(errorText + "Error building PEQ: " + err.Error())
			}
			//ap.PEQImpulse = myPEQ.Impulse

		}()
		wg.Wait()

		// test an amended combine filters which handles all the complex logic around the different cases of FIR and PEQ filters being present or not, and also handles normalization and resampling of the filters as needed
		ap.Logger.Debug("Combine Filters")

		firImpulse := foxFilters.ApplyFIRStrengthToImpulse(ap.Impulse, ap.Config.FIRStrength)

		myConvolvers, err := foxFilters.CombineFilters(firImpulse, myPEQ, ap.Decoder.NumChannels, targetSampleRate, ap.Logger)

		if err != nil {
			ap.Logger.Error(errorText + "Error combining filters: " + err.Error())
			return err
		}
		// set the convolvers
		ap.Convolvers = myConvolvers

		// lets test the combined impulse
		//	var myImpulse [][]float64
		saveImpulse = true

		// save the impulse
		if saveImpulse && len(ap.Impulse) > 0 {
			// Backup Impulses

			targetBitDepth := 32
			ap.Logger.Debug("Backup Impulse: " + myTempFirFilter)
			go foxAudioEncoder.WriteWavFile(
				myTempFirFilter,
				ap.Impulse,
				targetSampleRate,
				targetBitDepth,
				len(ap.Impulse),
				false, // i.e. do not overwrite
				ap.Logger,
			)
		} else {
			ap.Logger.Debug(errorText + "No impulse to backup")
		}

		// Load any Convolver Tail from previous track
		//ap.UseTail = false
		// Only Cache the Convolver state if they have been rebuilt
		ap.Logger.Debug("Cache Convolvers : " + ap.TempFilterPath)
		go ap.SaveConvolvers(ap.TempFilterPath)

	}

	// Initialise crossfeed — coefficients computed once, state restored from residuals
	cfParams := foxFilters.GetCrossfeedParams(ap.Config.Crossfeed)
	ap.Logger.Debug(errorText + fmt.Sprintf("Crossfeed: config='%s' params=%v", ap.Config.Crossfeed, cfParams))
	ap.Crossfeed = foxFilters.NewCrossfeedProcessor(cfParams, targetSampleRate)
	ap.Logger.Debug(errorText + fmt.Sprintf("CrossfeedProcessor initialised: %v", ap.Crossfeed != nil))
	// restore state only if residuals were loaded — gives gapless continuity
	if ap.ReuseFilter && ap.crossfeedState != nil {
		ap.Crossfeed.LoadState(ap.crossfeedState)
		ap.crossfeedState = nil
	}
	// Now get replaygain and compute channel gains, which may be needed for convolution depending on configuration
	ap.PrepareChannelGains()
	// the most the stages' tails come to, which a stream that nothing follows ends with (gapless.go)
	ap.gaplessTail()
	// the stream's place in the continuous stream, and its count if the input's is known (length.go)
	ap.lengthStart()

	// initialise encoder

	outputFormat := strings.ToUpper(ap.Args.OutputFormat)

	// Create the encoder with the appropriate type
	ap.Encoder = &foxAudioEncoder.AudioEncoder{
		Type:                outputFormat, // "WAV", "PCM", "FLC", "MP3"
		SampleRate:          ap.ProcRate,
		BitDepth:            ap.Args.OutBits, // ignored by adapter, used only for WAV/PCM
		NumChannels:         ap.Decoder.NumChannels,
		PlayerMaxSampleRate: ap.Args.MaxSampleRate,
		DebugFunc:           ap.Logger.Debug,
		DebugOn:             true,
	}

	// DSF (native DSD out): the PCM frames the stream carries, which its header states first: a DSD input's
	// own count, --wavlength, or a WAV input's header
	if outputFormat == "DSF" {
		rate, err := ap.dsfRate()
		if err != nil {
			return err
		}
		kind := ap.Args.DSDMod
		if kind == "" {
			kind = ap.Config.DSDOutMod
		}
		ap.Encoder.DSDRate, ap.Encoder.DSDKind, ap.Encoder.DSDGainDB = rate, kind, ap.Args.DSDOutGain
		ap.Encoder.DSDSource = ap.Args.DSDSource
		switch {
		case ap.Args.WavLength > 0:
			// the plugin's statement of what is left to play, LMS's duration with a margin (a stream's is whole
			// seconds): an upper bound, so the stream ends with the input (gipsyDSDEncoder Bounded)
			ap.Encoder.Frames = int64(ap.Args.WavLength*float64(ap.ProcRate) + 0.5)
			ap.Encoder.Bounded = true
		case ap.Decoder.DSDReader != nil:
			ap.Encoder.Frames = ap.Decoder.DSDReader.Frames
		case ap.Args.InPath != "" && ap.Decoder.Size > 0 && ap.Decoder.Size < 0xFFFFFFF0 && ap.Decoder.BitDepth > 0:
			// a WAV file's own header; a pipe's often states a placeholder, so a pipe needs --wavlength
			src := ap.Decoder.Size / int64(ap.Decoder.NumChannels*ap.Decoder.BitDepth/8)
			ap.Encoder.Frames = int64(math.Round(float64(src) * float64(ap.ProcRate) / float64(ap.Decoder.SampleRate)))
		}
		// note mode (gapless.go): the stream ends where its data does: short by what it holds for a next track, longer
		// by what the last track held for it and by the tails it plays out (each at most tailOut), so the header states an
		// upper bound with room for both, and the encoder never fills to it nor cuts the stream at it
		if ap.noteMode() && ap.Encoder.Frames > 0 {
			ap.Encoder.Frames += 2 * ap.gapless.tailOut
			ap.Encoder.Bounded = true
		}
		// the exact-length round (length.go): the input's count is known, so the header states the stream's DSD samples
		// to the sample: what the last track's encoder held for this one, this stream's frames, up to a whole group of
		// 4 bytes a channel (the loop's silence, under 32 samples; squeezelite plays whole groups)
		if ap.length.exact && rate%ap.ProcRate == 0 {
			var heldBits int64
			if res := ap.gapless.res; ap.gapless.carry && res != nil && res.Gate && res.Encoder != nil {
				heldBits = res.EncBits
			}
			ap.Encoder.Frames, ap.Encoder.Bounded = ap.length.stated, false
			ap.Encoder.DSDCount = (heldBits + ap.length.stated*int64(rate/ap.ProcRate) + 31) / 32 * 32
			// the exact contract, held (decided at the start): the samples this stream carries, the encoder's own hold
			// (its interpolator's frames, an adaptive loop's look-ahead) left for the next, down to a whole group
			if ap.length.fixed && ap.endHold() {
				hb := gipsyDSDEncoder.HeldBack(rate, ap.ProcRate, kind)
				ap.Encoder.DSDCount = max(heldBits+ap.length.stated*int64(rate/ap.ProcRate)-hb, 0) / gipsyDSDEncoder.Group * gipsyDSDEncoder.Group
				ap.Encoder.DSDHeldAt = true
			}
		}
		ap.Logger.Debug(errorText + fmt.Sprintf("DSF out: DSD %d, %s for a %s source, gain %.2f dB, %d frames at %d Hz (at most: %v)",
			ap.Encoder.DSDRate, ap.Encoder.DSDKind, map[bool]string{false: "DSD", true: "PCM"}[ap.Encoder.DSDSource == "pcm"],
			ap.Encoder.DSDGainDB, ap.Encoder.Frames, ap.ProcRate, ap.Encoder.Bounded))
	} else if outputFormat == "WAV" || outputFormat == "PCM" { // Size calculation is only relevant for uncompressed formats (WAV/PCM)
		maxSafeSize := int64(math.MaxUint32) - 36
		if ap.Decoder.Size != 0 {
			ap.Encoder.Size = int64(ap.Decoder.Size) * int64(ap.Args.OutBits) / int64(ap.Decoder.BitDepth)
			if ap.Upsampler != nil { // whole frames at the new rate
				frameBytes := int64(ap.Decoder.NumChannels) * int64(ap.Args.OutBits/8)
				ap.Encoder.Size = int64(math.Round(float64(ap.Encoder.Size/frameBytes)*float64(ap.ProcRate)/float64(ap.Decoder.SampleRate))) * frameBytes
			}
			if ap.Encoder.Size > maxSafeSize {
				ap.Encoder.Size = 0
			}
		}
		// --wavlength: the caller knows the length (raw input has none, and a decoder's header into a
		// pipe can be wrong), so the header states that many whole frames; past what a WAV can state,
		// unknown.
		if ap.Args.WavLength > 0 {
			frameBytes := int64(ap.Decoder.NumChannels) * int64(ap.Args.OutBits/8)
			ap.Encoder.Size = int64(ap.Args.WavLength*float64(ap.ProcRate)) * frameBytes
			if ap.Encoder.Size > maxSafeSize || ap.Encoder.Size < 0 {
				ap.Encoder.Size = 0
			}
			ap.Logger.Debug(errorText + fmt.Sprintf("WAV length stated: %.3f s, %d bytes", ap.Args.WavLength, ap.Encoder.Size))
		}
		// note mode (gapless.go): room for what the last track held for this one and the tails a stream that nothing
		// follows plays out (an upper bound, as --wavlength's is: the stream ends first)
		if ap.noteMode() && ap.Encoder.Size > 0 {
			frameBytes := int64(ap.Decoder.NumChannels) * int64(ap.Args.OutBits/8)
			if s := ap.Encoder.Size + 2*ap.gapless.tailOut*frameBytes; s <= maxSafeSize {
				ap.Encoder.Size = s
			} else {
				ap.Encoder.Size = 0
			}
		}
		// the exact-length round (length.go): the input's count is known, so the header states the stream's frames;
		// past what a WAV can state, unknown
		if ap.length.exact {
			frameBytes := int64(ap.Decoder.NumChannels) * int64(ap.Args.OutBits/8)
			if ap.Encoder.Size = ap.length.stated * frameBytes; ap.Encoder.Size > maxSafeSize {
				ap.Encoder.Size = 0
			}
		}
	} else {
		// For FLAC/MP3/AAC, size is unknown; set to 0 (adapter ignores it)
		if outputFormat == "FLC" {
			if ap.AppSettings.SoxExe == "" {
				return fmt.Errorf("output format %s requires SoxExe to be set", outputFormat)
			}
			ap.Encoder.AdapterPath = ap.AppSettings.SoxExe
		}
		if outputFormat == "MP3" {
			if ap.AppSettings.LameExe == "" {
				return fmt.Errorf("output format %s requires LameExe to be set", outputFormat)
			}
			ap.Encoder.AdapterPath = ap.AppSettings.LameExe
		}
		ap.Encoder.Size = 0
	}

	// Set output file if provided
	if ap.Args.OutPath != "" {
		ap.Encoder.Filename = ap.Args.OutPath
	}

	// Initialize the encoder (this will start adapter binary e.g. Sox for adapter formats)
	err = ap.Encoder.Initialise()
	if err != nil {
		ap.Logger.Error(errorText + "Error Initialising Audio Encoder: " + err.Error())
		return err
	}
	// note mode (gapless.go): DSF out closes held for a next track that goes on from it, and goes on from the last
	if ap.noteMode() {
		ap.Encoder.EndHold = ap.endHold
		if d := ap.Encoder.DSDEncoder; d != nil && ap.gapless.carry && ap.gapless.res.Encoder != nil {
			if err := d.SetState(ap.gapless.res.Encoder); err != nil {
				ap.Logger.Warn(errorText + "DSF out starts afresh: " + err.Error())
			} else {
				ap.Logger.Info(errorText + "DSF out goes on from the last track")
			}
		}
	}
	ap.lengthReport(false)
	// Finished
	return nil
}

// dsfRate: DSF out's DSD rate: a flag, else the player's DSDOutRate, else the source's; then the max (a flag, else the
// player's)
func (ap *AudioProcessor) dsfRate() (int, error) {
	rate, source := ap.Args.DSDRate, ap.Args.DSDRateSource
	if rate == 0 && !source {
		if r, err := strconv.Atoi(ap.Config.DSDOutRate); err == nil {
			rate = r
		} else {
			source = true // "source", or nothing set
		}
	}
	if source {
		if ap.Decoder.DSDReader == nil {
			return 0, fmt.Errorf("DSF out at the source's rate needs a DSD input")
		}
		rate = ap.Decoder.DSDReader.DSDRate
	}
	max := ap.Args.DSDMax
	if max == 0 {
		max = ap.Config.DSDOutMax
	}
	if max > 0 && rate > max {
		rate = max
	}
	return rate, nil
}

// ProcessAudio handles the main audio processing workflow
// uses channels to pass data between the different processing stages, which are defined as go routines
func (ap *AudioProcessor) ProcessAudio() {
	const functionName = "ProcessAudio"
	errorText := fmt.Sprintf("%s:%s: ", packageName, functionName)

	var wg sync.WaitGroup
	exitCode := 0
	isPipe := false

	if stat, _ := os.Stdout.Stat(); (stat.Mode() & os.ModeCharDevice) == 0 {
		isPipe = true
		ap.Logger.Debug(errorText + "Data sourced from stdin")
	}

	myOS := runtime.GOOS
	// bumping these up does not seem to make much difference1
	decodedBuffer, splitterBuffer, channelBuffer, mergedBuffer, feedbackBuffer := 4, 2, 1, 1, 1
	// for windows we need to be mindful of LMS socketwrapper, which kills processes 2 seconds after load completes
	// we therefore need to keep the buffer sizes small, on Linux no such issues
	if myOS == "windows" {
		decodedBuffer, splitterBuffer, channelBuffer, mergedBuffer, feedbackBuffer = 4, 2, 1, 1, 1
	}

	var (
		DecodedSamplesChannel chan [][]float64
		audioChannels         = make([]chan []float64, ap.Decoder.NumChannels)
		convolvedChannels     = make([]chan []float64, ap.Decoder.NumChannels)
		//scaledChannels        = make([]chan []byte, ap.Decoder.NumChannels)
		finishedChannel chan bool
		mergedChannel   chan [][]float64
	)

	finishedChannel = make(chan bool, feedbackBuffer)

	DecodedSamplesChannel = make(chan [][]float64, decodedBuffer)
	mergedChannel = make(chan [][]float64, mergedBuffer)

	for i := range audioChannels {
		audioChannels[i] = make(chan []float64, splitterBuffer)
		convolvedChannels[i] = make(chan []float64, channelBuffer)
		//scaledChannels[i] = make(chan []byte, channelBuffer)
	}

	//We do not use feedback on windows either!
	//if myOS != "windows" {

	//}
	ap.Logger.Debug(errorText + "Setup Audio Channels")
	meters := ap.attachMeters(true)
	lead := ap.attachLead()
	entry := DecodedSamplesChannel
	if lead != nil {
		entry = ap.entryStage(DecodedSamplesChannel, &wg)
	}

	wg.Add(1)
	go ap.channelSplitter(ap.upsampleStage(ap.tailStage(ap.countStage(entry, decodedBuffer, &wg), decodedBuffer, &wg), decodedBuffer, &wg), audioChannels, &wg)
	encodeIn := ap.lengthStage(mergedChannel, &wg) // the exact count, the last stage before the encoder (length.go)

	// Convolution setup
	ap.Logger.Debug(errorText + "Setting up Channel Convolver...")
	if len(ap.Convolvers) == 0 {
		ap.Logger.FatalError(errorText + "No convolvers initialised - cannot process audio")
		os.Exit(1)
	}

	for i := range ap.Decoder.NumChannels {
		//ap.Convolvers[i].DebugOn = false //true
		//ap.Convolvers[i].DebugFunc = ap.Logger.Debug
		wg.Add(1)

		go func(ch int) {
			defer func() {
				close(convolvedChannels[ch])
				ap.Logger.Debug(errorText + fmt.Sprintf("Convolution channel %d closed", ch))
				wg.Done()
			}()
			// convolve .2 second of audio at a time

			ap.Convolvers[ch].ConvolveChannel(audioChannels[ch], convolvedChannels[ch])
		}(i)
	}

	// Channel merging
	ap.Logger.Debug(errorText + "Setting up Channel Merger...")
	wg.Add(1)
	go func() {
		defer func() {
			close(mergedChannel)
			ap.Logger.Debug(errorText + "Merge channel closed")

			wg.Done()

		}()
		ap.mergeChannels(convolvedChannels, mergedChannel)
	}()

	// Encoding
	ap.Logger.Debug(errorText + "Setting up Encoder... ")
	wg.Add(1)
	go func() {
		defer func() {

			if finishedChannel != nil {
				finishedChannel <- true
			}
			ap.Logger.Debug(errorText + fmt.Sprintf("Encoding Done... %d", exitCode))
			wg.Done()
		}()
		err := ap.Encoder.EncodeSamplesChannel(encodeIn)
		if lead != nil { // what the reader has not taken yet, before the encoding is done
			if cerr := lead.Close(); err == nil && cerr != nil {
				err = fmt.Errorf("%s:%s: %w", packageName, functionName, cerr)
			}
		}
		if err != nil {
			if isPipe {
				switch {
				case errors.Is(err, syscall.EPIPE):
					ap.Logger.Error(errorText + "encoder: broken pipe (SIGPIPE) - output closed")
					ap.Terminate()
					//exitCode = 2
					//return
				case errors.Is(err, io.ErrClosedPipe):
					ap.Logger.Error(errorText + "encoder: output pipe closed prematurely")
					ap.Terminate()
					//exitCode = 3
					//return
				}
			}
			ap.Logger.Error(errorText + fmt.Errorf("encoder error: %w", err).Error())
			ap.Terminate()
			exitCode = 1
		}
		ap.Logger.Debug(errorText + "Finished Encoding... ")
	}()

	// Decoding
	wg.Add(1)
	ap.Logger.Debug(errorText + "Decoding Data...")
	go func() {
		defer func() {
			ap.Logger.Debug(errorText + "Finished Decoding Data...")
			close(DecodedSamplesChannel)

			writerFinished := false
			for {
				if finishedChannel != nil {
					writerFinished = <-finishedChannel
					if writerFinished {
						break
					}
					time.Sleep(100 * time.Millisecond)
				}

			}
			ap.Logger.Debug(errorText + "Decoding and writer Done...")
			wg.Done()

		}()
		ap.Decoder.DecodeSamples(DecodedSamplesChannel)
	}()

	ap.Logger.Debug(errorText + "Waiting for processing to complete...")
	stopSpeed := ap.reportSpeed(meters)
	wg.Wait()
	stopSpeed()

}

// attachMeters gives each stage of the route its meter before any of them starts: the decoder's reader (ours for DSD,
// else the WAV reader, as DecodeSamples picks), the upsampler (with one), with the DSP each channel's convolver and the
// merger, then the encoder; and the encoder its fill, the engine's speed. The splitter only slices and sends.
func (ap *AudioProcessor) attachMeters(dsp bool) []*gipsySpeed.Meter {
	dec := &gipsySpeed.Meter{Name: "decoder", Rate: ap.Decoder.SampleRate}
	if ap.Decoder.DSDReader != nil {
		ap.Decoder.DSDReader.Meter = dec
	} else if ap.Decoder.WavDecoder != nil {
		ap.Decoder.WavDecoder.Meter = dec
	}
	ms := []*gipsySpeed.Meter{dec}
	ap.upMeter, ap.mergeMeter = nil, nil
	if ap.Upsampler != nil {
		ap.upMeter = &gipsySpeed.Meter{Name: "upsampler", Rate: ap.ProcRate}
		ms = append(ms, ap.upMeter)
	}
	if dsp {
		for i, c := range ap.Convolvers {
			c.Meter = &gipsySpeed.Meter{Name: fmt.Sprintf("convolver %d", i+1), Rate: ap.ProcRate}
			ms = append(ms, c.Meter)
		}
		ap.mergeMeter = &gipsySpeed.Meter{Name: "merger", Rate: ap.ProcRate}
		ms = append(ms, ap.mergeMeter)
	}
	ap.Encoder.Meter = &gipsySpeed.Meter{Name: "encoder", Rate: ap.Encoder.SampleRate}
	ap.Encoder.Fill = &gipsySpeed.Fill{}
	ap.runs = &gipsySpeed.Runs{}
	ap.Meters = append(ms, ap.Encoder.Meter)
	return ap.Meters
}

// attachLead (0.1.72.46): with the encoder writing stdout itself, its writes go through a lead (gipsySpeed.Lead), its runs
// from LeadLow seconds to LeadRun more than the pipeline holds (up to LeadMax), timed by ap.runs (entryStage); nil
// without one. Not on Windows: there LMS runs
// every pipeline through its socketwrapper, which gives each process 2 s once the first in the pipeline has ended, then
// ends it (socketwrapper.cpp, tidy: waittimeout 2000, TerminateProcess), and the reader takes a lead at the player's
// pace, so a track's last seconds would be cut. There the fill alone, as before.
func (ap *AudioProcessor) attachLead() *gipsySpeed.Lead {
	if runtime.GOOS == "windows" || !ap.Encoder.WritesStdout() {
		return nil
	}
	bps := float64(ap.Encoder.SampleRate*ap.Encoder.NumChannels) * float64(ap.Encoder.BitDepth/8)
	if ap.Encoder.DSDEncoder != nil {
		bps = float64(ap.Encoder.DSDRate*ap.Encoder.NumChannels) / 8
	}
	if bps <= 0 {
		return nil
	}
	lead := gipsySpeed.NewLead(os.Stdout, bps, gipsySpeed.LeadLow, gipsySpeed.LeadRun, gipsySpeed.LeadMax, ap.runs)
	ap.Encoder.Out = lead
	return lead
}

// entryStage (0.1.72.46): the decoder's blocks passed on, each told to the runs (gipsySpeed.Runs.In) with the time the
// pipeline took it in, once the next has gone in: the input's last block, often short, never counts (the time to take
// it in is a whole block's). Its end told too (Runs.End).
func (ap *AudioProcessor) entryStage(in chan [][]float64, wg *sync.WaitGroup) chan [][]float64 {
	out := make(chan [][]float64)
	rate := float64(ap.Decoder.SampleRate)
	wg.Add(1)
	go func() {
		defer func() {
			ap.runs.End()
			close(out)
			wg.Done()
		}()
		var secs float64
		var at time.Time
		for blk := range in {
			out <- blk
			now := time.Now()
			if secs > 0 {
				ap.runs.In(secs, at)
			}
			secs, at = 0, now
			if len(blk) > 0 && rate > 0 {
				secs = float64(len(blk[0])) / rate
			}
		}
	}()
	return out
}

// stageTime: one stage's own time, in ms of work a second of audio (its share of one core, times ten).
type stageTime struct {
	Name string  `json:"name"`
	MS   float64 `json:"ms"`
}

// stageTimes: each metered stage's own time so far, in the route's order; a stage with no audio yet left out.
func stageTimes(meters []*gipsySpeed.Meter) []stageTime {
	var out []stageTime
	for _, m := range meters {
		if a, b := m.Figures(); a > 0 {
			out = append(out, stageTime{m.Name, math.Round(10000*b/a) / 10})
		}
	}
	return out
}

// StageTimes: the end line's figures, "decoder 1.2 ms, upsampler 15.3 ms, ...", ms of work a second of audio; "" with
// none (the engine's figures for each stage alone, beside the processes' CPU the page shows: ledger THE AVX2 LOOPS).
func (ap *AudioProcessor) StageTimes() string {
	var parts []string
	for _, s := range stageTimes(ap.Meters) {
		parts = append(parts, fmt.Sprintf("%s %.1f ms", s.Name, s.MS))
	}
	return strings.Join(parts, ", ")
}

// speedFigures: one report's figures (reportSpeedTo): the busiest stage; the speed's audio (seconds) and time, audio
// over busy being how many times faster than real time the engine runs flat out; whether the fill is over; the runs
// measured while it plays.
type speedFigures struct {
	Stage       string
	Audio, Busy float64
	Over        bool
	Runs        int
}

// reportSpeed: from its start and every second while the engine runs, its own speed for the SqueezeDSP page
// (Status.pm): its pid; its speed, the runs' of the last RunsKeep (0.1.72.46: gipsySpeed.Runs, the engine refilling its
// lead flat out while it plays) once there are any, else its fill's (gipsySpeed.Fill: the audio it wrote flat out at the
// stream's start and the time that took, its waits for the player left out); whether the fill is over; the runs
// measured; and its busiest stage by its own work (gipsySpeed.Slowest of the meters), the one to speed up. Neither the
// processes' CPU (the engine spreads its stages over cores), nor its output's timing once the player paces it (the
// stages before the output work while it waits), nor any stage's own time (the stages wait on each other) says how
// fast it can run (ledger THE PAGE'S SPEED WAS PER CPU-SECOND). Written whole, then renamed.
func (ap *AudioProcessor) reportSpeed(meters []*gipsySpeed.Meter) (stop func()) {
	fill, runs := ap.Encoder.Fill, ap.runs
	return reportSpeedTo(filepath.Join(ap.AppSettings.TempDataFolder, "speed_"+ap.Args.CleanClientID+".json"),
		func() (speedFigures, bool) {
			var f speedFigures
			f.Stage, _, _, _ = gipsySpeed.Slowest(meters...)
			f.Audio, f.Busy, f.Over = fill.Figures()
			ra, rs, n := runs.Figures()
			if rs > 0 {
				f.Audio, f.Busy = ra, rs
			}
			f.Runs = n
			return f, f.Audio > 0 || f.Stage != ""
		}, func() []stageTime { return stageTimes(meters) }, time.Second)
}

// RunsSpeed (0.1.72.46): the end line's speed while the engine played, "8.3x real time, 21 runs" (the runs of the last
// RunsKeep, as the report's); "" with none.
func (ap *AudioProcessor) RunsSpeed() string {
	audio, secs, n := ap.runs.Figures()
	if secs <= 0 {
		return ""
	}
	return fmt.Sprintf("%.1fx real time, %d runs", audio/secs, n)
}

// reportSpeedTo: the pid to path at once, then every period and once more at stop, with speed's figures once there
// are any (ok) and, with stages, each stage's own time (SqueezeDSP fork: Status.pm's stage_ms). Written from the start,
// a server with no /proc knows the engine runs by the file's age (Status.pm).
func reportSpeedTo(path string, speed func() (speedFigures, bool), stages func() []stageTime, every time.Duration) (stop func()) {
	write := func() {
		figures := map[string]any{"pid": os.Getpid()}
		if f, ok := speed(); ok {
			figures["stage"], figures["audio"], figures["busy"], figures["over"], figures["runs"] = f.Stage, f.Audio, f.Busy, f.Over, f.Runs
		}
		if stages != nil {
			figures["stages"] = stages()
		}
		b, err := json.Marshal(figures)
		if err != nil {
			return
		}
		tmp := path + "." + strconv.Itoa(os.Getpid())
		if os.WriteFile(tmp, b, 0644) == nil {
			os.Rename(tmp, path)
		}
	}
	write()
	done, finished := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		tick := time.NewTicker(every)
		defer tick.Stop()
		for {
			select {
			case <-done:
				write()
				return
			case <-tick.C:
				write()
			}
		}
	}()
	return func() {
		close(done)
		<-finished
	}
}

// upsampleStage: with an upsampler, a stage between the decoder and the rest that takes each block up to ProcRate
// and, when the decoder is done, the filters' last output (Flush), unless a track follows that goes on from its state
// (note mode, gapless.go: then its last outputs come out at that track's start); without one, the decoder's own channel.
func (ap *AudioProcessor) upsampleStage(in chan [][]float64, buffer int, wg *sync.WaitGroup) chan [][]float64 {
	if ap.Upsampler == nil {
		return in
	}
	out := make(chan [][]float64, buffer)
	wg.Add(1)
	go func() {
		defer func() {
			close(out)
			wg.Done()
		}()
		for blk := range in {
			t0 := time.Now()
			if up := ap.Upsampler.Process(blk); len(up) > 0 && len(up[0]) > 0 {
				ap.upMeter.Add(len(up[0]), time.Since(t0))
				out <- up
			} else {
				ap.upMeter.Add(0, time.Since(t0))
			}
		}
		if ap.noteMode() && ap.endHold() {
			return
		}
		t0 := time.Now()
		if up := ap.Upsampler.Flush(); len(up) > 0 && len(up[0]) > 0 {
			ap.upMeter.Add(len(up[0]), time.Since(t0))
			out <- up
		}
	}()
	return out
}

// Splits channels for parallel convolution and adds delays if they have been specificed
func (ap *AudioProcessor) channelSplitter(
	inputCh chan [][]float64,
	outputChs []chan []float64,
	WG *sync.WaitGroup,

) {
	errorText := packageName + "Channel Splitter: "
	chunkCounter := 0
	channelCount := ap.Decoder.NumChannels
	go func() {

		defer func() {

			for i := range channelCount {

				close(outputChs[i])
				ap.Logger.Debug(errorText + fmt.Sprintf("splitter closed channel %d", i))

			}
			WG.Done()
			ap.Logger.Debug(packageName + ":Channel Splitter Done... " +
				fmt.Sprintf("%d chunks processed", chunkCounter))
		}()
		retainStart := 0
		chunksizeLogged := false
		// report length of delay buffers
		for i := range channelCount {
			ap.Logger.Debug(packageName + ":Channel Delay Buffer... " +
				fmt.Sprintf("Channel %d, Length %d", i, len(ap.Delay.Buffers[i])))
		}

		for chunk := range inputCh {
			for i := range channelCount {

				if ap.Delay.Delays[i] > 0 {

					// Prepend buffer to current data combined is now longer than the chunk
					combined := append(ap.Delay.Buffers[i], chunk[i]...)
					// We should always output the full chunk and no more
					//output delay plus initial part of the chunk to give us a chunks worth of data
					outputChs[i] <- combined[:len(chunk[i])]
					if !chunksizeLogged {
						ap.Logger.Debug(errorText + fmt.Sprintf("Chunk size %d", len(chunk[i])))

						chunksizeLogged = true

					}
					// Retain last `delay.delays[i]` samples for next iteration
					retainStart = max(len(combined)-ap.Delay.Delays[i], 0)
					ap.Delay.Buffers[i] = combined[retainStart:]
				} else {
					outputChs[i] <- chunk[i] // No delay
				}
			}
			chunkCounter++

		}
	}()
}

// We are merging channels following convolution, and we are also applying individual channel gains and an
// overall width adjustment at this stage, as this is more efficient than doing it before convolution
func (ap *AudioProcessor) mergeChannels(inputChannels []chan []float64, outputChannel chan [][]float64) {
	const functionName = "mergeChannels"
	errorText := fmt.Sprintf("%s:%s", packageName, functionName)
	defer ap.Logger.Debug(errorText + ": All channels drained")
	numChannels := ap.Decoder.NumChannels
	channelGains := ap.ChannelGains

	var sigmaGain, deltaGain float64
	var applyWidth bool
	if ap.Config.Width != 0.0 && numChannels > 1 {
		applyWidth = true
		sigmaGain, deltaGain = foxFilters.GetWidthCoefficients(ap.Config.Width)
		ap.Logger.Debug(errorText + fmt.Sprintf(" : Setup, width delta %f, sigma %f", deltaGain, sigmaGain))
	}

	activeChannels := numChannels
	var mergedChunks [][]float64
	var mid, side float64
	allReady := true
	chunksizeLogged := false
	var work time.Duration // this block's arithmetic, for its meter: the receives and the send wait on others

	for activeChannels > 0 {
		// Reset mergedChunks for new data
		mergedChunks = make([][]float64, numChannels) // Read from all channels, blocking until data is available
		for i := range numChannels {
			if inputChannels[i] == nil {
				continue // Channel closed
			}

			chunk, ok := <-inputChannels[i]
			if !ok {
				inputChannels[i] = nil
				activeChannels--
				ap.Logger.Debug(errorText + fmt.Sprintf(" : Input Channel %d closed", i))
				continue
			}
			if !chunksizeLogged {
				ap.Logger.Debug(errorText + fmt.Sprintf("Chunk size %d", len(chunk)))

				chunksizeLogged = true

			}
			// Apply gain
			t0 := time.Now()
			for j := range chunk {
				chunk[j] *= channelGains[i]
			}
			work += time.Since(t0)
			mergedChunks[i] = chunk
		}

		// Check if all active channels have data
		allReady = true
		for i := range numChannels {
			if inputChannels[i] != nil && mergedChunks[i] == nil {
				allReady = false
				break
			}
		}
		if allReady {
			t0 := time.Now()

			if applyWidth {
				for mc := range mergedChunks[0] {
					mid = (mergedChunks[0][mc] + mergedChunks[1][mc]) * sigmaGain  // sigmaGain is the 'mid' coefficient
					side = (mergedChunks[0][mc] - mergedChunks[1][mc]) * deltaGain // deltaGain is the 'side' coefficient
					mergedChunks[0][mc] = mid + side                               // Left output (reconstructed)
					mergedChunks[1][mc] = mid - side                               // Right output (reconstructed)
				}
			}

			// in the allReady block:
			if ap.Crossfeed != nil && numChannels > 1 {
				ap.Crossfeed.ProcessChunk(mergedChunks[0], mergedChunks[1])
			}
			ap.mergeMeter.Add(len(mergedChunks[0]), work+time.Since(t0))
			work = 0
			// Send the processed chunks
			outputChannel <- mergedChunks

		}
	}
}

func (ap *AudioProcessor) Terminate() {
	const functionName = "Terminate"

	peakDBFS := PeakDBFS(ap.Encoder.Peak)
	if d := ap.Encoder.DSDEncoder; d != nil {
		ap.Logger.Info(fmt.Sprintf("%s: DSF out (cut short): peak %.4f of full swing, %d samples limited at %.2f, %d modulator resets",
			packageName, d.Peak, d.Limited, gipsyDSDEncoder.Limit, d.Resets))
	}
	ErrorText := fmt.Sprintf("%s:%s: ", packageName, functionName)
	ap.Logger.Info(ErrorText + "Closing because of output termination...")
	//11423050 samples, 241703.3034 ms (659.6813 init), 1.0717 * realtime, peak -8.6029 dBfs
	expectedSeconds := float64(ap.Encoder.NumSamples) / float64(ap.Encoder.SampleRate)
	relativeSpeed := expectedSeconds / 1.0

	rawPeakDBFS := PeakDBFS(ap.Decoder.RawPeak)
	ap.Logger.Debug(ErrorText + fmt.Sprintf("rawPeak %f Input Peak %f OutputPeak %f Diff %f", ap.Decoder.RawPeak, rawPeakDBFS, peakDBFS, peakDBFS-rawPeakDBFS))

	// Go code to match C# log format
	ap.Logger.Info(fmt.Sprintf(
		"%d samples, %.3f ms (%.3f init), %.4f * realtime, peak %.4f dBfs, input peak %.4f dBfs \n",
		ap.Encoder.NumSamples, // n (samples)
		0.0,                   // Convert seconds to milliseconds (e.g., 103.810 ms)
		0.0,                   // Convert init time to milliseconds
		relativeSpeed,         // realtime/runtime (e.g., 1.255)
		peakDBFS,              // dBfs peak value
		rawPeakDBFS,           // Input peak value
	))
	if st := ap.StageTimes(); st != "" {
		ap.Logger.Info("stage time per second of audio: " + st)
	}
	if rs := ap.RunsSpeed(); rs != "" {
		ap.Logger.Info("speed while playing: " + rs)
	}
	// Close the output file after all processing is done
	if ap.Encoder != nil {
		ap.Encoder.Close()
	}
	os.Exit(1)

}

func PeakDBFS(peak float64) float64 {
	if peak == 0 {
		return math.Inf(-1)
	}
	return 20 * math.Log10(peak)
}

// ByPassProcess handles the  processing workflow when bypass is enabled and the input and output formats are different
// uses channels to pass data between the different processing stages, which are defined as go routines
func (ap *AudioProcessor) ByPassProcess() {
	const functionName = "ByPassProcess"
	errorText := fmt.Sprintf("%s:%s: ", packageName, functionName)
	ap.Logger.Info(errorText + "Bypass mode enabled - minimal processing")

	// Bypass prioritises low latency over throughput
	ap.ProcessingMode = foxUtils.ModeRealtime
	ap.Decoder.ConfigureFrameSize(ap.ProcessingMode)

	var wg sync.WaitGroup
	exitCode := 0
	isPipe := false

	if stat, _ := os.Stdout.Stat(); (stat.Mode() & os.ModeCharDevice) == 0 {
		isPipe = true
		ap.Logger.Debug(errorText + "Data sourced from stdin")
	}

	DecodedSamplesChannel := make(chan [][]float64, 1)
	finishedChannel := make(chan bool, 1)
	meters := ap.attachMeters(false)
	encodeIn := ap.lengthStage(ap.upsampleStage(ap.countStage(DecodedSamplesChannel, 1, &wg), 1, &wg), &wg)

	// Encoding
	wg.Add(1)
	ap.Logger.Debug(errorText + "Setting up Encoder...")
	go func() {
		defer func() {
			finishedChannel <- true
			wg.Done()
			ap.Logger.Debug(errorText + fmt.Sprintf("Encoding Done... %d", exitCode))
		}()
		err := ap.Encoder.EncodeSamplesChannel(encodeIn)
		if err != nil {
			if isPipe {
				switch {
				case errors.Is(err, syscall.EPIPE):
					ap.Logger.Error(errorText + "encoder: broken pipe (SIGPIPE) - output closed")
					ap.Terminate()
				case errors.Is(err, io.ErrClosedPipe):
					ap.Logger.Error(errorText + "encoder: output pipe closed prematurely")
					ap.Terminate()
				}
			}
			ap.Logger.Error(errorText + fmt.Errorf("encoder error: %w", err).Error())
			ap.Terminate()
			exitCode = 1
		}
		ap.Logger.Debug(errorText + "Finished Encoding...")
	}()

	// Decoding
	wg.Add(1)
	ap.Logger.Debug(errorText + "Decoding Data...")
	go func() {
		defer func() {
			close(DecodedSamplesChannel)
			ap.Logger.Debug(errorText + "Finished Decoding Data...")
			writerFinished := false
			for !writerFinished {
				writerFinished = <-finishedChannel
				if !writerFinished {
					time.Sleep(200 * time.Millisecond)
				}
			}
			wg.Done()
			ap.Logger.Info(errorText + "Decoding and writer Done...")
		}()
		ap.Decoder.DecodeSamples(DecodedSamplesChannel)
	}()

	ap.Logger.Debug(errorText + "Waiting for processing to complete...")
	stopSpeed := ap.reportSpeed(meters)
	wg.Wait()
	stopSpeed()

	ap.Logger.Debug(errorText + "Processing Complete...")
	if err := ap.Encoder.Close(); err != nil {
		ap.Logger.Warn(errorText + " Error closing output file: " + err.Error())
	}
}

func (ap *AudioProcessor) SaveConvolvers(filename string) error {
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := gob.NewEncoder(file)
	// This will automatically use your custom GobEncode methods
	return encoder.Encode(ap.Convolvers)
}

func (ap *AudioProcessor) LoadConvolvers(filename string, minTime string) error {
	cacheInfo, err := os.Stat(filename)
	ap.Logger.Debug("LoadConvolvers" + " Checking cache file: " + filename)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("cache file does not exist: %s", filename)
		}
		return err // Other file error
	}
	// Check if cache is too old
	modTime := cacheInfo.ModTime()
	cacheTime := modTime.UTC().Format(time.RFC3339)
	if cacheTime < minTime {
		return fmt.Errorf("cache is outdated: cache=%s, required=%s",
			cacheTime, minTime)
	}
	ap.Logger.Debug("LoadConvolvers" + " Cache OK proceeding to load...")
	file, err := os.Open(filename)
	if err != nil {
		return err // No backup exists - that's OK
	}
	defer file.Close()

	decoder := gob.NewDecoder(file)
	// This will automatically use your custom GobDecode methods
	return decoder.Decode(&ap.Convolvers)
}

// calculateExpiryTime returns the time when the cache will expire it is based off the remaining track time plus 10 seconds
func calculateExpiryTime(trackDuration float64, processingTime float64) int64 {
	remainingTrackTime := trackDuration - processingTime
	if remainingTrackTime < 0 {
		remainingTrackTime = 0
	}
	// Add 10 seconds to the remaining track time
	expiryDuration := time.Duration((remainingTrackTime + 10) * float64(time.Second))
	return time.Now().Add(expiryDuration).Unix()
}

func (ap *AudioProcessor) SavePreviousTrack() {
	cache := PreviousTrackCache{
		TrackURL:  ap.Args.TrackURL,
		TrackGain: ap.ReplayGain,
	}
	file, err := os.Create(ap.previousTrackPath)
	if err != nil {
		ap.Logger.Warn("SavePreviousTrack: failed to create file: " + err.Error())
		return
	}
	defer file.Close()
	if err := gob.NewEncoder(file).Encode(cache); err != nil {
		ap.Logger.Warn("SavePreviousTrack: encode error: " + err.Error())
	}
	ap.Logger.Debug(fmt.Sprintf("SavePreviousTrack: saved URL=%s gain=%.2f dB", cache.TrackURL, cache.TrackGain))
}

func (ap *AudioProcessor) LoadPreviousTrack() {
	file, err := os.Open(ap.previousTrackPath)
	if err != nil {
		// Normal on first run
		ap.Logger.Debug("LoadPreviousTrack: no previous track file: " + err.Error())
		return
	}
	defer file.Close()
	var cache PreviousTrackCache
	if err := gob.NewDecoder(file).Decode(&cache); err != nil {
		ap.Logger.Warn("LoadPreviousTrack: decode error: " + err.Error())
		return
	}
	ap.PreviousTrackURL = cache.TrackURL
	ap.PreviousTrackGain = cache.TrackGain
	ap.Logger.Debug(fmt.Sprintf("LoadPreviousTrack: URL=%s gain=%.2f dB", ap.PreviousTrackURL, ap.PreviousTrackGain))
}

func (ap *AudioProcessor) SaveDSPResiduals(Duration float64, processingTime float64) error {
	// note mode (gapless.go): the stages' state only when this track held it for a next one (endHold: never a bypassed
	// stream, whose convolvers hold nothing of it); the album's gain whatever happens
	if ap.noteMode() {
		cache := DSPResidualsCache{
			Version:           CACHE_VERSION,
			SampleRate:        ap.ProcRate,
			NumChannels:       ap.Decoder.NumChannels,
			ExpiryTime:        calculateExpiryTime(Duration, processingTime),
			PreviousAlbumGain: ap.PreviousAlbumGain,
			PreviousAlbumID:   ap.PreviousAlbumID,
			TrackURL:          ap.Args.TrackURL,
		}
		if ap.endHold() {
			cache.Held = true
			cache.ConvolverStates = make([][]byte, len(ap.Convolvers))
			for i, conv := range ap.Convolvers {
				state, err := conv.EncodeState()
				if err != nil {
					return err
				}
				cache.ConvolverStates[i] = state
			}
			cache.DelayBuffers = ap.Delay
			if ap.Crossfeed != nil {
				cache.CrossfeedState = ap.Crossfeed.SaveState()
			}
			if ap.Upsampler != nil {
				s, err := ap.Upsampler.State()
				if err != nil {
					return err
				}
				cache.Upsampler = s
			}
			if d := ap.Encoder.DSDEncoder; d != nil {
				s, err := d.State()
				if err != nil {
					return err
				}
				cache.Encoder = s
				cache.EncBits = d.HeldBits()
			}
			if g := &ap.length; g.on && g.held {
				cache.Gate, cache.GateIn, cache.GateCut, cache.Pending = true, g.cumIn, g.cut, g.keep
			}
			cache.InKind, cache.InRate = "pcm", ap.Decoder.SampleRate
			if rd := ap.Decoder.DSDReader; rd != nil {
				cache.InKind, cache.InDSDRate = "dsd", rd.DSDRate
			}
			// a decode that ran to its file's end holding (not a cue sheet's track): the next file goes on from it
			if rd := ap.Decoder.DSDReader; rd != nil {
				if s, err := rd.State(); err == nil {
					cache.Reader = s
				} else {
					ap.Logger.Info(packageName + ": the next track's DSD decode starts afresh: " + err.Error())
				}
			}
		}
		if err := ap.saveResiduals(&cache); err != nil {
			return err
		}
		ap.Logger.Info(packageName + ": residual saved: " + map[bool]string{true: "every stage's state, held for the next track", false: "the album gain only"}[cache.Held])
		return nil
	}
	// Encode only the STATE (not the filter)
	convolverStates := make([][]byte, len(ap.Convolvers))
	for i, conv := range ap.Convolvers {
		state, err := conv.EncodeState() // Changed from GobEncode
		if err != nil {
			return err
		}
		convolverStates[i] = state
		//ap.Logger.Debug(fmt.Sprintf("Saving convolver ringbuffer position %d )", ap.Convolvers[i].RingPosition))
	}

	cache := DSPResidualsCache{
		Version:           CACHE_VERSION,
		SampleRate:        ap.ProcRate,
		NumChannels:       ap.Decoder.NumChannels,
		ExpiryTime:        calculateExpiryTime(Duration, processingTime),
		ConvolverStates:   convolverStates,
		DelayBuffers:      ap.Delay,
		PreviousAlbumGain: ap.PreviousAlbumGain,
		PreviousAlbumID:   ap.PreviousAlbumID, // *** NEW ***
		CrossfeedState:    ap.Crossfeed.SaveState(),
	}

	return ap.saveCacheToFile(cache)
}

// Keep the simple file helpers
func (ap *AudioProcessor) saveCacheToFile(cache interface{}) error {
	file, err := os.Create(ap.cachePath)
	if err != nil {
		return err
	}
	defer file.Close()
	return gob.NewEncoder(file).Encode(cache)
}

func (ap *AudioProcessor) loadCacheFromFile(filePath string) (*DSPResidualsCache, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var cache DSPResidualsCache
	if err := gob.NewDecoder(file).Decode(&cache); err != nil {
		return nil, err
	}
	return &cache, nil
}

// For RESIDUALS CACHE (dsp_residuals_*.gob) - state only
func (ap *AudioProcessor) LoadDSPResiduals() error {
	const functionName = "LoadDSPResiduals"
	errorText := fmt.Sprintf("%s:%s: ", packageName, functionName)
	var cache *DSPResidualsCache
	if ap.noteMode() {
		// read at the start (gaplessStart); the album's gain always, the stages' state only for the stream that
		// continues the track which held it
		if cache = ap.gapless.res; cache == nil {
			return fmt.Errorf("no residual")
		}
		ap.PreviousAlbumGain = cache.PreviousAlbumGain
		ap.PreviousAlbumID = cache.PreviousAlbumID
		if !ap.gapless.carry {
			return nil
		}
	} else {
		var err error
		cache, err = ap.loadCacheFromFile(ap.cachePath)
		if err != nil {
			return err
		}

		if cache.SampleRate != ap.ProcRate ||
			cache.NumChannels != ap.Decoder.NumChannels ||
			time.Now().Unix() > cache.ExpiryTime {
			return fmt.Errorf("cache invalid")
		}
		if cache.Version != CACHE_VERSION {
			return fmt.Errorf("cache version mismatch: got %d, want %d",
				cache.Version, CACHE_VERSION)
		}
	}

	ap.Delay = cache.DelayBuffers
	ap.PreviousAlbumGain = cache.PreviousAlbumGain
	ap.PreviousAlbumID = cache.PreviousAlbumID
	ap.crossfeedState = cache.CrossfeedState

	// Restore only the STATE (filter already loaded)
	for i := range ap.Convolvers {
		if i < len(cache.ConvolverStates) {
			err := ap.Convolvers[i].DecodeState(cache.ConvolverStates[i]) // Changed from GobDecode
			if err != nil {
				ap.Logger.Warn(errorText + fmt.Sprintf("Failed to restore convolver %d state: %v", i, err))
			}
		}
	}

	ap.Logger.Debug(errorText + "DSP Residuals loaded successfully")
	return nil
}
