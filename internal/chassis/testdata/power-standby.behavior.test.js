const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

// Behavior spec for the cold-start power-on and the POWER key in
// static/power-on.js.
//
// Cold start (once per tab session): the faceplate loads dark with the
// PWR lamp amber (`powering-up`), then after a relay delay it clears and
// the normal `warming` bloom runs. Reloads in the same tab, reduced
// motion, and unavailable storage all skip it.
//
// POWER key: toggles a client-side panel standby (`panel-standby`): the
// displays and lamps go dark and PWR shows amber, while the bridge keeps
// running. Pressing POWER again, or any other key/click, wakes the panel.

class FakeClassList {
  constructor() { this.classes = new Set(); }
  add(...n) { n.forEach((c) => this.classes.add(c)); }
  remove(...n) { n.forEach((c) => this.classes.delete(c)); }
  contains(c) { return this.classes.has(c); }
  toggle(c, on) {
    const want = on === undefined ? !this.classes.has(c) : on;
    if (want) this.classes.add(c); else this.classes.delete(c);
    return want;
  }
}

class FakeEl {
  constructor() {
    this.classList = new FakeClassList();
    this.attrs = new Map();
    this.listeners = new Map();
  }
  setAttribute(k, v) { this.attrs.set(k, String(v)); }
  getAttribute(k) { return this.attrs.has(k) ? this.attrs.get(k) : null; }
  addEventListener(name, fn) { this.listeners.set(name, fn); }
  contains(other) { return other === this; }
  fire(name, ev = {}) { const fn = this.listeners.get(name); if (fn) fn({ target: this, ...ev }); }
}

function createHarness({ session = {}, sessionThrows = false, reducedMotion = false, noSession = false, preMarked = false } = {}) {
  const timers = [];
  const body = new FakeEl();
  if (preMarked) body.classList.add('powering-up');
  body.offsetWidth = 0;
  const powerBtn = new FakeEl();
  const powerLed = new FakeEl();
  const docListeners = new Map();
  const document = {
    body,
    querySelector(sel) {
      if (sel === '[data-power-btn]') return powerBtn;
      if (sel === '[data-power-led]') return powerLed;
      return null;
    },
    addEventListener(name, fn) { docListeners.set(name, fn); },
  };
  const store = new Map(Object.entries(session));
  const sessionStorage = {
    getItem(k) { if (sessionThrows) throw new Error('blocked'); return store.has(k) ? store.get(k) : null; },
    setItem(k, v) { if (sessionThrows) throw new Error('blocked'); store.set(k, String(v)); },
  };
  const window = {
    Chassis: {
      State: { IDLE: 'idle', LIVE: 'live' },
      animators: { register(a) { a.handleState('idle'); } },
    },
    matchMedia: () => ({ matches: reducedMotion }),
    console: { log() {} },
  };
  if (!noSession) window.sessionStorage = sessionStorage;
  const context = {
    window,
    document,
    console: { log() {}, warn() {} },
    setTimeout: (fn, ms) => { timers.push({ fn, ms }); return timers.length; },
    clearTimeout() {},
  };
  vm.createContext(context);
  const code = fs.readFileSync(path.join(__dirname, '..', 'static', 'power-on.js'), 'utf8');
  vm.runInContext(code, context, { filename: 'power-on.js' });
  const flush = () => { while (timers.length) timers.shift().fn(); };
  const runNext = () => { const t = timers.shift(); if (t) t.fn(); return t; };
  return { body, powerBtn, powerLed, store, timers, flush, runNext, docListeners };
}

const has = (el, c) => el.classList.contains(c);

test('first load in a tab starts dark with an amber standby lamp, then the relay closes and the panel warms', () => {
  const h = createHarness();
  assert.equal(has(h.body, 'powering-up'), true, 'faceplate starts dark');
  assert.equal(h.powerLed.getAttribute('aria-label'), 'Power: standby');
  assert.equal(h.store.get('chassis.poweredOn'), '1', 'cold start recorded for this tab session');

  const relay = h.runNext();
  assert.ok(relay.ms >= 400, 'relay delay is perceptible');
  assert.equal(has(h.body, 'powering-up'), false);
  assert.equal(has(h.body, 'warming'), true, 'relay close fires the warm bloom');
  assert.equal(h.powerLed.getAttribute('aria-label'), 'Power on');
});

test('reload in the same tab does not replay the cold start', () => {
  const h = createHarness({ session: { 'chassis.poweredOn': '1' } });
  assert.equal(has(h.body, 'powering-up'), false);
  assert.equal(has(h.body, 'warming'), false);
});

test('reduced motion skips the cold start', () => {
  const h = createHarness({ reducedMotion: true });
  assert.equal(has(h.body, 'powering-up'), false);
});

test('unavailable session storage skips the cold start rather than replaying it every load', () => {
  assert.equal(has(createHarness({ sessionThrows: true }).body, 'powering-up'), false);
  assert.equal(has(createHarness({ noSession: true }).body, 'powering-up'), false);
});

test('POWER puts the panel in standby and says the bridge keeps running', () => {
  const h = createHarness({ session: { 'chassis.poweredOn': '1' } });
  assert.equal(h.powerBtn.getAttribute('aria-pressed'), 'true');
  h.powerBtn.fire('click');
  assert.equal(has(h.body, 'panel-standby'), true);
  assert.equal(h.powerBtn.getAttribute('aria-pressed'), 'false');
  assert.match(h.powerBtn.getAttribute('title'), /bridge keeps running/i);
  assert.equal(h.powerLed.getAttribute('aria-label'), 'Power: standby');
});

test('POWER again wakes the panel with the warm bloom', () => {
  const h = createHarness({ session: { 'chassis.poweredOn': '1' } });
  h.powerBtn.fire('click');
  h.flush();
  h.powerBtn.fire('click');
  assert.equal(has(h.body, 'panel-standby'), false);
  assert.equal(has(h.body, 'warming'), true);
  assert.equal(h.powerBtn.getAttribute('aria-pressed'), 'true');
  assert.equal(h.powerLed.getAttribute('aria-label'), 'Power on');
});

test('any other key or click wakes a standby panel', () => {
  const h = createHarness({ session: { 'chassis.poweredOn': '1' } });
  h.powerBtn.fire('click');
  const other = new FakeEl();
  h.docListeners.get('pointerdown')({ target: other });
  assert.equal(has(h.body, 'panel-standby'), false);

  h.powerBtn.fire('click');
  h.docListeners.get('keydown')({ target: other, key: 'Enter' });
  assert.equal(has(h.body, 'panel-standby'), false);
});

test('pressing POWER itself does not count as a wake-up click', () => {
  const h = createHarness({ session: { 'chassis.poweredOn': '1' } });
  h.powerBtn.fire('click');
  h.docListeners.get('pointerdown')({ target: h.powerBtn });
  assert.equal(has(h.body, 'panel-standby'), true, 'the POWER click owns the toggle');
});

// shell.html's inline pre-paint script adds `powering-up` (and records the
// session key) before first paint so the lit panel never flashes; power-on.js
// must carry that already-dark panel through the relay close.
test('a body pre-marked powering-up by the pre-paint script still completes the relay close', () => {
  const h = createHarness({ session: { 'chassis.poweredOn': '1' }, preMarked: true });
  assert.equal(has(h.body, 'powering-up'), true);
  h.runNext();
  assert.equal(has(h.body, 'powering-up'), false);
  assert.equal(has(h.body, 'warming'), true);
});
