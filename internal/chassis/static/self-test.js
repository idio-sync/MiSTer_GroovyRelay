// Receiver chassis self-test — hidden service-menu diagnostic. Delight spec.
//
// Period-correct easter egg: every 90s AV receiver hid a diagnostic mode
// behind a power-on key combo. Enter the konami sequence
//   Up Up Down Down Left Right Left Right B A
// or load the page with ?selftest=1 to run a staged faceplate self-test
// inside the VFD glass — each subsystem reports OK in turn while the
// matching lamps sweep, ending on ALL SYSTEMS OK.
//
// Entirely non-destructive: the live lamp classes are never touched (the
// sweep uses a throwaway `selftest-lit` class), and the readout is a
// disposable overlay element removed on teardown. Hangs off window.Chassis.
(() => {
  'use strict';

  const SEQ = [
    'ArrowUp', 'ArrowUp', 'ArrowDown', 'ArrowDown',
    'ArrowLeft', 'ArrowRight', 'ArrowLeft', 'ArrowRight', 'b', 'a',
  ];

  const CHECKS = [
    { label: 'VFD SEGMENT', sweep: null },
    { label: 'SOURCE INPUTS', sweep: 'sources' },
    { label: 'TALLY LAMPS', sweep: 'tally' },
    { label: 'GROOVY UDP', sweep: null },
    { label: 'AUDIO PCM', sweep: null },
  ];

  let running = false;
  let progress = 0;
  const timers = [];

  function reducedMotion() {
    try {
      return typeof window.matchMedia === 'function'
        && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
    } catch (_) {
      return false;
    }
  }

  function after(ms, fn) {
    const id = setTimeout(fn, ms);
    timers.push(id);
    return id;
  }

  function clearTimers() {
    while (timers.length) {
      clearTimeout(timers.pop());
    }
  }

  function buildScreen(rev) {
    const screen = document.createElement('div');
    screen.className = 'self-test-screen';
    screen.setAttribute('aria-hidden', 'true');

    const head = document.createElement('div');
    head.className = 'st-head';
    const title = document.createElement('span');
    title.textContent = '◉ SELF TEST';
    const revEl = document.createElement('span');
    revEl.className = 'st-rev';
    revEl.textContent = rev ? 'GEN ' + rev : 'DIAGNOSTIC';
    head.append(title, revEl);

    const list = document.createElement('div');
    list.className = 'st-list';
    CHECKS.forEach((check) => {
      const line = document.createElement('div');
      line.className = 'st-line';
      const name = document.createElement('span');
      name.textContent = check.label;
      const result = document.createElement('span');
      result.className = 'st-result';
      result.textContent = '....';
      line.append(name, result);
      list.appendChild(line);
    });

    const foot = document.createElement('div');
    foot.className = 'st-foot';
    foot.textContent = 'ALL SYSTEMS OK';

    screen.append(head, list, foot);
    return screen;
  }

  function sweep(kind) {
    let nodes = [];
    if (kind === 'sources') {
      nodes = Array.from(document.querySelectorAll('.source-cluster .hw-btn'));
    } else if (kind === 'tally') {
      nodes = Array.from(document.querySelectorAll('.led-row .led, .source-cluster .lamp .led'));
    }
    nodes.forEach((node, i) => {
      after(i * 70, () => node.classList.add('selftest-lit'));
      after(i * 70 + 300, () => node.classList.remove('selftest-lit'));
    });
  }

  function teardown(screen) {
    screen.classList.add('closing');
    after(reducedMotion() ? 0 : 320, () => {
      if (screen.parentNode) {
        screen.parentNode.removeChild(screen);
      }
      document.body.classList.remove('self-test-active');
      document.querySelectorAll('.selftest-lit').forEach((node) => node.classList.remove('selftest-lit'));
      clearTimers();
      running = false;
    });
  }

  function run() {
    if (running) {
      return;
    }
    const vfd = document.querySelector('.vfd');
    if (!vfd) {
      return;
    }
    running = true;

    const meta = document.querySelector('meta[name="chassis-generation"]');
    const rev = meta ? (meta.getAttribute('content') || '') : '';
    const screen = buildScreen(rev);
    vfd.appendChild(screen);
    document.body.classList.add('self-test-active');

    const lines = Array.from(screen.querySelectorAll('.st-line'));
    const fast = reducedMotion();
    const step = fast ? 60 : 460;

    CHECKS.forEach((check, i) => {
      after(220 + i * step, () => {
        const line = lines[i];
        if (line) {
          line.classList.add('ok');
          const result = line.querySelector('.st-result');
          if (result) {
            result.textContent = 'OK';
          }
        }
        if (check.sweep && !fast) {
          sweep(check.sweep);
        }
      });
    });

    const finale = 220 + CHECKS.length * step + 120;
    after(finale, () => screen.classList.add('done'));
    after(finale + (fast ? 400 : 1400), () => teardown(screen));
  }

  function isTextEntry(target) {
    if (!target || !target.tagName) {
      return false;
    }
    const tag = target.tagName.toUpperCase();
    return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || target.isContentEditable === true;
  }

  function onKey(ev) {
    // Don't swallow keys while the operator is editing a settings field.
    if (isTextEntry(ev.target)) {
      progress = 0;
      return;
    }
    const key = ev.key === 'B' ? 'b' : ev.key === 'A' ? 'a' : ev.key;
    if (key === SEQ[progress]) {
      progress += 1;
      if (progress === SEQ.length) {
        progress = 0;
        run();
      }
      return;
    }
    // A mismatch resets — but if the stray key is itself the sequence's first
    // key, count it as a fresh start so "↑ ↑↑↓↓…" still lands.
    progress = key === SEQ[0] ? 1 : 0;
  }

  if (!window.Chassis) {
    window.Chassis = {};
  }
  window.Chassis.selfTest = { run };

  document.addEventListener('keydown', onKey);

  document.addEventListener('DOMContentLoaded', () => {
    try {
      if (new URLSearchParams(location.search).get('selftest') === '1') {
        setTimeout(run, 400); // let the faceplate settle a beat first
      }
    } catch (_) {
      // location may be absent in some headless/test harnesses; ignore.
    }
  });
})();
