const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

class FakeClassList {
  constructor() {
    this.classes = new Set();
  }

  add(...names) { names.forEach((n) => this.classes.add(n)); }
  remove(...names) { names.forEach((n) => this.classes.delete(n)); }
  contains(name) { return this.classes.has(name); }
}

function createHarness(initialState) {
  const registered = [];
  const logs = [];
  const body = { classList: new FakeClassList(), offsetWidth: 0 };
  const document = {
    body,
    querySelector(sel) {
      if (sel === 'meta[name="chassis-generation"]') {
        return { getAttribute: () => '7' };
      }
      return null;
    },
  };
  const console = { log: (...a) => logs.push(a), warn() {} };
  const window = {
    Chassis: {
      State: { IDLE: 'idle', LIVE: 'live' },
      animators: {
        register(animator) { registered.push(animator); animator.handleState(initialState); },
      },
    },
    matchMedia: () => ({ matches: false }),
    console, // browsers expose console on window; power-on.js guards on it
  };
  const context = {
    window,
    document,
    console,
    setTimeout: () => 0,
    clearTimeout() {},
  };
  vm.createContext(context);
  const code = fs.readFileSync(path.join(__dirname, '..', 'static', 'power-on.js'), 'utf8');
  vm.runInContext(code, context, { filename: 'power-on.js' });
  return { body, registered, logs };
}

test('registration does not ignite the ritual (initial render is not a wake)', () => {
  const h = createHarness('idle');
  assert.equal(h.registered.length, 1);
  assert.equal(h.body.classList.contains('warming'), false);
});

test('idle->live transition adds the warming class', () => {
  const h = createHarness('idle');
  h.registered[0].handleState('live');
  assert.equal(h.body.classList.contains('warming'), true);
});

test('transition to idle never warms', () => {
  const h = createHarness('idle');
  h.registered[0].handleState('idle');
  assert.equal(h.body.classList.contains('warming'), false);
});

test('live->idle transition adds the cooling class and clears warming (power-down)', () => {
  const h = createHarness('idle');
  h.registered[0].handleState('live');
  assert.equal(h.body.classList.contains('warming'), true);
  h.registered[0].handleState('idle');
  assert.equal(h.body.classList.contains('cooling'), true);
  assert.equal(h.body.classList.contains('warming'), false);
});

test('a page that loads already-live does not replay the ritual on first state echo', () => {
  const h = createHarness('live');
  // The registration call handled 'live' as the initial (primed) state.
  assert.equal(h.body.classList.contains('warming'), false);
  // A subsequent genuine live event still ignites.
  h.registered[0].handleState('live');
  assert.equal(h.body.classList.contains('warming'), true);
});

test('boot banner is printed to the dev console', () => {
  const h = createHarness('idle');
  assert.ok(h.logs.length >= 1, 'expected a console banner');
  assert.ok(h.logs.some((args) => String(args[0]).includes('GROOVY RELAY')));
});
