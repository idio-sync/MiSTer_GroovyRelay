// Receiver chassis power-on ritual. Delight spec.
//
// Registers an animator on window.Chassis.animators that fires a brief,
// period-correct "the receiver wakes up" sequence on the idle->live
// transition: a VFD filament warm-up bloom, an all-segments self-test
// flash (reusing the .seg-ghost layer already in the DOM), then the real
// .seg-text resolving in. Driven entirely by a transient `warming` body
// class; chassis.css owns the keyframes and no-ops them under
// prefers-reduced-motion. No new globals — this hangs off window.Chassis,
// matching the file-per-spec convention of vfd-live.js / source-cluster.js.
//
// Also prints a one-time hardware boot banner to the dev console — a wink
// for anyone curious enough to open devtools.
(() => {
  'use strict';

  if (!window.Chassis || !window.Chassis.animators) {
    console.warn('power-on: window.Chassis.animators missing; chassis.js failed to load?');
    return;
  }

  const WARM_CLASS = 'warming';
  const COOL_CLASS = 'cooling';
  // Each must outlast its longest keyframe in chassis.css (warm bloom 760ms,
  // power-down 580ms) so the class is not yanked mid-animation. Standby is the
  // faster of the pair, per the "exit ~75% of entrance" rule.
  const WARM_MS = 950;
  const COOL_MS = 640;

  let ritualTimer = null;
  // The animator registry calls handleState once at registration with the
  // current state. That first call is the initial page render, not a
  // transition — skip it so a page that loads already-live (SSE reconnect, a
  // dev refresh mid-cast) does not replay a ritual. Only a genuine state
  // change wakes or powers down the faceplate.
  let primed = false;

  // runRitual flips on a transient body class (warming / cooling) that drives
  // the VFD power-on / power-down keyframes, then clears it. The opposite
  // class is removed first and a reflow is forced so the animation restarts
  // even on a rapid live<->idle bounce (re-adding a class without a reflow
  // would not retrigger it).
  function runRitual(addClass, otherClass, ms) {
    const body = document.body;
    if (!body) {
      return;
    }
    body.classList.remove(addClass, otherClass);
    void body.offsetWidth;
    body.classList.add(addClass);
    if (ritualTimer) {
      clearTimeout(ritualTimer);
    }
    ritualTimer = setTimeout(() => {
      body.classList.remove(addClass);
      ritualTimer = null;
    }, ms);
  }

  // --- Cold start + POWER key ------------------------------------------
  // A real receiver comes out of standby with an amber lamp, a relay click,
  // then the display lighting. The first load in a tab does the same:
  // `powering-up` holds the faceplate dark with PWR amber, then the relay
  // "closes" into the normal warm bloom. Once per tab session (reloads and
  // SSE reconnects do not replay it); skipped under reduced motion or when
  // session storage is unavailable.
  //
  // POWER toggles a client-side panel standby: displays and lamps go dark
  // and PWR shows amber, but the bridge and any cast keep running. POWER
  // again, or any other key or click, wakes the panel.
  const COLD_CLASS = 'powering-up';
  const STANDBY_CLASS = 'panel-standby';
  const COLD_KEY = 'chassis.poweredOn';
  const RELAY_MS = 600;

  function reducedMotion() {
    return !!(window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches);
  }

  function setPowerLamp(on) {
    const led = document.querySelector('[data-power-led]');
    if (!led) return;
    const label = on ? 'Power on' : 'Power: standby';
    led.setAttribute('aria-label', label);
    led.setAttribute('title', label);
  }

  function firstLoadThisSession() {
    try {
      const store = window.sessionStorage;
      if (!store || store.getItem(COLD_KEY)) return false;
      store.setItem(COLD_KEY, '1');
      return true;
    } catch (err) {
      return false;
    }
  }

  function coldStart() {
    const body = document.body;
    if (!body) return;
    // shell.html's pre-paint script normally marks the body before first
    // paint (so the lit panel never flashes); fall back to marking it here.
    const pending = body.classList.contains(COLD_CLASS)
      || (!reducedMotion() && firstLoadThisSession());
    if (!pending) return;
    body.classList.add(COLD_CLASS);
    setPowerLamp(false);
    setTimeout(() => {
      body.classList.remove(COLD_CLASS);
      setPowerLamp(true);
      runRitual(WARM_CLASS, COOL_CLASS, WARM_MS);
    }, RELAY_MS);
  }

  function bindPowerKey() {
    const btn = document.querySelector('[data-power-btn]');
    const body = document.body;
    if (!btn || !body) return;

    const setStandby = (standby) => {
      body.classList.toggle(STANDBY_CLASS, standby);
      btn.setAttribute('aria-pressed', standby ? 'false' : 'true');
      btn.setAttribute('title', standby
        ? 'Panel standby (the bridge keeps running): press to wake'
        : 'Panel power: press for standby (the bridge keeps running)');
      setPowerLamp(!standby);
      // Going dark is a CSS filter fade (the power-down keyframes end at
      // full brightness, which would flash before standby holds). Waking
      // gets the full filament bloom.
      if (!standby) {
        runRitual(WARM_CLASS, COOL_CLASS, WARM_MS);
      }
    };

    btn.setAttribute('aria-pressed', 'true');
    btn.setAttribute('title', 'Panel power: press for standby (the bridge keeps running)');
    btn.addEventListener('click', () => {
      setStandby(!body.classList.contains(STANDBY_CLASS));
    });

    // Any other key or click wakes the panel, like touching a front-panel
    // key on a unit whose display is off. POWER's own click owns the toggle.
    const wake = (ev) => {
      if (!body.classList.contains(STANDBY_CLASS)) return;
      if (ev && ev.target && btn.contains(ev.target)) return;
      setStandby(false);
    };
    document.addEventListener('pointerdown', wake, true);
    document.addEventListener('keydown', wake, true);
  }

  window.Chassis.animators.register({
    handleState(state) {
      if (!primed) {
        primed = true;
        return;
      }
      if (state === window.Chassis.State.LIVE) {
        runRitual(WARM_CLASS, COOL_CLASS, WARM_MS); // wake: filament bloom
      } else if (state === window.Chassis.State.IDLE) {
        runRitual(COOL_CLASS, WARM_CLASS, COOL_MS); // standby: fade to dark
      }
    },
  });

  function bootBanner() {
    if (!window.console || typeof console.log !== 'function') {
      return;
    }
    const meta = document.querySelector('meta[name="chassis-generation"]');
    const gen = meta ? (meta.getAttribute('content') || '?') : '?';
    console.log(
      '%c GROOVY RELAY %c RECEIVER ONLINE ',
      'background:#0b1a18;color:#28e0c8;font:700 11px monospace;padding:2px 6px;border-radius:2px 0 0 2px;',
      'background:#28e0c8;color:#0b1a18;font:700 11px monospace;padding:2px 6px;border-radius:0 2px 2px 0;',
    );
    console.log(
      '%cself-test: ↑↑↓↓←→←→ B A   (or load with ?selftest=1)   — gen ' + gen,
      'color:#5a9a90;font:12px monospace;',
    );
  }

  bootBanner();
  bindPowerKey();
  coldStart();
})();
