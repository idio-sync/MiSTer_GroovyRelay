const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

// Behavior spec for static/meter-display.js: the DISPLAY key switches the
// meter window off and on through body[data-meter-display], mirrors the
// state in aria-pressed, and remembers it per browser (storage failures
// must not break the key).

function createHarness({ stored = null, storageThrows = false } = {}) {
  const attrs = new Map();
  const listeners = new Map();
  const button = {
    attributes: new Map(),
    setAttribute(k, v) { this.attributes.set(k, String(v)); },
    getAttribute(k) { return this.attributes.has(k) ? this.attributes.get(k) : null; },
    addEventListener(name, fn) { listeners.set(name, fn); },
    click() { listeners.get('click')(); },
  };
  const body = {
    setAttribute(k, v) { attrs.set(k, String(v)); },
    getAttribute(k) { return attrs.has(k) ? attrs.get(k) : null; },
  };
  const store = new Map(stored === null ? [] : [['chassis.meterDisplay', stored]]);
  const localStorage = {
    getItem(k) { if (storageThrows) throw new Error('blocked'); return store.has(k) ? store.get(k) : null; },
    setItem(k, v) { if (storageThrows) throw new Error('blocked'); store.set(k, String(v)); },
  };
  const document = {
    readyState: 'complete',
    body,
    querySelector: (sel) => (sel === '[data-meter-display-toggle]' ? button : null),
    addEventListener() {},
  };
  const context = { document, window: { localStorage }, console: { warn() {} } };
  vm.createContext(context);
  const code = fs.readFileSync(path.join(__dirname, '..', 'static', 'meter-display.js'), 'utf8');
  vm.runInContext(code, context, { filename: 'meter-display.js' });
  return { button, body, store };
}

test('meter window starts on and DISPLAY toggles it off and back on', () => {
  const h = createHarness();
  assert.equal(h.body.getAttribute('data-meter-display'), 'on');
  assert.equal(h.button.getAttribute('aria-pressed'), 'true');
  h.button.click();
  assert.equal(h.body.getAttribute('data-meter-display'), 'off');
  assert.equal(h.button.getAttribute('aria-pressed'), 'false');
  h.button.click();
  assert.equal(h.body.getAttribute('data-meter-display'), 'on');
  assert.equal(h.button.getAttribute('aria-pressed'), 'true');
});

test('DISPLAY off persists per browser', () => {
  const h = createHarness({ stored: 'off' });
  assert.equal(h.body.getAttribute('data-meter-display'), 'off');
  h.button.click();
  assert.equal(h.store.get('chassis.meterDisplay'), 'on');
});

test('DISPLAY still works when storage is unavailable', () => {
  const h = createHarness({ storageThrows: true });
  h.button.click();
  assert.equal(h.body.getAttribute('data-meter-display'), 'off');
});

test('a garbage stored value falls back to on', () => {
  assert.equal(createHarness({ stored: 'sideways' }).body.getAttribute('data-meter-display'), 'on');
});
