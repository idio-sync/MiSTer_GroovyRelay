(function () {
  'use strict';
  // Search input element: #search-input
  const input = document.getElementById('search-input');
  const scope = document.getElementById('search-scope');
  const field = document.getElementById('search-field');
  if (!input) return;

  let query = '';

  function matchesPreset(el, q) {
    if (!q) return true;
    const name = (el.querySelector('.name') || {}).textContent || '';
    const badge = (el.querySelector('.badge') || {}).textContent || '';
    const channel = el.dataset.channel || '';
    return (name + ' ' + badge + ' ' + channel).toLowerCase().indexOf(q) >= 0;
  }

  function matchesCard(el, q) {
    if (!q) return true;
    const name = (el.querySelector('.name') || {}).textContent || '';
    const channel = el.dataset.channel || '';
    const provider = el.dataset.provider || '';
    return (name + ' ' + channel + ' ' + provider).toLowerCase().indexOf(q) >= 0;
  }

  function applyFilter() {
    const q = query.toLowerCase().trim();
    if (field) field.classList.toggle('has-value', q !== '');
    let presetMatches = 0;
    let catalogMatches = 0;
    document.querySelectorAll('.preset-bank .preset').forEach((el) => {
      const ok = matchesPreset(el, q);
      el.classList.toggle('filter-miss', !ok);
      if (ok && !el.classList.contains('empty')) presetMatches += 1;
    });
    document.querySelectorAll('.catalog-grid .ch-card').forEach((el) => {
      const ok = matchesCard(el, q);
      el.classList.toggle('filter-miss', !ok);
      if (ok) catalogMatches += 1;
    });
    if (scope) {
      scope.textContent = q === '' ? ' ' : (`presets: ${presetMatches} · catalog: ${catalogMatches}`);
    }
  }

  input.addEventListener('input', () => {
    query = input.value || '';
    applyFilter();
  });
  input.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') {
      input.value = '';
      query = '';
      applyFilter();
    }
  });

  // Re-apply when presets change (filtered-out slots may have become
  // empty/filled and need their .filter-miss class re-derived).
  if (window.Chassis && window.Chassis.events && typeof window.Chassis.events.subscribe === 'function') {
    window.Chassis.events.subscribe('presets', () => applyFilter());
  }
  // Re-apply when preset DOM or catalog-grid DOM changes.
  document.addEventListener('chassis:preset-rerendered', () => applyFilter());
  document.addEventListener('chassis:catalog-grid-changed', () => applyFilter());

  // Initial paint.
  applyFilter();
})();
