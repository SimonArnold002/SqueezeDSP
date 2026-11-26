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
   choices save at once; sliders, number boxes, the equaliser's bands, Reset and a filter file's
   import wait for Apply (PENDING, by settings path), because every save re-opens the playing
   stream (UI_Functions::saveallCommand seeks 0.5 s on) and a slider moves many times.

   PARITY (0.1.72.57, Simon 2026-11-20: "We need to ensure interface parity"): every setting the
   classic page has is here, with its help on hover. The user picks one interface (the plugin's pref
   interface): the Classic interface button switches to the classic page for good. */
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
        // a line break in a classic help text (PRESET_HELP's <BR>) is a line break here, never shown as markup
        if (text) { el.dataset.tip = String(text).replace(/<br\s*\/?>/gi, '\n'); } else { delete el.dataset.tip; }
    }
    function has(o, k) { return Object.prototype.hasOwnProperty.call(o, k); }
    function clone(v) { return v === undefined ? undefined : JSON.parse(JSON.stringify(v)); }
    function truthy(v) { return /^(1|on|true)$/i.test(String(v)); }
    // a setting by its path in the player's settings ('Preamp', 'Delay.delay', 'ReplayGain.mode')
    function getPath(o, path) {
        var parts = String(path).split('.');
        for (var i = 0; i < parts.length; i++) {
            if (o === null || o === undefined || typeof o !== 'object') { return undefined; }
            o = o[parts[i]];
        }
        return o;
    }
    function setPath(o, path, v) {
        var parts = String(path).split('.');
        for (var i = 0; i < parts.length - 1; i++) {
            if (o[parts[i]] === null || typeof o[parts[i]] !== 'object' || Array.isArray(o[parts[i]])) { o[parts[i]] = {}; }
            o = o[parts[i]];
        }
        o[parts[parts.length - 1]] = v;
    }
    // a file's name from its path, either separator
    function baseName(p) { return String(p).split(/[\\/]/).pop(); }
    // a label the classic page writes with a colon after it ("PEQ Bands:", "Load FIR File:")
    function noColon(s) { return String(s == null ? '' : s).replace(/\s*:\s*$/, ''); }
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
            Bypass: 1, ExactLength: 1, OutputFormat: 'FLAC', Upsample: 'off', DSDCutoff: 30000, DSDGain: 0,
            UpsampleDSDRate: 2822400, UpsampleDSDSource: 'upsample', UpsampleFilterCD: 'short-int', UpsampleFilterHiRes: 'short-lin', DSDOut: 0, DSDOutMod: 'standard',
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
    var PENDING = {};       // changes waiting for Apply, by settings path ('Preamp', 'Delay.delay', 'Filters')
    var FIRS = [];          // the impulse files on the server [{ path, file }] (squeezedsp.filters FIRWavFile_loop)
    var BAND = null;        // the equaliser band the graph draws on its own (the classic page's currentFilterIndex)
    var FIR_PICK = null;    // a FIR just imported from AutoEQ, to select once the server's list holds it
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
    // what the converter to DSD does with a DSD file (the player's UpsampleDSDSource, Upsample::dsdSource): to its DSD rate,
    // at the file's own, or untouched
    var DSD_SOURCES = ['upsample', 'source', 'untouched'];
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
    // the equaliser's band types (the classic page's filterTypes), crossfeed's settings (its crossfeed-selector)
    var PEQ_TYPES = ['peak', 'lowshelf', 'highshelf', 'lowpass', 'highpass'];
    var XFEEDS = ['off', 'light', 'medium', 'strong', 'bonkers'];
    var XFEED_NAMES = { off: 'Off', light: 'Light', medium: 'Medium', strong: 'Strong', bonkers: 'Bonkers' };
    // what Reset keeps (the classic page's LocalClearSettings): how the player is fed, not its DSP
    var RESET_KEEP = ['OutputFormat', 'Upsample', 'UpsamplePCMFilterCD', 'UpsamplePCMFilterHiRes', 'UpsampleTarget', 'UpsampleMax',
                      'UpsampleDSDRate', 'UpsampleDSDSource', 'UpsampleFilterCD', 'UpsampleFilterHiRes', 'DSDCutoff', 'DSDGain', 'DSDOut', 'DSDOutMod',
                      'DSDOutDSP', 'DSDIntegrator', 'ExactLength'];
    var ROLE_NAMES = { 'flac-dec': 'FLAC decode', 'flac-enc': 'FLAC encode', soxr: 'soxr', soxrate: 'SoX rate',
                       level: 'level', dsp: 'DSP', sox: 'SoX', ffmpeg: 'ffmpeg', wavlength: 'WAV length', dsd: 'DSD decode',
                       'mp3-enc': 'MP3 encode', 'opus-enc': 'Opus encode', 'ogg-enc': 'Vorbis encode' };
    // a reduced stream an app asks for (readclientSettings out_reduced), at Mobile Transcode Rules' qualities
    var REDUCED = { MP3: 'MP3 (320 kbps)', OPS: 'Opus (256 kbps)', OGG: 'Ogg Vorbis (quality 10)' };
    var CODECS = { flc: 'FLAC', alc: 'ALAC', aif: 'AIFF', wav: 'WAV', mp3: 'MP3', aac: 'AAC', mp4: 'AAC', ogg: 'Ogg Vorbis',
                   ops: 'Opus', dsf: 'DSF', dff: 'DFF', wvp: 'WavPack', ape: 'APE', wma: 'WMA', mpc: 'Musepack' };

    function client() { return DATA ? DATA.Client : null; }
    function dirty() { return Object.keys(PENDING).length > 0; }
    // the player's settings as the page shows them: what is stored, with what waits for Apply over it
    function view() {
        var c = client();
        if (!c || !dirty()) { return c; }
        var v = clone(c);
        Object.keys(PENDING).forEach(function (k) { setPath(v, k, clone(PENDING[k])); });
        return v;
    }

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
    // the players by name (Simon 2026-11-25: "players in our UIs alphabetical order as they seem random"): LMS answers
    // `players` unsorted, in a Perl hash's order (Slim::Player::Client::clients = values %clientHash); any case, numbers as
    // numbers (Kitchen 9 before Kitchen 10), the id last so two of one name keep one order
    function byName(a, b) {
        return a.name.localeCompare(b.name, undefined, { sensitivity: 'base', numeric: true }) || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0);
    }
    function loadPlayers() {
        return rpc('', ['players', 0, 100]).then(function (r) {
            PLAYERS = (r.players_loop || []).filter(function (p) {
                return p.playerid && (p.isplayer === undefined || num(p.isplayer, 1) !== 0);
            }).map(function (p) {
                return { id: String(p.playerid), name: p.name || String(p.playerid), connected: num(p.connected, 1) === 1 };
            }).sort(byName);
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
                preferNative: num(r.prefer_native, 0) === 1,
                // the engine's debug log, every player's (squeezedsp.debuglog; an older server: none)
                debugLog: num(r.debug_log, 0) === 1
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
            // and the impulse files (the same answer: the classic page's LocalBuildLists)
            FIRS = (r.FIRWavFile_loop || []).map(function (p) {
                var path = String(p[0] !== undefined ? p[0] : p);
                return { path: path, file: baseName(path) };
            });
            renderPresets();
            renderFirs();
            // a FIR AutoEQ just imported: selected for this player once the list holds it (Simon 2026-11-20: loading it,
            // in place of the one there, "is to be expected")
            if (FIR_PICK && FIR_PICK.pid === pid) {
                var want = FIR_PICK.file.toLowerCase(), hit = null;
                FIRS.forEach(function (f) { if (f.file.toLowerCase() === want) { hit = f; } });
                if (hit) { FIR_PICK = null; change('FIRWavFile', hit.path); }
            }
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

    // Apply: what waits goes into the file and the file is saved; resolves true once saved (or nothing waited), false
    // when the save failed (its error on the page). Every Save button (saveRow), and the page's ways out before leaving.
    function applyPending() {
        var c = client();
        if (!c || !dirty()) { return Promise.resolve(true); }
        var was = clone(PENDING), pid = PID;
        Object.keys(PENDING).forEach(function (k) { setPath(c, k, clone(PENDING[k])); });
        PENDING = {};
        GEN++;
        renderControls();
        return save().then(function (ok) {
            // not saved (Simon 2026-11-25: users must be told settings will be lost): they wait again, said so, with Save
            // now to try again; one changed since keeps its newer value; another player picked meanwhile: not its settings
            if (!ok && PID === pid) {
                Object.keys(was).forEach(function (k) { if (!has(PENDING, k)) { PENDING[k] = was[k]; } });
                GEN++;
                renderControls();
            }
            return ok;
        });
    }

    // The page's own question (Simon 2026-11-25: "the UI needs to feedback and say settings will be lost or save now"):
    // drawn on the page, as its help is, since the browser's own shows nothing in LyrPlay. Resolves with the value of the
    // button pressed; Escape or a press beside the box is the last one (Cancel).
    function ask(text, choices) {
        return new Promise(function (resolve) {
            var back = document.body.appendChild(make('div', 'askback'));
            var box = back.appendChild(make('div', 'askbox'));
            box.setAttribute('role', 'alertdialog');
            box.setAttribute('aria-modal', 'true');
            box.appendChild(make('p', 'askmsg', text));
            var row = box.appendChild(make('div', 'askbtns'));
            var last = choices[choices.length - 1][0];
            function key(e) { if (e.key === 'Escape') { e.preventDefault(); done(last); } }
            function done(v) {
                document.removeEventListener('keydown', key, true);
                if (back.parentNode) { back.parentNode.removeChild(back); }
                resolve(v);
            }
            choices.forEach(function (c, i) {
                var b = row.appendChild(make('button', 'btn ' + (c[2] || (i === 0 ? 'primary' : '')), c[1]));
                b.type = 'button';
                b.dataset.v = c[0];
                b.addEventListener('click', function () { done(c[0]); });
            });
            back.addEventListener('click', function (e) { if (e.target === back) { done(last); } });
            document.addEventListener('keydown', key, true);
            row.firstChild.focus();
        });
    }

    // Leaving the page by one of its own ways out (the filter guide in Material's frame, Classic interface, Back) with
    // settings not saved: asked first, Save (red, as the rows' own) / Don't save / Cancel. go() runs at once when nothing waits, once saved,
    // or on Don't save; Cancel, or a save that failed (its error on the page), stays. LEAVING: a leave already answered,
    // so the browser's own question (beforeunload, below) is not asked on top.
    var LEAVING = false, ASKING = false;
    function leaveGuard(go) {
        if (ASKING) { return; }
        if (!dirty()) { go(); return; }
        ASKING = true;
        ask(L.leaveAsk || 'Your settings are not saved and will be lost.',
            [['save', L.apply || 'Save', 'save'], ['lose', L.dontSave || "Don't save"], ['cancel', L.cancel || 'Cancel']]).then(function (v) {
            if (v === 'save') {
                return applyPending().then(function (ok) { ASKING = false; if (ok) { go(); } });
            }
            ASKING = false;
            if (v === 'lose') { LEAVING = true; go(); }
        });
    }

    // a switch or a choice: saved at once (key: its settings path); what waits for Apply under that path goes
    function change(key, value) {
        var c = client();
        if (!c) { return; }
        setPath(c, key, value);
        delete PENDING[key];
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
        // Reset, on every tab (the classic page's, beside its DSP switch): every DSP setting back to a new player's,
        // waiting for Save as there; its Save beside it for what no tab's own Save holds (saveRow, below)
        var acts = db.appendChild(make('div', 'dspacts'));
        UI.reset = acts.appendChild(make('button', 'btn danger', L.reset || 'Reset'));
        UI.reset.type = 'button';
        UI.reset.addEventListener('click', resetAll);
        UI.saveRows = {};
        saveRow(acts, 'top');
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
        // the clipping row's Clear Log (the classic page's): the engine's log, every player's
        UI.clearLog = UI.rows.clip.row.appendChild(make('button', 'btn small', L.clearLog || 'Clear Log'));
        UI.clearLog.type = 'button';
        UI.clearLog.addEventListener('click', clearLog);
        st.appendChild(rows);
        // the engine's debug log (Simon 2026-11-20: a switch, not a hand edit of the engine's config): every player's,
        // saved at once
        UI.debug = control(st, 'debuglog', L.debugLog || 'Debug log', L.debugLogHelp);
        UI.debugSw = UI.debug.c.appendChild(switchButton(setDebug));
        UI.debug.row.classList.add('sw-row');
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
        // the classic page's help for both rows
        tip(UI.presetSel, L.presetHelp);
        tip(UI.presetName, L.presetHelp);

        // DSP: its settings (on or off is at the top), the classic page's DSP tab in its order: preamp, ReplayGain (its
        // mode and gains shown while it is on), delay, balance, width, loudness (its level shown while it is on)
        var cc = pane('dsp', L.dsp || 'DSP', L.dspHelp);
        cc.classList.add('controls');
        UI.dspOffNote = cc.appendChild(make('div', 'note dspoffnote'));
        UI.sliders = {};
        UI.numbers = {};
        slider(cc, 'Preamp', L.preamp || 'Preamp (dB)', L.preampHelp, -30, 0, 0.1, ' dB', 0, false);
        UI.rg = control(cc, 'replaygain', L.replayGain || 'Replay Gain', L.replayGainHelp);
        UI.rgSw = UI.rg.c.appendChild(switchButton(function (on) { change('ReplayGain.enabled', on ? 1 : 0); }));
        UI.rg.row.classList.add('sw-row');
        UI.rgOpts = cc.appendChild(make('div', 'opts'));
        UI.rgMode = control(UI.rgOpts, 'rgmode', L.rgMode || 'Mode', null, true);
        UI.rgModeSeg = segment(UI.rgMode.c, [['3', L.rgSmart || 'Smart'], ['1', L.rgTrack || 'Track only'], ['2', L.rgAlbum || 'Album only']],
                               function (v) { change('ReplayGain.mode', Number(v)); });
        numberBox(UI.rgOpts, 'ReplayGain.fixed_gain', L.rgFixed || 'Fixed fallback gain (dB)', null, -20, 0, 0.1, -6);
        numberBox(UI.rgOpts, 'ReplayGain.spotify_gain', L.spotifyGain || 'Spotify Gain (dB)', L.spotifyGainHelp, -20, 0, 0.1, -4);
        // the classic page's own gesture: a double click puts delay, balance and width back to centre
        slider(cc, 'Delay.delay', L.delay || 'Delay (ms)', L.delayHelp, -20, 20, 0.1, '', 0, true);
        slider(cc, 'Balance', L.balance || 'Balance', L.balanceHelp, -12, 12, 0.1, '', 0, true);
        slider(cc, 'Width', L.width || 'Width', L.widthHelp, -12, 12, 0.1, '', 0, true);
        UI.loud = control(cc, 'loudness', L.loudness || 'Loudness', L.loudnessHelp);
        UI.loudSw = UI.loud.c.appendChild(switchButton(function (on) { change('Loudness.enabled', on ? 1 : 0); }));
        UI.loud.row.classList.add('sw-row');
        UI.loudOpts = cc.appendChild(make('div', 'opts'));
        slider(UI.loudOpts, 'Loudness.listening_level', L.loudLevel || 'Listening Level', L.loudLevelHelp, 50, 85, 0.1, '', 85, false, true);
        saveRow(cc, 'dsp');

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
        guideLink(UI.filters.UpsamplePCMFilterCD.parentNode, 'src');
        UI.dsdOpts = sc.appendChild(make('div', 'dsdopts'));
        UI.dsdRate = control(UI.dsdOpts, 'dsdrate', L.dsdRate || 'DSD rate', null, true);
        UI.dsdRateSeg = segment(UI.dsdRate.c, DSD_RATES.map(function (r) { return [String(r), DSD_RATE_NAMES[r]]; }),
                                function (v) { change('UpsampleDSDRate', Number(v)); });
        UI.dsdSrc = control(UI.dsdOpts, 'dsdsource', L.dsdSource || 'DSD sources', L.dsdSourceHelp, true);
        UI.dsdSrcSeg = segment(UI.dsdSrc.c, [['upsample', L.dsdSourceUp || 'Upsample'], ['source', L.dsdSourceOwn || 'Source rate'],
                                             ['untouched', L.dsdSourceLeave || 'Untouched']],
                               function (v) { change('UpsampleDSDSource', v); });
        // what each does, on its own button (Simon 2026-11-25); the row's help keeps the DSF caveat
        tip(UI.dsdSrcSeg.upsample, L.dsdSourceUpHelp);
        tip(UI.dsdSrcSeg.source, L.dsdSourceOwnHelp);
        tip(UI.dsdSrcSeg.untouched, L.dsdSourceLeaveHelp);
        // its filters: the engine takes every PCM track to 705.6k itself, by the one for the track's rate
        filterGroups(UI.dsdOpts, [['UpsampleFilterCD', L.filterCd || 'Filter, 44.1/48 kHz sources'],
                                  ['UpsampleFilterHiRes', L.filterHiRes || 'Filter, hi-res sources']], true);
        guideLink(UI.filters.UpsampleFilterCD.parentNode, 'src');

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
        guideLink(UI.dsdMod.c, 'mod');
        UI.dsdDsp = control(dp, 'dsdoutdsp', L.dsdOutDsp || 'DSP rate');
        UI.dsdDspSeg = segment(UI.dsdDsp.c, DSD_DSP_RATES.map(function (r) {
                                   return [r, r === '352800' ? 'DXD 352.8 kHz' : r === 'native' ? (L.dsdNative || 'Native') : rate(r)];
                               }), function (v) { change('DSDOutDSP', v === 'native' ? v : Number(v)); });

        // PEQ (the classic page's equaliser): the response graph (Fox's own plotter and filter maths, sqdsp_graph.js and
        // sqdsp_peq_lib.js, so both pages draw the same curve), a type and Add, the bands, a filter file's import and
        // AutoEQ's headphone search
        var pq = pane('peq', L.tabPeq || 'PEQ');
        pq.classList.add('controls', 'peq');
        UI.peqOffNote = pq.appendChild(make('div', 'note dspoffnote'));
        UI.graphBox = pq.appendChild(make('div', 'graph hide'));
        UI.graphBox.id = 'peqGraphContainer';     // the plotter reads its theme from this element ('dark')
        UI.graphBox.classList.toggle('dark', document.documentElement.className !== 'lt');
        UI.graph = UI.graphBox.appendChild(make('div'));
        UI.graph.id = 'peqGraph';
        var add = control(pq, 'peqadd', noColon(L.peqBands || 'PEQ Bands'), L.peqHelp);
        UI.addType = 'peak';
        UI.addSeg = segment(add.c, PEQ_TYPES.map(function (t) { return [t, typeName(t)]; }), function (v) {
            UI.addType = v;
            setSegment(UI.addSeg, v);
        });
        PEQ_TYPES.forEach(function (t) { tip(UI.addSeg[t], typeHelp(t)); });
        setSegment(UI.addSeg, UI.addType);
        UI.addBtn = add.c.appendChild(make('button', 'btn', L.add || 'Add'));
        UI.addBtn.type = 'button';
        UI.addBtn.addEventListener('click', addBand);
        UI.bands = pq.appendChild(make('div', 'bands'));
        saveRow(pq, 'peq');   // under the bands; an import's bands wait here too
        pq.appendChild(make('div', 'grp', L.importFilters || 'Import Filters'));
        UI.drop = pq.appendChild(make('div', 'drop'));
        UI.drop.appendChild(make('span', 'drop-t', L.dropZone || 'Drop a REW or FIR WAV file here'));
        UI.filePick = UI.drop.appendChild(make('input', 'hide'));
        UI.filePick.type = 'file';
        UI.filePick.accept = '.txt,.wav,text/plain,audio/wav';
        var choose = UI.drop.appendChild(make('button', 'btn', L.chooseFile || 'Choose REW or WAV File'));
        choose.type = 'button';
        choose.addEventListener('click', function () { UI.filePick.click(); });
        UI.filePick.addEventListener('change', function () {
            var files = UI.filePick.files;
            if (files && files.length > 1) { fileNote(L.fileOne || 'One file at a time'); }
            else if (files && files.length) { importFile(files[0]); }
            UI.filePick.value = '';
        });
        ['dragenter', 'dragover'].forEach(function (ev) {
            UI.drop.addEventListener(ev, function (e) { e.preventDefault(); e.stopPropagation(); UI.drop.classList.add('over'); });
        });
        UI.drop.addEventListener('dragleave', function () { UI.drop.classList.remove('over'); });
        UI.drop.addEventListener('drop', function (e) {
            e.preventDefault();
            e.stopPropagation();
            UI.drop.classList.remove('over');
            var files = e.dataTransfer && e.dataTransfer.files;
            if (files && files.length > 1) { fileNote(L.fileOne || 'One file at a time'); }
            else if (files && files.length) { importFile(files[0]); }
        });
        UI.fileNote = pq.appendChild(make('div', 'note filenote'));
        UI.aeq = control(pq, 'autoeq', [L.headphoneEq || 'Headphone', L.headphoneEqExp || 'EQ'].join(' '), L.headphoneEqHelp);
        UI.aeqInput = UI.aeq.c.appendChild(make('input', 'aeq'));
        UI.aeqInput.type = 'text';
        UI.aeqInput.autocomplete = 'off';
        UI.aeqInput.spellcheck = false;
        UI.aeqInput.placeholder = L.headphoneEqFind || 'Type headphone name (min. 2 chars)...';
        UI.aeqInput.setAttribute('aria-label', [L.headphoneEq || 'Headphone', L.headphoneEqExp || 'EQ'].join(' '));
        UI.aeqList = pq.appendChild(make('div', 'aeqlist hide'));
        UI.aeqInput.addEventListener('focus', function () { aeqIndex(); });
        UI.aeqInput.addEventListener('input', aeqSearchSoon);
        UI.aeqInput.addEventListener('keydown', function (e) { if (e.key === 'Escape') { aeqShow([]); } });

        // Convolution (the classic page's): the room-correction impulse, which saves at once, and its strength
        var cv = pane('conv', L.tabConv || 'Convolution', L.firHelp);
        cv.classList.add('controls');
        UI.convOffNote = cv.appendChild(make('div', 'note dspoffnote'));
        UI.fir = control(cv, 'fir', noColon(L.firFile || 'Load FIR File'), L.firHelp);
        UI.firSel = UI.fir.c.appendChild(make('select', 'filter'));
        UI.firSel.setAttribute('aria-label', noColon(L.firFile || 'Load FIR File'));
        UI.firSel.addEventListener('change', function () { change('FIRWavFile', UI.firSel.value); });
        slider(cv, 'FIRStrength', L.firStrength || 'FIR Strength', L.firStrengthHelp, 0, 100, 1, '', 100, false);
        saveRow(cv, 'conv');

        // Crossfeed (the classic page's): saves at once
        var xf = pane('xfeed', L.tabXfeed || 'Crossfeed', L.crossfeedHelp);
        xf.classList.add('controls');
        UI.xfOffNote = xf.appendChild(make('div', 'note dspoffnote'));
        UI.xf = control(xf, 'crossfeed', L.crossfeed || 'Crossfeed', L.crossfeedHelp);
        UI.xfSeg = segment(UI.xf.c, XFEEDS.map(function (x) { return [x, XFEED_NAMES[x]]; }), function (v) { change('Crossfeed', v); });

        // support: Fox's Ko-fi and Simon's Buy Me a Coffee (Simon 2026-11-20), plain links, no outside script
        var sup = make('div', 'support');
        [['https://ko-fi.com/Y8Y31UOE3D', 'Ko-fi (Foxenfurter)'], ['https://buymeacoffee.com/crystalgipsy', 'Buy me a coffee (CrystalGipsy)']].forEach(function (l) {
            var a = sup.appendChild(make('a', 'btn', l[1]));
            a.href = l[0];
            a.target = '_blank';
            a.rel = 'noopener';
        });
        app.appendChild(sup);

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

    // the filter guide (filters.html, 0.1.72.59; Simon 2026-11-23): a plain link beside the choice, at the part it is
    // about ('src', 'mod'); the version in its address, as the page's own files. Framed, in the frame (Simon 2026-11-25:
    // "It should just open in its iframe"): Material's dialog takes a plain link's click into its own frame and its back
    // arrow returns here, so a new tab there opened a second window as well. A new tab only when the page has the window.
    // Framed with settings not saved (Simon 2026-11-25: "when moving to guide ... ask user to save before moving"): the
    // click waits, Material's handler too, for the page's question (leaveGuard); answered, the same click again, which
    // Material and the browser then take. Standalone the guide is a new tab and the page stays: nothing is asked.
    function guideLink(parent, part) {
        var a = parent.appendChild(make('a', 'guide', L.filterGuide || 'Filter guide'));
        a.href = '/plugins/SqueezeDSP/filters.html?v=' + encodeURIComponent(CFG.version || '') + '#' + part;
        if (!inFrame()) { a.target = '_blank'; }
        a.rel = 'noopener';
        a.addEventListener('click', function (e) {
            if (a.dataset.go) { delete a.dataset.go; return; }   // the click sent once answered: let it through
            if (!inFrame() || (!dirty() && !ASKING)) { return; }
            e.preventDefault();
            e.stopPropagation();   // Material listens on the document: it waits for the answer as well
            leaveGuard(function () { if (a.isConnected) { a.dataset.go = '1'; a.click(); } });
        });
        return a;
    }

    // inside another page's frame (Material's dialog); a frame of another origin throws on window.top: framed too
    function inFrame() {
        try { return window.top !== window.self; } catch (e) { return true; }
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
        if (key === 'peq') { drawGraphSoon(); }    // the graph has a size only while its tab shows
    }

    // a slider with its number box (the classic page's pair), both waiting for Apply. key: its settings path; dflt: its
    // value when the settings hold none; centre: a double click puts it back to 0; sub: a sub-row
    function slider(parent, key, label, help, min, max, step, unit, dflt, centre, sub) {
        var ctl = control(parent, key, label, help, sub);
        var range = make('input', 'range');
        range.type = 'range'; range.min = min; range.max = max; range.step = String(step);
        range.setAttribute('aria-label', label);
        var val = make('span', 'val');
        var box = val.appendChild(make('input', 'num'));
        box.type = 'number'; box.min = min; box.max = max; box.step = String(step);
        box.setAttribute('aria-label', label);
        if (unit) { val.appendChild(make('span', 'unit', unit)); }
        ctl.c.appendChild(range);
        ctl.c.appendChild(val);
        var s = { ctl: ctl, range: range, val: val, box: box, unit: unit, min: min, max: max, step: step, dflt: dflt };
        function pend(v) {
            PENDING[key] = clampStep(v, s);
            renderSliders();
            if (key === 'Loudness.listening_level') { drawGraphSoon(); }
        }
        range.addEventListener('input', function () { pend(range.value); });
        // a typed value, once committed, shown as kept (held to its range and step) though the box still has the focus
        box.addEventListener('change', function () {
            if (box.value === '') {
                var was = has(PENDING, key) ? PENDING[key] : num(getPath(client() || {}, key), dflt);
                box.value = Number(was).toFixed(decimals(step));
                return;
            }
            pend(box.value);
            box.value = PENDING[key].toFixed(decimals(step));
        });
        if (centre) { range.addEventListener('dblclick', function () { range.value = 0; pend(0); }); }
        UI.sliders[key] = s;
        return s;
    }

    // a number box alone (ReplayGain's gains, as on the classic page), waiting for Apply
    function numberBox(parent, key, label, help, min, max, step, dflt) {
        var ctl = control(parent, key, label, help, true);
        var val = ctl.c.appendChild(make('span', 'val'));
        var box = val.appendChild(make('input', 'num'));
        box.type = 'number'; box.min = min; box.max = max; box.step = String(step);
        box.setAttribute('aria-label', label);
        var s = { ctl: ctl, val: val, box: box, min: min, max: max, step: step, dflt: dflt };
        box.addEventListener('change', function () {
            if (box.value === '') {
                var was = has(PENDING, key) ? PENDING[key] : num(getPath(client() || {}, key), dflt);
                box.value = Number(was).toFixed(decimals(step));
                return;
            }
            PENDING[key] = clampStep(box.value, s);
            renderSliders();
            box.value = PENDING[key].toFixed(decimals(step));
        });
        UI.numbers[key] = s;
        return s;
    }

    function decimals(step) { var t = String(step); return t.indexOf('.') < 0 ? 0 : t.length - t.indexOf('.') - 1; }
    // a typed or slid value inside its range, on its step (the classic page's valInputDecimal holds it to the range)
    function clampStep(v, s) {
        var n = Number(v);
        if (!isFinite(n)) { n = s.dflt; }
        n = Math.min(s.max, Math.max(s.min, n));
        var f = Math.pow(10, decimals(s.step));
        return Math.round(n * f) / f;
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
        // what is stored, with what waits for Apply over it (Reset, a slider, a band)
        var c = view(), can = CAN;
        var ready = !!(c && can);
        Object.keys(UI.panes).forEach(function (k) { if (k !== 'presets') { UI.panes[k].classList.toggle('dimmed', !ready); } });
        if (!ready) {
            UI.dspSw.disabled = true;
            UI.exactSw.disabled = true;
            UI.debugSw.disabled = true;
            UI.reset.disabled = true;
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
        setSegment(UI.dsdSrcSeg, dsdSource(c));
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
        // the decoder and the DSP rate are a DSD file's alone: shown overridden, still editable, while the converter leaves
        // it untouched (the modulator is the PCM tracks' too)
        var leave = mode === 'dsd' && dsdSource(c) === 'untouched';
        [UI.dsdCut, UI.dsdGain, UI.dsdInt, UI.dsdDsp].forEach(function (r) { r.row.classList.toggle('overridden', leave); });
        UI.dsdInt.note.textContent = native ? '' : (L.dsdNativeOnly || 'With the DSP rate Native');

        // what DSP on drives: dimmed while it is off, still editable, and the tabs that hold it say so
        // DSP tab: ReplayGain and loudness, their options shown only while they are on (as the classic page)
        var rg = c.ReplayGain || {}, rgOn = truthy(rg.enabled);
        setSwitch(UI.rgSw, rgOn, false);
        UI.rgOpts.classList.toggle('hide', !rgOn);
        var mode = String(num(rg.mode, 3));
        setSegment(UI.rgModeSeg, ['1', '2', '3'].indexOf(mode) >= 0 ? mode : '3');
        var loudOn = truthy((c.Loudness || {}).enabled);
        setSwitch(UI.loudSw, loudOn, false);
        UI.loudOpts.classList.toggle('hide', !loudOn);
        // Crossfeed as the engine takes it (any case), else off
        var xv = String(c.Crossfeed == null ? '' : c.Crossfeed).toLowerCase();
        setSegment(UI.xfSeg, XFEEDS.indexOf(xv) >= 0 ? xv : 'off');
        // the engine's debug log, every player's
        setSwitch(UI.debugSw, !!can.debugLog, false);
        UI.reset.disabled = false;

        // what DSP on drives: every tab but Presets dimmed while it is off, still editable, each saying so (the classic
        // page's DSP-off panes)
        ['dsp', 'src', 'pcm', 'dsd', 'peq', 'conv', 'xfeed'].forEach(function (k) { UI.panes[k].classList.toggle('dimmed', !dspOn); });
        [UI.dspOffNote, UI.srcOffNote, UI.pcmOffNote, UI.dsdOffNote, UI.peqOffNote, UI.convOffNote, UI.xfOffNote].forEach(function (n) {
            n.textContent = dspOn ? '' : (L.dspOffTab || '');
        });
        Object.keys(UI.sliders).forEach(function (k) { UI.sliders[k].ctl.row.classList.toggle('dimmed', !dspOn); });
        renderSliders();
        renderFirs();
        renderPEQ();
        tipRefresh();   // a greyed button's why may have changed
    }

    // the DSD settings as the engine takes them (LyrionDSPSettings buildConfig): unset or out of range, its default
    function dsdCutoff(c) { var v = num(c.DSDCutoff, NaN); return (v === 0 || (v > 20000 && v <= 60000)) ? v : 30000; }
    function dsdGain(c) { var v = num(c.DSDGain, NaN); return (v >= -24 && v <= 12) ? v : 0; }
    // a DSD file under the converter to DSD, as the plugin takes the player's UpsampleDSDSource (trimmed, any case), else upsample
    function dsdSource(c) {
        var v = String(c.UpsampleDSDSource == null ? '' : c.UpsampleDSDSource).replace(/^[ "]+|[ "]+$/g, '').toLowerCase();
        return DSD_SOURCES.indexOf(v) >= 0 ? v : 'upsample';
    }
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
        function shown(s, k) {
            var pending = has(PENDING, k);
            var v = pending ? PENDING[k] : num(getPath(c, k), s.dflt);
            // never move a thumb the user is holding, nor a number being typed
            if (s.range && (document.activeElement !== s.range || !pending)) { s.range.value = v; }
            if (document.activeElement !== s.box) { s.box.value = v.toFixed(decimals(s.step)); }
            if (s.range) { s.range.disabled = !DATA; }
            s.box.disabled = !DATA;
            s.val.classList.toggle('pending', pending);
        }
        Object.keys(UI.sliders).forEach(function (k) { shown(UI.sliders[k], k); });
        Object.keys(UI.numbers).forEach(function (k) { shown(UI.numbers[k], k); });
        renderSave();
    }

    // Save beside what waits (Simon 2026-11-25: "put the apply button/ or call it Save in Red if it needs saving, next to
    // the areas that it applies to rather than at top the others all save with buttons"): a row at the foot of each tab
    // whose controls wait (DSP, PEQ under its bands, Convolution), and one beside Reset for what Reset left waiting that
    // no tab's row holds; each shown, red, only while one of its own settings waits, and each saves all that waits
    function saveRow(parent, area) {
        var r = parent.appendChild(make('div', 'applyrow hide'));
        r.dataset.area = area;
        // said plainly (Simon 2026-11-25: "say settings will be lost or save now")
        r.appendChild(make('span', 'unsaved', L.unsaved || 'Not saved: settings will be lost unless you save now.'));
        var b = r.appendChild(make('button', 'btn save', L.apply || 'Save'));
        b.type = 'button';
        b.disabled = true;
        b.addEventListener('click', function () { applyPending(); });
        UI.saveRows[area] = r;
        return r;
    }
    // the row a waiting setting shows in: the tab holding its control (the bands: PEQ's); else beside Reset
    function areaOf(k) {
        if (k === 'Filters') { return 'peq'; }
        var s = UI.sliders[k] || UI.numbers[k];
        var p = s ? s.ctl.row.closest('.tabpane') : null;
        return p && UI.saveRows[p.dataset.t] ? p.dataset.t : 'top';
    }
    function renderSave() {
        var want = {};
        Object.keys(PENDING).forEach(function (k) { want[areaOf(k)] = true; });
        Object.keys(UI.saveRows).forEach(function (a) {
            UI.saveRows[a].classList.toggle('hide', !want[a]);
            UI.saveRows[a].lastChild.disabled = !want[a];
        });
    }

    // Reset (the classic page's LocalClearSettings): every DSP setting back to a new player's, SAVED AT ONCE (Simon
    // 2026-11-25: "reset should reset it should not need a save"); how the player is fed kept (RESET_KEEP); the DSP left
    // on. Whatever else waited goes in the same save; a save that fails leaves it all waiting (applyPending), Save beside
    // Reset for what no tab holds
    function resetAll() {
        if (!client()) { return; }
        var d = defaults();
        d.Bypass = 0;
        Object.keys(d).forEach(function (k) {
            if (RESET_KEEP.indexOf(k) >= 0) { return; }
            var v = d[k];
            if (v && typeof v === 'object' && !Array.isArray(v)) {
                Object.keys(v).forEach(function (sub) { PENDING[k + '.' + sub] = v[sub]; });
            } else {
                PENDING[k] = clone(v);
            }
        });
        BAND = null;
        applyPending();
    }

    // Clear Log (the classic page's): the engine's log emptied, every player's; the peaks read again
    function clearLog() {
        var pid = PID;
        rpc(pid, ['squeezedsp.clearlog']).then(function (r) {
            if (r && r._success !== undefined && num(r._success, 0) !== 1) { throw new Error(r._message || 'not cleared'); }
            if (pid !== PID) { return; }
            PEAKS = [];
            NEED.logs = true;
            renderStatus();
        }).catch(function (e) {
            var el = $('err');
            if (el) { el.textContent = e.message; }
        });
    }

    // the engine's debug log, on or off for every player (squeezedsp.debuglog), saved at once
    function setDebug(on) {
        if (!UI || !CAN) { return; }
        UI.debugSw.disabled = true;
        rpc('', ['squeezedsp.debuglog', 'set:' + (on ? 1 : 0)]).then(function (r) {
            if (CAN) { CAN.debugLog = num(r.debug, 0) === 1; }
            flash(L.saved || 'Saved');
        }, function (e) {
            var el = $('err');
            if (el) { el.textContent = fmt(L.saveFailed || 'Not saved: %s', e.message); }
        }).then(renderControls);
    }

    // ---------------------------------------------------------------- Convolution
    // The impulse files as a dropdown, None first; the stored one matched by its path, else by its file's name (the
    // classic page's highlightMatchingOption), else shown by its name. Rebuilt only when its options change.
    function renderFirs() {
        if (!UI || !UI.firSel) { return; }
        var c = view() || {};
        var stored = String(c.FIRWavFile == null || c.FIRWavFile === '' ? '-' : c.FIRWavFile);
        var cur = firValue(stored);
        var opts = [['-', L.firNone || 'None']].concat(FIRS.map(function (f) { return [f.path, f.file]; }));
        if (cur !== '-' && !opts.some(function (o) { return o[0] === cur; })) { opts.push([cur, baseName(cur)]); }
        var sig = JSON.stringify(opts);
        if (sig !== UI.firSig) {
            UI.firSig = sig;
            UI.firSel.textContent = '';
            opts.forEach(function (o) {
                var e = UI.firSel.appendChild(make('option', null, o[1]));
                e.value = o[0];
            });
        }
        if (document.activeElement !== UI.firSel) { UI.firSel.value = cur; }
        UI.firSel.disabled = !DATA;
    }
    function firValue(stored) {
        if (!stored || stored === '-') { return '-'; }
        var norm = function (p) { return String(p).replace(/\\/g, '/'); };
        var stem = function (p) { return baseName(p).split('.')[0]; };
        var i;
        for (i = 0; i < FIRS.length; i++) { if (norm(FIRS[i].path) === norm(stored)) { return FIRS[i].path; } }
        for (i = 0; i < FIRS.length; i++) { if (stem(FIRS[i].path) === stem(stored)) { return FIRS[i].path; } }
        return stored;
    }

    // ---------------------------------------------------------------- the equaliser
    function typeName(t) {
        return { peak: L.peak || 'Peak', lowshelf: L.lowShelf || 'Low Shelf', highshelf: L.highShelf || 'High Shelf',
                 lowpass: L.lowPass || 'Low Pass', highpass: L.highPass || 'High Pass' }[t] || t;
    }
    function typeHelp(t) {
        return { peak: L.peakHelp, lowshelf: L.lowShelfHelp, highshelf: L.highShelfHelp, lowpass: L.lowPassHelp, highpass: L.highPassHelp }[t];
    }
    function bandsOf(c) { return Array.isArray(c && c.Filters) ? c.Filters : []; }
    // the bands to change: those waiting for Apply, else a copy of the stored ones, which then wait
    function editBands() {
        if (!has(PENDING, 'Filters')) { PENDING.Filters = clone(bandsOf(client())); }
        return PENDING.Filters;
    }
    // a band as Add makes it (the classic page's AddPEQBand: PEQ_BAND_DEFAULTS, sqdsp_peq_lib.js)
    function addBand() {
        if (!client() || typeof PEQ_BAND_DEFAULTS === 'undefined') { return; }
        var list = editBands(), d = PEQ_BAND_DEFAULTS[UI.addType] || PEQ_BAND_DEFAULTS.peak;
        list.push({ FilterType: UI.addType, Frequency: d.Frequency, Gain: d.Gain, Slope: d.Slope, SlopeType: d.SlopeType });
        BAND = list.length - 1;
        renderControls();
    }
    function removeBand(i) {
        var list = editBands();
        if (i < 0 || i >= list.length) { return; }
        list.splice(i, 1);
        BAND = null;
        renderControls();
    }
    // a band's type: between peak and shelf its Q and Slope convert exactly at its gain (the classic page's
    // handleFilterTypeChange, as fixed in 0.1.72.57); a shelf holding a Q keeps it
    function bandType(i, t) {
        var list = editBands(), f = list[i];
        if (!f) { return; }
        var wasShelf = /shelf/.test(String(f.FilterType).toLowerCase()), isShelf = /shelf/.test(t);
        f.FilterType = t;
        if (wasShelf !== isShelf && typeof peqShelfQToSlope === 'function') {
            if (isShelf && f.SlopeType === 'Q') { f.SlopeType = 'slope'; f.Slope = peqShelfQToSlope(f.Slope, f.Gain); }
            else if (!isShelf && f.SlopeType === 'slope') { f.SlopeType = 'Q'; f.Slope = peqShelfSlopeToQ(f.Slope, f.Gain); }
        }
        BAND = i;
        renderControls();
    }
    // the frequency slider's position, log scaled from 20 Hz to 20 kHz, and back (the classic page's log2LinearMapper,
    // and logScaleMapper with its rounding)
    function freqToPos(f) {
        var lo = Math.log10(20), hi = Math.log10(20000);
        return Math.max(0, Math.min(100, (Math.log10(Math.max(1, Number(f) || 1000)) - lo) / (hi - lo) * 100));
    }
    function posToFreq(p) {
        var lo = Math.log10(20), hi = Math.log10(20000);
        var f = Math.round(Math.pow(10, lo + (hi - lo) * Number(p) / 100));
        var r = f > 12000 ? 1000 : f >= 5000 ? 100 : f >= 1000 ? 10 : f > 30 ? 5 : 1;
        return Math.round(f / r) * r;
    }

    // The bands, rebuilt only when they change from outside their own rows (a settings read, Add, a type, a file);
    // a band's own slider or number changes only its values, so a thumb in use is never replaced
    function renderPEQ() {
        if (!UI || !UI.bands) { return; }
        var list = bandsOf(view() || {});
        var sig = JSON.stringify(list);
        var busy = UI.bands.contains(document.activeElement) && document.activeElement.tagName === 'INPUT';
        if (sig !== UI.bandSig && !busy) {
            UI.bandSig = sig;
            UI.bands.textContent = '';
            list.forEach(function (f, i) { UI.bands.appendChild(bandCard(f, i)); });
        }
        var waiting = has(PENDING, 'Filters');
        Array.prototype.forEach.call(UI.bands.querySelectorAll('.val'), function (v) { v.classList.toggle('pending', waiting); });
        Array.prototype.forEach.call(UI.bands.querySelectorAll('input'), function (e) { e.disabled = !DATA; });
        drawGraphSoon();
    }

    function bandCard(f, i) {
        var card = make('div', 'band');
        card.dataset.i = i;
        var head = card.appendChild(make('div', 'band-h'));
        head.appendChild(make('span', 'band-n', fmt(L.band || 'Band %s', i + 1)));
        var type = String(f.FilterType || 'peak').toLowerCase();
        if (PEQ_TYPES.indexOf(type) < 0) { type = 'peak'; }
        var seg = segment(head, PEQ_TYPES.map(function (t) { return [t, typeName(t)]; }), function (v) { bandType(i, v); });
        PEQ_TYPES.forEach(function (t) { tip(seg[t], typeHelp(t)); });
        setSegment(seg, type);
        var x = head.appendChild(make('button', 'btn x', '\u00d7'));
        x.type = 'button';
        x.setAttribute('aria-label', fmt(L.band || 'Band %s', i + 1) + ': ' + (L.remove || 'Remove'));
        x.addEventListener('click', function () { removeBand(i); });
        var pass = type === 'lowpass' || type === 'highpass', slope = f.SlopeType === 'slope';
        bandRow(card, f, i, 'Frequency', L.freq || 'Frequency', null, 20, 20000, 1, true);
        if (!pass) { bandRow(card, f, i, 'Gain', L.gain || 'Gain', null, -25, 15, 0.1, false); }
        bandRow(card, f, i, 'Slope', slope ? 'Slope' : 'Q', slope ? L.slopeHelp : L.qHelp, slope ? 0.05 : 0.1, slope ? 1.2 : 20, 0.01, false);
        card.addEventListener('pointerdown', function () { if (BAND !== i) { BAND = i; drawGraphSoon(); } });
        card.addEventListener('focusin', function () { if (BAND !== i) { BAND = i; drawGraphSoon(); } });
        return card;
    }

    // one of a band's values: a slider (log scaled for the frequency) and its number box, both waiting for Apply; a
    // double click puts the gain back to 0 (the classic page's)
    function bandRow(card, f, i, prop, label, help, min, max, step, log) {
        var ctl = control(card, 'band' + i + prop, label, help, true);
        var range = make('input', 'range');
        range.type = 'range';
        if (log) { range.min = 0; range.max = 100; range.step = '0.01'; } else { range.min = min; range.max = max; range.step = String(step); }
        range.setAttribute('aria-label', fmt(L.band || 'Band %s', i + 1) + ' ' + label);
        var val = make('span', 'val');
        var box = val.appendChild(make('input', 'num'));
        box.type = 'number'; box.min = min; box.max = max; box.step = String(step);
        box.setAttribute('aria-label', fmt(L.band || 'Band %s', i + 1) + ' ' + label);
        ctl.c.appendChild(range);
        ctl.c.appendChild(val);
        var lim = { min: min, max: max, step: step, dflt: num(f[prop], min) };
        function show(v) {
            if (document.activeElement !== range) { range.value = log ? freqToPos(v) : v; }
            if (document.activeElement !== box) { box.value = Number(v).toFixed(decimals(step)); }
        }
        show(clampStep(f[prop], lim));
        function set(v) {
            var list = editBands();
            if (!list[i]) { return; }
            v = clampStep(v, lim);
            list[i][prop] = v;
            UI.bandSig = JSON.stringify(list);    // its rows already show it: no rebuild
            BAND = i;
            show(v);
            renderSliders();                      // PEQ's Save shows
            renderPEQ();
        }
        range.addEventListener('input', function () { set(log ? posToFreq(range.value) : range.value); });
        // a typed value, once committed, shown as kept (held to the range and step) though the box still has the focus
        box.addEventListener('change', function () {
            if (box.value !== '') { set(box.value); }
            var b = bandsOf(view() || {})[i];
            if (b) { box.value = clampStep(b[prop], lim).toFixed(decimals(step)); }
        });
        if (prop === 'Gain') { range.addEventListener('dblclick', function () { set(0); }); }
    }

    // The graph, as the classic page draws it (its updatePEQGraph, with Fox's FrequencyResponsePlotter): loudness, all
    // the bands, the band in use, and everything together; shown while loudness is on or a band is there; drawn at
    // the card's width (Fox's plotter is 300 px at least: the drawing is scaled to fit, CSS); at most once a frame
    var GRAPH_RAF = null;
    function drawGraphSoon() {
        if (GRAPH_RAF !== null) { return; }
        GRAPH_RAF = (window.requestAnimationFrame || function (f) { return setTimeout(f, 16); })(function () { GRAPH_RAF = null; drawGraph(); });
    }
    function drawGraph() {
        if (!UI || !UI.graph) { return; }
        var c = view() || {}, list = bandsOf(c), loud = c.Loudness || {}, loudOn = truthy(loud.enabled);
        UI.graphBox.classList.toggle('hide', !(loudOn || list.length));
        if (!(loudOn || list.length) || TAB !== 'peq') { return; }
        if (typeof FrequencyResponsePlotter !== 'function' || typeof SqueezeDSPHelper !== 'function') { return; }
        var w = UI.graphBox.clientWidth;
        if (!w) { return; }
        try {
            var plotter = new FrequencyResponsePlotter(w, Math.round(Math.max(200, Math.min(320, w * 0.45))));
            var responses = [], labels = [], data = { Client: { Filters: list } }, lr = null, pr = null;
            if (loudOn && typeof generateLoudnessCoefficients === 'function') {
                lr = generateLoudnessCoefficients(num(loud.listening_level, 85));
                if (lr && lr.coefficients.length) {
                    responses.push(plotter.calculateCombinedResponse(lr.coefficients.map(function (x) { return x.coefficients; }), 48000));
                    labels.push('Loudness');
                }
            }
            if (list.length) {
                pr = SqueezeDSPHelper.generateFromAllFilters(data, 48000);
                if (pr.coefficients.length) {
                    responses.push(plotter.calculateCombinedResponse(pr.coefficients.map(function (x) { return x.coefficients; }), 48000));
                    labels.push('All Bands');
                    if (BAND !== null && BAND >= 0 && BAND < list.length) {
                        try {
                            var one = SqueezeDSPHelper.generateFromFilterIndex(data, BAND, 48000);
                            responses.push(plotter.calculateFrequencyResponse(one.coefficients, 48000));
                            labels.push('Band ' + (BAND + 1));
                        } catch (e) { /* a band the maths refuses: drawn with the rest only */ }
                    }
                    if (lr && lr.coefficients.length) {
                        var all = lr.coefficients.map(function (x) { return x.coefficients; }).concat(pr.coefficients.map(function (x) { return x.coefficients; }));
                        responses.push(plotter.calculateCombinedResponse(all, 48000));
                        labels.push('Everything');
                    }
                }
            }
            UI.graph.innerHTML = responses.length ? plotter.generateSVGPlot(responses, labels, '') : '';
        } catch (e) {
            UI.graph.textContent = '';
        }
    }

    // ---------------------------------------------------------------- filter files and AutoEQ
    function fileNote(text) { if (UI && UI.fileNote) { UI.fileNote.textContent = text || ''; } }

    // A dropped or chosen file (the classic page's handleFileSelection): a .txt is REW's / AutoEQ's filter text, its
    // bands and preamp then waiting for Apply; a .wav goes to the server's impulse folder (the FIR list)
    function importFile(file) {
        if (!file || !PID) { return; }
        var ext = String(file.name).split('.').pop().toLowerCase();
        var rd = new FileReader();
        if (ext === 'txt') {
            rd.onload = function () { loadEQText(String(rd.result || ''), file.name); };
            rd.readAsText(file);
        } else if (ext === 'wav') {
            if (!window.confirm((L.wavAsk || 'Import the filter impulse response file:') + '\n' + file.name)) { return; }
            rd.onload = function () { uploadWav(file.name, String(rd.result || '').split(',')[1] || '', false); };
            rd.readAsDataURL(file);
        } else {
            fileNote(L.fileType || 'Only a .txt (REW) or .wav (FIR) file');
        }
    }

    // a filter file's text (parseEQText, sqdsp_peq_lib.js): asked first with the text shown, as the classic page asks;
    // its bands and preamp (0 when it has none) replace the player's and wait for Apply; nothing else changes (Simon
    // 2026-11-20: the classic page's import reset every other DSP setting)
    function loadEQText(text, name) {
        if (typeof parseEQText !== 'function' || !client()) { return; }
        var parsed = parseEQText(text);
        if (!parsed.filters.length) { fileNote(fmt(L.fileNoFilters || '%s holds no filters', name)); return; }
        if (!window.confirm((L.rewAsk || 'Load the settings:') + '\n' + text)) { return; }
        PENDING.Filters = parsed.filters;
        PENDING.Preamp = parsed.preamp === null ? 0 : parsed.preamp;
        BAND = null;
        fileNote(fmt(L.fileLoaded || '%s loaded', name));
        renderControls();
    }

    // a WAV to the impulse folder (squeezedsp.importwav, the server's sanitised name); the FIR list then read again,
    // and with pick, that file selected for this player once the list holds it (AutoEQ's)
    function uploadWav(name, b64, pick) {
        var pid = PID;
        var file = String(name).replace(/.*[\\\/]/, '').replace(/[^\w.\-]/g, '').replace(/\.wav$/i, '') + '.wav';
        if (file === '.wav') { file = 'audio_import.wav'; }
        return rpc(pid, ['squeezedsp.importwav', '_p0:' + name, '_p1:' + b64], 60000).then(function (r) {
            if (!r || num(r._success, 0) !== 1) { throw new Error('not imported'); }
            fileNote(fmt(L.fileImported || '%s imported', file));
            if (pick) { FIR_PICK = { pid: pid, file: file }; }
            NEED.presets = true;
            schedule(0);
        }).catch(function () {
            fileNote(fmt(L.fileFailed || '%s not imported', name));
        });
    }

    // AutoEQ's headphone list (AutoEQIndex, sqdsp_autoeq.js), read from GitHub when the search box is first used
    var AEQ = { timer: null, state: 'none' };
    function aeqIndex(then) {
        if (typeof AutoEQIndex === 'undefined') { return; }
        if (AEQ.state === 'ready') { if (then) { then(); } return; }
        AEQ.state = 'loading';
        AutoEQIndex.loadIndex(function (entries) {
            AEQ.state = entries ? 'ready' : 'none';
            if (!entries && UI && UI.aeq) { UI.aeq.note.textContent = L.aeqIndexFailed || 'The AutoEQ list could not be read'; }
            if (entries && then) { then(); }
        });
    }
    function aeqSearchSoon() {
        clearTimeout(AEQ.timer);
        var q = UI.aeqInput.value.trim();
        if (q.length < 2) { aeqShow([]); return; }
        AEQ.timer = setTimeout(function () {
            aeqIndex(function () { aeqShow(AutoEQIndex.search(UI.aeqInput.value.trim())); });
        }, 250);
    }
    function aeqShow(list) {
        UI.aeqList.textContent = '';
        UI.aeqList.classList.toggle('hide', !list.length);
        list.forEach(function (e) {
            var b = UI.aeqList.appendChild(make('button', 'aeq-o'));
            b.type = 'button';
            b.appendChild(make('span', 'aeq-n', e.name));
            b.appendChild(make('span', 'aeq-s', e.source));
            b.addEventListener('click', function () { UI.aeqInput.value = ''; aeqShow([]); aeqPick(e); });
        });
    }
    // a headphone picked: its FIR WAV first (48k, then 44.1k), loaded for this player in place of the FIR there (Simon
    // 2026-11-20: "Loading the fir file for AUtoEQ and replacing the current one is to be expected"), and kept in the
    // FIR folder to swap back; its ParametricEQ.txt only when it has no WAV
    function aeqPick(e) {
        if (typeof AutoEQIndex === 'undefined' || !AutoEQIndex.fetchBest) { return; }
        var note = UI.aeq.note;
        note.textContent = fmt(L.aeqFetch || 'Downloading %s', e.name);
        AutoEQIndex.fetchBest(e).then(function (best) {
            if (!best) { note.textContent = fmt(L.aeqNone || 'No filter found for %s', e.name); return; }
            note.textContent = '';
            var name = AutoEQIndex.fileName(e, best.type === 'wav' ? 'wav' : 'txt');
            if (best.type === 'wav') {
                if (!window.confirm((L.wavAsk || 'Import the filter impulse response file:') + '\n' + name)) { return; }
                var rd = new FileReader();
                rd.onload = function () { uploadWav(name, String(rd.result || '').split(',')[1] || '', true); };
                rd.readAsDataURL(best.blob);
            } else {
                best.blob.text().then(function (t) { loadEQText(t, name); });
            }
        }, function () {
            note.textContent = fmt(L.aeqFailed || '%s: download failed', e.name);
        });
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
    var framed = inFrame();
    if (back) {
        if (framed) {
            back.parentNode.removeChild(back);
        } else {
            back.textContent = L.back || 'Back';
            // settings not saved: asked first (leaveGuard)
            back.addEventListener('click', function () {
                leaveGuard(function () {
                    window.close();
                    setTimeout(function () { window.location.href = '/material/'; }, 80);
                });
            });
        }
    }

    // Classic interface (Simon 2026-11-20: the user's choice of one interface, and a way back to classic): the choice
    // saved on the server (squeezedsp.interface), then the classic page for this player, inside Material's skin when
    // Material frames this page
    var toClassicBtn = $('classic');
    if (toClassicBtn) {
        toClassicBtn.textContent = L.classicUi || 'Classic interface';
        // settings not saved: asked first (leaveGuard); the switch refused, the page stays and asks again next time
        toClassicBtn.addEventListener('click', function () {
            leaveGuard(function () {
                toClassicBtn.disabled = true;
                rpc('', ['squeezedsp.interface', 'set:classic']).then(function () {
                    var base = '/';
                    try { if (framed && /^\/material(\/|$)/.test(window.top.location.pathname)) { base = '/material/'; } } catch (e) {}
                    window.location.replace(base + 'plugins/SqueezeDSP/index.html' + (PID ? '?player=' + encodeURIComponent(PID) : ''));
                }, function (e) {
                    LEAVING = false;
                    toClassicBtn.disabled = false;
                    var el = $('err');
                    if (el) { el.textContent = e.message; }
                });
            });
        });
    }
    // any other way out with settings not saved (Material's back arrow, a reload, the window closed): the browser's own
    // question, as the classic page asks (its handleUnsavedChanges). Material's close and Home remove the frame unasked.
    window.addEventListener('beforeunload', function (e) {
        if (!dirty() || LEAVING) { return; }
        e.preventDefault();
        e.returnValue = '';
    });
    window.addEventListener('resize', function () { drawGraphSoon(); });

    tipStart();
    build();
    poll();

    // for the test harness only: what the page holds
    window.__sqdsp = function () {
        return { pid: PID, players: PLAYERS, can: CAN, data: DATA, status: STATUS, presets: PRESETS, pending: PENDING, gen: GEN,
                 firs: FIRS, band: BAND };
    };
}());
