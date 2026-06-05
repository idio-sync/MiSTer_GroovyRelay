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

  toggle(name, on) {
    const next = on === undefined ? !this.owner.classes.has(name) : on;
    if (next) {
      this.owner.classes.add(name);
    } else {
      this.owner.classes.delete(name);
    }
    return next;
  }
}

class FakeElement {
  constructor(className = '') {
    this.classes = new Set(String(className).split(/\s+/).filter(Boolean));
    this.classList = new FakeClassList(this);
    this.dataset = {};
    this.listeners = new Map();
    this.style = {};
    this.children = [];
    this.attributes = new Map();
    this.parentNode = null;
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
    child.parentNode = this;
    this.children.push(child);
    return child;
  }

  removeChild(child) {
    this.children = this.children.filter((candidate) => candidate !== child);
    child.parentNode = null;
    return child;
  }

  cloneNode() {
    return new FakeElement([...this.classes].join(' '));
  }

  closest(selector) {
    return selector === '.item' && this.classes.has('item') ? this : null;
  }

  getBoundingClientRect() {
    return { left: 0, top: 0, width: 100, height: 40 };
  }

  setAttribute(name, value) {
    this.attributes.set(name, String(value));
  }

  removeAttribute(name) {
    this.attributes.delete(name);
  }

  remove() {
    if (this.parentNode) {
      this.parentNode.removeChild(this);
    }
  }

  setPointerCapture() {}

  releasePointerCapture() {}
}

function createHarness() {
  const container = new FakeElement('list');
  const a = new FakeElement('item');
  a.dataset.id = 'a';
  const b = new FakeElement('item');
  b.dataset.id = 'b';
  container.appendChild(a);
  container.appendChild(b);

  let pointAt = b;
  const body = new FakeElement('body');
  const document = {
    body,
    addEventListener() {},
    elementFromPoint: () => pointAt,
  };
  const drops = [];
  const context = {
    document,
    window: { Chassis: {} },
    setPointAt(el) {
      pointAt = el;
    },
    onDrop(from, to) {
      drops.push([from.dataset.id, to.dataset.id]);
    },
    getDrops() {
      return drops;
    },
  };

  vm.createContext(context);
  const code = fs.readFileSync(path.join(__dirname, '..', 'static', 'reorder.js'), 'utf8');
  vm.runInContext(code, context, { filename: 'reorder.js' });
  context.window.Chassis.reorder.makeSortable({ container, itemSelector: '.item', onDrop: context.onDrop });

  return { container, a, b, context };
}

test('makeSortable fires onDrop(from,to) after a threshold-exceeding drag', () => {
  const h = createHarness();
  h.context.setPointAt(h.b);
  h.container.dispatch('pointerdown', {
    target: h.a,
    button: 0,
    pointerType: 'mouse',
    pointerId: 1,
    clientX: 0,
    clientY: 0,
    preventDefault() {},
  });
  h.container.dispatch('pointermove', { pointerId: 1, clientX: 50, clientY: 0 });
  h.container.dispatch('pointerup', { pointerId: 1, clientX: 50, clientY: 0 });

  assert.deepEqual(h.context.getDrops(), [['a', 'b']]);
});

test('sub-threshold press fires no drop', () => {
  const h = createHarness();
  h.container.dispatch('pointerdown', {
    target: h.a,
    button: 0,
    pointerType: 'mouse',
    pointerId: 2,
    clientX: 0,
    clientY: 0,
    preventDefault() {},
  });
  h.container.dispatch('pointermove', { pointerId: 2, clientX: 2, clientY: 0 });
  h.container.dispatch('pointerup', { pointerId: 2, clientX: 2, clientY: 0 });

  assert.equal(h.context.getDrops().length, 0);
});
