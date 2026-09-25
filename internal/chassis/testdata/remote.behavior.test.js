const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

// Behavior spec for static/remote.js, the phone remote. Below 600px the
// chassis defaults to a remote-control view (chassis.css); this script
// owns the PANEL/REMOTE toggle (body[data-phone-view], remembered per
// browser), the 1-12 keypad that recalls presets by pressing the matching
// preset tile, and VOL-/VOL+ keys that step the existing volume control.

class FakeEl {
  constructor(attrs = {}, classes = []) {
    this.attrs = new Map(Object.entries(attrs));
    this.classes = new Set(classes);
    this.classList = {
      contains: (c) => this.classes.has(c),
      toggle: (c, on) => { if (on) this.classes.add(c); else this.classes.delete(c); },
    };
    this.listeners = new Map();
    this.events = [];
    this.clicks = 0;
    this.textContent = '';
    this.value = '';
  }
  getAttribute(k) { return this.attrs.has(k) ? this.attrs.get(k) : null; }
  setAttribute(k, v) { this.attrs.set(k, String(v)); }
  addEventListener(name, fn) { this.listeners.set(name, fn); }
  dispatchEvent(ev) { this.events.push(ev.type); }
  click() { this.clicks += 1; const fn = this.listeners.get('click'); if (fn) fn({ currentTarget: this }); }
}

function createHarness({ stored = null, storageThrows = false, volume = '40' } = {}) {
  const body = new FakeEl();
  const toggle = new FakeEl();
  const tiles = new Map([
    ['1', new FakeEl({ 'data-slot': '1' })],
    ['3', new FakeEl({ 'data-slot': '3' }, ['empty'])],
  ]);
  const keys = ['1', '3'].map((n) => new FakeEl({ 'data-remote-preset': n }));
  const volDown = new FakeEl({ 'data-remote-volume': '-5' });
  const volUp = new FakeEl({ 'data-remote-volume': '5' });
  const range = new FakeEl();
  range.value = volume;
  const flashes = [];
  const store = new Map(stored === null ? [] : [['chassis.phoneView', stored]]);
  const document = {
    readyState: 'complete',
    body,
    querySelector(sel) {
      if (sel === '[data-phone-view-toggle]') return toggle;
      if (sel === '[data-volume-range]') return range;
      const m = /^\.preset-bank \.preset\[data-slot="(\d+)"\]$/.exec(sel);
      if (m) return tiles.get(m[1]) || null;
      return null;
    },
    querySelectorAll(sel) {
      if (sel === '[data-remote-preset]') return keys;
      if (sel === '[data-remote-volume]') return [volDown, volUp];
      return [];
    },
    addEventListener() {},
  };
  const window = {
    localStorage: {
      getItem(k) { if (storageThrows) throw new Error('blocked'); return store.has(k) ? store.get(k) : null; },
      setItem(k, v) { if (storageThrows) throw new Error('blocked'); store.set(k, String(v)); },
    },
    Chassis: { vfd: { flash: (...lines) => flashes.push(lines) } },
  };
  class Event { constructor(type) { this.type = type; } }
  const context = { document, window, Event, console: { warn() {} } };
  vm.createContext(context);
  const code = fs.readFileSync(path.join(__dirname, '..', 'static', 'remote.js'), 'utf8');
  vm.runInContext(code, context, { filename: 'remote.js' });
  return { body, toggle, tiles, keys, volDown, volUp, range, flashes, store };
}

test('phones default to the remote view and PANEL switches to the full faceplate', () => {
  const h = createHarness();
  assert.equal(h.body.getAttribute('data-phone-view'), 'remote');
  assert.equal(h.toggle.textContent, 'Panel');
  h.toggle.click();
  assert.equal(h.body.getAttribute('data-phone-view'), 'panel');
  assert.equal(h.toggle.textContent, 'Remote');
  assert.equal(h.store.get('chassis.phoneView'), 'panel');
  h.toggle.click();
  assert.equal(h.body.getAttribute('data-phone-view'), 'remote');
});

test('a remembered panel view is restored and storage failures leave the toggle working', () => {
  assert.equal(createHarness({ stored: 'panel' }).body.getAttribute('data-phone-view'), 'panel');
  assert.equal(createHarness({ stored: 'sideways' }).body.getAttribute('data-phone-view'), 'remote');
  const h = createHarness({ storageThrows: true });
  h.toggle.click();
  assert.equal(h.body.getAttribute('data-phone-view'), 'panel');
});

test('a number key recalls its preset by pressing the matching preset tile', () => {
  const h = createHarness();
  h.keys[0].click();
  assert.equal(h.tiles.get('1').clicks, 1);
  assert.deepEqual(h.flashes, []);
});

test('a number key for an empty slot flashes EMPTY instead of casting', () => {
  const h = createHarness();
  h.keys[1].click();
  assert.equal(h.tiles.get('3').clicks, 0);
  assert.deepEqual(h.flashes, [['PRESET 3', 'EMPTY', '']]);
});

test('VOL keys step the existing volume control and show the level', () => {
  const h = createHarness({ volume: '40' });
  h.volUp.click();
  assert.equal(h.range.value, '45');
  assert.deepEqual(h.range.events, ['input', 'change'], 'the volume knob commits through its own handlers');
  assert.deepEqual(h.flashes.at(-1), ['VOLUME', '45', '']);
  h.volDown.click();
  h.volDown.click();
  assert.equal(h.range.value, '35');
});

test('VOL keys clamp to the 0-100 range', () => {
  const top = createHarness({ volume: '98' });
  top.volUp.click();
  assert.equal(top.range.value, '100');
  const bottom = createHarness({ volume: '2' });
  bottom.volDown.click();
  assert.equal(bottom.range.value, '0');
});
