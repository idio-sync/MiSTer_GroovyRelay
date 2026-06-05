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

function makeStyle() {
  const props = new Map();
  return {
    setProperty(name, value) {
      props.set(name, String(value));
    },
    getPropertyValue(name) {
      return props.get(name) || '';
    },
  };
}

class FakeElement {
  constructor(tag = 'div', opts = {}) {
    this.tagName = String(tag).toUpperCase();
    this.nodeName = this.tagName;
    this.isText = tag === '#text';
    this.classes = new Set(String(opts.className || '').split(/\s+/).filter(Boolean));
    this.classList = new FakeClassList(this);
    this.dataset = { ...(opts.dataset || {}) };
    this.attributes = new Map();
    this.listeners = new Map();
    this.style = makeStyle();
    this.children = [];
    this.parentElement = null;
    this.parentNode = null;
    this.hidden = Boolean(opts.hidden);
    this.title = opts.title || '';
    this.type = opts.type || '';
    this._id = opts.id || '';
    this._text = opts.text || '';
    if (this._id) this.attributes.set('id', this._id);
    if (this.tagName === 'TEMPLATE') {
      this.content = new FakeElement('#fragment');
    }
  }

  get id() {
    return this._id;
  }

  set id(value) {
    this._id = String(value || '');
    if (this._id) {
      this.attributes.set('id', this._id);
    } else {
      this.attributes.delete('id');
    }
  }

  get className() {
    return Array.from(this.classes).join(' ');
  }

  set className(value) {
    this.classes = new Set(String(value || '').split(/\s+/).filter(Boolean));
  }

  get textContent() {
    if (this.children.length > 0) {
      return this.children.map((child) => child.textContent).join('');
    }
    return this._text;
  }

  get firstChild() {
    return this.children.length > 0 ? this.children[0] : null;
  }

  set textContent(value) {
    this._text = String(value == null ? '' : value);
    this.children = [];
  }

  appendChild(child) {
    if (child.parentElement) {
      child.parentElement.children = child.parentElement.children.filter((item) => item !== child);
    }
    child.parentElement = this;
    child.parentNode = this;
    this.children.push(child);
    return child;
  }

  insertBefore(child, before) {
    if (child.parentElement) {
      child.parentElement.children = child.parentElement.children.filter((item) => item !== child);
    }
    child.parentElement = this;
    child.parentNode = this;
    const index = this.children.indexOf(before);
    if (index < 0) {
      this.children.push(child);
    } else {
      this.children.splice(index, 0, child);
    }
    return child;
  }

  replaceChildren(...children) {
    this.children.forEach((child) => {
      child.parentElement = null;
      child.parentNode = null;
    });
    this.children = [];
    children.forEach((child) => this.appendChild(child));
  }

  remove() {
    if (!this.parentElement) return;
    this.parentElement.children = this.parentElement.children.filter((child) => child !== this);
    this.parentElement = null;
    this.parentNode = null;
  }

  addEventListener(type, fn) {
    const list = this.listeners.get(type) || [];
    list.push(fn);
    this.listeners.set(type, list);
  }

  setAttribute(name, value) {
    const text = String(value);
    this.attributes.set(name, text);
    if (name === 'id') this.id = text;
  }

  getAttribute(name) {
    return this.attributes.has(name) ? this.attributes.get(name) : null;
  }

  closest(selector) {
    let node = this;
    while (node) {
      if (matchesSelector(node, selector)) return node;
      node = node.parentElement;
    }
    return null;
  }

  querySelector(selector) {
    return walkFind(this, selector, false);
  }

  querySelectorAll(selector) {
    const results = [];
    walkFind(this, selector, true, results);
    return results;
  }

  getBoundingClientRect() {
    return { left: 0, top: 0, width: 0, height: 0 };
  }

  cloneNode(deep = false) {
    const clone = new FakeElement(this.tagName.toLowerCase(), {
      id: this.id,
      className: this.className,
      text: this._text,
      hidden: this.hidden,
      title: this.title,
      type: this.type,
      dataset: this.dataset,
    });
    clone.attributes = new Map(this.attributes);
    Object.assign(clone.style, this.style);
    if (deep) {
      this.children.forEach((child) => clone.appendChild(child.cloneNode(true)));
      if (this.content) {
        clone.content.replaceChildren(...this.content.children.map((child) => child.cloneNode(true)));
      }
    }
    return clone;
  }
}

function attrValue(node, attr) {
  if (attr.startsWith('data-')) {
    const key = attr.slice(5).replace(/-([a-z])/g, (_, ch) => ch.toUpperCase());
    return Object.prototype.hasOwnProperty.call(node.dataset, key) ? node.dataset[key] : null;
  }
  if (attr === 'id') return node.id || null;
  if (attr === 'hidden') return node.hidden ? '' : null;
  return node.attributes.has(attr) ? node.attributes.get(attr) : null;
}

function matchesSelector(node, selector) {
  const parts = String(selector || '').trim().split(/\s+/).filter(Boolean);
  if (parts.length > 1) return matchesSelectorPath(node, parts, parts.length - 1);
  return matchesSimpleSelector(node, parts[0] || '');
}

function matchesSelectorPath(node, parts, index) {
  if (!matchesSimpleSelector(node, parts[index])) return false;
  if (index === 0) return true;
  let parent = node.parentElement;
  while (parent) {
    if (matchesSelectorPath(parent, parts, index - 1)) return true;
    parent = parent.parentElement;
  }
  return false;
}

function matchesSimpleSelector(node, selector) {
  if (!node || !selector) return false;
  let rest = selector;
  const tagMatch = rest.match(/^[a-z][a-z0-9-]*/i);
  if (tagMatch) {
    if (node.tagName.toLowerCase() !== tagMatch[0].toLowerCase()) return false;
    rest = rest.slice(tagMatch[0].length);
  }
  while (rest.length > 0) {
    if (rest.startsWith('#')) {
      const match = rest.match(/^#([a-zA-Z0-9_:-]+)/);
      if (!match || node.id !== match[1]) return false;
      rest = rest.slice(match[0].length);
      continue;
    }
    if (rest.startsWith('.')) {
      const match = rest.match(/^\.([a-zA-Z0-9_-]+)/);
      if (!match || !node.classes.has(match[1])) return false;
      rest = rest.slice(match[0].length);
      continue;
    }
    if (rest.startsWith('[')) {
      const match = rest.match(/^\[([^=\]\^]+)(\^=|=)?(?:"([^"]*)")?\]/);
      if (!match) return false;
      const value = attrValue(node, match[1]);
      if (value == null) return false;
      if (match[2] === '=' && value !== match[3]) return false;
      if (match[2] === '^=' && !String(value).startsWith(match[3])) return false;
      rest = rest.slice(match[0].length);
      continue;
    }
    return false;
  }
  return true;
}

function walkFind(root, selector, all, acc = []) {
  for (const child of root.children) {
    if (matchesSelector(child, selector)) {
      if (!all) return child;
      acc.push(child);
    }
    const found = walkFind(child, selector, all, acc);
    if (!all && found) return found;
  }
  return all ? acc : null;
}

function findByID(root, id) {
  if (root.id === id) return root;
  for (const child of root.children) {
    const found = findByID(child, id);
    if (found) return found;
  }
  if (root.content) {
    const found = findByID(root.content, id);
    if (found) return found;
  }
  return null;
}

function provider(id, groups = []) {
  return {
    id,
    displayName: id === 'mtv' ? 'MTV' : 'Mix',
    badgeLabel: id === 'mtv' ? 'MTV' : 'MX',
    badgeClass: id === 'mtv' ? 'mtv' : 'u-teal',
    live: false,
    groups,
  };
}

function group(id, name, channels) {
  return { id, name, channels };
}

function channel(id, name, playMode = '', live = false) {
  return { id, name, playMode, live };
}

function createHarness() {
  const body = new FakeElement('body');
  const drawer = new FakeElement('div', { id: 'catalog-drawer' });
  const tabs = new FakeElement('div', { className: 'catalog-provider-tabs' });
  const indicator = new FakeElement('span', { id: 'catalog-tab-indicator', className: 'catalog-tab-indicator' });
  const initialWrap = new FakeElement('div', { className: 'catalog-provider-wrap' });
  const initialTab = new FakeElement('button', { className: 'catalog-provider-tab active', dataset: { provider: 'user:keep' } });
  initialWrap.appendChild(initialTab);
  tabs.appendChild(indicator);
  tabs.appendChild(initialWrap);
  drawer.appendChild(tabs);
  const browse = new FakeElement('button', { id: 'browse-toggle', text: 'Browse' });
  const rail = new FakeElement('div', { id: 'catalog-rail' });
  const grid = new FakeElement('div', { id: 'catalog-grid' });
  const mode = new FakeElement('span', { id: 'preset-mode-label', text: 'Presets' });
  const templateHost = new FakeElement('div', { id: 'catalog-tree-host' });
  const staleTemplate = new FakeElement('template', { id: 'catalog-tree-user:stale' });
  templateHost.appendChild(staleTemplate);
  body.appendChild(drawer);
  body.appendChild(browse);
  body.appendChild(rail);
  body.appendChild(grid);
  body.appendChild(mode);
  body.appendChild(templateHost);

  const document = {
    body,
    getElementById(id) {
      return findByID(body, id);
    },
    querySelector(selector) {
      return body.querySelector(selector);
    },
    querySelectorAll(selector) {
      return body.querySelectorAll(selector);
    },
    createElement(tag) {
      return new FakeElement(tag);
    },
    createTextNode(text) {
      return new FakeElement('#text', { text });
    },
    dispatchEvent() {},
  };
  const subscribers = new Map();
  const context = {
    document,
    window: {
      Chassis: {
        events: {
          subscribe(name, fn) {
            const list = subscribers.get(name) || [];
            list.push(fn);
            subscribers.set(name, list);
          },
        },
        input: { showError() {} },
      },
    },
    CSS: { escape: (value) => String(value) },
    CustomEvent: function CustomEvent() {},
    setTimeout() {},
    fetch: async () => ({ ok: true, json: async () => ({ ok: true }) }),
    URLSearchParams,
    console,
  };
  context.window.window = context.window;
  context.window.document = document;
  context.window.CSS = context.CSS;
  context.window.CustomEvent = context.CustomEvent;
  vm.createContext(context);
  const code = fs.readFileSync(path.join(__dirname, '..', 'static', 'catalog-browser.js'), 'utf8');
  vm.runInContext(code, context, { filename: 'catalog-browser.js' });

  function emit(name, payload) {
    for (const fn of subscribers.get(name) || []) {
      fn({ data: JSON.stringify(payload) });
    }
  }

  return {
    context,
    document,
    body,
    drawer,
    tabs,
    indicator,
    rail,
    grid,
    templateHost,
    subscribers,
    emit,
    cb: context.window.Chassis.catalogBrowser,
  };
}

function childWithClass(node, className) {
  return node.children.find((child) => child.classList && child.classList.contains(className));
}

test('_buildTab renders a provider wrapper with sibling user edit button', () => {
  const h = createHarness();
  const wrapper = h.cb._buildTab(provider('user:mix', [
    group('g1', 'Races', [channel('a', 'A'), channel('b', 'B')]),
  ]));

  assert.equal(wrapper.classList.contains('catalog-provider-wrap'), true);
  const tab = childWithClass(wrapper, 'catalog-provider-tab');
  const pencil = childWithClass(wrapper, 'cf-pencil');

  assert.equal(tab.dataset.provider, 'user:mix');
  assert.equal(pencil.dataset.editProvider, 'user:mix');
  assert.equal(pencil.parentElement, wrapper);
  assert.equal(pencil.closest('.catalog-provider-tab'), null);
  assert.equal(tab.children.includes(pencil), false);
  assert.equal(childWithClass(tab, 'ic').textContent, 'MX');
  assert.equal(childWithClass(tab, 'ic').classList.contains('u-teal'), true);
  assert.equal(childWithClass(tab, 'ch-count').textContent, '2');
});

test('_buildTreeNodes produces rail buttons and grid cards matching the hidden template shape', () => {
  const h = createHarness();
  const nodes = h.cb._buildTreeNodes(provider('user:mix', [
    group('races', 'Races', [channel('live', 'Live Race', 'shuffle', true)]),
    group('news', 'News', [channel('daily', 'Daily Update')]),
  ]), true);
  const rails = nodes.filter((node) => node.classList.contains('catalog-rail-group'));
  const grids = nodes.filter((node) => node.classList.contains('catalog-tree-grid'));

  assert.equal(rails.length, 2);
  assert.equal(rails[0].dataset.group, 'races');
  assert.equal(rails[0].classList.contains('active'), true);
  assert.equal(childWithClass(rails[0], 'count').textContent, '1');
  assert.equal(rails[0].style.getPropertyValue('--i'), '0');
  assert.equal(grids[0].dataset.group, 'races');
  assert.equal(grids[0].hidden, false);
  assert.equal(grids[1].hidden, true);

  const card = childWithClass(grids[0], 'ch-card');
  assert.equal(card.dataset.provider, 'user:mix');
  assert.equal(card.dataset.channel, 'live');
  assert.equal(card.classList.contains('live'), true);
  assert.equal(card.getAttribute('role'), 'button');
  assert.equal(card.getAttribute('tabindex'), '0');
  assert.equal(card.style.getPropertyValue('--i'), '0');
  assert.equal(childWithClass(card, 'star').textContent, '☆');
  assert.equal(childWithClass(card, 'name').textContent, 'Live Race');
  const meta = childWithClass(card, 'meta');
  assert.equal(meta.children[0].textContent, 'LIVE');
  assert.equal(meta.children[1].classList.contains('mode'), true);
  assert.equal(meta.children[1].textContent, 'shuffle');
});

test('catalog-browser subscribes to the catalog event', () => {
  const h = createHarness();

  assert.equal((h.subscribers.get('catalog') || []).length, 1);
});

test('rebuild preserves the indicator, active provider, new button, and hidden templates', () => {
  const h = createHarness();

  h.cb.rebuild({
    providers: [
      provider('mtv', [group('music', 'Music', [channel('classic', 'Classic')])]),
      provider('user:keep', [group('mix', 'Mix', [channel('list', 'List')])]),
    ],
  });

  assert.equal(h.tabs.children[0], h.indicator);
  assert.equal(h.indicator.parentElement, h.tabs);
  const active = h.tabs.querySelector('.catalog-provider-tab.active');
  assert.equal(active.dataset.provider, 'user:keep');
  assert.equal(h.tabs.children[h.tabs.children.length - 1].id, 'catalog-provider-new');
  assert.equal(h.document.getElementById('catalog-tree-user:stale'), null);
  const keepTemplate = h.document.getElementById('catalog-tree-user:keep');
  assert.ok(keepTemplate);
  const payload = keepTemplate.content.querySelector('.catalog-tree-payload');
  assert.equal(payload.dataset.provider, 'user:keep');

  h.cb.rebuild({
    providers: [
      provider('mtv', [group('music', 'Music', [channel('classic', 'Classic')])]),
    ],
  });

  assert.equal(h.tabs.querySelector('.catalog-provider-tab.active').dataset.provider, 'mtv');
  assert.equal(h.document.getElementById('catalog-tree-user:keep'), null);
  assert.ok(h.document.getElementById('catalog-tree-mtv'));
});

test('closed catalog rebuild refreshes active rail and grid before browse opens', () => {
  const h = createHarness();

  h.cb.rebuild({
    providers: [
      provider('user:keep', [group('fresh', 'Fresh', [channel('new-list', 'New List')])]),
    ],
  });

  assert.equal(h.rail.children.length, 1);
  assert.equal(h.rail.children[0].dataset.group, 'fresh');
  assert.equal(h.rail.children[0].classList.contains('active'), true);
  assert.equal(h.grid.children.length, 1);
  assert.equal(h.grid.children[0].dataset.provider, 'user:keep');
  assert.equal(h.grid.children[0].dataset.channel, 'new-list');
});

test('rebuild reapplies cached preset stars and tuned state to rebuilt templates', () => {
  const h = createHarness();

  h.emit('presets', { slots: [{ slot: 7, provider: 'user:mix', channel: 'list' }] });
  h.emit('transport', { adapterRef: 'streams:user:mix:list:sess:1' });
  h.cb.rebuild({
    providers: [
      provider('user:mix', [group('mix', 'Mix', [channel('list', 'List')])]),
    ],
  });

  const card = h.document.getElementById('catalog-tree-user:mix').content.querySelector('.ch-card[data-channel="list"]');
  const star = childWithClass(card, 'star');
  assert.equal(card.classList.contains('starred'), true);
  assert.equal(card.classList.contains('tuned'), true);
  assert.equal(star.textContent, '★');
  assert.equal(star.title, 'In preset 07');
});
