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

  remove(name) {
    this.owner.classes.delete(name);
  }
}

class FakeElement {
  constructor(className = '') {
    this.className = className;
    this.classes = new Set(className.split(/\s+/).filter(Boolean));
    this.classList = new FakeClassList(this);
    this.dataset = {};
    this.textContent = '';
    this.children = [];
    this.parent = null;
    this.listeners = new Map();
  }

  addEventListener(name, fn) {
    const list = this.listeners.get(name) || [];
    list.push(fn);
    this.listeners.set(name, list);
  }

  appendChild(child) {
    child.parent = this;
    this.children.push(child);
    return child;
  }

  insertBefore(child, before) {
    child.parent = this;
    const idx = this.children.indexOf(before);
    if (idx < 0) {
      this.children.unshift(child);
    } else {
      this.children.splice(idx, 0, child);
    }
    return child;
  }

  remove() {
    if (!this.parent) return;
    this.parent.children = this.parent.children.filter((child) => child !== this);
    this.parent = null;
  }

  querySelector(selector) {
    return this.children.find((child) => child.classes.has(selector.slice(1))) || null;
  }

  querySelectorAll(selector) {
    if (selector === '.preset') {
      return this.children.filter((child) => child.classes.has('preset'));
    }
    return [];
  }

  closest(selector) {
    return this.classes.has(selector.slice(1)) ? this : null;
  }
}

function createSlot(slot) {
  const el = new FakeElement('preset empty');
  el.dataset.slot = String(slot);
  const num = new FakeElement('num');
  num.textContent = String(slot).padStart(2, '0');
  el.appendChild(num);
  return el;
}

function createHarness() {
  const bank = new FakeElement('preset-bank');
  for (let i = 1; i <= 12; i += 1) {
    bank.appendChild(createSlot(i));
  }
  const modeLabel = new FakeElement();
  modeLabel.textContent = 'Memory · 0 / 12 slots';
  modeLabel.dataset.closedText = modeLabel.textContent;
  const count = new FakeElement();
  count.textContent = '★ 0';
  const handlers = new Map();

  const document = {
    querySelector: (selector) => selector === '.preset-bank' ? bank : null,
    getElementById: (id) => ({
      'preset-mode-label': modeLabel,
      'preset-count': count,
    })[id] || null,
    createElement: (tag) => new FakeElement(tag),
    dispatchEvent() {},
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
  const context = { document, window, CustomEvent: function CustomEvent() {} };
  vm.createContext(context);
  const code = fs.readFileSync(path.join(__dirname, '..', 'static', 'preset-bank.js'), 'utf8');
  vm.runInContext(code, context, { filename: 'preset-bank.js' });

  function emitPresets(slots) {
    const fn = handlers.get('presets');
    assert.equal(typeof fn, 'function');
    fn({ data: JSON.stringify({ slots }) });
  }

  return { modeLabel, count, emitPresets };
}

function emptySlots() {
  return Array.from({ length: 12 }, (_, i) => ({ slot: i + 1 }));
}

test('presets event refreshes preset count and closed mode label', () => {
  const h = createHarness();
  const slots = emptySlots();
  slots[0] = {
    slot: 1,
    provider: 'mtv-rewind',
    channel: '80s',
    title: 'MTV 80s',
    badgeLabel: 'MTV',
    badgeClass: 'mtv',
    live: false,
  };
  slots[1] = {
    slot: 2,
    provider: 'cartoon-rewind',
    channel: 'heman',
    title: 'He-Man',
    badgeLabel: 'CART',
    badgeClass: 'cartoon',
    live: false,
  };

  h.emitPresets(slots);

  assert.equal(h.count.textContent, '★ 2');
  assert.equal(h.modeLabel.textContent, 'Memory · drag to reorder · 2 / 12');
  assert.equal(h.modeLabel.dataset.closedText, 'Memory · drag to reorder · 2 / 12');
});
