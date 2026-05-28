const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

class FakeElement {
  constructor() {
    this.dataset = {};
    this.disabled = false;
    this.attrs = new Map();
    this.listeners = new Map();
    this.children = new Map();
    this.styleValues = new Map();
    this.style = {
      setProperty: (name, value) => this.styleValues.set(name, value),
    };
  }

  addEventListener(name, fn) {
    const list = this.listeners.get(name) || [];
    list.push(fn);
    this.listeners.set(name, list);
  }

  dispatch(name, event = {}) {
    for (const fn of this.listeners.get(name) || []) {
      fn({ type: name, target: this, ...event });
    }
  }

  setAttribute(name, value) {
    this.attrs.set(name, String(value));
  }

  getAttribute(name) {
    return this.attrs.get(name) || '';
  }

  removeAttribute(name) {
    this.attrs.delete(name);
  }

  hasAttribute(name) {
    return this.attrs.has(name);
  }

  querySelector(selector) {
    return this.children.get(selector) || null;
  }
}

function createHarness() {
  const handlers = new Map();
  const strip = new FakeElement();
  const bar = new FakeElement();
  const fill = new FakeElement();
  bar.children.set('[data-transport-seek-fill]', fill);

  const document = {
    readyState: 'complete',
    querySelector: (selector) => {
      if (selector === '.transport-strip') return strip;
      if (selector === '[data-transport-seek]') return bar;
      if (selector.startsWith('meta[')) return { getAttribute: () => '' };
      return null;
    },
    querySelectorAll: () => [],
  };
  const window = {
    Chassis: {
      events: {
        subscribe(name, fn) {
          handlers.set(name, fn);
        },
      },
    },
  };
  const context = {
    console: { warn() {} },
    document,
    fetch: () => Promise.resolve({ status: 204 }),
    window,
    URLSearchParams,
  };
  vm.createContext(context);
  const code = fs.readFileSync(path.join(__dirname, '..', 'static', 'transport.js'), 'utf8');
  vm.runInContext(code, context, { filename: 'transport.js' });

  return { bar, fill, handlers };
}

test('transport updates both seek fill and head position variable from SSE progress', () => {
  const h = createHarness();
  const fn = h.handlers.get('transport');
  assert.equal(typeof fn, 'function', 'missing transport subscription');

  fn({
    data: JSON.stringify({
      state: 'playing',
      seekFillPercent: 42,
      offsetMs: 4200,
      durationMs: 10000,
      actionsEnabled: { seek: true },
    }),
  });

  assert.equal(h.fill.style.width, '42%');
  assert.equal(h.bar.styleValues.get('--seek-percent'), '42%');
  assert.equal(h.bar.getAttribute('aria-valuenow'), '42');
});
