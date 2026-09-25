const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

class FakeClassList {
  constructor(owner) {
    this.owner = owner;
  }

  toggle(name, on) {
    if (on) {
      this.owner.classes.add(name);
    } else {
      this.owner.classes.delete(name);
    }
  }

  contains(name) {
    return this.owner.classes.has(name);
  }
}

class FakeLamp {
  constructor(sourceId, attrs = {}) {
    this.dataset = { sourceId };
    this.classes = new Set(['lamp']);
    this.listeners = [];
    this.classList = new FakeClassList(this);
    this.attrs = new Map([['data-source-id', sourceId], ...Object.entries(attrs)]);
    this.children = new Map([
      ['.state', { textContent: '' }],
      ['.led-well', { dataset: {} }],
    ]);
  }

  getAttribute(name) {
    return this.attrs.get(name) || '';
  }

  setAttribute(name, value) {
    this.attrs.set(name, value);
  }

  querySelector(selector) {
    return this.children.get(selector) || null;
  }

  addEventListener(name, fn) {
    if (name === 'click') this.listeners.push(fn);
  }

  click() {
    this.listeners.forEach((fn) => fn({ currentTarget: this }));
  }
}

function createHarness() {
  const lamps = [
    new FakeLamp('streams'),
    new FakeLamp('plex'),
    new FakeLamp('jellyfin'),
    new FakeLamp('aux', { 'data-source-action': 'aux-start' }),
  ];
  const handlers = new Map();
  const flashes = [];
  const timers = [];
  const bodyClasses = new Set();
  const tab = { clicks: 0, click() { this.clicks += 1; } };
  const plexField = {
    scrolled: 0,
    closest() { return this; },
    scrollIntoView() { this.scrolled += 1; },
  };
  const document = {
    body: {
      classList: {
        add: (c) => bodyClasses.add(c),
        contains: (c) => bodyClasses.has(c),
      },
    },
    querySelectorAll: (selector) => selector === '.source-cluster .lamp' ? lamps : [],
    querySelector: (selector) => {
      if (selector === '.settings-tab[data-tab="adapters"]') return tab;
      if (selector === '.settings-pane[data-pane="adapters"] [data-adapter="plex"]') return plexField;
      return null;
    },
  };
  const window = {
    Chassis: {
      events: {
        subscribe(name, fn) {
          handlers.set(name, fn);
        },
      },
      vfd: {
        flash(primary, secondary, tertiary) {
          flashes.push([primary, secondary, tertiary]);
        },
      },
    },
  };
  const context = {
    console: { warn() {} },
    document,
    window,
    setTimeout: (fn) => { timers.push(fn); return timers.length; },
    clearTimeout() {},
  };
  vm.createContext(context);
  const code = fs.readFileSync(path.join(__dirname, '..', 'static', 'source-cluster.js'), 'utf8');
  vm.runInContext(code, context, { filename: 'source-cluster.js' });

  function emit(name, payload) {
    const fn = handlers.get(name);
    assert.equal(typeof fn, 'function', `missing ${name} subscription`);
    fn({ data: JSON.stringify(payload) });
  }

  const expire = () => { while (timers.length) timers.shift()(); };

  return { lamps, emit, flashes, bodyClasses, tab, plexField, expire };
}

const lamp = (h, id) => h.lamps.find((l) => l.getAttribute('data-source-id') === id);

test('source event updates configured and casting lamp state', () => {
  const h = createHarness();
  h.emit('source', {
    buttons: [
      { label: 'STREAMS', configured: true, casting: false },
      { label: 'PLEX', configured: false, casting: false },
      { label: 'JELLYFIN', configured: true, casting: true },
    ],
  });

  assert.equal(h.lamps[0].classes.has('configured-idle'), true);
  assert.equal(h.lamps[0].classes.has('casting'), false);
  assert.equal(h.lamps[0].classes.has('unavailable'), false);
  assert.equal(h.lamps[1].classes.has('unavailable'), true);
  assert.equal(h.lamps[2].classes.has('configured-idle'), true);
  assert.equal(h.lamps[2].classes.has('casting'), true);
  assert.match(h.lamps[2].getAttribute('aria-label'), /currently casting/);
});

test('transport event still migrates casting state', () => {
  const h = createHarness();
  h.emit('source', {
    buttons: [
      { label: 'STREAMS', configured: true, casting: false },
      { label: 'PLEX', configured: true, casting: false },
      { label: 'JELLYFIN', configured: false, casting: false },
    ],
  });
  h.emit('transport', { adapterRef: 'streams:mtv-rewind:80s:sess:1' });

  assert.equal(h.lamps[0].classes.has('casting'), true);
  assert.equal(h.lamps[1].classes.has('casting'), false);
});

test('transport event uses canonical source for opaque adapter refs', () => {
  const h = createHarness();
  h.emit('source', {
    buttons: [
      { label: 'STREAMS', configured: true, casting: false },
      { label: 'PLEX', configured: true, casting: false },
      { label: 'JELLYFIN', configured: true, casting: false },
    ],
  });
  h.emit('transport', { source: 'plex', adapterRef: '/library/metadata/42' });

  assert.equal(h.lamps[0].classes.has('casting'), false);
  assert.equal(h.lamps[1].classes.has('casting'), true);
  assert.equal(h.lamps[2].classes.has('casting'), false);
});

// Input keys: a source has no input to switch to (sources push casts), so
// pressing a lamp key flashes that input's status on the VFD, like a
// receiver showing the selected input's name. A second press while the
// info is up opens that adapter's settings.
test('pressing a ready input key flashes its name and how to use it', () => {
  const h = createHarness();
  h.emit('source', { buttons: [{ label: 'PLEX', configured: true, casting: false }] });
  lamp(h, 'plex').click();
  assert.deepEqual(h.flashes, [['PLEX', 'READY · CAST FROM THE PLEX APP', 'PRESS AGAIN FOR SETUP']]);
});

test('pressing an input key with an issue shows the error on the VFD', () => {
  const h = createHarness();
  h.emit('source', {
    buttons: [{ label: 'JELLYFIN', configured: true, issue: true, lastError: 'Get "http://jf:8096/System/Info": connection refused' }],
  });
  lamp(h, 'jellyfin').click();
  assert.equal(h.flashes[0][0], 'JELLYFIN');
  assert.equal(h.flashes[0][1], 'ISSUE: GET "HTTP://JF:8096/SYSTEM/INFO": CONNECTION REFUSED');
  assert.equal(h.flashes[0][2], 'PRESS AGAIN FOR SETUP');
});

test('pressing an unconfigured input key says so', () => {
  const h = createHarness();
  h.emit('source', { buttons: [{ label: 'STREAMS', configured: false }] });
  lamp(h, 'streams').click();
  assert.deepEqual(h.flashes[0], ['STREAMS', 'NOT CONFIGURED', 'PRESS AGAIN FOR SETUP']);
});

test('pressing the on-air input key shows ON AIR and does not arm setup', () => {
  const h = createHarness();
  h.emit('source', { buttons: [{ label: 'PLEX', configured: true, casting: true }] });
  lamp(h, 'plex').click();
  assert.deepEqual(h.flashes[0], ['PLEX', 'ON AIR', '']);
  lamp(h, 'plex').click();
  assert.equal(h.bodyClasses.has('settings-open'), false);
});

test('a second press while the info is up opens that adapter in settings', () => {
  const h = createHarness();
  h.emit('source', { buttons: [{ label: 'PLEX', configured: true, casting: false }] });
  lamp(h, 'plex').click();
  lamp(h, 'plex').click();
  assert.equal(h.bodyClasses.has('settings-open'), true);
  assert.equal(h.tab.clicks, 1);
  assert.equal(h.plexField.scrolled, 1);
  assert.equal(h.flashes.length, 1, 'the second press opens setup instead of re-flashing');
});

test('after the info times out a press flashes again instead of opening setup', () => {
  const h = createHarness();
  h.emit('source', { buttons: [{ label: 'PLEX', configured: true, casting: false }] });
  lamp(h, 'plex').click();
  h.expire();
  lamp(h, 'plex').click();
  assert.equal(h.bodyClasses.has('settings-open'), false);
  assert.equal(h.flashes.length, 2);
});

test('AUX joins the lamps and a configured AUX press is left to the start action', () => {
  const h = createHarness();
  h.emit('source', { buttons: [{ label: 'AUX', action: 'aux-start', configured: true, casting: false }] });
  assert.equal(lamp(h, 'aux').classes.has('configured-idle'), true);
  lamp(h, 'aux').click();
  assert.equal(h.flashes.length, 0, 'chassis.js starts capture; no info flash');
});

test('an unconfigured AUX press flashes NOT CONFIGURED', () => {
  const h = createHarness();
  h.emit('source', { buttons: [{ label: 'AUX', action: 'aux-start', configured: false }] });
  assert.equal(lamp(h, 'aux').classes.has('unavailable'), true);
  lamp(h, 'aux').click();
  assert.deepEqual(h.flashes[0], ['AUX', 'NOT CONFIGURED', 'PRESS AGAIN FOR SETUP']);
});
