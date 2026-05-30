// Receiver audio/EQ strip: tone/balance knobs, 10-band EQ, switches,
// presets, and EQ memories. Posts previews while dragging and a commit on
// release to /receiver/audio/dsp; memories to /receiver/audio/dsp/memory.
// Syncs from the shared `audioDsp` SSE event (vfd-live.js owns EventSource).
(() => {
  'use strict';
  if (!window.Chassis) {
    console.warn('audio-strip: window.Chassis missing');
    return;
  }

  const PREVIEW_MS = 120;
  const HOLD_MS = 500;
  const PRESETS = {
    flat:  [0, 0, 0, 0, 0, 0, 0, 0, 0, 0],
    rock:  [4, 3, 1, 0, -1, -1, 0, 2, 3, 4],
    jazz:  [2, 1, 0, 1, 1, 0, 0, 1, 2, 2],
    vocal: [-2, -1, 0, 2, 3, 3, 2, 1, 0, -1],
  };

  let editing = false;
  let previewTimer = 0;
  let pendingPreview = null;

  function post(body) {
    return fetch('/receiver/audio/dsp', {
      method: 'POST', credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
  }
  function postMemory(body) {
    return fetch('/receiver/audio/dsp/memory', {
      method: 'POST', credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
  }
  // Trailing-edge throttle: each call records the latest params; the in-flight
  // window posts whatever is newest when it fires (not the first value captured),
  // so the audited audio tracks the live drag instead of lagging a window behind.
  function preview(params) {
    pendingPreview = params;
    if (previewTimer) return;
    previewTimer = window.setTimeout(() => {
      previewTimer = 0;
      const latest = pendingPreview;
      pendingPreview = null;
      if (latest) post({ commit: false, params: latest }).catch((e) => console.warn('audio-strip preview', e));
    }, PREVIEW_MS);
  }
  function commit(params) {
    if (previewTimer) { window.clearTimeout(previewTimer); previewTimer = 0; }
    pendingPreview = null;
    post({ commit: true, params }).catch((e) => console.warn('audio-strip commit', e));
  }

  function currentEQ() {
    return Array.from(document.querySelectorAll('[data-dsp-eq]'))
      .sort((a, b) => Number(a.dataset.dspEq) - Number(b.dataset.dspEq))
      .map((el) => Number(el.value));
  }

  // Drive the VFD-cyan fill bar so it rises with the fader value. The track is
  // styled in CSS via --eq-fill (0%..100%); the native input handles dragging.
  function paintEQFill(el) {
    if (!el || !el.style || !el.style.setProperty) return;
    const min = Number(el.min) || -12;
    const max = Number(el.max) || 12;
    const v = Number(el.value);
    const pct = max > min ? ((v - min) / (max - min)) * 100 : 0;
    el.style.setProperty('--eq-fill', `${Math.max(0, Math.min(100, pct)).toFixed(1)}%`);
  }

  function bindEQ() {
    document.querySelectorAll('[data-dsp-eq]').forEach((el) => {
      paintEQFill(el);
      el.addEventListener('pointerdown', () => { editing = true; });
      el.addEventListener('input', () => { editing = true; paintEQFill(el); preview({ eq: currentEQ() }); });
      el.addEventListener('change', () => { editing = false; commit({ eq: currentEQ() }); });
    });
  }

  function bindKnobs() {
    document.querySelectorAll('[data-dsp-knob-range]').forEach((el) => {
      const key = el.dataset.dspKnobRange;
      const field = key === 'balance' ? 'balance' : key; // bass/mid/treble/balance
      const read = () => (key === 'balance' ? parseInt(el.value, 10) : parseFloat(el.value));
      el.addEventListener('pointerdown', () => { editing = true; });
      el.addEventListener('input', () => { editing = true; preview({ [field]: read() }); });
      el.addEventListener('change', () => { editing = false; commit({ [field]: read() }); });
    });
  }

  function bindSwitches() {
    document.querySelectorAll('[data-dsp-switch]').forEach((el) => {
      el.addEventListener('click', () => {
        const on = !el.classList.contains('on');
        el.classList.toggle('on', on);
        el.setAttribute('aria-checked', on ? 'true' : 'false');
        const key = el.dataset.dspSwitch;
        if (key === 'defeat') {
          commit({ enabled: !on }); // EQ Out engaged = DSP disabled
        } else {
          commit({ [key]: on });
        }
      });
    });
  }

  function bindPresets() {
    document.querySelectorAll('[data-dsp-preset]').forEach((el) => {
      el.addEventListener('click', () => {
        const curve = PRESETS[el.dataset.dspPreset.toLowerCase()];
        if (!curve) return;
        document.querySelectorAll('[data-dsp-eq]').forEach((slider) => {
          slider.value = String(curve[Number(slider.dataset.dspEq)] || 0);
          paintEQFill(slider);
        });
        commit({ eq: curve.slice() });
      });
    });
  }

  function bindMemories() {
    document.querySelectorAll('[data-dsp-memory]').forEach((el) => {
      const slot = Number(el.dataset.dspMemory);
      let holdTimer = 0;
      let held = false;
      const startHold = () => {
        held = false;
        holdTimer = window.setTimeout(() => {
          held = true;
          postMemory({ op: 'store', slot, name: el.textContent.trim() })
            .then(() => { el.classList.add('stored'); flash(el, 'STORED'); })
            .catch((e) => console.warn('audio-strip store', e));
        }, HOLD_MS);
      };
      const endHold = () => {
        if (holdTimer) { window.clearTimeout(holdTimer); holdTimer = 0; }
        if (!held) postMemory({ op: 'recall', slot }).catch((e) => console.warn('audio-strip recall', e));
      };
      el.addEventListener('pointerdown', startHold);
      el.addEventListener('pointerup', endHold);
      el.addEventListener('pointerleave', () => { if (holdTimer) { window.clearTimeout(holdTimer); holdTimer = 0; } });
    });
  }

  function flash(el, text) {
    const prev = el.textContent;
    el.textContent = text;
    window.setTimeout(() => { el.textContent = prev; }, 600);
  }

  function applyFromEvent(params, engaged, persisted) {
    if (editing) return; // don't fight the operator mid-drag
    const setRange = (sel, v) => {
      const el = document.querySelector(sel);
      if (el && el.value !== String(v)) el.value = String(v);
    };
    setRange('[data-dsp-knob-range="bass"]', params.bass);
    setRange('[data-dsp-knob-range="mid"]', params.mid);
    setRange('[data-dsp-knob-range="treble"]', params.treble);
    setRange('[data-dsp-knob-range="balance"]', params.balance);
    (params.eq || []).forEach((g, i) => {
      const sel = `[data-dsp-eq="${i}"]`;
      setRange(sel, g);
      const el = document.querySelector(sel);
      if (el) paintEQFill(el);
    });
    const sw = (key, on) => {
      const el = document.querySelector(`[data-dsp-switch="${key}"]`);
      if (!el) return;
      el.classList.toggle('on', on);
      el.setAttribute('aria-checked', on ? 'true' : 'false');
    };
    sw('loudness', params.loudness);
    sw('mono', params.mono);
    sw('subsonic', params.subsonic);
    sw('defeat', !params.enabled);
    const led = document.querySelector('[data-eq-led]');
    if (led) led.classList.toggle('on', Boolean(engaged));
    const root = document.querySelector('[data-audio-strip]');
    if (root) root.dataset.dspPersisted = String(persisted);
  }

  function handleEvent(ev) {
    try {
      const data = JSON.parse(ev.data);
      applyFromEvent(data.params || {}, Boolean(data.engaged), Boolean(data.persisted));
    } catch (err) {
      console.warn('audio-strip: bad audioDsp payload', ev.data, err);
    }
  }

  function init() {
    bindEQ();
    bindKnobs();
    bindSwitches();
    bindPresets();
    bindMemories();
    window.Chassis.events.subscribe('audioDsp', handleEvent);
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
