/*---------------------------------------------------
File Import and Processing
-----------------------------------------------------*/

// A REW / AutoEQ filter file: its bands and preamp replace the player's, waiting for Save (PEQ's, and DSP's for the preamp). SqueezeDSP fork (0.1.72.57,
// Simon 2026-11-20): read by parseEQText (sqdsp_peq_lib.js, shared with the new page), so a shelf keeps its own Q, and
// the other DSP settings (FIR, crossfeed, loudness, ReplayGain, delay, balance, width) stay: Fox's import reset them all
// first (LocalClearSettings). A file with no Preamp line sets the preamp to 0, as that reset did.
function ProcessREW(EQText) {
    var parsed = parseEQText(EQText);
    SqueezeDSPData.Client.Filters = parsed.filters;
    SqueezeDSPData.Client.Preamp = parsed.preamp === null ? 0 : parsed.preamp;
    LocalUpdateSettings();
    LocalChanged('Filters');
    LocalChanged('Preamp');
}

function ConfirmREW(EQText) {
    if (!confirm('Load the Settings:\n' + EQText)) return;
    ProcessREW(EQText);
}

function readFile(file, callback) {
    const reader = new FileReader();
    reader.onload = () => callback(reader.result);
    reader.readAsText(file);
    outputlist("Last File loaded was: " + file.name);
}

function dodrop(event) {
    var dt = event.dataTransfer;
    var files = dt.files;
    
    if (files.length > 1) {
        outputlist('Only 1 file at a time Please');
        return;
    }

    for (var i = 0; i < files.length; i++) {
        var file = files[i];
        var extension = file.name.split('.').pop().toLowerCase();
        
        if (extension === 'txt') {
            readFile(file, ConfirmREW);
        } else if (extension === 'wav') {
            importWavFile(file, outputlist, refreshFIRWavList);
        } else {
            outputlist('Unsupported file type: ' + extension);
        }
    }
}

function refreshFIRWavList() {
    SqueezeDSPGetList(function(data) {
        LocalBuildList('FIRWavFile', data);
    });
}
  
function outputlist(text) {
    document.getElementById("LastFile").textContent = text;
    document.getElementById("filePicker").value = "";
}