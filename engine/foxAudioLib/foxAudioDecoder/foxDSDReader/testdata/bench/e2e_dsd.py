#!/usr/bin/env python3
# End to end: the engine (built from engine/ for this Mac, scratchpad) decoding DSD itself
# (--formatin=DSD --input <file>), against today's rule (ffmpeg | engine --formatin=PCM), both writing WAV
# 24-bit. Engine config as t/engine_gain.py's Bench: no FIR, no PEQ, ReplayGain off, dither 0.
# Run: python3 -I e2e_dsd.py <engine> <ffmpeg> <dsdana> <fixtures dir> <work dir>
import sys, os, json, struct, subprocess, time, shutil

ENGINE, FF, ANA, FX, W = sys.argv[1:6]
SOX = '/Users/simona/Documents/GitHub/SqueezeDSP/t/lmsbin/Bin/darwin/sox'
TALLY = [0, 0]
def ok(cond, label):
    TALLY[0 if cond else 1] += 1
    print(('ok     ' if cond else 'NOT OK ') + label, flush=True)

root = os.path.join(W, 'e2e-root')
shutil.rmtree(root, ignore_errors=True)
for d in ('bin', 'settings', 'temp'):
    os.makedirs(os.path.join(root, d))
shutil.copy(ENGINE, os.path.join(root, 'bin', 'SqueezeDSP'))
log = os.path.join(root, 'engine.log')
open(log, 'w').close()
json.dump({'settings': {'partitions': '0', 'convolvergain': '-4', 'soxExe': SOX, 'tail': 'true', 'dither': '0',
                        'debug': 'true', 'logFile': log, 'gain': '-10', 'pluginDataFolder': root,
                        'settingsDataFolder': os.path.join(root, 'settings'), 'impulseDataFolder': root,
                        'tempDataFolder': os.path.join(root, 'temp'), 'lmsHost': '127.0.0.1', 'lmsPort': '9'}},
          open(os.path.join(root, 'bin', 'SqueezeDSP_config.json'), 'w'))
json.dump({'Client': {'Bypass': '0', 'Loudness': {'listening_level': 85, 'enabled': 0}, 'Balance': 0, 'Width': 0,
                      'FIRWavFile': '-', 'Preset': 'None', 'FIRStrength': 100, 'Crossfeed': 'off', 'Delay': {'delay': 0},
                      'Filters': [], 'Preamp': 0, 'ReplayGain': {'enabled': '0', 'mode': 2, 'fixed_gain': 0}}},
          open(os.path.join(root, 'settings', 'bench.settings.json'), 'w'))
BIN = os.path.join(root, 'bin', 'SqueezeDSP')
BASE = [BIN, '--trackURL=file:///bench/t.dsf', '--Clientid=bench', '--bitsout=24', '--formatout=WAV']

def clear():
    for f in os.listdir(os.path.join(root, 'temp')):
        os.remove(os.path.join(root, 'temp', f))

def wav(b):
    """(stated data size, data bytes, rate, bits, channels)"""
    assert b[:4] == b'RIFF', b[:40]
    i, fmt = 12, None
    while b[i:i + 4] != b'data':
        n = struct.unpack('<I', b[i + 4:i + 8])[0]
        if b[i:i + 4] == b'fmt ':
            fmt = struct.unpack('<HHIIHH', b[i + 8:i + 24])
        i += 8 + n + (n & 1)
    size = struct.unpack('<I', b[i + 4:i + 8])[0]
    return size, b[i + 8:], fmt[2], fmt[5], fmt[1]

def ours(path, rate, cutoff=30000, gain=0, skip=None, until=None):
    clear()
    a = BASE + ['--formatin=DSD', '--input', path, '--samplerate=%d' % rate, '--dsdcutoff=%d' % cutoff, '--dsdgain=%g' % gain]
    if skip: a.append('--skip=%s' % skip)
    if until: a.append('--until=%s' % until)
    t0 = time.time()
    p = subprocess.run(a, capture_output=True, timeout=600)
    return p, time.time() - t0

def todays(path, rate):
    clear()
    ff = [FF, '-loglevel', 'quiet', '-i', path, '-af', 'aresample=%d:resampler=soxr:precision=28:cheby=0:internal_sample_fmt=dblp' % rate,
          '-c:a', 'pcm_s24le', '-ar', str(rate), '-ac', '2', '-f', 's24le', '-']
    t0 = time.time()
    f = subprocess.Popen(ff, stdout=subprocess.PIPE)
    p = subprocess.run(BASE + ['--formatin=PCM', '--be=false', '--bitsin=24', '--samplerate=%d' % rate, '--channels=2'],
                       stdin=f.stdout, capture_output=True, timeout=600)
    f.wait()
    return p, time.time() - t0

def ana(data, rate, tone, label):
    r = subprocess.run([ANA, '-s24', '-rate', str(rate), '-tone', str(tone), '-label', label, '-log2n', '20' if rate >= 176400 else '18'],
                       input=data, capture_output=True)
    out = r.stdout.decode()
    print('       ' + out.strip(), flush=True)
    return out

def mmss(s):   # LMS's %t / %v: Slim::Utils::DateTime::fracSecToMinSec
    return '%d:%02d.%s' % (int(s // 60), int(s % 60), str(int(s * 100) + 100)[-2:])

print('== level and noise: ours vs today\'s rule, through the engine')
for f, tone in (('d64_1k.dsf', 1000), ('d64_20k.dsf', 20000), ('d128_1k.dsf', 1000), ('d256_1k.dsf', 1000), ('d512_1k.dsf', 1000), ('d64_1k.dff', 1000)):
    path = os.path.join(FX, f)
    p, t1 = ours(path, 352800)
    ok(p.returncode == 0, '%s: our engine ran (rc %d)%s' % (f, p.returncode, '' if p.returncode == 0 else ' ' + p.stderr[-300:].decode(errors='replace')))
    if p.returncode:
        continue
    size, data, rate, bits, ch = wav(p.stdout)
    ok(rate == 352800 and bits == 24 and ch == 2, '%s: WAV 24/352.8k stereo (got %d/%d x%d)' % (f, bits, rate, ch))
    ok(size == len(data), '%s: WAV header states %d bytes, %d written' % (f, size, len(data)))
    o = ana(data, 352800, tone, 'ours ' + f)
    q, t2 = todays(path, 352800)
    _, d2, *_ = wav(q.stdout)
    o2 = ana(d2, 352800, tone, 'today ' + f)
    lv = lambda s: float(s.split('tone')[1].split()[0])
    ok(abs(lv(o) - lv(o2)) < 0.005, '%s: level %.3f, today\'s %.3f' % (f, lv(o), lv(o2)))
    secs = len(data) / 6 / 352800
    print('       speed: ours %.1fx, ffmpeg|engine %.1fx (%.2f s of audio)' % (secs / t1, secs / t2, secs))

print('== 88.2k target, cutoff 24k, gain 6')
p, _ = ours(os.path.join(FX, 'd128_1k.dsf'), 88200, cutoff=24000, gain=6)
size, data, rate, *_ = wav(p.stdout)
ok(rate == 88200 and size == len(data), 'WAV 88.2k, header %d = %d written' % (size, len(data)))
o = ana(data, 88200, 1000, 'ours d128 -> 88.2k +6 dB')
ok(abs(float(o.split('tone')[1].split()[0]) - (-6.041)) < 0.01, 'gain 6: tone at -6.04 dBFS')

print('== seek (--skip, LMS %t) and cue end (--until, LMS %v): sample-exact against the full run')
for f in ('d64_1k.dsf', 'd128_1k.dsf', 'd256_1k.dsf', 'd512_1k.dsf', 'd64_1k.dff'):
    path = os.path.join(FX, f)
    full = wav(ours(path, 352800)[0].stdout)[1]
    for t in (1.37, 2.5, 4.01):
        part = wav(ours(path, 352800, skip=mmss(t))[0].stdout)
        k = round(t * 352800) * 6
        ok(part[0] == len(part[1]) and part[1] == full[k:], '%s --skip=%s: %d bytes, the full run\'s from frame %d%s' % (
            f, mmss(t), len(part[1]), k // 6, '' if part[1] == full[k:] else ' DIFFERS (%d vs %d)' % (len(part[1]), len(full) - k)))
    head = wav(ours(path, 352800, until=mmss(2.5))[0].stdout)
    tail = wav(ours(path, 352800, skip=mmss(2.5))[0].stdout)
    ok(head[1] + tail[1] == full and head[0] == len(head[1]), '%s: --until=2:50 then --skip=2:50 join to the full run (%d + %d = %d)' % (
        f, len(head[1]), len(tail[1]), len(full)))
    cue = wav(ours(path, 352800, skip=mmss(1.0), until=mmss(3.0))[0].stdout)
    ok(cue[1] == full[352800 * 6:3 * 352800 * 6] and cue[0] == len(cue[1]), '%s: a cue track 1:00-3:00 is exactly those frames' % f)

print('== refused, with the reason in the log')
dst = os.path.join(W, 'dst.dff')
b = bytearray(open(os.path.join(FX, 'd64_1k.dff'), 'rb').read())
i = b.index(b'CMPR')   # 'CMPR', 8-byte size, then the compression id
assert b[i + 12:i + 16] == b'DSD '
b[i + 12:i + 16] = b'DST '
open(dst, 'wb').write(b)
off = os.path.getsize(log)
p, _ = ours(dst, 352800)
text = open(log, errors='replace').read()[off:] + p.stderr.decode(errors='replace')
ok(p.returncode != 0 and 'ffmpeg decodes it' in text, 'DST-compressed DFF: engine exits %d and says why' % p.returncode)
p, _ = ours(os.path.join(FX, 'd64_1k.dsf'), 96000)
ok(p.returncode != 0 and b'RIFF' not in p.stdout[:4], '96 kHz target refused, no audio written (rc %d)' % p.returncode)
p = subprocess.run(BASE + ['--formatin=DSD', '--samplerate=352800'], capture_output=True)
ok(p.returncode != 0, 'DSD without --input refused (rc %d)' % p.returncode)

print('\n%d ok, %d NOT OK' % tuple(TALLY))
sys.exit(1 if TALLY[1] else 0)
