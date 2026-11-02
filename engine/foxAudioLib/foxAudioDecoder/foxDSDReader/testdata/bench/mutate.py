# Mutation check for foxDSDReader: each mutant is applied to a fresh copy of foxAudioLib in the scratchpad
# and must make the named test FAIL. Run: python3 -I mutate.py <repo> <work>
import sys, os, shutil, subprocess
repo, work = sys.argv[1], sys.argv[2]
DEC = ('foxAudioLib', 'foxAudioDecoder/foxDSDReader/foxDSDReader.go', './foxAudioDecoder/foxDSDReader/')
SET = ('foxLyrionDSP', 'LyrionDSPSettings/LyrionDSPSettings.go', './LyrionDSPSettings/')
M = [
 ('delay not removed', '	r.delay = int64(d1 - 1 + c2)', '	r.delay = 0', 'TestAlignment'),
 ('stage 1 delay off by one', '	r.delay = int64(d1 - 1 + c2)', '	r.delay = int64(d1 + c2)', 'TestAlignment'),
 ('gain ignored', '	g := math.Pow(10, r.GainDB/20)', '	g := 1.0', 'TestGain'),
 ('stage 1 at 120 dB', 'float64(mid)-80000, 160)/(8*r.every)', 'float64(mid)-80000, 120)/(8*r.every)', 'TestFilterResponse'),
 ('seek start floored', '	r.kStart = int64(math.Round(r.StartTime.Seconds() * float64(r.OutRate)))', '	r.kStart = int64(r.StartTime.Seconds() * float64(r.OutRate))', 'TestSeekAndUntil'),
 ('DSF bits not reversed', 'out[c][got] = rev[buf[int64(c)*block+i]]', 'out[c][got] = buf[int64(c)*block+i]', 'TestDSFEqualsDFF'),
 ('pair partner leaks', '				b = mid[2*p+1]', '				b = mid[2*p]', 'TestThreeChannels'),
 ('DST accepted', '			return nil, errors.New("DSDIFF: DST-compressed: ffmpeg decodes it")', '			dataStart, _ = f.Seek(0, io.SeekCurrent); dataLen = size', 'TestRefused'),
 ('seek off the FFT grid', '	j0 := (floorDiv(jFirst, grid) - 1) * grid', '	j0 := jFirst - grid', 'TestSeekAndUntil'),
 ('CMPR DST accepted', '					if ss >= 4 && string(d[:4]) != "DSD " {', '					if false && ss >= 4 && string(d[:4]) != "DSD " {', 'TestRefused'),
 ('cutoff cap removed', '	if r.Cutoff != 0 && (r.Cutoff <= 20000 || r.Cutoff > 60000) {', '	if r.Cutoff != 0 && r.Cutoff <= 20000 {', 'TestRefused'),
 ('flag: cutoff to 100 kHz', '(val > 20000 && val <= 60000)', '(val > 20000 && val <= 100000)', 'TestDSDArgs', SET),
 ('flag: --until on any input', '		if strings.ToUpper(args.InputFormat) != "DSD" { // only the DSD decoder honours it: refused, not ignored', '		if false {', 'TestDSDArgs', SET),
 ('cut-short DSF not capped', '	if blocks := dataLen / (block * int64(ch)); total > blocks*block {', '	if blocks := dataLen / (block * int64(ch)); false && total > blocks*block {', 'TestCutShort'),
 ('passband not flat (cutoff edge at 15 kHz)', '			pass, stop = r.Cutoff-tw/2, r.Cutoff+tw/2', '			pass, stop = 15000, r.Cutoff+tw/2', 'TestLevelAndFlatness'),
 # stage 3 (the 48k family, 705.6k from DSD64)
 ('stage 3 phase off by one', '				taps := r.ph[k*int64(r.down)%int64(r.up)]', '				taps := r.ph[(k*int64(r.down)+1)%int64(r.up)]', 'TestStage3IsTheSignalAtEachFrame'),
 ('stage 3 taps not scaled by up', '					r.ph[p][t] = float64(L) * h[i]', '					r.ph[p][t] = h[i]', 'TestLevelAndFlatness'),
 ('stage 3 length for 150 dB', '		r.ki = (halfLength(fs, stop, float64(mid)-stop, 155) + L - 1) / L', '		r.ki = (halfLength(fs, stop, float64(mid)-stop, 150) + L - 1) / L', 'TestFilterResponse'),
 ('stage 2 not narrowed for stage 3', '	if r.up > 0 && stop > 100000 {', '	if false && r.up > 0 && stop > 100000 {', 'TestFilterResponse'),
 ('stage 3 drops one output too many', '			if n := min(lo-hb, int64(len(hist[0]))); n > 0 {', '			if n := min(lo-hb+1, int64(len(hist[0]))); n > 0 {', 'TestLevelAndFlatness'),
 ('stage 3 history numbered one late', '	hb := j0', '	hb := j0 + 1', 'TestAlignment'),
 ('frames counted in whole stage 1 outputs', '	last := r.src.bytes * 8 * int64(r.OutRate) / int64(r.DSDRate)', '	last := r.src.bytes / int64(r.every*r.dec)', 'TestAlignment'),
 ('705.6k from DSD64 by whole numbers', '	if r.OutRate == 2*DXD && r.DSDRate >= 16*DXD {', '	if r.OutRate == 2*DXD {', 'TestLevelAndFlatness'),
]
env = dict(os.environ)
killed = 0
for m in M:
    name, a, b, test = m[:4]
    mod, rel, pkg = m[4] if len(m) > 4 else DEC
    dst = os.path.join(work, mod)
    shutil.rmtree(dst, ignore_errors=True)
    shutil.copytree(os.path.join(repo, 'engine', mod), dst)
    if mod != 'foxAudioLib':  # its go.mod replaces foxAudioLib with ../foxAudioLib: a clean copy beside it
        shutil.rmtree(os.path.join(work, 'foxAudioLib'), ignore_errors=True)
        shutil.copytree(os.path.join(repo, 'engine', 'foxAudioLib'), os.path.join(work, 'foxAudioLib'))
    p = os.path.join(dst, rel)
    s = open(p).read()
    if s.count(a) != 1:
        print('SETUP  %-40s anchor not found once' % name); continue
    open(p, 'w').write(s.replace(a, b))
    r = subprocess.run(['/usr/local/go/bin/go', 'test', '-count=1', '-run', '^%s$' % test, pkg],
                       cwd=dst, env=env, capture_output=True, text=True, timeout=600)
    dead = r.returncode != 0 and ('FAIL' in r.stdout) and 'build failed' not in r.stdout + r.stderr
    killed += dead
    print('%-7s %-42s by %s%s' % ('KILLED' if dead else 'LIVED', name, test, '' if dead else ('  ' + (r.stdout + r.stderr)[-200:])))
print('%d of %d mutants killed' % (killed, len(M)))
