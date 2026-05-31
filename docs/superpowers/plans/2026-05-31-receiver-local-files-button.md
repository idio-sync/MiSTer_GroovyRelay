# Receiver Local Files Button Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `LOCAL FILES` receiver button beside `.TORRENT` that opens a receiver-level local file browser and casts selected files.

**Architecture:** Reuse the existing chassis Local Files service interfaces and add receiver-scoped route aliases for browse/cast. Render configured libraries into the receiver input-row data so the button can be disabled when no usable libraries exist. Implement the client browser inside `input-cast.js`, using the existing `upload-btn` and `catalog-drawer` visual vocabulary.

**Tech Stack:** Go templates and handlers in `internal/chassis`, vanilla JavaScript in `internal/chassis/static/input-cast.js`, CSS in `internal/chassis/static/chassis.css`, Go unit tests with `/tmp/go1.26.2/bin/go test`.

---

### Task 1: Render The Receiver Local Files Entry Point

**Files:**
- Modify: `internal/chassis/data.go`
- Modify: `internal/chassis/templates/input-row.html`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write the failing render tests**

Add tests near the existing receiver shell/input-row tests in `internal/chassis/chassis_test.go`:

```go
func TestReceiverInputRendersLocalFilesButtonBesideTorrent(t *testing.T) {
	t.Parallel()
	lf := &fakeLocalFilesService{libraries: []LocalFileLibraryRow{{Name: "Movies", Root: "/media/movies"}}}
	cfg := nonZeroConfig()
	cfg.LocalFiles = lf
	cfg.LocalFilesLibraryEditor = lf
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)
	rec := httptest.NewRecorder()
	s.handleIndex(rec, req)
	body := rec.Body.String()
	torrent := strings.Index(body, `id="upload-btn"`)
	local := strings.Index(body, `id="localfiles-btn"`)
	if torrent < 0 || local < 0 {
		t.Fatalf("input row missing torrent/local buttons: torrent=%d local=%d", torrent, local)
	}
	if local < torrent {
		t.Fatalf("LOCAL FILES button rendered before .TORRENT; want beside it after torrent")
	}
	for _, want := range []string{
		`class="upload-btn" id="localfiles-btn"`,
		`>LOCAL FILES</button>`,
		`id="localfiles-drawer"`,
		`data-receiver-localfiles-drawer`,
		`<option value="Movies">Movies</option>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("receiver local files UI missing %q in:\n%s", want, excerpt(body, "localfiles-btn"))
		}
	}
}

func TestReceiverInputDisablesLocalFilesButtonWithoutLibraries(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)
	rec := httptest.NewRecorder()
	s.handleIndex(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, `class="upload-btn" id="localfiles-btn" type="button" disabled`) {
		t.Fatalf("local files button should render disabled without libraries:\n%s", excerpt(body, "localfiles-btn"))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
env GOCACHE=/tmp/gocache GOMODCACHE=/tmp/gomodcache /tmp/go1.26.2/bin/go test ./internal/chassis -run 'TestReceiverInputRendersLocalFilesButtonBesideTorrent|TestReceiverInputDisablesLocalFilesButtonWithoutLibraries' -count=1
```

Expected: FAIL because `localfiles-btn` and `localfiles-drawer` do not exist yet.

- [ ] **Step 3: Add input-row data and template markup**

In `internal/chassis/data.go`, extend `InputData`:

```go
type InputData struct {
	PastePlaceholder    string
	DetectedKind        string
	CastEnabled         bool
	LocalFilesAvailable bool
	LocalFilesLibraries []LocalFileLibraryRow
}
```

Add a helper:

```go
func inputDataFromConfig(cfg Config) InputData {
	data := InputData{
		PastePlaceholder: "Paste URL or magnet",
		DetectedKind:     "URL",
		CastEnabled:      false,
	}
	if cfg.LocalFiles == nil || cfg.LocalFilesLibraryEditor == nil {
		return data
	}
	libs := cfg.LocalFilesLibraryEditor.Libraries()
	data.LocalFilesLibraries = append([]LocalFileLibraryRow(nil), libs...)
	data.LocalFilesAvailable = len(data.LocalFilesLibraries) > 0
	return data
}
```

Replace the inline `Input: InputData{...}` in `idleSnapshot` with:

```go
Input: inputDataFromConfig(cfg),
```

In `internal/chassis/templates/input-row.html`, add the button immediately after `.TORRENT`:

```html
    <button class="upload-btn" id="localfiles-btn" type="button"{{if not .LocalFilesAvailable}} disabled title="Configure Local Files in Settings"{{else}} title="Browse Local Files"{{end}}>LOCAL FILES</button>
```

Then add a receiver drawer before `</div>` of the input section:

```html
  <div class="catalog-drawer localfiles-drawer receiver-localfiles-drawer" id="localfiles-drawer" data-receiver-localfiles-drawer hidden>
    <div class="catalog-top">
      <button type="button" class="action-btn ghost" id="localfiles-close-btn">Close</button>
      <select class="field-input" id="localfiles-library-select" aria-label="Local Files library">
        {{ range .LocalFilesLibraries }}<option value="{{ .Name }}">{{ .Name }}</option>{{ end }}
      </select>
      <div class="catalog-title" id="localfiles-breadcrumb">/</div>
    </div>
    <div class="widget-err" id="localfiles-error" hidden></div>
    <div class="catalog-grid" id="localfiles-entries"></div>
  </div>
```

- [ ] **Step 4: Run tests to verify they pass**

Run:

```bash
env GOCACHE=/tmp/gocache GOMODCACHE=/tmp/gomodcache /tmp/go1.26.2/bin/go test ./internal/chassis -run 'TestReceiverInputRendersLocalFilesButtonBesideTorrent|TestReceiverInputDisablesLocalFilesButtonWithoutLibraries' -count=1
```

Expected: PASS.

---

### Task 2: Add Receiver-Scoped Local Files Browse/Cast Routes

**Files:**
- Modify: `internal/chassis/server.go`
- Modify: `internal/chassis/settings.go`
- Modify: `internal/chassis/settings_test.go`

- [ ] **Step 1: Write failing route alias tests**

Add tests near the existing local files handler tests in `internal/chassis/settings_test.go`:

```go
func TestHandleReceiverLocalfilesBrowseSuccess(t *testing.T) {
	t.Parallel()
	lf := &fakeLocalFilesService{entries: []LocalFileEntry{{Name: "clip.mp4", Rel: "clip.mp4", Playable: true}}}
	s := newURLWidgetTestServer(t, Config{LocalFiles: lf})
	rec := postLocalFilesForm(t, s, "/receiver/localfiles/browse", url.Values{
		"lib":  {"Media"},
		"path": {"Movies"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("Code = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if lf.gotLib != "Media" || lf.gotPath != "Movies" {
		t.Fatalf("Browse got lib/path = %q/%q, want Media/Movies", lf.gotLib, lf.gotPath)
	}
}

func TestHandleReceiverLocalfilesCastSuccess(t *testing.T) {
	t.Parallel()
	lf := &fakeLocalFilesService{}
	s := newURLWidgetTestServer(t, Config{LocalFiles: lf})
	rec := postLocalFilesForm(t, s, "/receiver/localfiles/cast", url.Values{
		"lib":  {"Media"},
		"path": {"clip.mp4"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("Code = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if lf.castLib != "Media" || lf.castPath != "clip.mp4" {
		t.Fatalf("Cast got lib/path = %q/%q, want Media/clip.mp4", lf.castLib, lf.castPath)
	}
}

func TestHandleReceiverLocalfilesCastErrorIsCompact(t *testing.T) {
	t.Parallel()
	lf := &fakeLocalFilesService{castErr: errors.New("open /media/private/clip.mp4: permission denied")}
	s := newURLWidgetTestServer(t, Config{LocalFiles: lf})
	rec := postLocalFilesForm(t, s, "/receiver/localfiles/cast", url.Values{
		"lib":  {"Media"},
		"path": {"private/clip.mp4"},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Code = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "/media/private") || strings.Contains(rec.Body.String(), "permission denied") {
		t.Fatalf("receiver localfiles error leaked raw filesystem detail: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"error":"not found"`) {
		t.Fatalf("receiver localfiles error should be compact not found JSON: %s", rec.Body.String())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
env GOCACHE=/tmp/gocache GOMODCACHE=/tmp/gomodcache /tmp/go1.26.2/bin/go test ./internal/chassis -run 'TestHandleReceiverLocalfilesBrowseSuccess|TestHandleReceiverLocalfilesCastSuccess|TestHandleReceiverLocalfilesCastErrorIsCompact' -count=1
```

Expected: FAIL with 404 for `/receiver/localfiles/*`.

- [ ] **Step 3: Add route aliases**

In `internal/chassis/server.go`, add routes near `/receiver/cast`:

```go
	mux.Handle("POST /receiver/localfiles/browse",
		requireSameOrigin(http.HandlerFunc(s.handleReceiverLocalfilesBrowse)))
	mux.Handle("POST /receiver/localfiles/cast",
		requireSameOrigin(http.HandlerFunc(s.handleReceiverLocalfilesCast)))
```

In `internal/chassis/settings.go`, add wrappers near the existing localfiles handlers:

```go
func (s *Server) handleReceiverLocalfilesBrowse(w http.ResponseWriter, r *http.Request) {
	s.handleSettingsAdapterLocalfilesBrowse(w, r)
}

func (s *Server) handleReceiverLocalfilesCast(w http.ResponseWriter, r *http.Request) {
	s.handleSettingsAdapterLocalfilesCast(w, r)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run:

```bash
env GOCACHE=/tmp/gocache GOMODCACHE=/tmp/gomodcache /tmp/go1.26.2/bin/go test ./internal/chassis -run 'TestHandleReceiverLocalfilesBrowseSuccess|TestHandleReceiverLocalfilesCastSuccess|TestHandleReceiverLocalfilesCastErrorIsCompact' -count=1
```

Expected: PASS.

---

### Task 3: Wire The Receiver Drawer Client

**Files:**
- Modify: `internal/chassis/static/input-cast.js`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write failing static contract test**

Add this test near other static asset contract tests in `internal/chassis/chassis_test.go`:

```go
func TestInputCastJSWiresReceiverLocalFilesBrowser(t *testing.T) {
	t.Parallel()
	src, err := chassisStaticFS.ReadFile("static/input-cast.js")
	if err != nil {
		t.Fatalf("ReadFile input-cast.js: %v", err)
	}
	text := string(src)
	for _, want := range []string{
		`document.getElementById('localfiles-btn')`,
		`document.getElementById('localfiles-drawer')`,
		`/receiver/localfiles/browse`,
		`/receiver/localfiles/cast`,
		`data-localfiles-dir`,
		`data-localfiles-file`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("input-cast.js missing receiver local files hook %q", want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
env GOCACHE=/tmp/gocache GOMODCACHE=/tmp/gomodcache /tmp/go1.26.2/bin/go test ./internal/chassis -run TestInputCastJSWiresReceiverLocalFilesBrowser -count=1
```

Expected: FAIL because `input-cast.js` has no receiver local files hooks yet.

- [ ] **Step 3: Implement client browsing**

In `internal/chassis/static/input-cast.js`, keep the existing torrent/url behavior and add optional local files wiring after `window.Chassis.input = { showError: setErrorChip };`:

```js
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
      setErrorChip('CAST STARTED');
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
```

- [ ] **Step 4: Run test to verify it passes**

Run:

```bash
env GOCACHE=/tmp/gocache GOMODCACHE=/tmp/gomodcache /tmp/go1.26.2/bin/go test ./internal/chassis -run TestInputCastJSWiresReceiverLocalFilesBrowser -count=1
```

Expected: PASS.

---

### Task 4: Match Button Styling And Drawer Polish

**Files:**
- Modify: `internal/chassis/static/chassis.css`
- Modify: `internal/chassis/css_scope_test.go`

- [ ] **Step 1: Write failing CSS contract test**

Add this test in `internal/chassis/css_scope_test.go` near the input-row CSS tests:

```go
func TestReceiverLocalFilesButtonUsesUploadButtonStyle(t *testing.T) {
	t.Parallel()
	cssBytes, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("ReadFile chassis.css: %v", err)
	}
	css := string(cssBytes)
	for _, want := range []string{
		`body.receiver .upload-btn`,
		`body.receiver .receiver-localfiles-drawer .catalog-top`,
		`body.receiver .receiver-localfiles-drawer .field-input`,
		`body.receiver .receiver-localfiles-drawer .widget-err`,
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("chassis.css missing local files/input styling hook %q", want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
env GOCACHE=/tmp/gocache GOMODCACHE=/tmp/gomodcache /tmp/go1.26.2/bin/go test ./internal/chassis -run TestReceiverLocalFilesButtonUsesUploadButtonStyle -count=1
```

Expected: FAIL because the receiver drawer styling hooks do not exist yet.

- [ ] **Step 3: Add drawer-specific CSS without changing `.TORRENT`**

In `internal/chassis/static/chassis.css`, keep `#localfiles-btn` on class `upload-btn`. Add:

```css
body.receiver .upload-btn:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

body.receiver .receiver-localfiles-drawer {
  grid-column: 2 / -1;
}

body.receiver .receiver-localfiles-drawer .catalog-top {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 10px 8px;
  background: linear-gradient(180deg, #0c0c0e, #060606);
  border: 1px solid #0a0a0b;
  border-radius: 2px 2px 0 0;
}

body.receiver .receiver-localfiles-drawer .field-input {
  min-width: 140px;
  background: #0a0a0b;
  border: 1px solid #1a1a1d;
  color: #d4d4d8;
  border-radius: 2px;
  padding: 7px 9px;
  font: 600 10px 'Inter', sans-serif;
}

body.receiver .receiver-localfiles-drawer .catalog-title {
  min-width: 0;
  color: var(--vfd);
  font: 700 10px 'DSEG14-Classic', monospace;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

body.receiver .receiver-localfiles-drawer .widget-err {
  margin: 8px 10px 0;
}
```

- [ ] **Step 4: Run test to verify it passes**

Run:

```bash
env GOCACHE=/tmp/gocache GOMODCACHE=/tmp/gomodcache /tmp/go1.26.2/bin/go test ./internal/chassis -run TestReceiverLocalFilesButtonUsesUploadButtonStyle -count=1
```

Expected: PASS.

---

### Task 5: Focused Verification And Commit

**Files:**
- Verify all files changed in Tasks 1-4.

- [ ] **Step 1: Run focused chassis tests**

Run:

```bash
env GOCACHE=/tmp/gocache GOMODCACHE=/tmp/gomodcache /tmp/go1.26.2/bin/go test ./internal/chassis -count=1
```

Expected: PASS.

- [ ] **Step 2: Run command package tests for compile coverage**

Run:

```bash
env GOCACHE=/tmp/gocache GOMODCACHE=/tmp/gomodcache /tmp/go1.26.2/bin/go test ./cmd/mister-groovy-relay -count=1
```

Expected: PASS.

- [ ] **Step 3: Inspect diff**

Run:

```bash
git diff -- internal/chassis/data.go internal/chassis/server.go internal/chassis/settings.go internal/chassis/templates/input-row.html internal/chassis/static/input-cast.js internal/chassis/static/chassis.css internal/chassis/chassis_test.go internal/chassis/settings_test.go internal/chassis/css_scope_test.go
```

Expected: diff only contains receiver local files button, route aliases, drawer JS/CSS, and tests.

- [ ] **Step 4: Commit implementation**

Run:

```bash
git add internal/chassis/data.go internal/chassis/server.go internal/chassis/settings.go internal/chassis/templates/input-row.html internal/chassis/static/input-cast.js internal/chassis/static/chassis.css internal/chassis/chassis_test.go internal/chassis/settings_test.go internal/chassis/css_scope_test.go
git commit -m "feat: expose local files in receiver input row"
```

Expected: commit succeeds on `feature/receiver-local-files-button`.
