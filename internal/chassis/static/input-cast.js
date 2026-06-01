(function () {
  const panel = document.querySelector('.input-section');
  if (!panel) return;
  const input = document.getElementById('paste-input');
  const clearBtn = document.getElementById('paste-clear');
  const chip = document.getElementById('paste-chip');
  const castBtn = document.getElementById('cast-btn');
  const uploadBtn = document.getElementById('upload-btn');
  const fileInput = document.getElementById('torrent-file-input');
  if (!input || !clearBtn || !chip || !castBtn || !uploadBtn || !fileInput) return;

  let queuedFile = null;
  let chipTimer = 0;
  let chipInError = false;

  function detectKind(raw) {
    if (!raw) return '';
    const trimmed = raw.trim();
    try {
      const u = new URL(trimmed);
      const scheme = u.protocol.replace(/:$/, '').toLowerCase();
      if (scheme === 'magnet') return 'magnet';
      if (scheme === 'http' || scheme === 'https') return 'url';
    } catch (_) {
      return '';
    }
    return '';
  }

  function chipText(kind) {
    if (queuedFile) return 'TORRENT FILE · ' + truncateBasename(queuedFile.name);
    if (kind === 'url') return 'URL';
    if (kind === 'magnet') return 'MAGNET';
    return 'PASTE URL';
  }

  function truncateBasename(name) {
    if (name.length <= 24) return name;
    return name.slice(0, 21) + '…';
  }

  function setChipKind(kind) {
    chip.dataset.chipKind = kind || '';
    chip.textContent = chipText(kind);
  }

  function setErrorChip(text) {
    chipInError = true;
    chip.dataset.chipKind = 'err';
    chip.textContent = text;
    clearTimeout(chipTimer);
    chipTimer = setTimeout(() => {
      chipInError = false;
      const k = queuedFile ? 'file' : detectKind(input.value);
      setChipKind(k);
    }, 4000);
  }

  function clearErrorChip() {
    if (!chipInError) return;
    chipInError = false;
    clearTimeout(chipTimer);
  }

  function updateState() {
    const kind = queuedFile ? 'file' : detectKind(input.value);
    if (!chipInError) setChipKind(kind);
    const canCast = !!queuedFile || kind === 'url' || kind === 'magnet';
    castBtn.disabled = !canCast;
    castBtn.classList.toggle('disabled', !canCast);
    clearBtn.style.display = (input.value || queuedFile) ? '' : 'none';
  }

  let debounceTimer = 0;
  input.addEventListener('input', () => {
    clearErrorChip();
    clearTimeout(debounceTimer);
    debounceTimer = setTimeout(updateState, 120);
  });

  clearBtn.addEventListener('click', () => {
    clearErrorChip();
    input.value = '';
    queuedFile = null;
    fileInput.value = '';
    updateState();
  });

  uploadBtn.addEventListener('click', () => fileInput.click());

  fileInput.addEventListener('change', () => {
    clearErrorChip();
    queuedFile = fileInput.files && fileInput.files[0] ? fileInput.files[0] : null;
    if (queuedFile) input.value = '';
    updateState();
  });

  castBtn.addEventListener('click', async () => {
    if (castBtn.disabled) return;
    panel.dataset.castState = 'submitting';
    castBtn.disabled = true;
    try {
      const res = await submit();
      if (!res.ok) {
        setErrorChip(res.chip || 'CAST FAILED');
      } else {
        input.value = '';
        queuedFile = null;
        fileInput.value = '';
      }
    } catch (_) {
      setErrorChip('CAST FAILED');
    } finally {
      panel.dataset.castState = 'idle';
      updateState();
    }
  });

  async function submit() {
    if (queuedFile) {
      const fd = new FormData();
      fd.append('kind', 'file');
      fd.append('torrent_file', queuedFile, queuedFile.name);
      return parse(await fetch('/receiver/cast', { method: 'POST', body: fd, credentials: 'same-origin' }));
    }
    const body = new URLSearchParams();
    const kind = detectKind(input.value);
    body.set('kind', kind);
    body.set('payload', input.value.trim());
    return parse(await fetch('/receiver/cast', {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: body.toString(),
      credentials: 'same-origin',
    }));
  }

  async function parse(resp) {
    try {
      return await resp.json();
    } catch (_) {
      return { ok: false, chip: 'CAST FAILED' };
    }
  }

  // Expose the error setter so preset-bank.js can render preset-click
  // errors in the same chip (single source of truth).
  window.Chassis = window.Chassis || {};
  window.Chassis.input = { showError: setErrorChip };

  const localFilesBtn = document.getElementById('localfiles-btn');
  const localFilesDrawer = document.getElementById('localfiles-drawer');
  const localFilesCloseBtn = document.getElementById('localfiles-close-btn');
  const localFilesSelect = document.getElementById('localfiles-library-select');
  const localFilesEntries = document.getElementById('localfiles-entries');
  const localFilesBreadcrumb = document.getElementById('localfiles-breadcrumb');
  const localFilesError = document.getElementById('localfiles-error');
  let localFilesPath = '';

  function localFileEl(tag, cls, text) {
    const node = document.createElement(tag);
    if (cls) node.className = cls;
    if (text != null) node.textContent = text;
    return node;
  }

  function paintLocalFilesError(msg) {
    if (localFilesError) {
      localFilesError.textContent = msg;
      localFilesError.hidden = false;
    }
    setErrorChip(msg.toUpperCase());
  }

  function clearLocalFilesError() {
    if (!localFilesError) return;
    localFilesError.hidden = true;
    localFilesError.textContent = '';
  }

  function openLocalFilesDrawer() {
    if (!localFilesDrawer) return;
    localFilesDrawer.hidden = false;
    localFilesDrawer.classList.add('localfiles-open');
  }

  function closeLocalFilesDrawer() {
    if (!localFilesDrawer) return;
    localFilesDrawer.classList.remove('localfiles-open');
    localFilesDrawer.hidden = true;
  }

  function setLocalFilesLibraries(libs) {
    if (!localFilesBtn || !localFilesSelect) return;
    const current = localFilesSelect.value || '';
    const rows = (libs || []).filter((lib) => lib && lib.name);
    localFilesSelect.replaceChildren();
    rows.forEach((lib) => {
      const opt = localFileEl('option', null, lib.name);
      opt.value = lib.name;
      localFilesSelect.appendChild(opt);
    });
    if (rows.some((lib) => lib.name === current)) {
      localFilesSelect.value = current;
    } else if (rows.length > 0) {
      localFilesSelect.value = rows[0].name;
    }
    const available = rows.length > 0;
    localFilesBtn.disabled = !available;
    localFilesBtn.title = available ? 'Browse Local Files' : 'Configure Local Files in Settings';
    if (!available) closeLocalFilesDrawer();
  }

  window.Chassis.localFiles = window.Chassis.localFiles || {};
  window.Chassis.localFiles.setLibraries = setLocalFilesLibraries;

  async function browseLocalFiles(path) {
    if (!localFilesSelect || !localFilesEntries) return;
    const lib = localFilesSelect.value || '';
    if (!lib) {
      paintLocalFilesError('no library selected');
      return;
    }
    clearLocalFilesError();
    const body = new URLSearchParams();
    body.set('lib', lib);
    body.set('path', path || '');
    try {
      const res = await fetch('/receiver/localfiles/browse', {
        method: 'POST',
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body: body.toString(),
        credentials: 'same-origin',
      });
      const payload = await res.json().catch(() => ({}));
      if (!payload.ok) {
        paintLocalFilesError(payload.error || payload.chip || 'browse failed');
        return;
      }
      localFilesPath = path || '';
      renderLocalFilesEntries(payload.entries || []);
      if (localFilesBreadcrumb) localFilesBreadcrumb.textContent = `/${localFilesPath}`;
      openLocalFilesDrawer();
    } catch (_) {
      paintLocalFilesError('network error');
    }
  }

  function renderLocalFilesEntries(entries) {
    if (!localFilesEntries) return;
    localFilesEntries.replaceChildren();
    if (localFilesPath) {
      const up = localFileEl('button', 'ch-card', '..');
      up.type = 'button';
      up.setAttribute('data-localfiles-dir', localFilesPath.split('/').slice(0, -1).join('/'));
      localFilesEntries.appendChild(up);
    }
    (entries || []).forEach((entry) => {
      const btn = localFileEl('button', 'ch-card', entry.name || entry.rel || '');
      btn.type = 'button';
      if (entry.is_dir) {
        btn.setAttribute('data-localfiles-dir', entry.rel || '');
      } else if (entry.playable) {
        btn.setAttribute('data-localfiles-file', entry.rel || '');
        if (entry.duration_s) btn.appendChild(localFileEl('span', 'help', ` ${Math.round(entry.duration_s)}s`));
      } else {
        btn.disabled = true;
      }
      localFilesEntries.appendChild(btn);
    });
  }

  async function castLocalFile(path) {
    if (!localFilesSelect) return;
    const body = new URLSearchParams();
    body.set('lib', localFilesSelect.value || '');
    body.set('path', path);
    try {
      const res = await fetch('/receiver/localfiles/cast', {
        method: 'POST',
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body: body.toString(),
        credentials: 'same-origin',
      });
      const payload = await res.json().catch(() => ({}));
      if (!payload.ok) {
        paintLocalFilesError(payload.error || payload.chip || 'cast failed');
        return;
      }
      closeLocalFilesDrawer();
      clearLocalFilesError();
    } catch (_) {
      paintLocalFilesError('network error');
    }
  }

  if (localFilesBtn && localFilesDrawer && localFilesSelect && localFilesEntries) {
    localFilesBtn.addEventListener('click', () => {
      if (localFilesBtn.disabled) {
        paintLocalFilesError('configure local files');
        return;
      }
      browseLocalFiles('');
    });
    if (localFilesCloseBtn) localFilesCloseBtn.addEventListener('click', closeLocalFilesDrawer);
    localFilesSelect.addEventListener('change', () => browseLocalFiles(''));
    localFilesEntries.addEventListener('click', (ev) => {
      const dir = ev.target.closest('[data-localfiles-dir]');
      if (dir && localFilesEntries.contains(dir)) {
        ev.preventDefault();
        browseLocalFiles(dir.getAttribute('data-localfiles-dir') || '');
        return;
      }
      const file = ev.target.closest('[data-localfiles-file]');
      if (file && localFilesEntries.contains(file)) {
        ev.preventDefault();
        castLocalFile(file.getAttribute('data-localfiles-file') || '');
      }
    });
  }

  updateState();
})();
