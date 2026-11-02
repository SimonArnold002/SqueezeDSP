# The engine takes DSDCutoff/DSDGain from the player's settings JSON; a command-line flag wins.
import sys, os, json, subprocess, importlib.util
ENGINE, FF, ANA, FX, W = sys.argv[1:6]
spec = importlib.util.spec_from_file_location('e2e', os.path.join(os.path.dirname(os.path.abspath(__file__)), 'e2e_dsd_setup.py'))
sys.argv = [sys.argv[0], ENGINE, FF, ANA, FX, W]
e = importlib.util.module_from_spec(spec); spec.loader.exec_module(e)
st = os.path.join(e.root, 'settings', 'bench.settings.json')
s = json.load(open(st))
def run(prefs, flags):
    s['Client'].pop('DSDCutoff', None); s['Client'].pop('DSDGain', None); s['Client'].update(prefs)
    json.dump(s, open(st, 'w')); e.clear()
    off = os.path.getsize(e.log)
    p = subprocess.run(e.BASE + ['--formatin=DSD', '--input', os.path.join(FX, 'd64_1k.dsf'), '--samplerate=352800'] + flags, capture_output=True)
    log = open(e.log, errors='replace').read()[off:]
    line = [l for l in log.splitlines() if 'DSD decode: cutoff' in l]
    o = e.ana(e.wav(p.stdout)[1], 352800, 1000, str(prefs) + ' ' + ' '.join(flags))
    return float(o.split('tone')[1].split()[0]), float(o.split('50k-Nyq')[1].split()[0]), (line[-1].split('DSD decode: ')[1] if line else '?')
T = [0, 0]
def ok(c, l): T[0 if c else 1] += 1; print(('ok     ' if c else 'NOT OK ') + l)
lv, hi, l = run({'DSDGain': 6, 'DSDCutoff': '24000'}, [])
ok(abs(lv + 6.041) < 0.01 and 'cutoff 24000' in l, 'settings DSDGain 6, DSDCutoff "24000": tone %.3f, engine says "%s"' % (lv, l))
lv, hi, l = run({'DSDGain': 6}, ['--dsdgain=0'])
ok(abs(lv + 12.041) < 0.01, '--dsdgain=0 wins over the setting 6: tone %.3f ("%s")' % (lv, l))
lv, hi, l = run({'DSDCutoff': 0}, [])
ok(hi > -60 and 'cutoff 0' in l, 'settings DSDCutoff 0 (none): 50k-Nyq %.1f dBFS ("%s")' % (hi, l))
lv, hi, l = run({'DSDCutoff': 70000, 'DSDGain': 'x'}, [])
ok(abs(lv + 12.041) < 0.01 and 'cutoff 30000' in l, 'invalid settings fall back to the defaults: tone %.3f ("%s")' % (lv, l))
lv, hi, l = run({}, [])
ok(abs(lv + 12.041) < 0.01 and 'cutoff 30000' in l and hi < -140, 'no settings: defaults, 30 kHz ("%s")' % l)
print('%d ok, %d NOT OK' % tuple(T)); sys.exit(1 if T[1] else 0)
