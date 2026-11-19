# The engine setup and helpers of e2e_dsd.py (its tests cut off), for e2e_dsd2.py to import.
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

