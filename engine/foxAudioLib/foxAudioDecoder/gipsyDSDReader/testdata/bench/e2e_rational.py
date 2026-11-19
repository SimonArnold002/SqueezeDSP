#!/usr/bin/env python3
# End to end for the decoder's stage 3 (targets 352.8k does not divide by a whole number: the 48k family, and
# 705.6k from DSD64), against today's rule for them (ffmpeg's soxr | engine). Reuses e2e_dsd.py's engine setup.
#  1. control: every 44.1k-family target is byte-identical to the engine before stage 3 (<before engine>)
#  2. level and noise against today's rule, WAV length; 3. seeks, cue ends and a cue track byte-exact;
#  4. a gapless join at 96k; 5. refusals; 6. speed over 60 s (the long_*.dsf in <fixtures>/.. or <work>).
# Run: python3 -I e2e_rational.py <engine> <ffmpeg> <dsdana> <fixtures dir> <work dir> <before engine> [nospeed]
import sys, os, shutil, subprocess, importlib.util, resource, math
ENGINE, FF, ANA, FX, W, BEFORE = sys.argv[1:7]
SPEED = 'nospeed' not in sys.argv[7:]
HERE = os.path.dirname(os.path.abspath(__file__))
sys.argv = [sys.argv[0], ENGINE, FF, ANA, FX, W, 'setup-only']
spec = importlib.util.spec_from_file_location('e2e', os.path.join(HERE, 'e2e_dsd_setup.py'))
e = importlib.util.module_from_spec(spec); spec.loader.exec_module(e)
TOOL = os.path.join(HERE, 'dsftool.py')
TALLY = [0, 0]
def ok(cond, label):
    TALLY[0 if cond else 1] += 1
    print(('ok     ' if cond else 'NOT OK ') + label, flush=True)

# the engine before stage 3, beside the new one with the same config
bdir = os.path.join(W, 'before-bin')
shutil.rmtree(bdir, ignore_errors=True)
shutil.copytree(os.path.join(e.root, 'bin'), bdir)
shutil.copy(BEFORE, os.path.join(bdir, 'SqueezeDSP'))
def before(path, rate, cutoff=30000, gain=0):
    e.clear()
    return subprocess.run([os.path.join(bdir, 'SqueezeDSP')] + e.BASE[1:] + ['--formatin=DSD', '--input', path,
                          '--samplerate=%d' % rate, '--dsdcutoff=%d' % cutoff, '--dsdgain=%g' % gain], capture_output=True, timeout=600)

lv = lambda s: float(s.split('tone')[1].split()[0])

print('== 1. control: 44.1k-family targets byte-identical to the engine before stage 3')
for f, rate, cut, gain in (('d64_1k.dsf', 352800, 30000, 0), ('d64_1k.dsf', 176400, 0, 0), ('d64_20k.dsf', 88200, 24000, 0),
                           ('d64_1k.dsf', 44100, 50000, 6), ('d128_1k.dsf', 705600, 0, 0), ('d128_1k.dsf', 705600, 30000, 0),
                           ('d256_1k.dsf', 352800, 30000, 0), ('d512_1k.dsf', 176400, 30000, 0), ('d64_1k.dff', 88200, 30000, 0)):
    path = os.path.join(FX, f)
    a, b = e.ours(path, rate, cutoff=cut, gain=gain)[0], before(path, rate, cutoff=cut, gain=gain)
    ok(a.returncode == 0 and b.returncode == 0 and a.stdout == b.stdout and len(a.stdout) > 44,
       '%s at %d cutoff %d gain %d: %d bytes, identical' % (f, rate, cut, gain, len(a.stdout)))
p = before(os.path.join(FX, 'd64_1k.dsf'), 96000)
ok(p.returncode != 0, 'control of the control: the engine before refuses 96k (rc %d)' % p.returncode)

print('== 2. level and noise against today\'s rule (ffmpeg soxr | engine), cutoff 30k unless named')
cases = [('d64_1k.dsf', 1000, r, 30000) for r in (48000, 96000, 192000, 384000, 705600, 768000)]
cases += [('d64_20k.dsf', 20000, 96000, 30000), ('d64_20k.dsf', 20000, 705600, 30000), ('d64_1k.dff', 1000, 96000, 30000)]
cases += [(f, 1000, r, 30000) for f in ('d128_1k.dsf', 'd256_1k.dsf', 'd512_1k.dsf') for r in (96000, 768000)]
cases += [('d64_1k.dsf', 1000, 384000, c) for c in (0, 24000, 50000)] + [('d64_1k.dsf', 1000, 96000, 0)]
seen = set()
for f, tone, rate, cut in cases:
    path = os.path.join(FX, f)
    p, _ = e.ours(path, rate, cutoff=cut)
    ok(p.returncode == 0, '%s at %d cutoff %d: ran (rc %d)%s' % (f, rate, cut, p.returncode, '' if p.returncode == 0 else ' ' + p.stderr[-300:].decode(errors='replace')))
    if p.returncode:
        continue
    size, data, r, bits, ch = e.wav(p.stdout)
    ok(r == rate and bits == 24 and ch == 2 and size == len(data) and len(data) == 6 * rate * 6,
       '%s at %d: WAV 24/%d x%d, header %d = %d written = 6.000 s' % (f, rate, r, ch, size, len(data)))
    o = e.ana(data, rate, tone, 'ours  %s %d c%d' % (f, rate, cut))
    if (f, rate) not in seen:
        seen.add((f, rate))
        q, _ = e.todays(path, rate)
        o2 = e.ana(e.wav(q.stdout)[1], rate, tone, 'today %s %d' % (f, rate)) if q.returncode == 0 else ''
        if o2 and 'tone' in o2:
            ok(abs(lv(o) - lv(o2)) < 0.005, '%s at %d: level %.3f, today\'s %.3f' % (f, rate, lv(o), lv(o2)))
p, _ = e.ours(os.path.join(FX, 'd64_1k.dsf'), 96000, cutoff=30000, gain=6)
o = e.ana(e.wav(p.stdout)[1], 96000, 1000, 'ours d64 96k +6 dB')
ok(abs(lv(o) - (-6.041)) < 0.01, 'gain 6 at 96k: tone at %.3f dBFS (want -6.04)' % lv(o))

print('== 3. seek (--skip), cue end (--until), cue track: byte-exact against the full run')
for f, rate in (('d64_1k.dsf', 96000), ('d64_1k.dsf', 705600), ('d128_1k.dsf', 384000), ('d512_1k.dsf', 768000),
                ('d64_1k.dff', 48000), ('d256_1k.dsf', 192000)):
    path = os.path.join(FX, f)
    full = e.wav(e.ours(path, rate)[0].stdout)[1]
    for t in (1.37, 2.5, 4.01):
        part = e.wav(e.ours(path, rate, skip=e.mmss(t))[0].stdout)
        k = round(t * rate) * 6
        ok(part[0] == len(part[1]) and part[1] == full[k:], '%s at %d --skip=%s: the full run from frame %d' % (f, rate, e.mmss(t), k // 6))
    head = e.wav(e.ours(path, rate, until=e.mmss(2.5))[0].stdout)
    tail = e.wav(e.ours(path, rate, skip=e.mmss(2.5))[0].stdout)
    ok(head[1] + tail[1] == full and head[0] == len(head[1]), '%s at %d: --until then --skip at 2.5 s join to the full run' % (f, rate))
    cue = e.wav(e.ours(path, rate, skip=e.mmss(1.0), until=e.mmss(3.0))[0].stdout)
    ok(cue[1] == full[rate * 6:3 * rate * 6] and cue[0] == len(cue[1]), '%s at %d: a cue track 1-3 s is exactly those frames' % (f, rate))

print('== 4. gapless at 96k: one stream split at 3.00 s, each half decoded alone and joined, against the whole')
src = os.path.join(FX, 'd64_1k.dsf')
a, b = os.path.join(W, 'split_a.dsf'), os.path.join(W, 'split_b.dsf')
subprocess.run(['python3', '-I', TOOL, 'split', src, str(3 * 352800), a, b], check=True)
def s24(data):
    return [int.from_bytes(data[i:i + 3], 'little', signed=True) for i in range(0, len(data), 3)]
for name, fn in (('ours, cutoff 30k', lambda p: e.wav(e.ours(p, 96000)[0].stdout)[1]),
                 ('ffmpeg|engine', lambda p: e.wav(e.todays(p, 96000)[0].stdout)[1])):
    whole, joined = fn(src), fn(a) + fn(b)
    if len(joined) != len(whole):
        ok(False, '%s: joined %d bytes, whole %d' % (name, len(joined), len(whole)))
        continue
    d = [abs(x - y) for x, y in zip(s24(whole), s24(joined))]
    near = [i // 2 for i, v in enumerate(d) if v > 0]
    print('       %-17s worst %7d LSB (%7.1f dBFS), %6d samples differ, frames %d-%d (join at %d)' % (name, max(d),
          20 * math.log10(max(max(d), 1) / 8388608), len(near), min(near) if near else 0, max(near) if near else 0, 3 * 96000), flush=True)
    ok(True, '%s: measured' % name)

print('== 5. refused, no audio written')
for rate in (32000, 800000, 44101):
    p, _ = e.ours(os.path.join(FX, 'd64_1k.dsf'), rate)
    ok(p.returncode != 0 and p.stdout[:4] != b'RIFF', '%d Hz target refused (rc %d)' % (rate, p.returncode))

if SPEED:
    print('== 6. speed over 60 s (the 6 s fixtures tiled x10), WAV 24-bit out, cutoff 30k')
    def cpu():
        r = resource.getrusage(resource.RUSAGE_CHILDREN)
        return r.ru_utime + r.ru_stime
    for rate in (96000, 384000, 705600, 768000):
        for f in ('d64_1k.dsf', 'd128_1k.dsf', 'd256_1k.dsf', 'd512_1k.dsf'):
            big = os.path.join(W, 'long_' + f)
            if not os.path.exists(big):
                subprocess.run(['python3', '-I', TOOL, 'tile', os.path.join(FX, f), '10', big], check=True)
            for name, fn in (('ours', lambda: e.ours(big, rate)), ('ffmpeg|engine', lambda: e.todays(big, rate))):
                c0 = cpu(); p, wall = fn(); c1 = cpu()
                secs = (len(p.stdout) - 44) / 6 / rate
                print('       %6d %-12s %-14s %5.1f s audio: wall %6.2f s = %6.1fx realtime, CPU %6.2f s = %6.1fx per CPU second' % (
                    rate, f, name, secs, wall, secs / wall, c1 - c0, secs / (c1 - c0)), flush=True)
                ok(p.returncode == 0 and abs(secs - 60.0) < 0.01, '%d %s %s: 60 s decoded' % (rate, f, name))

print('\n%d ok, %d NOT OK' % tuple(TALLY))
sys.exit(1 if TALLY[1] else 0)
