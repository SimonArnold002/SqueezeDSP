#!/usr/bin/env python3
# Second end-to-end pass for the engine's own DSD decoder: speed over a minute, gapless joins against today's
# route, Bypass, FLAC out. Reuses e2e_dsd.py's engine setup by import.
# Run: python3 -I e2e_dsd2.py <engine> <ffmpeg> <dsdana> <fixtures dir> <work dir>
import sys, os, subprocess, time, resource, struct, importlib.util, json
ENGINE, FF, ANA, FX, W = sys.argv[1:6]
HERE = os.path.dirname(os.path.abspath(__file__))
sys.argv = [sys.argv[0], ENGINE, FF, ANA, FX, W, 'setup-only']
spec = importlib.util.spec_from_file_location('e2e', os.path.join(HERE, 'e2e_dsd_setup.py'))
e = importlib.util.module_from_spec(spec); spec.loader.exec_module(e)
FLAC = '/Users/simona/Documents/GitHub/SqueezeDSP/t/lmsbin/Bin/darwin/flac'
TOOL = os.path.join(HERE, 'dsftool.py')
TALLY = [0, 0]
def ok(cond, label):
    TALLY[0 if cond else 1] += 1
    print(('ok     ' if cond else 'NOT OK ') + label, flush=True)

def cpu():
    r = resource.getrusage(resource.RUSAGE_CHILDREN)
    return r.ru_utime + r.ru_stime

print('== speed over 60 s (the 6 s fixtures tiled x10), WAV 24/352.8k out, cutoff 30k')
for f in ('d64_1k.dsf', 'd128_1k.dsf', 'd256_1k.dsf', 'd512_1k.dsf'):
    big = os.path.join(W, 'long_' + f)
    if not os.path.exists(big):
        subprocess.run(['python3', '-I', TOOL, 'tile', os.path.join(FX, f), '10', big], check=True)
    for name, fn in (('ours', lambda: e.ours(big, 352800)), ('ffmpeg|engine', lambda: e.todays(big, 352800))):
        c0 = cpu(); p, wall = fn(); c1 = cpu()
        secs = (len(p.stdout) - 44) / 6 / 352800
        print('       %-12s %-14s %5.1f s audio: wall %6.2f s = %5.1fx realtime, CPU %6.2f s = %5.1fx per CPU second' % (
            f, name, secs, wall, secs / wall, c1 - c0, secs / (c1 - c0)), flush=True)
        ok(p.returncode == 0 and abs(secs - 60.0) < 0.01, '%s %s: 60 s decoded' % (f, name))

print('== gapless: one stream split at 3.00 s into two files, each decoded alone and joined, against the whole')
src = os.path.join(FX, 'd64_1k.dsf')
a, b = os.path.join(W, 'split_a.dsf'), os.path.join(W, 'split_b.dsf')
subprocess.run(['python3', '-I', TOOL, 'split', src, str(3 * 352800), a, b], check=True)
def s24(data):
    return [int.from_bytes(data[i:i + 3], 'little', signed=True) for i in range(0, len(data), 3)]
for name, fn in (('ours, cutoff 30k', lambda p: e.ours(p, 352800)), ('ours, no cutoff', lambda p: e.ours(p, 352800, cutoff=0)),
                 ('ffmpeg|engine', lambda p: e.todays(p, 352800))):
    whole = e.wav(fn(src)[0].stdout)[1]
    joined = e.wav(fn(a)[0].stdout)[1] + e.wav(fn(b)[0].stdout)[1]
    if len(joined) != len(whole):
        ok(False, '%s: joined %d bytes, whole %d' % (name, len(joined), len(whole)))
        continue
    w, j = s24(whole), s24(joined)
    d = [abs(x - y) for x, y in zip(w, j)]
    worst = max(d)
    near = [i // 2 for i, v in enumerate(d) if v > 0]
    span = (min(near), max(near)) if near else (0, 0)
    print('       %-17s worst %7d LSB (%7.1f dBFS), %6d samples differ, frames %d-%d (join at %d)' % (
        name, worst, 20 * __import__('math').log10(max(worst, 1) / 8388608), len(near), span[0], span[1], 3 * 352800), flush=True)
    ok(True, '%s: measured' % name)

print('== Bypass on: the engine still decodes DSD (no DSP)')
st = os.path.join(e.root, 'settings', 'bench.settings.json')
s = json.load(open(st)); s['Client']['Bypass'] = '1'; json.dump(s, open(st, 'w'))
p, _ = e.ours(src, 352800)
s['Client']['Bypass'] = '0'; json.dump(s, open(st, 'w'))
ok(p.returncode == 0 and p.stdout[:4] == b'RIFF', 'Bypass: rc %d, WAV out' % p.returncode)
if p.returncode == 0:
    o = e.ana(e.wav(p.stdout)[1], 352800, 1000, 'bypass d64_1k')
    ok(abs(float(o.split('tone')[1].split()[0]) + 12.041) < 0.01, 'Bypass: tone at -12.04 dBFS')

print('== FLAC out (--formatout=FLC, through sox): within 2 LSB of WAV out')
wavd = e.wav(e.ours(src, 352800)[0].stdout)[1]
e.clear()
p = subprocess.run([e.BIN, '--trackURL=file:///bench/t.dsf', '--Clientid=bench', '--bitsout=24', '--formatout=FLC',
                    '--formatin=DSD', '--input', src, '--samplerate=352800', '--dsdcutoff=30000'], capture_output=True, timeout=600)
ok(p.returncode == 0 and p.stdout[:4] == b'fLaC', 'FLC: rc %d, FLAC stream' % p.returncode)
if p.returncode == 0:
    d = subprocess.run([FLAC, '-dcs', '--force-raw-format', '--endian=little', '--sign=signed', '-'], input=p.stdout, capture_output=True).stdout
    # FLC and WAV out differ by up to 2 LSB for every input (today's PCM route too). NOT sox dither: measured 0
    # differences with and without -D (ledger A3 SOX DOES NOT DITHER AT 24 BITS); cause in the engine's writers
    s1, s2 = s24(d), s24(wavd)
    worst = max(abs(x - y) for x, y in zip(s1, s2)) if len(s1) == len(s2) else -1
    ok(len(s1) == len(s2) and 0 <= worst <= 2, 'FLC: the WAV run\'s samples within 2 LSB (worst %d LSB, %d vs %d samples)' % (worst, len(s1), len(s2)))

print('\n%d ok, %d NOT OK' % tuple(TALLY))
sys.exit(1 if TALLY[1] else 0)
