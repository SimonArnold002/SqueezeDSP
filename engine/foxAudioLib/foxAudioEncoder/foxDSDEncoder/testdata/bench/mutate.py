# Mutation check for foxDSDEncoder (and its engine flags): each mutant is applied to a fresh copy of the engine's
# modules in the work folder and must make the named test FAIL. Run: python3 -I mutate.py <repo> <work>
# Not a mutant here, measured equivalent 2026-11-02: Close without its first drain. The padding loop then pushes an
# adaptive loop's held-back look-ahead through with the same bits (its future is silence either way).
# Not mutants here, the engine's speed (0.1.72.35): the encoder's meter with the adapter (its test would need sox), the
# merger's gain time left out and the DSD reader's time after its last send (both change a figure no test can pin).
import sys, os, shutil, subprocess
repo, work = sys.argv[1], sys.argv[2]
def E(f):
    return ('foxAudioLib', 'foxAudioEncoder/foxDSDEncoder/' + f, './foxAudioEncoder/foxDSDEncoder/')
SET = ('foxLyrionDSP', 'LyrionDSPSettings/LyrionDSPSettings.go', './LyrionDSPSettings/')
def U(f):
    return ('foxAudioLib', 'foxUpsampler/' + f, './foxUpsampler/')
PROC = ('foxLyrionDSP', 'LyrionDSPProcessAudio/LyrionDSPProcessAudio.go', './LyrionDSPProcessAudio/')
AE = ('foxAudioLib', 'foxAudioEncoder/foxAudioEncoder.go', './foxAudioEncoder/')
SPD = ('foxAudioLib', 'foxSpeed/foxSpeed.go', './foxSpeed/')
CONV = ('foxAudioLib', 'foxConvolverPartition/foxConvolverPartition.go', './foxConvolverPartition/')
FFT = ('foxAudioLib', 'foxFFT/foxFFT.go', './foxFFT/')
FFTR = ('foxAudioLib', 'foxFFT/foxFFTReal.go', './foxFFT/')
DSDR = ('foxAudioLib', 'foxAudioDecoder/foxDSDReader/foxDSDReader.go', './foxAudioDecoder/foxDSDReader/')
WAVR = ('foxAudioLib', 'foxAudioDecoder/foxWavReader/foxWavReader.go', './LyrionDSPProcessAudio/', 'foxLyrionDSP')  # tested from there
M = [
 # the loop
 ('loop: a section loses its a2 term', 'modulator.go', '		pb := -sb - a1b*wb1 - a2b*wb2', '		pb := -sb - a1b*wb1 - 0*a2b*wb2', 'TestLoopLevelAndNoise'),
 ('loop: a suffix sum skips a section', 'modulator.go', '		sb := cb + sc\n		sa := ca + sb\n		pa := -sa - a1a*wa', '		sb := cb\n		sa := ca + sb\n		pa := -sa - a1a*wa', 'TestLoopLevelAndNoise'),
 ('loop: a runaway never cleared', 'modulator.go', '		if v > guard || v < -guard {\n			wa, wb1', '		if false {\n			wa, wb1', 'TestRunawayIsCleared'),
 ('pair: B reads one of A\'s states', 'modulator.go', '		gb := d2b*yb2 + d1b*yb1', '		gb := d2b*xb2 + d1b*yb1', 'TestPairEqualsSingle'),
 ('pair: B cleared on A\'s runaway', 'modulator.go', '		if vb > guard || vb < -guard {', '		if v > guard || v < -guard {', 'TestPairEqualsSingle'),
 # the interpolator
 ('interp: phase 0 not a copy', 'interp.go', '			oj[0] = w[K]', '			oj[0] = w[K+1]', 'TestInterpolatorKeepsFramesAndRejectsImages'),
 ('interp: a tap dropped', 'interp.go', 'wa[12]*h[12] + wa[16]*h[16]', 'wa[12]*h[12]', 'TestInterpolatorKeepsFramesAndRejectsImages'),
 ('interp: designed for 80 dB', 'interp.go', '	interpStopDB   = 120.0', '	interpStopDB   = 80.0', 'TestInterpolatorKeepsFramesAndRejectsImages'),
 ('interp: no flush', 'interp.go', '	return it.push(make([]float64, it.K), out)', '	return out', 'TestInterpolatorKeepsFramesAndRejectsImages'),
 # the DSF stream
 ('DSF: MSB first', 'encoder.go', 'ch.put(b[0] | b[1]<<1 | b[2]<<2 | b[3]<<3 | b[4]<<4 | b[5]<<5 | b[6]<<6 | b[7]<<7)', 'ch.put(b[0]<<7 | b[1]<<6 | b[2]<<5 | b[3]<<4 | b[4]<<3 | b[5]<<2 | b[6]<<1 | b[7])', 'TestDecodesBackAtUnity'),
 ('DSF: MSB first in a byte begun before', 'encoder.go', '		ch.cur |= bits[0] << ch.nbit', '		ch.cur |= bits[0] << (7 - ch.nbit)', 'TestPackMatchesBits'),
 ('DSF: a whole byte from the wrong bits', 'encoder.go', '			bits = bits[8:]\n			continue', '			bits = bits[7:]\n			continue', 'TestPackMatchesBits'),
 ('DSF: bits per sample 8', 'encoder.go', '	h = le.AppendUint32(h, 1) // bits per sample 1: LSB first', '	h = le.AppendUint32(h, 8) // bits per sample 1: LSB first', 'TestDSFHeaderAndLength'),
 ('DSF: count one short', 'encoder.go', '	e.count = e.Frames * int64(e.L)', '	e.count = e.Frames*int64(e.L) - 1', 'TestDSFHeaderAndLength'),
 ('DSF: padded with zeros', 'encoder.go', '	pad := byte(silence)', '	pad := byte(0)', 'TestDSFHeaderAndLength'),
 # native (the native round, 2026-11-07): the DSD rate in, the loop on it as it comes; the engine's flag and setting
 ('native: the DSD rate in refused', 'encoder.go', '	if e.InRate <= 0 || e.DSDRate%e.InRate != 0 {', '	if e.InRate <= 0 || e.DSDRate%e.InRate != 0 || e.DSDRate/e.InRate < 2 {', 'TestNativeRateIsTheLoopAlone'),
 ('flag: --samplerate=native not taken', None, '					args.DSDNative, args.InputSampleRate = true, 0', '					args.DSDNative, args.InputSampleRate = false, 0', 'TestNativeArgs', SET),
 ('flag: native anywhere', None, '	if args.DSDNative && (strings.ToUpper(args.InputFormat) != "DSD" || strings.ToUpper(args.OutputFormat) != "DSF") {', '	if false {', 'TestNativeArgs', SET),
 ('setting: DSDIntegrator never read', None, '				config.DSDIntegrator = t', '				_ = t', 'TestNativeClientConfig', SET),
 ('setting: any DSDIntegrator read', None, 't == "lin" || t == "int" || t == "min" || t == "nonring" {', 'true {', 'TestNativeClientConfig', SET),
 # bounded (a PCM stream's length stated ahead with a margin): the stream ends with the input, zero-padded
 ('bounded: the stated count filled with silence', 'encoder.go', '		for !e.Bounded && g[0].done < e.count {', '		for g[0].done < e.count {', 'TestBoundedEndsWithTheInput'),
 ('bounded: padded with DSD silence', 'encoder.go', '		pad = 0\n', '		pad = silence\n', 'TestBoundedEndsWithTheInput'),
 ('DSF: data chunk size without its header', 'encoder.go', '	h = le.AppendUint64(h, uint64(12+data))', '	h = le.AppendUint64(h, uint64(data))', 'TestDSFHeaderAndLength'),
 ('DSF: channels not in pairs (one never run)', 'encoder.go', '		g := e.chans[c:min(c+2, len(e.chans))]', '		g := e.chans[c : c+1]', 'TestDSFHeaderAndLength'),
 ('a 44.1k input accepted (not flat to 20 kHz)', 'encoder.go', '	if e.InRate < 88200 {', '	if e.InRate < 44100 {', 'TestOpenRefuses'),
 ('limit not applied', 'encoder.go', '		if v > e.limit {', '		if v > 2*e.limit {', 'TestLimitAndPeak'),
 # the source's designs (0.1.72.33): a PCM source's held at LimitPCM, a DSD source's at Limit
 ('a PCM source given the DSD designs', 'encoder.go', '		table, limit = designsPCM, LimitPCM', '		table, limit = designs, LimitPCM', 'TestSourceChoosesDesigns'),
 ('a PCM source held at the DSD limit', 'encoder.go', '		table, limit = designsPCM, LimitPCM', '		table, limit = designsPCM, Limit', 'TestSourceChoosesDesigns'),
 ('an unknown source taken as DSD', 'encoder.go', '		return fmt.Errorf("foxDSDEncoder: no source %q (dsd or pcm)", e.Source)', '		e.Source = "dsd"', 'TestSourceChoosesDesigns'),
 ('the PCM limit at the DSD one', 'encoder.go', '	LimitPCM  = 0.63', '	LimitPCM  = 0.72', 'TestSourceChoosesDesigns'),
 ("the interpolator's overshoot not held", 'encoder.go', '\tlim := e.limit\n\tif reach*ch.it.bound*(1+1e-9) <= lim {', '\tlim := e.limit\n\tif true {', 'TestOvershootHeld'),
 # the encoder's pipeline (0.1.72.35): each channel's interpolation ahead of the paired loop; hold scans only what can pass
 ('hold: the skip blind to the history', 'encoder.go', '\tfor _, v := range ch.it.buf {\n\t\treach = math.Max(reach, math.Abs(v))\n\t}\n', '', 'TestHoldBound'),
 ('hold: the skip without the gain bound', 'encoder.go', 'if reach*ch.it.bound*(1+1e-9) <= lim {', 'if reach*(1+1e-9) <= lim {', 'TestHoldBound'),
 # (the bound one phase short is no mutant: phase l's taps mirror phase L-l's, the same sum)
 ('interp: the bound from phase 1 alone', 'interp.go', '\tfor l := 1; l < L; l++ {\n\t\tsum := 0.0', '\tfor l := 1; l < 2; l++ {\n\t\tsum := 0.0', 'TestHoldBound'),
 ('pipeline: the flush pass dropped', 'encoder.go', "\t\tchunks++ // the interpolators' last frames, against silence after the end\n", '', 'TestBoundedEndsWithTheInput'),
 ('pipeline: the channels swapped into the loop', 'encoder.go', '\t\te.modulate(g, ups)\n\t\tfor k := range g {\n\t\t\tfree[k] <- ups[k]', '\t\te.modulate(g, append(ups[1:len(ups):len(ups)], ups[0]))\n\t\tfor k := range g {\n\t\t\tfree[k] <- ups[k]', 'TestDecodesBackAtUnity'),
 ('pipeline: a sub-chunk skipped', 'encoder.go', '\t\t\t\tif lo := i * subFrames; lo < n {', '\t\t\t\tif lo := (i + i/7) * subFrames; lo < n {', 'TestChunkingDoesNotMatter'),
 # the upsampler (foxUpsampler)
 ('upsampler: Kaiser 10% short', 'design.go', '	n := int(math.Ceil((stopDB-7.95)/(2.285*dw)*1.04)) + 1', '	n := int(math.Ceil((stopDB-7.95)/(2.285*dw)*0.9)) + 1', 'TestStopBands', U('design.go')),
 ('upsampler: no headroom in the design', 'design.go', '	stopDB += 2.5\n', '', 'TestStageB', U('design.go')),
 ('upsampler: the minimum phase not folded', 'design.go', '		x[i] = complex(2*real(x[i]), 0)', '		x[i] = complex(real(x[i]), 0)', 'TestPhaseShapes', U('design.go')),
 ('upsampler: intermediate as two linear halves', 'design.go', '		h := convolve(half, minimumPhase(half))', '		h := convolve(half, half)', 'TestPhaseShapes', U('design.go')),
 ('upsampler: the long CD filter as the short', 'upsampler.go', '		fp = 0.99 * fs / 2', '		fp = fs * 20000 / 44100', 'TestWidths', U('upsampler.go')),
 ('upsampler: hi-res long at 80%', 'upsampler.go', '		fp = 0.9 * fs / 2', '		fp = 0.8 * fs / 2', 'TestWidths', U('upsampler.go')),
 ('upsampler: 48k a hi-res source', 'upsampler.go', 'func IsHiRes(rate int) bool { return rate > 48000 }', 'func IsHiRes(rate int) bool { return rate >= 48000 }', 'TestWidths', U('upsampler.go')),
 ('upsampler: B stopped past the first image', 'upsampler.go', '		u.bFst = float64(min(bIn, bOut)) - fst', '		u.bFst = float64(min(bIn, bOut)) + fst', 'TestNoImagesOrAliases', U('upsampler.go')),
 # every pair of rates (0.1.72.37): A at the lower rate's Nyquist, going up then B, going down B then A as a decimator
 ('upsampler: down through B alone again', 'upsampler.go', '	case u.Down:\n		u.stages = [][]*poly{b, a}', '	case u.Down:\n		u.stages = [][]*poly{b}', 'TestNoImagesOrAliases', U('upsampler.go')),
 ('upsampler: B before A going up', 'upsampler.go', '	default:\n		u.stages = [][]*poly{a, b}', '	default:\n		u.stages = [][]*poly{b, a}', 'TestSineExact', U('upsampler.go')),
 ('upsampler: the decimator at the interpolator\'s gain', 'upsampler.go', '	if u.Down {\n		gain = 1\n	}', '	if u.Down {\n		gain = 2\n	}', 'TestSineExact', U('upsampler.go')),
 ('upsampler: the down delay at twice the rate', 'upsampler.go', '		u.Delay = d / 2', '		u.Delay = d', 'TestAligned', U('upsampler.go')),
 ('upsampler: B flat short of A\'s edge', 'upsampler.go', '		u.bFp = fst\n', '		u.bFp = 0.9 * fst\n', 'TestStageB', U('upsampler.go')),
 ('upsampler: the source\'s class of widths going down', 'upsampler.go', '	fp, fst := widths(float64(lo), IsHiRes(lo), u.Choice)', '	fp, fst := widths(float64(lo), IsHiRes(inRate), u.Choice)', 'TestDownWidths', U('upsampler.go')),
 ('upsampler: A at the source\'s Nyquist going down', 'upsampler.go', '	lo := min(inRate, outRate)', '	lo := inRate', 'TestNoImagesOrAliases', U('upsampler.go')),
 ('upsampler: the delay not taken off', 'upsampler.go', '	u.drop = int(math.Round(u.Delay))', '	u.drop = 0', 'TestAligned', U('upsampler.go')),
 ('upsampler: no flush', 'upsampler.go', '	for u.out < target {', '	for false && u.out < target {', 'TestBlocksAndLength', U('upsampler.go')),
 ('upsampler: a choice of anything', 'upsampler.go', '	if ok && (l == "short" || l == "long") {', '	if ok {', 'TestParseChoice', U('upsampler.go')),
 ('poly: the phase step one too many', 'poly.go', '		s.p += M\n', '		s.p += M + 1\n', 'TestSineExact', U('poly.go')),
 ('poly: history kept one short', 'poly.go', '	keep := s.i - int64(K-1)', '	keep := s.i - int64(K-2)', 'TestBlocksAndLength', U('poly.go')),
 ('poly: a tap dropped from every phase', 'poly.go', '		for ; j < len(w); j++ {\n			a0 += h[j] * w[j]\n		}', '', 'TestSineExact', U('poly.go')),
 # the flag (0.1.72.37): a PCM target from 44.1k up, down as well as up
 ('flags: a 44.1k target refused', None, '				if err != nil || val < 44100 || val > 1536000 {', '				if err != nil || val < 88200 || val > 1536000 {', 'TestUpsampleArgs', SET),
 ('flags: a target under 44.1k taken', None, '				if err != nil || val < 44100 || val > 1536000 {', '				if err != nil || val < 44000 || val > 1536000 {', 'TestUpsampleArgs', SET),
 # the engine: the converter's stage between the decoder and the rest
 ('engine: the upsampler never flushed', None, '		if up := ap.Upsampler.Flush(); len(up) > 0 && len(up[0]) > 0 {', '		if up := ap.Upsampler.Flush(); false && len(up) > 0 {', 'TestUpsampleStage', PROC),
 ('engine: the stage skipped', None, '	if ap.Upsampler == nil {\n		return in\n	}', '	return in', 'TestUpsampleStage', PROC),
 ('gain ignored', 'encoder.go', '		v *= e.gain', '		v *= 1', 'TestLimitAndPeak'),
 ("the caller's samples overwritten", 'encoder.go', '\tfor _, v := range x {\n\t\tv *= e.gain\n', '\tfor i, v := range x {\n\t\tv *= e.gain\n\t\tx[i] = v\n', 'TestEncodeDataLeavesInput'),
 # the designs
 ('zeros all at z=1', 'ntf.go', '		z[i] = cmplx.Exp(complex(0, r*wB))', '		z[i] = cmplx.Exp(complex(0, 0*r*wB))', 'TestTablesMatchDesign'),
 ('standard: bisection inverted', 'ntf.go', '		if gainAt(z, butterworthHighPassPoles(n, mid), math.Pi) > obg {', '		if gainAt(z, butterworthHighPassPoles(n, mid), math.Pi) < obg {', 'TestStandardMeetsItsGain'),
 ('poles: the real one the farthest from the axis', 'ntf.go', '			if math.Abs(imag(p)) < math.Abs(imag(rest[ri])) {', '			if math.Abs(imag(p)) > math.Abs(imag(rest[ri])) {', 'TestTablesMatchDesign'),
 ('CLANS: the cap ignored', 'ntf.go', '		if v := l1(f.sections(), 3000) - cap; v > 0 {', '		if v := l1(f.sections(), 3000) - cap; false && v > 0 {', 'TestTablesMatchDesign'),
 ('Legendre recurrence wrong', 'ntf.go', '				p0, p1 = p1, ((2*float64(k)-1)*x*p1-(float64(k)-1)*p0)/float64(k)', '				p0, p1 = p1, ((2*float64(k)-1)*x*p1-(float64(k)-1)*p0)/float64(k+1)', 'TestLegendreRoots'),
 # adaptive
 ('adaptive: no look-ahead', 'adaptive.go', '		if l.nl[k]-idx <= l.ahead {', '		if l.nl[k]-idx <= 0 {', 'TestAdaptiveBursts'),
 ('adaptive: a hard switch, no fade', 'adaptive.go', '		step:  1 / (adaptRampSec * float64(rate)),', '		step:  1,', 'TestAdaptiveBursts'),
 ('adaptive: the blend ignores the safe design', 'adaptive.go', '		v := x + CA + alpha*(CB-CA)', '		v := x + CA', 'TestAdaptiveBursts'),
 ('adaptive: the safe design fed the wrong error', 'adaptive.go', '		qb := (CB - v) - tb - b1b*yb1 - b2b*yb2', '		qb := (CA - v) - tb - b1b*yb1 - b2b*yb2', 'TestAdaptiveBursts'),
 ('adaptive: loud start not on the safe design', 'adaptive.go', '	if l.next == 0 && n > 0 && l.nl[0] <= l.ahead {', '	if false {', 'TestAdaptiveLoudFromTheStart'),
 ('adaptive: no hold', 'adaptive.go', '		hold:  int64(adaptHoldSec * float64(rate)),', '		hold:  0,', 'TestAdaptiveHoldsAfterLoud'),
 ('adaptive: threshold 0.6 (over the quiet design\'s limit)', 'adaptive.go', '	adaptThreshold = 0.45', '	adaptThreshold = 0.6', 'TestAdaptiveHoldsAfterLoud'),
 # the flags
 ('flag: adaptive refused', None, 'v == "standard" || v == "clans" || v == "adaptive"', 'v == "standard" || v == "clans"', 'TestDSFArgs', SET),
 ('flag: DSD out flags taken without DSF', None, '	if strings.ToUpper(args.OutputFormat) != "DSF" && (args.DSDRate != 0 || args.DSDRateSource || args.DSDMax != 0 || args.DSDMod != "" || args.DSDOutGain != 0 || args.DSDSource != "") {', '	if false {', 'TestDSFArgs', SET),
 ('flag: --dsdsource taken without DSF', None, ' || args.DSDOutGain != 0 || args.DSDSource != "") {', ' || args.DSDOutGain != 0) {', 'TestUpsampleArgs', SET),
 ('flag: --upsample on a DSD input taken', None, '	if args.UpsampleRate > 0 && strings.ToUpper(args.InputFormat) == "DSD" {', '	if false {', 'TestUpsampleArgs', SET),
 ('flag: --upfilter without --upsample taken', None, '	if args.upFilterSet && args.UpsampleRate == 0 {', '	if false {', 'TestUpsampleArgs', SET),
 ('flag: one --upfilter for CD rates only', None, '				if !two {\n					hi = cd\n				}\n', '				if !two {\n					hi = "short-lin"\n				}\n', 'TestUpsampleArgs', SET),
 ('flag: --upsample any rate', None, '				if err != nil || val < 44100 || val > 1536000 {', '				if err != nil {', 'TestUpsampleArgs', SET),
 ('flag: --dsdsource anything', None, '				if v := strings.ToLower(value); v == "dsd" || v == "pcm" {', '				if v := strings.ToLower(value); true {', 'TestUpsampleArgs', SET),
 ('flag: a 48k-family DSD rate', None, '	return v == 2822400 || v == 5644800 || v == 11289600 || v == 22579200', '	return v == 2822400 || v == 5644800 || v == 11289600 || v == 22579200 || v == 3072000', 'TestDSFArgs', SET),
 # the engine's own speed for the page (0.1.72.35): each stage's own work, its waits left out; the slowest reported
 ('speed: the fastest stage taken', None, '\t\tif s := a / b; !ok || s < best {', '\t\tif s := a / b; !ok || s > best {', 'TestSlowest', SPD),
 ('speed: a stage not yet started left out', None, '\t\tif a <= 0 {\n\t\t\treturn "", 0, 0, false\n\t\t}', '\t\tif a <= 0 {\n\t\t\tcontinue\n\t\t}', 'TestSlowest', SPD),
 ('speed: a stage with no work taken', None, '\t\tif b <= 0 {\n\t\t\tcontinue\n\t\t}\n', '', 'TestSlowest', SPD),
 ('speed: the work not added', None, '\tm.busy.Add(int64(d))\n', '', 'TestMeterFigures', SPD),
 ('speed: the encoder\'s writes counted as work', None, '\terr = myEncoder.writeData(encodedData)\n', '\terr = myEncoder.writeData(encodedData)\n\tmyEncoder.Meter.Add(0, time.Since(t0))\n', 'TestMeterLeavesOutWrites', AE),
 ('speed: the encoder\'s frames not counted', None, '\t\tmyEncoder.Meter.Add(len(buffer[0]), time.Since(t0))', '\t\tmyEncoder.Meter.Add(0, time.Since(t0))', 'TestMeterCountsEveryFrame', AE),
 ('speed: the DSD reader\'s sends counted (stage 2)', None, '\t\t\t\t\t\tout <- block.s\n\t\t\t\t\t\tt0 = time.Now()\n', '\t\t\t\t\t\tout <- block.s\n', 'TestMeterLeavesOutWaits', DSDR),
 ('speed: the DSD reader\'s sends counted (stage 3)', None, '\t\t\t\t\tout <- block.s\n\t\t\t\t\tt0 = time.Now()\n', '\t\t\t\t\tout <- block.s\n', 'TestMeterLeavesOutWaits', DSDR),
 ('speed: the DSD reader\'s blocks not counted', None, '\t\t\t\t\t\tr.Meter.Add(len(block.s[0]), time.Since(t0))\n', '\t\t\t\t\t\tr.Meter.Add(0, time.Since(t0))\n', 'TestMeterLeavesOutWaits', DSDR),
 ('speed: the DSD reader\'s last block not counted', None, '\t\tr.Meter.Add(len(block.s[0]), 0) // its work is counted above\n', '', 'TestMeterLeavesOutWaits', DSDR),
 ('speed: the WAV reader\'s sends counted', None, '\t\t\t\tFD.Meter.Add(len(samples[0]), time.Since(t0))\n\t\t\t\tDecodedSamplesChannel <- samples\n', '\t\t\t\tDecodedSamplesChannel <- samples\n\t\t\t\tFD.Meter.Add(len(samples[0]), time.Since(t0))\n', 'TestWavReaderMeterLeavesOutWaits', WAVR),
 ('speed: the WAV reader\'s frames not counted', None, '\t\t\t\tFD.Meter.Add(len(samples[0]), time.Since(t0))\n', '\t\t\t\tFD.Meter.Add(0, time.Since(t0))\n', 'TestWavReaderMeterLeavesOutWaits', WAVR),
 ('speed: the convolver\'s sends counted', None, '\t\tpc.Meter.Add(len(inputBlock), time.Since(t0))\n\t\tpc.dropSent()\n\n\t\tfor len(pc.outputBuffer) >= pc.outputLength {\n\t\t\toutputSignalChannel <- pc.outputBuffer[:pc.outputLength]\n\t\t\tpc.outputBuffer = pc.outputBuffer[pc.outputLength:]\n\t\t}\n', '\t\tpc.dropSent()\n\n\t\tfor len(pc.outputBuffer) >= pc.outputLength {\n\t\t\toutputSignalChannel <- pc.outputBuffer[:pc.outputLength]\n\t\t\tpc.outputBuffer = pc.outputBuffer[pc.outputLength:]\n\t\t}\n\t\tpc.Meter.Add(len(inputBlock), time.Since(t0))\n', 'TestMeterLeavesOutWaits', CONV),
 ('speed: a convolver with no filter never counts', None, '\t\t\tpc.Meter.Add(len(inputBlock), 0) // passed along: no work\n', '', 'TestMeterLeavesOutWaits', CONV),
 ('speed: the upsampler\'s sends counted', None, '\t\t\t\tap.upMeter.Add(len(up[0]), time.Since(t0))\n\t\t\t\tout <- up\n', '\t\t\t\tout <- up\n\t\t\t\tap.upMeter.Add(len(up[0]), time.Since(t0))\n', 'TestStageMetersLeaveOutWaits', PROC),
 ('speed: the upsampler\'s flush not counted', None, '\t\t\tap.upMeter.Add(len(up[0]), time.Since(t0))\n\t\t\tout <- up\n\t\t}', '\t\t\tap.upMeter.Add(0, time.Since(t0))\n\t\t\tout <- up\n\t\t}', 'TestStageMetersLeaveOutWaits', PROC),
 ('speed: the merger\'s send counted', None, '\t\t\tap.mergeMeter.Add(len(mergedChunks[0]), work+time.Since(t0))\n\t\t\twork = 0\n\t\t\t// Send the processed chunks\n\t\t\toutputChannel <- mergedChunks\n', '\t\t\toutputChannel <- mergedChunks\n\t\t\tap.mergeMeter.Add(len(mergedChunks[0]), work+time.Since(t0))\n\t\t\twork = 0\n', 'TestStageMetersLeaveOutWaits', PROC),
 ('speed: a convolver counting on a meter not reported', None, '\t\t\tms = append(ms, c.Meter)', '\t\t\tms = append(ms, &foxSpeed.Meter{Name: c.Meter.Name, Rate: c.Meter.Rate})', 'TestAttachMeters', PROC),
 ('speed: the DSD reader given no meter', None, '\tif ap.Decoder.DSDReader != nil {\n\t\tap.Decoder.DSDReader.Meter = dec', '\tif false {\n\t\tap.Decoder.DSDReader.Meter = dec', 'TestAttachMeters', PROC),
 ('speed: the encoder given no meter', None, '\tap.Encoder.Meter = &foxSpeed.Meter{Name: "encoder", Rate: ap.Encoder.SampleRate}\n', '', 'TestAttachMeters', PROC),
 ('speed: the encoder given no fill', None, '\tap.Encoder.Fill = &foxSpeed.Fill{}\n', '', 'TestAttachMeters', PROC),
 ('speed: the decoder metered at the DSP\'s rate', None, '\tdec := &foxSpeed.Meter{Name: "decoder", Rate: ap.Decoder.SampleRate}', '\tdec := &foxSpeed.Meter{Name: "decoder", Rate: ap.ProcRate}', 'TestAttachMeters', PROC),
 ('speed report: none at stop', None, '\t\t\tcase <-done:\n\t\t\t\twrite()\n', '\t\t\tcase <-done:\n', 'TestReportSpeedToWritesAtStop', PROC),
 ('speed report: never rewritten while it runs', None, '\t\t\tcase <-tick.C:\n\t\t\t\twrite()\n', '\t\t\tcase <-tick.C:\n', 'TestReportSpeedTo', PROC),
 ('speed report: written before there are figures', None, '\t\tstage, audio, busy, over, ok := speed()\n\t\tif !ok {\n\t\t\treturn\n\t\t}\n', '\t\tstage, audio, busy, over, _ := speed()\n', 'TestReportSpeedTo', PROC),
 ('speed report: the fill never in it', None, '\tfill := ap.Encoder.Fill\n', '\tfill := &foxSpeed.Fill{}\n', 'TestReportSpeed', PROC),
 ('speed report: no busiest stage', None, '\t\t\tstage, _, _, _ := foxSpeed.Slowest(meters...)\n', '\t\t\tstage := ""\n', 'TestReportSpeed', PROC),
 ('speed report: over never said', None, '"busy": busy, "over": over}', '"busy": busy, "over": over && false}', 'TestReportSpeedTo', PROC),
 # the fill: the engine's speed flat out while the player's buffer fills (Simon's choice)
 ('fill: a held-back window joins it', None, '\tif f.winWait > fillWaitMin && float64(f.winWait) > fillWaitFrac*float64(span) {', '\tif false {', 'TestFill', SPD),
 ('fill: a short wait ends it', None, '\tif f.winWait > fillWaitMin && float64', '\tif float64', 'TestFillShortWaitsDoNotEndIt', SPD),
 ('fill: a small share ends it', None, 'float64(f.winWait) > fillWaitFrac*float64(span) {', 'float64(f.winWait) > 0.01*float64(span) {', 'TestFillShortWaitsDoNotEndIt', SPD),
 ('fill: the waits counted as work', None, '\tf.busy += (span - f.winWait).Seconds()', '\tf.busy += span.Seconds()', 'TestFill', SPD),
 ('fill: its last part never counted', None, '\tif f.begun && !f.over && f.winAudio > 0 {\n\t\tf.closeWindow()\n\t}\n', '', 'TestFillEnd', SPD),
 ('fill: writes after it counted', None, '\tif f.over {\n\t\treturn\n\t}\n\tif !f.begun {', '\tif !f.begun {', 'TestFill', SPD),
 ('fill: the clock from the first write\'s end', None, '\t\tf.begun, f.winStart = true, began', '\t\tf.begun, f.winStart = true, f.now()', 'TestFill', SPD),
 ('fill: the encoder\'s writes not counted', None, '\tif myEncoder.Filename == "" && myEncoder.adapterEncoder == nil && myEncoder.SampleRate > 0 {\n\t\tmyEncoder.Fill', '\tif false {\n\t\tmyEncoder.Fill', 'TestFillCountsTheLastPart', AE),
 ('fill: the write\'s wait not timed', None, 'float64(myEncoder.SampleRate), t0, time.Since(w0))', 'float64(myEncoder.SampleRate), t0, 0*time.Since(w0))', 'TestFillStopsWhenTheReaderHoldsBack', AE),
 ('fill: never ended', None, '\tdefer myEncoder.Fill.End() // a stream that ends inside the fill: its last part counts\n', '', 'TestFillCountsTheLastPart', AE),
 # the bounds-check round (0.1.72.35, Simon: "saving any time is plus"): the same sums, the same bits
 ('convolution: the odd bin left out', None, '\t\tfor ; i < len(res); i++ {\n\t\t\tres[i] += audioFFT[i] * impulseFFT[i]\n\t\t}\n', '', 'TestChunkBitsUnchanged', CONV),
 ('convolution: the chunk\'s first sample not taken', None, '\tcopy(realBuf[overlapSize:overlapSize+L], chunk[:L])', '\tcopy(realBuf[overlapSize+1:overlapSize+L], chunk[1:L])', 'TestChunkBitsUnchanged', CONV),
 ('convolution: a bin of four from the next partition\'s audio', None, '\t\t\tr4[3] += a4[3] * h4[3]', '\t\t\tr4[3] += a4[2] * h4[3]', 'TestChunkBitsUnchanged', CONV),
 ('poly: the fourth tap of each four dropped', 'poly.go', '\t\t\ta3 += hh[3] * ww[3]\n', '', 'TestSineExact', U('poly.go')),
 ('speed report: the stage under another name', None, '"pid": os.Getpid(), "stage": stage, ', '"pid": os.Getpid(), "name": stage, ', 'TestReportSpeedTo', PROC),
 # the FIR stage (0.1.72.36): FFT plans and pooled buffers above 8192 points (the same bits), larger partitions for a long
 # impulse at 176.4k and up (the same convolution to rounding), MaxGain still at Fox's size
 ('fft: a big plan of half the size', None, '\tbigPlanOnce[i].Do(func() { bigPlans[i] = buildPlan(N) })', '\tbigPlanOnce[i].Do(func() { bigPlans[i] = buildPlan(N / 2) })', 'TestBigPlansSameBits', FFT),
 ('fft: no complex plan above 8192', None, '\tif N <= FFTSize8192 || N > bigPlanMax || !IsPow2(N) {\n\t\treturn nil', '\tif true {\n\t\treturn nil', 'TestBigPlansExist', FFT),
 ('fft: no real plan above 8192', None, '\tif N <= RealFFTSize8192 || N > bigRealPlanMax || !IsPow2(N) {\n\t\treturn nil', '\tif true {\n\t\treturn nil', 'TestBigPlansExist', FFTR),
 ('fft: a big real plan of half the size', None, '{ bigRealPlans[i] = buildRealFFTPlan(N) }', '{ bigRealPlans[i] = buildRealFFTPlan(N / 2) }', 'TestBigPlansSameBits', FFTR),
 ('fft: real plans past their cap', None, 'const bigRealPlanMax = 1 << 18', 'const bigRealPlanMax = 1 << 19', 'TestBigPlansExist', FFTR),
 ('fft: big work buffers allocated', None, '        if p := bigWorkPool(M); p != nil {\n            return p.Get().(*[]complex128)\n        }\n', '', 'TestBigFFTDoesNotAllocate', FFTR),
 ('fft: big work buffers never returned', None, '        if bp := bigWorkPool(M); bp != nil {\n            bp.Put(p)\n        }\n', '', 'TestBigFFTDoesNotAllocate', FFTR),
 ('fft: a pooled buffer half the length', None, '\t\tbigWorkPools[i].New = func() any { buf := make([]complex128, m); return &buf }', '\t\tbigWorkPools[i].New = func() any { buf := make([]complex128, m/2); return &buf }', 'TestBigFFTDoesNotAllocate', FFTR),
 ('partitions: never larger than Fox\'s', None, '\tfor L < maxPartition && (taps+L-1)/L > partitionsWanted {', '\tfor false && (taps+L-1)/L > partitionsWanted {', 'TestPartitionSize', CONV),
 ('partitions: past the cap', None, '\tfor L < maxPartition && (taps+L-1)/L > partitionsWanted {', '\tfor L < 2*maxPartition && (taps+L-1)/L > partitionsWanted {', 'TestPartitionSize', CONV),
 ('partitions: one too many left at 8192', None, '\tfor L < maxPartition && (taps+L-1)/L > partitionsWanted {', '\tfor L < maxPartition && (taps+L-1)/L > partitionsWanted+1 {', 'TestPartitionSize', CONV),
 ('partitions: below 176.4k too', None, '\tif mode != foxUtils.ModeStreaming || sampleRate < 176400 {', '\tif mode != foxUtils.ModeStreaming || sampleRate < 96000 {', 'TestPartitionSize', CONV),
 ('partitions: in realtime mode too', None, '\tif mode != foxUtils.ModeStreaming || sampleRate < 176400 {', '\tif sampleRate < 176400 {', 'TestPartitionSize', CONV),
 ('partitions: the engine\'s convolver keeps Fox\'s', None, '\t\tSetPartitionSizes(partitionSize(sampleRate, len(impulse), ProcessingMode))', '\t\tSetPartitionSizes(foxUtils.CalculateChunkSize(sampleRate, ProcessingMode))', 'TestMaxGainIsTheFiltersPeak', CONV),
 # the headroom (0.1.72.37): MaxGain the whole filter's peak, not one partition's; the filter cache versioned for it
 ('MaxGain: Fox\'s per-partition measure back', None, '\tpc.MaxGain = peakGain(pc.FilterImpulse)', '\tpc.MaxGain = pc.maxGainFromPartitions()', 'TestMaxGainStrengthBlend', CONV),
 ('MaxGain: the peak not refined between bins', None, '\t\tif d := a - 2*y + c; d < 0 {', '\t\tif d := a - 2*y + c; false && d < 0 {', 'TestMaxGainBetweenBins', CONV),
 ('MaxGain: refined the wrong way', None, '\t\t\tpeak = math.Max(peak, math.Exp(y-0.125*(a-c)*(a-c)/d))', '\t\t\tpeak = math.Max(peak, math.Exp(y+0.125*(a-c)*(a-c)/d))', 'TestMaxGainBetweenBins', CONV),
 ('MaxGain: on a grid as long as the filter', None, '\tn := NextPowerOf2(max(4*len(h), 1<<16))', '\tn := NextPowerOf2(max(len(h), 1<<16))', 'TestMaxGainBetweenBins', CONV),
 ('MaxGain: the largest bin alone refined', None, '\tnear := top * math.Pow(10, -0.3/20)', '\tnear := top', 'TestMaxGainBetweenBins', CONV),
 ('cache: no version written', None, '\t\tfilterCacheVersion,\n\t\tPartitionedChunkSize,', '\t\tPartitionedChunkSize,', 'TestFilterCacheVersion', CONV),
 ('cache: the version not checked', None, '\tif version != filterCacheVersion {', '\tif false && version != filterCacheVersion {', 'TestFilterCacheVersion', CONV),
 ('cache: the partition sizes not put back', None, '\tPartitionedChunkSize = chunkSize\n\tPartitionedFFTSize = fftSize\n', '\tPartitionedFFTSize = fftSize\n', 'TestCacheKeepsPartitionSize', CONV),
 # the gapless hand-over (0.1.72.36): the next track goes on from the last whole partition
 ('join: the ring not put back after the padding', None, '\t\tpc.ringPosition, pc.ringFilled = pos, filled\n', '\t\t_, _ = pos, filled\n', 'TestGaplessJoin', CONV),
 ('join: the input short of a partition not kept', None, '\t\tpc.pending = append(pc.pending[:0], pc.Buffer...)', '\t\tpc.pending = pc.pending[:0]', 'TestGaplessJoin', CONV),
 ('join: the outputs sent already not dropped', None, '\tif pc.skip > 0 {\n\t\tn := min(pc.skip, len(pc.outputBuffer))', '\tif false {\n\t\tn := min(pc.skip, len(pc.outputBuffer))', 'TestGaplessJoin', CONV),
 ('join: the next track not started from that input', None, '\tpc.Buffer = append(make([]float64, 0, PartitionedChunkSize*2), pending...)', '\tpc.Buffer = make([]float64, 0, PartitionedChunkSize*2)', 'TestGaplessJoin', CONV),
 ('join: that input not in the saved state', None, '\t\tappend([]float64{}, pc.pending...), //', '\t\t[]float64{}, //', 'TestGaplessJoin', CONV),
 ('join: the last track\'s outputs dropped twice', None, '\tpc.pending, pc.skip = nil, len(pending)', '\tpc.pending, pc.skip = nil, 2*len(pending)', 'TestGaplessJoin', CONV),
]
env = dict(os.environ)
killed = 0
for m in M:
    name, f, a, b, test = m[:5]
    mod, rel, pkg = (m[5] if len(m) > 5 else E(f))[:3]
    testmod = (m[5] + (mod,))[3] if len(m) > 5 else mod  # the module whose test runs: the mutated one, or one using it
    dst = os.path.join(work, mod)
    shutil.rmtree(dst, ignore_errors=True)
    shutil.copytree(os.path.join(repo, 'engine', mod), dst)
    if mod != 'foxAudioLib':  # its go.mod replaces foxAudioLib with ../foxAudioLib: a clean copy beside it
        shutil.rmtree(os.path.join(work, 'foxAudioLib'), ignore_errors=True)
        shutil.copytree(os.path.join(repo, 'engine', 'foxAudioLib'), os.path.join(work, 'foxAudioLib'))
    if testmod != mod:  # a fresh copy of the module whose test runs, using the mutated one beside it
        shutil.rmtree(os.path.join(work, testmod), ignore_errors=True)
        shutil.copytree(os.path.join(repo, 'engine', testmod), os.path.join(work, testmod))
    p = os.path.join(dst, rel)
    s = open(p).read()
    if s.count(a) != 1:
        print('SETUP   %-48s anchor found %d times' % (name, s.count(a)), flush=True); continue
    open(p, 'w').write(s.replace(a, b))
    # -vet=off: Fox's foxAudioEncoder does not pass vet (its Sprintf calls), so vet would fail every build there;
    # a mutant is judged by the tests
    r = subprocess.run(['/usr/local/go/bin/go', 'test', '-vet=off', '-count=1', '-run', '^%s$' % test, pkg],
                       cwd=os.path.join(work, testmod), env=env, capture_output=True, text=True, timeout=900)
    dead = r.returncode != 0 and ('FAIL' in r.stdout) and 'build failed' not in r.stdout + r.stderr
    killed += dead
    print('%-7s %-48s by %s%s' % ('KILLED' if dead else 'LIVED', name, test, '' if dead else ('  ' + (r.stdout + r.stderr)[-300:])), flush=True)
print('%d of %d mutants killed' % (killed, len(M)))
sys.exit(0 if killed == len(M) else 1)
