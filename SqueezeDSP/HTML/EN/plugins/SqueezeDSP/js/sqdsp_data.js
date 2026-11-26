// Lets create the  SqueezeDSPData object
function getCurrentPlayer()
{
   return (playerid);
}

SqueezeDSPData = {
    Client: {},
    ClientName: "",
    Revision: 0
};

// what the current player can take, from readclientSettings (see LocalSetupOutput)
var SqueezeDSPPlayer = null;

function InitialiseData()

    {
    return {
        Bypass: 1,
        ExactLength: 1,         // "Exact track length": 1 for a player that needs a stream's true length first (Gapless.pm); on by default since 0.1.72.57 (Simon 2026-11-20)
        OutputFormat: "FLAC",   // or "PCM": 24-bit WAV with its header, for a player that reads one
        Upsample: "off",        // the Sample Rate Converter: "on" (PCM: the engine resamples before the DSP; Upsample.pm), or "dsd"
        UpsamplePCMFilterCD: "short-int",    // PCM's filter for a source at 48 kHz and under: one of Upsample.pm's @FILTERS
        UpsamplePCMFilterHiRes: "short-lin", // ... and for hi-res (Upsample::pcmFilters, the engine's --upfilter)
        UpsampleTarget: "family", // or "fixed": every track, DSD included, to the player's top rate
        UpsampleMax: 0,         // 0: the player's top rate; or 768000, 384000, 192000, 96000 below it
        DSDCutoff: 30000,       // a DSF on our engine (0.3.13): its low-pass, Hz; 24000, 30000, 50000, or 0 for none
        DSDGain: 0,             // ... and its gain, dB: 0 or 6 (DSD plays 6 dB under PCM)
        UpsampleDSDRate: 2822400, // the Sample Rate Converter to DSD (Upsample "dsd"): 2822400, 5644800, 11289600, 22579200
        UpsampleDSDSource: "upsample", // ... a DSD file: "upsample" to that rate, "source" its own rate, or "untouched" (Upsample::dsdSource)
        UpsampleFilterCD: "short-int",    // ... its filter for a source at 48 kHz and under: one of Upsample.pm's @FILTERS
        UpsampleFilterHiRes: "short-lin", // ... and for hi-res (Upsample::filters, the engine's --upfilter)
        DSDOut: 0,              // 1: with the converter off, a DSF goes out as DSD at its own rate (Configuration::dsdOut)
        DSDOutMod: "standard",  // DSD out's modulator: "standard", "clans" or "adaptive"
        DSDOutDSP: 352800,      // ... and the DSP's rate before it: 352800 (DXD), 176400 for a slower server, or "native": a DSF's own DSD rate
        DSDIntegrator: "lin",   // native: the integrator's shape, "lin", "int", "min" or "nonring"
        Preamp: 0,
        Balance: 0,
        Width: 0,
        Delay: { delay: 0 },
        Loudness: {
            enabled: 0,
            listening_level: 85
        },
        Filters: [],
        FIRWavFile: "-",
        Last_preset: "",
        Preset: "None",
		FIRStrength: 100,
		Crossfeed: "off",
		ReplayGain: {
			enabled: 0,
			mode:         3,   // 1=track only, 2=album only, 3=smart gain (default)
			fixed_gain: -6.0,
			spotify_gain: -4.0,
		},
    };
}

function InitialiseSqueezeDSPData()
{
    if (typeof SqueezeDSPData === 'undefined') {
        SqueezeDSPData = {
            ClientName: "",
            Revision: 0
        };
        return;
    }
    SqueezeDSPData.Client = InitialiseData();
    // Ensure top-level structure exists
    SqueezeDSPData.Client = SqueezeDSPData.Client || {};
    SqueezeDSPData.ClientName = SqueezeDSPData.ClientName || "";
    SqueezeDSPData.Revision = SqueezeDSPData.Revision || 0;

}

function deepMerge(target, source) {
    const result = Object.assign({}, target);
    for (const key of Object.keys(source)) {
        if (source[key] !== null &&
            typeof source[key] === 'object' &&
            !Array.isArray(source[key]) &&
            typeof target[key] === 'object' &&
            target[key] !== null) {
            result[key] = deepMerge(target[key], source[key]);
        } else if (source[key] !== undefined) {
            result[key] = source[key];
        }
    }
    return result;
}


function transformLegacyFilters(data) {
	if (!data || !data.Client) return data;

    const client = data.Client;
    if (!client.Filters) {
        client.Filters = [];
    }

    // Helper functions
    const toBoolean = val => String(val) === "1";
    const toNumber = val => parseFloat(val) || 0;

    // Process EQBand_* filters (Peaking filters)
    const eqBandKeys = Object.keys(client).filter(key => key.startsWith('EQBand_'));

    // Determine conversion count
    const eqBandsCount = client.EQBands ? parseInt(client.EQBands) : 0;
    const conversionCount = Math.min(eqBandsCount, eqBandKeys.length);

// Convert only the required bands
    for (let i = 0; i < conversionCount; i++) {
        const key = `EQBand_${i}`;
        if (client[key]) {
            const filter = client[key];
            client.Filters.push({
                FilterType: "peak",
                Frequency: toNumber(filter.freq),
                Gain: toNumber(filter.gain),
                SlopeType: "Q",
                Slope: toNumber(filter.q)
            });
        }
}

// Delete ALL EQBand_* keys and EQBands
eqBandKeys.forEach(key => delete client[key]);
delete client.EQBands;

    // Process Lowshelf
    if (client.Lowshelf) {
        const ls = client.Lowshelf;
        if (toBoolean(ls.enabled)) {
            client.Filters.push({
                FilterType: "lowshelf",
                Frequency: toNumber(ls.freq),
                Gain: toNumber(ls.gain),
                SlopeType: "slope",
                Slope: toNumber(ls.slope)
            });
        }
        delete client.Lowshelf;
    }

    // Process Highshelf
    if (client.Highshelf) {
        const hs = client.Highshelf;
        if (toBoolean(hs.enabled)) {
            client.Filters.push({
                FilterType: "highshelf",
                Frequency: toNumber(hs.freq),
                Gain: toNumber(hs.gain),
                SlopeType: "slope",
                Slope: toNumber(hs.slope)
            });
        }
        delete client.Highshelf;
    }

    // Process Lowpass
    if (client.Lowpass) {
        const lp = client.Lowpass;
        if (toBoolean(lp.enabled)) {
            client.Filters.push({
                FilterType: "lowpass",
                Frequency: toNumber(lp.freq),
                Gain: 0,
                SlopeType: "Q",
                Slope: toNumber(lp.q)
            });
        }
        delete client.Lowpass;
    }

    // Process Highpass
    if (client.Highpass) {
        const hp = client.Highpass;
        if (toBoolean(hp.enabled)) {
            client.Filters.push({
                FilterType: "highpass",
                Frequency: toNumber(hp.freq),
                Gain: 0,
                SlopeType: "Q",
                Slope: toNumber(hp.q)
            });
        }
        delete client.Highpass;
    }

    // Remove deprecated fields
    delete client.EQBands;
    
    return data;
}



function SqueezeDSPGetList(callback)
{
	// calls plugin filtersQuery
	new Ajax.Request('/jsonrpc.js', {
		method: 'post',
		asynchronous: true,
		postBody: Object.toJSON({
			id: 1, 
			method: 'slim.request', 
			params: [
				getCurrentPlayer(), 
				[
					'squeezedsp.filters'
				]
			]
		}),

		onFailure: function() { alert('Failed'); },

		onSuccess: function(response) {
			// debugger;
		var data = response.responseText.evalJSON(true);

		callback(data);

		}
	});
}


function SqueezeDSPLogSummary(callback)
{
	//alert ('Getting Settings');
	// calls plugin currentQuery
	new Ajax.Request('/jsonrpc.js', {
		method: 'post',
		asynchronous: true,
		//asynchronous: false,
		postBody: Object.toJSON({
			id: 1, 
			method: 'slim.request', 
			params: [
				getCurrentPlayer(), 
				[
					'squeezedsp.logsummary'
				]
			]
		}),

		onFailure: function() { alert('Failed'); },

		onSuccess: function(response) {
			// debugger;
		var data = response.responseText.evalJSON();
		callback(data);
		
		}
	});
}



function SqueezeDSPSelectPreset()
{
	// Save this value (async)
	// calls plugin setvalCommand - should drop through to loading preset
	var myProperty='Preset';
	var myVal = 'Joxy.preset.conf';
	new Ajax.Request('/jsonrpc.js', {
		method: 'post',
		asynchronous: true,
		postBody: Object.toJSON({
			id: 1, 
			method: 'slim.request', 
			params: [
				getCurrentPlayer(), 
				[
					'squeezedsp.setval',
					'key:'+myProperty,
					'val:'+myVal
				]
				]
			}),
			onFailure: function() { alert('Failed') },
			onSuccess: function(response) { 	// debugger;
			var data = response.responseText.evalJSON();
			SqueezeDSPGetSettings(LocalUpdateSettings);
			}
		});
	}

function SqueezeDSPSelectValue(myProperty, myVal, callback)
{
	// calls plugin setvalCommand - should drop through to setpref command
	// Save this value (async)

	//alert (myProperty);
	new Ajax.Request('/jsonrpc.js', {
		method: 'post',
		asynchronous: true,
		postBody: Object.toJSON({
			id: 1, 
			method: 'slim.request', 
			params: [
				getCurrentPlayer(), 
				[
					'squeezedsp.setval',
					'key:'+myProperty,
					'val:'+myVal
				]
				]
			}),
			onFailure: function() { alert('Failed') },
			onSuccess: function(response) { 	// debugger;
			var data = response.responseText.evalJSON();
			if(typeof callback !== 'undefined'){
					SqueezeDSPGetSettings(callback);
				};
			//SqueezeDSPGetSettings(LocalUpdateSettings);
			}
		});
	}
function SqueezeDSPSavePreset(myVal, callback)
{
	// calls plugin saveasCommand
	// Save this value (async)
	new Ajax.Request('/jsonrpc.js', {
		method: 'post',
		asynchronous: true,
		postBody: Object.toJSON({
			id: 1, 
			method: 'slim.request', 
			params: [
				getCurrentPlayer(), 
				[
					'squeezedsp.saveas',
					'preset:' + myVal,
				],
			]

		}),
		onFailure: function() { alert('Failed'); },

		onSuccess: function(response) {
			// debugger;
		var data = response.responseText.evalJSON();
				
		callback();
		}	});
}

function SqueezeDSPDeletePreset(presetName, callback) {
    if (!presetName || presetName === '-') return;
    new Ajax.Request('/jsonrpc.js', {
        method: 'post',
        asynchronous: true,
        postBody: Object.toJSON({
            id: 1,
            method: 'slim.request',
            params: [
                getCurrentPlayer(),
                [
                    'squeezedsp.deletepreset',
                    'preset:' + presetName,
                ],
            ],
        }),
        onFailure: function () { alert('Delete failed'); },
        onSuccess: function (response) {
            var data = response.responseText.evalJSON();
            if (callback) callback();
        },
    });
}


function SqueezeDSPSaveEQBand(myBand, myFreq, myGain, myQ)
{
				//calls plugin seteqCommand
				new Ajax.Request('/jsonrpc.js', {
					method: 'post',
					asynchronous: true,
					postBody: Object.toJSON({
						id: 1, 
						method: 'slim.request', 
						params: [
							getCurrentPlayer(), 
							[
								'squeezedsp.seteq',
								'band:'+myBand,
								'freq:'+myFreq,
								'gain:'+myGain,
								'q:'+myQ
							]
						]
					}),
					onFailure: function() { alert('Failed'); },
					onSuccess: function(response) {
					// debugger;
						var data = response.responseText.evalJSON();
					}
				});
}

// Fetch current settings from server
// Modified processing function
// The red bar's button: LMS's own `pref` command (no player) turns "Prefer native format" off, then the settings are read
// again, so the bar goes once the server says it is off (sqdsp_core.js LocalSetupNative)
function SqueezeDSPPreferNativeOff() {
    const btn = document.getElementById('sqdsp-native-off');
    if (btn) btn.disabled = true;
    const again = () => SqueezeDSPFetchCurrentSettings(LocalSetupAll);
    new Ajax.Request('/jsonrpc.js', {
        method: 'post',
        postBody: Object.toJSON({ id: 1, method: 'slim.request', params: ['', ['pref', 'prioritizeNative', '0']] }),
        onSuccess: again,
        onFailure: again
    });
}

// Fetch current settings from server
function SqueezeDSPFetchCurrentSettings(callback) {
    new Ajax.Request('/jsonrpc.js', {
        method: 'post',
        postBody: Object.toJSON({
            id: 1,
            method: 'slim.request',
            params: [getCurrentPlayer(), ['squeezedsp.readclientSettings']]
        }),
        onSuccess: (response) => {
            const data = response.responseText.evalJSON();
            const rawJson = data.result.json;

            // What this player can take (kept out of SqueezeDSPData, which saveall writes back):
            // the DSP at all, and PCM as WAV with its header. An older server sends neither.
            SqueezeDSPPlayer = {
                dspOk: Number(data.result.dsp_ok) !== 0,
                wavOk: Number(data.result.wav_ok) === 1,
                // Upsample: available here (the stream-start hook, a known max), why not, the two targets
                upOk: Number(data.result.up_ok) === 1,
                upWhy: data.result.up_why || '',
                upTargets: data.result.up_targets || '',
                // and the fixed target, the player's top rate (an older server sends none)
                upFixed: data.result.up_fixed || '',
                // the player's top rate with no max rate chosen: with FLAC out, with WAV out
                upLimits: data.result.up_limits || '',
                // a reduced stream (MP3, OPS, OGG), and the Bitrate Limiting an MP3 is at (0: an app's 320 kbps)
                outReduced: data.result.out_reduced || '',
                outKbps: Number(data.result.out_kbps) || 0,
                // native DSD out: a player that lists dsf itself, its output not a reduced stream (an older server: none)
                dsdOutOk: Number(data.result.dsd_out_ok) === 1,
                // the Sample Rate Converter to DSD: DSD out as above, and the stream-start hook (an older server: none)
                upDsdOk: Number(data.result.up_dsd_ok) === 1,
                // LMS's "Prefer native format", which SqueezeDSP needs off (an older server: none)
                preferNative: Number(data.result.prefer_native) === 1,
                // the engine's debug log, every player's (squeezedsp.debuglog; an older server: none)
                debugLog: Number(data.result.debug_log) === 1
            };

            try {
                // Start with a fully-defaulted structure
                const defaults = {
                    Client: InitialiseData()
                };

                // Parse server data (may be sparse or empty for new players)
                let serverData = {};
                try {
                    serverData = rawJson ? JSON.parse(rawJson) : {};
                } catch (e) {
                    console.warn("Could not parse server JSON, treating as fresh player:", e);
                }

                // Deep merge server data over defaults
                SqueezeDSPData = deepMerge(defaults, serverData);
                SqueezeDSPData.ClientName = data.result.clientName;

                SqueezeDSPData.Revision = data.result.revision || 1;
				const isFreshPlayer = data.result.fresh_player === 1;
				delete SqueezeDSPData.fresh_player; // belt-and-braces in case it leaked into serverData


                // Transform any legacy filter format
                SqueezeDSPData = transformLegacyFilters(SqueezeDSPData);
                // what was read is what is stored: a change saved at once goes from this (LocalSaveField)
                LocalStoredFromPage();

                // If fresh player: defaults are now in place, persist them immediately
                if (isFreshPlayer) {
                    console.log("Fresh player detected — saving default settings");
                    SqueezeDSPSaveAll(function(ok) {
                        if (!ok) console.warn("Failed to save defaults for fresh player");
                    });
                }
				console.log("Post-merge SqueezeDSPData:", JSON.stringify(SqueezeDSPData, null, 2));
				initializePEQGraph();
                if (callback) callback();

            } catch (e) {
                console.error("JSON parsing error:", e);
            }
        }
    });
}


function SqueezeDSPFetchPresetSettings(presetFilePath, callback) {
    // Only proceed if this is not a programmatic change
    const cleanedPresetFilePath = presetFilePath.replace(/\\/g, '/');
    
    // Better callback handling with validation
    let callbacks = [];
    if (callback) {
        if (Array.isArray(callback)) {
            callbacks = callback.filter(cb => typeof cb === 'function');
        } else if (typeof callback === 'function') {
            callbacks = [callback];
        } else {
            console.warn('SqueezeDSPFetchPresetSettings: callback is not a function or array of functions', callback);
        }
    }
    
    new Ajax.Request('/jsonrpc.js', {
        method: 'post',
        postBody: Object.toJSON({
            id: 1, 
            method: 'slim.request', 
            params: [getCurrentPlayer(), ['squeezedsp.readpresetSettings', 'presetFileName:' + cleanedPresetFilePath]]
        }),
        onSuccess: (response) => {
            const data = response.responseText.evalJSON();
            const rawJson = data.result.json;
            
            try {
                LocalClearSettings();
                SqueezeDSPData = JSON.parse(rawJson);
                SqueezeDSPData.ClientName = data.result.clientName;
                SqueezeDSPData.Revision = data.result.revision || 1;
                SqueezeDSPData = transformLegacyFilters(SqueezeDSPData);
                
                // Execute callbacks with better error handling
                if (callbacks.length > 0) {
                    callbacks.forEach(cb => {
                        try {
                            // Check if it's still a function before calling
                            if (typeof cb === 'function') {
                                cb();
                            } else {
                                console.warn('SqueezeDSPFetchPresetSettings: callback is no longer a function', cb);
                            }
                        } catch (e) {
                            console.error("Error executing callback in SqueezeDSPFetchPresetSettings:", e);
                        }
                    });
                }
            } catch (e) {
                console.error("JSON parsing error in SqueezeDSPFetchPresetSettings:", e);
            }
        },
        onFailure: (response) => {
            console.error('Failed to fetch preset settings', response.status, response.statusText);
        }
    });
}

/* ---------------------------------------------------------------------------------------------------------------------
   SAVE BESIDE WHAT IT SAVES (SqueezeDSP fork, Simon 2026-11-25: "change the classic it needs to follow suit", the
   SqueezeDSP page's way: "call it Save in Red if it needs saving, next to the areas that it applies to rather than at top
   the others all save with buttons"; "reset should reset it should not need a save"). The sliders, their number boxes and
   the PEQ bands wait for the red Save at the foot of their own tab (DSP; PEQ, under its bands; Convolution), because
   every save reopens a playing track; every other change (a switch, a choice, a dropdown) is saved at once, from what is
   stored plus that one change, so nothing waiting goes with it. Fox's one Apply (every change waited for it) is gone.
   SQDSP_STORED: the player's settings as last read or sent; SQDSP_WAITING: path -> the tab whose Save it waits for
   ('top': a save that failed, its Save beside Reset).
   --------------------------------------------------------------------------------------------------------------------- */
var SQDSP_STORED = null;
var SQDSP_WAITING = {};
const SQDSP_WAIT_TAB = { 'Preamp': 'dsp', 'ReplayGain.fixed_gain': 'dsp', 'ReplayGain.spotify_gain': 'dsp', 'Delay.delay': 'dsp',
                         'Balance': 'dsp', 'Width': 'dsp', 'Loudness.listening_level': 'dsp', 'FIRStrength': 'conv' };
// written without a save of its own: the preset's name, which goes with the preset's own save (Load and Save save it all)
const SQDSP_QUIET = ['Last_preset'];

function sqdspClone(o) { return o === undefined ? undefined : JSON.parse(JSON.stringify(o)); }
function sqdspWaitTab(path) { return /^Filters(\.|$)/.test(path) ? 'peq' : (SQDSP_WAIT_TAB[path] || null); }
function sqdspWaiting() { return Object.keys(SQDSP_WAITING).length > 0; }

// what the page has just read is what is stored: nothing waits
function LocalStoredFromPage() {
    SQDSP_STORED = sqdspClone(SqueezeDSPData.Client || {});
    SQDSP_WAITING = {};
    LocalRenderSave();
}

// a change made on the page (NewFieldValue, a band, an import): it waits for its tab's Save, or is saved at once
function LocalChanged(path) {
    if (SQDSP_QUIET.indexOf(path) >= 0) return;
    const tab = sqdspWaitTab(path);
    if (tab) {
        SQDSP_WAITING[path] = tab;
        LocalRenderSave();
        return;
    }
    LocalSaveField(path);
}

// a switch or a choice: what is stored, with this one change, saved at once
function LocalSaveField(path) {
    if (!SQDSP_STORED) { SqueezeDSPSaveAll(); return; }   // nothing read yet: all of it, as before
    const keys = path.split('.');
    let src = SqueezeDSPData.Client, dst = SQDSP_STORED;
    for (let i = 0; i < keys.length - 1; i++) {
        src = src ? src[keys[i]] : undefined;
        if (dst[keys[i]] === null || typeof dst[keys[i]] !== 'object') dst[keys[i]] = {};
        dst = dst[keys[i]];
    }
    const last = keys[keys.length - 1];
    if (src && src[last] !== undefined) dst[last] = sqdspClone(src[last]);
    else delete dst[last];
    SqueezeDSPSaveAll(null, SQDSP_STORED);
}

// each Save row shown only while something of its own tab waits; its button usable only then
function LocalRenderSave() {
    const want = {};
    Object.keys(SQDSP_WAITING).forEach(function (k) { want[SQDSP_WAITING[k]] = true; });
    document.querySelectorAll('.sqdsp-saverow').forEach(function (r) {
        const on = !!want[r.dataset.area];
        r.style.display = on ? '' : 'none';
        const b = r.querySelector('button');
        if (b) b.disabled = !on;
    });
}

// one save at a time, in the order asked: a later one is never overtaken by an earlier (the SqueezeDSP page's queue)
var SQDSP_SAVES = [], SQDSP_SAVING = false;
function sqdspNextSave() {
    if (SQDSP_SAVING || !SQDSP_SAVES.length) return;
    SQDSP_SAVING = true;
    SQDSP_SAVES.shift()(function () { SQDSP_SAVING = false; sqdspNextSave(); });
}

// The player's settings saved (squeezedsp.saveall). With no client: everything the page shows (a Save, Reset, a preset),
// so nothing waits after it; with one (LocalSaveField): that. What is sent is taken now. A save that fails puts back what
// waited and shows "Not saved" with a Save beside Reset (the save then takes everything the page shows).
function SqueezeDSPSaveAll(callback, client) {
    const full = !client;
    const sent = sqdspClone(client || SqueezeDSPData.Client || {});
    const was = full ? SQDSP_WAITING : {};
    if (full) {
        SQDSP_WAITING = {};
        SQDSP_STORED = sqdspClone(sent);
        LocalRenderSave();
    }
    const body = Object.toJSON({
        id: 1,
        method: 'slim.request',
        params: [getCurrentPlayer(), ['squeezedsp.saveall', 'val:' + JSON.stringify(Object.assign({}, SqueezeDSPData, { Client: sent }))]]
    });
    function failed() {
        Object.keys(was).forEach(function (k) { if (!(k in SQDSP_WAITING)) SQDSP_WAITING[k] = was[k]; });
        SQDSP_WAITING['*'] = 'top';
        LocalRenderSave();
    }
    SQDSP_SAVES.push(function (done) {
        // once per save, whatever answers: the queue moves on before the caller's callback (which may throw) is told
        let over = false;
        function finish(ok, why) {
            if (over) return;
            over = true;
            if (!ok) failed();
            done();
            if (typeof callback === 'function') {
                try { callback(ok, why); } catch (e) { console.error('Save callback failed', e); }
            }
        }
        new Ajax.Request('/jsonrpc.js', {
            method: 'post',
            postBody: body,
            onSuccess: (response) => {
                // Just check for done status, no result content
                let data = null;
                try { data = response.responseText.evalJSON(); } catch (e) { data = null; }
                if (data && data.result && data.result._done) {
                    finish(true);
                } else {
                    console.error('Save failed:', data);
                    finish(false, data && data.error);
                }
            },
            onFailure: (response) => {
                console.error('Save failed', response.status, response.statusText);
                finish(false, response.statusText);
            },
            onException: (request, e) => {
                console.error('Save failed', e);
                finish(false, String(e));
            }
        });
    });
    sqdspNextSave();
}


function importWavFile(file, callbackLog = console.log, callbackRefresh = "") {
    if (!confirm('Import Filter Impulse Response file:\n' + file.name)) {
        return;
    }
    
    callbackLog('Importing: ' + file.name);
   
    const reader = new FileReader();
    reader.onload = function(e) {
        const base64Data = e.target.result.split(',')[1];
        
        new Ajax.Request('/jsonrpc.js', {
            method: 'post',
            postBody: Object.toJSON({
                id: 1,
                method: 'slim.request',
                params: [
                    getCurrentPlayer(),
                    [
                        'squeezedsp.importwav',
                        '_p0:' + file.name,
                        '_p1:' + base64Data
                    ]
                ]
            }),
            onSuccess: (response) => {
                const json = response.responseText.evalJSON();
                if (json?.result?._success) {
                    callbackLog('Imported: ' + file.name);
					if (callbackRefresh) callbackRefresh();
                } else {
                    callbackLog('Failed to import: ' + file.name);
                }
            },
            onFailure: () => {
                callbackLog('Failed to import: ' + file.name);
            }
        });
    };
    reader.onerror = () => {
        callbackLog('Error reading file: ' + file.name);
    };
    reader.readAsDataURL(file);
}

function SqueezeDSPClearLog(callback) {
    // calls plugin clearlogCommand
    new Ajax.Request('/jsonrpc.js', {
        method: 'post',
        asynchronous: true,
        postBody: Object.toJSON({
            id: 1, 
            method: 'slim.request', 
            params: [
                getCurrentPlayer(), 
                [
                    'squeezedsp.clearlog'
                ]
            ]
        }),

        onFailure: function(response) { 
            console.error('Failed to clear log:', response.statusText);
            if (typeof callback === 'function') {
                callback(false, 'Failed to clear log: ' + response.statusText);
            }
        },

        onSuccess: function(response) {
            // debugger;
            var data = response.responseText.evalJSON(true);
            
            // Check if we got an error from the server
            if (data.error) {
                console.error('Server error clearing log:', data.error);
                if (typeof callback === 'function') {
                    callback(false, 'Server error: ' + data.error.message);
                }
                return;
            }
            
            // Success case - check the result structure
            if (data.result && data.result._success == 1) {
                console.log('Log cleared successfully');
                if (typeof callback === 'function') {
                    callback(true, data.result._message || 'Log cleared successfully');
                }
            } else {
                console.error('Log clear failed:', data.result ? data.result._message : 'Unknown error');
                if (typeof callback === 'function') {
                    callback(false, data.result ? data.result._message : 'Failed to clear log');
                }
            }
        }
    });
}
