const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

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
    this.attrs = new Map();
    this.rect = { left: 0, top: 0, width: 100, height: 100 };
    this.styleValues = new Map();
    this.style = {
      setProperty: (name, value) => this.styleValues.set(name, value),
    };
    this.classes = new Set();
    this.classList = {
      toggle: (name, on) => {
        if (on) {
          this.classes.add(name);
        } else {
          this.classes.delete(name);
        }
      },
      contains: (name) => this.classes.has(name),
    };
  }

  setAttribute(name, value) {
    this.attrs.set(name, String(value));
  }

  getAttribute(name) {
    return this.attrs.get(name);
  }

  matches(selector) {
    return selector === ':hover';
  }

  getBoundingClientRect() {
    return this.rect;
  }

  setPointerCapture() {}

  releasePointerCapture() {}

  focus() {}
}

function createHarness(initialValue = 40, initialMuted = false) {
  const root = new FakeElement();
  root.dataset.volumeValue = String(initialValue);
  root.dataset.volumeMuted = String(initialMuted);
  const range = new FakeElement(String(initialValue));
  const mute = new FakeElement();
  mute.dataset.volumeMute = '';
  mute.setAttribute('aria-pressed', String(initialMuted));
  const lamp = new FakeElement();
  lamp.dataset.volumeMuteLamp = '';
  const source = new FakeTarget();
  const subscriptions = new Map();
  const requests = [];
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

  function fetchFake(url, options) {
    let resolve;
    let reject;
    const promise = new Promise((res, rej) => {
      resolve = res;
      reject = rej;
    });
    requests.push({
      url,
      options,
      body: String(options.body),
      resolve,
      reject,
    });
    return promise;
  }

  const document = new FakeTarget();
  document.readyState = 'complete';
  document.activeElement = range;
  document.querySelector = (selector) => {
    if (selector === '[data-volume-knob]') {
      return root;
    }
    if (selector === '[data-volume-range]') {
      return range;
    }
    if (selector === '[data-volume-mute]') {
      return mute;
    }
    if (selector === '[data-volume-mute-lamp]') {
      return lamp;
    }
    return null;
  };
  range.focus = () => {
    document.activeElement = range;
  };
  source.emitVolume = (outputVolume, outputMuted = false) => {
    const fn = subscriptions.get('volume');
    if (fn) {
      fn({ data: JSON.stringify({ outputVolume, outputMuted }) });
    }
  };

  const window = {
    Chassis: {
      events: {
        source,
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
    URLSearchParams,
  };
  vm.createContext(context);
  const code = fs.readFileSync(path.join(__dirname, '..', 'static', 'volume-knob.js'), 'utf8');
  vm.runInContext(code, context, { filename: 'volume-knob.js' });

  return { root, range, mute, lamp, source, requests, advance };
}

async function settle() {
  for (let i = 0; i < 4; i += 1) {
    await Promise.resolve();
  }
}

function postedVolume(req) {
  return new URLSearchParams(req.body).get('output_volume');
}

function postedMuted(req) {
  return new URLSearchParams(req.body).get('muted');
}

test('coalesces non-final saves behind one in-flight request and preserves 200ms spacing', async () => {
  const h = createHarness(40);

  h.range.dispatch('pointerdown');
  h.range.value = '41';
  h.range.dispatch('input');
  h.range.value = '42';
  h.range.dispatch('input');

  h.advance(199);
  assert.equal(h.requests.length, 0);
  h.advance(1);
  assert.equal(h.requests.length, 1);
  assert.equal(postedVolume(h.requests[0]), '42');

  h.range.value = '43';
  h.range.dispatch('input');
  h.range.value = '44';
  h.range.dispatch('input');
  assert.equal(h.requests.length, 1);

  h.requests[0].resolve({ status: 204, text: async () => '' });
  await settle();
  assert.equal(h.requests.length, 1, 'queued non-final save must not drain immediately after a fast response');
  h.advance(199);
  assert.equal(h.requests.length, 1);
  h.advance(1);
  assert.equal(h.requests.length, 2);
  assert.equal(postedVolume(h.requests[1]), '44');
});

test('final commit bypasses the throttle after an in-flight save', async () => {
  const h = createHarness(40);

  h.range.dispatch('pointerdown');
  h.range.value = '50';
  h.range.dispatch('input');
  h.advance(200);
  assert.equal(h.requests.length, 1);

  h.range.value = '60';
  h.range.dispatch('input');
  h.range.dispatch('change');
  assert.equal(h.requests.length, 1);

  h.requests[0].resolve({ status: 204, text: async () => '' });
  await settle();
  h.advance(0);
  assert.equal(h.requests.length, 2);
  assert.equal(postedVolume(h.requests[1]), '60');
});

test('defers SSE while editing and rolls failed final save back to last authoritative value', async () => {
  const h = createHarness(40);

  h.source.emitVolume(50);
  assert.equal(h.range.value, '50');

  h.range.dispatch('pointerdown');
  h.range.value = '70';
  h.range.dispatch('input');
  h.source.emitVolume(30);
  assert.equal(h.range.value, '70');

  h.range.dispatch('change');
  h.advance(0);
  assert.equal(h.requests.length, 1);
  assert.equal(postedVolume(h.requests[0]), '70');

  h.requests[0].resolve({ status: 500, text: async () => 'nope' });
  await settle();
  assert.equal(h.range.value, '30');
  assert.equal(h.root.classes.has('failed'), true);
});

test('turning the knob with pointer drag commits the radial volume value', async () => {
  const h = createHarness(40);

  h.root.dispatch('pointerdown', {
    button: 0,
    pointerId: 7,
    clientX: 50,
    clientY: 0,
    preventDefault() {},
  });
  assert.equal(h.range.value, '50');

  h.root.dispatch('pointermove', {
    pointerId: 7,
    clientX: 100,
    clientY: 50,
    preventDefault() {},
  });
  h.root.dispatch('pointerup', {
    pointerId: 7,
    clientX: 100,
    clientY: 50,
    preventDefault() {},
  });

  h.advance(0);
  assert.equal(h.requests.length, 1);
  assert.equal(postedVolume(h.requests[0]), '83');
});

test('mute button posts separate muted state and follows SSE state', async () => {
  const h = createHarness(40, false);

  h.mute.dispatch('click');
  assert.equal(h.requests.length, 1);
  assert.equal(h.requests[0].url, '/ui/volume/mute');
  assert.equal(postedMuted(h.requests[0]), 'true');
  assert.equal(h.mute.classes.has('on'), true);
  assert.equal(h.lamp.classes.has('on'), true);
  assert.equal(h.mute.getAttribute('aria-pressed'), 'true');
  assert.equal(h.root.classes.has('muted'), true);

  h.source.emitVolume(40, false);
  assert.equal(h.mute.classes.has('on'), false);
  assert.equal(h.lamp.classes.has('on'), false);
  assert.equal(h.mute.getAttribute('aria-pressed'), 'false');
  assert.equal(h.root.classes.has('muted'), false);
});
