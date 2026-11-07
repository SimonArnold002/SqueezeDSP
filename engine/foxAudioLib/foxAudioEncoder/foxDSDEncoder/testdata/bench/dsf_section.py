#!/usr/bin/env python3
# A section of a DSF in an LMS library, over HTTP byte ranges (LMS /music/<id>/download serves 206), as a valid DSF of its own:
# the file's 92-byte header with the length fields set to the section, then whole block groups (4096 bytes per
# channel in turn). For the encoder spec bench (engine/encoder-spec.sh), 2026-11-03.
# Fetch one second more than the bench analyses, and run it with START=1 (the decoder's first second skipped).
#   python3 -I dsf_section.py <LMS base URL> <out dir> <name> <track id> <start s> <length s>
import sys, struct, urllib.request
base, out, name, tid, t0, dur = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4], float(sys.argv[5]), float(sys.argv[6])
URL = '%s/music/%s/download' % (base.rstrip('/'), tid)
def get(a, b):
    r = urllib.request.Request(URL, headers={'Range': 'bytes=%d-%d' % (a, b)})
    with urllib.request.urlopen(r, timeout=120) as f:
        assert f.status == 206, f.status
        return f.read()
h = bytearray(get(0, 91))
le = lambda o, n: int.from_bytes(h[o:o + n], 'little')
assert h[0:4] == b'DSD ' and h[28:32] == b'fmt ' and le(32, 8) == 52 and h[80:84] == b'data', bytes(h[:4])
ch, rate, bps, count, block = le(52, 4), le(56, 4), le(60, 4), le(64, 8), le(72, 4)
assert block == 4096 and bps == 1, (block, bps)
per = block * 8
g0, G = int(t0 * rate // per), -(-int(dur * rate) // per)
assert (g0 + G) * per <= count, 'past the end'
data = get(92 + g0 * block * ch, 92 + (g0 + G) * block * ch - 1)
assert len(data) == G * block * ch
h[12:20] = struct.pack('<Q', 92 + len(data))
h[20:28] = struct.pack('<Q', 0)
h[64:72] = struct.pack('<Q', G * per)
h[84:92] = struct.pack('<Q', 12 + len(data))
with open('%s/%s.dsf' % (out, name), 'wb') as f:
    f.write(bytes(h) + data)
print('%-28s DSD%-3d %d ch  %.2f-%.2f s of %.1f s  %d bytes' % (name, rate // 44100, ch, g0 * per / rate, (g0 + G) * per / rate, count / rate, len(data) + 92))
