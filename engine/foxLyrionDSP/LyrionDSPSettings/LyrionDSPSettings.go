package LyrionDSPSettings

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Foxenfurter/foxAudioLib/foxFilters"
	"github.com/Foxenfurter/foxAudioLib/foxLog"
	"github.com/Foxenfurter/foxAudioLib/gipsyUpsampler"
)

func InitializeSettings() (*Arguments, *AppSettings, *ClientConfig, *foxLog.Logger, error) {
	// Read command-line arguments
	myArgs, err := ReadArgs()
	if err != nil {
		displayUsage(err)
		return nil, nil, nil, nil, err
	}

	// Load application settings
	AppSettingFile := myArgs.AppName + "_config.json"
	exeDir, err := getExecutableDir()
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("could not get executable directory: %w", err)
	}

	configPath := filepath.Join(exeDir, AppSettingFile)
	myAppSettings, err := LoadAppSettings(configPath)
	if err != nil {
		return myArgs, nil, nil, nil, fmt.Errorf("settings load error: %w\n", err)
	}

	// Determine debug state (command line takes precedence)
	DebugEnabled := myArgs.Debug
	if !DebugEnabled {
		if strings.ToLower(myAppSettings.Debug) == "true" {
			DebugEnabled = true
		}
	}

	// Initialize logger
	logFilePath := filepath.Clean(myAppSettings.LogFile)                        // Sanitize path
	logger, err := foxLog.NewLogger(logFilePath, myArgs.ClientID, DebugEnabled) // Changed ClientID to UserID
	if err != nil {
		return myArgs, myAppSettings, nil, nil, fmt.Errorf("logger init error: %w", err)
	}

	// Load configuration
	myClientFilePrefix := myArgs.CleanClientID
	myConfigFile := myAppSettings.SettingsDataFolder + "/" + myClientFilePrefix + ".settings.json"
	config, err := LoadConfig(myConfigFile)
	if err != nil {
		logger.Error(fmt.Sprintf("config load error (bypass mode): %v", err))
		config = &ClientConfig{Bypass: true}
	}

	return myArgs, myAppSettings, config, logger, nil
}

func displayUsage(err error) {
	if err != nil {
		fmt.Printf("Error with Command Line: %s\n", err)
	}

	fmt.Print(`Usage: LyrionDSPSettings [options]

Options:
  --clientid=ID       (required) Client identifier (e.g., "aa:bb:cc:dd:ee:ff")
  --formatin=FORMAT   Input format: "PCM" or other supported formats (default inferred)
  --formatout=FORMAT  Output format (optional)
  --be[=true|false]   Big-endian byte order for PCM (default false)
  --input=FILE        Input file path (if not stdin)
  --output=FILE       Output file path (if not stdout)
  --bitsout=N         Output bit depth (8-32, default 24)
  --samplerate=N      Sample rate in Hz (required when --formatin=PCM); with --formatin=DSD the rate to decode to, or
                      native: the DSD rate itself, the DSP there, through the player's DSDIntegrator (--formatout=DSF)
  --bitsin=N          Input bit depth (required when --formatin=PCM)
  --channels=N        Number of audio channels (required when --formatin=PCM)
  --skip=MM:SS[.FFF]  Skip to a time position (e.g., 01:23 or 01:23.456)
  --trackurl=URL      URL for remote album detection (optional)
  --maxSampleRate=N   Maximum supported sample rate for Player (optional)
  --wavlength=SECONDS Length a WAV or DSF output's header states (optional; default from the input); for DSF an
                      upper bound: the stream ends with the input, its last block zero-padded
  --insamples=N       The frames the input will carry, exactly (optional; for raw input, which states none): the
                      output's header then states the stream's true length
  --insamples=header  The WAV header on the input pipe states its frames exactly (a decoder proven to): use it.
                      Without it a pipe's header is not taken as exact (a decoder can state the whole file after a
                      seek, or an AAC stream's padded length: measured 2026-11-15)
  --flacseek=OFFSET   A remote FLAC (--trackurl, http or https) arriving from byte OFFSET as LMS sends it (frames
                      only after a seek or a resume, aligned as LMS aligns them; 0: from its start): the engine reads
                      the stream's STREAMINFO and the first frame's number from the URL (two ranged reads) and states
                      the frames left exactly
  --tail              Play out the last track's held tail as a stream of its own (a player that needs the true length
                      first, whose next track went after the stream held for it): no input; the stages' state from the
                      residual, the same settings as that track's stream, its length stated exactly
  --dsdrate=N         DSF output: the DSD rate (2822400, 5644800, 11289600 or 22579200, or source: a DSD input's own);
                      without it, the player's DSDOutRate, else source
  --dsdmax=N          DSF output: the highest DSD rate (one of the four); without it, the player's DSDOutMax
  --dsdmod=KIND       DSF output: the modulator, standard (default), clans or adaptive
  --dsdoutgain=DB     DSF output: gain before the modulator, -24 to 6 dB (default 0: PCM full scale = full swing)
  --dsdsource=KIND    DSF output: what the source was, dsd (default: the designs for SACD's peaks) or pcm (a PCM
                      source at -6.02 dB: designs with less noise, held 2 dB over its full scale)
  --upsample=N        A PCM input resampled here, before the DSP, to N Hz, up or down (44100 to 1536000; a
                      player's PCM target, the converter's 705600 for DSF out), by the engine's own filters
  --upfilter=CD,HIRES With --upsample: the filter for a source at 48 kHz and under, then for one above (each
                      Kaiser short-, long- or xlong-, Gaussian gauss-short-, gauss-medium- or gauss-long-,
                      halfband- or slow-, then lin, int or min; or nonring; one value: both; default
                      short-int,short-lin)
  --debug           Enable debug logging


Examples:
  --clientid=aa:bb:cc:dd:ee:ff --formatin=PCM --samplerate=44100 --bitsin=16 --channels=2 --input=file.raw --output=out.wav
  --clientid=test --bitsout=32 --be --skip=1:30
`)
}

func getExecutableDir() (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("could not get executable path: %w", err)
	}
	return filepath.Dir(exePath), nil
}

func sanitizeClientID(clientID string) string {
	// Replace ':' with '_'
	clientID = strings.ReplaceAll(clientID, ":", "_")

	// Replace '-' with '_'
	clientID = strings.ReplaceAll(clientID, "-", "_")

	return clientID
}

// AppSettings holds the configuration settings from the JSON file.

type AppSettings struct {
	Partitions         string `json:"partitions"`
	ConvolverGain      string `json:"convolvergain"`
	SoxExe             string `json:"soxExe"`
	FFmpegExe          string `json:"ffmpegExe"`
	LameExe            string `json:"lameExe"`
	Tail               string `json:"tail"`
	Dither             string `json:"dither"`
	Debug              string `json:"debug"`
	LogFile            string `json:"logFile"`
	Gain               string `json:"gain"`
	PluginDataFolder   string `json:"pluginDataFolder"`
	SettingsDataFolder string `json:"settingsDataFolder"`
	ImpulseDataFolder  string `json:"impulseDataFolder"`
	TempDataFolder     string `json:"tempDataFolder"`
	LMSHost            string `json:"lmsHost"`
	LMSPort            string `json:"lmsPort"`
}

// appConfigWrapper is used to parse the nested JSON structure
type appConfigWrapper struct {
	Settings AppSettings `json:"settings"`
}

// LoadAppSettings reads the JSON file and unmarshals it into AppSettings
func LoadAppSettings(filePath string) (*AppSettings, error) {

	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	byteValue, _ := io.ReadAll(file)

	var wrapper appConfigWrapper
	err = json.Unmarshal(byteValue, &wrapper)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON: %v", err)
	}

	// Now lets set some defaults for the folders in case they have not been explicitly set
	if wrapper.Settings.PluginDataFolder == "" {
		return nil, fmt.Errorf("failed to find pluginDataFolder: %v", err)
	}
	if wrapper.Settings.SettingsDataFolder == "" {
		wrapper.Settings.SettingsDataFolder = filepath.Clean(wrapper.Settings.PluginDataFolder + "/Settings")
	}
	if wrapper.Settings.ImpulseDataFolder == "" {
		wrapper.Settings.ImpulseDataFolder = filepath.Clean(wrapper.Settings.PluginDataFolder + "/Impulses")
	}
	if wrapper.Settings.TempDataFolder == "" {
		wrapper.Settings.TempDataFolder = filepath.Clean(wrapper.Settings.PluginDataFolder + "/Temp")
	}

	return &wrapper.Settings, nil
}

// Arguments holds the command-line arguments for the application.
type Arguments struct {
	AppName       string
	ClientID      string
	CleanClientID string
	//IsRawIn         bool
	InputFormat     string
	OutputFormat    string
	BigEndian       bool
	InPath          string
	OutPath         string
	OutBits         int
	InputSampleRate int
	DSDNative       bool // --samplerate=native: a DSD input decoded at its own DSD rate, for DSF out (the native round)
	PCMBits         int
	PCMChannels     int
	skipTime        string        // Change to string for input and local only
	StartTime       time.Duration // Change to time.Duration for output
	TrackURL        string        // Optional URL for detecting whether track has changed for gain re-use
	MaxSampleRate   int           // Optional max sample rate supported by Player, for resampling if needed
	WavLength       float64       // Optional seconds a WAV output's header states (--wavlength); 0 = from the input
	InSamples       int64         // --insamples: the frames the input will carry, exactly; 0 = from the input, if it says
	InHeader        bool          // --insamples=header: a WAV header on the input pipe states its frames exactly
	FlacSeek        int64         // --flacseek: the byte a remote FLAC's stream starts at (FlacSeekSet: given)
	FlacSeekSet     bool
	Tail            bool    // --tail: the last track's held tail played out on its own, no input
	DSDCutoff       float64 // --dsdcutoff: a DSD input's low-pass, Hz, above 20000 to 60000; 0 = none
	DSDGain         float64 // --dsdgain: dB on a DSD input (0: its full swing at full scale)
	DSDCutoffSet    bool    // given on the command line: it wins over the player's DSDCutoff
	DSDGainSet      bool
	untilTime       string // --until: where a DSD input ends, m:ss.ff from the file's start (LMS's %v)
	EndTime         time.Duration
	DSDRate         int     // --dsdrate: DSF output's DSD rate (0: not given)
	DSDRateSource   bool    // --dsdrate=source: the DSD input's own rate
	DSDMax          int     // --dsdmax: the highest DSD rate out (0: none)
	DSDMod          string  // --dsdmod: standard, clans or adaptive
	DSDOutGain      float64 // --dsdoutgain: dB before the modulator
	DSDSource       string  // --dsdsource: dsd or pcm ("": dsd)
	UpsampleRate    int     // --upsample: a PCM input's rate before the DSP, up or down (0: as it comes)
	UpFilterCD      gipsyUpsampler.Choice
	UpFilterHiRes   gipsyUpsampler.Choice
	upFilterSet     bool
	Debug           bool
}

// ReadArgs reads the command-line arguments and returns an Arguments object.
// Argument labels are case-insensitive.
func ReadArgs() (*Arguments, error) {
	args := &Arguments{UpFilterCD: gipsyUpsampler.DefaultCD, UpFilterHiRes: gipsyUpsampler.DefaultHiRes}

	// Get executable name
	if len(os.Args) > 0 {
		args.AppName = strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe")
	}

	// Parse arguments manually with case-insensitive flag handling
	if err := parseArgs(args); err != nil {
		return nil, err
	}

	// Validate required arguments - these need tidying up
	if args.ClientID == "" {
		return nil, fmt.Errorf("missing required argument: --ClientID")
	}

	// if we have a raw input, we need to validate the raw bits and channels and sample rate
	if args.InputFormat == "PCM" {
		if args.InputSampleRate == 0 {
			return nil, fmt.Errorf("missing sample rate for PCM file: --r")
		}
		if args.PCMBits == 0 {
			return nil, fmt.Errorf("missing raw bits for PCM file: --b")
		}
		if args.PCMChannels == 0 {
			return nil, fmt.Errorf("missing raw channels for PCM file: --c")
		}
	}

	// Validate numeric ranges
	if args.OutBits != 0 && (args.OutBits < 8 || args.OutBits > 32) {
		return nil, fmt.Errorf("invalid output bits: %d (must be 8-32)", args.OutBits)
	}

	// Convert and validate skip time
	if err := convertSkipTimeToDuration(args); err != nil {
		return nil, fmt.Errorf("invalid --skip format: %w", err)
	}

	// DSD: decoded here from a file (it seeks), to --samplerate
	if strings.ToUpper(args.InputFormat) == "DSD" {
		if args.InPath == "" {
			return nil, fmt.Errorf("DSD input needs --input <file>")
		}
		if args.InputSampleRate == 0 && !args.DSDNative {
			return nil, fmt.Errorf("DSD input needs --samplerate, the PCM rate to decode to")
		}
	}
	// native: the DSP at a DSD input's own rate, back out as DSD; refused anywhere else, not ignored
	if args.DSDNative && (strings.ToUpper(args.InputFormat) != "DSD" || strings.ToUpper(args.OutputFormat) != "DSF") {
		return nil, fmt.Errorf("--samplerate=native needs --formatin=DSD and --formatout=DSF")
	}
	// DSF: native DSD out (its rate from --dsdrate, else the player's settings, else the source's); the DSD out flags
	// are refused without it, not ignored
	if strings.ToUpper(args.OutputFormat) != "DSF" && (args.DSDRate != 0 || args.DSDRateSource || args.DSDMax != 0 || args.DSDMod != "" || args.DSDOutGain != 0 || args.DSDSource != "") {
		return nil, fmt.Errorf("--dsdrate, --dsdmax, --dsdmod, --dsdoutgain and --dsdsource need --formatout=DSF")
	}
	// the converter upsamples PCM; a DSD input is decoded straight to --samplerate. Refused, not ignored.
	if args.UpsampleRate > 0 && strings.ToUpper(args.InputFormat) == "DSD" {
		return nil, fmt.Errorf("--upsample needs a PCM input (a DSD input decodes to --samplerate)")
	}
	if args.upFilterSet && args.UpsampleRate == 0 {
		return nil, fmt.Errorf("--upfilter needs --upsample")
	}
	if args.FlacSeekSet && !strings.HasPrefix(args.TrackURL, "http://") && !strings.HasPrefix(args.TrackURL, "https://") {
		return nil, fmt.Errorf("--flacseek needs --trackurl, the stream's http or https URL")
	}
	if args.untilTime != "" {
		if strings.ToUpper(args.InputFormat) != "DSD" { // only the DSD decoder honours it: refused, not ignored
			return nil, fmt.Errorf("--until needs --formatin=DSD")
		}
		end, err := parseMinSec(args.untilTime)
		if err != nil {
			return nil, fmt.Errorf("invalid --until format: %w", err)
		}
		args.EndTime = end
	}

	return args, nil
}

// IsDSDRate: DSD64, 128, 256 or 512, the rates DSF output writes.
func IsDSDRate(v int) bool {
	return v == 2822400 || v == 5644800 || v == 11289600 || v == 22579200
}

// parseMinSec reads LMS's m:ss.ff (%t, %v) as a duration.
func parseMinSec(s string) (time.Duration, error) {
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, fmt.Errorf("expected m:s or m:s.f")
	}
	minutes, err := strconv.Atoi(parts[0])
	if err != nil || minutes < 0 {
		return 0, fmt.Errorf("invalid minutes: %s", parts[0])
	}
	seconds, err := strconv.ParseFloat(parts[1], 64)
	if err != nil || seconds < 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return 0, fmt.Errorf("invalid seconds: %s", parts[1])
	}
	return time.Duration((float64(minutes)*60 + seconds) * float64(time.Second)), nil
}

func parseArgs(args *Arguments) error {
	args.OutBits = 24
	args.DSDCutoff = 30000 // Roon's "recommended"; the plugin passes its own
	// Add this block to find the --input argument and its value
	for i := 1; i < len(os.Args); i++ {
		if os.Args[i] == "--input" && i+1 < len(os.Args) {
			args.InPath = os.Args[i+1]
			break
		}
	}
	// Flags that do NOT take a value
	noValueFlags := map[string]bool{
		"debug": true,
		"tail":  true,
	}
	for i := 1; i < len(os.Args); i++ {
		arg := os.Args[i]
		if strings.HasPrefix(arg, "--") {
			flagName := strings.ToLower(arg[2:]) // Remove "--" and lowercase
			value := ""
			// the value as given: lowercasing "--name=value" whole lowercased a track's URL too, so the next track's
			// engine never matched the plugin's note to the residual (THE GAPLESS ROUND, found live 2026-11-05)
			raw := ""

			// Check for equals sign
			if strings.Contains(flagName, "=") {
				parts := strings.SplitN(flagName, "=", 2)
				flagName = parts[0]
				value = parts[1]
				raw = arg[strings.IndexByte(arg, '=')+1:]
			} else {
				// No equals – if this flag requires a value, take next arg
				if !noValueFlags[flagName] {
					if i+1 < len(os.Args) && !strings.HasPrefix(os.Args[i+1], "--") {
						value = os.Args[i+1]
						raw = value
						i++ // skip the value in next iteration
					} else {
						return fmt.Errorf("flag --%s requires a value", flagName)
					}
				}
			}

			switch flagName {
			case "clientid":

				args.ClientID = strings.ReplaceAll(value, "-", ":")
				// need the CleanClientID for use in file names
				args.CleanClientID = sanitizeClientID(value)
			case "formatin":
				args.InputFormat = value

			case "formatout":
				args.OutputFormat = value
				//big endian
			case "be":
				if value == "" || strings.ToLower(value) == "true" {
					args.BigEndian = true
				} else {
					args.BigEndian = false
				}
			case "input":
				args.InPath = value
			case "output":
				args.OutPath = value
				//bit depth output
			case "bitsout":
				val, err := strconv.Atoi(value)
				if err != nil {
					return fmt.Errorf("invalid value for --bitsout: %w", err)
				}
				args.OutBits = val
				if args.OutBits < 8 || args.OutBits > 32 {
					args.OutBits = 24
				}
			case "samplerate":
				if strings.EqualFold(value, "native") {
					args.DSDNative, args.InputSampleRate = true, 0
					break
				}
				val, err := strconv.Atoi(value)
				if err != nil {
					return fmt.Errorf("invalid value for --samplerate: %w", err)
				}
				args.InputSampleRate = val
				//bit depths
			case "bitsin":
				val, err := strconv.Atoi(value)
				if err != nil {
					return fmt.Errorf("invalid value for --bitsin: %w", err)
				}
				args.PCMBits = val
				//audio channels
			case "channels":
				val, err := strconv.Atoi(value)
				if err != nil {
					return fmt.Errorf("invalid value for --channels: %w", err)
				}
				args.PCMChannels = val
			case "skip":
				args.skipTime = value
			case "trackurl":
				args.TrackURL = raw
			case "maxsamplerate":
				val, err := strconv.Atoi(value)
				if err != nil {
					return fmt.Errorf("invalid value for --maxsamplerate: %w", err)
				}
				args.MaxSampleRate = val
			case "wavlength":
				val, err := strconv.ParseFloat(value, 64)
				if err != nil || val < 0 || math.IsNaN(val) || math.IsInf(val, 0) {
					return fmt.Errorf("invalid value for --wavlength: %s", value)
				}
				args.WavLength = val
			case "insamples":
				if value == "header" {
					args.InHeader = true
					break
				}
				val, err := strconv.ParseInt(value, 10, 64)
				if err != nil || val < 0 {
					return fmt.Errorf("invalid value for --insamples: %s", value)
				}
				args.InSamples = val
			case "flacseek":
				val, err := strconv.ParseInt(value, 10, 64)
				if err != nil || val < 0 {
					return fmt.Errorf("invalid value for --flacseek: %s", value)
				}
				args.FlacSeek, args.FlacSeekSet = val, true
			case "tail":
				args.Tail = true
			case "dsdcutoff":
				val, err := strconv.ParseFloat(value, 64)
				// to 60 kHz: the decoder's first stage is flat to 80 kHz, and a cutoff's band is up to 20 kHz wide
				if err != nil || !(val == 0 || (val > 20000 && val <= 60000)) {
					return fmt.Errorf("invalid value for --dsdcutoff: %s (0, or above 20000 to 60000 Hz)", value)
				}
				args.DSDCutoff, args.DSDCutoffSet = val, true
			case "dsdgain":
				val, err := strconv.ParseFloat(value, 64)
				if err != nil || val < -24 || val > 12 {
					return fmt.Errorf("invalid value for --dsdgain: %s (-24 to 12 dB)", value)
				}
				args.DSDGain, args.DSDGainSet = val, true
			case "until":
				args.untilTime = value
			case "dsdrate":
				if strings.ToLower(value) == "source" {
					args.DSDRateSource = true
					break
				}
				val, err := strconv.Atoi(value)
				if err != nil || !IsDSDRate(val) {
					return fmt.Errorf("invalid value for --dsdrate: %s (2822400, 5644800, 11289600, 22579200 or source)", value)
				}
				args.DSDRate = val
			case "dsdmax":
				val, err := strconv.Atoi(value)
				if err != nil || !IsDSDRate(val) {
					return fmt.Errorf("invalid value for --dsdmax: %s (2822400, 5644800, 11289600 or 22579200)", value)
				}
				args.DSDMax = val
			case "dsdmod":
				if v := strings.ToLower(value); v == "standard" || v == "clans" || v == "adaptive" {
					args.DSDMod = v
				} else {
					return fmt.Errorf("invalid value for --dsdmod: %s (standard, clans or adaptive)", value)
				}
			case "dsdoutgain":
				val, err := strconv.ParseFloat(value, 64)
				if err != nil || val < -24 || val > 6 {
					return fmt.Errorf("invalid value for --dsdoutgain: %s (-24 to 6 dB)", value)
				}
				args.DSDOutGain = val
			case "dsdsource":
				if v := strings.ToLower(value); v == "dsd" || v == "pcm" {
					args.DSDSource = v
				} else {
					return fmt.Errorf("invalid value for --dsdsource: %s (dsd or pcm)", value)
				}
			case "upsample":
				val, err := strconv.Atoi(value)
				if err != nil || val < 44100 || val > 1536000 {
					return fmt.Errorf("invalid value for --upsample: %s (44100 to 1536000)", value)
				}
				args.UpsampleRate = val
			case "upfilter":
				cd, hi, two := strings.Cut(value, ",")
				if !two {
					hi = cd
				}
				c, err := gipsyUpsampler.ParseChoice(cd)
				if err != nil {
					return fmt.Errorf("invalid value for --upfilter: %w", err)
				}
				h, err := gipsyUpsampler.ParseChoice(hi)
				if err != nil {
					return fmt.Errorf("invalid value for --upfilter: %w", err)
				}
				args.UpFilterCD, args.UpFilterHiRes, args.upFilterSet = c, h, true
			case "debug":
				args.Debug = true
			default:
				return fmt.Errorf("unknown flag: %s", arg)
			}
		}
	}
	return nil
}

func convertSkipTimeToDuration(args *Arguments) error {
	if args.skipTime == "" {
		return nil // No skip time provided
	}

	// Split on colon only – expected format is "minutes:seconds" or "minutes:seconds.fraction"
	parts := strings.Split(args.skipTime, ":")
	if len(parts) != 2 {
		return fmt.Errorf("invalid format: expected m:s or m:s.f")
	}

	minutes, err := strconv.Atoi(parts[0])
	if err != nil {
		return fmt.Errorf("invalid minutes: %w", err)
	}

	// Parse seconds as float to handle possible fractional part (e.g., "42.00")
	secondsFloat, err := strconv.ParseFloat(parts[1], 64)
	if err != nil {
		return fmt.Errorf("invalid seconds: %w", err)
	}

	totalSeconds := float64(minutes)*60 + secondsFloat
	args.StartTime = time.Duration(totalSeconds * float64(time.Second))

	return nil
}

func convertSkipTimeToDurationOld(args *Arguments) error {
	if args.skipTime == "" {
		return nil // No skip time provided
	}

	parts := strings.Split(args.skipTime, ":")
	if len(parts) != 2 && len(parts) != 3 {
		return fmt.Errorf("invalid format")
	}

	minutes, err := strconv.Atoi(parts[0])
	if err != nil {
		return fmt.Errorf("invalid minutes: %w", err)
	}

	seconds, err := strconv.Atoi(parts[1])
	if err != nil {
		return fmt.Errorf("invalid seconds: %w", err)
	}

	duration := time.Duration(minutes)*time.Minute + time.Duration(seconds)*time.Second

	if len(parts) == 3 {
		fraction, err := strconv.ParseFloat(parts[2], 64)
		if err != nil {
			return fmt.Errorf("invalid fraction: %w", err)
		}
		duration += time.Duration(fraction * float64(time.Second))
	}

	args.StartTime = duration
	return nil
}

// Config
type ClientConfig struct {
	foxFilters.PlayerConfig

	Name       string
	ClientID   string
	Bypass     bool
	Preset     string
	Version    string
	TimeStamp  string
	ReplayGain ReplayGainConfig
	// a DSD input's decode (SqueezeDSP fork): the player's DSDCutoff (Hz, 0 = none) and DSDGain (dB); nil = not
	// set or not valid, and then the engine's own (--dsdcutoff, --dsdgain, or their defaults)
	DSDCutoff *float64
	DSDGain   *float64
	// DSF output (SqueezeDSP fork): the player's DSDOutRate ("source", or one of the four DSD rates), DSDOutMax (one of
	// them) and DSDOutMod (standard, clans or adaptive); "" / 0 = not set or not valid. A flag wins.
	DSDOutRate string
	DSDOutMax  int
	DSDOutMod  string
	// a native decode's integrator (the native round): the player's DSDIntegrator, lin, int, min or nonring; "" = lin
	DSDIntegrator string
}

type ReplayGainConfig struct {
	Enabled     bool
	FixedGain   float64
	SpotifyGain float64
	Mode        int
}

type rawClientConfig struct {
	Client map[string]json.RawMessage `json:"Client"`
}

// LoadConfig loads the configuration from a specified JSON file and returns a ClientConfig.
// fixed issue where filtesrs were built when they were disabled and peak filters built when values were zero.

func LoadConfig(filePath string) (*ClientConfig, error) {
	// Open the JSON file

	file, err := os.Open(filePath)

	if err != nil {
		return nil, fmt.Errorf("error opening file: %w", err)
	}
	defer file.Close()
	// Get file info to access modification time
	fileInfo, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("error getting file info: %w", err)
	}
	modTime := fileInfo.ModTime()
	// Read the file contents
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("error reading file: %w", err)
	}

	// Pass the data to buildConfig to create a new ClientConfig
	config, err := buildConfig(data)
	if err != nil {
		return nil, fmt.Errorf("error building config: %w", err)
	}
	// Set TimeStamp to the file's last modification time in RFC3339 format
	config.TimeStamp = modTime.UTC().Format(time.RFC3339)

	return config, nil
}

func buildConfig(data []byte) (*ClientConfig, error) {
	var raw rawClientConfig // has the Client map[string]json.RawMessage field
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	config := &ClientConfig{}

	// ── Legacy format normalisation — Lyrion specific ─────────────────────
	// Translate legacy fields into current Filters array format BEFORE
	// calling the library. The library never sees the legacy structure.
	legacyFilter := translateLegacyFilters(raw.Client)

	// ── DSP fields — library call ─────────────────────────────────────────
	dspConfig, err := foxFilters.BuildPlayerConfig(legacyFilter)
	if err != nil {
		return nil, err
	}

	config.PlayerConfig = *dspConfig

	// ── Application fields ────────────────────────────────────────────────
	for key, value := range raw.Client {
		switch key {
		case "Name":
			json.Unmarshal(value, &config.Name)
			config.Name = strings.Trim(config.Name, "\"")
		case "ID":
			json.Unmarshal(value, &config.ClientID)
			config.ClientID = strings.Trim(config.ClientID, "\"")
		case "Bypass":
			var b json.Number
			if err := json.Unmarshal(value, &b); err == nil {
				config.Bypass = parseBool(b)
			}
		case "Preset":
			json.Unmarshal(value, &config.Preset)
			config.Preset = strings.Trim(config.Preset, "\"")
		case "Version":
			json.Unmarshal(value, &config.Version)
			config.Version = strings.Trim(config.Version, "\"")
		case "DSDCutoff": // the pages store what setval was given: a number or its string
			if v, ok := jsonFloat(value); ok && (v == 0 || (v > 20000 && v <= 60000)) {
				config.DSDCutoff = &v
			}
		case "DSDGain":
			if v, ok := jsonFloat(value); ok && v >= -24 && v <= 12 {
				config.DSDGain = &v
			}
		case "DSDOutRate": // "source" or a rate, as a number or its string
			if v, ok := jsonFloat(value); ok && IsDSDRate(int(v)) && v == math.Trunc(v) {
				config.DSDOutRate = strconv.Itoa(int(v))
			} else if t := strings.ToLower(strings.Trim(string(value), "\" ")); t == "source" {
				config.DSDOutRate = t
			}
		case "DSDOutMax":
			if v, ok := jsonFloat(value); ok && IsDSDRate(int(v)) && v == math.Trunc(v) {
				config.DSDOutMax = int(v)
			}
		case "DSDOutMod":
			if t := strings.ToLower(strings.Trim(string(value), "\" ")); t == "standard" || t == "clans" || t == "adaptive" {
				config.DSDOutMod = t
			}
		case "DSDIntegrator":
			if t := strings.ToLower(strings.Trim(string(value), "\" ")); t == "lin" || t == "int" || t == "min" || t == "nonring" {
				config.DSDIntegrator = t
			}
		case "ReplayGain":
			var rg struct {
				Enabled     json.Number `json:"enabled"`
				Mode        json.Number `json:"mode"`
				FixedGain   json.Number `json:"fixed_gain"`
				SpotifyGain json.Number `json:"spotify_gain"`
			}
			if err := json.Unmarshal(value, &rg); err == nil {
				config.ReplayGain.Enabled = parseBool(rg.Enabled)
				if m, err := rg.Mode.Int64(); err == nil {
					config.ReplayGain.Mode = int(m)
				} else {
					config.ReplayGain.Mode = 3 // default to smart gain
				}
				if fg, err := rg.FixedGain.Float64(); err == nil {
					config.ReplayGain.FixedGain = fg
				} else {
					config.ReplayGain.FixedGain = -6.0
				}
				if sg, err := rg.SpotifyGain.Float64(); err == nil {
					config.ReplayGain.SpotifyGain = sg
				} else {
					config.ReplayGain.SpotifyGain = -4.0
				}
			}
		}
	}
	return config, nil
}

// jsonFloat reads a JSON number, or a string holding one
func jsonFloat(value json.RawMessage) (float64, bool) {
	var n json.Number
	if err := json.Unmarshal(value, &n); err == nil {
		f, err := n.Float64()
		return f, err == nil
	}
	var s string
	if err := json.Unmarshal(value, &s); err == nil {
		f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		return f, err == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
	}
	return 0, false
}

// translateLegacyFilters translates EQBand_, Lowshelf, Highshelf,
// Lowpass, Highpass into the current Filters array format so the
// library only ever sees one consistent structure.
func translateLegacyFilters(raw map[string]json.RawMessage) map[string]json.RawMessage {
	// Work on a copy so original is unchanged
	translated := make(map[string]json.RawMessage)
	for k, v := range raw {
		translated[k] = v
	}

	var filters []json.RawMessage

	// Absorb any existing Filters array entries first
	if existing, ok := translated["Filters"]; ok {
		json.Unmarshal(existing, &filters)
	}

	// Translate EQBand_ entries → Peak filter entries
	for key, value := range raw {
		if strings.HasPrefix(key, "EQBand_") {
			var pf struct {
				Gain  json.Number `json:"gain"`
				Freq  json.Number `json:"freq"`
				Slope json.Number `json:"q"`
			}
			if err := json.Unmarshal(value, &pf); err == nil {
				if !isNullFilter(pf.Gain, pf.Freq, pf.Slope) {
					entry, _ := json.Marshal(map[string]any{
						"FilterType": "Peak",
						"Frequency":  pf.Freq,
						"Gain":       pf.Gain,
						"Slope":      pf.Slope,
						"SlopeType":  "Q",
					})
					filters = append(filters, entry)
				}
			}
			delete(translated, key)
		}
	}

	// Translate Lowshelf, Highshelf → shelf filter entries
	for _, key := range []string{"Lowshelf", "Highshelf"} {
		if value, ok := raw[key]; ok {
			var sf struct {
				Gain    json.Number `json:"gain"`
				Freq    json.Number `json:"freq"`
				Slope   json.Number `json:"slope"`
				Enabled json.Number `json:"enabled"`
			}
			if err := json.Unmarshal(value, &sf); err == nil && parseBool(sf.Enabled) {
				filterType := "LowShelf"
				if key == "Highshelf" {
					filterType = "HighShelf"
				}
				entry, _ := json.Marshal(map[string]any{
					"FilterType": filterType,
					"Frequency":  sf.Freq,
					"Gain":       sf.Gain,
					"Slope":      sf.Slope,
					"SlopeType":  "Q",
				})
				filters = append(filters, entry)
			}
			delete(translated, key)
		}
	}

	// Translate Lowpass, Highpass → pass filter entries
	for _, key := range []string{"Lowpass", "Highpass"} {
		if value, ok := raw[key]; ok {
			var pf struct {
				Freq    json.Number `json:"freq"`
				Slope   json.Number `json:"q"`
				Enabled json.Number `json:"enabled"`
			}
			if err := json.Unmarshal(value, &pf); err == nil && parseBool(pf.Enabled) {
				filterType := "LowPass"
				if key == "Highpass" {
					filterType = "HighPass"
				}
				entry, _ := json.Marshal(map[string]any{
					"FilterType": filterType,
					"Frequency":  pf.Freq,
					"Slope":      pf.Slope,
					"SlopeType":  "Q",
				})
				filters = append(filters, entry)
			}
			delete(translated, key)
		}
	}

	// Write translated Filters array back
	if len(filters) > 0 {
		translated["Filters"], _ = json.Marshal(filters)
	}

	return translated
}

func parseBool(n json.Number) bool {
	s := n.String()
	return s == "1" || strings.ToLower(s) == "true"
}

func isNullFilter(values ...json.Number) bool {
	for _, v := range values {
		if v.String() != "null" {
			return false
		}
	}
	return true
}
