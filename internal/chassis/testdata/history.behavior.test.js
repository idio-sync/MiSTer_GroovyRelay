// Behavior test for the recent-casts history surface in static/chassis.js.
// Like the other chassis behavior tests it is NOT wired into `go test` / CI —
// run it manually with: node --test internal/chassis/testdata/history.behavior.test.js
//
// chassis.js renders history from the SSE `history` event (the events module
// augments window.Chassis.events before DOMContentLoaded), so the harness wires
// a fake subscribe, fires DOMContentLoaded, then drives rendering by emitting
// payloads through that channel.
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

function camelToKebab(s) {
  return s.replace(/[A-Z]/g, (m) => '-' + m.toLowerCase());
}

class FakeNode {
  constructor(tag = '') {
    this.tagName = String(tag).toUpperCase();
    this.textContent = '';
    this.title = '';
    this.children = [];
    this.parentNode = null;
    this.attributes = new Map();
    this.classes = new Set();
    this.offsetWidth = 0;
    this.classList = {
      add: (n) => this.classes.add(n),
      remove: (n) => this.classes.delete(n),
      contains: (n) => this.classes.has(n),
      toggle: (n, on) => (on ? this.classes.add(n) : this.classes.delete(n)),
    };
    this.dataset = new Proxy(
      {},
      {
        set: (_t, prop, value) => {
          this.attributes.set('data-' + camelToKebab(String(prop)), String(value));
          return true;
        },
        get: (_t, prop) => this.attributes.get('data-' + camelToKebab(String(prop))),
      },
    );
  }

  get className() {
    return [...this.classes].join(' ');
  }

  set className(value) {
    this.classes = new Set(String(value).split(/\s+/).filter(Boolean));
  }

  append(...children) {
    for (const child of children) {
      child.parentNode = this;
      this.children.push(child);
    }
  }

  appendChild(child) {
    child.parentNode = this;
    this.children.push(child);
    return child;
  }

  replaceChildren(...children) {
    this.children = [];
    this.append(...children);
  }

  setAttribute(name, value) {
    this.attributes.set(name, String(value));
  }

  getAttribute(name) {
    return this.attributes.has(name) ? this.attributes.get(name) : null;
  }

  removeAttribute(name) {
    this.attributes.delete(name);
  }

  matches(selector) {
    if (selector.startsWith('[') && selector.endsWith(']')) {
      return this.attributes.has(selector.slice(1, -1));
    }
    if (selector.startsWith('.')) {
      return this.classes.has(selector.slice(1));
    }
    return false;
  }

  closest(selector) {
    let node = this;
    while (node) {
      if (node.matches && node.matches(selector)) {
        return node;
      }
      node = node.parentNode;
    }
    return null;
  }
}

function createHarness(opts = {}) {
  const fetchResult = opts.fetchResult || (() => ({ ok: true, json: async () => ({}) }));

  const section = new FakeNode('div');
  section.classes.add('history-section');
  const frame = new FakeNode('div');
  frame.classes.add('history-frame');
  section.append(frame);

  const body = new FakeNode('body');
  const docListeners = new Map();
  const requests = [];
  const errors = [];

  const timers = new Map();
  let now = 0;
  let nextTimer = 1;
  function setTimeoutFake(fn, delay) {
    const id = nextTimer++;
    timers.set(id, { at: now + delay, fn });
    return id;
  }
  function advance(ms) {
    now += ms;
    for (const [id, timer] of [...timers].sort((a, b) => a[1].at - b[1].at)) {
      if (timer.at <= now) {
        timers.delete(id);
        timer.fn();
      }
    }
  }

  const document = {
    body,
    querySelector: (sel) => (sel === '.history-section' ? section : null),
    querySelectorAll: () => [],
    createElement: (tag) => new FakeNode(tag),
    addEventListener: (name, fn) => {
      const list = docListeners.get(name) || [];
      list.push(fn);
      docListeners.set(name, list);
    },
  };

  function fire(name, event) {
    for (const fn of docListeners.get(name) || []) {
      fn(event);
    }
  }

  function fetchFake(url, options) {
    requests.push({ url, options, body: String(options.body) });
    return Promise.resolve(fetchResult(url, options));
  }

  const context = {
    document,
    window: {},
    location: { search: '' },
    fetch: fetchFake,
    URL,
    URLSearchParams,
    Element: FakeNode,
    setTimeout: setTimeoutFake,
    console: { warn() {}, error() {} },
  };
  vm.createContext(context);
  const code = fs.readFileSync(path.join(__dirname, '..', 'static', 'chassis.js'), 'utf8');
  vm.runInContext(code, context, { filename: 'chassis.js' });

  // Mirror the events module + input module that augment window.Chassis after
  // chassis.js evaluates but before DOMContentLoaded.
  const historyHandlers = [];
  context.window.Chassis.events = {
    subscribe: (name, cb) => {
      if (name === 'history') {
        historyHandlers.push(cb);
      }
    },
  };
  context.window.Chassis.input = { showError: (t) => errors.push(t) };

  fire('DOMContentLoaded', { type: 'DOMContentLoaded' });

  return {
    window: context.window,
    frame,
    requests,
    errors,
    advance,
    emitHistory(payload) {
      for (const cb of historyHandlers) {
        cb({ data: JSON.stringify(payload) });
      }
    },
    clickRow(row, target) {
      fire('click', { type: 'click', target: target || row });
    },
    keydownRow(row, key) {
      fire('keydown', {
        type: 'keydown',
        target: row,
        key,
        preventDefault() {},
      });
    },
    list() {
      return frame.children[0];
    },
    rows() {
      const list = frame.children[0];
      return list && list.tagName === 'UL' ? list.children : [];
    },
  };
}

async function settle() {
  for (let i = 0; i < 6; i += 1) {
    await Promise.resolve();
  }
}

const REPLAYABLE = {
  title: 'Big Buck Bunny',
  source: 'URL',
  sourceId: 'url',
  when: '2H AGO',
  whenIso: '2026-05-30T10:00:00Z',
  whenExact: '30 May 2026 · 10:00',
  artwork: 'URL',
  replayId: 'h_11111111111111111111111111111111',
};
const READONLY = {
  title: 'Read Only',
  source: 'DLNA',
  sourceId: 'dlna',
  when: '1H AGO',
  artwork: 'DLNA',
};

test('renders a list with an actionable replay row and a plain read-only row', () => {
  const h = createHarness();
  h.emitHistory({ rows: [REPLAYABLE, READONLY] });

  const list = h.list();
  assert.equal(list.tagName, 'UL');
  assert.equal(list.getAttribute('role'), 'list');

  const [row0, row1] = h.rows();
  // Replayable row IS the recast control.
  assert.equal(row0.tagName, 'LI');
  assert.equal(row0.getAttribute('role'), 'button');
  assert.equal(row0.getAttribute('tabindex'), '0');
  assert.equal(row0.getAttribute('data-history-replay-id'), REPLAYABLE.replayId);
  assert.equal(row0.getAttribute('aria-label'), 'Recast Big Buck Bunny from history');

  const [artwork, title, source, when, cue] = row0.children;
  assert.equal(artwork.getAttribute('data-source'), 'url');
  assert.equal(title.textContent, 'Big Buck Bunny');
  assert.equal(source.textContent, 'URL');
  assert.equal(when.tagName, 'TIME');
  assert.equal(when.getAttribute('datetime'), '2026-05-30T10:00:00Z');
  assert.equal(when.title, '30 May 2026 · 10:00');
  assert.equal(cue.classes.has('history-replay-cue'), true);

  // Read-only row advertises no interactivity and shows a placeholder cue.
  assert.equal(row1.getAttribute('role'), null);
  assert.equal(row1.getAttribute('data-history-replay-id'), null);
  assert.equal(row1.children[4].classes.has('history-replay-placeholder'), true);
});

test('empty payload renders the teaching empty state', () => {
  const h = createHarness();
  h.emitHistory({ rows: [], emptyMessage: 'No recent casts — paste a URL or pick a preset' });

  const child = h.frame.children[0];
  assert.equal(child.classes.has('history-empty'), true);
  assert.equal(child.textContent, 'No recent casts — paste a URL or pick a preset');
});

test('clicking a replayable row recasts it and flashes confirmation', async () => {
  const h = createHarness();
  h.emitHistory({ rows: [REPLAYABLE] });
  const row = h.rows()[0];

  // Click lands on the inner cue; the handler walks up to the actionable row.
  h.clickRow(row, row.children[4]);
  await settle();

  assert.equal(h.requests.length, 1);
  assert.equal(h.requests[0].url, '/ui/history/play');
  assert.equal(h.requests[0].body, 'id=' + REPLAYABLE.replayId);
  assert.equal(h.errors.length, 0);
  assert.equal(row.classes.has('recasting'), true, 'row should flash on success');

  h.advance(950);
  assert.equal(row.classes.has('recasting'), false, 'flash should clear after the animation window');
});

test('keyboard Enter recasts the focused row', async () => {
  const h = createHarness();
  h.emitHistory({ rows: [REPLAYABLE] });
  const row = h.rows()[0];

  h.keydownRow(row, 'Enter');
  await settle();

  assert.equal(h.requests.length, 1);
  assert.equal(h.requests[0].body, 'id=' + REPLAYABLE.replayId);
});

test('a failed recast surfaces the server chip and does not flash', async () => {
  const h = createHarness({
    fetchResult: () => ({ ok: false, json: async () => ({ chip: 'NOT FOUND' }) }),
  });
  h.emitHistory({ rows: [REPLAYABLE] });
  const row = h.rows()[0];

  h.clickRow(row);
  await settle();

  assert.equal(h.requests.length, 1);
  assert.deepEqual(h.errors, ['NOT FOUND']);
  assert.equal(row.classes.has('recasting'), false);
  assert.equal(row.getAttribute('aria-busy'), null, 'aria-busy should be cleared after the request');
});
