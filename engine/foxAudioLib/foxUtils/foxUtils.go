package foxUtils

type ProcessingMode int

const (
    ModeRealtime  ProcessingMode = iota // minimise latency, one convolver chunk per decode
    ModeStreaming                        // maximise throughput, larger decode chunks
)

// CalculateChunkSize returns the processing chunk size for a given sample rate and mode.
func CalculateChunkSize(sampleRate int, mode ProcessingMode) int {
    switch mode {
    case ModeRealtime:
        switch {
        case sampleRate >= 176400:
            return 512
        default:
            return 256
        }
    case ModeStreaming:
        switch {
        case sampleRate >= 176400:
            return 8192
        default:
            return 4096
        }
    default:
        return 4096
    }
}