// Receiver chassis physical volume knob.
//
// Uses the shared /ui/events EventSource exposed by vfd-live.js and
// posts global output-volume changes back to /ui/volume.
(() => {
  'use strict';

  if (!window.Chassis) {
    console.warn('volume-knob: window.Chassis missing; chassis.js failed to load?');
    return;
  }

  const MIN = 0;
  const MAX = 100;
  const START_DEG = -135;
  const ARC_DEG = 270;
  const SAVE_INTERVAL_MS = 200;

  let root = null;
  let range = null;
  let muteButton = null;
  let authoritative = 100;
  let authoritativeMuted = false;
  let localValue = 100;
  let localMuted = false;
  let inFlight = false;
  let queuedValue = null;
  let editing = false;
  let draggingPointer = null;
  let finalCommitNeeded = false;
  let saveTimer = 0;

  function clamp(value) {
    const n = Number.parseInt(value, 10);
    if (!Number.isFinite(n)) {
      return MIN;
    }
    return Math.min(MAX, Math.max(MIN, n));
  }

  function boolFrom(value) {
    return value === true || value === 'true';
  }

  function angleFor(value) {
    return START_DEG + (ARC_DEG * clamp(value) / MAX);
  }

  function valueFromPointer(ev) {
    if (!root || !root.getBoundingClientRect) {
      return localValue;
    }
    const rect = root.getBoundingClientRect();
    const cx = rect.left + rect.width / 2;
    const cy = rect.top + rect.height / 2;
    const dx = ev.clientX - cx;
    const dy = ev.clientY - cy;
    if (Math.hypot(dx, dy) < 8) {
      return localValue;
    }
    let deg = Math.atan2(dy, dx) * 180 / Math.PI + 90;
    if (deg > 180) {
      deg -= 360;
    }
    const angle = Math.min(START_DEG + ARC_DEG, Math.max(START_DEG, deg));
    return Math.round(((angle - START_DEG) / ARC_DEG) * MAX);
  }

  function setVisual(value, className) {
    localValue = clamp(value);
    if (root) {
      root.dataset.volumeValue = String(localValue);
      root.style.setProperty('--volume-angle', `${Math.round(angleFor(localValue))}deg`);
      root.classList.toggle('saving', className === 'saving');
      root.classList.toggle('failed', className === 'failed');
    }
    if (range && range.value !== String(localValue)) {
      range.value = String(localValue);
    }
  }

  function setMutedVisual(muted) {
    localMuted = Boolean(muted);
    if (root) {
      root.dataset.volumeMuted = String(localMuted);
      root.classList.toggle('muted', localMuted);
    }
    if (muteButton) {
      muteButton.classList.toggle('on', localMuted);
      muteButton.setAttribute('aria-pressed', localMuted ? 'true' : 'false');
    }
  }

  function postVolume(value) {
    const body = new URLSearchParams();
    body.set('output_volume', String(clamp(value)));
    return fetch('/ui/volume', {
      method: 'POST',
      credentials: 'same-origin',
      headers: {
        'Content-Type': 'application/x-www-form-urlencoded',
      },
      body,
    });
  }

  function postMute(muted) {
    const body = new URLSearchParams();
    body.set('muted', muted ? 'true' : 'false');
    return fetch('/ui/volume/mute', {
      method: 'POST',
      credentials: 'same-origin',
      headers: {
        'Content-Type': 'application/x-www-form-urlencoded',
      },
      body,
    });
  }

  function scheduleDrain(delay) {
    if (saveTimer) {
      return;
    }
    saveTimer = window.setTimeout(() => {
      saveTimer = 0;
      drainSaves();
    }, delay);
  }

  function scheduleSave(value, finalCommit) {
    queuedValue = clamp(value);
    finalCommitNeeded = finalCommitNeeded || Boolean(finalCommit);
    if (finalCommit && saveTimer) {
      window.clearTimeout(saveTimer);
      saveTimer = 0;
    }
    if (inFlight) {
      return;
    }
    scheduleDrain(finalCommit ? 0 : SAVE_INTERVAL_MS);
  }

  async function drainSaves() {
    if (inFlight || queuedValue === null) {
      return;
    }
    const value = queuedValue;
    const wasFinal = finalCommitNeeded;
    queuedValue = null;
    finalCommitNeeded = false;
    inFlight = true;
    setVisual(localValue, 'saving');
    let failed = false;
    try {
      const res = await postVolume(value);
      if (res.status !== 204) {
        if (wasFinal || queuedValue === null) {
          failed = true;
          setVisual(authoritative, 'failed');
          window.setTimeout(() => setVisual(authoritative, ''), 350);
        }
        res.text().then((text) => {
          console.warn('volume-knob: save failed', res.status, text);
        }).catch(() => {});
      }
    } catch (err) {
      console.warn('volume-knob: volume POST failed', err);
      if (wasFinal || queuedValue === null) {
        failed = true;
        setVisual(authoritative, 'failed');
        window.setTimeout(() => setVisual(authoritative, ''), 350);
      }
    } finally {
      inFlight = false;
      if (queuedValue !== null) {
        scheduleDrain(finalCommitNeeded ? 0 : SAVE_INTERVAL_MS);
      } else if (!editing && !failed) {
        setVisual(localValue, '');
      }
    }
  }

  function beginEdit() {
    editing = true;
  }

  function updateEdit(value) {
    const next = clamp(value);
    setVisual(next, '');
    scheduleSave(next, false);
  }

  function commitEdit(value) {
    editing = false;
    const next = clamp(value);
    setVisual(next, '');
    scheduleSave(next, true);
  }

  function beginPointerTurn(ev) {
    if (ev.button != null && ev.button !== 0) {
      return;
    }
    if (ev.preventDefault) {
      ev.preventDefault();
    }
    draggingPointer = ev.pointerId;
    if (range && range.focus) {
      range.focus({ preventScroll: true });
    }
    beginEdit();
    if (root.setPointerCapture && ev.pointerId != null) {
      root.setPointerCapture(ev.pointerId);
    }
    updateEdit(valueFromPointer(ev));
  }

  function updatePointerTurn(ev) {
    if (draggingPointer === null || ev.pointerId !== draggingPointer) {
      return;
    }
    if (ev.preventDefault) {
      ev.preventDefault();
    }
    updateEdit(valueFromPointer(ev));
  }

  function endPointerTurn(ev) {
    if (draggingPointer === null || ev.pointerId !== draggingPointer) {
      return;
    }
    if (ev.preventDefault) {
      ev.preventDefault();
    }
    draggingPointer = null;
    if (root.releasePointerCapture && ev.pointerId != null) {
      root.releasePointerCapture(ev.pointerId);
    }
    commitEdit(valueFromPointer(ev));
  }

  function cancelPointerTurn(ev) {
    if (draggingPointer === null || ev.pointerId !== draggingPointer) {
      return;
    }
    draggingPointer = null;
    if (root.releasePointerCapture && ev.pointerId != null) {
      root.releasePointerCapture(ev.pointerId);
    }
    commitEdit(localValue);
  }

  function handleVolumeEvent(ev) {
    try {
      const data = JSON.parse(ev.data);
      const next = clamp(data.outputVolume);
      authoritative = next;
      if (data.outputMuted !== undefined) {
        authoritativeMuted = Boolean(data.outputMuted);
        setMutedVisual(authoritativeMuted);
      }
      if (!editing && !inFlight && queuedValue === null) {
        setVisual(next, '');
      }
    } catch (err) {
      console.warn('volume-knob: bad volume payload', ev.data, err);
    }
  }

  function toggleMute() {
    const next = !localMuted;
    setMutedVisual(next);
    postMute(next).then((res) => {
      if (res.status !== 204) {
        setMutedVisual(authoritativeMuted);
        res.text().then((text) => {
          console.warn('volume-knob: mute failed', res.status, text);
        }).catch(() => {});
      } else {
        authoritativeMuted = next;
      }
    }).catch((err) => {
      console.warn('volume-knob: mute POST failed', err);
      setMutedVisual(authoritativeMuted);
    });
  }

  function bind() {
    if (!root || !range) {
      return;
    }
    authoritative = clamp(root.dataset.volumeValue || range.value);
    authoritativeMuted = boolFrom(root.dataset.volumeMuted);
    setVisual(authoritative, '');
    setMutedVisual(authoritativeMuted);

    range.addEventListener('pointerdown', () => beginEdit());
    range.addEventListener('input', () => updateEdit(range.value));
    range.addEventListener('change', () => commitEdit(range.value));
    range.addEventListener('blur', () => {
      if (editing) {
        commitEdit(range.value);
      }
    });
    range.addEventListener('keydown', (ev) => {
      if (ev.key === 'Home' || ev.key === 'End' || ev.key === 'PageUp' || ev.key === 'PageDown' || ev.key.startsWith('Arrow')) {
        beginEdit();
      }
    });
    root.addEventListener('pointerdown', beginPointerTurn);
    root.addEventListener('pointermove', updatePointerTurn);
    root.addEventListener('pointerup', endPointerTurn);
    root.addEventListener('pointercancel', cancelPointerTurn);
    root.addEventListener('wheel', (ev) => {
      if (document.activeElement !== range && !root.matches(':hover')) {
        return;
      }
      ev.preventDefault();
      beginEdit();
      const delta = ev.deltaY < 0 ? 1 : -1;
      const next = clamp(localValue + delta);
      updateEdit(next);
      commitEdit(next);
    }, { passive: false });
    if (muteButton) {
      muteButton.addEventListener('click', toggleMute);
    }
  }

  function init() {
    root = document.querySelector('[data-volume-knob]');
    range = document.querySelector('[data-volume-range]');
    muteButton = document.querySelector('[data-volume-mute]');
    bind();
    window.Chassis.events.subscribe('volume', handleVolumeEvent);
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
