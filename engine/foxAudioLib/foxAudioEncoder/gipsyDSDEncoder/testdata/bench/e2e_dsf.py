#!/usr/bin/env python3
# End to end: the engine writing native DSD (--formatout=DSF --dsdrate=N [--dsdmod=standard|clans]) from a DSD
# file (our decoder) and from PCM (WAV). The DSF read back by four readers: the engine's own decoder, ffmpeg (bench
# build with the DSD decoders), LMS's sox and DSDPlayer's dsdplay. Noise against LMS's sox modulator on the same
# PCM. Engine config as the decoder bench's: no FIR, no PEQ, ReplayGain off.
# Run: python3 -I e2e_dsf.py <engine> <ffmpeg> <dsdana> <dsdplay> <fixtures dir> <work dir> [nospeed]
import sys, os, json, struct, subprocess, time, shutil, re

ENGINE, FF, ANA, DSDPLAY, FX, W = sys.argv[1:7]
NOSPEED = 'nospeed' in sys.argv[7:]
SOX = '/Users/simona/Documents/GitHub/SqueezeDSP/t/lmsbin/Bin/darwin/sox'
TALLY = [0, 0]
def ok(cond, label):
    TALLY[0 if cond else 1] += 1
    print(('ok     ' if cond else 'NOT OK ') + label, flush=True)

root = os.path.join(W, 'e2e-root')
shutil.rmtree(root, ignore_errors=True)
for d in ('bin', 'settings', 'temp'):
    os.makedirs(os.path.join(root, d))
BIN = os.path.join(root, 'bin', 'SqueezeDSP')
shutil.copy(ENGINE, BIN)
os.chmod(BIN, 0o755)
LOG = os.path.join(root, 'engine.log')
open(LOG, 'w').close()
json.dump({'settings': {'partitions': '0', 'convolvergain': '-4', 'soxExe': SOX, 'tail': 'true', 'dither': '0',
                        'debug': 'true', 'logFile': LOG, 'gain': '-10', 'pluginDataFolder': root,
                        'settingsDataFolder': os.path.join(root, 'settings'), 'impulseDataFolder': root,
                        'tempDataFolder': os.path.join(root, 'temp'), 'lmsHost': '127.0.0.1', 'lmsPort': '9'}},
          open(os.path.join(root, 'bin', 'SqueezeDSP_config.json'), 'w'))
SETTINGS = os.path.join(root, 'settings', 'bench.settings.json')
def client(**extra):
    c = {'Bypass': '0', 'Loudness': {'listening_level': 85, 'enabled': 0}, 'Balance': 0, 'Width': 0, 'FIRWavFile': '-',
         'Preset': 'None', 'FIRStrength': 100, 'Crossfeed': 'off', 'Delay': {'delay': 0}, 'Filters': [], 'Preamp': 0,
         'ReplayGain': {'enabled': '0', 'mode': 2, 'fixed_gain': 0}}
    c.update(extra)
    json.dump({'Client': c}, open(SETTINGS, 'w'))
client()
BASE = [BIN, '--trackURL=file:///bench/t.dsf', '--Clientid=bench', '--bitsout=24']

def clear():
    for f in os.listdir(os.path.join(root, 'temp')):
        os.remove(os.path.join(root, 'temp', f))

def run(args, stdin=None, timeout=900):
    clear()
    open(LOG, 'w').close()
    t0 = time.time()
    p = subprocess.run(BASE + args, input=stdin, capture_output=True, timeout=timeout)
    return p, time.time() - t0

def dsf(b):
    """header fields, data, per-channel bits"""
    le = lambda o, n: int.from_bytes(b[o:o + n], 'little')
    h = dict(magic=b[0:4], dsdsize=le(4, 8), total=le(12, 8), meta=le(20, 8), fmt=b[28:32], fmtsize=le(32, 8),
             ver=le(40, 4), fid=le(44, 4), ctype=le(48, 4), ch=le(52, 4), rate=le(56, 4), bps=le(60, 4), count=le(64, 8),
             block=le(72, 4), data=b[80:84], datasize=le(84, 8))
    return h

def write(path, b):
    open(path, 'wb').write(b)
    return path

def decode_ours(path, rate=352800, cutoff=30000):
    p, _ = run(['--formatout=WAV', '--formatin=DSD', '--input', path, '--samplerate=%d' % rate, '--dsdcutoff=%d' % cutoff, '--dsdgain=0'])
    assert p.returncode == 0, p.stderr[-400:]
    return wavdata(p.stdout)

def wavdata(b):
    assert b[:4] == b'RIFF', b[:40]
    i = 12
    while b[i:i + 4] != b'data':
        n = struct.unpack('<I', b[i + 4:i + 8])[0]
        i += 8 + n + (n & 1)
    return b[i + 8:]

def ana(data, rate, tone, label, s24=True, logn=20):
    a = [ANA, '-rate', str(rate), '-tone', str(tone), '-label', label, '-log2n', str(logn)]
    if s24:
        a.insert(1, '-s24')
    out = subprocess.run(a, input=data, capture_output=True).stdout.decode()
    print('       ' + out.strip(), flush=True)
    m = re.search(r'tone\s+(\S+)\s+in-band noise\s+(\S+)\s+20-50k\s+(\S+)\s+50k-Nyq\s+(\S+)\s+peak\s+(\S+)', out)
    return dict(zip(('tone', 'inband', 'k20_50', 'k50', 'peak'), map(float, m.groups()))) if m else None

def logline():
    m = re.search(r'DSF out: peak ([0-9.]+) of full swing, (\d+) samples limited, (\d+) modulator resets, (\d+) samples on the safe design', open(LOG).read())
    return (float(m.group(1)), int(m.group(2)), int(m.group(3)), int(m.group(4))) if m else None

def wav24(frames, rate, f, amp, chans=2):
    """a 24-bit WAV of amp*sin at f, right channel at -amp/2"""
    data = bytearray()
    import math
    for i in range(frames):
        v = amp * math.sin(2 * math.pi * f * i / rate)
        for c in range(chans):
            s = int(round((v if c == 0 else -v / 2) * 8388607))
            data += struct.pack('<i', s)[:3]
    hdr = b'RIFF' + struct.pack('<I', 36 + len(data)) + b'WAVE' + b'fmt ' + struct.pack('<IHHIIHH', 16, 1, chans, rate, rate * chans * 3, chans * 3, 24) + b'data' + struct.pack('<I', len(data))
    return hdr + bytes(data)

# ---------------------------------------------------------------- 1: DSD64 in, DSF out at each rate
src = os.path.join(FX, 'd64_1k.dsf')   # sox clans-8, 1 kHz at 25% modulation, 6 s, stereo
srcb = open(src, 'rb').read()
sh = dsf(srcb)
ref = ana(decode_ours(src), 352800, 1000, 'source decoded (ours)')
print('== DSD64 source -> DSF at DSD64..512, both modulators')
for kind in ('standard', 'clans', 'adaptive'):
    for dsd in (2822400, 5644800, 11289600, 22579200):
        p, el = run(['--formatin=DSD', '--input', src, '--samplerate=352800', '--formatout=DSF', '--dsdrate=%d' % dsd, '--dsdmod=' + kind])
        ok(p.returncode == 0, '%s DSD%d: rc %d %s' % (kind, dsd // 44100, p.returncode, p.stderr[-200:].decode(errors='replace') if p.returncode else ''))
        if p.returncode:
            continue
        b = p.stdout
        h = dsf(b)
        L = dsd // 352800
        frames = sh['count'] // 8   # the decoder's frames at 352.8k from DSD64: one per byte
        blocks = ((h['count'] + 7) // 8 + 4095) // 4096
        ok(h['magic'] == b'DSD ' and h['fmt'] == b'fmt ' and h['data'] == b'data' and h['rate'] == dsd and h['ch'] == 2 and h['bps'] == 1
           and h['block'] == 4096 and h['count'] == frames * L and h['total'] == len(b) and h['datasize'] == len(b) - 80
           and len(b) == 92 + blocks * 4096 * 2,
           '%s DSD%d: header (count %d = %d frames x %d, total %d = %d bytes written)' % (kind, dsd // 44100, h['count'], frames, L, h['total'], len(b)))
        pk = logline()
        path = write(os.path.join(W, 'out_%s_%d.dsf' % (kind, dsd)), b)
        r = ana(decode_ours(path), 352800, 1000, '%s DSD%d -> ours' % (kind, dsd // 44100))
        ok(r and abs(r['tone'] - ref['tone']) < 0.01, '%s DSD%d: level %.3f, source %.3f (unity end to end)' % (kind, dsd // 44100, r['tone'], ref['tone']))
        ok(pk and pk[2] == 0 and pk[1] == 0, '%s DSD%d: no sample limited, no reset (log %s)' % (kind, dsd // 44100, pk))
        # the other readers
        f = subprocess.run([FF, '-loglevel', 'quiet', '-i', path, '-af', 'aresample=352800:resampler=soxr:precision=28', '-c:a', 'pcm_s24le', '-f', 's24le', '-'], capture_output=True)
        rf = ana(f.stdout, 352800, 1000, '%s DSD%d -> ffmpeg' % (kind, dsd // 44100))
        ok(rf and abs(rf['tone'] - ref['tone']) < 0.02 and abs(len(f.stdout) // 6 - frames) <= frames // 500, '%s DSD%d: ffmpeg reads it: level %.3f, %d frames' % (kind, dsd // 44100, rf['tone'] if rf else 0, len(f.stdout) // 6))
        if dsd <= 5644800:
            s = subprocess.run([SOX, path, '-t', 's24', '-r', '352800', '-', 'rate', '-v', '352800'], capture_output=True)
            rs = ana(s.stdout, 352800, 1000, '%s DSD%d -> sox' % (kind, dsd // 44100))
            ok(rs and abs(rs['tone'] - ref['tone']) < 0.02, '%s DSD%d: LMS sox reads it: level %.3f' % (kind, dsd // 44100, rs['tone'] if rs else 0))
        if dsd > 11289600:
            continue
        d = subprocess.run([DSDPLAY, '-R', 'vL', '-r', '352800', path], capture_output=True)
        fl = subprocess.run(['/Users/simona/Documents/GitHub/SqueezeDSP/t/lmsbin/Bin/darwin/flac', '-dcs', '--force-raw-format', '--endian=little', '--sign=signed', '-'], input=d.stdout, capture_output=True)
        rd = ana(fl.stdout, 352800, 1000, '%s DSD%d -> dsdplay' % (kind, dsd // 44100))
        ok(rd and abs(rd['tone'] - ref['tone']) < 0.02, '%s DSD%d: dsdplay reads it: level %.3f' % (kind, dsd // 44100, rd['tone'] if rd else 0))

# ---------------------------------------------------------------- 2: higher-rate sources
print('== DSD256 / DSD512 sources -> DSD256 out')
for name in ('d256_1k.dsf', 'd512_1k.dsf'):
    s2 = os.path.join(FX, name)
    r0 = ana(decode_ours(s2), 352800, 1000, name + ' decoded')
    p, el = run(['--formatin=DSD', '--input', s2, '--samplerate=352800', '--formatout=DSF', '--dsdrate=11289600'])
    ok(p.returncode == 0, name + ' -> DSD256: rc %d' % p.returncode)
    r = ana(decode_ours(write(os.path.join(W, 'o_' + name), p.stdout)), 352800, 1000, name + ' -> DSD256 -> ours')
    ok(r and abs(r['tone'] - r0['tone']) < 0.01, name + ': level %.3f, source %.3f' % (r['tone'], r0['tone']))

# ---------------------------------------------------------------- 3: PCM in
print('== PCM (WAV) in')
w = wav24(352800 * 4, 352800, 1000, 0.5)
wpath = write(os.path.join(W, 'tone05.wav'), w)
p, _ = run(['--formatin=WAV', '--input', wpath, '--formatout=DSF', '--dsdrate=2822400'])
ok(p.returncode == 0 and dsf(p.stdout)['count'] == 352800 * 4 * 8, 'WAV file: its header gives the length (count %d)' % (dsf(p.stdout)['count'] if p.returncode == 0 else -1))
r = ana(decode_ours(write(os.path.join(W, 'w.dsf'), p.stdout)), 352800, 1000, 'WAV 0.5 -> DSD64 -> ours')
ok(r and abs(r['tone'] - (-6.0206)) < 0.01, 'WAV at 0.5: back at %.3f dBFS (-6.021: unity)' % r['tone'])
# --dsdoutgain before the modulator: the plugin gives the converter to DSD's PCM sources -6.02 (TemplateConfig
# template_DSFUP), PCM full scale to SACD's 0 dB (50%), the level a DAC plays as loud as PCM full scale (Simon
# 2026-11-03: at unity a PCM track played about 6 dB louder as DSD than as PCM)
p, _ = run(['--formatin=WAV', '--input', wpath, '--formatout=DSF', '--dsdrate=2822400', '--dsdoutgain=-6.02'])
pk = logline()
r = ana(decode_ours(write(os.path.join(W, 'wg.dsf'), p.stdout)), 352800, 1000, 'WAV 0.5 at --dsdoutgain=-6.02 -> DSD64 -> ours')
ok(r and abs(r['tone'] - (-6.0206 - 6.02)) < 0.01 and pk and pk[1] == 0 and pk[2] == 0,
   '--dsdoutgain=-6.02: back at %.3f dBFS (-12.041: 6.02 dB under unity), nothing limited, no reset (log %s)' % (r['tone'] if r else 0, pk))
wf = write(os.path.join(W, 'tone1.wav'), wav24(352800 * 2, 352800, 1000, 0.999))
p, _ = run(['--formatin=WAV', '--input', wf, '--formatout=DSF', '--dsdrate=2822400', '--dsdoutgain=-6.02'])
pk = logline()
ok(p.returncode == 0 and pk and abs(pk[0] - 0.5) < 0.01 and pk[1] == 0 and pk[2] == 0,
   '... a full-scale PCM tone at -6.02 dB: peak %.3f of full swing (SACD 0 dB), nothing limited (log %s)' % (pk[0] if pk else 0, pk))
p, _ = run(['--formatin=WAV', '--formatout=DSF', '--dsdrate=2822400'], stdin=w)
ok(p.returncode != 0 and not p.stdout.startswith(b'DSD '), 'WAV through a pipe with no --wavlength: refused (rc %d, no DSF out)' % p.returncode)
p, _ = run(['--formatin=WAV', '--formatout=DSF', '--dsdrate=2822400', '--wavlength=2.5'], stdin=w)
ok(p.returncode == 0 and dsf(p.stdout)['count'] == int(2.5 * 352800) * 8 and len(p.stdout) == dsf(p.stdout)['total'],
   'pipe with --wavlength=2.5: states and writes 2.5 s (count %d)' % (dsf(p.stdout)['count'] if p.returncode == 0 else -1))
# --wavlength is an upper bound (the plugin states LMS's length plus a margin; a stream's is whole seconds): the stream
# ends with the input, the last block's rest zero (squeezelite strips it from a stream shorter than its count)
p5, _ = run(['--formatin=WAV', '--formatout=DSF', '--dsdrate=2822400', '--wavlength=5.0'], stdin=w)
p4, _ = run(['--formatin=WAV', '--formatout=DSF', '--dsdrate=2822400', '--wavlength=4.0'], stdin=w)
h5 = dsf(p5.stdout) if p5.returncode == 0 else {}
used = 4 * 352800 * 8 // 8                          # bytes a channel of the 4 s input
last = used - (used - 1) // 4096 * 4096             # of them in the last block
tail = p5.stdout[-4096:]                            # the last block's second channel
ok(p5.returncode == 0 and h5['count'] == int(5.0 * 352800) * 8 and len(p5.stdout) == 92 + (used + 4095) // 4096 * 4096 * 2
   and len(p5.stdout) < h5['total'], 'pipe, 4 s under --wavlength=5.0: states 5 s, writes the 4 s (%d of %d bytes)'
   % (len(p5.stdout), h5.get('total', -1)))
ok(p4.returncode == 0 and p5.stdout[92:] == p4.stdout[92:], '... its audio bit for bit the run stating exactly 4 s')
ok(tail[last:] == bytes(4096 - last) and any(tail[:last]), '... the last block zero after the input (%d bytes)' % (4096 - last))
for amp in (0.7, 0.72):
    wa = write(os.path.join(W, 'tone%g.wav' % amp), wav24(352800 * 4, 352800, 1000, amp))
    for kind in ('standard', 'clans', 'adaptive'):
        p, _ = run(['--formatin=WAV', '--input', wa, '--formatout=DSF', '--dsdrate=2822400', '--dsdmod=' + kind])
        pk = logline()
        r = ana(decode_ours(write(os.path.join(W, 'wa.dsf'), p.stdout)), 352800, 1000, '%s WAV %.2f -> DSD64' % (kind, amp))
        ok(pk and pk[2] == 0 and r and abs(r['tone'] - 20 * __import__('math').log10(amp)) < 0.01 and r['inband'] < -110,
           '%s at %.2f: no reset (log %s), level %.3f, noise %.1f' % (kind, amp, pk, r['tone'], r['inband']))
wo = write(os.path.join(W, 'tone09.wav'), wav24(352800, 352800, 1000, 0.9))
p, _ = run(['--formatin=WAV', '--input', wo, '--formatout=DSF', '--dsdrate=2822400'])
pk = logline()
ok(p.returncode == 0 and pk and pk[1] > 0 and pk[2] == 0 and abs(pk[0] - 0.9) < 0.01, 'over the limit (0.9): held, counted, no reset (log %s)' % (pk,))

# ---------------------------------------------------------------- 3b: the converter: PCM at its own rate, upsampled here
# The converter to DSD (0.1.72.33): a PCM source reaches the engine at its own rate and goes up to 705.6k by the
# engine's own filters (gipsyUpsampler: --upsample, --upfilter), the DSP there, then the PCM source's designs
# (--dsdsource=pcm, held at 0.63) after -6.02 dB (template_DSFUP).
print('== the converter: PCM at 44.1k / 48k / 96k, upsampled by the engine to 705.6k, then DSD')
def upline():
    m = re.search(r'Upsample: (\d+) to (\d+) Hz, (\S+) \((\S+) source\), stage A (\d+) taps, stage B (\d+)', open(LOG).read())
    return m.groups() if m else None
UP = ['--formatout=DSF', '--upsample=705600', '--dsdsource=pcm', '--dsdoutgain=-6.02']
for rate, want, kind in ((44100, 'short-int', 'CD-rate'), (48000, 'short-int', 'CD-rate'), (96000, 'short-lin', 'hi-res')):
    wr = write(os.path.join(W, 'up%d.wav' % rate), wav24(rate * 4, rate, 1000, 0.5))
    for dsd in (2822400, 11289600):
        p, _ = run(['--formatin=WAV', '--input', wr, '--dsdrate=%d' % dsd] + UP)
        ok(p.returncode == 0, '%d -> DSD%d: rc %d %s' % (rate, dsd // 44100, p.returncode, p.stderr[-200:].decode(errors='replace') if p.returncode else ''))
        if p.returncode:
            continue
        h, u, pk = dsf(p.stdout), upline(), logline()
        log = open(LOG).read()
        ok(h['count'] == 4 * dsd and h['total'] == len(p.stdout), '%d -> DSD%d: states and writes 4 s (count %d)' % (rate, dsd // 44100, h['count']))
        ok(u and u[:4] == (str(rate), '705600', want, kind), '%d -> DSD%d: the engine upsampled it, %s for a %s source (log %s)' % (rate, dsd // 44100, want, kind, u))
        ok(re.search(r'standard for a pcm source \(limit 0\.63\)', log) is not None, "%d -> DSD%d: the PCM source's designs, held at 0.63" % (rate, dsd // 44100))
        r = ana(decode_ours(write(os.path.join(W, 'up.dsf'), p.stdout)), 352800, 1000, '%d 0.5 -> 705.6k -> DSD%d -> ours' % (rate, dsd // 44100))
        ok(r and abs(r['tone'] - (-6.0206 - 6.02)) < 0.01 and r['inband'] < -120 and pk and pk[1] == 0 and pk[2] == 0,
           '%d -> DSD%d: back at %.3f dBFS (-12.041), in-band noise %.1f (under -120), nothing limited, no reset (log %s)'
           % (rate, dsd // 44100, r['tone'] if r else 0, r['inband'] if r else 0, pk))
w44 = os.path.join(W, 'up44100.wav')
p, _ = run(['--formatin=WAV', '--input', w44, '--dsdrate=2822400', '--upfilter=long-min,long-int'] + UP)
u = upline()
ok(p.returncode == 0 and u and u[2] == 'long-min', '--upfilter=long-min,long-int: a 44.1k source takes the first (log %s)' % (u,))
wf44 = write(os.path.join(W, 'up44fs.wav'), wav24(44100 * 2, 44100, 1000, 0.999))
p, _ = run(['--formatin=WAV', '--input', wf44, '--dsdrate=2822400'] + UP)
pk = logline()
ok(p.returncode == 0 and pk and abs(pk[0] - 0.5) < 0.01 and pk[1] == 0 and pk[2] == 0,
   'a full-scale 44.1k tone: peak %.3f of full swing after the upsampler (SACD 0 dB), nothing limited (log %s)' % (pk[0] if pk else 0, pk))
# inter-sample overs: a quarter-rate sine sampled 45 degrees off its peaks: every sample at 0.999, the waveform at
# 1.41 (+3 dB); at -6.02 dB it reaches 0.706, over 0.63: held there, counted, no reset
def overs_wav(path, rate=44100, secs=2):
    data = bytearray()
    for i in range(rate * secs):
        v = 0.999 * (1 if (i // 2) % 2 == 0 else -1)
        for c in range(2):
            data += struct.pack('<i', int(round(v * 8388607)))[:3]
    hdr = b'RIFF' + struct.pack('<I', 36 + len(data)) + b'WAVE' + b'fmt ' + struct.pack('<IHHIIHH', 16, 1, 2, rate, rate * 6, 6, 24) + b'data' + struct.pack('<I', len(data))
    open(path, 'wb').write(hdr + bytes(data))
    return path
p, _ = run(['--formatin=WAV', '--input', overs_wav(os.path.join(W, 'overs.wav')), '--dsdrate=2822400'] + UP)
pk = logline()
ok(p.returncode == 0 and pk and pk[0] > 0.65 and pk[1] > 0 and pk[2] == 0,
   'inter-sample overs (+3 dB): peak %.3f, held at 0.63 (%s samples), no reset (log %s)' % (pk[0] if pk else 0, pk[1] if pk else '?', pk))
# a seek, as the plugin runs it: the engine's own --skip, and WavLength's statement of what is left plus a second
p, _ = run(['--formatin=WAV', '--input', w44, '--dsdrate=2822400', '--skip=0:01.50', '--wavlength=3.5'] + UP)
h = dsf(p.stdout) if p.returncode == 0 else {}
got = (len(p.stdout) - 92) * 8 // 2 if p.returncode == 0 else -1     # DSD samples a channel written
ok(p.returncode == 0 and h['count'] == int(3.5 * 705600) * 4 and abs(got - 2.5 * 2822400) <= 4096 * 8,
   '--skip=0:01.50 --wavlength=3.5 on the converter: states 3.5 s, writes the 2.5 s left (%.3f s)' % (got / 2822400))
p, _ = run(['--formatin=WAV', '--dsdrate=2822400', '--wavlength=4.0'] + UP, stdin=open(w44, 'rb').read())
h = dsf(p.stdout) if p.returncode == 0 else {}
ok(p.returncode == 0 and h['count'] == 4 * 2822400 and len(p.stdout) <= h['total'],
   'a pipe with --wavlength=4.0 on the converter: states 4 s at the DSD rate (count %d)' % h.get('count', -1))
for args, why in ((['--formatin=DSD', '--input', src, '--samplerate=352800', '--formatout=DSF', '--dsdrate=2822400', '--upsample=705600'], '--upsample on a DSD input'),
                  (['--formatin=WAV', '--input', w44, '--formatout=DSF', '--dsdrate=2822400', '--upfilter=short-lin'], '--upfilter without --upsample'),
                  (['--formatin=WAV', '--input', w44, '--formatout=WAV', '--dsdsource=pcm'], '--dsdsource without DSF'),
                  (['--formatin=WAV', '--input', w44, '--dsdrate=2822400', '--upfilter=medium-lin'] + UP, 'an --upfilter that is not a filter'),
                  (['--formatin=WAV', '--input', w44, '--formatout=DSF', '--dsdrate=2822400', '--upsample=705600', '--dsdsource=sacd'], 'a --dsdsource that is not dsd or pcm')):
    p, _ = run(args)
    ok(p.returncode != 0 and not p.stdout.startswith(b'DSD '), 'refused: %s (rc %d, no DSF out)' % (why, p.returncode))

# ---------------------------------------------------------------- 4: against LMS's sox modulator, same PCM
print('== noise: ours against LMS sox, the same 352.8k PCM (1 kHz at 0.5 = DSD 0 dB), DSD64, read back by ours')
w5 = write(os.path.join(W, 'tone05b.wav'), wav24(352800 * 4, 352800, 1000, 0.5))
for kind in ('standard', 'clans', 'adaptive'):
    p, _ = run(['--formatin=WAV', '--input', w5, '--formatout=DSF', '--dsdrate=2822400', '--dsdmod=' + kind])
    ana(decode_ours(write(os.path.join(W, 'n_%s.dsf' % kind), p.stdout)), 352800, 1000, 'ours ' + kind)
# the noise above 20 kHz, read with no low-pass (ffmpeg): the source, and each modulator from it at DSD64
print('== DSD64 source -> DSD64, read by ffmpeg (no low-pass): 20-50 kHz is the modulator\'s')
def ffread(path, label):
    f = subprocess.run([FF, '-loglevel', 'quiet', '-i', path, '-af', 'aresample=352800:resampler=soxr:precision=28', '-c:a', 'pcm_s24le', '-f', 's24le', '-'], capture_output=True)
    return ana(f.stdout, 352800, 1000, label)
ffread(src, 'source (sox clans-8, 25%)')
for kind in ('standard', 'clans', 'adaptive'):
    p, _ = run(['--formatin=DSD', '--input', src, '--samplerate=352800', '--formatout=DSF', '--dsdrate=2822400', '--dsdmod=' + kind])
    r = ffread(write(os.path.join(W, 'u_%s.dsf' % kind), p.stdout), 'ours ' + kind)
    if kind == 'adaptive':
        ok(r and r['k20_50'] < -55, 'adaptive DSD64: 20-50 kHz %.1f dBFS at the source level (under -55)' % (r['k20_50'] if r else 0))
# loud passages: bursts at 0.72 through adaptive, unity, no reset, the safe design engaged
import math as _m
def burst_wav(path, secs, rate=352800):
    data = bytearray()
    for i in range(int(secs * rate)):
        t = i / rate
        amp = 0.72 if (t % 0.3) >= 0.2 else 0.3
        v = amp * _m.sin(2 * _m.pi * 1000 * t)
        for c in range(2):
            data += struct.pack('<i', int(round((v if c == 0 else -v / 2) * 8388607)))[:3]
    hdr = b'RIFF' + struct.pack('<I', 36 + len(data)) + b'WAVE' + b'fmt ' + struct.pack('<IHHIIHH', 16, 1, 2, rate, rate * 6, 6, 24) + b'data' + struct.pack('<I', len(data))
    open(path, 'wb').write(hdr + bytes(data))
    return path
bw = burst_wav(os.path.join(W, 'bursts.wav'), 3)
p, _ = run(['--formatin=WAV', '--input', bw, '--formatout=DSF', '--dsdrate=2822400', '--dsdmod=adaptive'])
pk = logline()
ok(p.returncode == 0 and pk and pk[2] == 0 and pk[3] > 0 and pk[1] == 0, 'adaptive, bursts 0.3/0.72: no reset, nothing limited, the safe design for %s samples (log %s)' % (pk[3] if pk else '?', pk))
for f in ('clans-7', 'sdm-7', 'sdm-8'):
    sp = os.path.join(W, 's_%s.dsf' % f)
    subprocess.run([SOX, w5, '-r', '2822400', sp, 'gain', '6', 'rate', '-v', '2822400', 'sdm', '-f', f], capture_output=True)
    if os.path.exists(sp):
        ana(decode_ours(sp), 352800, 1000, 'LMS sox ' + f)

# ---------------------------------------------------------------- 5: seek and cue end on a DSD input
print('== --skip / --until')
p, _ = run(['--formatin=DSD', '--input', src, '--samplerate=352800', '--formatout=DSF', '--dsdrate=2822400', '--skip=0:02.00'])
h = dsf(p.stdout)
ok(p.returncode == 0 and h['count'] == (sh['count'] // 8 - 2 * 352800) * 8 and h['total'] == len(p.stdout), '--skip=0:02.00: count %d (4 s left)' % h['count'])
p, _ = run(['--formatin=DSD', '--input', src, '--samplerate=352800', '--formatout=DSF', '--dsdrate=2822400', '--until=0:03.50'])
h = dsf(p.stdout)
ok(p.returncode == 0 and h['count'] == int(3.5 * 352800) * 8 and h['total'] == len(p.stdout), '--until=0:03.50: count %d' % h['count'])
r = ana(decode_ours(write(os.path.join(W, 'u.dsf'), p.stdout)), 352800, 1000, 'until 3.5 s', logn=19)
ok(r and abs(r['tone'] - ref['tone']) < 0.01, 'cut track level %.3f' % r['tone'])

# ---------------------------------------------------------------- 5b: the player's DSDGain is for DSD to PCM, not DSF
print("== a player's DSDGain (+6 dB) on DSF out")
client(DSDGain=6)
p, _ = run(['--formatin=DSD', '--input', src, '--samplerate=352800', '--formatout=DSF', '--dsdrate=2822400'])
client()
r = ana(decode_ours(write(os.path.join(W, 'g6.dsf'), p.stdout)), 352800, 1000, 'DSDGain 6 in the settings, DSF out')
ok(r and abs(r['tone'] - ref['tone']) < 0.01, "the player's DSDGain is not applied to DSF out: %.3f, source %.3f" % (r['tone'], ref['tone']))
client(DSDGain=6)
p, _ = run(['--formatout=WAV', '--formatin=DSD', '--input', src, '--samplerate=352800'])
client()
r = ana(wavdata(p.stdout), 352800, 1000, 'DSDGain 6 in the settings, WAV out (control)')
ok(r and abs(r['tone'] - (ref['tone'] + 6)) < 0.02, 'control: the same setting does apply to PCM out: %.3f' % (r['tone'] if r else 0))

# ---------------------------------------------------------------- 5c: the rate from the player's settings, or the source's
print("== the DSD rate: the source's, the player's DSDOutRate / DSDOutMax / DSDOutMod, flags winning")
s256 = os.path.join(FX, 'd256_1k.dsf')
def rate_of(args, **settings):
    client(**settings)
    p, _ = run(args)
    client()
    return dsf(p.stdout)['rate'] if p.returncode == 0 and p.stdout[:4] == b'DSD ' else 'rc %d' % p.returncode
D176 = ['--formatin=DSD', '--input', src, '--samplerate=176400', '--formatout=DSF']
ok(rate_of(D176) == 2822400, "no flag, no setting: the source's own rate (DSD64)")
ok(rate_of(['--formatin=DSD', '--input', s256, '--samplerate=176400', '--formatout=DSF']) == 11289600, "... a DSD256 source: DSD256")
ok(rate_of(['--formatin=DSD', '--input', s256, '--samplerate=176400', '--formatout=DSF'], DSDOutMax=5644800) == 5644800, 'DSDOutMax 5644800 caps a DSD256 source at DSD128')
ok(rate_of(D176, DSDOutRate=5644800) == 5644800, 'DSDOutRate 5644800 (a number): DSD128 from a DSD64 source')
ok(rate_of(D176, DSDOutRate='11289600', DSDOutMax='5644800') == 5644800, '... DSDOutRate DSD256 with DSDOutMax DSD128 (strings): DSD128')
ok(rate_of(D176 + ['--dsdrate=2822400'], DSDOutRate=5644800) == 2822400, 'a --dsdrate flag wins over the setting')
ok(rate_of(D176 + ['--dsdrate=source', '--dsdmax=2822400'], DSDOutRate=11289600) == 2822400, '--dsdrate=source --dsdmax wins too')
client(DSDOutMod='adaptive')
p, _ = run(D176)
client()
ok(p.returncode == 0 and re.search(r'gipsyDSDEncoder: 176400 Hz x2 -> DSD 2822400 \(x16, 17 taps per phase\), adaptive', open(LOG).read()) is not None, "DSDOutMod adaptive from the settings: the engine's modulator is adaptive")
w176 = write(os.path.join(W, 'w176.wav'), wav24(176400, 176400, 1000, 0.3))
p, _ = run(['--formatin=WAV', '--input', w176, '--formatout=DSF'])
ok(p.returncode != 0 and not p.stdout.startswith(b'DSD '), "a WAV input at the source's rate (nothing set): refused, it has no DSD rate (rc %d)" % p.returncode)
p, _ = run(['--formatin=WAV', '--input', w176, '--formatout=DSF', '--dsdrate=2822400'])
ok(p.returncode == 0 and dsf(p.stdout)['rate'] == 2822400, 'a WAV input with --dsdrate: written')

# ---------------------------------------------------------------- 5b: the engine's own speed (0.1.72.35)
print("== the engine's own speed: its fill (read flat out here, so the whole track) and busiest stage, left in the temp folder")
def speed_report():
    try:
        return json.load(open(os.path.join(root, 'temp', 'speed_bench.json')))
    except Exception:
        return None
ALL = {'decoder', 'upsampler', 'convolver 1', 'convolver 2', 'merger', 'encoder'}
for args, secs, label, stages in (
        (['--formatin=DSD', '--input', src, '--samplerate=352800', '--formatout=DSF', '--dsdrate=2822400'], 6, 'DSD64 -> DSD64', ALL - {'upsampler'}),
        (['--formatin=WAV', '--input', w44, '--dsdrate=2822400'] + UP, 4, 'WAV 44.1k -> 705.6k -> DSD64', ALL),
        (['--formatin=WAV', '--input', w44, '--formatout=WAV'], 4, 'WAV 44.1k -> WAV', ALL - {'upsampler'})):
    p, _ = run(args)
    j = speed_report() or {}
    ok(p.returncode == 0 and isinstance(j.get('pid'), int) and j.get('stage') in stages and abs(j.get('audio', 0) - secs) < 0.2
       and j.get('busy', 0) > 0 and j['audio'] / j['busy'] > 1 and j.get('over') is True,
       "%s: the report names a stage of the route (%s), its fill the track's %g s of audio (%.2f) in %.4f s, over" % (
           label, j.get('stage'), secs, j.get('audio', 0), j.get('busy', 0)))

# ---------------------------------------------------------------- 6: refusals
print('== refusals')
D = ['--formatin=DSD', '--input', src]
for args, why in ((D + ['--samplerate=352800', '--formatout=DSF', '--dsdrate=fast'], 'a --dsdrate that is not a rate or source'),
                  (D + ['--samplerate=352800', '--formatout=WAV', '--dsdrate=2822400'], '--dsdrate without DSF'),
                  (D + ['--samplerate=352800', '--formatout=DSF', '--dsdrate=3072000'], 'a 48k-family DSD rate'),
                  (D + ['--samplerate=352800', '--formatout=DSF', '--dsdrate=2822400', '--dsdmod=sdm'], 'unknown modulator'),
                  (D + ['--samplerate=96000', '--formatout=DSF', '--dsdrate=2822400'], 'a PCM rate that does not go in')):
    p, _ = run(args)
    ok(p.returncode != 0 and not p.stdout.startswith(b'DSD '), 'refused: %s (rc %d, no DSF out; the engine prints its usage)' % (why, p.returncode))

# ---------------------------------------------------------------- 7: speed
if not NOSPEED:
    print('== speed over 60 s, per CPU-second (user+sys of the engine), wall')
    long64 = os.path.join(W, 'd64_60.dsf')
    tool = os.path.join(os.path.dirname(os.path.abspath(__file__)), '../../../../foxAudioDecoder/gipsyDSDReader/testdata/bench/dsftool.py')
    subprocess.run([sys.executable, '-I', tool, 'tile', src, '10', long64], check=True)
    for name, srcf in (('DSD64', long64),):
        for dsd in (2822400, 5644800, 11289600, 22579200):
            for rate in (176400, 352800):
                if dsd // rate > 64:
                    continue
                clear()
                t0 = os.times()
                p = subprocess.run(BASE + ['--formatin=DSD', '--input', srcf, '--samplerate=%d' % rate, '--formatout=DSF', '--dsdrate=%d' % dsd], capture_output=True)
                t1 = os.times()
                cpu = (t1.children_user - t0.children_user) + (t1.children_system - t0.children_system)
                wall = t1.elapsed - t0.elapsed
                print('       %s -> DSD%d via %d: %.1fx per CPU-second, %.1fx wall (rc %d)' % (name, dsd // 44100, rate, 60 / cpu, 60 / wall, p.returncode), flush=True)
    for rate in (176400, 352800):
        clear()
        t0 = os.times()
        p = subprocess.run(BASE + ['--formatin=DSD', '--input', long64, '--samplerate=%d' % rate, '--formatout=DSF', '--dsdrate=2822400', '--dsdmod=adaptive'], capture_output=True)
        t1 = os.times()
        cpu = (t1.children_user - t0.children_user) + (t1.children_system - t0.children_system)
        print('       DSD64 -> DSD64 adaptive via %d: %.1fx per CPU-second, %.1fx wall (rc %d)' % (rate, 60 / cpu, 60 / (t1.elapsed - t0.elapsed), p.returncode), flush=True)

print('TALLY: %d ok, %d not ok' % tuple(TALLY))
sys.exit(1 if TALLY[1] else 0)
