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
})();
