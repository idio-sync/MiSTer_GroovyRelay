const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

class FakeClassList {
  constructor() { this.classes = new Set(); }
  add(name) { this.classes.add(name); }
  remove(name) { this.classes.delete(name); }
  contains(name) { return this.classes.has(name); }
  toggle(name, on) { if (on === false) { this.remove(name); } else { this.add(name); } }
}

class FakeEl {
  constructor(tag) {
    this.tagName = (tag || 'DIV').toUpperCase();
    this.className = '';
    this.classList = new FakeClassList();
    this.children = [];
    this.attrs = new Map();
    this.textContent = '';
    this.parentNode = null;
  }

  append(...nodes) { nodes.forEach((n) => this.appendChild(n)); }
  appendChild(node) { node.parentNode = this; this.children.push(node); return node; }
  removeChild(node) { this.children = this.children.filter((c) => c !== node); node.parentNode = null; }
  setAttribute(name, value) { this.attrs.set(name, value); }
  getAttribute(name) { return this.attrs.has(name) ? this.attrs.get(name) : null; }
  querySelector() { return null; }
  querySelectorAll() { return []; }
}

function createHarness() {
  const vfd = new FakeEl('div');
  const body = new FakeEl('body');
  let keyHandler = null;
  const document = {
    body,
    createElement: (tag) => new FakeEl(tag),
    querySelector: (sel) => (sel === '.vfd' ? vfd : null),
    querySelectorAll: () => [],
    addEventListener: (type, fn) => { if (type === 'keydown') keyHandler = fn; },
  };
  const window = { Chassis: {}, matchMedia: () => ({ matches: false }) };
  const context = {
    window,
    document,
    console: { warn() {}, log() {} },
    setTimeout: () => 0, // record-only: synchronous wiring is what we assert
    clearTimeout() {},
  };
  vm.createContext(context);
  const code = fs.readFileSync(path.join(__dirname, '..', 'static', 'self-test.js'), 'utf8');
  vm.runInContext(code, context, { filename: 'self-test.js' });

  const SEQ = ['ArrowUp', 'ArrowUp', 'ArrowDown', 'ArrowDown', 'ArrowLeft', 'ArrowRight', 'ArrowLeft', 'ArrowRight', 'b', 'a'];
  function press(key, target) { keyHandler({ key, target: target || null }); }
  function enterKonami() { SEQ.forEach((k) => press(k)); }

  return { vfd, body, window, press, enterKonami };
}

test('the konami sequence opens the self-test overlay', () => {
  const h = createHarness();
  h.enterKonami();
  assert.equal(h.body.classList.contains('self-test-active'), true);
  assert.equal(h.vfd.children.length, 1);
  assert.equal(h.vfd.children[0].className, 'self-test-screen');
});

test('a wrong key resets progress so a partial sequence does not trigger', () => {
  const h = createHarness();
  ['ArrowUp', 'ArrowUp', 'ArrowDown', 'x'].forEach((k) => h.press(k));
  // Now the full sequence should still be required from scratch.
  ['ArrowDown', 'ArrowLeft', 'ArrowRight', 'ArrowLeft', 'ArrowRight', 'b', 'a'].forEach((k) => h.press(k));
  assert.equal(h.body.classList.contains('self-test-active'), false);
  assert.equal(h.vfd.children.length, 0);
});

test('keystrokes inside a text field never advance the sequence', () => {
  const h = createHarness();
  const input = new FakeEl('input');
  // Feed the whole sequence but as if typed into a settings field.
  ['ArrowUp', 'ArrowUp', 'ArrowDown', 'ArrowDown', 'ArrowLeft', 'ArrowRight', 'ArrowLeft', 'ArrowRight', 'b', 'a']
    .forEach((k) => h.press(k, input));
  assert.equal(h.body.classList.contains('self-test-active'), false);
});

test('selfTest.run is exposed on window.Chassis and is idempotent while running', () => {
  const h = createHarness();
  assert.equal(typeof h.window.Chassis.selfTest.run, 'function');
  h.window.Chassis.selfTest.run();
  h.window.Chassis.selfTest.run(); // second call is a no-op while running
  assert.equal(h.vfd.children.length, 1);
});
