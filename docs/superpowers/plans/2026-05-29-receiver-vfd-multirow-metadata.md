# Receiver VFD Multi-Row Metadata Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Render up to three rows of real media metadata (per media type) in the receiver chassis VFD, plumbed adapter → core → SSE → DOM, and realign the CRT visualizer overlay to title-first so both surfaces agree.

**Architecture:** A new adapter-agnostic `core.DisplayMetadata{Primary,Secondary,Tertiary}` carries pre-formatted strings (each adapter owns its media-type formatting — core never interprets). The chassis renders three stacked DSEG14 rows with marquee scrolling and empty-row collapse. The CRT visualizer keeps its structured `VisualizerMetadata` rendering, reordered title→artist→album.

**Tech Stack:** Go 1.26 stdlib (`net/http`, `encoding/xml`, `encoding/json`, `html/template`), embedded HTML/CSS/JS via `go:embed`, vanilla ES2022. Tests use the stdlib `testing` + `net/http/httptest`.

**Spec reference:** [`docs/superpowers/specs/2026-05-29-receiver-vfd-multirow-metadata-design.md`](../specs/2026-05-29-receiver-vfd-multirow-metadata-design.md).

---

## Conventions used throughout this plan

- **Names (must stay identical across tasks):** `core.DisplayMetadata` with fields `Primary`, `Secondary`, `Tertiary`; `SessionRequest.DisplayMetadata`; `StatusHomeView.Display`; `VFDData.Primary/Secondary/Tertiary`; SSE JSON keys `primary`/`secondary`/`tertiary`; DOM attrs `data-vfd-primary`/`-secondary`/`-tertiary`; CSS row classes `tier-primary`/`tier-secondary`/`tier-tertiary` and state classes `is-empty`/`is-scrolling`; shared helpers `adapters.FormatSeasonEpisode` and `adapters.FormatUploadDate`.
- **CI gates (keep green):** `make lint` (`go vet ./...`), `make test`, `go test -race ./...`, `make test-integration`.
- **Commits:** one per task (each task ends green and builds).

## File map

**Created:**
- `internal/adapters/displaymeta.go` — shared `FormatSeasonEpisode` / `FormatUploadDate` helpers.
- `internal/adapters/displaymeta_test.go` — their tests.
- `internal/adapters/plex/video_metadata.go` — `VideoMetadata` + `VideoMetadataFor` (new PMS video fetch).
- `internal/adapters/plex/video_metadata_test.go`.

**Modified (core):** `internal/core/types.go`, `internal/core/manager.go`, `internal/core/manager_test.go`.
**Modified (chassis):** `internal/chassis/data.go`, `internal/chassis/session.go`, `internal/chassis/events.go`, `internal/chassis/templates/vfd.html`, `internal/chassis/static/vfd-live.js`, `internal/chassis/static/chassis.css`, plus test files `internal/chassis/events_test.go`, `internal/chassis/chassis_test.go`, `internal/chassis/session_test.go` (migrate `Title`/`Marquee` references).
**Modified (CRT):** `internal/ffmpeg/pipeline.go`, `internal/ffmpeg/pipeline_test.go`.
**Modified (adapters):** `internal/adapters/plex/companion.go` (+`companion_test.go`), `internal/adapters/plex/transcode.go`, `internal/adapters/jellyfin/playback.go` (+`playback_test.go`), `internal/adapters/url/ytdlp/resolver.go`, `internal/adapters/url/play.go` (+test), `internal/adapters/streams/playback.go` (+test), `internal/adapters/dlna/play.go` + DIDL parse site, `internal/adapters/torrent/session.go`, `internal/adapters/auxadapter/session.go`.

---

## Task 1: Add `core.DisplayMetadata` and surface it on `StatusHomeView`

**Files:**
- Modify: `internal/core/types.go`
- Modify: `internal/core/manager.go:1494-1509` (inside `StatusHomeView`)
- Test: `internal/core/manager_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/core/manager_test.go`:

```go
func TestManager_StatusHomeView_CarriesDisplayMetadata(t *testing.T) {
	m := newTestManager(t)
	m.active = &activeSession{req: SessionRequest{
		Title:           "Fallback Title",
		DisplayMetadata: DisplayMetadata{Primary: "P", Secondary: "S", Tertiary: "T"},
	}}
	view := m.StatusHomeView()
	if view.Display.Primary != "P" || view.Display.Secondary != "S" || view.Display.Tertiary != "T" {
		t.Fatalf("Display = %+v, want {P S T}", view.Display)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/core -run TestManager_StatusHomeView_CarriesDisplayMetadata -v`
Expected: FAIL — `undefined: DisplayMetadata` (and `view.Display`).

- [ ] **Step 3: Add the type and fields**

In `internal/core/types.go`, add the type next to `VisualizerMetadata` (after line 56):

```go
// DisplayMetadata is the adapter-agnostic, pre-formatted text the
// receiver VFD renders as three stacked rows. Adapters compose these
// strings (they own per-media-type formatting); core never interprets
// them. Empty tiers render as collapsed rows. See
// docs/superpowers/specs/2026-05-29-receiver-vfd-multirow-metadata-design.md.
type DisplayMetadata struct {
	Primary   string // headline row (biggest)
	Secondary string // attribution row
	Tertiary  string // detail row (dim)
}
```

In `SessionRequest` (after the `Title` field, ~line 174), add:

```go
	// DisplayMetadata is the adapter-composed three-row VFD text. When
	// zero-valued, consumers fall back to Title for the primary row.
	DisplayMetadata DisplayMetadata
```

In `StatusHomeView` (after the `Title` field, ~line 226), add:

```go
	Display     DisplayMetadata // adapter-composed VFD rows; empty when idle
```

- [ ] **Step 4: Populate it in `StatusHomeView`**

In `internal/core/manager.go`, inside the `if m.active != nil {` block (right after line 1496 `view.Title = m.active.req.Title`), add:

```go
		view.Display = m.active.req.DisplayMetadata
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/core -run TestManager_StatusHomeView_CarriesDisplayMetadata -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/core/types.go internal/core/manager.go internal/core/manager_test.go
git commit -m "feat(core): add DisplayMetadata carried through StatusHomeView"
```

---

## Task 2: Reshape `VFDData` to three tiers + idle snapshot

**Files:**
- Modify: `internal/chassis/data.go:46-54` (`VFDData`), `:493-501` (`idleSnapshot`)
- Test: `internal/chassis/chassis_test.go`, `internal/chassis/events_test.go` (compile-fix only this task)

- [ ] **Step 1: Replace the `VFDData` struct**

In `internal/chassis/data.go`, replace the `VFDData` struct (lines 46-54) with:

```go
// VFDData drives the VFD frame in the top row of the chassis. The three
// tiers map per media type (see the metadata design spec); empty tiers
// render as collapsed rows. SystemTime is server-rendered for first
// paint and ticked client-side.
type VFDData struct {
	State        string // "idle" | "live"
	Primary      string
	Secondary    string
	Tertiary     string
	QueueCurrent int
	QueueTotal   int
	SystemTime   string
	Uptime       string
}
```

- [ ] **Step 2: Update `idleSnapshot`**

In `internal/chassis/data.go`, replace the `VFD: VFDData{...}` literal in `idleSnapshot` (lines 493-501) with:

```go
		VFD: VFDData{
			State:        string(StateIdle),
			Primary:      "STANDBY",
			Secondary:    "MISTER LINK OK · 4MS · 12 PRESETS · 90 CHANNELS · PASTE URL OR PICK PRESET",
			QueueCurrent: 0,
			QueueTotal:   0,
			SystemTime:   fmt.Sprintf("%02d:%02d", now.Hour(), now.Minute()),
			Uptime:       formatUptime(now.Sub(cfg.StartedAt)),
		},
```

- [ ] **Step 3: Build to find all broken references**

Run: `go build ./internal/chassis/ 2>&1`
Expected: FAIL — references to `VFDData.Title` / `.Marquee` in `session.go`, `events.go`, and test files. (Tasks 3 and 4 fix `session.go` and `events.go`. For this task, fix only the compile errors in non-test production files that are NOT owned by later tasks; leave `session.go`/`events.go` for Tasks 3/4 by doing those next.)

Because `session.go` and `events.go` still reference the old fields, complete Tasks 3 and 4 before re-running the full build. To keep this task self-contained and committable, perform Tasks 3 and 4's production edits is NOT required here — instead, this task's commit is deferred: proceed directly to Task 3, then Task 4, then commit the chassis reshape together at the end of Task 4.

> **Note:** Tasks 2-4 form one compile unit (the struct rename cascades). Implement 2→3→4, run the build after Task 4, then make a single commit. Steps below for Tasks 3 and 4 say "no commit yet" accordingly.

---

## Task 3: Map `Display`→tiers in the snapshot; remove the old marquee

**Files:**
- Modify: `internal/chassis/session.go:75-117` (`snapshotFromStatusView`), `:216-235` (`formatLiveMarquee` — delete)
- Test: `internal/chassis/session_test.go`

- [ ] **Step 1: Replace the live-branch VFD assignment**

In `internal/chassis/session.go`, inside `snapshotFromStatusView`, replace these two lines (currently 81-82):

```go
		base.VFD.Title = view.Title
		base.VFD.Marquee = formatLiveMarquee(view)
```

with:

```go
		base.VFD.Primary, base.VFD.Secondary, base.VFD.Tertiary = vfdTiersFromView(view)
```

- [ ] **Step 2: Add the tier helper and delete `formatLiveMarquee`**

In `internal/chassis/session.go`, add this helper (place it where `formatLiveMarquee` was):

```go
// vfdTiersFromView maps the adapter-composed DisplayMetadata onto the
// VFD's three rows. Primary falls back to the legacy Title so adapters
// that have not yet populated DisplayMetadata still show their label.
func vfdTiersFromView(view core.StatusHomeView) (primary, secondary, tertiary string) {
	primary = view.Display.Primary
	if primary == "" {
		primary = view.Title
	}
	return primary, view.Display.Secondary, view.Display.Tertiary
}
```

Then **delete** the entire `formatLiveMarquee` function (lines 216-235, the comment block + func). The `SOURCE · pos / dur` line is intentionally removed — source is shown by the source-cluster lamps and elapsed/total live in the transport row.

- [ ] **Step 3: Migrate `session_test.go`**

Run: `go vet ./internal/chassis/ 2>&1 | grep -i marquee` to find references. In `internal/chassis/session_test.go`, delete any test of `formatLiveMarquee` (e.g. `TestFormatLiveMarquee*`) and change assertions reading `snap.VFD.Title` to `snap.VFD.Primary` and `snap.VFD.Marquee` to `snap.VFD.Secondary`/`Tertiary` as appropriate. (No commit yet — see Task 2 note.)

---

## Task 4: Rename the `vfdEnvelope` wire fields + `vfdChanged`

**Files:**
- Modify: `internal/chassis/events.go:45-65` (`vfdEnvelope`, `vfdEnvelopeFrom`), `:163-169` (`vfdChanged`)
- Test: `internal/chassis/events_test.go`, `internal/chassis/chassis_test.go`

- [ ] **Step 1: Replace `vfdEnvelope` and `vfdEnvelopeFrom`**

In `internal/chassis/events.go`, replace the `vfdEnvelope` struct (45-51) and `vfdEnvelopeFrom` (57-65) with:

```go
type vfdEnvelope struct {
	Primary      string `json:"primary"`
	Secondary    string `json:"secondary"`
	Tertiary     string `json:"tertiary"`
	QueueCurrent int    `json:"queueCurrent"`
	QueueTotal   int    `json:"queueTotal"`
	Uptime       string `json:"uptime"`
}

func vfdEnvelopeFrom(v VFDData) vfdEnvelope {
	return vfdEnvelope{
		Primary:      v.Primary,
		Secondary:    v.Secondary,
		Tertiary:     v.Tertiary,
		QueueCurrent: v.QueueCurrent,
		QueueTotal:   v.QueueTotal,
		Uptime:       v.Uptime,
	}
}
```

- [ ] **Step 2: Replace `vfdChanged`**

In `internal/chassis/events.go`, replace `vfdChanged` (163-169) with:

```go
func vfdChanged(a, b VFDData) bool {
	return a.Primary != b.Primary ||
		a.Secondary != b.Secondary ||
		a.Tertiary != b.Tertiary ||
		a.QueueCurrent != b.QueueCurrent ||
		a.QueueTotal != b.QueueTotal ||
		a.Uptime != b.Uptime
}
```

- [ ] **Step 3: Migrate the chassis tests**

In `internal/chassis/events_test.go`, apply these field renames (verified line numbers from the current file):
- ~45-46: `Title: "STANDBY"` / `Marquee: "MISTER LINK OK"` → `Primary: "STANDBY"` / `Secondary: "MISTER LINK OK"`.
- ~136-137: `Title: "STANDBY"` / `Marquee: "hint"` → `Primary: "STANDBY"` / `Secondary: "hint"`.
- ~147-148: the `vfdChanged` mutation cases `{"title", func(v){v.Title=...}}` and `{"marquee", func(v){v.Marquee=...}}` → three cases `{"primary", func(v){v.Primary="Live Primary"}}`, `{"secondary", func(v){v.Secondary="Sec"}}`, `{"tertiary", func(v){v.Tertiary="Ter"}}`.
- ~166: `VFDData{Title:"X", ...}` → `VFDData{Primary:"X", ...}`.
- ~177: `VFDData{Title:"X", Marquee:"Y", ...}` → `VFDData{Primary:"X", Secondary:"Y", ...}`.
- ~632: SSE body assertion `"title":"Seeded Title"` → `"primary":"Seeded Title"` (the fallback maps `StatusHomeView.Title` → `Primary`).
- ~768: `TestHandleEvents_EmitsVfdEventOnTitleChange` — keep the `core.StatusHomeView{...Title:...}` inputs (fallback still drives `Primary`); update any body assertion from `"title"` to `"primary"`.

In `internal/chassis/chassis_test.go`, change `snap.VFD.Title` (~1417) to `snap.VFD.Primary` and its message string.

- [ ] **Step 4: Build + test the whole chassis package**

Run: `go test ./internal/chassis/ -run 'Vfd|VFD|Snapshot|HandleEvents|Index' -v && go build ./...`
Expected: PASS / clean build (Tasks 2-4 now compile together).

- [ ] **Step 5: Commit Tasks 2-4 together**

```bash
git add internal/chassis/data.go internal/chassis/session.go internal/chassis/events.go internal/chassis/events_test.go internal/chassis/chassis_test.go internal/chassis/session_test.go
git commit -m "feat(chassis): three-tier VFDData + Display mapping; drop source/time marquee"
```

---

## Task 5: Render three tier rows in the VFD template

**Files:**
- Modify: `internal/chassis/templates/vfd.html:6-9`
- Test: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write the failing render test**

Append to `internal/chassis/chassis_test.go` (it already imports `net/http`, `net/http/httptest`; reuse the existing handler-setup helper used by `TestHandleIndex_RendersShell200` — construct the server the same way that test does, then):

```go
func TestHandleIndex_RendersThreeVfdTierHooks(t *testing.T) {
	s := newTestServer(t) // existing helper at chassis_test.go:83
	mux := http.NewServeMux()
	s.Mount(mux)
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	body := rr.Body.String()
	for _, hook := range []string{"data-vfd-primary", "data-vfd-secondary", "data-vfd-tertiary"} {
		if !strings.Contains(body, hook) {
			t.Errorf("rendered shell missing %q", hook)
		}
	}
	if strings.Contains(body, "data-vfd-title") || strings.Contains(body, "data-vfd-marquee") {
		t.Errorf("rendered shell still contains old data-vfd-title/marquee hooks")
	}
}
```

> `newTestServer(t)` is defined at chassis_test.go:83 and returns a `*Server` ready to `Mount`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chassis -run TestHandleIndex_RendersThreeVfdTierHooks -v`
Expected: FAIL — hooks not found (template still renders title/marquee).

- [ ] **Step 3: Replace the left zone of the VFD template**

In `internal/chassis/templates/vfd.html`, replace the inner `<div>` (lines 6-9, the title-line + marquee-line block) with:

```html
      <div>
        <div class="vfd-row tier-primary seg-display{{if not .Primary}} is-empty{{end}}"><span class="seg-ghost" aria-hidden="true">~~~~~~~</span><span class="seg-text" data-vfd-primary>{{.Primary}}</span></div>
        <div class="vfd-row tier-secondary seg-display{{if not .Secondary}} is-empty{{end}}"><span class="seg-ghost" aria-hidden="true">~~~~ ~~~~~ ~~</span><span class="seg-text" data-vfd-secondary>{{.Secondary}}</span></div>
        <div class="vfd-row tier-tertiary seg-display{{if not .Tertiary}} is-empty{{end}}"><span class="seg-ghost" aria-hidden="true">~~ ~~ ~~~~</span><span class="seg-text" data-vfd-tertiary>{{.Tertiary}}</span></div>
      </div>
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/chassis -run TestHandleIndex_RendersThreeVfdTierHooks -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/templates/vfd.html internal/chassis/chassis_test.go
git commit -m "feat(chassis): render three VFD tier rows in template"
```

---

## Task 6: VFD tier styling + marquee scroll keyframes (CSS)

**Files:**
- Modify: `internal/chassis/static/chassis.css:681-704` (replace `.title-line`/`.marquee-line`), and the responsive block (~845-926) that references those classes.
- Test: `internal/chassis/chassis_test.go` (served-CSS substring check)

- [ ] **Step 1: Write the failing test**

Append to `internal/chassis/chassis_test.go`:

```go
func TestStaticCSS_HasVfdTierAndScrollRules(t *testing.T) {
	s := newTestServer(t)
	mux := http.NewServeMux()
	s.Mount(mux)
	req := httptest.NewRequest(http.MethodGet, "/receiver/static/chassis.css", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	css := rr.Body.String()
	for _, want := range []string{".tier-primary", ".tier-secondary", ".tier-tertiary", "vfd-marquee", "prefers-reduced-motion", ".vfd-row.is-empty"} {
		if !strings.Contains(css, want) {
			t.Errorf("chassis.css missing %q", want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chassis -run TestStaticCSS_HasVfdTierAndScrollRules -v`
Expected: FAIL.

- [ ] **Step 3: Replace the title/marquee CSS with tier + scroll rules**

In `internal/chassis/static/chassis.css`, replace the `.title-line` and `.marquee-line` rule blocks (681-704) and the `.title-line.seg-display, .marquee-line.seg-display` block (706-709) with:

```css
  body.receiver .vfd .vfd-row {
    display: block;
    max-width: 100%;
    overflow: hidden;
    white-space: nowrap;
    position: relative;
  }

  body.receiver .vfd .vfd-row.is-empty {
    display: none;
  }

  body.receiver .vfd .vfd-row .seg-text {
    display: inline-block;
    will-change: transform;
  }

  body.receiver .vfd .vfd-row.is-scrolling .seg-text {
    animation: vfd-marquee var(--vfd-scroll-dur, 8s) linear infinite;
  }

  @keyframes vfd-marquee {
    0%, 8% { transform: translateX(0); }
    100% { transform: translateX(calc(-1 * var(--vfd-scroll-dist, 0px))); }
  }

  @media (prefers-reduced-motion: reduce) {
    body.receiver .vfd .vfd-row.is-scrolling .seg-text { animation: none; }
  }

  body.receiver .vfd .tier-primary {
    margin-bottom: 7px;
    font-family: 'DSEG14-Classic', monospace;
    font-size: 23px;
    font-weight: 700;
    line-height: 1.05;
    letter-spacing: 0.04em;
    text-transform: uppercase;
  }

  body.receiver .vfd .tier-secondary {
    margin-bottom: 5px;
    font-family: 'DSEG14-Classic', monospace;
    font-size: 14px;
    font-weight: 400;
    line-height: 1.1;
    letter-spacing: 0.06em;
    text-transform: uppercase;
  }

  body.receiver .vfd .tier-tertiary {
    color: var(--vfd-dim);
    font-family: 'DSEG14-Classic', monospace;
    font-size: 12px;
    font-weight: 400;
    line-height: 1.1;
    letter-spacing: 0.08em;
    text-transform: uppercase;
  }
```

- [ ] **Step 4: Update the responsive block**

In the responsive section (~845-926), find every selector mentioning `.title-line` or `.marquee-line` and update them to scale the tiers. Replace those selectors so each breakpoint shrinks the trio, e.g. at the 720px container query:

```css
  @container vfd (max-width: 720px) {
    body.receiver .vfd .tier-primary { font-size: 20px; }
    body.receiver .vfd .tier-secondary { font-size: 13px; }
    body.receiver .vfd .tier-tertiary { font-size: 11px; }
  }
```

Mirror the existing smaller breakpoints (520px, 420px) with proportionally smaller sizes (e.g. 18/12/10 and 16/11/10), preserving the existing `@container vfd` query syntax already in the file.

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/chassis -run TestStaticCSS_HasVfdTierAndScrollRules -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/chassis/static/chassis.css internal/chassis/chassis_test.go
git commit -m "feat(chassis): VFD tier styles + marquee scroll keyframes"
```

---

## Task 7: Wire three spans + scrolling in the live JS

**Files:**
- Modify: `internal/chassis/static/vfd-live.js:70-84` (`handleVfdEvent`) + `connect`/init
- Test: `internal/chassis/chassis_test.go` (served-JS substring check; behavior verified manually)

- [ ] **Step 1: Write the failing test**

Append to `internal/chassis/chassis_test.go`:

```go
func TestStaticJS_VfdLiveUsesTierHooks(t *testing.T) {
	s := newTestServer(t)
	mux := http.NewServeMux()
	s.Mount(mux)
	req := httptest.NewRequest(http.MethodGet, "/receiver/static/vfd-live.js", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	js := rr.Body.String()
	for _, want := range []string{"data-vfd-primary", "data-vfd-secondary", "data-vfd-tertiary", "is-scrolling", "fonts"} {
		if !strings.Contains(js, want) {
			t.Errorf("vfd-live.js missing %q", want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chassis -run TestStaticJS_VfdLiveUsesTierHooks -v`
Expected: FAIL.

- [ ] **Step 3: Replace `handleVfdEvent` and add scroll helpers**

In `internal/chassis/static/vfd-live.js`, replace `handleVfdEvent` (70-84) with:

```js
  function applyTier(attr, text) {
    const el = document.querySelector(`[${attr}]`);
    if (!el) return;
    el.textContent = text || '';
    const row = el.closest('.vfd-row');
    if (row) row.classList.toggle('is-empty', !text);
    measureScroll(row, el);
  }

  // measureScroll toggles marquee animation when a tier's text overflows
  // its column. Distance + duration are set as CSS custom properties so
  // the @keyframes can translate by exactly the overflow (constant ~40px/s
  // so long titles aren't dizzyingly fast).
  function measureScroll(row, el) {
    if (!row || !el) return;
    row.classList.remove('is-scrolling');
    row.style.removeProperty('--vfd-scroll-dist');
    row.style.removeProperty('--vfd-scroll-dur');
    const overflow = el.scrollWidth - row.clientWidth;
    if (overflow > 4) {
      const dist = overflow + 24; // trailing gap before the loop restarts
      const dur = Math.max(6, dist / 40);
      row.style.setProperty('--vfd-scroll-dist', dist + 'px');
      row.style.setProperty('--vfd-scroll-dur', dur + 's');
      row.classList.add('is-scrolling');
    }
  }

  function remeasureAllTiers() {
    ['[data-vfd-primary]', '[data-vfd-secondary]', '[data-vfd-tertiary]'].forEach((sel) => {
      const el = document.querySelector(sel);
      if (el) measureScroll(el.closest('.vfd-row'), el);
    });
  }

  function handleVfdEvent(ev) {
    try {
      const data = JSON.parse(ev.data);
      applyTier('data-vfd-primary', data.primary);
      applyTier('data-vfd-secondary', data.secondary);
      applyTier('data-vfd-tertiary', data.tertiary);
      const queue = document.querySelector('[data-vfd-queue]');
      const uptime = document.querySelector('[data-vfd-uptime]');
      if (queue) queue.textContent = `${data.queueCurrent} / ${data.queueTotal}`;
      if (uptime) uptime.textContent = data.uptime || '';
      // Re-measure once fonts are final (DSEG14 metrics differ from the
      // fallback monospace; a pre-font measurement mis-sizes the marquee).
      if (document.fonts && document.fonts.ready) {
        document.fonts.ready.then(remeasureAllTiers);
      }
    } catch (err) {
      console.warn('vfd-live: bad vfd payload', ev.data, err);
    }
  }
```

- [ ] **Step 4: Add a resize listener**

In `internal/chassis/static/vfd-live.js`, inside the `connect()` function (after `source.addEventListener('vfd', handleVfdEvent);`, ~line 119), add:

```js
    window.addEventListener('resize', remeasureAllTiers);
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/chassis -run TestStaticJS_VfdLiveUsesTierHooks -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/chassis/static/vfd-live.js internal/chassis/chassis_test.go
git commit -m "feat(chassis): live three-tier VFD updates with overflow marquee scroll"
```

---

## Task 8: Realign the CRT visualizer overlay to title-first

**Files:**
- Modify: `internal/ffmpeg/pipeline.go:385-407` (`visualizerTextLines`)
- Test: `internal/ffmpeg/pipeline_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/ffmpeg/pipeline_test.go`:

```go
func TestVisualizerTextLines_TitleFirstOrder(t *testing.T) {
	s := PipelineSpec{OutputHeight: 480}
	s.Visualizer.Metadata = VisualizerMetadata{Title: "Song", Artist: "Band", Album: "Record"}
	lines := visualizerTextLines(s)
	var roles []string
	for _, l := range lines {
		if l.Role == visualizerTextRoleProgress {
			continue
		}
		roles = append(roles, l.Role)
	}
	want := []string{visualizerTextRoleTitle, visualizerTextRoleArtist, visualizerTextRoleAlbum}
	if len(roles) != len(want) {
		t.Fatalf("roles = %v, want %v", roles, want)
	}
	for i := range want {
		if roles[i] != want[i] {
			t.Fatalf("roles = %v, want %v", roles, want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ffmpeg -run TestVisualizerTextLines_TitleFirstOrder -v`
Expected: FAIL — current order is artist, title, album.

- [ ] **Step 3: Reorder the lines**

In `internal/ffmpeg/pipeline.go`, replace the body of `visualizerTextLines` (the `y := 0` block through the album append, lines 390-403) with title-first ordering:

```go
	y := 0
	title := strings.TrimSpace(md.Title)
	if title == "" {
		title = "Now Playing"
	}
	lines = append(lines, visualizerMetadataLine(layout, visualizerTextRoleTitle, title, layout.MetadataY[y], 20, visualizerMetadataColor))
	y++
	if artist := strings.TrimSpace(md.Artist); artist != "" {
		lines = append(lines, visualizerMetadataLine(layout, visualizerTextRoleArtist, artist, layout.MetadataY[y], 20, visualizerMetadataColor))
		y++
	}
	if album := strings.TrimSpace(md.Album); album != "" {
		lines = append(lines, visualizerMetadataLine(layout, visualizerTextRoleAlbum, album, layout.MetadataY[y], 18, visualizerAlbumColor))
	}
```

(The progress-clock append after this block, and `MetadataY` indexing via `y`, are unchanged.)

- [ ] **Step 4: Fix any existing order assertion**

Run: `go test ./internal/ffmpeg -run Visualizer -v`. If a pre-existing test asserts the old artist-first order, update its expected sequence to title→artist→album. Do not weaken unrelated assertions.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/ffmpeg -run Visualizer -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/ffmpeg/pipeline.go internal/ffmpeg/pipeline_test.go
git commit -m "feat(ffmpeg): CRT visualizer renders title-first to match the VFD"
```

---

## Task 9: Shared adapter formatting helpers

**Files:**
- Create: `internal/adapters/displaymeta.go`, `internal/adapters/displaymeta_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/adapters/displaymeta_test.go`:

```go
package adapters

import "testing"

func TestFormatSeasonEpisode(t *testing.T) {
	cases := []struct{ s, e, y int; want string }{
		{4, 5, 2008, "S04E05 · 2008"},
		{4, 5, 0, "S04E05"},
		{4, 0, 0, "S04"},
		{0, 5, 0, "E05"},
		{0, 0, 2017, "2017"},
		{0, 0, 0, ""},
	}
	for _, c := range cases {
		if got := FormatSeasonEpisode(c.s, c.e, c.y); got != c.want {
			t.Errorf("FormatSeasonEpisode(%d,%d,%d) = %q, want %q", c.s, c.e, c.y, got, c.want)
		}
	}
}

func TestFormatUploadDate(t *testing.T) {
	cases := []struct{ in, want string }{
		{"20240315", "2024-03-15"},
		{"2024031", ""},
		{"abcd1234", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := FormatUploadDate(c.in); got != c.want {
			t.Errorf("FormatUploadDate(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/adapters -run 'FormatSeasonEpisode|FormatUploadDate' -v`
Expected: FAIL — undefined functions.

- [ ] **Step 3: Implement the helpers**

Create `internal/adapters/displaymeta.go`:

```go
package adapters

import "fmt"

// FormatSeasonEpisode renders a TV detail string for the VFD tertiary
// row: "S04E05", "S04", "E05", optionally suffixed " · YYYY". Zero
// components are omitted. With no S/E but a year, returns the year alone
// ("2017"); used for movies too. Empty when everything is zero.
func FormatSeasonEpisode(season, episode, year int) string {
	var se string
	switch {
	case season > 0 && episode > 0:
		se = fmt.Sprintf("S%02dE%02d", season, episode)
	case season > 0:
		se = fmt.Sprintf("S%02d", season)
	case episode > 0:
		se = fmt.Sprintf("E%02d", episode)
	}
	switch {
	case se != "" && year > 0:
		return fmt.Sprintf("%s · %d", se, year)
	case se != "":
		return se
	case year > 0:
		return fmt.Sprintf("%d", year)
	default:
		return ""
	}
}

// FormatUploadDate converts yt-dlp's "YYYYMMDD" to ISO "YYYY-MM-DD".
// Malformed or empty input returns "" (the tertiary row then collapses).
func FormatUploadDate(raw string) string {
	if len(raw) != 8 {
		return ""
	}
	for _, r := range raw {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return raw[0:4] + "-" + raw[4:6] + "-" + raw[6:8]
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/adapters -run 'FormatSeasonEpisode|FormatUploadDate' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/displaymeta.go internal/adapters/displaymeta_test.go
git commit -m "feat(adapters): shared S/E + upload-date formatting helpers"
```

---

## Task 10: Plex music tiers

**Files:**
- Modify: `internal/adapters/plex/companion.go:393-413` (`musicSessionRequestForPlay`)
- Test: `internal/adapters/plex/companion_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/adapters/plex/companion_test.go`:

```go
func TestMusicSessionRequestForPlay_SetsDisplayTiers(t *testing.T) {
	c := NewCompanion(CompanionConfig{DeviceUUID: "dev", DeviceName: "Relay"}, nil)
	p := PlayMediaRequest{Title: "Midnight City", MediaKey: "/library/metadata/1", TranscodeSessionID: "ts"}
	md := MusicMetadata{Title: "Midnight City", Artist: "M83", Album: "Hurry Up, We're Dreaming"}
	req := c.musicSessionRequestForPlay(p, md)
	if req.DisplayMetadata.Primary != "Midnight City" || req.DisplayMetadata.Secondary != "M83" || req.DisplayMetadata.Tertiary != "Hurry Up, We're Dreaming" {
		t.Fatalf("DisplayMetadata = %+v", req.DisplayMetadata)
	}
}
```

> `NewCompanion(CompanionConfig{...}, nil)` matches the existing pattern (e.g. companion_test.go:59).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/plex -run TestMusicSessionRequestForPlay_SetsDisplayTiers -v`
Expected: FAIL — `DisplayMetadata` empty.

- [ ] **Step 3: Set the tiers**

In `internal/adapters/plex/companion.go`, inside `musicSessionRequestForPlay`, add to the `core.SessionRequest{...}` literal (alongside `Title: title,`):

```go
		DisplayMetadata: core.DisplayMetadata{
			Primary:   title,
			Secondary: md.Artist,
			Tertiary:  md.Album,
		},
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/plex -run TestMusicSessionRequestForPlay_SetsDisplayTiers -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/plex/companion.go internal/adapters/plex/companion_test.go
git commit -m "feat(plex): music VFD tiers (title/artist/album)"
```

---

## Task 11: Plex TV/movie metadata fetch + tiers

**Files:**
- Create: `internal/adapters/plex/video_metadata.go`, `internal/adapters/plex/video_metadata_test.go`
- Modify: `internal/adapters/plex/transcode.go:262-292` (extend `pmsMediaContainer.Video`)
- Modify: `internal/adapters/plex/companion.go:444-449` (`sessionRequestForPlay`)

- [ ] **Step 1: Extend the XML container**

In `internal/adapters/plex/transcode.go`, replace the `Video []struct {...}` field of `pmsMediaContainer` (lines 263-274) with one that also captures the display attributes:

```go
	Video []struct {
		Type             string `xml:"type,attr"`
		Title            string `xml:"title,attr"`
		GrandparentTitle string `xml:"grandparentTitle,attr"`
		ParentTitle      string `xml:"parentTitle,attr"`
		Index            int    `xml:"index,attr"`
		ParentIndex      int    `xml:"parentIndex,attr"`
		Year             int    `xml:"year,attr"`
		Media            []struct {
			Part []struct {
				ID     string `xml:"id,attr"`
				Stream []struct {
					ID         string `xml:"id,attr"`
					StreamType string `xml:"streamType,attr"`
					Key        string `xml:"key,attr"`
				} `xml:"Stream"`
			} `xml:"Part"`
		} `xml:"Media"`
	} `xml:"Video"`
```

(The `Track []struct{...}` field and everything else in the container are unchanged. `PartIDFor` still compiles — it only reads `v.Media`.)

- [ ] **Step 2: Write the failing test for `VideoMetadataFor` + tier mapping**

Create `internal/adapters/plex/video_metadata_test.go`:

```go
package plex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

func TestVideoMetadataFor_Episode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<MediaContainer><Video type="episode" title="The Constant" grandparentTitle="Lost" index="5" parentIndex="4" year="2008"/></MediaContainer>`))
	}))
	defer srv.Close()
	md, ok, err := VideoMetadataFor(context.Background(), srv.URL, "/library/metadata/1", "tok")
	if err != nil || !ok {
		t.Fatalf("VideoMetadataFor ok=%v err=%v", ok, err)
	}
	d := plexVideoDisplay(md, "fallback")
	if d.Primary != "Lost" || d.Secondary != "The Constant" || d.Tertiary != "S04E05 · 2008" {
		t.Fatalf("episode display = %+v", d)
	}
}

func TestPlexVideoDisplay_Movie(t *testing.T) {
	d := plexVideoDisplay(VideoMetadata{Type: "movie", Title: "Blade Runner 2049", Year: 2017}, "fallback")
	if d.Primary != "Blade Runner 2049" || d.Secondary != "2017" || d.Tertiary != "" {
		t.Fatalf("movie display = %+v", d)
	}
}

func TestPlexVideoDisplay_FallbackWhenEmpty(t *testing.T) {
	d := plexVideoDisplay(VideoMetadata{}, "Controller Title")
	if d.Primary != "Controller Title" {
		t.Fatalf("fallback display = %+v", d)
	}
	_ = core.DisplayMetadata{}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/adapters/plex -run 'VideoMetadataFor|PlexVideoDisplay' -v`
Expected: FAIL — `VideoMetadataFor` / `plexVideoDisplay` / `VideoMetadata` undefined.

- [ ] **Step 4: Implement `VideoMetadataFor` and the tier mapping**

Create `internal/adapters/plex/video_metadata.go`:

```go
package plex

import (
	"context"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// VideoMetadata is the slice of PMS /library/metadata video attributes
// the VFD needs. Reuses the existing fetchMetadata path (transcode.go).
type VideoMetadata struct {
	Type    string // "movie" | "episode"
	Title   string // episode title (episode) or movie title (movie)
	Show    string // grandparentTitle (episode only)
	Season  int    // parentIndex (episode only)
	Episode int    // index (episode only)
	Year    int
}

// VideoMetadataFor fetches and decodes the first <Video> element under
// the media key. ok=false when the response carries no Video element
// (e.g. the key is music or the fetch returned nothing useful).
func VideoMetadataFor(ctx context.Context, serverURL, mediaKey, token string) (VideoMetadata, bool, error) {
	mc, err := fetchMetadata(ctx, serverURL, mediaKey, token)
	if err != nil {
		return VideoMetadata{}, false, err
	}
	if len(mc.Video) == 0 {
		return VideoMetadata{}, false, nil
	}
	v := mc.Video[0]
	return VideoMetadata{
		Type:    v.Type,
		Title:   v.Title,
		Show:    v.GrandparentTitle,
		Season:  v.ParentIndex,
		Episode: v.Index,
		Year:    v.Year,
	}, true, nil
}

// plexVideoDisplay maps video metadata onto the three VFD tiers.
// Episode: show-first (Primary=show, Secondary=episode, Tertiary=S·E·year).
// Movie: Primary=title, Secondary=year. Falls back to the controller
// title when metadata is absent.
func plexVideoDisplay(md VideoMetadata, fallbackTitle string) core.DisplayMetadata {
	switch md.Type {
	case "episode":
		return core.DisplayMetadata{
			Primary:   firstNonEmpty(md.Show, fallbackTitle),
			Secondary: md.Title,
			Tertiary:  adapters.FormatSeasonEpisode(md.Season, md.Episode, md.Year),
		}
	case "movie":
		return core.DisplayMetadata{
			Primary:   firstNonEmpty(md.Title, fallbackTitle),
			Secondary: adapters.FormatSeasonEpisode(0, 0, md.Year),
		}
	default:
		return core.DisplayMetadata{Primary: firstNonEmpty(md.Title, fallbackTitle)}
	}
}
```

(`firstNonEmpty` already exists in the `plex` package — used at companion.go:382.)

- [ ] **Step 5: Wire the fetch into the video path**

In `internal/adapters/plex/companion.go`, replace `sessionRequestForPlay` (444-449) with a version that fetches video metadata (bounded to 2s, mirroring `musicMetadataForPlay` at line 250) and sets `DisplayMetadata`:

```go
func (c *Companion) sessionRequestForPlay(ctx context.Context, p PlayMediaRequest, preset core.ModelinePreset) core.SessionRequest {
	if md, ok := c.musicMetadataForPlay(ctx, p); ok {
		return c.musicSessionRequestForPlay(p, md)
	}
	req := c.sessionRequestForPreset(p, preset)
	req.DisplayMetadata = c.videoDisplayForPlay(ctx, p)
	return req
}

// videoDisplayForPlay fetches PMS video metadata under a 2s deadline and
// maps it to VFD tiers. On any failure/timeout it degrades to the
// controller-supplied title as Primary (no regression vs. today).
func (c *Companion) videoDisplayForPlay(ctx context.Context, p PlayMediaRequest) core.DisplayMetadata {
	lookupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	md, ok, err := VideoMetadataFor(lookupCtx, p.serverURL(), p.MediaKey, p.PlexToken)
	if err != nil {
		slog.Debug("plex video metadata lookup failed", "key", p.MediaKey, "err", err)
	}
	if !ok {
		return core.DisplayMetadata{Primary: p.Title}
	}
	return plexVideoDisplay(md, p.Title)
}
```

(`time` and `log/slog` are already imported in companion.go.)

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/adapters/plex -run 'VideoMetadataFor|PlexVideoDisplay|SessionRequestForPlay' -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/adapters/plex/video_metadata.go internal/adapters/plex/video_metadata_test.go internal/adapters/plex/transcode.go internal/adapters/plex/companion.go
git commit -m "feat(plex): fetch TV/movie metadata and compose VFD tiers (show-first)"
```

---

## Task 12: Jellyfin TV/movie/music tiers

**Files:**
- Modify: `internal/adapters/jellyfin/playback.go` — DTOs (73-79, 115-122), `PlaybackInfoResult` (38-49), `ItemMetadataResult` (104-113), constructors (~219, ~280), `mergePlaybackMetadata` (291-311), `buildSessionRequest` (359-390)
- Test: `internal/adapters/jellyfin/playback_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/adapters/jellyfin/playback_test.go`:

```go
func TestJellyfinDisplayMetadata(t *testing.T) {
	episode := PlaybackInfoResult{ItemType: "Episode", Title: "The Constant", SeriesName: "Lost", Season: 4, Episode: 5, Year: 2008}
	if d := jellyfinDisplayMetadata(episode); d.Primary != "Lost" || d.Secondary != "The Constant" || d.Tertiary != "S04E05 · 2008" {
		t.Fatalf("episode = %+v", d)
	}
	movie := PlaybackInfoResult{ItemType: "Movie", Title: "Blade Runner 2049", Year: 2017}
	if d := jellyfinDisplayMetadata(movie); d.Primary != "Blade Runner 2049" || d.Secondary != "2017" {
		t.Fatalf("movie = %+v", d)
	}
	music := PlaybackInfoResult{ItemType: "Audio", Title: "Midnight City", Artist: "M83", Album: "Hurry Up"}
	if d := jellyfinDisplayMetadata(music); d.Primary != "Midnight City" || d.Secondary != "M83" || d.Tertiary != "Hurry Up" {
		t.Fatalf("music = %+v", d)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/jellyfin -run TestJellyfinDisplayMetadata -v`
Expected: FAIL — undefined `jellyfinDisplayMetadata`; `PlaybackInfoResult` lacks `SeriesName`/`Season`/`Episode`/`Year`.

- [ ] **Step 3: Add fields to the DTOs and result structs**

In `internal/adapters/jellyfin/playback.go`:

In `playbackInfoResponseDTO.Item` (73-79), add after `RunTimeTicks`:

```go
			SeriesName        string `json:"SeriesName"`
			IndexNumber       int    `json:"IndexNumber"`
			ParentIndexNumber int    `json:"ParentIndexNumber"`
			ProductionYear    int    `json:"ProductionYear"`
```

In `itemMetadataDTO` (115-122), add after `RunTimeTicks`:

```go
	SeriesName        string `json:"SeriesName"`
	IndexNumber       int    `json:"IndexNumber"`
	ParentIndexNumber int    `json:"ParentIndexNumber"`
	ProductionYear    int    `json:"ProductionYear"`
```

In `PlaybackInfoResult` (38-49) and `ItemMetadataResult` (104-113), add to each:

```go
	SeriesName string
	Season     int
	Episode    int
	Year       int
```

- [ ] **Step 4: Populate the new fields in the constructors and merge**

In the `FetchPlaybackInfo` return literal (~219-229), add:

```go
		SeriesName: dto.Item.SeriesName,
		Season:     dto.Item.ParentIndexNumber,
		Episode:    dto.Item.IndexNumber,
		Year:       dto.Item.ProductionYear,
```

In `itemMetadataFromDTO` (~280-288), add:

```go
		SeriesName: dto.SeriesName,
		Season:     dto.ParentIndexNumber,
		Episode:    dto.IndexNumber,
		Year:       dto.ProductionYear,
```

In `mergePlaybackMetadata` (291-311), before `return info`, add:

```go
	if info.SeriesName == "" {
		info.SeriesName = meta.SeriesName
	}
	if info.Season == 0 {
		info.Season = meta.Season
	}
	if info.Episode == 0 {
		info.Episode = meta.Episode
	}
	if info.Year == 0 {
		info.Year = meta.Year
	}
```

- [ ] **Step 5: Add `jellyfinDisplayMetadata` and set it on the request**

In `internal/adapters/jellyfin/playback.go`, add the helper (near `buildSessionRequest`):

```go
// jellyfinDisplayMetadata maps a negotiated PlaybackInfoResult onto the
// three VFD tiers. Episode: show-first. Movie: title + year. Audio:
// title/artist/album. Unknown types: title only.
func jellyfinDisplayMetadata(info PlaybackInfoResult) core.DisplayMetadata {
	switch {
	case strings.EqualFold(info.ItemType, "Episode"):
		primary := info.SeriesName
		if primary == "" {
			primary = info.Title
		}
		return core.DisplayMetadata{
			Primary:   primary,
			Secondary: info.Title,
			Tertiary:  adapters.FormatSeasonEpisode(info.Season, info.Episode, info.Year),
		}
	case strings.EqualFold(info.ItemType, "Movie"):
		return core.DisplayMetadata{
			Primary:   info.Title,
			Secondary: adapters.FormatSeasonEpisode(0, 0, info.Year),
		}
	case info.MediaKind == core.MediaKindMusic || strings.EqualFold(info.ItemType, "Audio"):
		return core.DisplayMetadata{Primary: info.Title, Secondary: info.Artist, Tertiary: info.Album}
	default:
		return core.DisplayMetadata{Primary: info.Title}
	}
}
```

In `buildSessionRequest` (359-390), add to the `core.SessionRequest{...}` literal (alongside `Title: in.PlayInfo.Title,`):

```go
		DisplayMetadata: jellyfinDisplayMetadata(in.PlayInfo),
```

Ensure the file imports `github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters` (add to the import block if missing; `strings` is already imported).

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/adapters/jellyfin -run 'JellyfinDisplayMetadata|Playback' -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/adapters/jellyfin/playback.go internal/adapters/jellyfin/playback_test.go
git commit -m "feat(jellyfin): parse series/index/year and compose VFD tiers"
```

---

## Task 13: URL / YouTube tiers (yt-dlp channel + upload date)

**Files:**
- Modify: `internal/adapters/url/ytdlp/resolver.go:53-60` (`Resolution`), `:144-154` (raw struct) + the two `Resolution{...}` return sites
- Modify: `internal/adapters/url/play.go` (capture channel/date; set `DisplayMetadata`)
- Test: `internal/adapters/url/ytdlp/resolver_test.go`, `internal/adapters/url/play_test.go`

- [ ] **Step 1: Write the failing resolver test**

Append to `internal/adapters/url/ytdlp/resolver_test.go` (mirror an existing resolver test's `Runner` stub that returns canned JSON stdout):

```go
func TestResolve_ParsesChannelAndUploadDate(t *testing.T) {
	jsonOut := `{"url":"https://v/stream.mp4","title":"Repaired a Trinitron","channel":"Tech Connections","upload_date":"20240315"}`
	runner := &stubRunner{stdouts: [][]byte{[]byte(jsonOut)}}
	r := &Resolver{Binary: "yt-dlp", Timeout: time.Second, Runner: runner}
	res, err := r.Resolve(context.Background(), "https://example/watch", "best", "")
	if err != nil {
		t.Fatalf("Resolve err = %v", err)
	}
	if res.Channel != "Tech Connections" || res.UploadDate != "20240315" {
		t.Fatalf("res = %+v", res)
	}
}
```

> `stubRunner{stdouts: [][]byte{...}}` matches the existing resolver_test.go stub (resolver_test.go:13, used at :53).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/url/ytdlp -run TestResolve_ParsesChannelAndUploadDate -v`
Expected: FAIL — `res.Channel` / `res.UploadDate` undefined.

- [ ] **Step 3: Extend the structs and populate**

In `internal/adapters/url/ytdlp/resolver.go`, add to `Resolution` (after `Title`, line 59):

```go
	Channel    string // yt-dlp channel/uploader — VFD secondary
	UploadDate string // yt-dlp upload_date, raw "YYYYMMDD" — formatted by the caller
```

Add to the raw struct (after `Title`, line 148):

```go
		Channel     string `json:"channel"`
		Uploader    string `json:"uploader"`
		UploadDate  string `json:"upload_date"`
```

At **each** site that constructs the returned `&Resolution{...}` (the dual-stream DASH path and the single-stream fallback path, both later in `Resolve`), add these two fields:

```go
		Channel:    firstNonEmptyStr(raw.Channel, raw.Uploader),
		UploadDate: raw.UploadDate,
```

If a `firstNonEmptyStr` helper does not already exist in the package, add it to `resolver.go`:

```go
func firstNonEmptyStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
```

- [ ] **Step 4: Set `DisplayMetadata` in the URL adapter**

In `internal/adapters/url/play.go`, in the yt-dlp resolution branch where `resolvedTitle` is assigned from the resolver result (the block around lines 204-232 that calls `resolver.Resolve` and reads `resolved.Title`), capture two more locals alongside `resolvedTitle`:

```go
	resolvedChannel := resolved.Channel
	resolvedUploadDate := resolved.UploadDate
```

(Declare `resolvedChannel`/`resolvedUploadDate` as `string` in the same scope as `resolvedTitle` so they default to empty on the non-yt-dlp path.)

Then, after the `req := core.SessionRequest{...}` literal (after line 292), add:

```go
	req.DisplayMetadata = core.DisplayMetadata{Primary: title}
	if resolvedChannel != "" || resolvedUploadDate != "" {
		req.DisplayMetadata.Secondary = resolvedChannel
		req.DisplayMetadata.Tertiary = adapters.FormatUploadDate(resolvedUploadDate)
	} else {
		req.DisplayMetadata.Secondary = "URL"
	}
```

Ensure `internal/adapters` is imported in `play.go` (add if missing).

- [ ] **Step 5: Write a focused URL-adapter test (optional but recommended)**

If `play_test.go` already exercises `castURLWithStarter` with a stub resolver, add an assertion that a yt-dlp-resolved cast yields `DisplayMetadata{Primary: <title>, Secondary: <channel>, Tertiary: "2024-03-15"}` and a direct URL yields `Secondary: "URL"`. Reuse the existing test's harness.

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/adapters/url/... -run 'Resolve|Cast|Display' -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/adapters/url/ytdlp/resolver.go internal/adapters/url/ytdlp/resolver_test.go internal/adapters/url/play.go internal/adapters/url/play_test.go
git commit -m "feat(url): surface yt-dlp channel + upload date as VFD tiers"
```

---

## Task 14: Streams / saved-cast tiers

**Files:**
- Modify: `internal/adapters/streams/playback.go` — direct-HLS request (341-353) and yt-dlp-resolved request (~429-441), plus a helper
- Test: `internal/adapters/streams/playback_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/adapters/streams/playback_test.go`:

```go
func TestStreamsDisplayMetadata(t *testing.T) {
	d := streamsDisplayMetadata("Adult Swim", "Toonami Aftermath", "Action Block")
	if d.Primary != "Toonami Aftermath" || d.Secondary != "Adult Swim" || d.Tertiary != "Action Block" {
		t.Fatalf("d = %+v", d)
	}
	// When the item title equals the channel, the tertiary collapses.
	same := streamsDisplayMetadata("YouTube", "Lofi Radio", "Lofi Radio")
	if same.Tertiary != "" {
		t.Fatalf("expected empty tertiary, got %+v", same)
	}
	// Empty channel falls back to the item title as primary.
	fb := streamsDisplayMetadata("YouTube", "", "Some Video")
	if fb.Primary != "Some Video" {
		t.Fatalf("fallback primary = %+v", fb)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/streams -run TestStreamsDisplayMetadata -v`
Expected: FAIL — undefined `streamsDisplayMetadata`.

- [ ] **Step 3: Add the helper**

In `internal/adapters/streams/playback.go`, add:

```go
// streamsDisplayMetadata composes VFD tiers for a stream/saved cast:
// Primary = channel name (the recognizable station), Secondary =
// provider, Tertiary = the specific item title when it differs from the
// channel (the "what's on right now" within a channel). Empty channel
// falls back to the item title as Primary. (The spec names "group" for
// the tertiary; the per-item title is what's in scope at this callsite
// and is more informative for now-playing — see plan handoff note.)
func streamsDisplayMetadata(providerName, channelName, itemTitle string) core.DisplayMetadata {
	channel := strings.TrimSpace(channelName)
	item := strings.TrimSpace(itemTitle)
	if channel == "" {
		channel = item
	}
	d := core.DisplayMetadata{Primary: channel, Secondary: strings.TrimSpace(providerName)}
	if item != "" && !strings.EqualFold(item, channel) {
		d.Tertiary = item
	}
	return d
}
```

(`strings` and `core` are already imported in this file.)

- [ ] **Step 4: Set it on both request sites**

In the direct-HLS branch, add to the `core.SessionRequest{...}` literal (341-353, alongside `Title: title,`):

```go
		DisplayMetadata: streamsDisplayMetadata(q.ProviderName, q.ChannelName, streamSessionTitle(item, "")),
```

In the yt-dlp-resolved branch (~429-441, alongside `Title: title,`):

```go
		DisplayMetadata: streamsDisplayMetadata(q.ProviderName, q.ChannelName, streamSessionTitle(item, resolved.Title)),
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/adapters/streams -run 'StreamsDisplayMetadata|Playback' -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/adapters/streams/playback.go internal/adapters/streams/playback_test.go
git commit -m "feat(streams): VFD tiers (channel/provider/item) for streams + saved casts"
```

---

## Task 15: DLNA title plumbing + tiers

**Files:**
- Modify: `internal/adapters/dlna/play.go:406-414` (set `Title` + `DisplayMetadata` from the already-parsed DIDL metadata)
- Test: `internal/adapters/dlna/play_test.go`

The adapter already stores parsed DIDL-Lite metadata: `a.loadedMeta DIDLMetadata` (adapter.go:150), set during `SetAVTransportURI` (set_avtransport_uri.go:68) and read elsewhere under `a.mu` (avtransport.go:119). `DIDLMetadata.Title` exists (metadata.go:170). `play()` currently sets NO `Title` — we add `Title` + `DisplayMetadata` from `a.loadedMeta.Title`, so **no new struct field is needed.**

- [ ] **Step 1: Confirm the source field**

`a.loadedMeta.Title` (field of type `DIDLMetadata`, adapter.go:150) holds the parsed title; `play()` reads it under `a.mu`. No new field is required.

- [ ] **Step 2: Write the failing test**

Append to `internal/adapters/dlna/play_test.go` a test asserting that after a DIDL title is recorded, the built request carries it. If the request builder is not directly callable, add a small unit test on a new helper `dlnaDisplayMetadata(title string) core.DisplayMetadata`:

```go
func TestDLNADisplayMetadata(t *testing.T) {
	d := dlnaDisplayMetadata("Big Buck Bunny")
	if d.Primary != "Big Buck Bunny" {
		t.Fatalf("d = %+v", d)
	}
	if got := dlnaDisplayMetadata("").Primary; got != "" {
		t.Fatalf("empty title primary = %q", got)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/adapters/dlna -run TestDLNADisplayMetadata -v`
Expected: FAIL — undefined `dlnaDisplayMetadata`.

- [ ] **Step 4: Implement**

Add the helper and use it in `play()` (the parsed title already lives on `a.loadedMeta`):

```go
func dlnaDisplayMetadata(title string) core.DisplayMetadata {
	return core.DisplayMetadata{Primary: strings.TrimSpace(title)}
}
```

In `play()` (406-414), read the stored title and set both `Title` and `DisplayMetadata` on the request literal:

```go
	a.mu.Lock()
	didlTitle := a.loadedMeta.Title
	a.mu.Unlock()
	req := core.SessionRequest{
		StreamURL:        playbackURI,
		Capabilities:     core.Capabilities{CanSeek: canSeek, CanPause: true},
		AdapterRef:       ref,
		DirectPlay:       canSeek,
		SeekOffsetMs:     seekOffsetMs,
		OnStop:           onStop,
		MediaInputPolicy: dlnaInputPolicyForSource(canSeek),
		Title:            didlTitle,
		DisplayMetadata:  dlnaDisplayMetadata(didlTitle),
	}
```

Ensure `strings` and `core` are imported (core already is).

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/adapters/dlna -run 'DLNADisplayMetadata|Play' -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/adapters/dlna/
git commit -m "feat(dlna): plumb DIDL title to VFD primary tier"
```

---

## Task 16: Torrent + AUX tiers

**Files:**
- Modify: `internal/adapters/torrent/session.go:202-219`, `internal/adapters/auxadapter/session.go:198-215`
- Test: each package's existing session test

- [ ] **Step 1: Write the failing tests**

In `internal/adapters/torrent/session_test.go` (or the file with the existing session test), add an assertion that the built request's `DisplayMetadata.Primary == s.Title`. In `internal/adapters/auxadapter/session_test.go`, add `DisplayMetadata.Primary == input.Name` and `DisplayMetadata.Secondary == "AUX"`. Reuse each package's existing request-construction test setup.

(If a package has no convenient test seam for the request literal, add a one-line assertion to the nearest existing test that already builds the request.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/adapters/torrent ./internal/adapters/auxadapter -run Display -v`
Expected: FAIL.

- [ ] **Step 3: Set the tiers**

In `internal/adapters/torrent/session.go`, add to the `core.SessionRequest{...}` literal (alongside `Title: s.Title,`):

```go
		DisplayMetadata: core.DisplayMetadata{Primary: s.Title},
```

In `internal/adapters/auxadapter/session.go`, add to the `core.SessionRequest{...}` literal (alongside `Title: input.Name,`):

```go
		DisplayMetadata: core.DisplayMetadata{Primary: input.Name, Secondary: "AUX"},
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/adapters/torrent ./internal/adapters/auxadapter -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/torrent/session.go internal/adapters/auxadapter/session.go internal/adapters/torrent/session_test.go internal/adapters/auxadapter/session_test.go
git commit -m "feat(torrent,aux): VFD primary tiers"
```

---

## Task 17: Full verification + manual CRT smoke

**Files:** none (verification only)

- [ ] **Step 1: Lint + unit tests + race**

Run:
```bash
make lint
make test
go test -race ./...
```
Expected: all green. Fix any remaining `Title`/`Marquee` references the compiler flags.

- [ ] **Step 2: Integration tests**

Run: `make test-integration`
Expected: green (requires ffmpeg + ffprobe on PATH).

- [ ] **Step 3: Manual CRT overlay smoke (title-first)**

Run the bridge against `fake-mister` with a music cast and confirm the CRT overlay draws **title on top**, then artist, then album, all scrolling when long:
```bash
./fake-mister -addr :32100 -out ./dumps -png-every 30
# In config.toml set bridge.mister.host = "127.0.0.1", run the bridge,
# cast a music item, inspect ./dumps/*.png for title-first overlay order.
```

- [ ] **Step 4: Manual VFD smoke (three rows + scroll)**

Load the receiver page (`/receiver`), cast items of several types (music, a TV episode, a movie, a YouTube link, a stream), and confirm: three rows populate per the contract, empty tiers collapse, and over-long rows scroll smoothly.

- [ ] **Step 5: Final commit (if any test fixups were needed)**

```bash
git add -A
git commit -m "test: VFD multi-row metadata end-to-end green across all CI gates"
```

---

## Self-review notes

- **Spec coverage:** music (T10/T12/T16-aux), TV (T11/T12), movie (T11/T12), YouTube (T13), streams+saved-cast (T14), DLNA (T15), torrent (T16), idle (T2), three-tier render+scroll (T2-T7), CRT title-first (T8), shared formatting (T9), core model (T1). All spec sections map to a task.
- **Deliberate spec deviation:** streams **tertiary** uses the per-item title (when distinct from the channel) rather than the channel "group" — the group name is not in scope at the playback callsite, and the item title is more informative for now-playing. Flagged in Task 14 and the handoff.
- **Type consistency:** `DisplayMetadata{Primary,Secondary,Tertiary}`, `StatusHomeView.Display`, `VFDData.Primary/Secondary/Tertiary`, SSE `primary/secondary/tertiary`, attrs `data-vfd-primary/-secondary/-tertiary`, classes `tier-primary/-secondary/-tertiary` + `is-empty`/`is-scrolling`, helpers `adapters.FormatSeasonEpisode`/`FormatUploadDate` — used identically across all tasks.
- **Compile-unit caveat:** Tasks 2-4 are one cascading rename and commit together (noted inline).
