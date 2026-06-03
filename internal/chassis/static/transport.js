// Receiver chassis transport controls.
//
// Uses the shared /ui/events EventSource exposed by vfd-live.js and
// posts playback actions back to the transport endpoints.
(() => {
  'use strict';

  if (!window.Chassis) {
    console.warn('transport: window.Chassis missing; chassis.js failed to load?');
    return;
  }

  let adapterRef = metaContent('chassis-adapter-ref');
  let generation = parseInteger(metaContent('chassis-generation'), 0);
  let transportState = '';
  let draggingSeek = null;
  let serverSeekPercent = 0;
  let progressAnchor = null;
  let progressFrame = 0;

  function metaContent(name) {
    const el = document.querySelector(`meta[name="${name}"]`);
    return el ? el.getAttribute('content') || '' : '';
  }

  function parseInteger(raw, fallback) {
    const n = Number.parseInt(raw, 10);
    return Number.isFinite(n) ? n : fallback;
  }

  function parseMaybeInteger(raw) {
    const n = Number.parseInt(raw, 10);
    return Number.isFinite(n) ? n : null;
  }

  function clamp(n, min, max) {
    return Math.min(max, Math.max(min, n));
  }

  function nowMs() {
    if (window.performance && typeof window.performance.now === 'function') {
      return window.performance.now();
    }
    return Date.now();
  }

  function formatPercent(percent) {
    const rounded = Math.round(clamp(percent, 0, 100) * 1000) / 1000;
    if (Object.is(rounded, -0)) {
      return '0';
    }
    return String(rounded);
  }

  function strip() {
    return document.querySelector('.transport-strip');
  }

  function seekBar() {
    return document.querySelector('[data-transport-seek]');
  }

  function actionButton(action) {
    return document.querySelector(`[data-transport-action="${action}"]`);
  }

  function setText(selector, value) {
    const el = document.querySelector(selector);
    if (el) {
      el.textContent = value || '';
    }
  }

  function setButton(action, enabled) {
    const btn = actionButton(action);
    if (btn) {
      btn.disabled = !enabled;
    }
  }

  function setSeekDisabled(bar, disabled) {
    if (!bar) {
      return;
    }
    if (disabled) {
      bar.setAttribute('data-transport-seek-disabled', '');
      bar.setAttribute('aria-disabled', 'true');
    } else {
      bar.removeAttribute('data-transport-seek-disabled');
      bar.setAttribute('aria-disabled', 'false');
    }
  }

  function setSeekVisual(bar, percent) {
    if (!bar) {
      return;
    }
    const nextPercent = formatPercent(percent);
    if (bar.style && bar.style.setProperty) {
      bar.style.setProperty('--seek-percent', `${nextPercent}%`);
    }
    const fill = bar.querySelector('[data-transport-seek-fill]');
    if (fill) {
      fill.style.width = `${nextPercent}%`;
    }
    bar.setAttribute('aria-valuenow', String(nextPercent));
  }

  function seekPercentFromOffset(offsetMs, durationMs) {
    if (durationMs <= 0) {
      return 0;
    }
    return clamp((offsetMs / durationMs) * 100, 0, 100);
  }

  function restoreSeekVisual(bar) {
    setSeekVisual(bar, serverSeekPercent);
  }

  function stopProgressClock() {
    progressAnchor = null;
    if (progressFrame && typeof window.cancelAnimationFrame === 'function') {
      window.cancelAnimationFrame(progressFrame);
    }
    progressFrame = 0;
  }

  function scheduleProgressFrame() {
    if (progressFrame || typeof window.requestAnimationFrame !== 'function') {
      return;
    }
    progressFrame = window.requestAnimationFrame(tickProgress);
  }

  function tickProgress() {
    progressFrame = 0;
    const bar = seekBar();
    if (
      !bar ||
      !progressAnchor ||
      transportState !== 'playing' ||
      bar.hasAttribute('data-seek-interacting')
    ) {
      return;
    }
    const elapsedMs = Math.max(0, nowMs() - progressAnchor.startedAt);
    const offsetMs = clamp(progressAnchor.offsetMs + elapsedMs, 0, progressAnchor.durationMs);
    serverSeekPercent = seekPercentFromOffset(offsetMs, progressAnchor.durationMs);
    setSeekVisual(bar, serverSeekPercent);
    if (offsetMs < progressAnchor.durationMs) {
      scheduleProgressFrame();
    }
  }

  function updateProgressClock(offsetMs, durationMs) {
    if (transportState !== 'playing' || durationMs <= 0) {
      stopProgressClock();
      return;
    }
    progressAnchor = {
      offsetMs: clamp(offsetMs, 0, durationMs),
      durationMs,
      startedAt: nowMs(),
    };
    scheduleProgressFrame();
  }

  function percentFromPointer(bar, ev) {
    const rect = bar.getBoundingClientRect();
    if (!rect.width) {
      return 0;
    }
    return clamp(((ev.clientX - rect.left) / rect.width) * 100, 0, 100);
  }

  function canSeek(bar) {
    return Boolean(
      bar &&
      !bar.hasAttribute('data-transport-seek-disabled') &&
      adapterRef &&
      generation > 0,
    );
  }

  function postForm(url, fields) {
    const body = new URLSearchParams();
    Object.keys(fields).forEach((key) => {
      body.set(key, fields[key]);
    });
    return fetch(url, {
      method: 'POST',
      credentials: 'same-origin',
      headers: {
        'Content-Type': 'application/x-www-form-urlencoded',
      },
      body,
    });
  }

  async function postAction(action) {
    if (!adapterRef || generation <= 0) {
      return;
    }
    try {
      const res = await postForm('/ui/transport/action', {
        adapter_ref: adapterRef,
        generation: String(generation),
        action,
      });
      if (res.status !== 204) {
        const text = await res.text().catch(() => '');
        console.warn('transport: action failed', res.status, text);
      }
    } catch (err) {
      console.warn('transport: action POST failed', err);
    }
  }

  async function postSeek(offsetMs) {
    if (!adapterRef || generation <= 0) {
      return;
    }
    try {
      const res = await postForm('/ui/transport/seek', {
        adapter_ref: adapterRef,
        generation: String(generation),
        offset_ms: String(offsetMs),
      });
      if (res.status !== 204) {
        const text = await res.text().catch(() => '');
        console.warn('transport: seek failed', res.status, text);
      }
    } catch (err) {
      console.warn('transport: seek POST failed', err);
    }
  }

  function applyTransport(data) {
    const nextAdapterRef = data.adapterRef || '';
    const nextGeneration = parseInteger(data.generation, 0);
    const bar = seekBar();
    if (bar && draggingSeek && (nextAdapterRef !== draggingSeek.adapterRef || nextGeneration !== draggingSeek.generation)) {
      draggingSeek = null;
      bar.removeAttribute('data-seek-interacting');
    }

    transportState = data.state || transportState || '';
    adapterRef = nextAdapterRef;
    generation = nextGeneration;

    const root = strip();
    if (root) {
      root.dataset.transportState = transportState;
    }

    const actions = data.actionsEnabled || {};
    setButton('previous', Boolean(actions.previous));
    setButton('next', Boolean(actions.next));
    setButton('pauseResume', Boolean(actions.pauseResume));
    setButton('stop', Boolean(actions.stop));
    setButton('replay', Boolean(actions.replay));

    if (bar) {
      const rawOffsetMs = parseMaybeInteger(data.offsetMs);
      const rawDurationMs = parseMaybeInteger(data.durationMs);
      const offsetMs = rawOffsetMs === null ? 0 : rawOffsetMs;
      const durationMs = rawDurationMs === null ? 0 : rawDurationMs;
      bar.setAttribute('data-transport-offset-ms', String(offsetMs));
      bar.setAttribute('data-transport-duration-ms', String(durationMs));
      setSeekDisabled(bar, !actions.seek);
      serverSeekPercent = rawOffsetMs !== null && durationMs > 0
        ? seekPercentFromOffset(offsetMs, durationMs)
        : data.seekFillPercent || 0;
      updateProgressClock(offsetMs, durationMs);
      if (!bar.hasAttribute('data-seek-interacting')) {
        setSeekVisual(bar, serverSeekPercent);
      }
    } else {
      stopProgressClock();
    }

    setText('[data-transport-elapsed]', data.elapsedTime);
    setText('[data-transport-total]', data.totalTime);
    setText('[data-transport-percent]', data.percentPlayed);
  }

  function handleTransportEvent(ev) {
    try {
      applyTransport(JSON.parse(ev.data));
    } catch (err) {
      console.warn('transport: bad transport payload', ev.data, err);
    }
  }

  function bindActions() {
    Array.from(document.querySelectorAll('[data-transport-action]')).forEach((btn) => {
      btn.addEventListener('click', () => {
        const action = btn.getAttribute('data-transport-action');
        if (!action || btn.disabled) {
          return;
        }
        if (action === 'pauseResume' && transportState === 'stopped') {
          return;
        }
        if (action === 'pauseResume') {
          postAction(transportState === 'paused' ? 'resume' : 'pause');
          return;
        }
        postAction(action);
      });
    });
  }

  function bindSeek() {
    const bar = seekBar();
    if (!bar) {
      return;
    }

    bar.addEventListener('pointerdown', (ev) => {
      if (!canSeek(bar)) {
        return;
      }
      const durationMs = parseInteger(bar.getAttribute('data-transport-duration-ms'), 0);
      if (durationMs <= 0) {
        restoreSeekVisual(bar);
        return;
      }
      ev.preventDefault();
      draggingSeek = {
        pointerId: ev.pointerId,
        percent: percentFromPointer(bar, ev),
        adapterRef: adapterRef,
        generation: generation,
        durationMs: durationMs,
      };
      bar.setAttribute('data-seek-interacting', '');
      if (bar.setPointerCapture) {
        bar.setPointerCapture(ev.pointerId);
      }
      setSeekVisual(bar, draggingSeek.percent);
    });

    bar.addEventListener('pointermove', (ev) => {
      if (!draggingSeek || draggingSeek.pointerId !== ev.pointerId) {
        return;
      }
      draggingSeek.percent = percentFromPointer(bar, ev);
      setSeekVisual(bar, draggingSeek.percent);
    });

    bar.addEventListener('pointerup', (ev) => {
      if (!draggingSeek || draggingSeek.pointerId !== ev.pointerId) {
        return;
      }
      const drag = draggingSeek;
      draggingSeek = null;
      bar.removeAttribute('data-seek-interacting');
      if (bar.releasePointerCapture) {
        bar.releasePointerCapture(ev.pointerId);
      }
      if (
        !canSeek(bar) ||
        adapterRef !== drag.adapterRef ||
        generation !== drag.generation ||
        drag.durationMs <= 0
      ) {
        restoreSeekVisual(bar);
        return;
      }
      const offsetMs = Math.round(drag.durationMs * (drag.percent / 100));
      bar.setAttribute('data-transport-offset-ms', String(offsetMs));
      postSeek(offsetMs);
    });

    bar.addEventListener('pointercancel', (ev) => {
      if (!draggingSeek || draggingSeek.pointerId !== ev.pointerId) {
        return;
      }
      draggingSeek = null;
      bar.removeAttribute('data-seek-interacting');
      if (bar.releasePointerCapture) {
        bar.releasePointerCapture(ev.pointerId);
      }
      restoreSeekVisual(bar);
    });
  }

  function init() {
    const root = strip();
    if (root) {
      transportState = root.dataset.transportState || transportState;
    }
    bindActions();
    bindSeek();
    window.Chassis.events.subscribe('transport', handleTransportEvent);
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
