/*---------------------------------------------------
Core UI Controls and Display Logic
-----------------------------------------------------*/

function logParentTheme() {
    const body = document.body;
    const textColor = getComputedStyle(body).color;
    
    console.log('=== THEME DETECTION LOG ===');
    console.log('Body classes:', body.className);
    console.log('Body computed color:', textColor);
    
    // Simple rule: if text color is light, we're in dark mode. If dark, we're in light mode.
    const rgb = textColor.match(/\d+/g);
    let isDark = false;
    
    if (rgb && rgb.length >= 3) {
        const [r, g, b] = rgb;
        const brightness = (r * 299 + g * 587 + b * 114) / 1000;
        isDark = brightness > 128; // Light text = dark theme
    }
    
    console.log('Decision: Using', isDark ? 'DARK' : 'LIGHT', 'theme based on text color');
    return isDark ? 'dark' : 'light';
}


function refreshPresetList() {
    SqueezeDSPGetList(function(data) {
        LocalBuildList('Preset', data);
    });
}



function LocalBuildLists(data) {
    LocalBuildList('FIRWavFile', data);
    LocalBuildList('Preset', data);
}

function LocalBuildList(listType, data) {
    var select = document.getElementById("sel_" + listType);
    select.options.length = 1;
    
    var loop = data.result[listType + "_loop"];
    if (typeof loop == "object") {
        for (var j = 0; j < loop.length; j++) {
            var fullPath = loop[j][0];
            var fileName = fullPath.split(/[\\/]/).pop();
            
            var el = document.createElement("option");
            el.value = fullPath;
            el.textContent = fileName;
            el.dataset.fullPath = fullPath;
            
            select.appendChild(el);
        }
    }
}


function LocalUpdateSettings() {
    rebuildPEQUI('peqContainer');
    LocalDisplayVals();    
    
    myRadio = LocalSetupToggle('DSP', SqueezeDSPData.Client.Bypass, true);
    LocalSetupToggle('ExactLength', String(SqueezeDSPData.Client.ExactLength) === '1' ? 1 : 0);
    LocalSetupAll();

    myRadio = LocalSetupToggle('Loudness', replaceNull(SqueezeDSPData.Client.Loudness.enabled,0));
    ToggleControl(myRadio, 'control_Loudness', false); 
	myRadio = LocalSetupToggle('ReplayGain', replaceNull(SqueezeDSPData.Client.ReplayGain && SqueezeDSPData.Client.ReplayGain.enabled, 0));
    updateReplayGainToggle();
    
    ListenForSlide();
	GetLogSummary();
    // the engine's debug log (every player's), as the server holds it
    const debugBox = document.getElementById('DebugLog');
    if (debugBox && SqueezeDSPPlayer) debugBox.checked = !!SqueezeDSPPlayer.debugLog;
}

function LocalDisplayVals() {
    if (!SqueezeDSPData.Client) SqueezeDSPData.Client = {};
    DisplayVal('Delay.delay', replaceNull(SqueezeDSPData.Client.Delay.delay, 0));
    DisplayVal('Width', replaceNull(SqueezeDSPData.Client.Width, 0));
    DisplayVal('Balance', replaceNull(SqueezeDSPData.Client.Balance, 0));
    DisplayVal('Preamp', replaceNull(SqueezeDSPData.Client.Preamp, 0));
    DisplayVal('Loudness.listening_level', replaceNull(SqueezeDSPData.Client.Loudness.listening_level,85));
	// Replay Gain — use specific defaults if not set
	const rgFixed = (SqueezeDSPData.Client.ReplayGain && SqueezeDSPData.Client.ReplayGain.fixed_gain != null)
    ? SqueezeDSPData.Client.ReplayGain.fixed_gain
    : -6.0;
	document.getElementById('val_ReplayGain.fixed_gain').value = rgFixed;
	// Replay Gain Spotify gain — use specific default if not set ( spotify internal replaygain is targetting -14 LUFS, which is about -4 dB compared to the common -11 LUFS target)
	const rgSpotify = (SqueezeDSPData.Client.ReplayGain && SqueezeDSPData.Client.ReplayGain.spotify_gain != null)
    ? SqueezeDSPData.Client.ReplayGain.spotify_gain
    : -4.0;
	document.getElementById('val_ReplayGain.spotify_gain').value = rgSpotify;

	DisplayVal('FIRStrength', replaceNull(SqueezeDSPData.Client.FIRStrength, 100));

	// Crossfeed — restore radio state
	const cfVal = replaceNull(SqueezeDSPData.Client.Crossfeed, 'off');
	const cfRadio = document.querySelector(`#crossfeed-selector input[value="${cfVal}"]`);
	if (cfRadio) cfRadio.checked = true;


    const presetPath = SqueezeDSPData.Client.Preset || 'none';
  
    document.getElementById('edt_NewPreset').value = getBaseFilename(presetPath);
    
    const firPath = SqueezeDSPData.Client.FIRWavFile || 'none';
    highlightMatchingOption('sel_FIRWavFile', firPath);
    
  
    document.getElementById('ClientName').value = SqueezeDSPData.ClientName;
}

function DisplayVal(ElName, MyVal) {
    try {
        document.getElementById(ElName).value = MyVal;
        document.getElementById('val_' + ElName).value = MyVal;
    } catch {
        DisplayAlert(ElName, "Function DisplayVal failing for:");
    }
}

function LocalSetupToggle(myControl, newValue, InvertToggle = false) {
    myCheck = document.getElementById(myControl);
    myCheck.value = newValue;
    
    if (myCheck.value == 1) {
        if (!InvertToggle) myCheck.checked = true;
        else myCheck.checked = false;
    }
    if (myCheck.value == 0) {
        if (InvertToggle) myCheck.checked = true;
        else myCheck.checked = false;
    }
    return myCheck;
}

// Output format: FLAC 24-bit (the default), or PCM as 24-bit WAV with its header, which only
// a player that reads the header can take (a squeezelite started with -W). A player that
// cannot take 24-bit FLAC gets no DSP at all. readclientSettings reports both (dsp_ok,
// wav_ok); the server applies the same rule whatever the page sends.
function LocalSetupOutput() {
    const can = (typeof SqueezeDSPPlayer === 'object' && SqueezeDSPPlayer) ? SqueezeDSPPlayer : { dspOk: true, wavOk: false };
    const outVal = replaceNull(SqueezeDSPData.Client.OutputFormat, 'FLAC');
    const flac = document.querySelector('#output-selector input[value="FLAC"]');
    const pcm  = document.querySelector('#output-selector input[value="PCM"]');
    const note = document.getElementById('note_OutputFormat');
    const dsp  = document.getElementById('DSP');

    if (pcm) {
        pcm.disabled = !can.wavOk;
        pcm.checked = (outVal === 'PCM');
    }
    if (flac) flac.checked = (outVal !== 'PCM');
    if (dsp) dsp.disabled = !can.dspOk;

    let msg = '';
    const toDsd = typeof srcMode === 'function' && srcMode() === 'dsd';
    const orow = document.getElementById('control_OutputFormat');
    if (orow) orow.classList.toggle('sqdsp-overridden', toDsd);
    if (!can.dspOk) msg = document.getElementById('NoDSPBelowFLAC_Help').value;
    else if (toDsd) msg = '';
    else if (can.outReduced) {
        // a reduced stream overrides the output chosen here while it is due (Bitrate Limiting, or the app's own list)
        const names = { MP3: 'MP3 (320 kbps)', OPS: 'Opus (256 kbps)', OGG: 'Ogg Vorbis (quality 10)' };
        const limit = can.outReduced === 'MP3' && can.outKbps;
        const el = document.getElementById(limit ? 'ReducedLimit_Help' : 'ReducedApp_Help');
        msg = el ? el.value.replace('%s', limit ? can.outKbps : (names[can.outReduced] || can.outReduced)) : '';
    }
    else if (!can.wavOk) msg = document.getElementById('PCMNeedsHeader_Help').value;
    if (note) {
        note.textContent = msg;
        note.style.display = msg ? '' : 'none';
    }
}

// The Sample Rate Converter (Upsample.pm, Configuration::dsdSpec; Simon 2026-11-03: "Call upsample Sample Rate Converter
// and we choose either DSD or PCM here"): the player's Upsample 'off', 'on' (PCM: every track to its PCM target, as
// before) or 'dsd' (every PCM track to DSD at UpsampleDSDRate, a DSD file as UpsampleDSDSource says). readclientSettings says whether each can run: PCM (up_ok: the
// stream-start hook, a known max; the engine resamples, since 0.1.72.37) and DSD (up_dsd_ok: the player lists dsf, and
// the hook; the engine takes PCM tracks up itself, with the converter's own filters, since 0.1.72.33). One that cannot
// shows as off, its button disabled with the reason on it, and the note says why when it is the one stored. PCM's rows
// (target, max rate) show with PCM, the DSD rate and its filters with DSD. PCM's own filters are on the PCM tab (its
// Resampler). The target rate is each track's own family (up_targets) or one fixed rate, the player's top (up_fixed); a
// max rate caps both, up to the player's top rate for its output (up_limits: FLAC out, WAV out), named as the server
// does (Upsample::targets).
const UPSAMPLE_MAX_RATES = [768000, 384000, 192000, 96000];
const DSD_RATE_NAMES = { 2822400: 'DSD64', 5644800: 'DSD128', 11289600: 'DSD256', 22579200: 'DSD512' };
// The engine's filters (Upsample.pm @FILTERS; 0.1.72.44, Simon 2026-11-06: "choose the filters from a drop down ... the
// name, lenght and phase"), in its order: [value, family, length, phase]. Kaiser's values as before, so a stored
// choice carries over. Each length's phases linear to minimum, the linear-leaning intermediates (80% and 70% linear,
// 2026-11-15) between.
const FILTER_PHASES = ['lin', 'int80', 'int70', 'int', 'min'];
const UPSAMPLE_FILTERS = (() => {
    const out = [];
    [['', 'Kaiser', ['short', 'long', 'xlong']], ['gauss-', 'Gaussian', ['short', 'medium', 'long']],
     ['halfband-', 'Half-band', ['']], ['slow-', 'Slow roll-off', ['']]].forEach(([pre, fam, lens]) =>
        lens.forEach(l => FILTER_PHASES.forEach(p => out.push([pre + (l ? l + '-' : '') + p, fam, l, p]))));
    out.push(['nonring', 'Non-ringing', '', '']);
    return out;
})();
const FILTER_LENGTH_NAMES = { short: 'Short', medium: 'Medium', long: 'Long', xlong: 'Extra-long' };
const FILTER_PHASE_NAMES = { lin: 'Linear', int80: 'Intermediate 80%', int70: 'Intermediate 70%', int: 'Intermediate', min: 'Minimum' };
// a filter dropdown's options, grouped by family, each named "Kaiser · Short · Linear"
function fillFilters(sel) {
    let group = null;
    UPSAMPLE_FILTERS.forEach(([v, fam, l, p]) => {
        if (!group || group.label !== fam) {
            group = sel.appendChild(document.createElement('optgroup'));
            group.label = fam;
        }
        const o = group.appendChild(document.createElement('option'));
        o.value = v;
        o.textContent = [fam, FILTER_LENGTH_NAMES[l], FILTER_PHASE_NAMES[p]].filter(Boolean).join(' · ');
    });
}
function upsampleRates(limit, max) {
    const ceil = (max && (!limit || max < limit)) ? max : limit;
    if (!ceil) return null;
    let f44 = 44100, f48 = 48000;
    while (f44 * 2 <= ceil) f44 *= 2;
    while (f48 * 2 <= ceil) f48 *= 2;
    if (f48 > ceil) f48 = f44;
    return { f44: f44, f48: f48, top: Math.max(f44, f48) };
}
function playerCan() {
    return (typeof SqueezeDSPPlayer === 'object' && SqueezeDSPPlayer) ? SqueezeDSPPlayer
        : { dspOk: true, wavOk: false, upOk: false, upWhy: '', upTargets: '', upFixed: '', upLimits: '',
            dsdOutOk: false, upDsdOk: false, outReduced: '' };
}
// the converter's mode as stored ('off', 'on', 'dsd': Upsample::mode) and as the server runs it
function srcWant() {
    const v = String(replaceNull(SqueezeDSPData.Client.Upsample, 'off')).toLowerCase();
    return v === 'on' || v === 'dsd' ? v : 'off';
}
function srcMode() {
    const can = playerCan(), want = srcWant();
    return (want === 'on' && can.upOk) ? 'on' : (want === 'dsd' && can.upDsdOk) ? 'dsd' : 'off';
}
// what the converter to DSD does with a DSD file (Upsample::dsdSource: any case, spaces and quotes trimmed), else upsample
const DSD_SOURCES = ['upsample', 'source', 'untouched'];
function dsdSource() {
    const v = String(replaceNull(SqueezeDSPData.Client.UpsampleDSDSource, 'upsample')).replace(/^[ "]+|[ "]+$/g, '').toLowerCase();
    return DSD_SOURCES.includes(v) ? v : 'upsample';
}
// why the converter to PCM cannot run (readclientSettings up_why), and why not to DSD
function whyNoPcm(can) {
    const ids = { nomax: 'UpsampleNoMax_Help', hook: 'UpsampleNoHook_Help', reduced: 'UpsampleNoReduced_Help' };
    const el = ids[can.upWhy] ? document.getElementById(ids[can.upWhy]) : null;
    return el ? el.value : '';
}
function whyNoDsd(can) {
    if (!can.dsdOutOk) {
        const el = document.getElementById(can.outReduced ? 'DSDOutNoReduced_Help' : 'DSDOutNoDsf_Help');
        return el ? el.value : '';
    }
    return whyNoPcm(can);
}
function helpText(id) { const el = document.getElementById(id); return el ? el.value : ''; }
function LocalSetupUpsample() {
    const can = playerCan();
    const c = SqueezeDSPData.Client;
    const want = srcWant(), mode = srcMode();
    const note = document.getElementById('note_Upsample');
    document.querySelectorAll('#src-selector input[type="radio"]').forEach(el => {
        const no = (el.value === 'on' && !can.upOk) ? whyNoPcm(can) : (el.value === 'dsd' && !can.upDsdOk) ? whyNoDsd(can) : '';
        el.disabled = (el.value === 'on' && !can.upOk) || (el.value === 'dsd' && !can.upDsdOk);
        el.checked = (el.value === mode);
        const lab = el.closest('label');
        if (lab) lab.title = no;
    });
    document.querySelectorAll('.sqdsp-src-pcm').forEach(r => { r.style.display = mode === 'on' ? '' : 'none'; });
    document.querySelectorAll('.sqdsp-src-dsd').forEach(r => { r.style.display = mode === 'dsd' ? '' : 'none'; });

    // the target the server writes: one it knows, else each track's own family
    const targets = ['family', 'fixed'];
    let target = String(replaceNull(c.UpsampleTarget, 'family')).toLowerCase();
    if (!targets.includes(target)) target = 'family';
    targets.forEach(t => {
        const el = document.querySelector(`#upsample-target-selector input[value="${t}"]`);
        if (el) el.checked = (t === target);
    });

    // the max rate the server writes: one it knows, at or below the player's top rate for its output;
    // above it, or unknown, the player's own (0)
    const limits = String(can.upLimits || '').split(',').map(Number);
    const pcm = replaceNull(c.OutputFormat, 'FLAC') === 'PCM' && can.wavOk;
    const limit = (pcm ? limits[1] : limits[0]) || 0;
    let max = Number(replaceNull(c.UpsampleMax, 0));
    if (!UPSAMPLE_MAX_RATES.includes(max) || (limit && max > limit)) max = 0;
    document.querySelectorAll('#upsample-max-selector input[type="radio"]').forEach(el => {
        const v = Number(el.value);
        el.disabled = v > 0 && limit > 0 && v > limit;
        el.checked = (v === max);
    });

    // the DSD rate (Upsample::options): one of the four, in digits alone, else DSD64
    let dr = /^\d+$/.test(String(c.UpsampleDSDRate)) ? Number(c.UpsampleDSDRate) : 2822400;
    if (!DSD_RATE_NAMES[dr]) dr = 2822400;
    setChoice('dsd-rate-selector', dr, 2822400);
    const src = dsdSource();
    document.querySelectorAll('#dsd-source-selector input[type="radio"]').forEach(el => { el.checked = (el.value === src); });
    // its filters (0.1.72.33, Upsample::filters), and PCM's (0.1.72.37, Upsample::pcmFilters): a dropdown each of the
    // filter family (0.1.72.44), filled once; the stored one in any case, else the default (Kaiser short intermediate
    // for a CD-rate source, short linear for hi-res)
    [['upsample-filter-cd-selector', 'UpsampleFilterCD', 'short-int'], ['upsample-filter-hires-selector', 'UpsampleFilterHiRes', 'short-lin'],
     ['upsample-pcm-filter-cd-selector', 'UpsamplePCMFilterCD', 'short-int'], ['upsample-pcm-filter-hires-selector', 'UpsamplePCMFilterHiRes', 'short-lin']].forEach(([id, key, def]) => {
        const sel = document.getElementById(id);
        if (!sel) return;
        if (!sel.options.length) fillFilters(sel);
        let v = String(replaceNull(c[key], def)).toLowerCase();
        if (!UPSAMPLE_FILTERS.some(f => f[0] === v)) v = def;
        sel.value = v;
    });

    let msg = '';
    if (want === 'on' && mode !== 'on') {
        msg = whyNoPcm(can);
    } else if (want === 'dsd' && mode !== 'dsd') {
        msg = whyNoDsd(can);
    } else if (mode === 'on' && can.upTargets) {
        // from the limit and max rate when the server sent the limits, else as the server last named them
        const r = upsampleRates(limit, max);
        const fixed = target === 'fixed' && (r || can.upFixed);
        const el = document.getElementById(fixed ? 'UpsampleFixed_Help' : 'UpsampleTargets_Help');
        const khz = x => (Number(x) / 1000) + ' kHz';
        const rates = fixed ? khz(r ? r.top : can.upFixed)
                            : (r ? [r.f44, r.f48] : can.upTargets.split(',')).map(khz).join(' / ');
        msg = (el ? el.value + ' ' : '') + rates;
    }
    if (note) {
        note.textContent = msg;
        note.style.display = msg ? '' : 'none';
    }
}

// DSD: how our engine decodes a DSF (ledger OUR OWN DSD DECODER), the player's DSDCutoff and DSDGain, shown as the engine
// takes them (LyrionDSPSettings buildConfig): unset, unreadable or out of range, its default, 30 kHz and 0 dB
function LocalSetupDSD() {
    const c = SqueezeDSPData.Client;
    const cs = c.DSDCutoff;   // (not replaceNull: None is 0, which it would take for none)
    const cut = (cs === undefined || cs === null || cs === '') ? 30000 : Number(cs);
    const gain = Number(replaceNull(c.DSDGain, 0));
    setChoice('dsd-cutoff-selector', (cut === 0 || (cut > 20000 && cut <= 60000)) ? cut : 30000, 30000);
    setChoice('dsd-gain-selector', (gain >= -24 && gain <= 12) ? gain : 0, 0);
}

// DSD Processing's Output (Configuration::dsdOut): with the converter off, a DSF goes out as PCM ('0', the default) or as
// DSD at its own rate ('1'), for a player that can have it (dsd_out_ok: it lists dsf itself, its output is not a reduced
// stream); with the converter on, the converter decides (the row shown overridden, still editable). A player that cannot
// shows DSD to PCM whatever is stored, and says why. The encoder's modulator and DSP rate as the plugin and the engine take them: the
// modulator standard unless clans or adaptive (spaces and quotes trimmed, any case), the DSP at DXD unless 176400.
function LocalSetupDSDOut() {
    const can = playerCan();
    const c = SqueezeDSPData.Client;
    const mode = srcMode();
    const on = !!can.dsdOutOk && /^(?:1|on|true)$/i.test(String(c.DSDOut));
    document.querySelectorAll('#dsd-output-selector input[type="radio"]').forEach(el => {
        el.disabled = el.value === '1' && !can.dsdOutOk;
        el.checked = el.value === (on ? '1' : '0');
    });
    const row = document.getElementById('control_DSDOut');
    if (row) row.classList.toggle('sqdsp-overridden', mode !== 'off');
    let mod = String(replaceNull(c.DSDOutMod, 'standard')).replace(/^[ "]+|[ "]+$/g, '').toLowerCase();
    if (!['standard', 'clans', 'adaptive'].includes(mod)) mod = 'standard';
    document.querySelectorAll('#dsdout-mod-selector input[type="radio"]').forEach(el => { el.checked = (el.value === mod); });
    // the DSP rate: DXD unless 176400, or native (Configuration::dsdNative: any case, quotes and spaces trimmed)
    const dsp = /^[ "]*native[ "]*$/i.test(String(c.DSDOutDSP)) ? 'native' : String(c.DSDOutDSP) === '176400' ? '176400' : '352800';
    document.querySelectorAll('#dsdout-dsp-selector input[type="radio"]').forEach(el => { el.checked = (el.value === dsp); });
    // the integrator, as the engine takes the player's DSDIntegrator (lin unless int, min or nonring): greyed, still
    // editable, with its why, unless the DSP rate is native
    let integ = String(replaceNull(c.DSDIntegrator, 'lin')).replace(/^[ "]+|[ "]+$/g, '').toLowerCase();
    if (!['lin', 'int', 'min', 'nonring'].includes(integ)) integ = 'lin';
    document.querySelectorAll('#dsd-integrator-selector input[type="radio"]').forEach(el => { el.checked = (el.value === integ); });
    // the decoder and the DSP rate are a DSD file's alone: shown overridden, still editable, while the converter leaves it
    // untouched (the modulator is the PCM tracks' too)
    const leave = mode === 'dsd' && dsdSource() === 'untouched';
    ['control_DSDCutoff', 'control_DSDGain', 'control_DSDOutDSP'].forEach(id => {
        const r = document.getElementById(id);
        if (r) r.classList.toggle('sqdsp-overridden', leave);
    });
    const irow = document.getElementById('control_DSDIntegrator');
    if (irow) irow.classList.toggle('sqdsp-overridden', dsp !== 'native' || leave);
    const inote = document.getElementById('note_DSDIntegrator');
    if (inote) {
        inote.textContent = dsp === 'native' ? '' : helpText('DSDNativeOnly_Help');
        inote.style.display = dsp === 'native' ? 'none' : '';
    }
    // nothing restated (Simon 2026-11-05: "any over explanation needs to go"): only why DSD is not on offer
    let msg = '';
    if (mode === 'off' && !can.dsdOutOk) {
        msg = helpText(can.outReduced ? 'DSDOutNoReduced_Help' : 'DSDOutNoDsf_Help');
    }
    const note = document.getElementById('note_DSDOut');
    if (note) {
        note.textContent = msg;
        note.style.display = msg ? '' : 'none';
    }
}

// Every setup that reads the player's settings and the converter's mode, which each tab's notes follow
function LocalSetupAll() {
    LocalSetupNative();
    LocalSetupOutput();
    LocalSetupUpsample();
    LocalSetupDSD();
    LocalSetupDSDOut();
    LocalSetupDspOff();
}

// LMS's "Prefer native format" on (readclientSettings prefer_native): the red bar at the top, with its button
// (sqdsp_data.js SqueezeDSPPreferNativeOff); hidden while it is off
function LocalSetupNative() {
    const bar = document.getElementById('sqdsp-native');
    if (!bar) return;
    const on = !!(typeof SqueezeDSPPlayer !== 'undefined' && SqueezeDSPPlayer && SqueezeDSPPlayer.preferNative);
    bar.style.display = on ? '' : 'none';
    const btn = document.getElementById('sqdsp-native-off');
    if (btn) btn.disabled = false;
}

// DSP off: what it drives is dimmed (still editable), and each tab of it says so, as on the SqueezeDSP page; the presets
// stay as they are (a preset can switch the DSP on)
function LocalSetupDspOff() {
    const on = String(SqueezeDSPData.Client.Bypass) === '0';
    document.querySelectorAll('.sqdsp-pane[data-dsp]').forEach(p => p.classList.toggle('sqdsp-dimmed', !on));
    document.querySelectorAll('.sqdsp-dspoff').forEach(n => { n.style.display = on ? 'none' : ''; });
    const exact = document.getElementById('control_ExactLength');
    if (exact) exact.classList.toggle('sqdsp-dimmed', !on);
}

// The tabs (Simon 2026-11-03, the SqueezeDSP page's, in its order): one pane shown, the last one picked remembered in
// this browser. The PEQ graph redraws itself when its pane gets a size (index.html's ResizeObserver).
const SQDSP_TABS = ['presets', 'dsp', 'src', 'pcm', 'dsd', 'peq', 'conv', 'xfeed'];
function LocalShowTab(key) {
    if (!SQDSP_TABS.includes(key)) key = 'presets';
    SQDSP_TABS.forEach(t => {
        const pane = document.getElementById('pane_' + t);
        if (pane) pane.style.display = (t === key) ? '' : 'none';
        const r = document.querySelector(`#sqdsp-tabs input[value="${t}"]`);
        if (r) r.checked = (t === key);
    });
    try { localStorage.setItem('sqdsp-classic-tab', key); } catch (e) {}
}
function LocalInitTabs() {
    let key = null;
    try { key = localStorage.getItem('sqdsp-classic-tab'); } catch (e) { key = null; }
    LocalShowTab(key || 'presets');
}

// check the button of a choice group whose value is the stored one, compared as numbers ('0.90' is 0.9);
// none matching, the fallback's
function setChoice(groupId, value, fallback) {
    const inputs = document.querySelectorAll('#' + groupId + ' input[type="radio"]');
    const same = (a, b) => Number(a) === Number(b) && String(a).trim() !== '' && String(b).trim() !== '';
    const want = Array.prototype.some.call(inputs, i => same(i.value, value)) ? value : fallback;
    inputs.forEach(i => { i.checked = same(i.value, want); });
}

function ToggleControl(myElement, myControltoToggle, InvertToggle = false) {
    var myValue = myElement.value;
    var element = document.getElementById(myControltoToggle);

    if (InvertToggle) {
        if (myValue == 1) {
            element.style.display = 'none';
            
        } else {
            element.style.display = ''; // or 'block' depending on your layout
            
        }
    } else {
        if (myValue == 0) {
            element.style.display = 'none';
            
        } else {
            element.style.display = '';
            
        }
    }
}

function updateLoudnessToggle() {
    const loudnessEnabled = SqueezeDSPData.Client.Loudness && SqueezeDSPData.Client.Loudness.enabled == 1;
    const loudnessControl = document.getElementById('control_Loudness');
    
    if (loudnessControl) {
        loudnessControl.style.display = loudnessEnabled ? '' : 'none';
    }
    
    // Update graph visibility and content
    updateGraphVisibility();
    updatePEQGraph();
}

function updateReplayGainToggle() {
    const enabled = SqueezeDSPData.Client.ReplayGain && SqueezeDSPData.Client.ReplayGain.enabled == 1;
    const fixedGainInput  = document.getElementById('val_ReplayGain.fixed_gain');
    const spotifyGainInput = document.getElementById('val_ReplayGain.spotify_gain');
    const spotifyRow      = document.getElementById('control_ReplayGain.spotify_gain');
    const modeLabels      = document.getElementById('replaygain-mode-labels');
    const trackOnly       = document.getElementById('ReplayGainTrackOnly');
    const albumOnly       = document.getElementById('ReplayGainAlbumOnly');

    if (fixedGainInput) {
        fixedGainInput.disabled    = !enabled;
        fixedGainInput.style.opacity = enabled ? '1' : '0.4';
    }
    if (spotifyGainInput) {
        spotifyGainInput.disabled    = !enabled;
        spotifyGainInput.style.opacity = enabled ? '1' : '0.4';
    }
    if (spotifyRow) {
        spotifyRow.style.display = enabled ? '' : 'none';
    }

    // Show mode checkboxes only when ReplayGain is enabled
    if (modeLabels) {
        modeLabels.style.display = enabled ? '' : 'none';
    }

    // Reflect current mode onto checkboxes
    if (trackOnly && albumOnly) {
        const mode = SqueezeDSPData.Client.ReplayGain.mode || 3;
        trackOnly.checked = (mode === 1);
        albumOnly.checked = (mode === 2);
    }
}

// Called when either mode checkbox changes.
// The two are mutually exclusive — checking one clears the other.
// If both are unchecked the mode falls back to smart gain (3).
function onReplayGainModeChange(which, checkbox) {
    const trackOnly = document.getElementById('ReplayGainTrackOnly');
    const albumOnly = document.getElementById('ReplayGainAlbumOnly');

    if (which === 'track') {
        if (checkbox.checked) albumOnly.checked = false;
    } else {
        if (checkbox.checked) trackOnly.checked = false;
    }

    const mode = trackOnly.checked ? 1 : albumOnly.checked ? 2 : 3;
    SqueezeDSPData.Client.ReplayGain.mode = mode;
    NewFieldValue('ReplayGain.mode', { value: mode }, false);
}


function reStyleElement(ElementId, fromStyle, toStyle) {
    const myElement = document.getElementById(ElementId);
    if (myElement) {
        myElement.classList.remove(fromStyle);
        myElement.classList.add(toStyle);
    } else {
        console.warn(`Element with ID '${ElementId}' not found.`);
    }
}

function HideAlert() {
    myMessage.className = "bubble";
    myMessage.innerText = '';
}

function DisplayAlert(FieldName, Message, Duration = 2000) {
    myMessage.className = "localAlert";
    myMessage.innerText = Message + ' ' + FieldName + " ...";
    setTimeout(function() { HideAlert(); }, Duration);    
}

function SetNewPreset(myEl) {
    var val = myEl.value;
    //Remove callback, it was originally to check that everything had been saved
    SqueezeDSPSavePreset(val);    
    DisplayAlert('Preset', 'Saving to ' + val);
}

function GetLogSummary() {
    SqueezeDSPLogSummary(LocalDisplayLogSummary);
}

function LocalDisplayLogSummary(logData) {
    let myHTML = '';
    const tracklimit = 10;
    let enabled = SqueezeDSPData.Client.ReplayGain && SqueezeDSPData.Client.ReplayGain.enabled == 1;
	if (enabled == undefined) enabled = 0;
    try {
        for (let i = 1; i <= tracklimit; i++) {
            let peakdBfs = logData.result ? logData.result['peakdBfs_' + i] : undefined;
            // (SqueezeDSP fork, 0.1.72.46) a DSF track's peak is against DSD's full swing: read against DSD's 0 dB (50%,
            // where PCM full scale goes), clipped only where the engine held samples at its limit (below)
            const dsd = !!(logData.result && /^dsf$/i.test(String(logData.result['outtype_' + i] || '')));
            if (dsd && isFinite(Number(peakdBfs))) peakdBfs = (Number(peakdBfs) + 6.02).toFixed(4);
            let peakStyle = 'peak peak-unknown';

            // Skip if peak is not a number
			/*
            if (typeof peakdBfs !== 'number') {
                peakStyle = 'peak peak-unknown';
			}*/
           if (enabled == 1) {
                if (peakdBfs < -12) peakStyle = 'peak peak-toolow';
                else if (peakdBfs < -8) peakStyle = 'peak peak-bitlow'; // fixed name
                else if (peakdBfs < -3) peakStyle = 'peak peak-ok';
                else if (peakdBfs < 0) peakStyle = 'peak peak-high';
				else if (peakdBfs >= 0) peakStyle = 'peak peak-clipping';
                else peakStyle = 'peak peak peak-unknown';
            } else {
                if (peakdBfs < -5) peakStyle = 'peak peak-toolow';
                else if (peakdBfs < -1) peakStyle = 'peak peak-ok';
                else if (peakdBfs <= 0.1) peakStyle = 'peak peak-high';
                else if (peakdBfs >= 0) peakStyle = 'peak peak-clipping';
				else peakStyle = 'peak peak peak-unknown';
                // No final else – unknown stays unknown*/
            }
            // (SqueezeDSP fork) a DSD track over its 0 dB is high, not clipped: the engine takes a PCM source 2 dB past it
            if (dsd) {
                if (Number(logData.result['held_' + i]) > 0) peakStyle = 'peak peak-clipping';
                else if (peakStyle === 'peak peak-clipping') peakStyle = 'peak peak-high';
            }

            // Build tooltip with safe fallbacks
            const safeValue = (key) => (logData.result && logData.result[key] != null) ? logData.result[key] : '—';
				const tooltipContent = `
					<div class="tooltip-row">
						<div class="tooltip-label">Playerid:</div>
						<div class="tooltip-value">${safeValue('playerid_' + i)}</div>
					</div>
					<div class="tooltip-row">
						<div class="tooltip-label">Start Date:</div>
						<div class="tooltip-value">${safeValue('date_' + i)}</div>
					</div>
					<div class="tooltip-row">
						<div class="tooltip-label">Start Time:</div>
						<div class="tooltip-value">${safeValue('time_' + i)}</div>
					</div>
					<div class="tooltip-row">
						<div class="tooltip-label">Preamp Gain:</div>
						<div class="tooltip-value">${safeValue('preamp_' + i)}</div>
					</div>
					<div class="tooltip-row">
						<div class="tooltip-label">Internal Gain:</div>
						<div class="tooltip-value">${safeValue('internalgain_' + i)}</div>
					</div>
					<div class="tooltip-row">
						<div class="tooltip-label">Sample Rate:</div>
						<div class="tooltip-value">${safeValue('outputrate_' + i)}</div>
					</div>
					<div class="tooltip-row">
						<div class="tooltip-label">Peak Level:</div>
						<div class="tooltip-value">${peakdBfs}</div>
					</div>
					<div class="tooltip-row">
						<div class="tooltip-label">Input Peak:</div>
						<div class="tooltip-value">${safeValue('inputpeak_' + i)}</div>
					</div>
				`;

            myHTML += `
                <div class="led-container">
                    <div class="${peakStyle}"></div>
                    <div class="tooltiptext">
                        ${tooltipContent}
                    </div>
                </div>
            `;
        }
    } catch (error) {
        console.error("LocalDisplayLogSummary error:", error);
        DisplayAlert("LocalDisplayLogSummary", "Error building log summary", 3000);
    }

    document.getElementById('logSummary').innerHTML = myHTML;
}

function getBaseFilename(fullPath) {
    // Extract filename with extension using both Windows and Unix path separators
    const filenameWithExt = fullPath.split(/[\\/]/).pop();
    
    // Remove all extensions (everything after the first dot)
    const baseFilename = filenameWithExt.split('.')[0];
    
    return baseFilename;
}

function highlightMatchingOption(selectElementId, targetFullPath) {
    const selectElement = document.getElementById(selectElementId);
    if (!selectElement) {
        console.warn('Element not found:', selectElementId);
        return;
    }

    if (!targetFullPath || targetFullPath === '-') {
        selectElement.selectedIndex = 0;
        setSelectValue(selectElement, selectElement.value);   // redraws the skin's custom-select
        return;
    }

    // Use getBaseFilename to extract the short name (keeps original case)
    const targetBaseName = getBaseFilename(targetFullPath);
    const normalizePath = path => path.replace(/\\/g, '/');
    
    console.log(`Matching target: ${targetFullPath}`);
    console.log(`Base filename: ${targetBaseName}`);

    const isMaterialUI = selectElement.classList.contains('MuiSelect-nativeInput') || 
                        selectElement.closest('.MuiInputBase-root');

    const setValue = (element, value) => {
        if (isMaterialUI) {
            const hiddenInput = element.closest('.MuiInputBase-root')?.querySelector('input[type="hidden"]');
            if (hiddenInput) {
                hiddenInput.value = value;
                const event = new Event('change', { bubbles: true });
                hiddenInput.dispatchEvent(event);
            }
            
            const displayNode = element.closest('.MuiInputBase-root')?.querySelector('.MuiSelect-select');
            if (displayNode) {
                displayNode.textContent = value;
            }
        } else {
            // SqueezeDSP fork (2026-11-25): shown, not changed: no change event, whose onchange (NewFieldValue) now SAVES a
            // choice at once (sqdsp_data.js LocalChanged): a redraw, Reset and a preset's load each saved the FIR again
            for (let i = 0; i < element.options.length; i++) {
                if (element.options[i].value === value) {
                    setSelectValue(element, value);
                    break;
                }
            }
        }
    };

    // First try: match by base filename in option text
    if (!isMaterialUI) {
        for (let i = 0; i < selectElement.options.length; i++) {
            const optionBaseName = getBaseFilename(selectElement.options[i].text);
            if (optionBaseName === targetBaseName) {
                console.log(`Matched by base filename in text: ${selectElement.options[i].text}`);
                setValue(selectElement, selectElement.options[i].value);
                return;
            }
        }
    }

    // Second try: match by full path in option value
    const targetPath = normalizePath(targetFullPath);
    for (let i = 0; i < selectElement.options.length; i++) {
        const optionValue = normalizePath(selectElement.options[i].value);
        if (optionValue === targetPath) {
            console.log(`Matched by full path: ${optionValue}`);
            setValue(selectElement, selectElement.options[i].value);
            return;
        }
    }

    // Third try: match by base filename in option value
    for (let i = 0; i < selectElement.options.length; i++) {
        const optionBaseName = getBaseFilename(selectElement.options[i].value);
        if (optionBaseName === targetBaseName) {
            console.log(`Matched by base filename in value: ${selectElement.options[i].value}`);
            setValue(selectElement, selectElement.options[i].value);
            return;
        }
    }

    console.warn('No matching option found for:', targetFullPath);
}

function ListenForSlide() {
    const allRanges = document.querySelectorAll(".Slide");
    allRanges.forEach(wrap => {
        const range = wrap.querySelector(".range");
        const bubble = wrap.querySelector(".bubble");

        range.addEventListener("input", () => {
            setBubble(range, bubble);
        });
        setBubble(range, bubble);
    });
}

function setBubble(range, bubble) {
    const val = range.value;
    const min = range.min ? range.min : 0;
    const max = range.max ? range.max : 100;
  
    var mySlideName = range.id;
    if (mySlideName.endsWith("freq") || mySlideName.endsWith("Frequency")) {
        bubble.innerHTML = logScaleMapper(val);
    } else {
        bubble.innerHTML = val;
    }    
}

function showBubble(myObj) {
    var myBubble = document.getElementById(myObj.id + 'Bubble');
    setBubble(myObj, myBubble);
    myBubble.style.display = 'block';
}

function hideBubble(myObj) {
    document.getElementById(myObj.id + 'Bubble').style.display = 'none';
}

function improveMobileSliders() {
    const sliders = document.querySelectorAll('.range');
    
    sliders.forEach(slider => {
        // Prevent accidental changes on mobile
        slider.addEventListener('touchstart', function(e) {
            this.setAttribute('data-touch-start', this.value);
        });
        
        slider.addEventListener('touchend', function(e) {
            const startValue = this.getAttribute('data-touch-start');
            if (Math.abs(this.value - startValue) < 0.5) {
                // Small change, might be accidental
                console.log('Potential accidental touch on slider');
            }
        });
    });
}

// Simplified mobile initialization
function initializeTouchProtection() {
    if ('ontouchstart' in window) {
        console.log('Initializing touch protection for mobile');
        
        const ranges = document.querySelectorAll('.range');
        ranges.forEach(range => {
            range.addEventListener('touchstart', function(e) {
                // Small delay to prevent accidental changes
                setTimeout(() => {
                    this.focus();
                }, 100);
            });
        });
    }
}


// ── Kebab mini-menu ──────────────────────────────────────────────────────────

(function () {
    // One shared menu element, created once
    var _menu = null;
    var _dismissHandler = null;

    function getMenu() {
        if (_menu) return _menu;

        _menu = document.createElement('div');
        _menu.id = 'sqdsp-preset-menu';
        _menu.style.cssText = [
            'position:absolute',
            'z-index:9999',
            'background:var(--sq-bg, #2a2a2a)',
            'border:1px solid var(--sq-border, #555)',
            'border-radius:4px',
            'box-shadow:0 2px 8px rgba(0,0,0,0.45)',
            'min-width:130px',
            'padding:4px 0',
            'display:none',
            'font-size:0.9em',
        ].join(';');
        document.body.appendChild(_menu);
        return _menu;
    }

    function closeMenu() {
        var m = getMenu();
        m.style.display = 'none';
        if (_dismissHandler) {
            document.removeEventListener('pointerdown', _dismissHandler, true);
            _dismissHandler = null;
        }
    }

    function menuItem(label, cssClass, action) {
        var li = document.createElement('div');
        li.textContent = label;
        li.style.cssText = [
            'padding:7px 14px',
            'cursor:pointer',
            'white-space:nowrap',
            'color:' + (cssClass === 'danger'
                ? 'var(--sq-red,#e05555)'
                : 'var(--sq-text,#ddd)'),
        ].join(';');
        li.addEventListener('pointerover', function () {
            li.style.background = 'var(--sq-hover,#3a3a3a)';
        });
        li.addEventListener('pointerout', function () {
            li.style.background = '';
        });
        li.addEventListener('pointerdown', function (e) {
            e.stopPropagation();   // don't trigger the dismiss handler
            closeMenu();
            action();
        });
        return li;
    }

function openPresetMenu(anchorEl) {
    var presetName = document.getElementById('sel_Preset').value;
    var m = getMenu();
    m.innerHTML = '';

    // ── menu items ──────────────────────────────────────────────────────
    m.appendChild(menuItem('🗑 Delete preset', 'danger', function () {
        // --- REAL DELETE LOGIC (copied from old version) ---
        if (!presetName || presetName === '-') {
            DisplayAlert('Preset', 'No preset selected', 1500);
            return;
        }
        if (!confirm('Delete preset "' + presetName + '"?')) return;

        SqueezeDSPDeletePreset(presetName, function () {
            // Remove option from the select
            var sel = document.getElementById('sel_Preset');
            for (var i = sel.options.length - 1; i >= 0; i--) {
                if (sel.options[i].value === presetName) {
                    sel.remove(i);
                    break;
                }
            }
            sel.value = '-';
            DisplayAlert('Preset', 'Deleted', 2000);
        });
    }));

    // ── position the menu (your new smart positioning) ──────────────
    var rect = anchorEl.getBoundingClientRect();
    var scrollX = window.scrollX || document.documentElement.scrollLeft;
    var scrollY = window.scrollY || document.documentElement.scrollTop;

    m.style.display = 'block';
    var menuW = m.offsetWidth;
    var menuH = m.offsetHeight;

    var spaceBelow = window.innerHeight - rect.bottom;
    var top = (spaceBelow >= menuH || spaceBelow >= rect.top)
        ? rect.bottom + scrollY + 2
        : rect.top  + scrollY  - menuH - 2;

    var left = rect.left + scrollX;
    if (rect.left + menuW > window.innerWidth) {
        left = rect.right + scrollX - menuW;
    }
    if (left < scrollX + 5) {
        left = scrollX + 5;
    }

    m.style.top  = top + 'px';
    m.style.left = left + 'px';

    // Dismiss handler
    _dismissHandler = function (e) {
        if (!m.contains(e.target) && e.target !== anchorEl) {
            closeMenu();
        }
    };
    document.addEventListener('pointerdown', _dismissHandler, true);
}

    // Expose for the HTML onclick
    window.openPresetMenu = openPresetMenu;
})();


/*---------------------------------------------------
SqueezeDSP fork (0.1.72.57, Simon 2026-11-20): the processing speed, the choice of interface and the engine's debug log
-----------------------------------------------------*/

// one JSON-RPC request with a time limit (the new page's way); done(result or null)
function SqueezeDSPRpc(player, cmd, done) {
    const x = new XMLHttpRequest();
    x.open('POST', '/jsonrpc.js', true);
    x.setRequestHeader('Content-Type', 'application/json');
    x.timeout = 8000;
    x.onload = function () {
        let r = null;
        try { r = JSON.parse(x.responseText); } catch (e) { r = null; }
        done(r && !r.error && r.result ? r.result : null);
    };
    x.onerror = x.ontimeout = function () { done(null); };
    x.send(JSON.stringify({ id: 1, method: 'slim.request', params: [player || '', cmd] }));
}

// The processing speed (Simon: "would like to see processing speed on the old one"): the new page's headline figure,
// read as it reads it (squeezedsp.status: the engine's own figure, a finished track's kept), the figure alone; one
// request at a time, a 2 s gap after each, never a second one while one is out
let _speedTimer = null;
function LocalStartSpeed() {
    if (_speedTimer !== null) return;
    _speedTimer = 0;
    const turn = function () {
        SqueezeDSPRpc(getCurrentPlayer(), ['squeezedsp.status'], function (r) {
            LocalShowSpeed(r ? r.status : null);
            _speedTimer = setTimeout(turn, 2000);
        });
    };
    turn();
}
function LocalShowSpeed(st) {
    const el = document.getElementById('sqdsp-speed');
    if (!el) return;
    const p = (st && st.pipeline) || {};
    const real = !!(st && (p.running || p.finished)) && p.realtime !== undefined && p.realtime !== null;
    if (real) {
        const n = Number(p.realtime) || 0;
        el.textContent = (helpText('Realtime_Desc') || '%sx realtime').replace('%s', n >= 100 ? String(Math.round(n)) : n.toFixed(1));
        el.className = 'sqdsp-speed';
    } else if (st && p.running) {
        el.textContent = helpText('Measuring_Desc') || 'Measuring...';
        el.className = 'sqdsp-speed none';
    } else {
        el.textContent = '-';
        el.className = 'sqdsp-speed none';
    }
}

// The new interface instead of this page (the user's choice of one, kept on the server: squeezedsp.interface), then
// the new page for this player. Changes not saved yet (waiting for a Save): asked first, as leaving the page asks.
function SqueezeDSPSwitchInterface(to) {
    if (sqdspWaiting() &&
        !confirm('You have unsaved changes. Press OK to leave without them and Cancel to go back.')) return;
    const btn = document.getElementById('btn_NewInterface');
    if (btn) btn.disabled = true;
    SqueezeDSPRpc('', ['squeezedsp.interface', 'set:' + to], function (r) {
        if (!r) {
            if (btn) btn.disabled = false;
            DisplayAlert('Interface', 'Not changed', 2000);
            return;
        }
        if (typeof handleUnsavedChanges === 'function') window.removeEventListener('beforeunload', handleUnsavedChanges);
        const player = getCurrentPlayer();
        window.location.replace('/squeezedsp' + (player ? '?player=' + encodeURIComponent(player) : ''));
    });
}

// The engine's debug log, on or off for every player (squeezedsp.debuglog): saved at once, outside Apply
function SqueezeDSPSetDebugLog(on) {
    const box = document.getElementById('DebugLog');
    SqueezeDSPRpc('', ['squeezedsp.debuglog', 'set:' + (on ? 1 : 0)], function (r) {
        if (!r) {
            if (box) box.checked = !on;
            DisplayAlert('Debug log', 'Not saved', 2000);
            return;
        }
        if (box) box.checked = Number(r.debug) === 1;
        DisplayAlert('Debug log', 'Saved', 2000);
    });
}
