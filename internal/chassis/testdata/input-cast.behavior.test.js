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
    this.dataset = {};
    this.style = { display: '' };
    this.files = [];
    this.listeners = new Map();
    this.classes = new Set();
    this.classList = {
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
}

function createHarness() {
  const panel = new FakeElement();
  const input = new FakeElement('https://example.com/video.mp4');
  const clearBtn = new FakeElement();
  const chip = new FakeElement();
  const castBtn = new FakeElement();
  const uploadBtn = new FakeElement();
  const fileInput = new FakeElement();
  const requests = [];
  const timers = new Map();
  let now = 0;
  let nextTimer = 1;

  const document = {
    querySelector: (selector) => selector === '.input-section' ? panel : null,
    getElementById: (id) => ({
      'paste-input': input,
      'paste-clear': clearBtn,
      'paste-chip': chip,
      'cast-btn': castBtn,
      'upload-btn': uploadBtn,
      'torrent-file-input': fileInput,
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

  return { input, chip, castBtn, requests, advance };
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
