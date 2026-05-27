(function () {
  'use strict';
  if (!window.Chassis || !window.Chassis.events || typeof window.Chassis.events.subscribe !== 'function') {
    return;
  }
  const drawer = document.getElementById('catalog-drawer');
  const browseBtn = document.getElementById('browse-toggle');
  const railHost = document.getElementById('catalog-rail');
  const gridHost = document.getElementById('catalog-grid');
  if (!drawer || !browseBtn || !railHost || !gridHost) return;

  const tabsContainer = drawer.querySelector('.catalog-provider-tabs');
  const indicator = document.getElementById('catalog-tab-indicator');
  const modeLabel = document.getElementById('preset-mode-label');
  const browseClosedText = browseBtn.textContent.trim();

  let activeProviderID = (tabsContainer.querySelector('.catalog-provider-tab.active') || {}).dataset?.provider || '';
  let activeGroupID = (railHost.querySelector('.catalog-rail-group.active') || {}).dataset?.group || '';
  let isOpen = false;

  function getTreeTemplate(providerID) {
    return document.getElementById('catalog-tree-' + providerID);
  }

  function cloneRail(providerID) {
    const tpl = getTreeTemplate(providerID);
    if (!tpl) return [];
    return Array.from(tpl.content.querySelectorAll('button.catalog-rail-group'));
  }

  function cloneGrid(providerID, groupID) {
    const tpl = getTreeTemplate(providerID);
    if (!tpl) return null;
    const grid = tpl.content.querySelector('.catalog-tree-grid[data-group="' + cssEscape(groupID) + '"]');
    return grid ? grid.cloneNode(true) : null;
  }

  function cssEscape(s) {
    if (typeof CSS !== 'undefined' && typeof CSS.escape === 'function') return CSS.escape(s);
    return String(s).replace(/[^a-zA-Z0-9_-]/g, '\\$&');
  }

  function pad2(n) {
    return n < 10 ? '0' + n : '' + n;
  }

  function notifyCatalogGridChanged() {
    document.dispatchEvent(new CustomEvent('chassis:catalog-grid-changed'));
  }

  function setBrowseLabel() {
    browseBtn.textContent = isOpen ? '◂ Back to presets' : browseClosedText;
    browseBtn.setAttribute('aria-expanded', isOpen ? 'true' : 'false');
  }

  function setModeLabel() {
    if (!modeLabel) return;
    if (!isOpen) {
      // Server-rendered closed-state text is the source of truth.
      // Re-apply it on close.
      modeLabel.textContent = modeLabel.dataset.closedText || modeLabel.textContent;
      return;
    }
    const provName = (tabsContainer.querySelector(`.catalog-provider-tab[data-provider="${cssEscape(activeProviderID)}"]`) || {}).textContent || '';
    const groupBtn = railHost.querySelector('.catalog-rail-group.active');
    const groupName = groupBtn ? groupBtn.firstChild.textContent.trim() : '';
    const channelCount = gridHost.querySelectorAll('.ch-card').length;
    modeLabel.textContent = `Catalog · ${provName.trim()} · ${groupName} · ${channelCount} channels`;
  }

  function positionIndicator() {
    if (!indicator) return;
    const active = tabsContainer.querySelector('.catalog-provider-tab.active');
    if (!active) return;
    const tabsRect = tabsContainer.getBoundingClientRect();
    const r = active.getBoundingClientRect();
    indicator.style.transform = `translateX(${r.left - tabsRect.left}px)`;
    indicator.style.width = r.width + 'px';
  }

  function toggleBrowse() {
    isOpen = !isOpen;
    document.body.classList.toggle('browse-open', isOpen);
    drawer.setAttribute('aria-hidden', isOpen ? 'false' : 'true');
    if (isOpen) {
      document.body.classList.add('catalog-scanning');
      setTimeout(() => document.body.classList.remove('catalog-scanning'), 600);
      positionIndicator();
    }
    setBrowseLabel();
    setModeLabel();
    if (isOpen) notifyCatalogGridChanged();
  }

  function switchProvider(providerID) {
    if (!providerID || providerID === activeProviderID) return;
    activeProviderID = providerID;
    tabsContainer.querySelectorAll('.catalog-provider-tab').forEach((b) => {
      b.classList.toggle('active', b.dataset.provider === providerID);
    });
    // Repopulate rail from the hidden template.
    railHost.replaceChildren();
    const railButtons = cloneRail(providerID);
    railButtons.forEach((b) => railHost.appendChild(b.cloneNode(true)));
    const firstRailBtn = railHost.querySelector('.catalog-rail-group');
    activeGroupID = firstRailBtn ? firstRailBtn.dataset.group : '';
    railHost.querySelectorAll('.catalog-rail-group').forEach((b) => {
      b.classList.toggle('active', b.dataset.group === activeGroupID);
    });
    switchGrid(activeGroupID);
    positionIndicator();
    setModeLabel();
  }

  function switchGrid(groupID) {
    if (!groupID) return;
    activeGroupID = groupID;
    railHost.querySelectorAll('.catalog-rail-group').forEach((b) => {
      b.classList.toggle('active', b.dataset.group === groupID);
    });
    gridHost.replaceChildren();
    const cloned = cloneGrid(activeProviderID, groupID);
    if (cloned) {
      while (cloned.firstChild) gridHost.appendChild(cloned.firstChild);
    }
    setModeLabel();
    notifyCatalogGridChanged();
  }

  browseBtn.addEventListener('click', toggleBrowse);

  tabsContainer.addEventListener('click', (e) => {
    const tab = e.target.closest('.catalog-provider-tab');
    if (!tab) return;
    switchProvider(tab.dataset.provider);
  });

  railHost.addEventListener('click', (e) => {
    const btn = e.target.closest('.catalog-rail-group');
    if (!btn) return;
    switchGrid(btn.dataset.group);
  });

  gridHost.addEventListener('click', (e) => {
    const star = e.target.closest('.star');
    if (star) {
      e.stopPropagation();
      handleStarClick(star);
      return;
    }
    const card = e.target.closest('.ch-card');
    if (!card) return;
    handleChannelCast(card);
  });

  gridHost.addEventListener('keydown', (e) => {
    if (e.key !== 'Enter' && e.key !== ' ') return;
    const target = e.target;
    if (target.classList && target.classList.contains('star')) {
      e.preventDefault();
      e.stopPropagation();
      handleStarClick(target);
      return;
    }
    const card = target.closest && target.closest('.ch-card');
    if (card) {
      e.preventDefault();
      handleChannelCast(card);
    }
  });

  function reportChip(chip) {
    if (window.Chassis && window.Chassis.input && typeof window.Chassis.input.showError === 'function') {
      window.Chassis.input.showError(chip || 'CAST FAILED');
    }
  }

  async function handleChannelCast(card) {
    const provider = card.dataset.provider;
    const channel = card.dataset.channel;
    if (!provider || !channel) return;
    try {
      const body = new URLSearchParams({ provider, channel });
      const resp = await fetch('/receiver/streams/cast', {
        method: 'POST',
        credentials: 'same-origin',
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body: body.toString(),
      });
      const json = await resp.json().catch(() => ({ ok: false, chip: 'CAST FAILED' }));
      if (!json.ok) reportChip(json.chip);
    } catch (_) {
      reportChip('CAST FAILED');
    }
  }

  async function handleStarClick(star) {
    const card = star.closest('.ch-card');
    if (!card) return;
    const provider = card.dataset.provider;
    const channel = card.dataset.channel;
    if (!provider || !channel) return;
    const wasStarred = card.classList.contains('starred');
    const desired = !wasStarred;
    star.classList.add('pending');
    try {
      const body = new URLSearchParams({ provider, channel, starred: desired ? 'true' : 'false' });
      const resp = await fetch('/receiver/preset/star', {
        method: 'POST',
        credentials: 'same-origin',
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body: body.toString(),
      });
      const json = await resp.json().catch(() => ({ ok: false, chip: 'CAST FAILED' }));
      if (!json.ok) {
        reportChip(json.chip);
        return;
      }
      // Visual flip happens on the next `presets` SSE tick, which is
      // imminent because the chassis calls refreshSnapshotNow().
    } catch (_) {
      reportChip('CAST FAILED');
    } finally {
      star.classList.remove('pending');
    }
  }

  function applyTuned(providerId, channelId) {
    document.querySelectorAll('.ch-card').forEach((el) => {
      el.classList.toggle('tuned',
        el.dataset.provider === providerId && el.dataset.channel === channelId &&
        providerId !== '' && channelId !== '');
    });
    // Same for the hidden trees so a tab switch shows the right .tuned card.
    document.querySelectorAll('template[id^="catalog-tree-"]').forEach((tpl) => {
      tpl.content.querySelectorAll('.ch-card').forEach((el) => {
        el.classList.toggle('tuned',
          el.dataset.provider === providerId && el.dataset.channel === channelId &&
          providerId !== '' && channelId !== '');
      });
    });
  }

  function applyStars(payload) {
    if (!payload || !Array.isArray(payload.slots)) return;
    const membership = new Map();
    payload.slots.forEach((s) => {
      if (s.provider && s.channel) membership.set(s.provider + ':' + s.channel, s.slot);
    });

    function updateCard(el) {
      const key = (el.dataset.provider || '') + ':' + (el.dataset.channel || '');
      const slot = membership.get(key) || 0;
      el.classList.toggle('starred', slot > 0);
      const star = el.querySelector('.star');
      if (star) {
        star.textContent = slot > 0 ? '★' : '☆';
        star.title = slot > 0 ? 'In preset ' + pad2(slot) : 'Save to preset';
      }
    }

    document.querySelectorAll('.ch-card').forEach(updateCard);
    document.querySelectorAll('template[id^="catalog-tree-"]').forEach((tpl) => {
      tpl.content.querySelectorAll('.ch-card').forEach(updateCard);
    });
  }

  function parseAdapterRef(ref) {
    if (!ref || typeof ref !== 'string' || !ref.startsWith('streams:')) return [null, null];
    const parts = ref.split(':');
    if (parts.length < 3) return [null, null];
    return [parts[1], parts[2]];
  }

  window.Chassis.events.subscribe('transport', (ev) => {
    let data = {};
    try { data = JSON.parse(ev.data); } catch (_) { return; }
    const [provider, channel] = parseAdapterRef(data.adapterRef);
    applyTuned(provider || '', channel || '');
  });

  window.Chassis.events.subscribe('presets', (ev) => {
    let data = {};
    try { data = JSON.parse(ev.data); } catch (_) { return; }
    applyStars(data);
  });

  // Capture the server-rendered closed-state mode label so we can
  // restore it on close (open-state form is computed in setModeLabel).
  if (modeLabel) modeLabel.dataset.closedText = modeLabel.textContent;
})();
