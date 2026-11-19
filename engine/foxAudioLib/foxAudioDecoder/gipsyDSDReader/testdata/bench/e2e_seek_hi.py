#!/usr/bin/env python3
# Seeks (--skip) and cue ends (--until) at DSD256 and DSD512, byte-exact against the full decode, at 352.8k and
# 88.2k (e2e_dsd.py covers 352.8k only). Reuses e2e_dsd.py's engine setup by import.
# Run: python3 -I e2e_seek_hi.py <engine> <ffmpeg> <dsdana> <fixtures dir> <work dir>
import sys, os, importlib.util
ENGINE, FF, ANA, FX, W = sys.argv[1:6]
HERE = os.path.dirname(os.path.abspath(__file__))
sys.argv = [sys.argv[0], ENGINE, FF, ANA, FX, W, 'setup-only']
spec = importlib.util.spec_from_file_location('e2e', os.path.join(HERE, 'e2e_dsd_setup.py'))
e = importlib.util.module_from_spec(spec); spec.loader.exec_module(e)
TALLY = [0, 0]
def ok(cond, label):
    TALLY[0 if cond else 1] += 1
    print(('ok     ' if cond else 'NOT OK ') + label, flush=True)

for f in ('d256_1k.dsf', 'd512_1k.dsf'):
    path = os.path.join(FX, f)
    for rate in (352800, 88200):
        full = e.wav(e.ours(path, rate)[0].stdout)[1]
        ok(len(full) == 6 * rate * 6, '%s at %d: the full run is 6.000 s' % (f, rate))
        for t in (1.37, 2.5, 4.01):
            part = e.wav(e.ours(path, rate, skip=e.mmss(t))[0].stdout)
            k = round(t * rate) * 6
            ok(part[0] == len(part[1]) and part[1] == full[k:], '%s at %d --skip=%s: the full run from frame %d' % (f, rate, e.mmss(t), k // 6))
        head = e.wav(e.ours(path, rate, until=e.mmss(2.5))[0].stdout)
        tail = e.wav(e.ours(path, rate, skip=e.mmss(2.5))[0].stdout)
        ok(head[1] + tail[1] == full and head[0] == len(head[1]), '%s at %d: --until then --skip at 2.5 s join to the full run' % (f, rate))
        cue = e.wav(e.ours(path, rate, skip=e.mmss(1.0), until=e.mmss(3.0))[0].stdout)
        ok(cue[1] == full[rate * 6:3 * rate * 6] and cue[0] == len(cue[1]), '%s at %d: a cue track 1-3 s is exactly those frames' % (f, rate))
print('%d ok, %d NOT OK' % tuple(TALLY))
sys.exit(1 if TALLY[1] else 0)
