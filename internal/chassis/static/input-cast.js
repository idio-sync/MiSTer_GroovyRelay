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

  updateState();
})();
