const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

// Behavior spec for static/vfd-dimmer.js: the DIMMER key cycles the
// display glass bright -> dim -> dimmest -> bright through
// body[data-dimmer], keeps its accessible label truthful, and remembers
// the level per browser (storage failures must not break the key).

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
  const store = new Map(stored === null ? [] : [['chassis.vfdDimmer', stored]]);
  const localStorage = {
    getItem(k) { if (storageThrows) throw new Error('blocked'); return store.has(k) ? store.get(k) : null; },
    setItem(k, v) { if (storageThrows) throw new Error('blocked'); store.set(k, String(v)); },
  };
  const document = {
    readyState: 'complete',
    body,
    querySelector: (sel) => (sel === '[data-vfd-dimmer]' ? button : null),
    addEventListener() {},
  };
  const context = { document, window: { localStorage }, console: { warn() {} } };
  vm.createContext(context);
  const code = fs.readFileSync(path.join(__dirname, '..', 'static', 'vfd-dimmer.js'), 'utf8');
  vm.runInContext(code, context, { filename: 'vfd-dimmer.js' });
  return { button, body, store };
}

test('DIMMER cycles bright, dim, dimmest, bright', () => {
  const h = createHarness();
  assert.equal(h.body.getAttribute('data-dimmer'), '0');
  assert.equal(h.button.getAttribute('aria-label'), 'Display dimmer: bright');
  h.button.click();
  assert.equal(h.body.getAttribute('data-dimmer'), '1');
  assert.equal(h.button.getAttribute('aria-label'), 'Display dimmer: dim');
  h.button.click();
  assert.equal(h.body.getAttribute('data-dimmer'), '2');
  assert.equal(h.button.getAttribute('aria-label'), 'Display dimmer: dimmest');
  h.button.click();
  assert.equal(h.body.getAttribute('data-dimmer'), '0');
});

test('DIMMER level persists per browser', () => {
  const h = createHarness({ stored: '2' });
  assert.equal(h.body.getAttribute('data-dimmer'), '2');
  h.button.click();
  assert.equal(h.store.get('chassis.vfdDimmer'), '0');
});

test('DIMMER still works when storage is unavailable', () => {
  const h = createHarness({ storageThrows: true });
  h.button.click();
  assert.equal(h.body.getAttribute('data-dimmer'), '1');
});

test('DIMMER ignores a garbage stored level', () => {
  const h = createHarness({ stored: '9' });
  assert.equal(h.body.getAttribute('data-dimmer'), '0');
});
