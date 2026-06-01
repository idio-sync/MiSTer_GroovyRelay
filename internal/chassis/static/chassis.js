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

  function showInputError(text) {
    if (window.Chassis && window.Chassis.input && typeof window.Chassis.input.showError === 'function') {
      window.Chassis.input.showError(text);
    }
  }

  function historyBody() {
    const section = document.querySelector('.history-section');
    if (!section) {
      return null;
    }
    return Array.from(section.children).find((child) => child.tagName === 'DIV') || null;
  }

  function historyCell(className, text) {
    const cell = document.createElement('div');
    cell.className = className;
    cell.textContent = text || '';
    return cell;
  }

  function historyReplayControl(row) {
    const replayID = row.replayId || row.replayID || '';
    if (!replayID) {
      const placeholder = document.createElement('span');
      placeholder.className = 'history-replay-placeholder';
      placeholder.setAttribute('aria-hidden', 'true');
      return placeholder;
    }

    const button = document.createElement('button');
    button.className = 'history-replay-btn';
    button.type = 'button';
    button.dataset.historyReplayId = replayID;
    button.setAttribute('aria-label', `Recast ${row.title || 'history item'} from history`);
    button.title = 'Recast';
    button.textContent = '\u25b6';
    return button;
  }

  function renderHistory(payload) {
    const body = historyBody();
    if (!body) {
      return;
    }
    const rows = Array.isArray(payload && payload.rows) ? payload.rows : [];
    if (rows.length === 0) {
      const empty = document.createElement('div');
      empty.className = 'history-empty';
      empty.textContent = (payload && payload.emptyMessage) || 'No recent casts';
      body.replaceChildren(empty);
      return;
    }

    body.replaceChildren(...rows.map((row) => {
      const item = document.createElement('div');
      item.className = 'history-row';
      item.append(
        historyCell('artwork', row.artwork),
        historyCell('title', row.title),
        historyCell('source', row.source),
        historyCell('when', row.when),
        historyReplayControl(row),
      );
      return item;
    }));
  }

  function installHistoryLiveUpdates() {
    if (!window.Chassis || !window.Chassis.events || typeof window.Chassis.events.subscribe !== 'function') {
      return;
    }
    window.Chassis.events.subscribe('history', (ev) => {
      try {
        renderHistory(JSON.parse(ev.data));
      } catch (err) {
        console.warn('history: bad payload', ev.data, err);
      }
    });
  }

  function installHistoryReplayActions() {
    document.addEventListener('click', async (event) => {
      const target = event.target instanceof Element ? event.target : null;
      const btn = target ? target.closest('[data-history-replay-id]') : null;
      if (!btn || btn.disabled) {
        return;
      }
      const id = btn.getAttribute('data-history-replay-id') || '';
      if (!id) {
        return;
      }
      btn.disabled = true;
      try {
        const body = new URLSearchParams();
        body.set('id', id);
        const res = await fetch('/receiver/history/play', {
          method: 'POST',
          credentials: 'same-origin',
          headers: {
            'Content-Type': 'application/x-www-form-urlencoded',
          },
          body,
        });
        if (!res.ok) {
          const payload = await res.json().catch(() => ({}));
          showInputError(payload.chip || 'CAST FAILED');
        }
      } catch (_) {
        showInputError('CAST FAILED');
      } finally {
        btn.disabled = false;
      }
    });
  }

  window.Chassis = { State, animators };

  document.addEventListener('DOMContentLoaded', () => {
    startSystemTimeTicker();
    installSourceActions();
    installHistoryReplayActions();
    installHistoryLiveUpdates();
    if (new URLSearchParams(location.search).get('dev') === '1') {
      installDevStateToggle();
    }
  });
})();
