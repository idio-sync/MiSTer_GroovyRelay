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
    this.children = [];
    this.attributes = new Map();
    this.style = {
      _props: new Map(),
      setProperty(k, v) { this._props.set(k, v); },
      removeProperty(k) { this._props.delete(k); },
    };
    // jsdom returns 0 for layout measurements; scrollWidth === clientWidth
    // so the marquee branch does not fire in the test env — that's fine.
    this.scrollWidth = 0;
    this.parent = null;
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
    this.closest = (selector) => {
      if (selector === '.vfd-row') return this.parent;
      return null;
    };
  }

  appendChild(child) {
    child.parent = this;
    this.children.push(child);
    return child;
  }

  replaceChildren(...children) {
    this.children = [];
    children.forEach((child) => this.appendChild(child));
  }

  setAttribute(name, value) {
    this.attributes.set(name, String(value));
  }

  getAttribute(name) {
    return this.attributes.get(name) || '';
  }

  removeAttribute(name) {
    this.attributes.delete(name);
  }
}

// FakeRow simulates the .vfd-row wrapper that vfd-live.js calls
// closest('.vfd-row') on each tier span to find.
class FakeRow {
  constructor() {
    this.classes = new Set(['vfd-row']);
    this.clientWidth = 0;
    this.style = {
      _props: new Map(),
      setProperty(k, v) { this._props.set(k, v); },
      removeProperty(k) { this._props.delete(k); },
    };
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

function createHarness(initialState = 'idle', options = {}) {
  const vfdRoot = new FakeElement();
  vfdRoot.classes.add('vfd');
  vfdRoot.classes.add('vfd-density');
  vfdRoot.classes.add('vfd-density--sparse-one');

  const vfdState = new FakeElement();
  vfdState.classes.add(initialState === 'live' ? 'vfd-state--live' : 'vfd-state--idle');

  // Three tier spans, each wrapped in a FakeRow so closest('.vfd-row') works.
  const primaryRow = new FakeRow();
  const secondaryRow = new FakeRow();
  const tertiaryRow = new FakeRow();

  const primary = new FakeElement();
  const secondary = new FakeElement();
  const tertiary = new FakeElement();

  primary.parent = primaryRow;
  secondary.parent = secondaryRow;
  tertiary.parent = tertiaryRow;

  const queueSlots = new FakeElement();
  const queueTotalLabel = new FakeElement();
  const queueRail = new FakeElement();
  queueRail.setAttribute('data-vfd-queue-current', options.queueCurrent ?? 0);
  queueRail.setAttribute('data-vfd-queue-total', options.queueTotal ?? 0);
  const uptime = new FakeElement();

  const resizeListeners = [];
  const window = {
    addEventListener(name, fn) {
      if (name === 'resize') resizeListeners.push(fn);
    },
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

  const stateCalls = [];
  const bodyClasses = new Set(['receiver', initialState]);
  const document = new FakeTarget();
  document.createElement = () => new FakeElement();
  document.querySelector = (selector) => {
    switch (selector) {
      case '.vfd':
        return vfdRoot;
      case '.vfd-state':
        return vfdState;
      case '[data-vfd-primary]':
        return primary;
      case '[data-vfd-secondary]':
        return secondary;
      case '[data-vfd-tertiary]':
        return tertiary;
      case '[data-vfd-queue-slots]':
        return queueSlots;
      case '[data-vfd-queue-total-label]':
        return queueTotalLabel;
      case '[data-vfd-queue-current]':
        return queueRail;
      case '[data-vfd-uptime]':
        return uptime;
      default:
        return null;
    }
  };
  document.dispatchEvent = () => {};
  // document.fonts is absent in the vm context — the guard in handleVfdEvent
  // checks `document.fonts && document.fonts.ready` so this is safe.

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
  return {
    source,
    vfdRoot,
    vfdState,
    primary,
    secondary,
    tertiary,
    primaryRow,
    secondaryRow,
    tertiaryRow,
    queueRail,
    queueSlots,
    queueTotalLabel,
    uptime,
    stateCalls,
    bodyClasses,
    resizeListeners,
  };
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

test('vfd event updates primary, secondary, and tertiary tier spans', () => {
  const h = createHarness('idle');

  h.source.dispatch('vfd', {
    data: JSON.stringify({
      primary: 'Blade Runner',
      secondary: 'PLEX · 00:12 / 01:57',
      tertiary: '1982 · Ridley Scott',
      queueCurrent: 1,
      queueTotal: 3,
      uptime: '1H 2M',
    }),
  });

  assert.equal(h.primary.textContent, 'Blade Runner');
  assert.equal(h.secondary.textContent, 'PLEX · 00:12 / 01:57');
  assert.equal(h.tertiary.textContent, '1982 · Ridley Scott');
});

test('DOMContentLoaded renders the initial server queue memory state', () => {
  const h = createHarness('idle', { queueCurrent: 4, queueTotal: 8 });

  assert.deepEqual(h.queueSlots.children.map((child) => child.textContent), ['01', '02', '03', '04', '05', '06', '07', '08']);
  assert.equal(h.queueSlots.children[3].classList.contains('current'), true);
  assert.equal(h.queueTotalLabel.textContent, 'TOTAL 08');
});

test('vfd event updates queue memory rail and uptime span', () => {
  const h = createHarness('idle');

  h.source.dispatch('vfd', {
    data: JSON.stringify({
      primary: 'Test',
      secondary: '',
      tertiary: '',
      queueCurrent: 2,
      queueTotal: 5,
      uptime: '3H 15M',
    }),
  });

  assert.deepEqual(h.queueSlots.children.map((child) => child.textContent), ['01', '02', '03', '04', '05']);
  assert.equal(h.queueSlots.children[1].classList.contains('current'), true);
  assert.equal(h.queueTotalLabel.textContent, 'TOTAL 05');
  assert.equal(h.uptime.textContent, '3H 15M');
});

test('vfd event assigns density classes from populated tiers', () => {
  const cases = [
    { name: 'dense', primary: 'P', secondary: 'S', tertiary: 'T', want: 'vfd-density--dense' },
    { name: 'sparse two', primary: 'P', secondary: 'S', tertiary: '', want: 'vfd-density--sparse-two' },
    { name: 'sparse one', primary: 'P', secondary: '', tertiary: '', want: 'vfd-density--sparse-one' },
    { name: 'empty', primary: '', secondary: '', tertiary: '', want: 'vfd-density--empty' },
  ];

  cases.forEach((tc) => {
    const h = createHarness('idle');

    h.source.dispatch('vfd', {
      data: JSON.stringify({
        primary: tc.primary,
        secondary: tc.secondary,
        tertiary: tc.tertiary,
        queueCurrent: 0,
        queueTotal: 0,
        uptime: '',
      }),
    });

    assert.equal(h.vfdRoot.classList.contains(tc.want), true, `${tc.name} should set ${tc.want}`);
    ['vfd-density--dense', 'vfd-density--sparse-two', 'vfd-density--sparse-one', 'vfd-density--empty']
      .filter((name) => name !== tc.want)
      .forEach((name) => {
        assert.equal(h.vfdRoot.classList.contains(name), false, `${tc.name} should clear ${name}`);
      });
  });
});

test('vfd event renders dormant queue cells for empty queue totals', () => {
  const h = createHarness('idle');

  h.source.dispatch('vfd', {
    data: JSON.stringify({
      primary: 'Solo',
      secondary: '',
      tertiary: '',
      queueCurrent: 0,
      queueTotal: 0,
      uptime: '',
    }),
  });

  assert.equal(h.queueSlots.children.length, 8);
  h.queueSlots.children.forEach((child) => {
    assert.equal(child.classList.contains('dormant'), true);
    assert.equal(child.textContent, '');
  });
  assert.equal(h.queueTotalLabel.textContent, '');
});

test('vfd event renders long queue as a current-centered window plus total', () => {
  const h = createHarness('idle');

  h.source.dispatch('vfd', {
    data: JSON.stringify({
      primary: 'Movie',
      secondary: '1982',
      tertiary: '',
      queueCurrent: 9,
      queueTotal: 32,
      uptime: '',
    }),
  });

  assert.deepEqual(h.queueSlots.children.map((child) => child.textContent), ['07', '08', '09', '10', '11']);
  assert.equal(h.queueSlots.children[0].classList.contains('past'), true);
  assert.equal(h.queueSlots.children[1].classList.contains('past'), true);
  assert.equal(h.queueSlots.children[2].classList.contains('current'), true);
  assert.equal(h.queueSlots.children[3].classList.contains('future'), true);
  assert.equal(h.queueSlots.children[4].classList.contains('future'), true);
  assert.equal(h.queueTotalLabel.textContent, 'TOTAL 32');
});

test('vfd event marks rows with is-empty when tier text is empty', () => {
  const h = createHarness('idle');

  h.source.dispatch('vfd', {
    data: JSON.stringify({
      primary: 'Title',
      secondary: '',
      tertiary: '',
      queueCurrent: 0,
      queueTotal: 0,
      uptime: '',
    }),
  });

  assert.equal(h.primaryRow.classList.contains('is-empty'), false, 'primary row with text should not be empty');
  assert.equal(h.secondaryRow.classList.contains('is-empty'), true, 'secondary row without text should be empty');
  assert.equal(h.tertiaryRow.classList.contains('is-empty'), true, 'tertiary row without text should be empty');
});

test('vfd event clears is-empty when tier receives text', () => {
  const h = createHarness('idle');

  // First dispatch sets tertiary empty
  h.source.dispatch('vfd', {
    data: JSON.stringify({
      primary: 'First',
      secondary: '',
      tertiary: '',
      queueCurrent: 0,
      queueTotal: 0,
      uptime: '',
    }),
  });
  assert.equal(h.tertiaryRow.classList.contains('is-empty'), true);

  // Second dispatch fills tertiary
  h.source.dispatch('vfd', {
    data: JSON.stringify({
      primary: 'First',
      secondary: 'Sub',
      tertiary: 'Details',
      queueCurrent: 0,
      queueTotal: 0,
      uptime: '',
    }),
  });
  assert.equal(h.tertiaryRow.classList.contains('is-empty'), false, 'is-empty must be cleared when text is provided');
});

test('connect wires a resize listener for re-measuring tier overflow', () => {
  const h = createHarness('idle');
  assert.equal(h.resizeListeners.length, 1, 'resize listener must be registered exactly once at load, not per connect()/reconnect()');
});
