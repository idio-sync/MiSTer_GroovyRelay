const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

// Behavior spec for static/vfd-live.js. The load-bearing case is the
// runtime idle->live transition: the page is opened while idle (server
// renders <div class="vfd-state vfd-state--idle">), then a cast starts
// and the SSE `state` event flips body to live. chassis.css hides the
// mismatched modifier:
//
//   body.receiver.idle       .vfd-state--live { display: none; }
//   body.receiver:not(.idle) .vfd-state--idle { display: none; }
//
// So vfd-live.js must keep the .vfd-state element's --idle/--live
// modifier in sync with the live state, or the whole VFD content block
// gets display:none'd the instant a cast starts (blank "top screen").

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
    for (const fn of [...(this.listeners.get(name) || [])]) {
      fn({ type: name, target: this, ...event });
    }
  }
}

class FakeElement {
  constructor() {
    this.textContent = '';
    this.classes = new Set();
    this.classList = {
      toggle: (name, on) => {
        const want = on === undefined ? !this.classes.has(name) : on;
        if (want) {
          this.classes.add(name);
        } else {
          this.classes.delete(name);
        }
        return want;
      },
      add: (...names) => names.forEach((n) => this.classes.add(n)),
      remove: (...names) => names.forEach((n) => this.classes.delete(n)),
      contains: (name) => this.classes.has(name),
    };
  }
}

class FakeEventSource extends FakeTarget {
  constructor(url) {
    super();
    this.url = url;
    FakeEventSource.last = this;
  }

  close() {
    this.closed = true;
  }
}

function createHarness(initialState = 'idle') {
  const vfdState = new FakeElement();
  vfdState.classes.add(initialState === 'live' ? 'vfd-state--live' : 'vfd-state--idle');
  const title = new FakeElement();
  const marquee = new FakeElement();
  const queue = new FakeElement();
  const uptime = new FakeElement();

  const stateCalls = [];
  const document = new FakeTarget();
  document.querySelector = (selector) => {
    switch (selector) {
      case '.vfd-state':
        return vfdState;
      case '[data-vfd-title]':
        return title;
      case '[data-vfd-marquee]':
        return marquee;
      case '[data-vfd-queue]':
        return queue;
      case '[data-vfd-uptime]':
        return uptime;
      default:
        return null;
    }
  };
  document.dispatchEvent = () => {};

  const bodyClasses = new Set(['receiver', initialState]);
  const window = {
    Chassis: {
      State: {
        set(next) {
          stateCalls.push(next);
          bodyClasses.delete('idle');
          bodyClasses.delete('live');
          bodyClasses.add(next);
        },
      },
      events: {},
    },
  };

  const context = {
    console: { warn() {}, info() {} },
    document,
    window,
    EventSource: FakeEventSource,
    CustomEvent: class {
      constructor(name, init) {
        this.type = name;
        Object.assign(this, init);
      }
    },
  };
  vm.createContext(context);
  const code = fs.readFileSync(path.join(__dirname, '..', 'static', 'vfd-live.js'), 'utf8');
  vm.runInContext(code, context, { filename: 'vfd-live.js' });

  // Fire DOMContentLoaded so connect() builds the EventSource + listeners.
  document.dispatch('DOMContentLoaded');
  const source = FakeEventSource.last;
  return { source, vfdState, title, marquee, stateCalls, bodyClasses };
}

test('live state event syncs the .vfd-state modifier so the VFD is not hidden', () => {
  const h = createHarness('idle');

  h.source.dispatch('state', { data: JSON.stringify({ state: 'live' }) });

  assert.deepEqual(h.stateCalls, ['live'], 'body-class state still flips via Chassis.State.set');
  assert.equal(
    h.vfdState.classList.contains('vfd-state--live'),
    true,
    '.vfd-state must gain --live so it is not hidden once body is live',
  );
  assert.equal(
    h.vfdState.classList.contains('vfd-state--idle'),
    false,
    '.vfd-state must drop --idle so body:not(.idle) .vfd-state--idle does not display:none it',
  );
});

test('idle state event restores the .vfd-state idle modifier', () => {
  const h = createHarness('live');

  h.source.dispatch('state', { data: JSON.stringify({ state: 'idle' }) });

  assert.equal(h.vfdState.classList.contains('vfd-state--idle'), true);
  assert.equal(h.vfdState.classList.contains('vfd-state--live'), false);
});

test('vfd event still updates the title text content', () => {
  const h = createHarness('idle');

  h.source.dispatch('vfd', {
    data: JSON.stringify({
      title: 'Blade Runner',
      marquee: 'PLEX · 00:12 / 01:57',
      queueCurrent: 0,
      queueTotal: 0,
      uptime: '1H 2M',
    }),
  });

  assert.equal(h.title.textContent, 'Blade Runner');
  assert.equal(h.marquee.textContent, 'PLEX · 00:12 / 01:57');
});
