/*---------------------------------------------------
Utility Functions
-----------------------------------------------------*/

function replaceNull(ValueToCheck, DefaultValue) {
    if (!ValueToCheck) return DefaultValue;
    else return ValueToCheck;
}

// Sets a <select> from the settings. LMS's skin header dresses every select with custom-select once the
// page has loaded, and that widget redraws only on a change event or its own setter, so a plain .value
// write left the old text showing (Upsample precision showed 20-bit with 33 stored and in use). Its setter
// redraws without a change event, so nothing is marked unsaved. A value no option has takes the fallback.
function setSelectValue(el, value, fallback) {
    if (!el) return;
    const want = String(value);
    const has = Array.prototype.some.call(el.options, o => o.value === want);
    if (has || fallback !== undefined) el.value = has ? want : String(fallback);
    if (el.customSelect) el.customSelect.value = el.value;
}

function iSlide(myObj) {
    myVal = document.getElementById('val_' + myObj.id);
    var mySlideName = myObj.id;
    
    if (mySlideName.endsWith("freq") || mySlideName.endsWith("Frequency")) {
        myVal.value = logScaleMapper(myObj.value);
    } else {
        myVal.value = myObj.value;
    }
    
    document.getElementById(myObj.id + 'Bubble').style.display = 'none';    
    NewFieldValue(myObj.id, myVal, false);
}

function SliderReset() {
    try {
        this.value = 0;
        myVal = document.getElementById('val_' + this.id);
        myVal.value = this.value;
        NewFieldValue(this.id, myVal, false);
    } catch {
        alert('Error on SliderReset');
    }
}

function LocalClearSettings() {
    // the output format, upsampling and DSD decoding are how the player is fed, not DSP settings: Reset keeps them
    const keepKeys = ['OutputFormat', 'Upsample', 'UpsamplePrecision', 'UpsampleSteep', 'UpsamplePassband', 'UpsamplePhase', 'UpsampleTarget', 'UpsampleMax',
                      'DSDCutoff', 'DSDGain'];
    const kept = {};
    keepKeys.forEach(k => { if (SqueezeDSPData.Client && SqueezeDSPData.Client[k] !== undefined) kept[k] = SqueezeDSPData.Client[k]; });
    InitialiseSqueezeDSPData();
	SqueezeDSPData.Client.Bypass = 0;  // Reset clears settings but stays active
    Object.keys(kept).forEach(k => { SqueezeDSPData.Client[k] = kept[k]; });
    LocalUpdateSettings();
    highlightMatchingOption('sel_FIRWavFile', 'None');
    highlightMatchingOption('sel_Preset', 'None');
    reStyleElement('btn_Apply', 'button-blue', 'button-amber');
    reStyleElement('btn_LoadPreset', 'button-amber', 'button-teal');
}

function SwitchCheckbox(myElement, InvertToggle = false) {
    if (myElement.checked) {
        if (!InvertToggle) myElement.value = 1;
        else myElement.value = 0;
    }
    
    if (!myElement.checked) {
        if (InvertToggle) myElement.value = 1;
        else myElement.value = 0;
    }
}