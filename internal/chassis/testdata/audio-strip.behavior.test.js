const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

// ---------------------------------------------------------------------------
// Shared fakes — mirrored from volume-knob.behavior.test.js
// ---------------------------------------------------------------------------

class FakeTarget {
  constructor() {
    this.listeners = new Map();
  }

  addEventListener(name, fn) {
    const list = this.listeners.get(name) || [];
    list.push(fn);
    this.listeners.set(name, list);
  }

  removeEventListener(name, fn) {
    const list = this.listeners.get(name) || [];
    this.listeners.set(name, list.filter((item) => item !== fn));
  }

  dispatch(name, event = {}) {
    for (const fn of this.listeners.get(name) || []) {
      fn({ type: name, target: this, ...event });
    }
  }
}

class FakeElement extends FakeTarget {
  constructor(value = '') {
    super();
    this.value = value;
    this.dataset = {};
    this.textContent = '';
    this.attrs = new Map();
    this.classes = new Set();
    this.classList = {
      toggle: (name, on) => {
        if (on === undefined) {
          if (this.classes.has(name)) {
            this.classes.delete(name);
          } else {
            this.classes.add(name);
          }
        } else if (on) {
          this.classes.add(name);
        } else {
          this.classes.delete(name);
        }
      },
      contains: (name) => this.classes.has(name),
      add: (name) => this.classes.add(name),
      remove: (name) => this.classes.delete(name),
    };
  }

  getAttribute(name) {
    return this.attrs.get(name) || null;
  }

  setAttribute(name, value) {
    this.attrs.set(name, value);
  }
}

// ---------------------------------------------------------------------------
// Harness factory
// ---------------------------------------------------------------------------

const PREVIEW_MS = 120;
const HOLD_MS = 500;

function createHarness() {
  // 10 EQ sliders [data-dsp-eq="0".."9"]
  const eqSliders = Array.from({ length: 10 }, (_, i) => {
    const el = new FakeElement('0');
    el.dataset.dspEq = String(i);
    return el;
  });

  // 4 knob ranges
  const knobBass = new FakeElement('0');
  knobBass.dataset.dspKnobRange = 'bass';
  const knobMid = new FakeElement('0');
  knobMid.dataset.dspKnobRange = 'mid';
  const knobTreble = new FakeElement('0');
  knobTreble.dataset.dspKnobRange = 'treble';
  const knobBalance = new FakeElement('0');
  knobBalance.dataset.dspKnobRange = 'balance';
  const knobRanges = [knobBass, knobMid, knobTreble, knobBalance];

  // 4 switches
  const swLoudness = new FakeElement();
  swLoudness.dataset.dspSwitch = 'loudness';
  const swMono = new FakeElement();
  swMono.dataset.dspSwitch = 'mono';
  const swSubsonic = new FakeElement();
  swSubsonic.dataset.dspSwitch = 'subsonic';
  const swDefeat = new FakeElement();
  swDefeat.dataset.dspSwitch = 'defeat';
  const switches = [swLoudness, swMono, swSubsonic, swDefeat];

  // Preset buttons
  const presetFlat = new FakeElement();
  presetFlat.dataset.dspPreset = 'Flat';
  const presetRock = new FakeElement();
  presetRock.dataset.dspPreset = 'Rock';
  const presetJazz = new FakeElement();
  presetJazz.dataset.dspPreset = 'Jazz';
  const presetVocal = new FakeElement();
  presetVocal.dataset.dspPreset = 'Vocal';
  const presets = [presetFlat, presetRock, presetJazz, presetVocal];

  // Memory buttons
  const memButtons = Array.from({ length: 3 }, (_, i) => {
    const el = new FakeElement();
    el.dataset.dspMemory = String(i + 1);
    el.textContent = `M${i + 1}`;
    return el;
  });

  // EQ LED and root
  const eqLed = new FakeElement();
  const audioStripRoot = new FakeElement();

  // --- selector maps ---
  const byDspEq = new Map(eqSliders.map((el) => [el.dataset.dspEq, el]));
  const byKnobRange = new Map(knobRanges.map((el) => [el.dataset.dspKnobRange, el]));
  const bySwitch = new Map(switches.map((el) => [el.dataset.dspSwitch, el]));
  const byPreset = new Map(presets.map((el) => [el.dataset.dspPreset.toLowerCase(), el]));
  const byMemory = new Map(memButtons.map((el) => [el.dataset.dspMemory, el]));

  function querySelectorAll(selector) {
    if (selector === '[data-dsp-eq]') return eqSliders.slice();
    if (selector === '[data-dsp-knob-range]') return knobRanges.slice();
    if (selector === '[data-dsp-switch]') return switches.slice();
    if (selector === '[data-dsp-preset]') return presets.slice();
    if (selector === '[data-dsp-memory]') return memButtons.slice();
    // attribute=value selectors for eq sliders
    const m = selector.match(/^\[data-dsp-eq="(\d+)"\]$/);
    if (m) { const el = byDspEq.get(m[1]); return el ? [el] : []; }
    return [];
  }

  function querySelector(selector) {
    if (selector === '[data-eq-led]') return eqLed;
    if (selector === '[data-audio-strip]') return audioStripRoot;
    // [data-dsp-knob-range="..."]
    const km = selector.match(/^\[data-dsp-knob-range="([^"]+)"\]$/);
    if (km) return byKnobRange.get(km[1]) || null;
    // [data-dsp-eq="..."]
    const em = selector.match(/^\[data-dsp-eq="(\d+)"\]$/);
    if (em) return byDspEq.get(em[1]) || null;
    // [data-dsp-switch="..."]
    const sm = selector.match(/^\[data-dsp-switch="([^"]+)"\]$/);
    if (sm) return bySwitch.get(sm[1]) || null;
    return null;
  }

  // --- fake timers ---
  const timers = new Map();
  let now = 0;
  let nextTimer = 1;

  function setTimeoutFake(fn, delay) {
    const id = nextTimer++;
    timers.set(id, { at: now + delay, fn });
    return id;
  }

  function clearTimeoutFake(id) {
    timers.delete(id);
  }

  function advance(ms) {
    now += ms;
    let ran = true;
    while (ran) {
      ran = false;
      const due = [...timers.entries()]
        .filter(([, timer]) => timer.at <= now)
        .sort((a, b) => a[1].at - b[1].at)[0];
      if (due) {
        timers.delete(due[0]);
        due[1].fn();
        ran = true;
      }
    }
  }

  // --- fake fetch ---
  const requests = [];

  function fetchFake(url, options) {
    let resolve;
    let reject;
    const promise = new Promise((res, rej) => { resolve = res; reject = rej; });
    requests.push({ url, options, body: String(options.body), resolve, reject });
    return promise;
  }

  // --- Chassis.events ---
  const subscriptions = new Map();

  // --- fake document ---
  const document = {
    readyState: 'complete',
    querySelector,
    querySelectorAll,
    addEventListener() {},
  };

  // --- fake window ---
  const window = {
    Chassis: {
      events: {
        subscribe(name, fn) {
          subscriptions.set(name, fn);
        },
      },
    },
    setTimeout: setTimeoutFake,
    clearTimeout: clearTimeoutFake,
  };

  const context = {
    console: { warn() {} },
    document,
    fetch: fetchFake,
    window,
  };
  vm.createContext(context);
  const code = fs.readFileSync(path.join(__dirname, '..', 'static', 'audio-strip.js'), 'utf8');
  vm.runInContext(code, context, { filename: 'audio-strip.js' });

  function emitAudioDsp(payload) {
    const fn = subscriptions.get('audioDsp');
    assert.equal(typeof fn, 'function', 'audioDsp subscription missing');
    fn({ data: JSON.stringify(payload) });
  }

  return {
    eqSliders,
    knobBass, knobMid, knobTreble, knobBalance,
    swLoudness, swMono, swSubsonic, swDefeat,
    presetFlat, presetRock, presetJazz, presetVocal,
    memButtons,
    eqLed,
    audioStripRoot,
    requests,
    advance,
    emitAudioDsp,
  };
}

// helper: parse JSON body from a request
function body(req) {
  return JSON.parse(req.body);
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

test('EQ slider input (pointerdown+input) then advance PREVIEW_MS posts preview', () => {
  const h = createHarness();
  h.eqSliders[0].dispatch('pointerdown');
  h.eqSliders[0].value = '3';
  h.eqSliders[0].dispatch('input');

  assert.equal(h.requests.length, 0, 'no post before preview timer fires');
  h.advance(PREVIEW_MS);
  assert.equal(h.requests.length, 1);
  const b = body(h.requests[0]);
  assert.equal(b.commit, false);
  assert.ok(Array.isArray(b.params.eq));
  assert.equal(b.params.eq[0], 3);
  assert.equal(h.requests[0].url, '/receiver/audio/dsp');
});

test('EQ slider change event posts commit=true', () => {
  const h = createHarness();
  h.eqSliders[2].dispatch('pointerdown');
  h.eqSliders[2].value = '-4';
  h.eqSliders[2].dispatch('input');
  h.eqSliders[2].dispatch('change');

  // change cancels preview timer and commits immediately
  assert.equal(h.requests.length, 1);
  const b = body(h.requests[0]);
  assert.equal(b.commit, true);
  assert.equal(b.params.eq[2], -4);
});

test('Loud switch click posts commit:true params:{loudness:true}', () => {
  const h = createHarness();
  h.swLoudness.dispatch('click');
  assert.equal(h.requests.length, 1);
  const b = body(h.requests[0]);
  assert.equal(b.commit, true);
  assert.equal(b.params.loudness, true);
  assert.equal(h.requests[0].url, '/receiver/audio/dsp');
});

test('Loud switch second click toggles off, posts loudness:false', () => {
  const h = createHarness();
  h.swLoudness.dispatch('click'); // on
  h.swLoudness.dispatch('click'); // off
  assert.equal(h.requests.length, 2);
  assert.equal(body(h.requests[1]).params.loudness, false);
});

test('EQ Out (defeat) switch click posts commit:true params:{enabled:false} (inverted)', () => {
  const h = createHarness();
  h.swDefeat.dispatch('click');
  assert.equal(h.requests.length, 1);
  const b = body(h.requests[0]);
  assert.equal(b.commit, true);
  assert.equal(b.params.enabled, false); // defeat engaged => DSP disabled
});

test('EQ Out switch second click posts enabled:true', () => {
  const h = createHarness();
  h.swDefeat.dispatch('click'); // defeat on => enabled:false
  h.swDefeat.dispatch('click'); // defeat off => enabled:true
  assert.equal(body(h.requests[1]).params.enabled, true);
});

test('Rock preset click sets sliders to rock curve and commits eq', () => {
  const h = createHarness();
  const rockCurve = [4, 3, 1, 0, -1, -1, 0, 2, 3, 4];
  h.presetRock.dispatch('click');
  assert.equal(h.requests.length, 1);
  const b = body(h.requests[0]);
  assert.equal(b.commit, true);
  assert.deepEqual(b.params.eq, rockCurve);
  // check sliders updated in DOM
  for (let i = 0; i < 10; i++) {
    assert.equal(h.eqSliders[i].value, String(rockCurve[i]));
  }
});

test('Jazz preset click commits jazz curve', () => {
  const h = createHarness();
  const jazzCurve = [2, 1, 0, 1, 1, 0, 0, 1, 2, 2];
  h.presetJazz.dispatch('click');
  assert.equal(h.requests.length, 1);
  assert.deepEqual(body(h.requests[0]).params.eq, jazzCurve);
});

test('Vocal preset click commits vocal curve', () => {
  const h = createHarness();
  const vocalCurve = [-2, -1, 0, 2, 3, 3, 2, 1, 0, -1];
  h.presetVocal.dispatch('click');
  assert.deepEqual(body(h.requests[0]).params.eq, vocalCurve);
});

test('Flat preset click commits flat (all zeros) curve', () => {
  const h = createHarness();
  h.presetFlat.dispatch('click');
  assert.deepEqual(body(h.requests[0]).params.eq, [0, 0, 0, 0, 0, 0, 0, 0, 0, 0]);
});

test('memory pointerdown + pointerup before HOLD_MS posts recall', () => {
  const h = createHarness();
  const mem = h.memButtons[0]; // slot 1
  mem.dispatch('pointerdown');
  h.advance(HOLD_MS - 1); // just before threshold
  mem.dispatch('pointerup');
  assert.equal(h.requests.length, 1);
  const b = body(h.requests[0]);
  assert.equal(b.op, 'recall');
  assert.equal(b.slot, 1);
  assert.equal(h.requests[0].url, '/receiver/audio/dsp/memory');
});

test('memory pointerdown then advance HOLD_MS posts store', () => {
  const h = createHarness();
  const mem = h.memButtons[1]; // slot 2
  mem.dispatch('pointerdown');
  h.advance(HOLD_MS);
  assert.equal(h.requests.length, 1);
  const b = body(h.requests[0]);
  assert.equal(b.op, 'store');
  assert.equal(b.slot, 2);
  assert.equal(h.requests[0].url, '/receiver/audio/dsp/memory');
});

test('memory pointerdown then pointerleave before HOLD_MS cancels store and no recall', () => {
  const h = createHarness();
  const mem = h.memButtons[2]; // slot 3
  mem.dispatch('pointerdown');
  h.advance(HOLD_MS - 1);
  mem.dispatch('pointerleave');
  h.advance(HOLD_MS * 2); // advance well past — nothing should fire
  assert.equal(h.requests.length, 0);
});

test('audioDsp SSE event updates bass knob when not editing', () => {
  const h = createHarness();
  h.emitAudioDsp({
    params: { bass: 5, mid: 0, treble: 0, balance: 0, eq: [0, 0, 0, 0, 0, 0, 0, 0, 0, 0] },
    engaged: false,
    persisted: false,
  });
  assert.equal(h.knobBass.value, '5');
});

test('audioDsp SSE event updates treble and mid knobs', () => {
  const h = createHarness();
  h.emitAudioDsp({
    params: { bass: 0, mid: 3, treble: -2, balance: 10, eq: [] },
    engaged: false,
    persisted: false,
  });
  assert.equal(h.knobMid.value, '3');
  assert.equal(h.knobTreble.value, '-2');
  assert.equal(h.knobBalance.value, '10');
});

test('audioDsp SSE event toggles loudness switch on', () => {
  const h = createHarness();
  h.emitAudioDsp({
    params: { loudness: true, mono: false, subsonic: false, enabled: true, eq: [] },
    engaged: false,
    persisted: false,
  });
  assert.ok(h.swLoudness.classes.has('on'), 'loudness switch should have class on');
  assert.equal(h.swLoudness.attrs.get('aria-checked'), 'true');
});

test('audioDsp SSE event sets defeat switch on when enabled:false', () => {
  const h = createHarness();
  h.emitAudioDsp({
    params: { loudness: false, mono: false, subsonic: false, enabled: false, eq: [] },
    engaged: false,
    persisted: false,
  });
  assert.ok(h.swDefeat.classes.has('on'), 'defeat switch should be on when enabled is false');
});

test('audioDsp SSE event sets eq-led class when engaged:true', () => {
  const h = createHarness();
  h.emitAudioDsp({
    params: { eq: [] },
    engaged: true,
    persisted: false,
  });
  assert.ok(h.eqLed.classes.has('on'));
});

test('audioDsp SSE event sets data-dsp-persisted on root when persisted:true', () => {
  const h = createHarness();
  h.emitAudioDsp({
    params: { eq: [] },
    engaged: false,
    persisted: true,
  });
  assert.equal(h.audioStripRoot.dataset.dspPersisted, 'true');
});

test('knob bass input posts preview, change posts commit', () => {
  const h = createHarness();
  h.knobBass.dispatch('pointerdown');
  h.knobBass.value = '3';
  h.knobBass.dispatch('input');
  assert.equal(h.requests.length, 0);
  h.advance(PREVIEW_MS);
  assert.equal(h.requests.length, 1);
  assert.equal(body(h.requests[0]).commit, false);
  assert.equal(body(h.requests[0]).params.bass, 3);

  h.knobBass.value = '5';
  h.knobBass.dispatch('change');
  assert.equal(h.requests.length, 2);
  assert.equal(body(h.requests[1]).commit, true);
  assert.equal(body(h.requests[1]).params.bass, 5);
});
