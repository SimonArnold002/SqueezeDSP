package foxConvolverPartition

import (
	"math"
	"math/rand"
	"testing"

	fft "github.com/Foxenfurter/foxAudioLib/foxFFT"
)

// foxProcessChunk is Fox's processChunk as committed (the fork's base), kept as the reference for the rewrite.
func foxProcessChunk(pc *PartitionedConvolver, chunk []float64, updateTail bool) []float64 {
	if pc.ringSize == 0 {
		return chunk
	}
	L := PartitionedChunkSize
	N := PartitionedFFTSize
	halfN := PartitionedHalfFFTSize
	overlapSize := L - 1
	realBuf := pc.workRealBuf
	for i := 0; i < overlapSize; i++ {
		realBuf[i] = pc.previousChunkTail[i]
	}
	for i := 0; i < L; i++ {
		realBuf[overlapSize+i] = chunk[i]
	}
	realBuf[N-1] = 0
	currentSlot := pc.audioFFTRing[pc.ringPosition]
	fft.RealFftUnchecked(realBuf, currentSlot)
	pc.audioFFTRing[pc.ringPosition] = currentSlot
	if updateTail {
		copy(pc.previousChunkTail, chunk[1:])
	}
	clear(pc.workResult)
	var maxPartitions int
	if pc.ringFilled {
		maxPartitions = pc.numPartitions
	} else {
		maxPartitions = pc.ringPosition + 1
		if maxPartitions > pc.numPartitions {
			maxPartitions = pc.numPartitions
		}
	}
	for p := 0; p < maxPartitions; p++ {
		audioIdx := (pc.ringPosition - p + pc.ringSize) % pc.ringSize
		audioFFT := pc.audioFFTRing[audioIdx]
		impulseFFT := pc.impulsePartitions[p]
		i := 0
		for i < halfN-3 {
			pc.workResult[i] += audioFFT[i] * impulseFFT[i]
			pc.workResult[i+1] += audioFFT[i+1] * impulseFFT[i+1]
			pc.workResult[i+2] += audioFFT[i+2] * impulseFFT[i+2]
			pc.workResult[i+3] += audioFFT[i+3] * impulseFFT[i+3]
			i += 4
		}
		for ; i < halfN; i++ {
			pc.workResult[i] += audioFFT[i] * impulseFFT[i]
		}
	}
	fft.RealIfftUnchecked(pc.workResult, realBuf)
	output := pc.workOutput
	for i := 0; i < L; i++ {
		output[i] = realBuf[overlapSize+i]
	}
	pc.ringPosition = (pc.ringPosition + 1) % pc.ringSize
	if !pc.ringFilled && pc.ringPosition == 0 {
		pc.ringFilled = true
	}
	return output
}

func chunkPair(t testing.TB, taps, rate int) (a, b *PartitionedConvolver, rng *rand.Rand) {
	rng = rand.New(rand.NewSource(1))
	h := make([]float64, taps)
	for i := range h {
		h[i] = rng.NormFloat64() * math.Exp(-float64(i)/float64(taps/6))
	}
	a, b = NewPartitionedConvolver(h, rate), NewPartitionedConvolver(h, rate)
	a.InitForStreaming()
	b.InitForStreaming()
	return a, b, rng
}

// TestChunkBitsUnchanged (SqueezeDSP fork): processChunk without its bounds checks gives the bits of Fox's loop, chunk
// for chunk, while the ring fills and after it wraps: a 50,000-tap impulse at 96k (13 partitions of 4096), 40 chunks.
func TestChunkBitsUnchanged(t *testing.T) {
	a, b, rng := chunkPair(t, 50000, 96000)
	if a.numPartitions < 10 {
		t.Fatalf("%d partitions: the test wants the ring to fill and wrap", a.numPartitions)
	}
	for c := 0; c < 40; c++ {
		chunk := make([]float64, PartitionedChunkSize)
		for i := range chunk {
			chunk[i] = rng.Float64()*2 - 1
		}
		got := append([]float64(nil), a.processChunk(chunk, true)...)
		want := foxProcessChunk(b, chunk, true)
		for i := range got {
			if math.Float64bits(got[i]) != math.Float64bits(want[i]) {
				t.Fatalf("chunk %d sample %d: %v, Fox's loop %v", c, i, got[i], want[i])
			}
		}
	}
}

// Fox's loop against the rewrite at 705.6k with a 1 s filter in his partitions there (87 of 8192; the engine's own
// choice is larger now, partition_test.go): go test -bench Chunk
func benchChunk(b *testing.B, f func(pc *PartitionedConvolver, chunk []float64)) {
	old := PartitionedChunkSize
	SetPartitionSizes(8192)
	defer SetPartitionSizes(old)
	pc, _, rng := chunkPair(b, 705600, 0) // rate 0: the sizes as set
	chunk := make([]float64, PartitionedChunkSize)
	for i := range chunk {
		chunk[i] = rng.Float64()*2 - 1
	}
	for range pc.ringSize { // the ring full, as in steady playback
		f(pc, chunk)
	}
	b.ResetTimer()
	for range b.N {
		f(pc, chunk)
	}
}
func BenchmarkChunkFox(b *testing.B) {
	benchChunk(b, func(pc *PartitionedConvolver, c []float64) { foxProcessChunk(pc, c, true) })
}
func BenchmarkChunkOurs(b *testing.B) {
	benchChunk(b, func(pc *PartitionedConvolver, c []float64) { pc.processChunk(c, true) })
}
