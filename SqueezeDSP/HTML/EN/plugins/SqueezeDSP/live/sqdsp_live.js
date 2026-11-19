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
    // an element's help on hover (none when empty): shown by the page's own box (tipStart), never the browser's title
    function tip(el, text) {
        if (text) { el.dataset.tip = text; } else { delete el.dataset.tip; }
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
            Bypass: 1, ExactLength: 0, OutputFormat: 'FLAC', Upsample: 'off', DSDCutoff: 30000, DSDGain: 0,
            UpsampleDSDRate: 2822400, UpsampleFilterCD: 'short-int', UpsampleFilterHiRes: 'short-lin', DSDOut: 0, DSDOutMod: 'standard',
            DSDOutDSP: 352800, DSDIntegrator: 'lin', UpsamplePCMFilterCD: 'short-int', UpsamplePCMFilterHiRes: 'short-lin',
            UpsampleTarget: 'family', UpsampleMax: 0, Preamp: 0, Balance: 0, Width: 0,
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
    var NEED = { players: true, settings: true, presets: true, logs: true };
    var T = { players: 0, settings: 0, presets: 0, logs: 0 };
    var PEAKS = [];         // the player's last tracks' output peaks, oldest first (squeezedsp.logsummary): { v: dBFS,
                            // dsd: DSF out, held: its samples held at the limit }
    var DSD_0DB = 6.02;     // DSD's 0 dB (50% modulation) over its full swing
    var UI = null;          // the built controls

    // a DSF on our engine's own decoder (0.3.13): its low-pass (Hz, 0 none) and gain (dB), the player's DSDCutoff and DSDGain
    var DSD_CUTOFFS = [24000, 30000, 50000, 0];
    var DSD_GAINS = [0, 6];
    // DSD out: the converter's DSD rates (the player's UpsampleDSDRate; DSD to DSD keeps the source's own rate), the
    // modulators (DSDOutMod) and the DSP's rate in between (DSDOutDSP)
    var DSD_RATES = [2822400, 5644800, 11289600, 22579200];
    var DSD_RATE_NAMES = { 2822400: 'DSD64', 5644800: 'DSD128', 11289600: 'DSD256', 22579200: 'DSD512' };
    var DSD_MODS = ['standard', 'clans', 'adaptive'];
    // the engine's filters (Upsample.pm @FILTERS, the engine's --upfilter), one for a source at 48 kHz and under and one
    // for hi-res; the converter to DSD's own (0.1.72.33) and PCM's own (0.1.72.37; Simon 2026-11-04: "Separate for PCM"),
    // with the defaults Simon asked for ("defaults set to what we consider best": ledger STAGE 1 STUDY). Since 0.1.72.44
    // the filter family, one dropdown each (Simon 2026-11-06: "choose the filters from a drop down ... the name, lenght and
    // phase"), in the engine's order: [value, family, length, phase]; Kaiser's values as before. Each length's phases
    // linear to minimum, the linear-leaning intermediates (80% and 70% linear, 2026-11-15) between
    var FILTERS = (function () {
        var out = [];
        [['', 'kaiser', ['short', 'long', 'xlong']], ['gauss-', 'gauss', ['short', 'medium', 'long']],
         ['halfband-', 'halfband', ['']], ['slow-', 'slow', ['']]].forEach(function (f) {
            f[2].forEach(function (l) {
                ['lin', 'int80', 'int70', 'int', 'min'].forEach(function (p) { out.push([f[0] + (l ? l + '-' : '') + p, f[1], l, p]); });
            });
        });
        out.push(['nonring', 'nonring', '', '']);
        return out;
    })();
    var FILTER_PHASE_NAMES = { lin: 'Linear', int80: 'Intermediate 80%', int70: 'Intermediate 70%', int: 'Intermediate', min: 'Minimum' };
    var FILTER_DEFAULTS = { UpsampleFilterCD: 'short-int', UpsampleFilterHiRes: 'short-lin',
                            UpsamplePCMFilterCD: 'short-int', UpsamplePCMFilterHiRes: 'short-lin' };
    // the settings' tabs, in Simon's order (2026-11-03: "Presets first then DSP, then the rest as they are")
    var TABS = ['presets', 'dsp', 'src', 'pcm', 'dsd', 'peq', 'conv', 'xfeed'];
    var TAB = null;         // the tab shown
    // DSD out's DSP rate (DSDOutDSP): DXD, 176.4k, or native, a DSF's own DSD rate (Configuration::dsdNative); native's
    // integrator (DSDIntegrator), the engine's names
    var DSD_DSP_RATES = ['352800', '176400', 'native'];
    var DSD_INTEGRATORS = ['lin', 'int', 'min', 'nonring'];
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
        PID = id; CAN = null; DATA = null; STATUS = null; PRESETS = []; PENDING = {}; PEAKS = [];
        NEED.settings = true; NEED.presets = true; NEED.logs = true;
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
                // a reduced stream (MP3, OPS, OGG), and the Bitrate Limiting an MP3 is at (0: an app's 320 kbps)
                outReduced: r.out_reduced || '',
                outKbps: num(r.out_kbps, 0),
                // native DSD out: a player that lists dsf itself, its output not a reduced stream (an older server: none)
                dsdOutOk: num(r.dsd_out_ok, 0) === 1,
                // the Sample Rate Converter to DSD: DSD out, and the resampler (an older server: none)
                upDsdOk: num(r.up_dsd_ok, 0) === 1,
                // LMS's "Prefer native format", which SqueezeDSP needs off (an older server: none)
                preferNative: num(r.prefer_native, 0) === 1
            };
            renderNative();
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

    // the engine's end-of-track lines for this player (UI_Functions::logsummaryQuery: up to 10, oldest first): each
    // finished track's output peak, which the clipping row shows (the classic page's peak bars)
    function loadLogs() {
        var pid = PID;
        return rpc(pid, ['squeezedsp.logsummary']).then(function (r) {
            if (pid !== PID) { return; }
            var n = num(r.trackcount, 0), out = [];
            for (var i = 1; i <= n; i++) {
                var v = num(r['peakdBfs_' + i], NaN);
                if (!isFinite(v)) { continue; }
                // a DSF track's peak is against DSD's full swing: read against DSD's 0 dB (50%, where PCM full scale
                // goes: TemplateConfig's --dsdoutgain=-6.02), as the same track on PCM out would read
                var dsd = /^dsf$/i.test(String(r['outtype_' + i] || ''));
                out.push({ v: dsd ? v + DSD_0DB : v, dsd: dsd, held: dsd ? num(r['held_' + i], 0) : 0 });
            }
            PEAKS = out;
            renderStatus();
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
                 ['presets', 60000, function () { return loadPresets(); }],
                 ['logs', 15000, function () { return loadLogs(); }]];

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

        // LMS's "Prefer native format" on: a red bar above everything, on every tab, with a button that turns it off
        // through LMS's own `pref` command (Simon 2026-11-08); shown only while the server says it is on
        UI.native = make('div', 'card nativebar hide');
        UI.native.appendChild(make('span', 'nativebar-t', L.preferNative || '"Prefer native format" is on in LMS\'s File Types settings. SqueezeDSP needs it off.'));
        UI.nativeOff = UI.native.appendChild(make('button', 'btn nativebar-b', L.preferNativeOff || 'Turn it off'));
        UI.nativeOff.type = 'button';
        UI.nativeOff.addEventListener('click', nativeOff);
        app.appendChild(UI.native);

        // DSP on or off, first, so every tab has it (Simon 2026-11-05: "The DSP on button needs to move to top of UI so
        // its availble on each page like the classic menu"); its settings stay on the DSP tab
        var db = make('div', 'card dspbar');
        UI.dsp = control(db, 'dsp', L.dsp || 'DSP', L.dspHelp);
        UI.dspSw = UI.dsp.c.appendChild(switchButton(function (on) { change('Bypass', on ? 0 : 1); }));
        // "Exact track length" (the player's own, Simon 2026-11-15): beside the DSP switch, dimmed while the DSP is off
        UI.exact = control(db, 'exact', L.exact || 'Exact track length', L.exactHelp);
        UI.exactSw = UI.exact.c.appendChild(switchButton(function (on) { change('ExactLength', on ? 1 : 0); }));
        app.appendChild(db);

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
        st.appendChild(sp);
        UI.statusNote = st.appendChild(make('div', 'note'));
        var rows = make('div', 'rows');
        UI.rows = {};
        [['source', L.source || 'Source'], ['up', L.tabSrc || 'SRC'], ['dsp', L.dsp || 'DSP'],
         ['out', L.output || 'Output'], ['stages', L.stages || 'Stages'], ['clip', L.clipping || 'Clipping']].forEach(function (r) {
            var row = make('div', 'row hide');
            row.appendChild(make('div', 'k', r[1]));
            var v = row.appendChild(make('div', 'v'));
            rows.appendChild(row);
            UI.rows[r[0]] = { row: row, v: v };
        });
        st.appendChild(rows);
        app.appendChild(st);

        // the settings, a tab per category (Simon 2026-11-03: "for this view we need tabs per settings category",
        // "Presets first then DSP, then the rest as they are"); the last one used comes back
        var bar = make('div', 'tabs');
        bar.setAttribute('role', 'tablist');
        UI.tabs = {};
        UI.panes = {};
        var names = { presets: L.presets || 'Presets', dsp: L.tabDsp || 'DSP', src: L.tabSrc || 'SRC', pcm: L.tabPcm || 'PCM',
                      dsd: L.tabDsd || 'DSD', peq: L.tabPeq || 'PEQ', conv: L.tabConv || 'Convolution', xfeed: L.tabXfeed || 'Crossfeed' };
        // the short names' full ones, on hover (Simon: "use SRC for SampleRateConverter and leave the others as PCM and DSD")
        var titles = { src: L.src, pcm: L.pcmProc, dsd: L.dsdProc };
        TABS.forEach(function (t) {
            var b = make('button', 'tab', names[t]);
            b.type = 'button';
            b.setAttribute('role', 'tab');
            b.dataset.t = t;
            tip(b, titles[t]);
            b.addEventListener('click', function () { showTab(t); });
            bar.appendChild(b);
            UI.tabs[t] = b;
        });
        app.appendChild(bar);
        function pane(key, title, help) {
            var p = make('div', 'card tabpane');
            p.dataset.t = key;
            var h = p.appendChild(make('h2', null, title));
            tip(h, help);
            app.appendChild(p);
            UI.panes[key] = p;
            return p;
        }

        // Presets
        var pc = pane('presets', L.presets || 'Presets');
        pc.classList.add('presets');
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

        // DSP: its settings (on or off is at the top; the rest still on the classic page)
        var cc = pane('dsp', L.dsp || 'DSP', L.dspHelp);
        cc.classList.add('controls');
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
        classicLink(cc, L.dspClassic || 'ReplayGain, delay and loudness are on the classic page for now.');

        // Sample Rate Converter: off, PCM (its target and max rate) or DSD (its rate)
        var sc = pane('src', L.src || 'Sample Rate Converter', L.srcHelp);
        sc.classList.add('controls');
        UI.srcOffNote = sc.appendChild(make('div', 'note dspoffnote'));
        UI.src = control(sc, 'src', L.convertTo || 'Convert to', L.srcHelp);
        UI.srcSeg = segment(UI.src.c, [['off', L.off || 'Off'], ['pcm', 'PCM'], ['dsd', 'DSD']], function (v) {
            change('Upsample', v === 'pcm' ? 'on' : v);
        });
        UI.upOpts = sc.appendChild(make('div', 'upopts'));
        UI.target = control(UI.upOpts, 'target', L.target || 'Target rate', null, true);
        UI.targetSeg = segment(UI.target.c, TARGETS.map(function (t) { return [t, TARGET_NAMES[t]]; }),
                               function (v) { change('UpsampleTarget', v); });
        UI.max = control(UI.upOpts, 'max', L.max || 'Max rate', null, true);
        UI.maxSeg = segment(UI.max.c, MAXRATES.map(function (m) { return [String(m), m ? rate(m) : (L.maxNone || 'No limit')]; }),
                            function (v) { change('UpsampleMax', Number(v)); });
        // its filters (0.1.72.37, PCM's own): the engine takes every track to its target itself, up or down, by the one for
        // the track's rate; with the converter's PCM options, as DSD's are with its DSD ones (Simon 2026-11-05: "PCM
        // filters should also be on SRC and show when in PCM mode, Like they do for DSD")
        UI.filters = {};
        filterGroups(UI.upOpts, [['UpsamplePCMFilterCD', L.filterCd || 'Filter, 44.1/48 kHz sources'],
                                 ['UpsamplePCMFilterHiRes', L.filterHiRes || 'Filter, hi-res sources']], true);
        UI.dsdOpts = sc.appendChild(make('div', 'dsdopts'));
        UI.dsdRate = control(UI.dsdOpts, 'dsdrate', L.dsdRate || 'DSD rate', null, true);
        UI.dsdRateSeg = segment(UI.dsdRate.c, DSD_RATES.map(function (r) { return [String(r), DSD_RATE_NAMES[r]]; }),
                                function (v) { change('UpsampleDSDRate', Number(v)); });
        // its filters: the engine takes every PCM track to 705.6k itself, by the one for the track's rate
        filterGroups(UI.dsdOpts, [['UpsampleFilterCD', L.filterCd || 'Filter, 44.1/48 kHz sources'],
                                  ['UpsampleFilterHiRes', L.filterHiRes || 'Filter, hi-res sources']], true);

        // PCM Processing: the output (its resampler's filters are the converter's, on the SRC tab)
        var pp = pane('pcm', L.pcmProc || 'PCM Processing Settings', L.pcmProcHelp);
        pp.classList.add('controls');
        UI.pcmOffNote = pp.appendChild(make('div', 'note dspoffnote'));
        UI.out = control(pp, 'output', L.output || 'Output', L.outputHelp);
        UI.outSeg = segment(UI.out.c, [['FLAC', 'FLAC 24-bit'], ['PCM', 'PCM (WAV 24-bit)']],
                            function (v) { change('OutputFormat', v); });
        tip(UI.outSeg.PCM, L.outputWavHelp);   // WAV's 4 GB limit, on the WAV button

        // DSD Processing: the output (with the converter off), the decoder, the encoder
        var dp = pane('dsd', L.dsdProc || 'DSD Processing Settings', L.dsdProcHelp);
        dp.classList.add('controls');
        UI.dsdOffNote = dp.appendChild(make('div', 'note dspoffnote'));
        UI.dsdOutput = control(dp, 'dsdoutput', L.output || 'Output');
        UI.dsdOutputSeg = segment(UI.dsdOutput.c, [['pcm', L.dsdToPcm || 'DSD to PCM'], ['dsd', L.dsdToDsd || 'DSD to DSD']],
                                  function (v) { change('DSDOut', v === 'dsd' ? 1 : 0); });
        dp.appendChild(make('div', 'grp', L.decoder || 'Decoder'));
        UI.dsdCut = control(dp, 'dsdcutoff', L.dsdCutoff || 'Low-pass');
        UI.dsdCutSeg = segment(UI.dsdCut.c, DSD_CUTOFFS.map(function (h) { return [String(h), h ? rate(h) : (L.dsdNone || 'None')]; }),
                               function (v) { change('DSDCutoff', Number(v)); });
        UI.dsdGain = control(dp, 'dsdgain', L.dsdGain || 'Gain');
        UI.dsdGainSeg = segment(UI.dsdGain.c, DSD_GAINS.map(function (g) { return [String(g), (g > 0 ? '+' : '') + g + ' dB']; }),
                                function (v) { change('DSDGain', Number(v)); });
        UI.dsdInt = control(dp, 'dsdintegrator', L.dsdIntegrator || 'Integrator');
        UI.dsdIntSeg = segment(UI.dsdInt.c, DSD_INTEGRATORS.map(function (i) { return [i, integratorName(i)]; }),
                               function (v) { change('DSDIntegrator', v); });
        dp.appendChild(make('div', 'grp', L.encoder || 'Encoder'));
        UI.dsdMod = control(dp, 'dsdoutmod', L.dsdOutMod || 'Modulator');
        UI.dsdModSeg = segment(UI.dsdMod.c, [['standard', L.dsdOutStandard || 'Standard'], ['clans', 'CLANS'],
                                             ['adaptive', L.dsdOutAdaptive || 'Adaptive']],
                               function (v) { change('DSDOutMod', v); });
        UI.dsdDsp = control(dp, 'dsdoutdsp', L.dsdOutDsp || 'DSP rate');
        UI.dsdDspSeg = segment(UI.dsdDsp.c, DSD_DSP_RATES.map(function (r) {
                                   return [r, r === '352800' ? 'DXD 352.8 kHz' : r === 'native' ? (L.dsdNative || 'Native') : rate(r)];
                               }), function (v) { change('DSDOutDSP', v === 'native' ? v : Number(v)); });

        // PEQ, Convolution, Crossfeed: still on the classic page (Simon 2026-11-03: "PEQ and Filters each have their own
        // tabs as PEQ and Convolution", "as would crossfeed")
        [['peq', L.tabPeq || 'PEQ', L.peqClassic], ['conv', L.tabConv || 'Convolution', L.convClassic],
         ['xfeed', L.tabXfeed || 'Crossfeed', L.xfeedClassic]].forEach(function (t) {
            classicLink(pane(t[0], t[1]), t[2] || '');
        });

        // the rest is on the classic page
        var more = make('div', 'more');
        var a = make('a', null, L.more || 'Equaliser, filters and the rest are on the full settings page.');
        a.href = '/plugins/SqueezeDSP/index.html?player=' + encodeURIComponent(PID);
        more.appendChild(a);
        app.appendChild(more);

        UI.dsp.row.classList.add('sw-row');
        UI.exact.row.classList.add('sw-row');
        var saved = null;
        try { saved = window.localStorage.getItem('sqdsp-tab'); } catch (e) { saved = null; }
        showTab(TAB || saved || 'presets');
        renderControls();
        renderPresets();
        renderStatus();
    }

    // A filter choice per group (the engine's --upfilter): one dropdown of the filters, grouped by family, labelled by
    // the group. sub: the rows as sub-rows (the converter's options).
    function filterGroups(parent, groups, sub) {
        groups.forEach(function (g) {
            var key = g[0], row = control(parent, key, g[1], null, sub), sel = row.c.appendChild(make('select', 'filter'));
            var group = null;
            sel.setAttribute('aria-label', g[1]);
            FILTERS.forEach(function (f) {
                if (!group || group.dataset.f !== f[1]) {
                    group = sel.appendChild(make('optgroup'));
                    group.dataset.f = f[1];
                    group.label = familyName(f[1]);
                }
                var o = group.appendChild(make('option', null, filterName(f[0])));
                o.value = f[0];
            });
            sel.addEventListener('change', function () { change(key, sel.value); });
            UI.filters[key] = sel;
        });
    }

    function familyName(f) {
        return { kaiser: L.filterKaiser || 'Kaiser', gauss: L.filterGauss || 'Gaussian', halfband: L.filterHalfBand || 'Half-band',
                 slow: L.filterSlow || 'Slow roll-off', nonring: L.filterNonRinging || 'Non-ringing' }[f] || f;
    }

    // a filter as the page names it: "Kaiser · Short · Linear", "Non-ringing"
    function filterName(v) {
        for (var i = 0; i < FILTERS.length; i++) {
            var f = FILTERS[i];
            if (f[0] !== v) { continue; }
            var len = { short: L.filterShort || 'Short', medium: L.filterMedium || 'Medium', long: L.filterLong || 'Long',
                        xlong: L.filterXLong || 'Extra-long' }[f[2]];
            return [familyName(f[1]), len, FILTER_PHASE_NAMES[f[3]]].filter(Boolean).join(' · ');
        }
        return v;
    }

    function nameOf(id) {
        for (var i = 0; i < PLAYERS.length; i++) { if (PLAYERS[i].id === id) { return PLAYERS[i].name; } }
        return id;
    }

    // one tab shown, the last one used remembered in this browser
    function showTab(key) {
        if (!UI || !UI.panes[key]) { key = 'presets'; }
        TAB = key;
        Object.keys(UI.panes).forEach(function (k) {
            UI.panes[k].classList.toggle('on', k === key);
            UI.tabs[k].setAttribute('aria-selected', k === key ? 'true' : 'false');
        });
        try { window.localStorage.setItem('sqdsp-tab', key); } catch (e) {}
    }

    // what is still on the classic page: said so, and a link to it
    function classicLink(parent, text) {
        var m = parent.appendChild(make('div', 'note classic'));
        if (text) { m.appendChild(make('span', null, text + ' ')); }
        var a = m.appendChild(make('a', null, L.openClassic || 'Open the classic page'));
        a.href = '/plugins/SqueezeDSP/index.html?player=' + encodeURIComponent(PID);
        return m;
    }

    function control(parent, key, label, help, sub) {
        var row = make('div', 'ctl' + (sub ? ' sub-ctl' : ''));
        row.dataset.k = key;
        var k = row.appendChild(make('div', 'k', label));
        tip(k, help);
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
        var tg = String(c.UpsampleTarget || '').toLowerCase();
        if (TARGETS.indexOf(tg) < 0) { tg = 'family'; }
        // the converter's mode as stored (Upsample::mode: 'on' PCM, 'dsd' DSD, else off) and as the server runs it: PCM
        // needs the stream-start hook and the player's top rate, DSD the player's DSD and the hook (dsdSpec; the engine
        // resamples for both, no soxr: 0.1.72.33, 0.1.72.37); and its DSD rate (Upsample::options): one of the four, in
        // digits alone, else DSD64
        var want = converter(c);
        var mode = (want === 'pcm' && can.upOk) ? 'pcm' : (want === 'dsd' && can.upDsdOk) ? 'dsd' : 'off';
        var dr = /^\d+$/.test(String(c.UpsampleDSDRate)) ? Number(c.UpsampleDSDRate) : 2822400;
        if (DSD_RATES.indexOf(dr) < 0) { dr = 2822400; }
        // the max rate: one the server knows, at or below the player's top rate for its output (up_limits:
        // FLAC out, WAV out); above it, the player's own
        var limits = String(can.upLimits || '').split(',');
        var limit = num(c.OutputFormat === 'PCM' && can.wavOk ? limits[1] : limits[0], 0);
        var mx = num(c.UpsampleMax, 0);
        if (MAXRATES.indexOf(mx) < 0 || (limit && mx > limit)) { mx = 0; }
        return { target: tg, max: mx, limit: limit, want: want, mode: mode, dsdrate: dr };
    }

    // an engine filter as the plugin reads it (Upsample::filters, pcmFilters): one of FILTERS, in any case, else its default
    function filterOf(c, key) {
        var v = String(c && c[key] != null ? c[key] : '').toLowerCase();
        return FILTERS.some(function (f) { return f[0] === v; }) ? v : FILTER_DEFAULTS[key];
    }

    // the Sample Rate Converter's mode as the plugin reads the player's Upsample (Upsample::mode)
    function converter(c) {
        var v = String(c.Upsample == null ? '' : c.Upsample).toLowerCase();
        return v === 'on' ? 'pcm' : v === 'dsd' ? 'dsd' : 'off';
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
        if (can.upWhy === 'nomax') { return L.noMax || ''; }
        if (can.upWhy === 'hook') { return L.noHook || ''; }
        return '';
    }

    // the red bar: shown while the server reports "Prefer native format" on
    function renderNative() {
        if (!UI || !UI.native) { return; }
        UI.native.classList.toggle('hide', !(CAN && CAN.preferNative));
        UI.nativeOff.disabled = false;
    }

    // its button: LMS's own `pref` command (no player), then the settings read again, so the bar goes once it is off
    function nativeOff() {
        if (!UI || !UI.nativeOff) { return; }
        UI.nativeOff.disabled = true;
        rpc('', ['pref', 'prioritizeNative', '0']).then(function () { return loadSettings(); },
            function () { return loadSettings(); }).then(renderNative, renderNative);
    }

    function renderControls() {
        if (!UI) { return; }
        renderNative();
        var c = client(), can = CAN;
        var ready = !!(c && can);
        Object.keys(UI.panes).forEach(function (k) { if (k !== 'presets') { UI.panes[k].classList.toggle('dimmed', !ready); } });
        if (!ready) {
            UI.dspSw.disabled = true;
            UI.exactSw.disabled = true;
            return;
        }
        var dspOn = String(c.Bypass) === '0';
        setSwitch(UI.dspSw, dspOn && can.dspOk, !can.dspOk);
        setSwitch(UI.exactSw, String(c.ExactLength) === '1', !can.dspOk);
        UI.exact.row.classList.toggle('dimmed', !(dspOn && can.dspOk));
        UI.dsp.note.textContent = can.dspOk ? '' : (L.noDsp || '');

        // the Sample Rate Converter as the server runs it (effective): one it cannot run shows as off, and says why
        var e = effective(c, can);
        var want = e.want, mode = e.mode;
        var noDsd = !can.dsdOutOk ? (can.outReduced ? (L.dsdOutNoReduced || '') : (L.dsdOutNoDsf || '')) : whyNoUpsample(can);
        setSegment(UI.srcSeg, mode, [].concat(can.upOk ? [] : ['pcm'], can.upDsdOk ? [] : ['dsd']));
        tip(UI.srcSeg.pcm, can.upOk ? '' : whyNoUpsample(can));
        tip(UI.srcSeg.dsd, can.upDsdOk ? '' : noDsd);
        UI.upOpts.classList.toggle('hide', mode !== 'pcm');
        UI.dsdOpts.classList.toggle('hide', mode !== 'dsd');
        var ur = upRates(e.limit, e.max), note = '';
        if (mode === 'pcm') {
            note = (e.target === 'fixed' && (ur || can.upFixed)) ? (L.fixed || '') + ' ' + rate(ur ? ur.top : can.upFixed)
                 : can.upTargets ? (L.targets || '') + ' ' + (ur ? [ur.f44, ur.f48] : can.upTargets.split(',')).map(rate).join(' / ') : '';
        }
        // the choice stored that cannot run here: why
        if (want === 'pcm' && mode !== 'pcm') { note = whyNoUpsample(can); }
        if (want === 'dsd' && mode !== 'dsd') { note = noDsd; }
        UI.src.note.textContent = note;
        setSegment(UI.targetSeg, e.target);
        setSegment(UI.maxSeg, String(e.max), MAXRATES.filter(function (m) { return m && e.limit && m > e.limit; }).map(String));
        setSegment(UI.dsdRateSeg, String(e.dsdrate));
        Object.keys(UI.filters).forEach(function (key) {
            if (document.activeElement !== UI.filters[key]) { UI.filters[key].value = filterOf(c, key); }
        });

        // PCM processing: the output (every track's but on the converter to DSD)
        var pcm = c.OutputFormat === 'PCM' && can.wavOk;
        setSegment(UI.outSeg, pcm ? 'PCM' : 'FLAC', can.wavOk ? [] : ['PCM']);
        // a reduced stream overrides the output chosen here while it is due (Bitrate Limiting, or the app's own list)
        UI.out.note.textContent = mode === 'dsd' ? ''
            : can.outReduced
            ? (can.outReduced === 'MP3' && can.outKbps ? fmt(L.reducedLimit || '%s', can.outKbps)
                                                      : fmt(L.reducedApp || '%s', REDUCED[can.outReduced] || can.outReduced))
            : (can.wavOk ? '' : (L.pcmNeeds || ''));
        UI.out.row.classList.toggle('overridden', mode === 'dsd');

        // DSD processing: the output, only with the converter off (Configuration::dsdOut); a player that cannot take DSD
        // gets PCM, and says why; the decoder; the encoder, as the plugin and the engine take them
        var dsdOutOn = can.dsdOutOk && dsdOut(c);
        setSegment(UI.dsdOutputSeg, dsdOutOn ? 'dsd' : 'pcm', can.dsdOutOk ? [] : ['dsd']);
        UI.dsdOutput.note.textContent = (mode !== 'off' || can.dsdOutOk) ? ''
            : (can.outReduced ? (L.dsdOutNoReduced || '') : (L.dsdOutNoDsf || ''));
        UI.dsdOutput.row.classList.toggle('overridden', mode !== 'off');
        setSegment(UI.dsdCutSeg, String(dsdCutoff(c)));
        setSegment(UI.dsdGainSeg, String(dsdGain(c)));
        setSegment(UI.dsdModSeg, dsdOutMod(c));
        setSegment(UI.dsdDspSeg, String(dsdOutDsp(c)));
        // the integrator: native's alone, greyed (still editable) with its why on the other DSP rates
        var native = dsdOutDsp(c) === 'native';
        setSegment(UI.dsdIntSeg, dsdIntegrator(c));
        UI.dsdInt.row.classList.toggle('dimmed', !native);
        UI.dsdInt.note.textContent = native ? '' : (L.dsdNativeOnly || 'With the DSP rate Native');

        // what DSP on drives: dimmed while it is off, still editable, and the tabs that hold it say so
        ['src', 'pcm', 'dsd'].forEach(function (k) { UI.panes[k].classList.toggle('dimmed', !dspOn); });
        [UI.srcOffNote, UI.pcmOffNote, UI.dsdOffNote].forEach(function (n) { n.textContent = dspOn ? '' : (L.dspOffTab || ''); });
        Object.keys(UI.sliders).forEach(function (k) { UI.sliders[k].ctl.row.classList.toggle('dimmed', !dspOn); });
        renderSliders();
        tipRefresh();   // a greyed button's why may have changed
    }

    // the DSD settings as the engine takes them (LyrionDSPSettings buildConfig): unset or out of range, its default
    function dsdCutoff(c) { var v = num(c.DSDCutoff, NaN); return (v === 0 || (v > 20000 && v <= 60000)) ? v : 30000; }
    function dsdGain(c) { var v = num(c.DSDGain, NaN); return (v >= -24 && v <= 12) ? v : 0; }
    // DSD out as the plugin takes it (Configuration::dsdOut: 1, on or true; dsdOutDSP: DXD unless 176400) and the engine
    // (LyrionDSPSettings: the modulator standard, clans or adaptive, spaces and quotes trimmed as the engine trims them,
    // else standard)
    function dsdOut(c) { return /^(?:1|on|true)$/i.test(String(c.DSDOut)); }
    function dsdOutMod(c) {
        var v = String(c.DSDOutMod == null ? '' : c.DSDOutMod).replace(/^[ "]+|[ "]+$/g, '').toLowerCase();
        return DSD_MODS.indexOf(v) >= 0 ? v : 'standard';
    }
    function dsdOutDsp(c) {
        return /^[ "]*native[ "]*$/i.test(String(c.DSDOutDSP)) ? 'native' : String(c.DSDOutDSP) === '176400' ? 176400 : 352800;
    }
    // native's integrator as the engine takes the player's DSDIntegrator (lin unless int, min or nonring, trimmed, any case)
    function dsdIntegrator(c) {
        var v = String(c.DSDIntegrator == null ? '' : c.DSDIntegrator).replace(/^[ "]+|[ "]+$/g, '').toLowerCase();
        return DSD_INTEGRATORS.indexOf(v) >= 0 ? v : 'lin';
    }
    function integratorName(i) {
        return { lin: L.dsdIntLin || 'Linear', int: L.dsdIntInt || 'Intermediate', min: L.dsdIntMin || 'Minimum',
                 nonring: L.filterNonRinging || 'Non-ringing' }[i] || i;
    }

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
        } else if (!p.running && !p.finished) {
            // nothing said of a track fully sent or of the next one started (Simon 2026-11-05: "this is not something a
            // user needs to see")
            note = mode === 'play' ? (L.notInPath || '') : '';
        }
        UI.statusNote.textContent = note;
        renderClip();
        tipRefresh();   // the clipping dots are new elements now

        // figures: a running pipeline's, or a finished one's last (none when the page never saw it)
        var figures = !!(STATUS && (p.running || p.finished) && p.stages);
        // the engine's own speed (Status _engineSpeed), the one figure on every platform: how many times faster than real
        // time it runs flat out, measured in the engine while it plays (0.1.72.46, its runs) or at its stream's start (its
        // fill). Never a process's audio per CPU-second, which is no speed (ledger THE SPEED SHOWN IS ONE START'S FILL).
        // The figure alone (Simon 2026-11-05: "remove all the text thats after the speed its not needed at all")
        // (without its stages: a server with no /proc, the engine's report alone, Status _reportOnly)
        var real = !!(STATUS && (p.running || p.finished)) && p.realtime !== undefined && p.realtime !== null;
        if (real) {
            UI.speed.textContent = fmt(L.realtime || '%sx realtime', speedText(p.realtime));
            UI.speed.className = 'speed-v';
        } else if (STATUS && p.running) {
            UI.speed.textContent = L.measuring || 'Measuring...';
            UI.speed.className = 'speed-v none';
        } else {
            UI.speed.textContent = '-';
            UI.speed.className = 'speed-v none';
        }

        var track = ahead ? p.next : now, src = [];
        if (track.type) { src.push(CODECS[track.type] || String(track.type).toUpperCase()); }
        if (track.rate) { src.push(rate(track.rate)); }
        if (track.bits) { src.push(track.bits + '-bit'); }
        row('source', (mode !== 'stop' || p.running) ? src.join(' · ') : '');

        if (figures) {
            var r = p.resampler;
            if (r && r.tool === 'engine') {
                // the converter to DSD (0.1.72.33): the engine took the track up itself, by the filter for its rate
                row('up', filterName(r.filter) + ' → ' + rate(r.target));
            } else if (r && r.tool === 'decoder') {
                // our engine's own DSD decode (0.3.13): its low-pass and gain, as the engine takes them (Status _dsdSettings)
                var dd = [L.dsdDecoder || 'Our decoder',
                          r.cutoff ? fmt(L.dsdLowpass || '%s low-pass', rate(r.cutoff)) : (L.dsdNoLowpass || 'no low-pass')];
                if (r.gain) { dd.push((r.gain > 0 ? '+' : '') + r.gain + ' dB'); }
                // native (the native round): its integrator, and the DSD rate it stays at
                if (r.native) { dd.splice(1, 0, L.dsdNative || 'Native', integratorName(r.integrator)); }
                row('up', fmt(L.dsdUp || 'DSD: %s', dd.join(' · ') + ' → ' + (r.native && DSD_RATE_NAMES[r.target] || rate(r.target))));
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
            // what the DSP works in: the engine decodes to 64-bit float and stays there on every route (its decoder,
            // upsampler, filters and encoder); what it READS (d.bitsin: the source's bits, or soxr's 32-bit float) is
            // not its precision, and the Source and SRC rows already name that
            row('dsp', [(r && r.native && DSD_RATE_NAMES[d.rate]) || rate(d.rate), '64-bit ' + (L.float || 'float')].filter(Boolean).join(' · '));
            var o = p.output || {};
            if (o.format === 'DSF') {
                // native DSD out: the engine writes the DSF, at the DSD rate and with the modulator it took (Status _dsdOut)
                var mods = { standard: L.dsdOutStandard || 'Standard', clans: 'CLANS', adaptive: L.dsdOutAdaptive || 'Adaptive' };
                row('out', ['DSF', DSD_RATE_NAMES[o.dsd] || rate(o.dsd), o.mod ? fmt(L.dsdOutModulator || '%s modulator', mods[o.mod] || o.mod) : '']
                    .filter(Boolean).join(' · '));
            } else {
                // a reduced stream's encoder states a bitrate or a quality, not a depth
                var oq = o.kbps ? o.kbps + ' kbps' : (o.quality !== undefined && o.quality !== null ? 'q' + o.quality : (o.bits || 24) + '-bit');
                row('out', o.format ? [o.format + ' ' + oq, rate(o.rate)].filter(Boolean).join(' · ') : '');
            }
            // each stage's cost: its share of the server's whole CPU, every core, at real time (Simon 2026-11-05: "it needs
            // to be overall cpu performance"); none without the audio or a count of the cores (no /proc: macOS, Windows).
            // Never an 'x' per CPU-second, which would read as a second, slower speed
            var cores = num(p.cores, 0), share = num(p.audio, 0) > 0 && cores > 0;
            var stages = share ? (p.stages || []).map(function (s) {
                return (ROLE_NAMES[s.role] || s.role) + ' ' + (100 * num(s.cpu, 0) / num(p.audio, 1) / cores).toFixed(1) + '%';
            }).join(' · ') : '';
            row('stages', stages ? stages + ' ' + (L.totalCpu || 'of total CPU at real time') : '');
        } else {
            ['up', 'dsp', 'out', 'stages'].forEach(function (n) { row(n, ''); });
        }
    }

    // The clipping row (Simon 2026-11-03: "also want the clipping indicator in signal path"): the last finished track's
    // output peak, and a dot for each of the last tracks (oldest first), coloured as the classic page colours its peak
    // bars (sqdsp_core.js LocalDisplayLogSummary): with ReplayGain on, under -12 too low, under -8 a bit low, under -3
    // fine, under 0 high, else clipped; with it off, under -5 too low, under -1 fine, up to +0.1 high, above clipped
    function peakClass(v, rg) {
        if (rg) { return v < -12 ? 'low' : v < -8 ? 'bitlow' : v < -3 ? 'ok' : v < 0 ? 'high' : 'clip'; }
        return v < -5 ? 'low' : v < -1 ? 'ok' : v <= 0.1 ? 'high' : 'clip';
    }
    // a track's colour: PCM's is peakClass's; a DSD track over its 0 dB is high, not clipped (the engine takes a PCM
    // source 2 dB past it), and clipped only where the engine held samples at its limit (0.1.72.46, Simon 2026-11-06)
    function trackClass(p, rg) {
        var cls = peakClass(p.v, rg);
        if (!p.dsd) { return cls; }
        return p.held > 0 ? 'clip' : cls === 'clip' ? 'high' : cls;
    }
    function renderClip() {
        var r = UI.rows.clip;
        r.v.textContent = '';
        r.row.classList.toggle('hide', !PEAKS.length);
        if (!PEAKS.length) { return; }
        var c = client() || {};
        var rg = /^(1|on|true)$/i.test(String((c.ReplayGain || {}).enabled));
        var last = PEAKS[PEAKS.length - 1], cls = trackClass(last, rg);
        var box = r.v.appendChild(make('span', 'clip'));
        box.appendChild(make('span', 'pk ' + cls));
        var db = (last.v > 0 ? '+' : '') + last.v.toFixed(1) + ' dBFS';
        box.appendChild(make('span', null, fmt(cls === 'clip' ? (L.clipYes || 'Clipped: last track\'s peak %s')
                                                              : (L.clipNone || 'None: last track\'s peak %s'), db)));
        var hist = box.appendChild(make('span', 'pkhist'));
        tip(hist, L.clipHistory);
        PEAKS.forEach(function (p) {
            var d = hist.appendChild(make('span', 'pk ' + trackClass(p, rg)));
            tip(d, (p.v > 0 ? '+' : '') + p.v.toFixed(1) + ' dBFS');
        });
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

    // ---------------------------------------------------------------- help on hover
    // Simon 2026-11-16: "Tool tips dont seem to be working in the new interface". The help was the browser's own title
    // tooltip, which never showed for him (in Material's frame); the classic page's, drawn by the page, does. So the page
    // draws this one too: one box, shown a moment after the pointer rests on an element with data-tip (tip()), under
    // it (above it near the window's foot), kept inside the window. A greyed button takes no pointer
    // (.seg button:disabled), so its why is found among the children of what is under the pointer, by position.
    var TIP = { box: null, el: null, quiet: null, x: -1, y: -1, timer: null };
    var TIP_DELAY = 200;

    function tipTarget(node, x, y) {
        if (!node || node.nodeType !== 1) { return null; }
        var el = node.closest('[data-tip]');
        if (el) { return el; }
        var kids = node.querySelectorAll('[data-tip]');
        for (var i = 0; i < kids.length; i++) {
            var r = kids[i].getBoundingClientRect();
            if (r.width && x >= r.left && x < r.right && y >= r.top && y < r.bottom) { return kids[i]; }
        }
        return null;
    }
    function tipHide() {
        clearTimeout(TIP.timer);
        TIP.el = null;
        if (TIP.box) { TIP.box.classList.remove('on'); }
    }
    function tipShow(el) {
        var b = TIP.box, m = 8;
        TIP.el = el;
        b.textContent = el.dataset.tip;
        b.classList.add('on');
        var r = el.getBoundingClientRect(), w = b.offsetWidth, h = b.offsetHeight;
        var top = r.bottom + 6;
        if (top + h > window.innerHeight - m) { top = Math.max(m, r.top - h - 6); }
        b.style.left = Math.max(m, Math.min(r.left, window.innerWidth - w - m)) + 'px';
        b.style.top = top + 'px';
    }
    function tipAt(node, x, y) {
        TIP.x = x; TIP.y = y;
        var el = tipTarget(node, x, y);
        if (el !== TIP.quiet) { TIP.quiet = null; }
        if (el && el === TIP.quiet) { return; }
        if (el === TIP.el) { return; }
        tipHide();
        if (!el) { return; }
        TIP.el = el;
        TIP.timer = setTimeout(function () { if (TIP.el === el && el.isConnected && el.dataset.tip) { tipShow(el); } }, TIP_DELAY);
    }
    // a redraw may have replaced what the pointer rests on, or changed its help: look again where it is
    function tipRefresh() {
        if (!TIP.box || TIP.x < 0) { return; }
        var on = TIP.box.classList.contains('on');
        var el = tipTarget(document.elementFromPoint(TIP.x, TIP.y), TIP.x, TIP.y);
        if (!el || el === TIP.quiet) { tipHide(); return; }
        if (on) { tipShow(el); return; }
        if (el !== TIP.el) { tipAt(el, TIP.x, TIP.y); }
    }
    function tipStart() {
        TIP.box = document.body.appendChild(make('div', 'tipbox'));
        TIP.box.setAttribute('role', 'tooltip');
        document.addEventListener('mouseover', function (e) { tipAt(e.target, e.clientX, e.clientY); });
        document.addEventListener('mousemove', function (e) { tipAt(e.target, e.clientX, e.clientY); });
        // out of the window: nothing under the pointer any more
        document.addEventListener('mouseout', function (e) { if (!e.relatedTarget) { TIP.x = -1; TIP.quiet = null; tipHide(); } });
        // a press puts it away, and it stays away until the pointer leaves that element
        document.addEventListener('mousedown', function (e) { TIP.quiet = tipTarget(e.target, e.clientX, e.clientY); tipHide(); }, true);
        window.addEventListener('scroll', tipHide, true);
        window.addEventListener('blur', tipHide);
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

    tipStart();
    build();
    poll();

    // for the test harness only: what the page holds
    window.__sqdsp = function () {
        return { pid: PID, players: PLAYERS, can: CAN, data: DATA, status: STATUS, presets: PRESETS, pending: PENDING, gen: GEN };
    };
}());
