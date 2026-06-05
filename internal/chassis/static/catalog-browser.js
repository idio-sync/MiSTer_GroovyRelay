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
  let lastPresetsPayload = null;
  let lastTunedRef = ['', ''];

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
    if (window.Chassis && Chassis.setupBlocked()) return;
    const provider = card.dataset.provider;
    const channel = card.dataset.channel;
    if (!provider || !channel) return;
    try {
      const body = new URLSearchParams({ provider, channel });
      const resp = await fetch('/ui/streams/cast', {
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
      const resp = await fetch('/ui/preset/star', {
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
    const parts = ref.slice('streams:'.length).split(':');
    if (parts.length < 2) return [null, null];
    if (parts[0] === 'user' && parts.length >= 3) {
      return ['user:' + parts[1], parts[2]];
    }
    return [parts[0], parts[1]];
  }

  function elem(tag, className) {
    const el = document.createElement(tag);
    if (className) el.className = className;
    return el;
  }

  function providerChannelCount(provider) {
    let count = 0;
    (provider.groups || []).forEach((group) => {
      count += (group.channels || []).length;
    });
    return count;
  }

  function isUserProviderID(providerID) {
    return String(providerID || '').indexOf('user:') === 0;
  }

  function buildTab(provider) {
    const wrap = elem('div', 'catalog-provider-wrap');
    const active = provider.id === activeProviderID;
    const tab = elem('button', 'catalog-provider-tab' + (active ? ' active' : ''));
    tab.type = 'button';
    tab.dataset.provider = provider.id || '';

    const badge = elem('span', 'ic ' + (provider.badgeClass || ''));
    badge.textContent = provider.badgeLabel || '';
    tab.appendChild(badge);
    tab.appendChild(document.createTextNode(' ' + (provider.displayName || '') + ' '));

    const count = elem('span', 'ch-count');
    count.textContent = String(providerChannelCount(provider));
    tab.appendChild(count);
    wrap.appendChild(tab);

    if (isUserProviderID(provider.id)) {
      const pencil = elem('button', 'cf-pencil');
      pencil.type = 'button';
      pencil.dataset.editProvider = provider.id;
      pencil.title = 'Edit provider';
      pencil.setAttribute('aria-label', 'Edit ' + (provider.displayName || provider.id));
      pencil.textContent = '✎';
      wrap.appendChild(pencil);
    }
    return wrap;
  }

  function buildNewTab() {
    const tab = elem('button', 'catalog-provider-tab cf-new');
    tab.type = 'button';
    tab.id = 'catalog-provider-new';
    tab.title = 'New provider';
    const icon = elem('span', 'ic');
    icon.textContent = '+';
    tab.appendChild(icon);
    tab.appendChild(document.createTextNode(' New'));
    return tab;
  }

  function buildCard(provider, channel, index) {
    const card = elem('div', 'ch-card' + (channel.live ? ' live' : ''));
    card.setAttribute('role', 'button');
    card.setAttribute('tabindex', '0');
    card.dataset.provider = provider.id || '';
    card.dataset.channel = channel.id || '';
    if (card.style && typeof card.style.setProperty === 'function') {
      card.style.setProperty('--i', String(index));
    }

    const star = elem('button', 'star');
    star.type = 'button';
    star.title = 'Save to preset';
    star.textContent = '☆';
    const name = elem('div', 'name');
    name.textContent = channel.name || '';
    const meta = elem('div', 'meta');
    const id = elem('span');
    id.textContent = String(channel.id || '').toUpperCase();
    const mode = elem('span', 'mode');
    mode.textContent = channel.playMode || '';
    meta.appendChild(id);
    meta.appendChild(mode);
    card.appendChild(star);
    card.appendChild(name);
    card.appendChild(meta);
    return card;
  }

  function buildTreeNodes(provider, providerIsFirst) {
    const nodes = [];
    (provider.groups || []).forEach((group, index) => {
      const rail = elem('button', 'catalog-rail-group' + (providerIsFirst && index === 0 ? ' active' : ''));
      rail.type = 'button';
      rail.dataset.group = group.id || '';
      if (rail.style && typeof rail.style.setProperty === 'function') {
        rail.style.setProperty('--i', String(index));
      }
      rail.appendChild(document.createTextNode(group.name || ''));
      const count = elem('span', 'count');
      count.textContent = String((group.channels || []).length);
      rail.appendChild(count);
      nodes.push(rail);
    });
    (provider.groups || []).forEach((group, groupIndex) => {
      const grid = elem('div', 'catalog-tree-grid');
      grid.dataset.group = group.id || '';
      grid.hidden = !(providerIsFirst && groupIndex === 0);
      (group.channels || []).forEach((channel, channelIndex) => {
        grid.appendChild(buildCard(provider, channel, channelIndex));
      });
      nodes.push(grid);
    });
    return nodes;
  }

  function treeHostParent() {
    const existing = document.querySelector('template[id^="catalog-tree-"]');
    return (existing && existing.parentNode) || (drawer && drawer.parentNode) || document.body;
  }

  function rebuildCatalog(payload) {
    if (!payload || !Array.isArray(payload.providers)) return;
    const providers = payload.providers;
    const providerIDs = new Set(providers.map((provider) => provider.id || ''));
    if (!providerIDs.has(activeProviderID)) {
      activeProviderID = providers.length > 0 ? providers[0].id || '' : '';
    }

    const tabNodes = [];
    if (indicator) tabNodes.push(indicator);
    providers.forEach((provider) => {
      tabNodes.push(buildTab(provider));
    });
    tabNodes.push(buildNewTab());
    tabsContainer.replaceChildren(...tabNodes);

    const host = treeHostParent();
    providers.forEach((provider, index) => {
      let tpl = getTreeTemplate(provider.id);
      if (!tpl) {
        tpl = document.createElement('template');
        tpl.id = 'catalog-tree-' + provider.id;
        host.appendChild(tpl);
      }
      const tree = elem('div', 'catalog-tree-payload');
      tree.dataset.provider = provider.id || '';
      buildTreeNodes(provider, index === 0).forEach((node) => tree.appendChild(node));
      tpl.content.replaceChildren(tree);
    });

    document.querySelectorAll('template[id^="catalog-tree-"]').forEach((tpl) => {
      const providerID = tpl.id.slice('catalog-tree-'.length);
      if (!providerIDs.has(providerID)) tpl.remove();
    });

    if (lastPresetsPayload) applyStars(lastPresetsPayload);
    applyTuned(lastTunedRef[0], lastTunedRef[1]);
    if (isOpen && activeProviderID) {
      const nextProvider = activeProviderID;
      activeProviderID = '';
      switchProvider(nextProvider);
      notifyCatalogGridChanged();
    }
    positionIndicator();
  }

  window.Chassis.events.subscribe('transport', (ev) => {
    let data = {};
    try { data = JSON.parse(ev.data); } catch (_) { return; }
    const [provider, channel] = parseAdapterRef(data.adapterRef);
    lastTunedRef = [provider || '', channel || ''];
    applyTuned(provider || '', channel || '');
  });

  window.Chassis.events.subscribe('presets', (ev) => {
    let data = {};
    try { data = JSON.parse(ev.data); } catch (_) { return; }
    lastPresetsPayload = data;
    applyStars(data);
  });

  window.Chassis.events.subscribe('catalog', (ev) => {
    let data = {};
    try { data = JSON.parse(ev.data); } catch (_) { return; }
    rebuildCatalog(data);
  });

  window.Chassis.catalogBrowser = {
    rebuild: rebuildCatalog,
    _buildTab: buildTab,
    _buildTreeNodes: buildTreeNodes,
  };

  // Capture the server-rendered closed-state mode label so we can
  // restore it on close (open-state form is computed in setModeLabel).
  if (modeLabel) modeLabel.dataset.closedText = modeLabel.textContent;
})();
