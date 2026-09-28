// calibration.js — CRT picture calibration pad (Settings → Video & Audio).
//
// "Calibrate on CRT" puts a Go-rendered test pattern on the CRT; the pad
// nudges the draft picture size/position and POSTs each change to
// /ui/calibration/preview, which redraws the pattern in place (no pipeline
// restart). Save persists the draft; Cancel discards it. The `calibration`
// SSE event is the source of truth for state; the local draft leads only
// while the operator is adjusting. See
// docs/superpowers/specs/2026-09-28-crt-picture-calibration-design.md.
//
// Pure helpers are published on window.Chassis.calibration before any DOM
// wiring so behaviour tests can drive them without a document. Drawer
// helpers are reached only through window.Chassis.settings.*.
(() => {
  'use strict';

  const LIMITS = { hSize: [80, 100], vSize: [80, 100], hOffset: [-72, 72], vOffset: [-28, 28] };
  const DEFAULTS = { hSize: 100, vSize: 100, hOffset: 0, vOffset: 0 };
  const KEYS = ['hSize', 'vSize', 'hOffset', 'vOffset'];
  const MOVES = {
    position: { up: ['vOffset', -1], down: ['vOffset', 1], left: ['hOffset', -1], right: ['hOffset', 1] },
    size: { up: ['vSize', 1], down: ['vSize', -1], left: ['hSize', -1], right: ['hSize', 1] },
  };
  const STEP = { position: 1, size: 0.5 };
  const LABELS = {
    position: { up: 'Move up', down: 'Move down', left: 'Move left', right: 'Move right', centre: 'Centre the picture' },
    size: { up: 'Taller', down: 'Shorter', left: 'Narrower', right: 'Wider', centre: 'Full size' },
  };
  const END_TEXT = {
    cast: 'A cast started, so calibration ended. Your unsaved values are still here.',
    stopped: 'Calibration was stopped. Your unsaved values are still here.',
    timeout: 'Calibration timed out. Your unsaved values are still here.',
    error: 'The test pattern stopped. Check the MiSTer connection. Your unsaved values are still here.',
    'save-failed': 'The values could not be saved. Try Save again.',
  };

  // Sizes keep one decimal (the server rounds the same way); offsets are
  // whole pixels / field lines.
  function tidy(key, v) {
    const [lo, hi] = LIMITS[key];
    const n = key.endsWith('Size') ? Math.round(v * 10) / 10 : Math.round(v);
    return Math.min(hi, Math.max(lo, n));
  }

  function normalize(g) {
    const out = {};
    for (const k of KEYS) {
      const v = g && Number(g[k]);
      out[k] = Number.isFinite(v) && !(k.endsWith('Size') && v === 0) ? tidy(k, v) : DEFAULTS[k];
    }
    return out;
  }

  function sameGeometry(a, b) {
    return KEYS.every((k) => a[k] === b[k]);
  }

  // nudge returns the draft after one D-pad press in mode, factor steps.
  function nudge(draft, mode, dir, factor) {
    const move = MOVES[mode] && MOVES[mode][dir];
    if (!move) return draft;
    const [key, sign] = move;
    return { ...draft, [key]: tidy(key, draft[key] + sign * STEP[mode] * (factor || 1)) };
  }

  // centred resets the pair the current mode adjusts.
  function centred(draft, mode) {
    return mode === 'size'
      ? { ...draft, hSize: DEFAULTS.hSize, vSize: DEFAULTS.vSize }
      : { ...draft, hOffset: DEFAULTS.hOffset, vOffset: DEFAULTS.vOffset };
  }

  // pictureBox places the picture in the raster diagram, in percent.
  function pictureBox(draft, raster) {
    const width = (raster && raster.rasterWidth) || 720;
    const lines = (raster && raster.fieldLines) || 240;
    return {
      left: (100 - draft.hSize) / 2 + (draft.hOffset / width) * 100,
      top: (100 - draft.vSize) / 2 + (draft.vOffset / lines) * 100,
      width: draft.hSize,
      height: draft.vSize,
    };
  }

  // previewQueue sends at most one preview at a time; a change made while
  // one is in flight replaces the queued value (latest wins).
  function previewQueue(send) {
    let inflight = false;
    let pending = null;
    async function pump() {
      while (pending) {
        const next = pending;
        pending = null;
        inflight = true;
        try {
          await send(next);
        } finally {
          inflight = false;
        }
      }
    }
    return {
      push(draft) {
        pending = { ...draft };
        if (!inflight) return pump();
        return undefined;
      },
      get busy() { return inflight; },
    };
  }

  window.Chassis = window.Chassis || {};
  window.Chassis.calibration = { LIMITS, DEFAULTS, END_TEXT, tidy, normalize, sameGeometry, nudge, centred, pictureBox, previewQueue };

  const doc = window.document;
  const root = doc && doc.querySelector && doc.querySelector('[data-calibration]');
  if (!root) return;

  const $ = (sel) => root.querySelector(sel);
  const $$ = (sel) => Array.from(root.querySelectorAll(sel));
  const startBtn = $('[data-calib-start]');
  const busyNote = $('[data-calib-busy]');
  const fields = $('[data-calib-fields]');
  const pad = $('[data-calib-pad]');
  const statusEl = $('[data-calib-status]');
  const picture = $('[data-calib-picture]');
  const resumeBtn = $('[data-calib-resume]');
  const saveBtn = $('[data-calib-save]');
  const cancelBtn = $('[data-calib-cancel]');
  const resetBtn = $('[data-calib-reset]');
  const centreBtn = $('[data-calib-centre]');
  const dirBtns = $$('[data-calib-dir]');
  const modeBtns = $$('[data-calib-mode]');
  const valueInputs = $$('[data-calib-value]');

  let mode = 'position';
  let calibState = 'idle';
  let endReason = '';
  let coreState = 'idle';
  let draft = { ...DEFAULTS };
  let raster = { rasterWidth: 720, fieldLines: 240 };
  let busyAction = false;

  const settings = () => (window.Chassis && window.Chassis.settings) || {};
  function notice(text, variant) {
    const s = settings();
    if (typeof s.showNotice === 'function') s.showNotice(text, variant);
  }

  async function post(path, body) {
    try {
      const res = await fetch(path, {
        method: 'POST',
        credentials: 'same-origin',
        headers: body ? { 'Content-Type': 'application/json' } : {},
        body: body ? JSON.stringify(body) : undefined,
      });
      let data = {};
      if (res.status !== 204) {
        try { data = await res.json(); } catch (_) { data = {}; }
      }
      return { status: res.status, body: data || {} };
    } catch (_) {
      return { netErr: true, body: {} };
    }
  }

  const queue = previewQueue(async (g) => {
    const r = await post('/ui/calibration/preview', g);
    if (r.netErr) {
      notice('NETWORK ERROR', 'err');
    } else if (r.status === 400 && r.body.errors) {
      const first = Object.keys(r.body.errors)[0];
      notice(`${first}: ${r.body.errors[first]}`, 'err');
    }
    // 409 NOT ACTIVE: the calibration just ended; the SSE event explains why.
  });

  function update(next) {
    if (calibState !== 'active' || sameGeometry(next, draft)) return;
    draft = next;
    render();
    queue.push(draft);
  }

  function render() {
    for (const input of valueInputs) {
      const key = input.dataset.calibValue;
      if (doc.activeElement !== input) input.value = String(draft[key]);
    }
    if (picture) {
      const box = pictureBox(draft, raster);
      picture.style.setProperty('--calib-left', `${box.left}%`);
      picture.style.setProperty('--calib-top', `${box.top}%`);
      picture.style.setProperty('--calib-width', `${box.width}%`);
      picture.style.setProperty('--calib-height', `${box.height}%`);
    }
    for (const btn of modeBtns) {
      btn.setAttribute('aria-pressed', btn.dataset.calibMode === mode ? 'true' : 'false');
    }
    for (const btn of dirBtns) {
      const label = LABELS[mode][btn.dataset.calibDir];
      btn.setAttribute('aria-label', label);
      btn.title = label;
    }
    if (centreBtn) {
      centreBtn.setAttribute('aria-label', LABELS[mode].centre);
      centreBtn.title = LABELS[mode].centre;
    }
  }

  function setView() {
    const active = calibState === 'active';
    const open = active || calibState === 'ended';
    const idleBridge = coreState === 'idle';
    if (pad) pad.hidden = !open;
    if (fields) fields.hidden = open;
    if (startBtn) {
      startBtn.hidden = open;
      startBtn.disabled = !idleBridge || busyAction;
    }
    if (busyNote) busyNote.hidden = open || idleBridge;
    if (resumeBtn) {
      resumeBtn.hidden = active;
      resumeBtn.disabled = !idleBridge || busyAction;
    }
    for (const el of [...dirBtns, ...modeBtns, centreBtn, resetBtn, ...valueInputs]) {
      if (el) el.disabled = !active;
    }
    if (saveBtn) saveBtn.disabled = busyAction;
    if (cancelBtn) cancelBtn.disabled = busyAction;
    if (statusEl) {
      statusEl.textContent = active ? 'Test pattern is on the CRT.' : (END_TEXT[endReason] || 'Calibration ended.');
    }
    root.classList.toggle('calibrating', active);
  }

  // syncSaved mirrors saved values into the at-rest settings fields, which
  // autosave on their own; after a calibration Save they must show the new
  // values.
  function syncSaved(saved) {
    const map = {
      video_picture_h_size: saved.hSize,
      video_picture_v_size: saved.vSize,
      video_picture_h_offset: saved.hOffset,
      video_picture_v_offset: saved.vOffset,
    };
    for (const [name, value] of Object.entries(map)) {
      const input = root.querySelector(`input[name="${name}"]`);
      if (input && doc.activeElement !== input) input.value = String(value);
    }
  }

  function applySnapshot(snap) {
    if (!snap || !snap.state) return;
    const prev = calibState;
    calibState = snap.state;
    endReason = snap.endReason || '';
    if (snap.rasterWidth) raster = { rasterWidth: snap.rasterWidth, fieldLines: snap.fieldLines };
    if (snap.saved) syncSaved(normalize(snap.saved));
    // The server draft wins except while this pad is actively adjusting.
    if (calibState !== 'active' || prev !== 'active') draft = normalize(snap.draft);
    render();
    setView();
  }

  function onCalibration(ev) {
    try { applySnapshot(JSON.parse(ev.data)); } catch (_) { /* ignore malformed frames */ }
  }

  function onState(ev) {
    try { coreState = JSON.parse(ev.data).state || coreState; } catch (_) { return; }
    setView();
  }

  // runAction disables the action buttons while fn runs. Focus moves only
  // after it finishes: a still-disabled button cannot take focus.
  async function runAction(fn, focusAfter) {
    if (busyAction) return;
    busyAction = true;
    setView();
    let ok = false;
    try { ok = await fn(); } finally {
      busyAction = false;
      setView();
    }
    const target = ok && focusAfter && focusAfter();
    if (target && typeof target.focus === 'function') target.focus();
  }

  function start() {
    return runAction(async () => {
      const r = await post('/ui/calibration/start');
      if (r.netErr) { notice('NETWORK ERROR', 'err'); return false; }
      if (r.status === 200 && r.body.calibration) {
        applySnapshot(r.body.calibration);
        return true;
      }
      if (r.body.chip === 'BUSY') { notice('Stop the current cast to calibrate.', 'err'); return false; }
      notice(r.body.chip || 'START FAILED', 'err');
      return false;
    }, () => modeBtns[0]);
  }

  function save() {
    return runAction(async () => {
      const r = await post('/ui/calibration/save');
      if (r.netErr) { notice('NETWORK ERROR', 'err'); return false; }
      if (r.status === 200 && r.body.calibration) {
        applySnapshot(r.body.calibration);
        notice('Picture saved. It applies from the next cast.');
        return true;
      }
      notice(r.body.chip || 'WRITE FAILED', 'err');
      return false;
    }, () => startBtn);
  }

  function cancel() {
    return runAction(async () => {
      const r = await post('/ui/calibration/cancel');
      if (r.netErr) { notice('NETWORK ERROR', 'err'); return false; }
      calibState = 'idle';
      endReason = '';
      return true;
    }, () => startBtn);
  }

  // D-pad: click steps once (Shift = 4); holding a key repeats after a
  // short delay. The click that ends a repeat is swallowed.
  const HOLD_DELAY_MS = 400;
  const HOLD_EVERY_MS = 90;
  for (const btn of dirBtns) {
    let holdTimer = null;
    let repeated = false;
    const stopHold = () => {
      if (holdTimer) { clearTimeout(holdTimer); clearInterval(holdTimer); holdTimer = null; }
    };
    btn.addEventListener('pointerdown', (e) => {
      if (btn.disabled || e.button !== 0) return;
      repeated = false;
      stopHold();
      holdTimer = setTimeout(() => {
        holdTimer = setInterval(() => {
          repeated = true;
          update(nudge(draft, mode, btn.dataset.calibDir, 1));
        }, HOLD_EVERY_MS);
      }, HOLD_DELAY_MS);
    });
    for (const evt of ['pointerup', 'pointerleave', 'pointercancel']) btn.addEventListener(evt, stopHold);
    btn.addEventListener('click', (e) => {
      if (repeated) { repeated = false; return; }
      update(nudge(draft, mode, btn.dataset.calibDir, e.shiftKey ? 4 : 1));
    });
  }

  for (const btn of modeBtns) {
    btn.addEventListener('click', () => {
      mode = btn.dataset.calibMode === 'size' ? 'size' : 'position';
      render();
    });
  }
  if (centreBtn) centreBtn.addEventListener('click', () => update(centred(draft, mode)));
  if (resetBtn) resetBtn.addEventListener('click', () => update({ ...DEFAULTS }));
  if (startBtn) startBtn.addEventListener('click', start);
  if (resumeBtn) resumeBtn.addEventListener('click', start);
  if (saveBtn) saveBtn.addEventListener('click', save);
  if (cancelBtn) cancelBtn.addEventListener('click', cancel);

  for (const input of valueInputs) {
    input.addEventListener('change', () => {
      const key = input.dataset.calibValue;
      const v = parseFloat(input.value);
      if (!Number.isFinite(v)) { input.value = String(draft[key]); return; }
      update({ ...draft, [key]: tidy(key, v) });
      input.value = String(draft[key]);
    });
  }

  const ARROWS = { ArrowUp: 'up', ArrowDown: 'down', ArrowLeft: 'left', ArrowRight: 'right' };
  if (pad) {
    pad.addEventListener('keydown', (e) => {
      const dir = ARROWS[e.key];
      if (!dir || calibState !== 'active') return;
      // Number inputs keep their own arrow-key stepping.
      if (e.target && e.target.matches && e.target.matches('input')) return;
      e.preventDefault();
      update(nudge(draft, mode, dir, e.shiftKey ? 4 : 1));
    });
  }

  render();
  setView();
  const events = window.Chassis.events;
  if (events && typeof events.subscribe === 'function') {
    events.subscribe('calibration', onCalibration);
    events.subscribe('state', onState);
  }
})();
