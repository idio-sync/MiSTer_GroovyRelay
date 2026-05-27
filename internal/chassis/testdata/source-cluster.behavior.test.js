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
  constructor(sourceId) {
    this.dataset = { sourceId };
    this.classes = new Set(['lamp']);
    this.classList = new FakeClassList(this);
    this.attrs = new Map([['data-source-id', sourceId]]);
  }

  getAttribute(name) {
    return this.attrs.get(name) || '';
  }

  setAttribute(name, value) {
    this.attrs.set(name, value);
  }
}

function createHarness() {
  const lamps = [new FakeLamp('streams'), new FakeLamp('plex'), new FakeLamp('jellyfin')];
  const handlers = new Map();
  const document = {
    querySelectorAll: (selector) => selector === '.source-cluster .lamp' ? lamps : [],
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
  const context = { console: { warn() {} }, document, window };
  vm.createContext(context);
  const code = fs.readFileSync(path.join(__dirname, '..', 'static', 'source-cluster.js'), 'utf8');
  vm.runInContext(code, context, { filename: 'source-cluster.js' });

  function emit(name, payload) {
    const fn = handlers.get(name);
    assert.equal(typeof fn, 'function', `missing ${name} subscription`);
    fn({ data: JSON.stringify(payload) });
  }

  return { lamps, emit };
}

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
