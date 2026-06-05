const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

class FakeClassList {
  constructor(owner) {
    this.owner = owner;
  }

  add(name) {
    this.owner.classes.add(name);
  }

  remove(name) {
    this.owner.classes.delete(name);
  }

  contains(name) {
    return this.owner.classes.has(name);
  }
}

class FakeElement {
  constructor(className = '') {
    this.classes = new Set(className.split(/\s+/).filter(Boolean));
    this.classList = new FakeClassList(this);
    this.dataset = {};
    this.listeners = new Map();
    this.style = {};
    this.children = [];
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

  appendChild(child) {
    this.children.push(child);
    return child;
  }

  closest(selector) {
    return selector === '.preset' && this.classes.has('preset') ? this : null;
  }

  querySelector() {
    return null;
  }

  querySelectorAll(selector) {
    if (selector === '.preset') {
      return this.children.filter((child) => child.classes.has('preset'));
    }
    return [];
  }

  cloneNode() {
    return new FakeElement([...this.classes].join(' '));
  }

  getBoundingClientRect() {
    return { left: 0, top: 0, width: 120, height: 64 };
  }

  remove() {}

  removeAttribute() {}

  setPointerCapture() {}
}

function createHarness() {
  const bank = new FakeElement('preset-bank');
  const preset = new FakeElement('preset');
  preset.dataset.slot = '1';
  bank.appendChild(preset);
  const document = {
    body: {
      style: {},
      appendChild() {},
    },
    querySelector: (selector) => selector === '.preset-bank' ? bank : null,
    addEventListener() {},
    elementFromPoint: () => preset,
  };
  const context = {
    document,
    fetch: async () => ({ json: async () => ({ ok: true }) }),
    URLSearchParams,
  };
  vm.createContext(context);
  const code = fs.readFileSync(path.join(__dirname, '..', 'static', 'preset-reorder.js'), 'utf8');
  vm.runInContext(code, context, { filename: 'preset-reorder.js' });
  return { bank, preset };
}

test('preset pointer press class is visible for click feedback and clears on pointerup', () => {
  const h = createHarness();
  h.bank.dispatch('pointerdown', {
    target: h.preset,
    button: 0,
    pointerType: 'mouse',
    pointerId: 7,
    clientX: 0,
    clientY: 0,
    preventDefault() {},
  });

  assert.equal(h.preset.classes.has('pressed'), true);

  h.bank.dispatch('pointerup', {
    target: h.preset,
    pointerId: 7,
  });

  assert.equal(h.preset.classes.has('pressed'), false);
});
