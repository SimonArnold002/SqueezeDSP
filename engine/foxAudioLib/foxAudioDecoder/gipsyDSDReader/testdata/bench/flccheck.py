# Is FLC out != WAV out the engine's existing behaviour (any input), not the DSD decoder's? Same check on today's
# PCM input route, and what sox is asked to do. (It is; and it is NOT sox dither: ledger A3 SOX DOES NOT DITHER.)
import sys, os, subprocess, importlib.util, re
ENGINE, FF, ANA, FX, W = sys.argv[1:6]
spec = importlib.util.spec_from_file_location('e2e', os.path.join(os.path.dirname(os.path.abspath(__file__)), 'e2e_dsd_setup.py'))
sys.argv = [sys.argv[0], ENGINE, FF, ANA, FX, W]
e = importlib.util.module_from_spec(spec); spec.loader.exec_module(e)
FLAC = '/Users/simona/Documents/GitHub/SqueezeDSP/t/lmsbin/Bin/darwin/flac'
src = os.path.join(FX, 'd64_1k.dsf')
pcm = subprocess.run([FF, '-loglevel', 'quiet', '-i', src, '-c:a', 'pcm_s24le', '-ar', '352800', '-ac', '2', '-f', 's24le', '-'], capture_output=True).stdout
def run(fmt):
    e.clear()
    off = os.path.getsize(e.log)
    p = subprocess.run(e.BASE[:-1] + ['--formatout=' + fmt, '--formatin=PCM', '--be=false', '--bitsin=24', '--samplerate=352800', '--channels=2'],
                       input=pcm, capture_output=True, timeout=300)
    return p.stdout, open(e.log, errors='replace').read()[off:]
w, _ = run('WAV')
f, log = run('FLC')
d = subprocess.run([FLAC, '-dcs', '--force-raw-format', '--endian=little', '--sign=signed', '-'], input=f, capture_output=True).stdout
wd = e.wav(w)[1]
diff = sum(1 for i in range(0, min(len(d), len(wd)), 3) if d[i:i+3] != wd[i:i+3])
print('PCM input (today\'s route): FLC vs WAV out: %d of %d samples differ' % (diff, len(wd) // 3))
for l in log.splitlines():
    if re.search(r'sox|Sox|SOX|adapter|Adapter|dither', l):
        print('   log: ' + l.strip()[:220])
