// Receiver chassis meter telemetry. Spec 5A.
(() => {
  'use strict';

  if (!window.Chassis || !window.Chassis.events || !window.Chassis.events.subscribe) {
    console.warn('meter: subscribe helper missing');
    return;
  }

  let lastGeneration = 0;

  function setText(selector, value) {
    const el = document.querySelector(selector);
    if (el) el.textContent = value == null ? '' : String(value);
  }

  function setLamp(selector, active) {
    const el = document.querySelector(selector);
    if (el) el.classList.toggle('active', !!active);
  }

  function updateHLS(strip) {
    const cached = Math.max(0, Number(strip.hlsCachedSegments || 0));
    const max = Math.max(0, Number(strip.hlsMaxSegments || 0));
    Array.from(document.querySelectorAll('[data-meter-hls-seg]')).forEach((seg, idx) => {
      const threshold = max <= 0 ? 0 : Math.ceil((cached / max) * 12);
      seg.classList.toggle('on', idx < threshold);
    });
  }

  function drawLine(canvasId, values) {
    const canvas = document.getElementById(canvasId);
    if (!canvas || !canvas.getContext) return;
    const ctx = canvas.getContext('2d');
    const width = canvas.width;
    const height = canvas.height;
    ctx.clearRect(0, 0, width, height);
    if (!Array.isArray(values) || values.length === 0) return;
    const max = values.reduce((m, v) => Math.max(m, Number(v) || 0), 1);
    ctx.beginPath();
    values.forEach((v, idx) => {
      const x = values.length === 1 ? width : (idx / (values.length - 1)) * width;
      const y = height - ((Number(v) || 0) / max) * (height - 4) - 2;
      if (idx === 0) ctx.moveTo(x, y);
      else ctx.lineTo(x, y);
    });
    ctx.strokeStyle = '#7cffb2';
    ctx.lineWidth = 2;
    ctx.stroke();
  }

  function applyMeter(data) {
    const strip = data.sourceStrip || {};
    const mid = data.midRow || {};
    const readout = data.readout || {};
    if (data.generation !== lastGeneration || data.state === 'idle') {
      lastGeneration = data.generation || 0;
    }

    setText('[data-meter-audio-in]', strip.audioIn);
    setText('[data-meter-audio-out]', strip.audioOut);
    setText('[data-meter-src]', strip.src);
    setText('[data-meter-crop]', strip.crop);
    setText('[data-meter-hls-buffer]', strip.hlsBuffer);
    setText('[data-meter-drops]', strip.drops);
    setText('[data-meter-bitrate]', mid.bitrateMbps);
    setText('[data-meter-freq-khz]', mid.freqKHz);
    setText('[data-meter-mode]', mid.mode);
    setLamp('[data-meter-standard-ntsc]', mid.standard === 'ntsc');
    setLamp('[data-meter-standard-pal]', mid.standard === 'pal');
    setText('[data-meter-field-lock]', mid.fieldLock);
    setText('[data-meter-throughput]', mid.throughputMBs);
    setText('[data-meter-ack]', mid.ackMS);
    setText('[data-meter-output]', readout.output);
    setText('[data-meter-aspect]', readout.aspect);
    setText('[data-meter-pipe]', readout.pipe);
    setText('[data-meter-speed]', readout.speed);
    setText('[data-meter-link]', readout.link);
    updateHLS(strip);
    drawLine('throughput-canvas', mid.throughputHistoryMBs);
    drawLine('ack-canvas', mid.ackHistoryMS);
    const scopes = document.querySelector('[data-meter-audio-scopes-status]');
    if (scopes && data.audioScopes) {
      scopes.setAttribute('data-meter-audio-scopes-status', data.audioScopes.status || 'pending');
    }
  }

  window.Chassis.events.subscribe('meter', (ev) => {
    try {
      applyMeter(JSON.parse(ev.data));
    } catch (err) {
      console.warn('meter: bad meter payload', ev.data, err);
    }
  });

  // ---- Audio scope renderer (Spec 5B) ----
  // Drives the meter DOM hooks from the 30 Hz `audio` SSE event.
  const vuBars = Array.from(document.querySelectorAll('.tr-vu .vu-lr .ch-bar'));
  const vuChBarL = vuBars[0];
  const vuChBarR = vuBars[1];
  const phaseNeedle = document.getElementById('phase-needle');
  const lufsTextEl = document.querySelector('#lufs-val .seg-text');
  const spectrumCanvas = document.getElementById('spectrum-canvas');
  const spectrumCtx = spectrumCanvas && spectrumCanvas.getContext('2d');
  const gonioCanvas = document.getElementById('gonio-canvas');
  const gonioCtx = gonioCanvas && gonioCanvas.getContext('2d');

  const peakHold = new Array(32).fill(-90);
  // 0.5 dB per frame at 60 fps = 30 dB/s — quite aggressive vs.
  // industry-standard VU peak-hold (~1.5-3 dB/s with a 1-2 s hold).
  // Starting value; tune visually after first integration. If peak
  // ticks disappear too quickly to see, try 0.02-0.05.
  const peakHoldDecayPerFrame = 0.5;
  let lastAudioGeneration = 0;
  let lastSpectrum = null;
  let lastGoniometer = null;
  let audioIsLive = false;

  function setVUBarSegments(chBar, level) {
    if (!chBar) return;
    const segs = chBar.querySelectorAll('.s');
    const lit = Math.round(Math.max(0, Math.min(1, level)) * 12);
    segs.forEach((s, i) => s.classList.toggle('on', i < lit));
  }

  function renderAudioPending() {
    audioIsLive = false;
    setVUBarSegments(vuChBarL, 0);
    setVUBarSegments(vuChBarR, 0);
    if (phaseNeedle) phaseNeedle.style.left = '';
    if (lufsTextEl) lufsTextEl.textContent = '--.-';
    if (spectrumCtx) spectrumCtx.clearRect(0, 0, spectrumCanvas.width, spectrumCanvas.height);
    for (let i = 0; i < peakHold.length; i++) peakHold[i] = -90;
    if (gonioCtx) gonioCtx.clearRect(0, 0, gonioCanvas.width, gonioCanvas.height);
    lastSpectrum = null;
    lastGoniometer = null;
  }

  function renderAudioLive(payload) {
    audioIsLive = true;
    setVUBarSegments(vuChBarL, payload.vu.left.peak);
    setVUBarSegments(vuChBarR, payload.vu.right.peak);
    if (phaseNeedle) {
      const pct = 50 + payload.phaseCorr * 36;
      phaseNeedle.style.left = `calc(${pct}% - 1.5px)`;
    }
    if (lufsTextEl) {
      const v = payload.lufsShort;
      lufsTextEl.textContent = v <= -100 ? '--.-' : v.toFixed(1);
    }
    lastSpectrum = payload.spectrum;
    lastGoniometer = payload.goniometer;
  }

  function paintAudio() {
    if (!audioIsLive) {
      requestAnimationFrame(paintAudio);
      return;
    }
    if (spectrumCtx && lastSpectrum && lastSpectrum.length === 32) {
      const w = spectrumCanvas.width;
      const h = spectrumCanvas.height;
      spectrumCtx.clearRect(0, 0, w, h);
      const gap = 1;
      const barW = Math.max(2, Math.floor((w - gap * 31) / 32));
      for (let i = 0; i < 32; i++) {
        const db = Math.max(-90, Math.min(0, Number(lastSpectrum[i]) || -90));
        if (db > peakHold[i]) peakHold[i] = db;
        else peakHold[i] = Math.max(-90, peakHold[i] - peakHoldDecayPerFrame);
        const norm = Math.max(0, Math.min(1, (db + 60) / 60));
        const peakNorm = Math.max(0, Math.min(1, (peakHold[i] + 60) / 60));
        const x = i * (barW + gap);
        const barH = Math.max(1, norm * h);
        spectrumCtx.fillStyle = i < 20 ? '#7cffb2' : '#ffd76a';
        spectrumCtx.fillRect(x, h - barH, barW, barH);
        spectrumCtx.fillStyle = '#ff6f61';
        spectrumCtx.fillRect(x, h - peakNorm * h, barW, 1);
      }
    }
    if (gonioCtx && lastGoniometer) {
      const w = gonioCanvas.width;
      const h = gonioCanvas.height;
      gonioCtx.fillStyle = 'rgba(0,0,0,0.15)';
      gonioCtx.fillRect(0, 0, w, h);
      gonioCtx.fillStyle = '#7cffb2';
      const cx = w / 2, cy = h / 2;
      const scale = Math.min(w, h) / 2 * 0.9;
      const r2 = 0.70710678;
      for (let i = 0; i < lastGoniometer.length; i++) {
        const pair = lastGoniometer[i];
        const x = cx + (pair[0] - pair[1]) * r2 * scale;
        const y = cy - (pair[0] + pair[1]) * r2 * scale;
        gonioCtx.fillRect(x, y, 1, 1);
      }
    }
    requestAnimationFrame(paintAudio);
  }

  window.Chassis.events.subscribe('audio', (ev) => {
    let payload;
    try {
      payload = JSON.parse(ev.data);
    } catch (err) {
      console.warn('meter.js: bad audio payload', ev.data, err);
      return;
    }
    if (payload.status !== 'live') {
      renderAudioPending();
      return;
    }
    if (payload.generation !== lastAudioGeneration) {
      lastAudioGeneration = payload.generation;
      for (let i = 0; i < peakHold.length; i++) peakHold[i] = -90;
      lastSpectrum = null;
      lastGoniometer = null;
    }
    renderAudioLive(payload);
  });

  requestAnimationFrame(paintAudio);
})();
