// setup.js — first-run setup mode client behavior. Loaded only when the
// page renders in setup mode (shell.html gates the <script> on .SetupMode).
// The server-side 409 gate is the source of truth; this is UX affordance.
(function () {
  'use strict';

  // Cast-trigger selectors disabled while setup mode is active. Kept here so
  // a single list governs the visual lockout; each cast script also guards
  // its POST via Chassis.setupBlocked() (see those files).
  //
  // Real selectors confirmed from templates:
  //   #cast-btn            — input-row.html: the main CAST button
  //   .preset              — preset-bank.html: individual preset slot buttons
  //   .ch-card             — catalog-grid.html / preset-bank.html: channel cards
  //   [data-history-replay-id] — history.html: replayable history rows (role=button)
  //   [data-source-action="aux-start"] — source-cluster.html: AUX source buttons
  //   [data-localfiles-file] — rendered by input-cast.js into #localfiles-entries
  var CAST_SELECTORS = [
    '#cast-btn',
    '.preset:not(.empty)',
    '.ch-card',
    '[data-history-replay-id]',
    '[data-source-action="aux-start"]',
    '[data-localfiles-file]',
  ];

  function inSetup() {
    return document.body.classList.contains('setup');
  }

  function disableCastControls() {
    if (!inSetup()) return;
    CAST_SELECTORS.forEach(function (sel) {
      document.querySelectorAll(sel).forEach(function (el) {
        el.classList.add('setup-disabled');
        el.setAttribute('aria-disabled', 'true');
      });
    });
  }

  function applyStatus(st) {
    var steps = document.querySelectorAll('.setup-checklist .setup-step');
    if (steps[0]) steps[0].classList.toggle('done', !!st.hostSet);
    if (steps[1]) steps[1].classList.toggle('done', !!st.sourceEnabled);
    var finish = document.getElementById('setup-finish');
    if (finish) {
      finish.disabled = !st.complete;
      if (st.complete) finish.removeAttribute('aria-disabled');
      else finish.setAttribute('aria-disabled', 'true');
    }
  }

  function refreshStatus() {
    fetch('/receiver/setup/status', { credentials: 'same-origin' })
      .then(function (r) { return r.ok ? r.json() : null; })
      .then(function (st) { if (st) applyStatus(st); })
      .catch(function () {});
  }

  function finish() {
    fetch('/receiver/setup/finish', { method: 'POST', credentials: 'same-origin' })
      .then(function (r) {
        if (r.status === 200) {
          window.location.assign('/receiver');
        } else {
          // Criteria not yet met server-side; refresh the checklist.
          refreshStatus();
        }
      })
      .catch(function () {});
  }

  function init() {
    if (!inSetup()) return;
    disableCastControls();
    var btn = document.getElementById('setup-finish');
    if (btn) btn.addEventListener('click', finish);
    // Re-check status whenever the drawer reports a relevant save.
    document.addEventListener('chassis:settings-saved', refreshStatus);
    refreshStatus();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }

  if (window.Chassis) {
    window.Chassis.setup = { refreshStatus: refreshStatus, inSetup: inSetup };
  }
})();
