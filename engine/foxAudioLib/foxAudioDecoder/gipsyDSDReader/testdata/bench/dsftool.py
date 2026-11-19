#!/usr/bin/env python3
# DSF fixture tool for the bench: read a DSF's bits per channel (raw block bytes, LSB-first as stored), and write
# DSF files from them: tile (repeat the stream n times) or split (at a byte per channel, into two files).
#   python3 -I dsftool.py tile  <in.dsf> <n> <out.dsf>
#   python3 -I dsftool.py split <in.dsf> <byte per channel> <a.dsf> <b.dsf>
import sys, struct

def read(path):
    b = open(path, 'rb').read()
    assert b[:4] == b'DSD '
    fmt = b[28:28 + 52]
    ch, rate, bits, count, block = struct.unpack('<I', fmt[24:28])[0], struct.unpack('<I', fmt[28:32])[0], \
        struct.unpack('<I', fmt[32:36])[0], struct.unpack('<Q', fmt[36:44])[0], struct.unpack('<I', fmt[44:48])[0]
    i = 80
    while b[i:i + 4] != b'data':
        i += struct.unpack('<Q', b[i + 4:i + 12])[0]
    size = struct.unpack('<Q', b[i + 4:i + 12])[0] - 12
    data = b[i + 12:i + 12 + size]
    n = (count + 7) // 8
    chans = [bytearray() for _ in range(ch)]
    for blk in range(len(data) // (block * ch)):
        for c in range(ch):
            chans[c] += data[(blk * ch + c) * block:(blk * ch + c + 1) * block]
    return rate, [bytes(c[:n]) for c in chans]

def write(path, rate, chans):
    ch, n = len(chans), len(chans[0])
    blocks = (n + 4095) // 4096
    data = bytearray()
    for blk in range(blocks):
        for c in range(ch):
            part = chans[c][blk * 4096:(blk + 1) * 4096]
            data += part + bytes(4096 - len(part))
    hdr = b'DSD ' + struct.pack('<QQQ', 28, 28 + 52 + 12 + len(data), 0)
    hdr += b'fmt ' + struct.pack('<QIIIIIIQII', 52, 1, 0, 2, ch, rate, 1, n * 8, 4096, 0)
    hdr += b'data' + struct.pack('<Q', 12 + len(data))
    open(path, 'wb').write(hdr + data)

cmd = sys.argv[1]
rate, chans = read(sys.argv[2])
if cmd == 'tile':
    write(sys.argv[4], rate, [c * int(sys.argv[3]) for c in chans])
elif cmd == 'split':
    k = int(sys.argv[3])
    write(sys.argv[4], rate, [c[:k] for c in chans])
    write(sys.argv[5], rate, [c[k:] for c in chans])
