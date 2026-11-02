/* The SqueezeDSP page: one player's signal path, its processing speed and its main controls.
   Page.pm serves the document (with window.SQDSP: the labels and the version); this builds it.

   PLAYER: ?player=<id> (Material's menu entry passes it), else the player Material has selected
   (lms-material::player, same origin), else the first. A chip picks another; the address then
   names it, so a reload keeps it.

   POLLING: one request at a time, and the period is a GAP after the last one settled, never a
   cadence: LMS speaks HTTP/1.1, a browser allows about six connections to it, and this page runs
   inside Material, whose own Now Playing lives on one of them (HQPlayer Bridge's Live page
   starved it once with overlapping polls). Each turn asks for the status, after whatever else is
   due (the player list every 30 s, the player's settings every 10 s, the presets every minute),
   one request after another, so the status keeps its 2 s whatever else is read. It never stops
   itself; a failure is shown and the next turn tries again.

   SAVING: squeezedsp.saveall writes the player's WHOLE settings file, so the page keeps the whole
   file it read (unknown fields included) and changes only the fields it shows. Switches and
   choices save at once; the three sliders wait for Apply, because every save re-opens the
   playing stream (UI_Functions::saveallCommand seeks 0.5 s on) and a slider moves many times. */
(function () {
    'use strict';

    var CFG = window.SQDSP || {};
    var L = CFG.strings || {};
    var PERIOD = 2000;

    // ---------------------------------------------------------------- helpers
    function $(id) { return document.getElementById(id); }
    function fmt(s, v) { return String(s == null ? '' : s).replace('%s', v); }
    function num(v, d) { var n = Number(v); return (v === '' || v === null || v === undefined || !isFinite(n)) ? d : n; }
    function make(tag, cls, text) {
        var e = document.createElement(tag);
        if (cls) { e.className = cls; }
        if (text !== undefined && text !== null) { e.textContent = text; }
        return e;
    }
    function rate(hz) {
        hz = num(hz, 0);
        if (!hz) { return ''; }
        if (hz >= 1000000) { return (Math.round(hz / 10000) / 100) + ' MHz'; }
        return (Math.round(hz / 100) / 10) + ' kHz';
    }
    function speedText(n) { n = num(n, 0); return n >= 100 ? String(Math.round(n)) : n.toFixed(1); }

    // one JSON-RPC request; resolves with its result, rejects with an Error that says why
    function rpc(player, cmd, ms) {
        return new Promise(function (resolve, reject) {
            var x = new XMLHttpRequest();
            x.open('POST', '/jsonrpc.js', true);
            x.setRequestHeader('Content-Type', 'application/json');
            x.timeout = ms || 8000;
            x.onload = function () {
                var r = null;
                try { r = JSON.parse(x.responseText); } catch (e) { r = null; }
                if (!r || r.error || !r.result) {
                    reject(new Error((r && r.error && (r.error.message || r.error)) || ('no result (HTTP ' + x.status + ')')));
                    return;
                }
                resolve(r.result);
            };
            x.onerror = function () { reject(new Error('request failed')); };
            x.ontimeout = function () { reject(new Error('request timed out')); };
            x.send(JSON.stringify({ id: 1, method: 'slim.request', params: [player || '', cmd] }));
        });
    }

    // The defaults the classic page gives a player with no settings yet (sqdsp_data.js
    // InitialiseData), so a player first set up here gets the same file. Keep them in step.
    function defaults() {
        return {
            Bypass: 1, OutputFormat: 'FLAC', Upsample: 'off', UpsamplePrecision: 28, UpsampleSteep: 0, DSDCutoff: 30000, DSDGain: 0,
            UpsamplePassband: 0.99, UpsamplePhase: 'linear', UpsampleTarget: 'family', UpsampleMax: 0, Preamp: 0, Balance: 0, Width: 0,
            Delay: { delay: 0 }, Loudness: { enabled: 0, listening_level: 85 }, Filters: [],
            FIRWavFile: '-', Last_preset: '', Preset: 'None', FIRStrength: 100, Crossfeed: 'off',
            ReplayGain: { enabled: 0, mode: 3, fixed_gain: -6.0, spotify_gain: -4.0 }
        };
    }
    // sqdsp_data.js deepMerge: the source's values over the target's, objects merged, arrays replaced
    function merge(target, source) {
        var out = {}, k;
        for (k in target) { if (Object.prototype.hasOwnProperty.call(target, k)) { out[k] = target[k]; } }
        for (k in source) {
            if (!Object.prototype.hasOwnProperty.call(source, k)) { continue; }
            var s = source[k], t = target[k];
            if (s !== null && typeof s === 'object' && !Array.isArray(s) && t !== null && typeof t === 'object' && !Array.isArray(t)) {
                out[k] = merge(t, s);
            } else if (s !== undefined) {
                out[k] = s;
            }
        }
        return out;
    }

    // ---------------------------------------------------------------- state
    var PLAYERS = [];       // [{ id, name, connected }]
    var PID = null;         // the player shown
    var CAN = null;         // what it can take (readclientSettings)
    var DATA = null;        // its whole settings file, over the defaults
    var STATUS = null;      // squeezedsp.status
    var PRESETS = [];       // [{ path, name }]
    var PENDING = {};       // slider values not applied yet
    var GEN = 0;            // bumped by every local change: a settings read sent before one is stale
    var NEED = { players: true, settings: true, presets: true };
    var T = { players: 0, settings: 0, presets: 0 };
    var UI = null;          // the built controls

    var PRECISIONS = [20, 24, 28, 33];
    var PASSBANDS = [0, 0.9, 0.95, 0.99, 0.995];
    // the passband (Upsample.pm PASSBAND_FAMILY, PASSBAND_FIXED): per family 99% by default; a fixed target 95% by
    // default and at most that
    var PB_FAMILY = 0.99, PB_FIXED = 0.95;
    // a DSF on our engine's own decoder (0.3.13): its low-pass (Hz, 0 none) and gain (dB), the player's DSDCutoff and DSDGain
    var DSD_CUTOFFS = [24000, 30000, 50000, 0];
    var DSD_GAINS = [0, 6];
    var PHASES = ['linear', 'intermediate', 'minimum'];
    var PHASE_NAMES = { linear: 'Linear', intermediate: 'Intermediate', minimum: 'Minimum' };
    var TARGETS = ['family', 'fixed'];
    var TARGET_NAMES = { family: 'Per family', fixed: 'Player max' };
    var MAXRATES = [0, 768000, 384000, 192000, 96000];   // 0: the player's own top rate (Upsample.pm @MAXRATES)
    var ROLE_NAMES = { 'flac-dec': 'FLAC decode', 'flac-enc': 'FLAC encode', soxr: 'soxr', soxrate: 'SoX rate',
                       level: 'level', dsp: 'DSP', sox: 'SoX', ffmpeg: 'ffmpeg', wavlength: 'WAV length', dsd: 'DSD decode',
                       'mp3-enc': 'MP3 encode', 'opus-enc': 'Opus encode', 'ogg-enc': 'Vorbis encode' };
    // a reduced stream an app asks for (readclientSettings out_reduced), at Mobile Transcode Rules' qualities
    var REDUCED = { MP3: 'MP3 (320 kbps)', OPS: 'Opus (256 kbps)', OGG: 'Ogg Vorbis (quality 10)' };
    var CODECS = { flc: 'FLAC', alc: 'ALAC', aif: 'AIFF', wav: 'WAV', mp3: 'MP3', aac: 'AAC', mp4: 'AAC', ogg: 'Ogg Vorbis',
                   ops: 'Opus', dsf: 'DSF', dff: 'DFF', wvp: 'WavPack', ape: 'APE', wma: 'WMA', mpc: 'Musepack' };

    function client() { return DATA ? DATA.Client : null; }
    function dirty() { return Object.keys(PENDING).length > 0; }

    // ---------------------------------------------------------------- the player
    function param(name) {
        try { return new URLSearchParams(window.location.search).get(name); } catch (e) { return null; }
    }
    function find(id) {
        if (!id) { return null; }
        id = String(id).toLowerCase();
        for (var i = 0; i < PLAYERS.length; i++) { if (PLAYERS[i].id.toLowerCase() === id) { return PLAYERS[i].id; } }
        return null;
    }
    function wanted() {
        var m = null;
        try { m = window.localStorage.getItem('lms-material::player'); } catch (e) { m = null; }
        return find(param('player')) || find(m) || (PLAYERS[0] ? PLAYERS[0].id : null);
    }
    function select(id, remember) {
        if (id === PID) { return; }
        PID = id; CAN = null; DATA = null; STATUS = null; PRESETS = []; PENDING = {};
        NEED.settings = true; NEED.presets = true;
        if (remember && window.history && window.history.replaceState) {
            try { window.history.replaceState(null, '', '?player=' + encodeURIComponent(id)); } catch (e) {}
        }
        build();
        renderPick();
    }

    // ---------------------------------------------------------------- loading
    function loadPlayers() {
        return rpc('', ['players', 0, 100]).then(function (r) {
            PLAYERS = (r.players_loop || []).filter(function (p) {
                return p.playerid && (p.isplayer === undefined || num(p.isplayer, 1) !== 0);
            }).map(function (p) {
                return { id: String(p.playerid), name: p.name || String(p.playerid), connected: num(p.connected, 1) === 1 };
            });
            var keep = find(PID);
            if (!keep) { select(wanted(), false); } else { renderPick(); }
            if (!PID) { build(); }
        });
    }

    function loadSettings() {
        var pid = PID, gen = GEN;
        return rpc(pid, ['squeezedsp.readclientSettings']).then(function (r) {
            if (pid !== PID) { return; }
            CAN = {
                dspOk: num(r.dsp_ok, 1) !== 0,
                wavOk: num(r.wav_ok, 0) === 1,
                upOk: num(r.up_ok, 0) === 1,
                upWhy: r.up_why || '',
                upTargets: r.up_targets || '',
                upFixed: r.up_fixed || '',
                upLimits: r.up_limits || '',
                upFfmpeg: r.up_ffmpeg || '',
                upSox: r.up_sox || '',
                // a reduced stream (MP3, OPS, OGG), and the Bitrate Limiting an MP3 is at (0: an app's 320 kbps)
                outReduced: r.out_reduced || '',
                outKbps: num(r.out_kbps, 0)
            };
            // a change of ours went out after this was asked for, or is still going out
            if (gen !== GEN || SAVE.busy) { renderControls(); return; }
            var server = {};
            try { server = r.json ? JSON.parse(r.json) : {}; } catch (e) { server = {}; }
            if (!server || typeof server !== 'object') { server = {}; }
            DATA = merge({ Client: defaults() }, server);
            DATA.ClientName = r.clientName || DATA.ClientName;
            DATA.Revision = r.revision || DATA.Revision || 1;
            delete DATA.fresh_player;
            renderControls();
            renderPresets();
        });
    }

    function loadPresets() {
        var pid = PID;
        return rpc(pid, ['squeezedsp.filters']).then(function (r) {
            if (pid !== PID) { return; }
            PRESETS = (r.Preset_loop || []).map(function (p) {
                var path = String(p[0] !== undefined ? p[0] : p);
                var name = path.split(/[\\/]/).pop().replace(/\.preset\.json$/i, '');
                return { path: path, name: name };
            });
            renderPresets();
        });
    }

    function loadStatus() {
        var pid = PID;
        return rpc(pid, ['squeezedsp.status']).then(function (r) {
            if (pid !== PID) { return; }
            STATUS = r.status || null;
            renderStatus();
        });
    }

    // ---------------------------------------------------------------- the poller
    var timer = null, inFlight = false;

    function schedule(ms) {
        if (timer) { clearTimeout(timer); }
        timer = setTimeout(poll, ms === undefined ? PERIOD : ms);
    }

    // Before the status, whatever else is due: what is NEEDED (the first load, after a preset
    // action) one after another, else at most one periodic re-read. A step that fails is put back
    // for the next turn. Still one request at a time: each waits for the one before.
    var STEPS = [['players', 30000, function () { return loadPlayers(); }],
                 ['settings', 10000, function () { return loadSettings(); }],
                 ['presets', 60000, function () { return loadPresets(); }]];

    function housekeeping(periodicDone) {
        var now = Date.now();
        for (var i = 0; i < STEPS.length; i++) {
            var name = STEPS[i][0];
            if (name !== 'players' && !PID) { continue; }
            var due = NEED[name] || (!periodicDone && now - T[name] > STEPS[i][1]);
            if (!due) { continue; }
            var needed = NEED[name];
            NEED[name] = false;
            T[name] = now;
            return STEPS[i][2]().then(function () {
                return housekeeping(periodicDone || !needed);
            }, function (e) {
                NEED[name] = true;
                throw e;
            });
        }
        return Promise.resolve();
    }

    function poll() {
        if (inFlight) { schedule(); return; }
        inFlight = true;
        var problem = null;
        Promise.resolve().then(function () { return housekeeping(false); })
            .catch(function (e) { problem = e; })
            .then(function () { return PID ? loadStatus() : null; })
            .then(function () { done(!problem, problem && problem.message); },
                  function (e) { done(false, e && e.message); });
    }

    function done(ok, msg) {
        inFlight = false;
        stamp(ok, msg);
        schedule();
    }

    function stamp(ok, msg) {
        var d = $('dot'), e = $('err');
        if (d) { d.className = 'dot ' + (ok ? 'live' : 'err'); }
        if (e) { e.textContent = ok ? '' : (msg || ''); }
    }

    var flashTimer = null;
    function flash(text) {
        var f = $('flash');
        if (!f) { return; }
        f.textContent = text;
        if (flashTimer) { clearTimeout(flashTimer); }
        flashTimer = setTimeout(function () { f.textContent = ''; }, 2000);
    }

    // ---------------------------------------------------------------- saving
    // One save at a time. A change made while one is out is sent after it, as the whole file then. A waiting
    // save holds its player and that player's file, taken when the change is made, so picking another player
    // before it goes out neither drops it nor sends it to the other player.
    var SAVE = { busy: false, queue: [] };   // [{ pid, data, waiters }], one per player

    function save() {
        var pid = PID, data = DATA;
        return new Promise(function (resolve) {
            if (!pid || !data) { resolve(false); return; }
            var job = null;
            SAVE.queue.forEach(function (j) { if (j.pid === pid) { job = j; } });
            if (!job) { job = { pid: pid, waiters: [] }; SAVE.queue.push(job); }
            job.data = data;   // a preset load replaces the file
            job.waiters.push(resolve);
            if (!SAVE.busy) { runSave(); }
        });
    }

    function runSave() {
        var job = SAVE.queue.shift();
        if (!job) { return; }
        // a save for a player no longer shown says whose it was
        var who = function () { return job.pid === PID ? '' : nameOf(job.pid) + ': '; };
        SAVE.busy = true;
        rpc(job.pid, ['squeezedsp.saveall', 'val:' + JSON.stringify(job.data)], 15000).then(function (r) {
            if (!r || !r._done) { throw new Error('the server did not confirm the save'); }
            flash(who() + (L.saved || 'Saved'));
            return true;
        }).catch(function (e) {
            var el = $('err');
            if (el) { el.textContent = who() + fmt(L.saveFailed || 'Not saved: %s', e.message); }
            return false;
        }).then(function (ok) {
            SAVE.busy = false;
            job.waiters.forEach(function (w) { w(ok); });
            runSave();
            // a player picked while this was out had its first settings read held back (loadSettings)
            if (!DATA) { NEED.settings = true; }
            // the stream was re-opened through the new settings: look at it soon
            schedule(600);
        });
    }

    function change(key, value) {
        var c = client();
        if (!c) { return; }
        c[key] = value;
        GEN++;
        renderControls();
        save();
    }

    // ---------------------------------------------------------------- building the page
    function build() {
        var app = $('app');
        if (!app) { return; }
        app.textContent = '';
        UI = null;
        var who = $('who');
        if (who) { who.textContent = PID ? nameOf(PID) : ''; }
        if (!PID) {
            app.appendChild(make('div', 'card note', L.noPlayer || 'No player is connected.'));
            return;
        }
        UI = {};

        // the signal path, and its speed as the headline
        var st = make('div', 'card status');
        st.appendChild(make('h2', null, L.path || 'Signal path'));
        var np = make('div', 'np');
        UI.npState = np.appendChild(make('span', 'np-state'));
        UI.npTitle = np.appendChild(make('span', 'np-title'));
        UI.npArtist = np.appendChild(make('span', 'np-artist'));
        st.appendChild(np);
        var sp = make('div', 'speed');
        sp.appendChild(make('span', 'speed-k', L.speed || 'Processing speed'));
        UI.speed = sp.appendChild(make('span', 'speed-v none', '-'));
        UI.speedSub = sp.appendChild(make('span', 'speed-s'));
        st.appendChild(sp);
        UI.statusNote = st.appendChild(make('div', 'note'));
        var rows = make('div', 'rows');
        UI.rows = {};
        [['source', L.source || 'Source'], ['up', L.upsample || 'Upsample'], ['dsp', L.dsp || 'DSP'],
         ['out', L.output || 'Output'], ['stages', L.stages || 'Stages']].forEach(function (r) {
            var row = make('div', 'row hide');
            row.appendChild(make('div', 'k', r[1]));
            var v = row.appendChild(make('div', 'v'));
            rows.appendChild(row);
            UI.rows[r[0]] = { row: row, v: v };
        });
        st.appendChild(rows);
        app.appendChild(st);

        // the controls
        var cc = make('div', 'card controls');
        UI.dsp = control(cc, 'dsp', L.dsp || 'DSP', L.dspHelp);
        UI.dspSw = UI.dsp.c.appendChild(switchButton(function (on) { change('Bypass', on ? 0 : 1); }));
        UI.dspOff = cc.appendChild(make('div', 'note'));

        UI.out = control(cc, 'output', L.output || 'Output', L.outputHelp);
        UI.outSeg = segment(UI.out.c, [['FLAC', 'FLAC 24-bit'], ['PCM', 'PCM (WAV 24-bit)']],
                            function (v) { change('OutputFormat', v); });

        UI.up = control(cc, 'upsample', L.upsample || 'Upsample', L.upHelp);
        UI.upSw = UI.up.c.appendChild(switchButton(function (on) { change('Upsample', on ? 'on' : 'off'); }));
        UI.upOpts = cc.appendChild(make('div', 'upopts'));
        UI.target = control(UI.upOpts, 'target', L.target || 'Target rate', null, true);
        UI.targetSeg = segment(UI.target.c, TARGETS.map(function (t) { return [t, TARGET_NAMES[t]]; }),
                               function (v) { change('UpsampleTarget', v); });
        UI.max = control(UI.upOpts, 'max', L.max || 'Max rate', null, true);
        UI.maxSeg = segment(UI.max.c, MAXRATES.map(function (m) { return [String(m), m ? rate(m) : (L.maxNone || 'No limit')]; }),
                            function (v) { change('UpsampleMax', Number(v)); });
        UI.phase = control(UI.upOpts, 'phase', L.phase || 'Phase', null, true);
        UI.phaseSeg = segment(UI.phase.c, PHASES.map(function (p) { return [p, PHASE_NAMES[p]]; }),
                              function (v) { change('UpsamplePhase', v); });
        UI.prec = control(UI.upOpts, 'precision', L.precision || 'Precision', null, true);
        UI.precSeg = segment(UI.prec.c, PRECISIONS.map(function (p) { return [String(p), p + '-bit']; }),
                             function (v) { change('UpsamplePrecision', Number(v)); });
        UI.pb = control(UI.upOpts, 'passband', L.passband || 'Passband', null, true);
        UI.pbSeg = segment(UI.pb.c, PASSBANDS.map(function (p) {
            return [String(p), p ? (Math.round(p * 1000) / 10) + '%' : (L.pbDefault || 'Auto')];
        }), function (v) { change('UpsamplePassband', Number(v)); });
        UI.steep = control(UI.upOpts, 'steep', L.steep || 'Steep (Chebyshev)', null, true);
        UI.steepSw = UI.steep.c.appendChild(switchButton(function (on) { change('UpsampleSteep', on ? 1 : 0); }));

        // DSD: how our engine decodes a DSF (ledger OUR OWN DSD DECODER); the engine reads them at each track
        UI.dsd = control(cc, 'dsd', L.dsd || 'DSD', L.dsdHelp);
        UI.dsdOpts = cc.appendChild(make('div', 'dsdopts'));
        UI.dsdCut = control(UI.dsdOpts, 'dsdcutoff', L.dsdCutoff || 'Low-pass', null, true);
        UI.dsdCutSeg = segment(UI.dsdCut.c, DSD_CUTOFFS.map(function (h) { return [String(h), h ? rate(h) : (L.dsdNone || 'None')]; }),
                               function (v) { change('DSDCutoff', Number(v)); });
        UI.dsdGain = control(UI.dsdOpts, 'dsdgain', L.dsdGain || 'Gain', null, true);
        UI.dsdGainSeg = segment(UI.dsdGain.c, DSD_GAINS.map(function (g) { return [String(g), (g > 0 ? '+' : '') + g + ' dB']; }),
                                function (v) { change('DSDGain', Number(v)); });

        UI.sliders = {};
        [['Preamp', L.preamp || 'Preamp (dB)', L.preampHelp, -30, 0, ' dB'],
         ['Balance', L.balance || 'Balance', L.balanceHelp, -12, 12, ''],
         ['Width', L.width || 'Width', L.widthHelp, -12, 12, '']].forEach(function (s) {
            var ctl = control(cc, s[0], s[1], s[2]);
            var range = make('input', 'range');
            range.type = 'range'; range.min = s[3]; range.max = s[4]; range.step = '0.1';
            range.setAttribute('aria-label', s[1]);
            var val = make('span', 'val');
            ctl.c.appendChild(range);
            ctl.c.appendChild(val);
            var key = s[0], unit = s[5];
            function pend(v) {
                PENDING[key] = Math.round(Number(v) * 10) / 10;
                renderSliders();
            }
            range.addEventListener('input', function () { pend(range.value); });
            // the classic page's own gesture: a double click puts balance and width back to centre
            if (key !== 'Preamp') { range.addEventListener('dblclick', function () { range.value = 0; pend(0); }); }
            UI.sliders[key] = { ctl: ctl, range: range, val: val, unit: unit };
        });
        var ar = cc.appendChild(make('div', 'applyrow'));
        UI.apply = ar.appendChild(make('button', 'btn primary', L.apply || 'Apply'));
        UI.apply.type = 'button';
        UI.apply.addEventListener('click', function () {
            var c = client();
            if (!c || !dirty()) { return; }
            Object.keys(PENDING).forEach(function (k) { c[k] = PENDING[k]; });
            PENDING = {};
            GEN++;
            renderControls();
            save();
        });
        app.appendChild(cc);

        // presets
        var pc = make('div', 'card presets');
        pc.appendChild(make('h2', null, L.presets || 'Presets'));
        // no row labels here: the buttons say what each row does
        var pl = control(pc, 'load', '');
        UI.presetSel = pl.c.appendChild(make('select'));
        UI.presetSel.setAttribute('aria-label', L.presets || 'Presets');
        UI.presetLoad = pl.c.appendChild(make('button', 'btn', L.load || 'Load'));
        UI.presetDel = pl.c.appendChild(make('button', 'btn', L.del || 'Delete'));
        UI.presetLoad.type = UI.presetDel.type = 'button';
        UI.presetNote = pc.appendChild(make('div', 'note'));
        var ps = control(pc, 'saveas', '');
        UI.presetName = ps.c.appendChild(make('input'));
        UI.presetName.type = 'text';
        UI.presetName.placeholder = L.presetName || 'Preset name';
        UI.presetName.setAttribute('aria-label', L.presetName || 'Preset name');
        UI.presetSave = ps.c.appendChild(make('button', 'btn', L.saveAs || 'Save as'));
        UI.presetSave.type = 'button';
        UI.presetLoad.addEventListener('click', loadPreset);
        UI.presetDel.addEventListener('click', deletePreset);
        UI.presetSave.addEventListener('click', savePreset);
        UI.presetName.addEventListener('keydown', function (e) { if (e.key === 'Enter') { savePreset(); } });
        app.appendChild(pc);

        // the rest is on the classic page
        var more = make('div', 'more');
        var a = make('a', null, L.more || 'Equaliser, filters and the rest are on the full settings page.');
        a.href = '/plugins/SqueezeDSP/index.html?player=' + encodeURIComponent(PID);
        more.appendChild(a);
        app.appendChild(more);

        [UI.dsp, UI.up, UI.steep].forEach(function (c) { c.row.classList.add('sw-row'); });
        renderControls();
        renderPresets();
        renderStatus();
    }

    function nameOf(id) {
        for (var i = 0; i < PLAYERS.length; i++) { if (PLAYERS[i].id === id) { return PLAYERS[i].name; } }
        return id;
    }

    function control(parent, key, label, help, sub) {
        var row = make('div', 'ctl' + (sub ? ' sub-ctl' : ''));
        row.dataset.k = key;
        var k = row.appendChild(make('div', 'k', label));
        if (help) { k.title = help; }
        var c = row.appendChild(make('div', 'c'));
        var note = row.appendChild(make('div', 'note'));
        parent.appendChild(row);
        return { row: row, c: c, note: note };
    }

    // a switch: its row keeps label and switch on one line at any width (.sw-row)
    function switchButton(onToggle) {
        var b = make('button', 'sw');
        b.type = 'button';
        b.setAttribute('role', 'switch');
        b.setAttribute('aria-checked', 'false');
        b.addEventListener('click', function () {
            if (b.disabled) { return; }
            onToggle(b.getAttribute('aria-checked') !== 'true');
        });
        return b;
    }

    function setSwitch(b, on, disabled) {
        b.setAttribute('aria-checked', on ? 'true' : 'false');
        b.disabled = !!disabled;
    }

    function segment(parent, options, onPick) {
        var seg = make('div', 'seg');
        seg.setAttribute('role', 'radiogroup');
        var buttons = {};
        options.forEach(function (o) {
            var b = make('button', null, o[1]);
            b.type = 'button';
            b.setAttribute('role', 'radio');
            b.setAttribute('aria-checked', 'false');
            b.dataset.value = o[0];
            b.addEventListener('click', function () {
                if (b.disabled || b.getAttribute('aria-checked') === 'true') { return; }
                onPick(o[0]);
            });
            buttons[o[0]] = b;
            seg.appendChild(b);
        });
        parent.appendChild(seg);
        return buttons;
    }

    function setSegment(buttons, value, disabledValues) {
        Object.keys(buttons).forEach(function (v) {
            buttons[v].setAttribute('aria-checked', v === String(value) ? 'true' : 'false');
            buttons[v].disabled = !!(disabledValues && disabledValues.indexOf(v) >= 0);
        });
    }

    // ---------------------------------------------------------------- rendering
    function renderPick() {
        var pick = $('pick');
        if (!pick) { return; }
        pick.textContent = '';
        var who = $('who');
        if (who) { who.textContent = PID ? nameOf(PID) : ''; }
        if (PLAYERS.length < 2) { return; }
        PLAYERS.forEach(function (p) {
            var b = make('button', 'chip' + (p.id === PID ? ' on' : '') + (p.connected ? '' : ' off'), p.name);
            b.type = 'button';
            b.addEventListener('click', function () { select(p.id, true); schedule(0); });
            pick.appendChild(b);
        });
    }

    // what the server will actually do with a stored value (Upsample::options, Configuration.pm)
    function effective(c, can) {
        var p = num(c.UpsamplePrecision, 28);
        if (PRECISIONS.indexOf(p) < 0) { p = 28; }
        var tg = String(c.UpsampleTarget || '').toLowerCase();
        if (TARGETS.indexOf(tg) < 0) { tg = 'family'; }
        // the passband: the target's default for none or one without a button; a fixed target plays at most 95%
        var pbDef = tg === 'fixed' ? PB_FIXED : PB_FAMILY;
        var pb = num(c.UpsamplePassband, pbDef);
        var pbOk = false;
        PASSBANDS.forEach(function (x) { if (Math.abs(x - pb) < 0.0005) { pb = x; pbOk = true; } });
        if (!pbOk) { pb = pbDef; }
        if (tg === 'fixed' && pb > PB_FIXED) { pb = PB_FIXED; }
        var ph = String(c.UpsamplePhase || '').toLowerCase();
        if (PHASES.indexOf(ph) < 0 || (ph !== 'linear' && !can.upSox)) { ph = 'linear'; }
        var steep = /^(1|on|true)$/i.test(String(c.UpsampleSteep));
        // the max rate: one the server knows, at or below the player's top rate for its output (up_limits:
        // FLAC out, WAV out); above it, the player's own
        var limits = String(can.upLimits || '').split(',');
        var limit = num(c.OutputFormat === 'PCM' && can.wavOk ? limits[1] : limits[0], 0);
        var mx = num(c.UpsampleMax, 0);
        if (MAXRATES.indexOf(mx) < 0 || (limit && mx > limit)) { mx = 0; }
        return { precision: p, passband: pb, phase: ph, steep: steep, target: tg, max: mx, limit: limit };
    }

    // the targets the server writes under a limit and a max rate (Upsample::targets): the highest rate of each
    // family at or below the lower of the two, so a change of output or max rate is named before the server is
    // asked again; null with no limit known
    function upRates(limit, max) {
        var ceil = (max && (!limit || max < limit)) ? max : limit;
        if (!ceil) { return null; }
        var f44 = 44100, f48 = 48000;
        while (f44 * 2 <= ceil) { f44 *= 2; }
        while (f48 * 2 <= ceil) { f48 *= 2; }
        if (f48 > ceil) { f48 = f44; }
        return { f44: f44, f48: f48, top: Math.max(f44, f48) };
    }

    function whyNoUpsample(can) {
        if (can.upWhy === 'reduced') { return L.noUpReduced || ''; }
        if (can.upWhy === 'nosoxr') { return (L.noSoxr || '') + (can.upFfmpeg ? ' ' + can.upFfmpeg : ''); }
        if (can.upWhy === 'nomax') { return L.noMax || ''; }
        if (can.upWhy === 'hook') { return L.noHook || ''; }
        return L.noFfmpeg || '';
    }

    function renderControls() {
        if (!UI) { return; }
        var c = client(), can = CAN;
        var ready = !!(c && can);
        var cardCtl = UI.dsp.row.parentNode;
        cardCtl.classList.toggle('dimmed', !ready);
        if (!ready) {
            [UI.dspSw, UI.upSw, UI.steepSw].forEach(function (b) { b.disabled = true; });
            return;
        }
        var dspOn = String(c.Bypass) === '0';
        setSwitch(UI.dspSw, dspOn && can.dspOk, !can.dspOk);
        UI.dsp.note.textContent = can.dspOk ? '' : (L.noDsp || '');
        UI.dspOff.textContent = (can.dspOk && !dspOn) ? (L.dspOffNote || '') : '';

        var pcm = c.OutputFormat === 'PCM' && can.wavOk;
        setSegment(UI.outSeg, pcm ? 'PCM' : 'FLAC', can.wavOk ? [] : ['PCM']);
        // a reduced stream overrides the output chosen here while it is due (Bitrate Limiting, or the app's own list)
        UI.out.note.textContent = can.outReduced
            ? (can.outReduced === 'MP3' && can.outKbps ? fmt(L.reducedLimit || '%s', can.outKbps)
                                                      : fmt(L.reducedApp || '%s', REDUCED[can.outReduced] || can.outReduced))
            : (can.wavOk ? '' : (L.pcmNeeds || ''));

        var upOn = c.Upsample === 'on' && can.upOk;
        var e = effective(c, can);
        var ur = upRates(e.limit, e.max);
        setSwitch(UI.upSw, upOn, !can.upOk);
        UI.upOpts.classList.toggle('hide', !upOn);
        if (!can.upOk) {
            UI.up.note.textContent = whyNoUpsample(can);
        } else if (upOn && e.target === 'fixed' && (ur || can.upFixed)) {
            UI.up.note.textContent = (L.fixed || '') + ' ' + rate(ur ? ur.top : can.upFixed);
        } else if (upOn && can.upTargets) {
            UI.up.note.textContent = (L.targets || '') + ' ' + (ur ? [ur.f44, ur.f48] : can.upTargets.split(',')).map(rate).join(' / ');
        } else {
            UI.up.note.textContent = '';
        }
        setSegment(UI.targetSeg, e.target);
        setSegment(UI.maxSeg, String(e.max), MAXRATES.filter(function (m) { return m && e.limit && m > e.limit; }).map(String));
        setSegment(UI.phaseSeg, e.phase, can.upSox ? [] : ['intermediate', 'minimum']);
        UI.phase.note.textContent = can.upSox ? '' : (L.noSox || '');
        setSegment(UI.precSeg, String(e.precision));
        setSegment(UI.pbSeg, String(e.passband), e.target === 'fixed' ? PASSBANDS.filter(function (x) { return x > PB_FIXED; }).map(String) : []);
        setSwitch(UI.steepSw, e.steep, false);
        setSegment(UI.dsdCutSeg, String(dsdCutoff(c)));
        setSegment(UI.dsdGainSeg, String(dsdGain(c)));

        // what DSP on drives: dimmed while it is off, still editable
        [UI.up.row, UI.upOpts, UI.dsd.row, UI.dsdOpts].concat(Object.keys(UI.sliders).map(function (k) { return UI.sliders[k].ctl.row; }))
            .forEach(function (r) { r.classList.toggle('dimmed', !dspOn); });
        renderSliders();
    }

    // the DSD settings as the engine takes them (LyrionDSPSettings buildConfig): unset or out of range, its default
    function dsdCutoff(c) { var v = num(c.DSDCutoff, NaN); return (v === 0 || (v > 20000 && v <= 60000)) ? v : 30000; }
    function dsdGain(c) { var v = num(c.DSDGain, NaN); return (v >= -24 && v <= 12) ? v : 0; }

    function renderSliders() {
        if (!UI) { return; }
        var c = client() || {};
        Object.keys(UI.sliders).forEach(function (k) {
            var s = UI.sliders[k];
            var pending = Object.prototype.hasOwnProperty.call(PENDING, k);
            var v = pending ? PENDING[k] : num(c[k], 0);
            // never move a thumb the user is holding
            if (document.activeElement !== s.range || !pending) { s.range.value = v; }
            s.range.disabled = !DATA;
            s.val.textContent = (v > 0 && k !== 'Preamp' ? '+' : '') + v.toFixed(1) + s.unit;
            s.val.classList.toggle('pending', pending);
        });
        UI.apply.disabled = !dirty();
        UI.apply.parentNode.classList.toggle('hide', !dirty());
    }

    // The options are rebuilt only when the list changed: this runs after every settings read,
    // and rebuilding a select's options closes it if the user has it open.
    function renderPresets() {
        if (!UI) { return; }
        var sel = UI.presetSel, c = client() || {};
        var last = String(c.Last_preset || '');
        var keep = sel.value;
        var sig = PRESETS.map(function (p) { return p.path; }).join('\n');
        if (sig === UI.presetSig && (keep || !PRESETS.length)) { return; }
        UI.presetSig = sig;
        sel.textContent = '';
        PRESETS.forEach(function (p) {
            var o = make('option', null, p.name);
            o.value = p.path;
            sel.appendChild(o);
        });
        var match = null;
        PRESETS.forEach(function (p) { if (p.path === keep) { match = p.path; } });
        if (!match) { PRESETS.forEach(function (p) { if (p.path === last || p.name === last) { match = p.path; } }); }
        if (match) { sel.value = match; }
        var none = PRESETS.length === 0;
        sel.disabled = UI.presetLoad.disabled = UI.presetDel.disabled = none;
        UI.presetNote.textContent = none ? (L.noPresets || '') : '';
    }

    function renderStatus() {
        if (!UI) { return; }
        var st = STATUS || {}, now = st.now || {}, p = st.pipeline || {};
        var mode = now.mode || 'stop';
        UI.npState.textContent = mode === 'play' ? '' : (mode === 'pause' ? (L.paused || 'Paused') : (L.stopped || 'Not playing'));
        UI.npTitle.textContent = now.title || '';
        UI.npArtist.textContent = now.artist || '';

        var rows = UI.rows;
        function row(name, text) {
            rows[name].v.textContent = text || '';
            rows[name].row.classList.toggle('hide', !text);
        }
        // A pipeline runs while LMS streams its track: near a track's end it has finished (the whole
        // track is with the player) and the server keeps its last figures (finished), and the next
        // track's starts a few seconds before the change (next: that track, whose path is shown).
        var ahead = !!(p.running && p.next);
        var note = '';
        if (!STATUS) {
            note = '';
        } else if (!p.speed_ok) {
            note = L.noProc || '';
        } else if (ahead) {
            note = p.next.title ? fmt(L.nextTrack || 'Processing the next track: %s', p.next.title)
                                : fmt(L.nextTrack || 'Processing the next track: %s', '').replace(/[\s:]+$/, '');
        } else if (p.finished) {
            note = L.finished || '';
        } else if (!p.running) {
            note = mode === 'play' ? (L.notInPath || '') : '';
        }
        UI.statusNote.textContent = note;

        // figures: a running pipeline's, or a finished one's last (none when the page never saw it)
        var figures = !!(STATUS && (p.running || p.finished) && p.stages);
        if (figures && p.speed !== undefined && p.speed !== null) {
            UI.speed.textContent = fmt(L.realtime || '%sx realtime', speedText(p.speed));
            UI.speed.className = 'speed-v';
        } else if (STATUS && p.running) {
            UI.speed.textContent = L.measuring || 'Measuring...';
            UI.speed.className = 'speed-v none';
        } else {
            UI.speed.textContent = '-';
            UI.speed.className = 'speed-v none';
        }
        var sub = [];
        if (figures && p.slowest) { sub.push(fmt(L.slowest || 'slowest stage: %s', ROLE_NAMES[p.slowest] || p.slowest)); }
        if (figures && p.cpu !== undefined && p.cpu !== null) { sub.push(fmt(L.cpu || 'CPU %s% of one core', p.cpu)); }
        UI.speedSub.textContent = sub.join(' · ');

        var track = ahead ? p.next : now, src = [];
        if (track.type) { src.push(CODECS[track.type] || String(track.type).toUpperCase()); }
        if (track.rate) { src.push(rate(track.rate)); }
        if (track.bits) { src.push(track.bits + '-bit'); }
        row('source', (mode !== 'stop' || p.running) ? src.join(' · ') : '');

        if (figures) {
            var r = p.resampler;
            if (r && r.tool === 'decoder') {
                // our engine's own DSD decode (0.3.13): its low-pass and gain, as the engine takes them (Status _dsdSettings)
                var dd = [L.dsdDecoder || 'Our decoder',
                          r.cutoff ? fmt(L.dsdLowpass || '%s low-pass', rate(r.cutoff)) : (L.dsdNoLowpass || 'no low-pass')];
                if (r.gain) { dd.push((r.gain > 0 ? '+' : '') + r.gain + ' dB'); }
                row('up', fmt(L.dsdUp || 'DSD: %s', dd.join(' · ') + ' → ' + rate(r.target)));
            } else if (r) {
                var u = [PHASE_NAMES[r.phase] || r.phase];
                if (r.precision) { u.push(r.precision + '-bit'); }
                if (r.passband) { u.push((Math.round(r.passband * 1000) / 10) + '%'); }
                if (r.steep) { u.push(L.steep || 'steep'); }
                var upText = u.join(' · ') + ' → ' + rate(r.target) + ' (' + (r.tool === 'sox' ? 'SoX' : 'soxr') + ')';
                // DSD's own rule takes it to the target through soxr, Upsample on or off: said so, not read as Upsample
                row('up', r.dsd ? fmt(L.dsdUp || 'DSD: %s', upText) : upText);
            } else if (p.bypassed) {
                // Upsample on, the track already at its target: the stream start skipped the resampler
                row('up', fmt(L.bypassed || 'Bypassed: the track is already at %s', rate((p.dsp || {}).rate)));
            } else {
                row('up', L.off || 'Off');
            }
            var d = p.dsp || {};
            row('dsp', [rate(d.rate), d.float ? '32-bit ' + (L.float || 'float') : (d.bitsin ? d.bitsin + '-bit' : '')]
                .filter(Boolean).join(' · '));
            var o = p.output || {};
            // a reduced stream's encoder states a bitrate or a quality, not a depth
            var oq = o.kbps ? o.kbps + ' kbps' : (o.quality !== undefined && o.quality !== null ? 'q' + o.quality : (o.bits || 24) + '-bit');
            row('out', o.format ? [o.format + ' ' + oq, rate(o.rate)].filter(Boolean).join(' · ') : '');
            row('stages', (p.stages || []).map(function (s) {
                return (ROLE_NAMES[s.role] || s.role) + ' ' + (s.speed !== undefined && s.speed !== null ? speedText(s.speed) + 'x' : '-');
            }).join(' · '));
        } else {
            ['up', 'dsp', 'out', 'stages'].forEach(function (n) { row(n, ''); });
        }
    }

    // ---------------------------------------------------------------- presets
    function selectedPreset() {
        var v = UI && UI.presetSel.value;
        for (var i = 0; i < PRESETS.length; i++) { if (PRESETS[i].path === v) { return PRESETS[i]; } }
        return null;
    }

    function loadPreset() {
        var p = selectedPreset(), pid = PID;
        if (!p || !pid) { return; }
        rpc(pid, ['squeezedsp.readpresetSettings', 'presetFileName:' + p.path], 15000).then(function (r) {
            if (pid !== PID) { return; }
            var loaded = {};
            try { loaded = JSON.parse(r.json || '{}'); } catch (e) { loaded = {}; }
            DATA = merge({ Client: defaults() }, loaded);
            DATA.ClientName = r.clientName || DATA.ClientName;
            DATA.Revision = r.revision || DATA.Revision || 1;
            PENDING = {};
            // as the classic page does: remember it, and save, which re-opens the stream through it
            change('Last_preset', p.path);
        }).catch(function (e) {
            var el = $('err');
            if (el) { el.textContent = e.message; }
        });
    }

    function savePreset() {
        var name = UI ? UI.presetName.value.trim() : '';
        var pid = PID;
        if (!name || /[\\/]/.test(name) || !pid || !DATA) { return; }
        DATA.Client.Last_preset = name;
        GEN++;
        save().then(function (ok) {
            if (!ok || pid !== PID) { return; }
            return rpc(pid, ['squeezedsp.saveas', 'preset:' + name]).then(function () {
                UI.presetName.value = '';
                NEED.presets = true;
                NEED.settings = true;
                schedule(0);
            });
        }).catch(function (e) {
            var el = $('err');
            if (el) { el.textContent = e.message; }
        });
    }

    function deletePreset() {
        var p = selectedPreset(), pid = PID;
        if (!p || !pid) { return; }
        if (!window.confirm(fmt(L.delAsk || 'Delete the preset %s?', p.name))) { return; }
        rpc(pid, ['squeezedsp.deletepreset', 'preset:' + p.path]).then(function () {
            NEED.presets = true;
            NEED.settings = true;
            schedule(0);
        }).catch(function (e) {
            var el = $('err');
            if (el) { el.textContent = e.message; }
        });
    }

    // ---------------------------------------------------------------- start
    // Back to Material: only when standalone. Framed, Material's own dialog has its close, and
    // the only way out of a frame (window.top.location) would throw the whole app away.
    var back = $('back');
    var framed = false;
    try { framed = window.top !== window.self; } catch (e) { framed = true; }
    if (back) {
        if (framed) {
            back.parentNode.removeChild(back);
        } else {
            back.textContent = L.back || 'Back';
            back.addEventListener('click', function () {
                window.close();
                setTimeout(function () { window.location.href = '/material/'; }, 80);
            });
        }
    }

    build();
    poll();

    // for the test harness only: what the page holds
    window.__sqdsp = function () {
        return { pid: PID, players: PLAYERS, can: CAN, data: DATA, status: STATUS, presets: PRESETS, pending: PENDING, gen: GEN };
    };
}());
