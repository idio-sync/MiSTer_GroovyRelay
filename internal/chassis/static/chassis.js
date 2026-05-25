// Receiver chassis runtime. Phase 0 ships the state machinery, system
// time ticker, and ?dev=1 toggle. Later specs can attach feature code
// through window.Chassis without creating additional globals.
(() => {
  'use strict';

  // State registry. The body class is the source of truth.
  const State = {
    IDLE: 'idle',
    LIVE: 'live',
    current() {
      return document.body.classList.contains('live') ? State.LIVE : State.IDLE;
    },
    set(next) {
      if (next !== State.IDLE && next !== State.LIVE) {
        return;
      }
      document.body.classList.remove('idle', 'live');
      document.body.classList.add(next);
      animators.notify(next);
    },
  };

  // Animator registry. Phase 0 is empty; later specs register loops here.
  const animators = {
    items: [],
    register(animator) {
      if (!animator) {
        return;
      }
      this.items.push(animator);
      if (typeof animator.handleState === 'function') {
        animator.handleState(State.current());
      }
    },
    notify(state) {
      this.items.slice().forEach((animator) => {
        if (animator && typeof animator.handleState === 'function') {
          animator.handleState(state);
        }
      });
    },
  };

  function startSystemTimeTicker() {
    const el = document.querySelector('[data-system-time]');
    if (!el) {
      return;
    }

    const tick = () => {
      const now = new Date();
      el.textContent = `${pad(now.getHours())}:${pad(now.getMinutes())}`;
    };

    tick();

    const scheduleNextTick = () => {
      const msUntilNextMinute = 60_000 - (Date.now() % 60_000);
      setTimeout(() => {
        tick();
        scheduleNextTick();
      }, msUntilNextMinute);
    };
    scheduleNextTick();
  }

  function pad(value) {
    return value.toString().padStart(2, '0');
  }

  function installDevStateToggle() {
    const btn = document.createElement('button');
    btn.id = 'chassis-dev-state-toggle';
    btn.type = 'button';
    btn.style.cssText = [
      'position:fixed',
      'bottom:12px',
      'right:12px',
      'z-index:9999',
      'padding:8px 14px',
      'background:#3a3a3e',
      'color:#c0c0c4',
      'border:1px solid #0a0a0b',
      'border-radius:3px',
      'font:600 11px Inter,sans-serif',
      'letter-spacing:0.14em',
      'text-transform:uppercase',
      'cursor:pointer',
    ].join(';');

    const refreshLabel = () => {
      btn.textContent = `[dev] state: ${State.current()}`;
    };

    refreshLabel();
    btn.addEventListener('click', () => {
      State.set(State.current() === State.IDLE ? State.LIVE : State.IDLE);
      refreshLabel();
    });
    document.body.appendChild(btn);
  }

  function installSourceActions() {
    document.querySelectorAll('[data-source-action="aux-start"]').forEach((btn) => {
      btn.addEventListener('click', () => {
        if (btn.disabled || btn.getAttribute('aria-disabled') === 'true') {
          return;
        }
        const form = new URLSearchParams();
        const inputID = btn.getAttribute('data-input-id') || '';
        if (inputID) {
          form.set('input_id', inputID);
        }
        fetch('/receiver/aux/start', {
          method: 'POST',
          headers: {
            'Content-Type': 'application/x-www-form-urlencoded',
          },
          body: form,
        }).then((res) => {
          if (!res.ok) {
            console.warn('source: AUX start failed', res.status);
            return;
          }
          document.querySelectorAll('.source-cluster .hw-btn').forEach((sourceBtn) => {
            sourceBtn.classList.remove('active');
            sourceBtn.setAttribute('aria-checked', 'false');
          });
          btn.classList.add('active', 'lit');
          btn.setAttribute('aria-checked', 'true');
        }).catch((err) => {
          console.warn('source: AUX start failed', err);
        });
      });
    });
  }

  window.Chassis = { State, animators };

  document.addEventListener('DOMContentLoaded', () => {
    startSystemTimeTicker();
    installSourceActions();
    if (new URLSearchParams(location.search).get('dev') === '1') {
      installDevStateToggle();
    }
  });
})();
