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
        if (Chassis.setupBlocked()) return;
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
    const cell = document.createElement('span');
    cell.className = className;
    cell.textContent = text || '';
    return cell;
  }

  function historyArtwork(row) {
    const cell = document.createElement('span');
    cell.className = 'artwork';
    cell.setAttribute('data-source', (row && row.sourceId) || '');
    cell.setAttribute('aria-hidden', 'true');
    cell.textContent = (row && row.artwork) || '';
    return cell;
  }

  function historyWhen(row) {
    if (row && row.whenIso) {
      const time = document.createElement('time');
      time.className = 'when';
      time.setAttribute('datetime', row.whenIso);
      if (row.whenExact) {
        time.title = row.whenExact;
      }
      time.textContent = row.when || '';
      return time;
    }
    return historyCell('when', row && row.when);
  }

  function historyCue(replayID) {
    const cue = document.createElement('span');
    cue.setAttribute('aria-hidden', 'true');
    if (replayID) {
      cue.className = 'history-replay-cue';
      cue.textContent = '\u25b8';
    } else {
      cue.className = 'history-replay-placeholder';
    }
    return cue;
  }

  // A replayable row is the recast control itself: a role="button" list item
  // carrying the replay id + aria-label, with the glyph as a visual cue only.
  // Read-only rows render as plain, non-actionable list items.
  function buildHistoryRow(row) {
    const item = document.createElement('li');
    item.className = 'history-row';
    const replayID = (row && (row.replayId || row.replayID)) || '';
    if (replayID) {
      item.setAttribute('role', 'button');
      item.setAttribute('tabindex', '0');
      item.dataset.historyReplayId = replayID;
      item.setAttribute('aria-label', `Recast ${(row && row.title) || 'history item'} from history`);
    }
    item.append(
      historyArtwork(row),
      historyCell('title', row && row.title),
      historyCell('source', row && row.source),
      historyWhen(row),
      historyCue(replayID),
    );
    return item;
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

    const list = document.createElement('ul');
    list.className = 'history-list';
    list.setAttribute('role', 'list');
    list.append(...rows.map(buildHistoryRow));
    body.replaceChildren(list);
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

  function flashHistoryRecast(row) {
    if (!row || !row.classList) {
      return;
    }
    row.classList.remove('recasting');
    // Force a reflow so re-adding the class restarts the animation when the
    // same row is recast twice in succession.
    void row.offsetWidth;
    row.classList.add('recasting');
    setTimeout(() => row.classList.remove('recasting'), 950);
  }

  async function triggerHistoryRecast(row) {
    if (!row) {
      return;
    }
    if (Chassis.setupBlocked()) return;
    const id = row.getAttribute('data-history-replay-id') || '';
    if (!id || row.getAttribute('aria-busy') === 'true') {
      return;
    }
    row.setAttribute('aria-busy', 'true');
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
        return;
      }
      flashHistoryRecast(row);
    } catch (_) {
      showInputError('CAST FAILED');
    } finally {
      row.removeAttribute('aria-busy');
    }
  }

  function historyRowFromEvent(event) {
    const target = event.target instanceof Element ? event.target : null;
    return target ? target.closest('[data-history-replay-id]') : null;
  }

  function installHistoryReplayActions() {
    document.addEventListener('click', (event) => {
      const row = historyRowFromEvent(event);
      if (row) {
        triggerHistoryRecast(row);
      }
    });
    // The row is role="button" + tabindex=0, so Enter/Space must recast it.
    document.addEventListener('keydown', (event) => {
      if (event.key !== 'Enter' && event.key !== ' ' && event.key !== 'Spacebar') {
        return;
      }
      const row = historyRowFromEvent(event);
      if (!row) {
        return;
      }
      event.preventDefault();
      triggerHistoryRecast(row);
    });
  }

  window.Chassis = { State, animators };

  // setupBlocked returns true while first-run setup mode is active. Cast
  // scripts call this before POSTing; the server-side 409 is authoritative.
  Chassis.setupBlocked = function () {
    return document.body.classList.contains('setup');
  };

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
