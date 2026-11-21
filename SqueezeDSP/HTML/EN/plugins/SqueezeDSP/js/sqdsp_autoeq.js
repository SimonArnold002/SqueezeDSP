/*---------------------------------------------------
  AutoEQ Headphone Profile Lookup and Import
  Fetches profiles from jaakkopasanen/AutoEq on GitHub
  Integrates with existing SqueezeDSP file import pipeline
-----------------------------------------------------*/

const AutoEQIndex = (function () {
    const RAW_BASE    = 'https://raw.githubusercontent.com/jaakkopasanen/AutoEq/master/results/';
    const API_BASE    = 'https://api.github.com/repos/jaakkopasanen/AutoEq/contents/results/';
    const INDEX_URL   = RAW_BASE + '../results/INDEX.md';
    // Simpler: use the known raw URL directly
    const INDEX_RAW   = 'https://raw.githubusercontent.com/jaakkopasanen/AutoEq/master/results/INDEX.md';

    let entries      = [];   // [{name, path, source}]
    let indexLoaded  = false;
    let indexLoading = false;
    let pendingCallbacks = [];

    /*
     * Parse a line like:
     *   - [Sennheiser HD 599](./crinacle/711 in-ear/Sennheiser HD 599) by crinacle on 711
     * into { name, path, source }
     * path returned is relative to results/, e.g. "crinacle/711 in-ear/Sennheiser HD 599"
     */
function parseLine(line) {
    const m = line.match(/^\s*-\s*\[([^\]]+)\]\(\.\/(.+)\)(?:\s+\S.*)?$/);
    if (!m) return null;

    const name    = m[1].trim();
    const relPath = m[2].trim();
    const parts   = relPath.split('/');
    // SqueezeDSP fork (0.1.72.57): the source shown as written ("GRAS 43AG-7"), not URL-encoded ("GRAS%2043AG-7")
    const dec     = function (t) { try { return decodeURIComponent(t); } catch (e) { return t; } };
    const source  = parts.slice(0, -1).map(dec).join(' / ');

    return { name, path: relPath, source };
}

    function loadIndex(callback) {
        if (indexLoaded) { callback(entries); return; }
        pendingCallbacks.push(callback);
        if (indexLoading) return;   // will flush all callbacks when done

        indexLoading = true;
        fetch(INDEX_RAW)
            .then(r => {
                if (!r.ok) throw new Error('HTTP ' + r.status);
                return r.text();
            })
            .then(text => {
                entries = [];
                for (const line of text.split('\n')) {
                    const e = parseLine(line);
                    if (e) entries.push(e);
                }
                indexLoaded  = true;
                indexLoading = false;
                console.log('AutoEQ index loaded:', entries.length, 'entries');
                pendingCallbacks.forEach(cb => cb(entries));
                pendingCallbacks = [];
            })
            .catch(err => {
                console.error('AutoEQ: failed to load index:', err);
                indexLoading = false;
                // SqueezeDSP fork (0.1.72.57): each waiting caller told (null), so a page can say the list was not read
                const waiting = pendingCallbacks;
                pendingCallbacks = [];
                waiting.forEach(cb => cb(null));
            });
    }

    /* Case-insensitive substring search, capped at 60 results */
    function search(query) {
        if (!indexLoaded || query.length < 2) return [];
        const q = query.toLowerCase();
        return entries.filter(e => e.name.toLowerCase().includes(q)).slice(0, 60);
    }

    /*
     * Fetch file listing for a result directory via GitHub Contents API.
     * path: relative to results/, e.g. "crinacle/711 in-ear/Sennheiser HD 599"
     */
	function listFiles(path, callback) {
    // Decode first to get raw path, then re-encode cleanly
    const decodedPath = decodeURIComponent(path);
    const encodedPath = decodedPath.split('/').map(encodeURIComponent).join('/');
    const url = API_BASE + encodedPath;
    
    console.log('Fetching:', url);  // remove once confirmed working
    
    fetch(url)
        .then(r => {
            if (!r.ok) throw new Error('HTTP ' + r.status);
            return r.json();
        })
        .then(files => callback(files, null))
        .catch(err => callback(null, err));
	}

    /*
     * From a file listing, pick the best file in priority order:
     *   1. 48000Hz WAV
     *   2. 44100Hz WAV
     *   3. ParametricEQ.txt
     * Returns { fileObj, type } or null
     */
    function selectBestFile(files) {
        if (!Array.isArray(files)) return null;

        const byName = name => files.find(
            f => f.name.toLowerCase() === name.toLowerCase()
        );
        const wavFiles = files.filter(f => f.name.toLowerCase().endsWith('.wav'));
        const wav48    = wavFiles.find(f => f.name.includes('48000'));
        if (wav48) return { fileObj: wav48, type: 'wav' };

        const wav44 = wavFiles.find(f => f.name.includes('44100'));
        if (wav44) return { fileObj: wav44, type: 'wav' };

        const peq = files.find(
            f => f.name.toLowerCase().includes('parametriceq') &&
                 f.name.toLowerCase().endsWith('.txt')
        );
        if (peq) return { fileObj: peq, type: 'peq' };

        return null;
    }

    /*
     * Download a file from GitHub and dispatch it through the existing
     * SqueezeDSP file import pipeline, identical to a manual file-picker load.
     * logFn : function(string) for status messages
     */
	function importFile(fileObj, type, logFn, customName) {
		const displayName = customName || fileObj.name;
		logFn('Downloading: ' + displayName);

		fetch(fileObj.download_url)
			.then(r => {
				if (!r.ok) throw new Error('HTTP ' + r.status);
				return r.blob();
			})
			.then(blob => {
				const mimeType = (type === 'wav') ? 'audio/wav' : 'text/plain';
				const file     = new File([blob], displayName, { type: mimeType });

				if (type === 'wav') {
					importWavFile(file, logFn, refreshFIRWavList);
				} else {
					readFile(file, ConfirmREW);
					logFn('PEQ file ready for import: ' + displayName);
				}
			})
			.catch(err => logFn('Failed to download ' + displayName + ': ' + err.message));
	}
    /*
     * SqueezeDSP fork (0.1.72.57): a headphone's folder is named for it and so are its files (AutoEQ's results, read
     * 2026-11-20: "<name> minimum phase 48000Hz.wav", "... 44100Hz.wav", "<name> ParametricEQ.txt"), so they are
     * fetched straight from raw.githubusercontent.com in selectBestFile's order, with no api.github.com call (60 an
     * hour per address, unauthenticated); only when none of them is there is the folder listed through the API, as
     * before. Resolves with { type: 'wav' | 'peq', blob } or null (nothing usable), rejects on a failed download.
     */
    function fetchBest(entry) {
        const dec   = function (t) { try { return decodeURIComponent(t); } catch (e) { return t; } };
        const parts = String(entry.path).split('/').map(dec);
        const dir   = RAW_BASE + parts.map(encodeURIComponent).join('/') + '/';
        const head  = parts[parts.length - 1];
        const tries = [[head + ' minimum phase 48000Hz.wav', 'wav'], [head + ' minimum phase 44100Hz.wav', 'wav'],
                       [head + ' ParametricEQ.txt', 'peq']];
        let i = 0;
        function viaApi() {
            return new Promise(function (resolve, reject) {
                listFiles(entry.path, function (files, err) {
                    if (err || !files) { reject(err || new Error('no file list')); return; }
                    const best = selectBestFile(files);
                    if (!best) { resolve(null); return; }
                    fetch(best.fileObj.download_url)
                        .then(r => { if (!r.ok) throw new Error('HTTP ' + r.status); return r.blob(); })
                        .then(blob => resolve({ type: best.type, blob: blob }), reject);
                });
            });
        }
        function next() {
            if (i >= tries.length) return viaApi();
            const t = tries[i++];
            return fetch(dir + encodeURIComponent(t[0])).then(function (r) {
                if (r.status === 404) return next();
                if (!r.ok) throw new Error('HTTP ' + r.status);
                return r.blob().then(blob => ({ type: t[1], blob: blob }));
            });
        }
        return next();
    }

    /* the name an import is saved under: the headphone and its source, as Fox's handleAutoEQSelection names it */
    function fileName(entry, ext) {
        return (entry.name + '_' + entry.source).replace(/[^a-zA-Z0-9_\-]/g, '_').replace(/_+/g, '_').substring(0, 80) + '.' + ext;
    }

    /* Public API */
    return { loadIndex, search, listFiles, selectBestFile, importFile, fetchBest, fileName };
})();


/*---------------------------------------------------
  AutoEQ UI
-----------------------------------------------------*/

function initAutoEQUI() {
    const input      = document.getElementById('autoeq-search');
    const dropdown   = document.getElementById('autoeq-dropdown');
    const statusSpan = document.getElementById('autoeq-status');

    if (!input) return;

    let debounceTimer = null;

    /* SqueezeDSP fork (0.1.72.57): the index (about 850 KB from GitHub) read when the search box is first used, not at
       every opening of the page */
    input.addEventListener('focus', function () {
        AutoEQIndex.loadIndex(function() {
            if (statusSpan) statusSpan.textContent = '';
        });
    }, { once: true });

    input.addEventListener('input', function () {
        clearTimeout(debounceTimer);
        const query = this.value.trim();

        if (query.length < 2) {
            hideDropdown(dropdown);
            return;
        }

        debounceTimer = setTimeout(function () {
            const results = AutoEQIndex.search(query);
            renderDropdown(results, dropdown, input, statusSpan);
        }, 250);
    });

    /* Close dropdown on outside click */
    document.addEventListener('click', function (e) {
        if (!e.target.closest('#autoeq-search-container')) {
            hideDropdown(dropdown);
        }
    });

    /* Keyboard nav */
    input.addEventListener('keydown', function (e) {
        const options = dropdown.querySelectorAll('.autoeq-option');
        const current = dropdown.querySelector('.autoeq-option.focused');
        let idx = Array.from(options).indexOf(current);

        if (e.key === 'ArrowDown') {
            e.preventDefault();
            idx = Math.min(idx + 1, options.length - 1);
            setFocused(options, idx);
        } else if (e.key === 'ArrowUp') {
            e.preventDefault();
            idx = Math.max(idx - 1, 0);
            setFocused(options, idx);
        } else if (e.key === 'Enter' && current) {
            e.preventDefault();
            current.click();
        } else if (e.key === 'Escape') {
            hideDropdown(dropdown);
        }
    });
}

function setFocused(options, idx) {
    options.forEach(o => o.classList.remove('focused'));
    if (options[idx]) {
        options[idx].classList.add('focused');
        options[idx].scrollIntoView({ block: 'nearest' });
    }
}

function renderDropdown(results, dropdown, input, statusSpan) {
    dropdown.innerHTML = '';

    if (results.length === 0) {
        hideDropdown(dropdown);
        return;
    }

    results.forEach(function (entry) {
        const div = document.createElement('div');
        div.className = 'autoeq-option';

        const nameSpan   = document.createElement('span');
        nameSpan.className = 'autoeq-name';
        nameSpan.textContent = entry.name;

        const sourceSpan = document.createElement('span');
        sourceSpan.className = 'autoeq-source';
        sourceSpan.textContent = entry.source;

        div.appendChild(nameSpan);
        div.appendChild(sourceSpan);

        div.addEventListener('click', function () {
            input.value = '';
            hideDropdown(dropdown);
            handleAutoEQSelection(entry, statusSpan);
        });

        dropdown.appendChild(div);
    });

    dropdown.style.display = 'block';
}

function hideDropdown(dropdown) {
    if (dropdown) dropdown.style.display = 'none';
}

// SqueezeDSP fork (0.1.72.57): the files fetched by name (AutoEQIndex.fetchBest, no GitHub API call unless they are
// not there); a FIR WAV is imported and then selected for the player (Simon 2026-11-20: loading it in place of the FIR
// there "is to be expected"), waiting for Apply as every change on this page does; a ParametricEQ.txt goes through the
// file import (ConfirmREW)
function handleAutoEQSelection(entry, statusSpan) {
    const log = function (msg) {
        if (statusSpan) statusSpan.textContent = msg;
        if (typeof outputlist === 'function') outputlist(msg);
    };
    log('Downloading: ' + entry.name);
    AutoEQIndex.fetchBest(entry).then(function (best) {
        if (!best) { log('No compatible file found for: ' + entry.name); return; }
        const name = AutoEQIndex.fileName(entry, best.type === 'wav' ? 'wav' : 'txt');
        if (best.type === 'wav') {
            const file = new File([best.blob], name, { type: 'audio/wav' });
            importWavFile(file, log, function () {
                SqueezeDSPGetList(function (data) {
                    LocalBuildList('FIRWavFile', data);
                    const sel = document.getElementById('sel_FIRWavFile');
                    if (!sel) return;
                    highlightMatchingOption('sel_FIRWavFile', name);
                    NewFieldValue('FIRWavFile', sel, false);
                });
            });
        } else {
            readFile(new File([best.blob], name, { type: 'text/plain' }), ConfirmREW);
        }
    }, function (err) {
        log('Failed to download ' + entry.name + ': ' + (err && err.message ? err.message : err));
    });
}
