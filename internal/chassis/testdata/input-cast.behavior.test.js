const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

class FakeElement {
  constructor(value = '') {
    this.value = value;
    this.textContent = '';
    this.disabled = false;
    this.hidden = false;
    this.title = '';
    this.dataset = {};
    this.style = { display: '' };
    this.files = [];
    this.children = [];
    this.attributes = new Map();
    this.listeners = new Map();
    this.classes = new Set();
    this.classList = {
      add: (name) => this.classes.add(name),
      remove: (name) => this.classes.delete(name),
      contains: (name) => this.classes.has(name),
      toggle: (name, on) => on ? this.classes.add(name) : this.classes.delete(name),
    };
  }

  addEventListener(name, fn) {
    const list = this.listeners.get(name) || [];
    list.push(fn);
    this.listeners.set(name, list);
  }

  async dispatch(name) {
    for (const fn of this.listeners.get(name) || []) {
      await fn({ type: name, target: this });
    }
  }

  click() {
    return this.dispatch('click');
  }

  appendChild(child) {
    this.children.push(child);
    if (this.tagName === 'SELECT' && !this.value && child.value) this.value = child.value;
    return child;
  }

  replaceChildren(...children) {
    this.children = [];
    this.value = this.tagName === 'SELECT' ? '' : this.value;
    children.forEach((child) => this.appendChild(child));
  }

  setAttribute(name, value) {
    this.attributes.set(name, String(value));
  }

  getAttribute(name) {
    return this.attributes.get(name) || null;
  }
}

function createHarness() {
  const body = new FakeElement();
  const panel = new FakeElement();
  const input = new FakeElement('https://example.com/video.mp4');
  const clearBtn = new FakeElement();
  const chip = new FakeElement();
  const castBtn = new FakeElement();
  const uploadBtn = new FakeElement();
  const fileInput = new FakeElement();
  const localFilesBtn = new FakeElement();
  localFilesBtn.disabled = true;
  localFilesBtn.title = 'Configure Local Files in Settings';
  const localFilesDrawer = new FakeElement();
  const localFilesCloseBtn = new FakeElement();
  const localFilesSelect = new FakeElement();
  localFilesSelect.tagName = 'SELECT';
  const localFilesEntries = new FakeElement();
  const localFilesBreadcrumb = new FakeElement();
  const localFilesError = new FakeElement();
  const requests = [];
  const timers = new Map();
  let now = 0;
  let nextTimer = 1;

  const document = {
    body,
    querySelector: (selector) => selector === '.input-section' ? panel : null,
    createElement: (tag) => {
      const el = new FakeElement();
      el.tagName = String(tag).toUpperCase();
      return el;
    },
    getElementById: (id) => ({
      'paste-input': input,
      'paste-clear': clearBtn,
      'paste-chip': chip,
      'cast-btn': castBtn,
      'upload-btn': uploadBtn,
      'torrent-file-input': fileInput,
      'localfiles-btn': localFilesBtn,
      'localfiles-drawer': localFilesDrawer,
      'localfiles-close-btn': localFilesCloseBtn,
      'localfiles-library-select': localFilesSelect,
      'localfiles-entries': localFilesEntries,
      'localfiles-breadcrumb': localFilesBreadcrumb,
      'localfiles-error': localFilesError,
    })[id] || null,
  };

  function setTimeoutFake(fn, delay) {
    const id = nextTimer++;
    timers.set(id, { at: now + delay, fn });
    return id;
  }

  function clearTimeoutFake(id) {
    timers.delete(id);
  }

  function advance(ms) {
    now += ms;
    let ran = true;
    while (ran) {
      ran = false;
      const due = [...timers.entries()]
        .filter(([, timer]) => timer.at <= now)
        .sort((a, b) => a[1].at - b[1].at)[0];
      if (due) {
        timers.delete(due[0]);
        due[1].fn();
        ran = true;
      }
    }
  }

  function fetchFake(url, options) {
    requests.push({ url, options, body: String(options.body) });
    if (url === '/ui/localfiles/browse') {
      return Promise.resolve({ json: async () => ({ ok: true, entries: [] }) });
    }
    return Promise.resolve({ json: async () => ({ ok: false, chip: 'BAD URL' }) });
  }

  const context = {
    document,
    fetch: fetchFake,
    window: {},
    URL,
    URLSearchParams,
    setTimeout: setTimeoutFake,
    clearTimeout: clearTimeoutFake,
  };
  vm.createContext(context);
  const code = fs.readFileSync(path.join(__dirname, '..', 'static', 'input-cast.js'), 'utf8');
  vm.runInContext(code, context, { filename: 'input-cast.js' });

  return {
    input,
    chip,
    castBtn,
    requests,
    advance,
    window: context.window,
    body,
    localFilesBtn,
    localFilesDrawer,
    localFilesSelect,
  };
}

async function settle() {
  for (let i = 0; i < 4; i += 1) {
    await Promise.resolve();
  }
}

test('failed cast keeps error chip after controls re-enable and clears on input', async () => {
  const h = createHarness();
  assert.equal(h.chip.dataset.chipKind, 'url');
  assert.equal(h.castBtn.disabled, false);

  await h.castBtn.dispatch('click');
  await settle();
  assert.equal(h.requests.length, 1);
  assert.equal(h.chip.dataset.chipKind, 'err');
  assert.equal(h.chip.textContent, 'BAD URL');
  assert.equal(h.castBtn.disabled, false);

  h.input.value = 'https://example.com/other.mp4';
  await h.input.dispatch('input');
  h.advance(120);
  assert.equal(h.chip.dataset.chipKind, 'url');
  assert.equal(h.chip.textContent, 'URL');
});

test('settings library save enables receiver local files without reload', async () => {
  const h = createHarness();
  assert.equal(h.localFilesBtn.disabled, true);
  assert.equal(h.localFilesSelect.children.length, 0);
  assert.equal(typeof h.window.Chassis.localFiles.setLibraries, 'function');

  h.window.Chassis.localFiles.setLibraries([{ name: 'Array', root: '/array' }]);

  assert.equal(h.localFilesBtn.disabled, false);
  assert.equal(h.localFilesBtn.title, 'Browse Local Files');
  assert.equal(h.localFilesSelect.children.length, 1);
  assert.equal(h.localFilesSelect.children[0].value, 'Array');
  assert.equal(h.localFilesSelect.value, 'Array');

  await h.localFilesBtn.click();
  await settle();
  assert.equal(h.requests.at(-1).url, '/ui/localfiles/browse');
  assert.equal(h.requests.at(-1).body, 'lib=Array&path=');
  assert.equal(h.localFilesDrawer.hidden, false);
  assert.equal(h.localFilesDrawer.classList.contains('localfiles-open'), true);
  assert.equal(h.localFilesDrawer.getAttribute('aria-hidden'), 'false');
});

test('receiver local files button toggles lit drawer state closed on second click', async () => {
  const h = createHarness();
  h.window.Chassis.localFiles.setLibraries([{ name: 'Array', root: '/array' }]);

  await h.localFilesBtn.click();
  await settle();
  assert.equal(h.requests.length, 1);
  assert.equal(h.localFilesDrawer.hidden, false);
  assert.equal(h.localFilesDrawer.classList.contains('localfiles-open'), true);
  assert.equal(h.localFilesDrawer.getAttribute('aria-hidden'), 'false');
  assert.equal(h.body.classList.contains('localfiles-open'), true);
  assert.equal(h.localFilesBtn.getAttribute('aria-expanded'), 'true');

  await h.localFilesBtn.click();
  await settle();
  assert.equal(h.requests.length, 1);
  assert.equal(h.localFilesDrawer.classList.contains('localfiles-open'), false);
  assert.equal(h.localFilesDrawer.getAttribute('aria-hidden'), 'true');
  assert.equal(h.body.classList.contains('localfiles-open'), false);
  assert.equal(h.localFilesBtn.getAttribute('aria-expanded'), 'false');
});
