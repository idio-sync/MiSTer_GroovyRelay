# Receiver Chassis Input Row & Preset Bank Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire the chassis input row to the existing `adapters.QuickCastProvider` interface (URL + torrent paste) and make the 12-slot preset bank render and click-cast bundled Streams catalog entries from the v24 mockup, with typed cast-error propagation through a new `adapters.QuickCastError`.

**Architecture:** Two new chassis HTTP routes (`POST /receiver/cast`, `POST /receiver/preset/{slot}/cast`) call into existing adapter interfaces. URL and torrent adapters' `HandleQuickCast` returns are wrapped in a new `*adapters.QuickCastError` so the chassis can extract status + chip text via `errors.As`. Streams adapter implements two new narrow interfaces (`PresetViewer`, `PresetCaster`) wired through chassis `Config`. Client-side LIT state derives from the existing `transport` SSE event's `AdapterRef`; no new SSE event.

**Tech Stack:** Go 1.26 stdlib (`context`, `encoding/json`, `errors`, `net/http`, `net/url`, `strings`), existing internal packages (`internal/adapters`, `internal/adapters/streams`, `internal/adapters/url`, `internal/adapters/torrent`, `internal/chassis`, `internal/core`), vanilla ES2022 (no bundler). No new go.mod dependencies.

**Spec:** [docs/superpowers/specs/2026-05-25-receiver-chassis-input-and-presets-design.md](../specs/2026-05-25-receiver-chassis-input-and-presets-design.md)

---

## File Structure

**New files:**

| Path | Responsibility |
|---|---|
| `internal/adapters/preset.go` | `PresetEntry`, `PresetViewer`, `PresetCaster` interfaces |
| `internal/adapters/streams/preset.go` | Streams adapter implementations of `PresetViewer` and `PresetCaster`; `bundledChassisPresets` is in `assets.go` |
| `internal/adapters/streams/preset_test.go` | BundledPresets coverage, CastPreset slot validation + startup-snapshot guard, NOT READY error |
| `internal/chassis/cast.go` | `castKindToTab` / `valuesKeyForTab` / `fileFieldForTab` maps, `detectCastKind`, `writeCastJSON`, startup verification helpers, `POST /receiver/cast` handler |
| `internal/chassis/cast_test.go` | Kind detection, startup verification, handler tests, response shape, error wrapping |
| `internal/chassis/preset.go` | `POST /receiver/preset/{slot}/cast` handler |
| `internal/chassis/preset_test.go` | Slot validation, nil-caster 404, error mapping |
| `internal/chassis/static/input-cast.js` | Live kind detection, CAST submit, file upload, error chip |
| `internal/chassis/static/preset-bank.js` | Subscribe to `transport`, derive LIT, click handler |

**Modified files:**

| Path | Change |
|---|---|
| `internal/adapters/playback.go` | Add `QuickCastError` type with `Error()` / `Unwrap()`; export `MaxQuickCastBytes` const |
| `internal/adapters/preset.go` | (created above) |
| `internal/adapters/url/playback_provider.go` | Wrap `HandleQuickCast` returns in `*QuickCastError`; lift `castURLWithHLSBuffer`'s status int; wrap `IsEnabled()` false |
| `internal/adapters/url/playback_provider_test.go` | Cover the new wrapping per status code |
| `internal/adapters/torrent/playback_provider.go` | Convert top-level `fmt.Errorf` returns to typed `*TorrentError`; wrap `HandleQuickCast` returns in `*QuickCastError` via per-kind chip table |
| `internal/adapters/torrent/playback_provider_test.go` | Cover the new wrapping for every `TorrentError.Kind` |
| `internal/adapters/torrent/errors.go` | Add `wrapQuickCastError(err error) *adapters.QuickCastError` helper |
| `internal/adapters/streams/assets.go` | Add `bundledChassisPresets` constant |
| `internal/chassis/server.go` | Add `Config.PresetViewer` and `Config.PresetCaster`; store on `*Server`; register two new routes in `Mount` |
| `internal/chassis/data.go` | Extend `PresetSlot` struct; populate in `idleSnapshot` (all 12 with `Slot: i+1`) and `snapshotFromStatusView` (with LIT derivation from `transport.AdapterRef`) |
| `internal/chassis/templates/shell.html` | Add `<script>` tags for `input-cast.js` and `preset-bank.js` after `vfd-live.js` |
| `internal/chassis/templates/input-row.html` | Add `data-chip-kind` and `data-cast-state` attributes; wire CAST button id |
| `internal/chassis/templates/preset-bank.html` | Add `data-slot` / `data-provider` / `data-channel` attributes; conditional ` · LIVE` badge suffix |
| `internal/chassis/static/chassis.css` | Add `.chip[data-chip-kind="err"]`, `.badge.mtv` / `.cartoon` / `.toonami`, `.browse-btn[disabled]` rules |
| `internal/chassis/import_check_test.go` | Extend forbidden list to include concrete adapter sub-packages (`streams`, `url`, `torrent`, `plex`, `jellyfin`, `dlna`) |
| `internal/chassis/chassis_test.go` | Template-render tests for new data attributes; script-presence tests; no-fake-values lint for new JS files |
| `cmd/mister-groovy-relay/main.go` | Pass streams adapter as `PresetViewer` and `PresetCaster` in `chassis.Config` |
| `tests/integration/chassis_test.go` | End-to-end cast and preset POSTs against a real chassis + fake adapters |

**Files intentionally unchanged:**

- `internal/ui/*` and `internal/uiserver/*` — 3A is additive under `/receiver/*`.
- All Plex/Jellyfin/DLNA adapter packages — they don't implement `QuickCastProvider`; pasted URLs route through URL adapter as today.
- `internal/core/*` — no new core surface; existing transport SSE event already carries `AdapterRef`.

---

## Sequencing

Task 1 (extend `import_check_test.go`) MUST land as a standalone commit at the start of the branch — it guards every chassis change after it. If a later task accidentally introduces `import "internal/adapters/streams"` in a chassis source file, CI will fail immediately.

Tasks 2-5 are foundational: they add the `QuickCastError` type and update URL/torrent adapters to wrap their errors with it. Task 6 introduces the streams `PresetViewer`/`PresetCaster` interfaces; Task 7 implements them.

Tasks 8-11 add the chassis-side data and HTTP plumbing. Tasks 12-13 add templates, JS, and CSS. Task 14 wires `main.go` and the integration test.

Each task is independently committable and testable.

---

## Task 1: Prerequisite — Extend `import_check_test.go`

**Files:**
- Modify: `internal/chassis/import_check_test.go:26-33`

- [ ] **Step 1: Edit the chassis forbidden-imports list**

Open [internal/chassis/import_check_test.go](../../../internal/chassis/import_check_test.go) and locate the rule block for `internal/chassis` (around line 26). Replace its `forbidden` slice with the extended list:

```go
{
    fromPkg: modulePath + "/internal/chassis",
    fromDir: filepath.Join(repoRoot, "internal", "chassis"),
    forbidden: []string{
        modulePath + "/internal/ui",
        modulePath + "/internal/uiserver",
        modulePath + "/internal/adapters/auxadapter",
        modulePath + "/internal/adapters/streams",
        modulePath + "/internal/adapters/url",
        modulePath + "/internal/adapters/torrent",
        modulePath + "/internal/adapters/plex",
        modulePath + "/internal/adapters/jellyfin",
        modulePath + "/internal/adapters/dlna",
    },
},
```

- [ ] **Step 2: Run the test to confirm it passes against current chassis source**

```bash
go test ./internal/chassis -run TestProductionImports_NoCrossPackageCoupling -v
```

Expected: PASS. Chassis source has no such imports today; the extension is defense-in-depth for the rest of 3A.

- [ ] **Step 3: Commit**

```bash
git add internal/chassis/import_check_test.go
git commit -m "test(chassis): forbid chassis imports of concrete adapter packages"
```

---

## Task 2: Add `adapters.QuickCastError` Type

**Files:**
- Modify: `internal/adapters/playback.go`
- Create: `internal/adapters/playback_test.go` (if absent — else extend)

- [ ] **Step 1: Write failing tests**

Create or extend [internal/adapters/playback_test.go](../../../internal/adapters/playback_test.go) with:

```go
package adapters

import (
	"errors"
	"fmt"
	"testing"
)

func TestQuickCastError_ErrorPrefersMessageOverChip(t *testing.T) {
	e := &QuickCastError{Status: 400, Chip: "BAD URL", Message: "url could not be parsed"}
	if got := e.Error(); got != "url could not be parsed" {
		t.Errorf("Error() = %q, want %q", got, "url could not be parsed")
	}
}

func TestQuickCastError_ErrorFallsBackToCauseThenChip(t *testing.T) {
	cause := fmt.Errorf("underlying failure")
	e := &QuickCastError{Status: 500, Chip: "CAST FAILED", Cause: cause}
	if got := e.Error(); got != "underlying failure" {
		t.Errorf("Error() with cause = %q, want %q", got, "underlying failure")
	}
	e2 := &QuickCastError{Status: 409, Chip: "BLOCKED"}
	if got := e2.Error(); got != "BLOCKED" {
		t.Errorf("Error() chip fallback = %q, want %q", got, "BLOCKED")
	}
}

func TestQuickCastError_UnwrapReturnsCause(t *testing.T) {
	cause := fmt.Errorf("io: deadline exceeded")
	e := &QuickCastError{Status: 504, Chip: "TIMEOUT", Cause: cause}
	if got := errors.Unwrap(e); got != cause {
		t.Errorf("Unwrap() = %v, want %v", got, cause)
	}
}

func TestQuickCastError_ErrorsAsRoundTrip(t *testing.T) {
	wrapped := fmt.Errorf("wrap: %w", &QuickCastError{Status: 413, Chip: "FILE TOO BIG"})
	var qerr *QuickCastError
	if !errors.As(wrapped, &qerr) {
		t.Fatalf("errors.As failed to extract *QuickCastError from %v", wrapped)
	}
	if qerr.Status != 413 || qerr.Chip != "FILE TOO BIG" {
		t.Errorf("extracted = %+v, want Status=413 Chip=%q", qerr, "FILE TOO BIG")
	}
}

func TestQuickCastError_NilSafety(t *testing.T) {
	var e *QuickCastError
	if got := e.Error(); got != "" {
		t.Errorf("nil.Error() = %q, want empty", got)
	}
	if got := e.Unwrap(); got != nil {
		t.Errorf("nil.Unwrap() = %v, want nil", got)
	}
}

func TestMaxQuickCastBytes_Exported(t *testing.T) {
	// Sanity-check the exported constant exists and matches the legacy value.
	const want = 4*1024*1024 + 64*1024
	if MaxQuickCastBytes != want {
		t.Errorf("MaxQuickCastBytes = %d, want %d", MaxQuickCastBytes, want)
	}
}
```

- [ ] **Step 2: Run tests to confirm they fail**

```bash
go test ./internal/adapters -run "TestQuickCastError|TestMaxQuickCastBytes" -v
```

Expected: FAIL with `undefined: QuickCastError` and `undefined: MaxQuickCastBytes`.

- [ ] **Step 3: Implement `QuickCastError` and export the multipart cap**

In [internal/adapters/playback.go](../../../internal/adapters/playback.go), after the existing `QuickCastFile` type, append:

```go
// MaxQuickCastBytes caps multipart payloads accepted by QuickCastProvider
// implementations. Shared between /ui/playback/quick-cast and the chassis
// /receiver/cast route so the limit moves in lockstep.
const MaxQuickCastBytes = 4*1024*1024 + 64*1024

// QuickCastError is the typed error returned by QuickCastProvider
// implementations when a quick-cast attempt fails for a known reason.
// The chassis JSON route extracts Status/Chip via errors.As; the
// existing /ui route uses Error() for inline-banner rendering.
//
// Adapter implementations set Status to the HTTP status the chassis
// should emit and Chip to the short uppercase text the chassis chip
// displays. Cause is the wrapped underlying error (may be nil); Message
// is the human-readable form preferred by Error() when set.
type QuickCastError struct {
	Status  int
	Chip    string
	Message string
	Cause   error
}

func (e *QuickCastError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return e.Chip
}

func (e *QuickCastError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}
```

Also remove the local constant from `internal/ui/playback.go:212` (`const maxQuickCastMultipartBytes = 4*1024*1024 + 64*1024`) and replace its single use at line 218 with `adapters.MaxQuickCastBytes`:

```go
r.Body = http.MaxBytesReader(w, r.Body, adapters.MaxQuickCastBytes)
```

- [ ] **Step 4: Run tests to confirm they pass**

```bash
go test ./internal/adapters -run "TestQuickCastError|TestMaxQuickCastBytes" -v
go test ./internal/ui -run "TestQuickCast" -v
```

Expected: all PASS. The `/ui` tests still pass because `adapters.MaxQuickCastBytes == maxQuickCastMultipartBytes`.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/playback.go internal/adapters/playback_test.go internal/ui/playback.go
git commit -m "feat(adapters): add QuickCastError type and export MaxQuickCastBytes"
```

---

## Task 3: Add Preset Interfaces

**Files:**
- Create: `internal/adapters/preset.go`

- [ ] **Step 1: Create the interface file**

Create [internal/adapters/preset.go](../../../internal/adapters/preset.go):

```go
package adapters

import "context"

// PresetEntry is one slot in the chassis preset bank — a reference to
// a specific streams catalog entry plus the display metadata the
// chassis needs to render it.
//
// 3A: produced exclusively by the streams adapter's BundledPresets.
// Future per-source preset banks may produce these too, but only
// streams is registered as a PresetViewer in 3A.
type PresetEntry struct {
	Slot       int    // 1..12, 1-indexed to match the mockup
	ProviderID string // e.g. "mtv-rewind"
	ChannelID  string // e.g. "1stday"
	Title      string // "First Day on MTV" — rendered in the slot's name line
	BadgeLabel string // "MTV REWIND" — rendered in the slot's badge line
	BadgeClass string // "mtv" | "cartoon" | "toonami" — CSS hook for badge color
	Live       bool   // matches mockup `.preset.live` — always-on live channels
}

// PresetViewer returns the 12-slot preset bank snapshot. The chassis
// reads this once per page render. 3A treats the result as static for
// the lifetime of the bridge process; future user-edit specs may
// expose a notification channel.
type PresetViewer interface {
	BundledPresets() [12]PresetEntry
}

// PresetCaster fires a cast for a specific preset slot. Implementations
// look up the slot's catalog entry from their own state and start the
// appropriate session.
//
// Slot is 1-indexed (1..12) to match the URL path parameter and the
// mockup. Implementations MUST return a non-nil error for slots
// outside this range; chassis validates first as defense-in-depth.
type PresetCaster interface {
	CastPreset(ctx context.Context, slot int) error
}
```

- [ ] **Step 2: Verify the package compiles**

```bash
go build ./internal/adapters/...
```

Expected: clean exit.

- [ ] **Step 3: Commit**

```bash
git add internal/adapters/preset.go
git commit -m "feat(adapters): add PresetEntry, PresetViewer, PresetCaster interfaces"
```

---

## Task 4: Wrap URL Adapter Errors With `QuickCastError`

**Files:**
- Modify: `internal/adapters/url/playback_provider.go`
- Modify: `internal/adapters/url/playback_provider_test.go`

- [ ] **Step 1: Write failing tests**

Append to [internal/adapters/url/playback_provider_test.go](../../../internal/adapters/url/playback_provider_test.go):

```go
func TestHandleQuickCast_WrapsDisabledAsQuickCastErrorBlocked(t *testing.T) {
	a := newAdapterForTest(t) // existing helper; constructs disabled adapter
	a.Disable()                // existing method or set IsEnabled() false
	_, err := a.HandleQuickCast(context.Background(), adapters.QuickCastRequest{
		TabID:  "url",
		Values: map[string]string{"url": "https://example.test/video.mp4"},
	})
	var qerr *adapters.QuickCastError
	if !errors.As(err, &qerr) {
		t.Fatalf("err = %v, want *QuickCastError", err)
	}
	if qerr.Status != http.StatusConflict {
		t.Errorf("Status = %d, want 409", qerr.Status)
	}
	if qerr.Chip != "BLOCKED" {
		t.Errorf("Chip = %q, want BLOCKED", qerr.Chip)
	}
}

func TestHandleQuickCast_WrapsParseFailureAsBadURL(t *testing.T) {
	a := newAdapterForTest(t)
	_, err := a.HandleQuickCast(context.Background(), adapters.QuickCastRequest{
		TabID:  "url",
		Values: map[string]string{"url": ""},
	})
	var qerr *adapters.QuickCastError
	if !errors.As(err, &qerr) {
		t.Fatalf("err = %v, want *QuickCastError", err)
	}
	if qerr.Status != http.StatusBadRequest {
		t.Errorf("Status = %d, want 400", qerr.Status)
	}
	if qerr.Chip != "BAD URL" {
		t.Errorf("Chip = %q, want BAD URL", qerr.Chip)
	}
}

func TestHandleQuickCast_PreservesPlainErrorMessage(t *testing.T) {
	// /ui consumers still call err.Error() so the human-readable message
	// must be preserved through the wrap.
	a := newAdapterForTest(t)
	_, err := a.HandleQuickCast(context.Background(), adapters.QuickCastRequest{
		TabID:  "url",
		Values: map[string]string{"url": ""},
	})
	if err == nil {
		t.Fatal("err = nil, want non-nil")
	}
	if got := err.Error(); !strings.Contains(strings.ToLower(got), "url") {
		t.Errorf("Error() = %q, want a message mentioning url", got)
	}
}
```

Add imports as needed (`errors`, `net/http`, `strings`, and the adapters package).

The exact name of the existing helper (`newAdapterForTest`) and disabled-state setter (`Disable()`) may differ; use the existing test scaffolding patterns in the file.

- [ ] **Step 2: Run tests to confirm they fail**

```bash
go test ./internal/adapters/url -run "TestHandleQuickCast_Wraps" -v
```

Expected: FAIL — current `HandleQuickCast` returns plain `fmt.Errorf`, not `*QuickCastError`.

- [ ] **Step 3: Update `HandleQuickCast` to wrap errors**

In [internal/adapters/url/playback_provider.go](../../../internal/adapters/url/playback_provider.go), replace `HandleQuickCast` (starting at line 173):

```go
func (a *Adapter) HandleQuickCast(ctx context.Context, req adapters.QuickCastRequest) (adapters.QuickCastResult, error) {
	if !a.IsEnabled() {
		return adapters.QuickCastResult{}, &adapters.QuickCastError{
			Status:  http.StatusConflict,
			Chip:    "BLOCKED",
			Message: "url adapter is disabled",
		}
	}
	rawURL := strings.TrimSpace(req.Values["url"])
	if rawURL == "" {
		return adapters.QuickCastResult{}, &adapters.QuickCastError{
			Status:  http.StatusBadRequest,
			Chip:    "BAD URL",
			Message: "url is required",
		}
	}
	mode := strings.TrimSpace(req.Values["mode"])
	if mode == "" {
		mode = "auto"
	}
	hlsBufferMode := strings.TrimSpace(req.Values["hls_buffer"])
	if hlsBufferMode == "" {
		hlsBufferMode = "auto"
	}
	ref, _, status, err := a.castURLWithHLSBuffer(ctx, rawURL, mode, hlsBufferMode)
	if err != nil {
		return adapters.QuickCastResult{}, wrapURLCastError(status, err)
	}
	return adapters.QuickCastResult{Message: "cast started", AdapterRef: ref}, nil
}

// wrapURLCastError lifts the integer status returned by castURLWithHLSBuffer
// into a *QuickCastError with a chip derived from the status code. The
// underlying error is preserved as Cause so errors.Unwrap still works.
func wrapURLCastError(status int, err error) *adapters.QuickCastError {
	chip := "CAST FAILED"
	switch status {
	case http.StatusBadRequest:
		chip = "BAD URL"
	case http.StatusForbidden:
		chip = "BLOCKED"
	}
	if status == 0 {
		status = http.StatusInternalServerError
	}
	return &adapters.QuickCastError{
		Status:  status,
		Chip:    chip,
		Cause:   err,
		Message: err.Error(),
	}
}
```

Add `"net/http"` to the import block if not already present.

- [ ] **Step 4: Run tests to confirm they pass**

```bash
go test ./internal/adapters/url -v
```

Expected: all URL adapter tests PASS, including the new wrapping tests and the existing `/ui` quick-cast tests (`QuickCastError.Error()` still returns the human-readable message).

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/url/playback_provider.go internal/adapters/url/playback_provider_test.go
git commit -m "feat(url): wrap HandleQuickCast errors with adapters.QuickCastError"
```

---

## Task 5: Wrap Torrent Adapter Errors With `QuickCastError`

**Files:**
- Modify: `internal/adapters/torrent/playback_provider.go`
- Modify: `internal/adapters/torrent/errors.go`
- Modify: `internal/adapters/torrent/playback_provider_test.go`

- [ ] **Step 1: Write failing tests**

Append to [internal/adapters/torrent/playback_provider_test.go](../../../internal/adapters/torrent/playback_provider_test.go):

```go
func TestHandleQuickCast_WrapsDisabledAdapter(t *testing.T) {
	a := newAdapterForTest(t)
	a.Disable() // existing test helper or equivalent
	_, err := a.HandleQuickCast(context.Background(), adapters.QuickCastRequest{
		TabID:  "torrent-magnet",
		Values: map[string]string{"magnet": "magnet:?xt=urn:btih:abc"},
	})
	var qerr *adapters.QuickCastError
	if !errors.As(err, &qerr) {
		t.Fatalf("err = %v, want *QuickCastError", err)
	}
	if qerr.Status != http.StatusConflict || qerr.Chip != "BLOCKED" {
		t.Errorf("got Status=%d Chip=%q, want 409/BLOCKED", qerr.Status, qerr.Chip)
	}
	// Inner TorrentError must still be reachable.
	var terr *TorrentError
	if !errors.As(err, &terr) {
		t.Fatalf("inner TorrentError not reachable via errors.As: %v", err)
	}
	if terr.Kind != ErrDisabled {
		t.Errorf("inner kind = %q, want ErrDisabled", terr.Kind)
	}
}

func TestHandleQuickCast_WrapsTrafficNotAcknowledged(t *testing.T) {
	a := newAdapterForTest(t)
	// existing helper: enable adapter but leave traffic ack false
	a.Enable()
	_, err := a.HandleQuickCast(context.Background(), adapters.QuickCastRequest{
		TabID:  "torrent-magnet",
		Values: map[string]string{"magnet": "magnet:?xt=urn:btih:abc"},
	})
	var qerr *adapters.QuickCastError
	if !errors.As(err, &qerr) {
		t.Fatalf("err = %v, want *QuickCastError", err)
	}
	if qerr.Status != http.StatusForbidden || qerr.Chip != "BLOCKED" {
		t.Errorf("got Status=%d Chip=%q, want 403/BLOCKED", qerr.Status, qerr.Chip)
	}
}

func TestHandleQuickCast_WrapsByTorrentErrorKind(t *testing.T) {
	cases := []struct {
		name     string
		kind     TorrentErrorKind
		wantCode int
		wantChip string
	}{
		{"bad input", ErrBadInput, 400, "BAD INPUT"},
		{"upload too large", ErrUploadTooLarge, 413, "FILE TOO BIG"},
		{"metadata timeout", ErrMetadataTimeout, 504, "TIMEOUT"},
		{"no playable file", ErrNoPlayableFile, 422, "NO VIDEO"},
		{"expired token", ErrExpiredToken, 404, "NOT FOUND"},
		{"non-loopback", ErrNonLoopback, 403, "BLOCKED"},
		{"core start", ErrCoreStart, 500, "CAST FAILED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := &TorrentError{Kind: tc.kind, Message: "synthetic " + string(tc.kind)}
			got := wrapQuickCastError(raw)
			if got.Status != tc.wantCode {
				t.Errorf("Status = %d, want %d", got.Status, tc.wantCode)
			}
			if got.Chip != tc.wantChip {
				t.Errorf("Chip = %q, want %q", got.Chip, tc.wantChip)
			}
			if got.Cause != raw {
				t.Errorf("Cause = %v, want raw TorrentError", got.Cause)
			}
		})
	}
}

func TestHandleQuickCast_UntypedErrorPassesThroughUnwrapped(t *testing.T) {
	// Plain (non-TorrentError) errors should NOT be silently wrapped as
	// QuickCastError — the chassis collapses them to 500/CAST FAILED via
	// its own fallback. This ensures the wrapper helper doesn't fabricate
	// status codes for unfamiliar errors.
	raw := fmt.Errorf("synthetic untyped failure")
	if got := wrapQuickCastError(raw); got != nil {
		t.Errorf("wrapQuickCastError(plain) = %+v, want nil", got)
	}
}
```

Add imports: `errors`, `net/http`, `fmt`, plus the adapters package as needed.

- [ ] **Step 2: Run tests to confirm they fail**

```bash
go test ./internal/adapters/torrent -run "TestHandleQuickCast_Wraps|TestHandleQuickCast_Untyped" -v
```

Expected: FAIL with `undefined: wrapQuickCastError`.

- [ ] **Step 3: Add the chip mapping + wrapping helper**

Append to [internal/adapters/torrent/errors.go](../../../internal/adapters/torrent/errors.go):

```go
import "github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"

// torrentChipForKind maps each TorrentError.Kind to the chassis chip
// text. Statuses come from torrentErrorStatus (preserved from existing
// /ui behavior). The chassis route extracts both Status and Chip via
// errors.As.
var torrentChipForKind = map[TorrentErrorKind]string{
	ErrDisabled:               "BLOCKED",
	ErrTrafficNotAcknowledged: "BLOCKED",
	ErrNonLoopback:            "BLOCKED",
	ErrBadInput:               "BAD INPUT",
	ErrUploadTooLarge:         "FILE TOO BIG",
	ErrMetadataTimeout:        "TIMEOUT",
	ErrNoPlayableFile:         "NO VIDEO",
	ErrExpiredToken:           "NOT FOUND",
	ErrCoreStart:              "CAST FAILED",
}

// wrapQuickCastError wraps a *TorrentError as *adapters.QuickCastError
// for the chassis quick-cast route. Returns nil if err is not a
// *TorrentError — the chassis collapses plain errors to 500/CAST FAILED
// via its untyped fallback; the helper deliberately doesn't fabricate
// status codes for unfamiliar errors.
func wrapQuickCastError(err error) *adapters.QuickCastError {
	var terr *TorrentError
	if !errors.As(err, &terr) {
		return nil
	}
	chip, ok := torrentChipForKind[terr.Kind]
	if !ok {
		chip = "CAST FAILED"
	}
	return &adapters.QuickCastError{
		Status:  torrentErrorStatus(err),
		Chip:    chip,
		Message: terr.Error(),
		Cause:   err,
	}
}
```

- [ ] **Step 4: Update `HandleQuickCast` to wrap and to use typed `*TorrentError` at the top**

In [internal/adapters/torrent/playback_provider.go](../../../internal/adapters/torrent/playback_provider.go), replace the top-level `fmt.Errorf` returns and add a single wrap at the end:

```go
func (a *Adapter) HandleQuickCast(ctx context.Context, req adapters.QuickCastRequest) (adapters.QuickCastResult, error) {
	enabled := a.IsEnabled()
	a.mu.Lock()
	ack := a.cfg.TrafficAcknowledged
	a.mu.Unlock()
	if !enabled {
		return adapters.QuickCastResult{}, asQuickCastError(&TorrentError{
			Kind:    ErrDisabled,
			Message: "torrent adapter is disabled",
		})
	}
	if !ack {
		return adapters.QuickCastResult{}, asQuickCastError(&TorrentError{
			Kind:    ErrTrafficNotAcknowledged,
			Message: "BitTorrent traffic acknowledgement required",
		})
	}

	result, err := a.handleQuickCastDispatch(ctx, req)
	if err != nil {
		return adapters.QuickCastResult{}, asQuickCastError(err)
	}
	return result, nil
}

// handleQuickCastDispatch carries the body of the switch on req.TabID
// previously inlined in HandleQuickCast. Extracted so the single
// wrapping site at the top of HandleQuickCast covers every return.
func (a *Adapter) handleQuickCastDispatch(ctx context.Context, req adapters.QuickCastRequest) (adapters.QuickCastResult, error) {
	switch req.TabID {
	case "torrent-magnet":
		raw := strings.TrimSpace(req.Values["magnet"])
		if raw == "" {
			return adapters.QuickCastResult{}, &TorrentError{Kind: ErrBadInput, Message: "magnet is required"}
		}
		started, err := a.startMagnet(ctx, raw)
		if err != nil {
			return adapters.QuickCastResult{}, err
		}
		return adapters.QuickCastResult{Message: "torrent started", AdapterRef: started.AdapterRef}, nil
	case "torrent-file":
		if req.File == nil || req.File.Header == nil {
			return adapters.QuickCastResult{}, &TorrentError{Kind: ErrBadInput, Message: "torrent_file is required"}
		}
		f, err := req.File.Header.Open()
		if err != nil {
			return adapters.QuickCastResult{}, &TorrentError{Kind: ErrBadInput, Message: "open torrent_file: " + err.Error(), Err: err}
		}
		defer f.Close()
		body, err := io.ReadAll(io.LimitReader(f, maxTorrentUploadBytes+1))
		if err != nil {
			return adapters.QuickCastResult{}, &TorrentError{Kind: ErrBadInput, Message: "read torrent_file: " + err.Error(), Err: err}
		}
		if len(body) > maxTorrentUploadBytes {
			return adapters.QuickCastResult{}, &TorrentError{Kind: ErrUploadTooLarge, Message: "torrent file exceeds 4 MiB"}
		}
		started, err := a.startTorrentBytes(ctx, body)
		if err != nil {
			return adapters.QuickCastResult{}, err
		}
		return adapters.QuickCastResult{Message: "torrent started", AdapterRef: started.AdapterRef}, nil
	case "torrent-url":
		raw := strings.TrimSpace(req.Values["torrent_url"])
		if raw == "" {
			return adapters.QuickCastResult{}, &TorrentError{Kind: ErrBadInput, Message: "torrent_url is required"}
		}
		started, err := a.startTorrentURL(ctx, raw)
		if err != nil {
			return adapters.QuickCastResult{}, err
		}
		return adapters.QuickCastResult{Message: "torrent started", AdapterRef: started.AdapterRef}, nil
	default:
		return adapters.QuickCastResult{}, &TorrentError{Kind: ErrBadInput, Message: "unknown quick-cast tab " + req.TabID}
	}
}

// asQuickCastError wraps a *TorrentError as *adapters.QuickCastError if
// possible; otherwise returns the original error unchanged so the chassis
// untyped fallback handles it.
func asQuickCastError(err error) error {
	if q := wrapQuickCastError(err); q != nil {
		return q
	}
	return err
}
```

- [ ] **Step 5: Run tests to confirm they pass**

```bash
go test ./internal/adapters/torrent -v
```

Expected: all torrent adapter tests PASS, including new wrapping tests and existing tests for `startMagnet` / `startTorrentBytes` / `startTorrentURL` (their error paths still return raw `*TorrentError`, but the wrap at the top of `HandleQuickCast` now converts them).

- [ ] **Step 6: Commit**

```bash
git add internal/adapters/torrent/errors.go internal/adapters/torrent/playback_provider.go internal/adapters/torrent/playback_provider_test.go
git commit -m "feat(torrent): wrap HandleQuickCast errors with adapters.QuickCastError"
```

---

## Task 6: Bundled 12-Slot Preset List + Streams `BundledPresets`

**Files:**
- Modify: `internal/adapters/streams/assets.go`
- Create: `internal/adapters/streams/preset.go`
- Create: `internal/adapters/streams/preset_test.go`

- [ ] **Step 1: Write failing tests**

Create [internal/adapters/streams/preset_test.go](../../../internal/adapters/streams/preset_test.go):

```go
package streams

import (
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func TestBundledPresets_ReturnsTwelveEntries(t *testing.T) {
	a := &Adapter{}
	presets := a.BundledPresets()
	if len(presets) != 12 {
		t.Fatalf("len(BundledPresets) = %d, want 12", len(presets))
	}
}

func TestBundledPresets_EveryEntryResolvesAgainstBundledManifest(t *testing.T) {
	a := &Adapter{}
	presets := a.BundledPresets()
	manifest := bundledManifest()
	providers := map[string]*ProviderDefinition{}
	for i := range manifest.Providers {
		p := manifest.Providers[i]
		providers[p.ID] = &p
	}
	for i, p := range presets {
		prov, ok := providers[p.ProviderID]
		if !ok {
			t.Errorf("slot %d: ProviderID %q not in bundled manifest", i+1, p.ProviderID)
			continue
		}
		var found bool
		for _, c := range prov.Channels {
			if c.ID == p.ChannelID {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("slot %d: ChannelID %q not in provider %q channels", i+1, p.ChannelID, p.ProviderID)
		}
	}
}

func TestBundledPresets_BadgeClassWithinEnum(t *testing.T) {
	allowed := map[string]bool{"mtv": true, "cartoon": true, "toonami": true}
	a := &Adapter{}
	for i, p := range a.BundledPresets() {
		if !allowed[p.BadgeClass] {
			t.Errorf("slot %d: BadgeClass = %q, want one of {mtv, cartoon, toonami}", i+1, p.BadgeClass)
		}
	}
}

func TestBundledPresets_LiveFlagOnToonamiSlots(t *testing.T) {
	a := &Adapter{}
	presets := a.BundledPresets()
	for i, p := range presets {
		switch i + 1 {
		case 11, 12:
			if !p.Live {
				t.Errorf("slot %d: Live = false, want true (toonami)", i+1)
			}
		default:
			if p.Live {
				t.Errorf("slot %d: Live = true, want false (non-toonami)", i+1)
			}
		}
	}
	_ = adapters.PresetEntry{} // exercise the import
}

func TestBundledPresets_SlotsAre1Indexed(t *testing.T) {
	a := &Adapter{}
	for i, p := range a.BundledPresets() {
		if p.Slot != i+1 {
			t.Errorf("BundledPresets[%d].Slot = %d, want %d", i, p.Slot, i+1)
		}
	}
}
```

- [ ] **Step 2: Run tests to confirm they fail**

```bash
go test ./internal/adapters/streams -run "TestBundledPresets" -v
```

Expected: FAIL with `BundledPresets undefined`.

- [ ] **Step 3: Add the bundled preset constant**

Append to [internal/adapters/streams/assets.go](../../../internal/adapters/streams/assets.go):

```go
import "github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"

// bundledChassisPresets is the source of truth for the 12-slot chassis
// preset bank. The mockup's PRESETS map at docs/superpowers/reference/
// 2026-05-21-receiver-v24.html is the visual spec; this literal mirrors
// it. Live is hardcoded per-slot — ChannelDefinition has no Live field
// to derive from. Channels named here MUST exist in the bundled
// manifest; a unit test asserts this at run time.
var bundledChassisPresets = [12]adapters.PresetEntry{
	{Slot: 1, ProviderID: "mtv-rewind", ChannelID: "1stday", Title: "First Day on MTV", BadgeLabel: "MTV REWIND", BadgeClass: "mtv"},
	{Slot: 2, ProviderID: "mtv-rewind", ChannelID: "80s", Title: "MTV 80s", BadgeLabel: "MTV REWIND", BadgeClass: "mtv"},
	{Slot: 3, ProviderID: "mtv-rewind", ChannelID: "90s", Title: "MTV 90s", BadgeLabel: "MTV REWIND", BadgeClass: "mtv"},
	{Slot: 4, ProviderID: "mtv-rewind", ChannelID: "trl", Title: "TRL", BadgeLabel: "MTV REWIND", BadgeClass: "mtv"},
	{Slot: 5, ProviderID: "mtv-rewind", ChannelID: "120minutes", Title: "120 Minutes", BadgeLabel: "MTV REWIND", BadgeClass: "mtv"},
	{Slot: 6, ProviderID: "mtv-rewind", ChannelID: "unplugged", Title: "Unplugged", BadgeLabel: "MTV REWIND", BadgeClass: "mtv"},
	{Slot: 7, ProviderID: "cartoon-rewind", ChannelID: "loonytunes", Title: "Looney Tunes", BadgeLabel: "CARTOON", BadgeClass: "cartoon"},
	{Slot: 8, ProviderID: "cartoon-rewind", ChannelID: "animaniacs", Title: "Animaniacs", BadgeLabel: "CARTOON", BadgeClass: "cartoon"},
	{Slot: 9, ProviderID: "cartoon-rewind", ChannelID: "heman", Title: "He-Man", BadgeLabel: "CARTOON", BadgeClass: "cartoon"},
	{Slot: 10, ProviderID: "cartoon-rewind", ChannelID: "all", Title: "All Cartoons", BadgeLabel: "CARTOON", BadgeClass: "cartoon"},
	{Slot: 11, ProviderID: "toonami-aftermath", ChannelID: "east", Title: "Toonami East", BadgeLabel: "TOONAMI", BadgeClass: "toonami", Live: true},
	{Slot: 12, ProviderID: "toonami-aftermath", ChannelID: "movies", Title: "Toonami Movies", BadgeLabel: "TOONAMI", BadgeClass: "toonami", Live: true},
}
```

- [ ] **Step 4: Create the `BundledPresets` method**

Create [internal/adapters/streams/preset.go](../../../internal/adapters/streams/preset.go):

```go
package streams

import "github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"

// BundledPresets returns the 12 default chassis preset slots. The
// list is constant for the adapter's lifetime; 3A does not support
// editing. CastPreset (in this file) consumes the same array.
func (a *Adapter) BundledPresets() [12]adapters.PresetEntry {
	return bundledChassisPresets
}
```

(`CastPreset` lands in Task 7.)

- [ ] **Step 5: Run tests to confirm they pass**

```bash
go test ./internal/adapters/streams -run "TestBundledPresets" -v
```

Expected: all 5 tests PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/adapters/streams/assets.go internal/adapters/streams/preset.go internal/adapters/streams/preset_test.go
git commit -m "feat(streams): add bundled 12-slot chassis preset list"
```

---

## Task 7: Streams `CastPreset` With Startup-Snapshot Guard

**Files:**
- Modify: `internal/adapters/streams/preset.go`
- Modify: `internal/adapters/streams/preset_test.go`

- [ ] **Step 1: Write failing tests**

Append to [internal/adapters/streams/preset_test.go](../../../internal/adapters/streams/preset_test.go):

```go
import (
	"context"
	"errors"
	"net/http"
	// ...existing imports...
)

func TestCastPreset_SlotOutOfRange(t *testing.T) {
	a := newAdapterForTest(t) // existing test helper — see adapter_test.go for the canonical builder
	for _, slot := range []int{0, -1, 13, 99} {
		err := a.CastPreset(context.Background(), slot)
		if err == nil {
			t.Errorf("CastPreset(%d) err = nil, want non-nil", slot)
		}
	}
}

func TestCastPreset_StartupSnapshotFailureReturnsNotReady(t *testing.T) {
	a := newAdapterForTest(t)
	// Force ensureStartupSnapshot to fail. The simplest way: replace the
	// adapter's snapshot-builder dependency with one that returns an error.
	// If newAdapterForTest doesn't expose a knob, add one — or stub a.fetcher
	// (the existing httpFetcher dep) with a fake that errors.
	a.injectFakeSnapshotFailure(errors.New("synthetic fetch failure"))
	err := a.CastPreset(context.Background(), 7)
	if err == nil {
		t.Fatal("err = nil, want NOT READY")
	}
	var qerr *adapters.QuickCastError
	if !errors.As(err, &qerr) {
		t.Fatalf("err = %v, want *QuickCastError", err)
	}
	if qerr.Status != http.StatusServiceUnavailable {
		t.Errorf("Status = %d, want 503", qerr.Status)
	}
	if qerr.Chip != "NOT READY" {
		t.Errorf("Chip = %q, want NOT READY", qerr.Chip)
	}
}

func TestCastPreset_SuccessfulSlotCallsStartResolvedStream(t *testing.T) {
	a := newAdapterForTest(t)
	a.eagerlyPopulateCatalogs(t) // existing test helper that drives ensureStartupSnapshot
	captured := a.captureStartResolvedStream() // record what arrives in StartResolvedStream
	if err := a.CastPreset(context.Background(), 7); err != nil {
		t.Fatalf("CastPreset(7) err = %v", err)
	}
	if got := captured.last(); got.ProviderID != "cartoon-rewind" || got.ChannelID != "loonytunes" {
		t.Errorf("captured = %+v, want {cartoon-rewind, loonytunes}", got)
	}
}
```

The exact names of helper methods (`injectFakeSnapshotFailure`, `eagerlyPopulateCatalogs`, `captureStartResolvedStream`) will depend on what's already in `test_helpers_test.go`. If they don't exist, add them. The capture helper can be a simple slice-appending wrapper over the existing `StartResolvedStream`. If `StartResolvedStream` is reachable only via the real path, a per-adapter fake registered through the existing seam (see `playback_test.go` for the pattern) is enough.

- [ ] **Step 2: Run tests to confirm they fail**

```bash
go test ./internal/adapters/streams -run "TestCastPreset" -v
```

Expected: FAIL with `CastPreset undefined` on `*Adapter`.

- [ ] **Step 3: Implement `CastPreset`**

Append to [internal/adapters/streams/preset.go](../../../internal/adapters/streams/preset.go):

```go
import (
	"context"
	"fmt"
	"net/http"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/streamhandoff"
)

// CastPreset starts a Streams cast for slot N (1-indexed). The slot's
// ProviderID/ChannelID come from bundledChassisPresets. Returns a typed
// *adapters.QuickCastError for surfaces that need HTTP status + chip
// text; untyped errors collapse to 500/CAST FAILED via the chassis
// fallback.
func (a *Adapter) CastPreset(ctx context.Context, slot int) error {
	if slot < 1 || slot > 12 {
		return fmt.Errorf("streams: preset slot %d out of range", slot)
	}
	// main.go binds and serves HTTP before adapter Start(ctx) runs, so
	// preset clicks can arrive before catalogs are populated. Guard with
	// the existing ensureStartupSnapshot path and surface a typed
	// QuickCastError so the chassis emits 503/NOT READY.
	if err := a.ensureStartupSnapshot(ctx); err != nil {
		return &adapters.QuickCastError{
			Status:  http.StatusServiceUnavailable,
			Chip:    "NOT READY",
			Message: "streams catalog is not ready",
			Cause:   err,
		}
	}
	entry := bundledChassisPresets[slot-1]
	res := streamhandoff.Resolution{
		ProviderID: entry.ProviderID,
		ChannelID:  entry.ChannelID,
	}
	if err := a.validatePlayRequest(res); err != nil {
		// validatePlayRequest errors here represent adapter-coding bugs
		// (a slot pointing to a non-existent channel), not user-facing
		// failures. They collapse to 500/CAST FAILED via the chassis's
		// errors.As fallback. preset_test.go asserts every slot resolves.
		return err
	}
	_, err := a.StartResolvedStream(ctx, res)
	return err
}
```

- [ ] **Step 4: Run tests to confirm they pass**

```bash
go test ./internal/adapters/streams -run "TestCastPreset|TestBundledPresets" -v
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/streams/preset.go internal/adapters/streams/preset_test.go
git commit -m "feat(streams): implement CastPreset with ensureStartupSnapshot guard"
```

---

## Task 8: Chassis `Config`, `PresetSlot` Data Model, and LIT Derivation

**Files:**
- Modify: `internal/chassis/server.go`
- Modify: `internal/chassis/data.go`
- Modify: `internal/chassis/chassis_test.go` (or a new `data_test.go`)

- [ ] **Step 1: Write failing tests**

Append to [internal/chassis/chassis_test.go](../../../internal/chassis/chassis_test.go) (or create a new `data_test.go` in the same package):

```go
func TestIdleSnapshot_PresetsAreSlotNumberedEvenWhenEmpty(t *testing.T) {
	cfg := minimalConfigForTest() // existing helper — see chassis_test.go
	cfg.PresetViewer = nil
	data := idleSnapshot(cfg, time.Now())
	for i, slot := range data.Presets.Slots {
		if slot.Slot != i+1 {
			t.Errorf("Slots[%d].Slot = %d, want %d", i, slot.Slot, i+1)
		}
		if slot.Filled {
			t.Errorf("Slots[%d].Filled = true with nil PresetViewer, want false", i)
		}
	}
	if data.Presets.ModeLabel != "Memory · 0 / 12 slots" {
		t.Errorf("ModeLabel = %q, want %q", data.Presets.ModeLabel, "Memory · 0 / 12 slots")
	}
}

func TestIdleSnapshot_PresetsHydratedWhenViewerWired(t *testing.T) {
	cfg := minimalConfigForTest()
	cfg.PresetViewer = fakePresetViewer{
		entries: [12]adapters.PresetEntry{
			{Slot: 1, ProviderID: "mtv-rewind", ChannelID: "1stday", Title: "First Day on MTV", BadgeLabel: "MTV REWIND", BadgeClass: "mtv"},
			// remaining entries left zero-valued to assert hydration handles them
		},
	}
	data := idleSnapshot(cfg, time.Now())
	if !data.Presets.Slots[0].Filled || data.Presets.Slots[0].Title != "First Day on MTV" {
		t.Errorf("slot 1 not hydrated: %+v", data.Presets.Slots[0])
	}
	if data.Presets.Slots[0].Subtitle != "MTV REWIND" {
		t.Errorf("slot 1 Subtitle = %q, want %q", data.Presets.Slots[0].Subtitle, "MTV REWIND")
	}
	if data.Presets.Slots[0].BadgeClass != "mtv" {
		t.Errorf("slot 1 BadgeClass = %q, want %q", data.Presets.Slots[0].BadgeClass, "mtv")
	}
	if data.Presets.Slots[1].Filled {
		t.Errorf("slot 2 should be empty (zero-valued PresetEntry), got Filled=true")
	}
	if data.Presets.Slots[1].Slot != 2 {
		t.Errorf("slot 2 Slot = %d, want 2 (numbered even when empty)", data.Presets.Slots[1].Slot)
	}
	if data.Presets.ModeLabel != "Memory · 12 / 12 slots" {
		t.Errorf("ModeLabel = %q, want %q", data.Presets.ModeLabel, "Memory · 12 / 12 slots")
	}
}

func TestSnapshotFromStatusView_LitDerivesFromAdapterRef(t *testing.T) {
	cfg := minimalConfigForTest()
	cfg.PresetViewer = bundledFakeViewer()
	view := fakeStatusView(t, "streams:mtv-rewind:90s:sess-1:42") // helper that returns a core.StatusHomeView with AdapterRef
	data := snapshotFromStatusView(cfg, view, nil, nil, nil, nil, time.Now())
	// slot 3 is mtv-rewind:90s in bundledChassisPresets
	if !data.Presets.Slots[2].Lit {
		t.Errorf("slot 3 not LIT: %+v", data.Presets.Slots[2])
	}
	if data.Presets.Slots[0].Lit {
		t.Errorf("slot 1 LIT, want false (not the active stream)")
	}
}

func TestSnapshotFromStatusView_NonStreamsAdapterRefClearsAllLit(t *testing.T) {
	cfg := minimalConfigForTest()
	cfg.PresetViewer = bundledFakeViewer()
	view := fakeStatusView(t, "url:https://example.test/x.mp4")
	data := snapshotFromStatusView(cfg, view, nil, nil, nil, nil, time.Now())
	for i, slot := range data.Presets.Slots {
		if slot.Lit {
			t.Errorf("Slots[%d].Lit = true, want false (non-streams adapter)", i)
		}
	}
}

type fakePresetViewer struct {
	entries [12]adapters.PresetEntry
}

func (f fakePresetViewer) BundledPresets() [12]adapters.PresetEntry { return f.entries }

func bundledFakeViewer() fakePresetViewer {
	return fakePresetViewer{
		entries: [12]adapters.PresetEntry{
			{Slot: 1, ProviderID: "mtv-rewind", ChannelID: "1stday", Title: "First Day on MTV", BadgeLabel: "MTV REWIND", BadgeClass: "mtv"},
			{Slot: 2, ProviderID: "mtv-rewind", ChannelID: "80s", Title: "MTV 80s", BadgeLabel: "MTV REWIND", BadgeClass: "mtv"},
			{Slot: 3, ProviderID: "mtv-rewind", ChannelID: "90s", Title: "MTV 90s", BadgeLabel: "MTV REWIND", BadgeClass: "mtv"},
		},
	}
}
```

If `fakeStatusView` doesn't already exist, add a simple helper returning `core.StatusHomeView{State: core.StatePlaying, AdapterRef: ref, Source: "streams"}`.

- [ ] **Step 2: Run tests to confirm they fail**

```bash
go test ./internal/chassis -run "TestIdleSnapshot_Presets|TestSnapshotFromStatusView_Lit|TestSnapshotFromStatusView_NonStreams" -v
```

Expected: FAIL — `Config.PresetViewer` undefined; `PresetSlot.Slot` / `.Lit` / `.BadgeClass` undefined; `Subtitle` populated but not from `BadgeLabel`.

- [ ] **Step 3: Extend `PresetSlot` and `Config`**

In [internal/chassis/data.go](../../../internal/chassis/data.go), extend `PresetSlot` (currently at ~line 233):

```go
type PresetSlot struct {
	Filled   bool
	Title    string
	Subtitle string

	Slot       int    // 1..12 — needed for the POST URL
	BadgeClass string // "mtv" | "cartoon" | "toonami" — CSS color hook
	Lit        bool   // currently casting from this slot (server-side initial paint)
	Live       bool   // .preset.live class — always-on live channels
	ProviderID string // streams provider id — for client-side LIT migration
	ChannelID  string // streams channel id — same
}
```

In [internal/chassis/server.go](../../../internal/chassis/server.go), add to `Config`:

```go
type Config struct {
	// ... existing fields ...

	// PresetViewer is the optional source of the 12-slot chassis preset
	// bank. When nil, the preset bank renders all 12 slots in the
	// .empty state (no name, no badge). 3A wires the streams adapter.
	PresetViewer adapters.PresetViewer

	// PresetCaster is the optional handler for preset slot clicks.
	// When nil, POST /receiver/preset/{slot}/cast returns 404.
	PresetCaster adapters.PresetCaster
}
```

And store on `*Server`:

```go
type Server struct {
	// ... existing fields ...
	presetViewer adapters.PresetViewer
	presetCaster adapters.PresetCaster
}
```

Wire in `New`:

```go
s := &Server{
	// ... existing wiring ...
	presetViewer: cfg.PresetViewer,
	presetCaster: cfg.PresetCaster,
}
```

- [ ] **Step 4: Update `idleSnapshot` and `snapshotFromStatusView` to hydrate presets**

In [internal/chassis/data.go](../../../internal/chassis/data.go), replace the existing `Presets` population in `idleSnapshot`:

```go
Presets: buildPresetsData(cfg.PresetViewer, "", ""),
```

And add the helper at the bottom of the file:

```go
// buildPresetsData hydrates a PresetsData from a PresetViewer (or an
// empty/numbered view when viewer is nil). When activeProviderID and
// activeChannelID are non-empty, the matching slot's Lit is set.
func buildPresetsData(viewer adapters.PresetViewer, activeProviderID, activeChannelID string) PresetsData {
	var data PresetsData
	for i := 0; i < 12; i++ {
		data.Slots[i] = PresetSlot{Slot: i + 1}
	}
	if viewer == nil {
		data.ModeLabel = "Memory · 0 / 12 slots"
		data.Count = "★ 0"
		return data
	}
	entries := viewer.BundledPresets()
	filled := 0
	for i, e := range entries {
		slot := PresetSlot{Slot: i + 1}
		if e.ProviderID != "" {
			slot.Filled = true
			slot.Title = e.Title
			slot.Subtitle = e.BadgeLabel
			slot.BadgeClass = e.BadgeClass
			slot.Live = e.Live
			slot.ProviderID = e.ProviderID
			slot.ChannelID = e.ChannelID
			if activeProviderID != "" && activeChannelID != "" &&
				e.ProviderID == activeProviderID && e.ChannelID == activeChannelID {
				slot.Lit = true
			}
			filled++
		}
		data.Slots[i] = slot
	}
	data.ModeLabel = fmt.Sprintf("Memory · %d / 12 slots", filled)
	data.Count = fmt.Sprintf("★ %d", filled)
	return data
}

// parseStreamsAdapterRef extracts (providerID, channelID) from a streams
// AdapterRef of the form "streams:<providerID>:<channelID>:<sessionID>:
// <itemToken>" (see queueAdapterRef in internal/adapters/streams/
// playback.go). Returns empty strings if the ref doesn't start with
// "streams:" or has fewer than 3 segments.
func parseStreamsAdapterRef(ref string) (providerID, channelID string) {
	if !strings.HasPrefix(ref, "streams:") {
		return "", ""
	}
	parts := strings.SplitN(ref, ":", 5)
	if len(parts) < 3 {
		return "", ""
	}
	return parts[1], parts[2]
}
```

Add `"fmt"` and `"strings"` imports if not already present, plus `"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"`.

In `snapshotFromStatusView`, replace the existing `Presets` population:

```go
providerID, channelID := parseStreamsAdapterRef(view.AdapterRef)
base.Presets = buildPresetsData(cfg.PresetViewer, providerID, channelID)
```

(The exact spot in `snapshotFromStatusView` will be wherever the existing `Presets:` literal lives — replace the literal with a call.)

- [ ] **Step 5: Run tests to confirm they pass**

```bash
go test ./internal/chassis -v
```

Expected: all chassis tests PASS, including the new preset tests and any existing tests that touch `PresetsData`. If a pre-existing chassis test references the old `PresetsData{...}` literal, update its expectation to use the helper output.

- [ ] **Step 6: Commit**

```bash
git add internal/chassis/server.go internal/chassis/data.go internal/chassis/chassis_test.go
git commit -m "feat(chassis): hydrate preset bank from PresetViewer with LIT derivation"
```

---

## Task 9: Chassis `cast.go` Helpers (`castKindToTab`, `detectCastKind`, `writeCastJSON`, Startup Verification)

**Files:**
- Create: `internal/chassis/cast.go`
- Create: `internal/chassis/cast_test.go`

- [ ] **Step 1: Write failing tests**

Create [internal/chassis/cast_test.go](../../../internal/chassis/cast_test.go):

```go
package chassis

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func TestDetectCastKind_URL(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"https://example.com/video.mp4", "url"},
		{"  http://example.com/  ", "url"},
		{"HTTPS://example.com", "url"},
		{"magnet:?xt=urn:btih:abc", "magnet"},
		{"  MAGNET:?xt=urn:btih:abc  ", "magnet"},
		{"magnet:abc", "magnet"},
		{"ftp://example.com/x", ""},
		{"not-a-url", ""},
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := detectCastKind(tc.in, false)
			if got != tc.want {
				t.Errorf("detectCastKind(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestDetectCastKind_FilePresentWinsOverPayload(t *testing.T) {
	if got := detectCastKind("https://example.com/x.mp4", true); got != "file" {
		t.Errorf("detectCastKind with file=true = %q, want file", got)
	}
	if got := detectCastKind("", true); got != "file" {
		t.Errorf("detectCastKind empty payload + file=true = %q, want file", got)
	}
}

func TestWriteCastJSON_SuccessShape(t *testing.T) {
	rec := httptest.NewRecorder()
	writeCastJSON(rec, 200, true, "")
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if rec.Code != 200 {
		t.Errorf("Code = %d, want 200", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if body["ok"] != true {
		t.Errorf("ok = %v, want true", body["ok"])
	}
	if _, present := body["chip"]; present {
		t.Errorf("chip present on success body, want omitted")
	}
}

func TestWriteCastJSON_ErrorShapeIncludesChip(t *testing.T) {
	rec := httptest.NewRecorder()
	writeCastJSON(rec, 400, false, "BAD URL")
	if rec.Code != 400 {
		t.Errorf("Code = %d, want 400", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if body["ok"] != false || body["chip"] != "BAD URL" {
		t.Errorf("body = %+v, want {ok:false, chip:BAD URL}", body)
	}
}

func TestVerifyCastTabBindings_AllResolveAgainstRegistry(t *testing.T) {
	reg := adapters.NewRegistry()
	// Use real URL + torrent adapters that implement QuickCastProvider.
	urlAdapter := newURLAdapterStub(t)         // local test helper, see below
	torrentAdapter := newTorrentAdapterStub(t) // local test helper, see below
	if err := reg.Register(urlAdapter); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(torrentAdapter); err != nil {
		t.Fatal(err)
	}
	if err := verifyCastTabBindings(reg); err != nil {
		t.Fatalf("verifyCastTabBindings: %v", err)
	}
}

func TestVerifyCastTabBindings_MissingAdapterFails(t *testing.T) {
	reg := adapters.NewRegistry()
	if err := reg.Register(newURLAdapterStub(t)); err != nil {
		t.Fatal(err)
	}
	// No torrent adapter registered — castKindToTab["magnet"] -> torrent-magnet
	// should not resolve.
	err := verifyCastTabBindings(reg)
	if err == nil {
		t.Fatal("verifyCastTabBindings = nil, want missing-tab error")
	}
	if !strings.Contains(err.Error(), "torrent-magnet") {
		t.Errorf("err = %v, want mention of torrent-magnet", err)
	}
}
```

The local stubs `newURLAdapterStub` and `newTorrentAdapterStub` are minimal `adapters.Adapter` + `adapters.QuickCastProvider` implementations defined in the same test file. They return tab IDs `"url"`, `"torrent-magnet"`, `"torrent-file"` to satisfy the mapping.

Sample stub (place at the bottom of `cast_test.go`):

```go
type urlAdapterStub struct{}

func (urlAdapterStub) Name() string                           { return "url" }
func (urlAdapterStub) Description() string                    { return "" }
func (urlAdapterStub) UIRoutes() []adapters.Route             { return nil }
func (urlAdapterStub) CompanionRoutes() []adapters.Route      { return nil }
func (urlAdapterStub) Start(ctx context.Context) error        { return nil }
func (urlAdapterStub) Stop(ctx context.Context) error         { return nil }
func (urlAdapterStub) IsEnabled() bool                        { return true }
func (urlAdapterStub) Status() adapters.Status                { return adapters.Status{} }
func (urlAdapterStub) QuickCastTabs() []adapters.QuickCastTab {
	return []adapters.QuickCastTab{{
		ID:       "url",
		Enabled:  true,
		Encoding: adapters.QuickCastEncodingForm,
		Fields:   []adapters.QuickCastField{{Name: "url", Type: "url"}},
	}}
}
func (urlAdapterStub) HandleQuickCast(ctx context.Context, req adapters.QuickCastRequest) (adapters.QuickCastResult, error) {
	return adapters.QuickCastResult{}, nil
}

func newURLAdapterStub(t *testing.T) adapters.Adapter { return urlAdapterStub{} }
```

(Similar stub for `newTorrentAdapterStub` exposes `torrent-magnet` and `torrent-file` tabs with the right field names.)

If the `adapters.Adapter` interface has more required methods than shown, copy them from the existing `internal/adapters/adapter.go`.

- [ ] **Step 2: Run tests to confirm they fail**

```bash
go test ./internal/chassis -run "TestDetectCastKind|TestWriteCastJSON|TestVerifyCastTabBindings" -v
```

Expected: FAIL with `undefined: detectCastKind` etc.

- [ ] **Step 3: Create `cast.go`**

Create [internal/chassis/cast.go](../../../internal/chassis/cast.go):

```go
package chassis

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// castKindToTab maps the chassis input row's detected kind to the
// QuickCastTab.ID it submits against. verifyCastTabBindings asserts
// every value resolves to a real tab in the registered adapters at
// startup time.
var castKindToTab = map[string]string{
	"url":    "url",
	"magnet": "torrent-magnet",
	"file":   "torrent-file",
}

// valuesKeyForTab maps each chassis cast kind to the QuickCastField.Name
// the adapter reads from QuickCastRequest.Values. Only used for non-file
// kinds; file uploads use fileFieldForTab.
var valuesKeyForTab = map[string]string{
	"url":    "url",
	"magnet": "magnet",
}

// fileFieldForTab maps each chassis cast kind to the QuickCastField.Name
// the adapter expects for multipart file uploads. The chassis populates
// QuickCastRequest.File.FieldName to match.
var fileFieldForTab = map[string]string{
	"file": "torrent_file",
}

// detectCastKind classifies a chassis input-row payload by URL scheme.
// hasFile takes precedence — when a torrent file is queued, the chip
// renders "TORRENT FILE" regardless of the paste box contents. Empty
// strings and non-supported schemes return "" (chassis renders BAD INPUT).
func detectCastKind(payload string, hasFile bool) string {
	if hasFile {
		return "file"
	}
	parsed, err := url.Parse(strings.TrimSpace(payload))
	if err != nil || parsed.Scheme == "" {
		return ""
	}
	switch strings.ToLower(parsed.Scheme) {
	case "magnet":
		return "magnet"
	case "http", "https":
		return "url"
	}
	return ""
}

// writeCastJSON emits the {"ok": bool, "chip": string?} shape used by
// both /receiver/cast and /receiver/preset/{slot}/cast. Sets
// Content-Type: application/json. When ok is true, chip is omitted
// from the body; when ok is false, chip is required.
func writeCastJSON(w http.ResponseWriter, status int, ok bool, chip string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body := map[string]any{"ok": ok}
	if !ok {
		body["chip"] = chip
	}
	_ = json.NewEncoder(w).Encode(body)
}

// verifyCastTabBindings walks the registry's QuickCastProvider adapters
// and asserts every (kind, tabID) and (kind, fieldName) pair in
// castKindToTab/valuesKeyForTab/fileFieldForTab resolves to a real tab
// + field. Called from main.go at startup so adapter renames fail loud
// instead of producing 404s at request time.
func verifyCastTabBindings(reg *adapters.Registry) error {
	type tabIndex struct {
		tab    adapters.QuickCastTab
		fields map[string]adapters.QuickCastField
	}
	tabs := map[string]tabIndex{}
	for _, a := range reg.List() {
		p, ok := a.(adapters.QuickCastProvider)
		if !ok {
			continue
		}
		for _, t := range p.QuickCastTabs() {
			idx := tabIndex{tab: t, fields: map[string]adapters.QuickCastField{}}
			for _, f := range t.Fields {
				idx.fields[f.Name] = f
			}
			tabs[t.ID] = idx
		}
	}
	for kind, tabID := range castKindToTab {
		if _, ok := tabs[tabID]; !ok {
			return fmt.Errorf("castKindToTab[%q] = %q: tab not registered", kind, tabID)
		}
	}
	for kind, fieldName := range valuesKeyForTab {
		tabID, ok := castKindToTab[kind]
		if !ok {
			return fmt.Errorf("valuesKeyForTab[%q] has no castKindToTab entry", kind)
		}
		idx := tabs[tabID]
		if _, ok := idx.fields[fieldName]; !ok {
			return fmt.Errorf("valuesKeyForTab[%q] = %q: field not present on tab %q", kind, fieldName, tabID)
		}
	}
	for kind, fieldName := range fileFieldForTab {
		tabID, ok := castKindToTab[kind]
		if !ok {
			return fmt.Errorf("fileFieldForTab[%q] has no castKindToTab entry", kind)
		}
		idx := tabs[tabID]
		if _, ok := idx.fields[fieldName]; !ok {
			return fmt.Errorf("fileFieldForTab[%q] = %q: field not present on tab %q", kind, fieldName, tabID)
		}
	}
	return nil
}
```

- [ ] **Step 4: Run tests to confirm they pass**

```bash
go test ./internal/chassis -run "TestDetectCastKind|TestWriteCastJSON|TestVerifyCastTabBindings" -v
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chassis/cast.go internal/chassis/cast_test.go
git commit -m "feat(chassis): add cast helpers (detectCastKind, writeCastJSON, tab verification)"
```

---

## Task 10: `POST /receiver/cast` Route Handler

**Files:**
- Modify: `internal/chassis/cast.go`
- Modify: `internal/chassis/cast_test.go`
- Modify: `internal/chassis/server.go`

- [ ] **Step 1: Write failing handler tests**

Append to [internal/chassis/cast_test.go](../../../internal/chassis/cast_test.go):

```go
func TestHandleCastPost_URLSuccess(t *testing.T) {
	calls := &recordedQuickCasts{}
	srv := newServerWithAdaptersForTest(t, calls)
	form := url.Values{}
	form.Set("kind", "url")
	form.Set("payload", "https://example.com/video.mp4")
	req := httptest.NewRequest(http.MethodPost, "/receiver/cast", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	srv.handleCastPost(rec, req)
	if rec.Code != 200 {
		t.Fatalf("Code = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if got := calls.last(); got.TabID != "url" || got.Values["url"] != "https://example.com/video.mp4" {
		t.Errorf("recorded = %+v, want TabID=url Values[url]=...", got)
	}
}

func TestHandleCastPost_MagnetRoutesToTorrent(t *testing.T) {
	calls := &recordedQuickCasts{}
	srv := newServerWithAdaptersForTest(t, calls)
	form := url.Values{}
	form.Set("kind", "magnet")
	form.Set("payload", "magnet:?xt=urn:btih:abc")
	req := httptest.NewRequest(http.MethodPost, "/receiver/cast", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	srv.handleCastPost(rec, req)
	if rec.Code != 200 {
		t.Fatalf("Code = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if got := calls.last(); got.TabID != "torrent-magnet" {
		t.Errorf("TabID = %q, want torrent-magnet", got.TabID)
	}
	if got := calls.last(); got.Values["magnet"] != "magnet:?xt=urn:btih:abc" {
		t.Errorf("Values[magnet] = %q, want the magnet uri", got.Values["magnet"])
	}
}

func TestHandleCastPost_FileUploadPopulatesFile(t *testing.T) {
	calls := &recordedQuickCasts{}
	srv := newServerWithAdaptersForTest(t, calls)
	body, contentType := makeMultipart(t, "torrent_file", "example.torrent", []byte("d8:announce..."))
	req := httptest.NewRequest(http.MethodPost, "/receiver/cast", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	srv.handleCastPost(rec, req)
	if rec.Code != 200 {
		t.Fatalf("Code = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if got := calls.last(); got.TabID != "torrent-file" || got.File == nil {
		t.Errorf("recorded = %+v, want TabID=torrent-file with File set", got)
	}
	if got := calls.last(); got.File.FieldName != "torrent_file" {
		t.Errorf("File.FieldName = %q, want torrent_file", got.File.FieldName)
	}
}

func TestHandleCastPost_BadInputReturns400(t *testing.T) {
	srv := newServerWithAdaptersForTest(t, &recordedQuickCasts{})
	form := url.Values{}
	form.Set("kind", "url")
	form.Set("payload", "not-a-url")
	req := httptest.NewRequest(http.MethodPost, "/receiver/cast", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	srv.handleCastPost(rec, req)
	if rec.Code != 400 {
		t.Errorf("Code = %d, want 400", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["chip"] != "BAD INPUT" {
		t.Errorf("chip = %v, want BAD INPUT", body["chip"])
	}
}

func TestHandleCastPost_AdapterQuickCastErrorPropagates(t *testing.T) {
	calls := &recordedQuickCasts{
		respond: func(req adapters.QuickCastRequest) (adapters.QuickCastResult, error) {
			return adapters.QuickCastResult{}, &adapters.QuickCastError{Status: 413, Chip: "FILE TOO BIG"}
		},
	}
	srv := newServerWithAdaptersForTest(t, calls)
	body, contentType := makeMultipart(t, "torrent_file", "huge.torrent", []byte("..."))
	req := httptest.NewRequest(http.MethodPost, "/receiver/cast", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	srv.handleCastPost(rec, req)
	if rec.Code != 413 {
		t.Errorf("Code = %d, want 413", rec.Code)
	}
	var bodyOut map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &bodyOut)
	if bodyOut["chip"] != "FILE TOO BIG" {
		t.Errorf("chip = %v, want FILE TOO BIG", bodyOut["chip"])
	}
}

func TestHandleCastPost_UntypedErrorCollapsesToCastFailed(t *testing.T) {
	calls := &recordedQuickCasts{
		respond: func(req adapters.QuickCastRequest) (adapters.QuickCastResult, error) {
			return adapters.QuickCastResult{}, fmt.Errorf("synthetic untyped error")
		},
	}
	srv := newServerWithAdaptersForTest(t, calls)
	form := url.Values{}
	form.Set("kind", "url")
	form.Set("payload", "https://example.com/x.mp4")
	req := httptest.NewRequest(http.MethodPost, "/receiver/cast", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	srv.handleCastPost(rec, req)
	if rec.Code != 500 {
		t.Errorf("Code = %d, want 500", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["chip"] != "CAST FAILED" {
		t.Errorf("chip = %v, want CAST FAILED", body["chip"])
	}
}
```

Where:

```go
type recordedQuickCasts struct {
	mu       sync.Mutex
	calls    []adapters.QuickCastRequest
	respond  func(adapters.QuickCastRequest) (adapters.QuickCastResult, error)
}

func (r *recordedQuickCasts) record(req adapters.QuickCastRequest) (adapters.QuickCastResult, error) {
	r.mu.Lock()
	r.calls = append(r.calls, req)
	r.mu.Unlock()
	if r.respond != nil {
		return r.respond(req)
	}
	return adapters.QuickCastResult{}, nil
}

func (r *recordedQuickCasts) last() adapters.QuickCastRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.calls) == 0 {
		return adapters.QuickCastRequest{}
	}
	return r.calls[len(r.calls)-1]
}

// newServerWithAdaptersForTest constructs a *Server with a Registry
// containing URL and torrent adapter stubs that route both tab IDs
// through the recorder.
func newServerWithAdaptersForTest(t *testing.T, calls *recordedQuickCasts) *Server { /* ... */ }

// makeMultipart builds a multipart body with the named field.
func makeMultipart(t *testing.T, fieldName, filename string, data []byte) (io.Reader, string) { /* ... */ }
```

Implement both helpers inline at the bottom of `cast_test.go`. `newServerWithAdaptersForTest` uses `chassis.New` with a `Config` that has a `Registry` containing two stubs whose `HandleQuickCast` defers to `calls.record(...)`.

- [ ] **Step 2: Run tests to confirm they fail**

```bash
go test ./internal/chassis -run "TestHandleCastPost" -v
```

Expected: FAIL with `undefined: handleCastPost`.

- [ ] **Step 3: Implement the handler**

Add `"errors"` to the existing import block at the top of [internal/chassis/cast.go](../../../internal/chassis/cast.go), then append:

```go
const castKindFormField = "kind"
const castPayloadFormField = "payload"

func (s *Server) handleCastPost(w http.ResponseWriter, r *http.Request) {
	contentType := r.Header.Get("Content-Type")
	var (
		payload  string
		file     *adapters.QuickCastFile
		formKind string
	)
	switch {
	case strings.HasPrefix(contentType, "multipart/form-data"):
		r.Body = http.MaxBytesReader(w, r.Body, adapters.MaxQuickCastBytes)
		if err := r.ParseMultipartForm(4 << 20); err != nil {
			writeCastJSON(w, http.StatusBadRequest, false, "BAD INPUT")
			return
		}
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
		formKind = strings.TrimSpace(r.FormValue(castKindFormField))
		payload = strings.TrimSpace(r.FormValue(castPayloadFormField))
		for fieldName, headers := range r.MultipartForm.File {
			if len(headers) == 0 {
				continue
			}
			file = &adapters.QuickCastFile{FieldName: fieldName, Header: headers[0]}
			break
		}
	default:
		if err := r.ParseForm(); err != nil {
			writeCastJSON(w, http.StatusBadRequest, false, "BAD INPUT")
			return
		}
		formKind = strings.TrimSpace(r.PostFormValue(castKindFormField))
		payload = strings.TrimSpace(r.PostFormValue(castPayloadFormField))
	}
	_ = formKind // client hint only; server re-detects.

	kind := detectCastKind(payload, file != nil)
	if kind == "" {
		writeCastJSON(w, http.StatusBadRequest, false, "BAD INPUT")
		return
	}
	tabID, ok := castKindToTab[kind]
	if !ok {
		writeCastJSON(w, http.StatusBadRequest, false, "BAD INPUT")
		return
	}

	provider, _, ok := s.quickCastProviderForTab(tabID)
	if !ok {
		writeCastJSON(w, http.StatusNotFound, false, "NOT FOUND")
		return
	}

	req := adapters.QuickCastRequest{TabID: tabID, Values: map[string]string{}}
	if file != nil {
		expectedFieldName, ok := fileFieldForTab[kind]
		if !ok {
			writeCastJSON(w, http.StatusBadRequest, false, "BAD INPUT")
			return
		}
		file.FieldName = expectedFieldName
		req.File = file
	} else {
		valuesKey, ok := valuesKeyForTab[kind]
		if !ok {
			writeCastJSON(w, http.StatusBadRequest, false, "BAD INPUT")
			return
		}
		req.Values[valuesKey] = payload
	}

	_, err := provider.HandleQuickCast(r.Context(), req)
	if err != nil {
		var qerr *adapters.QuickCastError
		if errors.As(err, &qerr) {
			writeCastJSON(w, qerr.Status, false, qerr.Chip)
			return
		}
		writeCastJSON(w, http.StatusInternalServerError, false, "CAST FAILED")
		return
	}
	writeCastJSON(w, http.StatusOK, true, "")
}

// quickCastProviderForTab finds the QuickCastProvider that advertises
// the given tab ID. Mirror of internal/ui/playback.go:338. Returns
// (provider, tab, ok).
func (s *Server) quickCastProviderForTab(tabID string) (adapters.QuickCastProvider, adapters.QuickCastTab, bool) {
	if s.cfg.Registry == nil {
		return nil, adapters.QuickCastTab{}, false
	}
	for _, a := range s.cfg.Registry.List() {
		p, ok := a.(adapters.QuickCastProvider)
		if !ok {
			continue
		}
		for _, t := range p.QuickCastTabs() {
			if t.ID == tabID {
				return p, t, true
			}
		}
	}
	return nil, adapters.QuickCastTab{}, false
}
```

(The `context` and `io` imports added at the top of the file are used by `handleCastPost` via `r.Context()` and by multipart parsing respectively.)

- [ ] **Step 4: Register the route in `Mount`**

In [internal/chassis/server.go](../../../internal/chassis/server.go), inside the `Mount` method (around line 158), add the new route after the existing `aux/start` registrations:

```go
mux.Handle("POST /receiver/cast", requireSameOrigin(http.HandlerFunc(s.handleCastPost)))
```

- [ ] **Step 5: Run tests to confirm they pass**

```bash
go test ./internal/chassis -run "TestHandleCastPost" -v
```

Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/chassis/cast.go internal/chassis/cast_test.go internal/chassis/server.go
git commit -m "feat(chassis): add POST /receiver/cast handler"
```

---

## Task 11: `POST /receiver/preset/{slot}/cast` Route Handler

**Files:**
- Create: `internal/chassis/preset.go`
- Create: `internal/chassis/preset_test.go`
- Modify: `internal/chassis/server.go`

- [ ] **Step 1: Write failing tests**

Create [internal/chassis/preset_test.go](../../../internal/chassis/preset_test.go):

```go
package chassis

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

type fakePresetCaster struct {
	mu       sync.Mutex
	calls    []int
	respond  func(slot int) error
}

func (f *fakePresetCaster) CastPreset(ctx context.Context, slot int) error {
	f.mu.Lock()
	f.calls = append(f.calls, slot)
	f.mu.Unlock()
	if f.respond != nil {
		return f.respond(slot)
	}
	return nil
}

func newServerWithPresetCasterForTest(t *testing.T, caster adapters.PresetCaster) *Server {
	cfg := minimalConfigForTest()
	cfg.PresetCaster = caster
	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv
}

func TestHandlePresetCast_Success(t *testing.T) {
	caster := &fakePresetCaster{}
	srv := newServerWithPresetCasterForTest(t, caster)
	req := httptest.NewRequest(http.MethodPost, "/receiver/preset/7/cast", strings.NewReader(""))
	req.SetPathValue("slot", "7")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	srv.handlePresetCast(rec, req)
	if rec.Code != 200 {
		t.Fatalf("Code = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if len(caster.calls) != 1 || caster.calls[0] != 7 {
		t.Errorf("calls = %v, want [7]", caster.calls)
	}
}

func TestHandlePresetCast_NilCasterReturns404(t *testing.T) {
	srv := newServerWithPresetCasterForTest(t, nil)
	req := httptest.NewRequest(http.MethodPost, "/receiver/preset/1/cast", strings.NewReader(""))
	req.SetPathValue("slot", "1")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	srv.handlePresetCast(rec, req)
	if rec.Code != 404 {
		t.Errorf("Code = %d, want 404", rec.Code)
	}
}

func TestHandlePresetCast_OutOfRangeSlots(t *testing.T) {
	srv := newServerWithPresetCasterForTest(t, &fakePresetCaster{})
	for _, slot := range []string{"0", "13", "-1", "abc"} {
		t.Run("slot="+slot, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/receiver/preset/"+slot+"/cast", strings.NewReader(""))
			req.SetPathValue("slot", slot)
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			rec := httptest.NewRecorder()
			srv.handlePresetCast(rec, req)
			if rec.Code != 400 {
				t.Errorf("Code = %d, want 400", rec.Code)
			}
			var body map[string]any
			_ = json.Unmarshal(rec.Body.Bytes(), &body)
			if body["chip"] != "BAD SLOT" {
				t.Errorf("chip = %v, want BAD SLOT", body["chip"])
			}
		})
	}
}

func TestHandlePresetCast_QuickCastErrorPropagates(t *testing.T) {
	caster := &fakePresetCaster{
		respond: func(slot int) error {
			return &adapters.QuickCastError{Status: 503, Chip: "NOT READY"}
		},
	}
	srv := newServerWithPresetCasterForTest(t, caster)
	req := httptest.NewRequest(http.MethodPost, "/receiver/preset/3/cast", strings.NewReader(""))
	req.SetPathValue("slot", "3")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	srv.handlePresetCast(rec, req)
	if rec.Code != 503 {
		t.Errorf("Code = %d, want 503", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["chip"] != "NOT READY" {
		t.Errorf("chip = %v, want NOT READY", body["chip"])
	}
}

func TestHandlePresetCast_UntypedErrorCollapsesToCastFailed(t *testing.T) {
	caster := &fakePresetCaster{respond: func(slot int) error { return errors.New("synthetic") }}
	srv := newServerWithPresetCasterForTest(t, caster)
	req := httptest.NewRequest(http.MethodPost, "/receiver/preset/3/cast", strings.NewReader(""))
	req.SetPathValue("slot", "3")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	srv.handlePresetCast(rec, req)
	if rec.Code != 500 {
		t.Errorf("Code = %d, want 500", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["chip"] != "CAST FAILED" {
		t.Errorf("chip = %v, want CAST FAILED", body["chip"])
	}
}
```

- [ ] **Step 2: Run tests to confirm they fail**

```bash
go test ./internal/chassis -run "TestHandlePresetCast" -v
```

Expected: FAIL with `undefined: handlePresetCast`.

- [ ] **Step 3: Implement the handler**

Create [internal/chassis/preset.go](../../../internal/chassis/preset.go):

```go
package chassis

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func (s *Server) handlePresetCast(w http.ResponseWriter, r *http.Request) {
	slotStr := r.PathValue("slot")
	slot, err := strconv.Atoi(slotStr)
	if err != nil || slot < 1 || slot > 12 {
		writeCastJSON(w, http.StatusBadRequest, false, "BAD SLOT")
		return
	}
	if s.presetCaster == nil {
		writeCastJSON(w, http.StatusNotFound, false, "NOT FOUND")
		return
	}
	if err := s.presetCaster.CastPreset(r.Context(), slot); err != nil {
		var qerr *adapters.QuickCastError
		if errors.As(err, &qerr) {
			writeCastJSON(w, qerr.Status, false, qerr.Chip)
			return
		}
		writeCastJSON(w, http.StatusInternalServerError, false, "CAST FAILED")
		return
	}
	writeCastJSON(w, http.StatusOK, true, "")
}
```

- [ ] **Step 4: Register the route in `Mount`**

In [internal/chassis/server.go](../../../internal/chassis/server.go), add to `Mount`:

```go
mux.Handle("POST /receiver/preset/{slot}/cast", requireSameOrigin(http.HandlerFunc(s.handlePresetCast)))
```

- [ ] **Step 5: Run tests to confirm they pass**

```bash
go test ./internal/chassis -run "TestHandlePresetCast" -v
```

Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/chassis/preset.go internal/chassis/preset_test.go internal/chassis/server.go
git commit -m "feat(chassis): add POST /receiver/preset/{slot}/cast handler"
```

---

## Task 12: Templates — `input-row.html`, `preset-bank.html`, `shell.html`

**Files:**
- Modify: `internal/chassis/templates/input-row.html`
- Modify: `internal/chassis/templates/preset-bank.html`
- Modify: `internal/chassis/templates/shell.html`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write failing template tests**

Append to [internal/chassis/chassis_test.go](../../../internal/chassis/chassis_test.go):

```go
func TestPresetBankTemplate_RendersDataAttributes(t *testing.T) {
	tmpl := parseTemplatesForTest(t)
	data := PresetsData{Slots: [12]PresetSlot{
		{Slot: 1, Filled: true, Title: "First Day on MTV", Subtitle: "MTV REWIND", BadgeClass: "mtv", ProviderID: "mtv-rewind", ChannelID: "1stday"},
		{Slot: 11, Filled: true, Title: "Toonami East", Subtitle: "TOONAMI", BadgeClass: "toonami", ProviderID: "toonami-aftermath", ChannelID: "east", Live: true},
	}, ModeLabel: "Memory · 12 / 12 slots", Count: "★ 12"}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "preset-bank", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	html := buf.String()
	for _, want := range []string{
		`data-slot="1"`,
		`data-provider="mtv-rewind"`,
		`data-channel="1stday"`,
		`data-slot="11"`,
		`data-provider="toonami-aftermath"`,
		`data-channel="east"`,
		`class="preset live"`,
		`<div class="badge toonami">TOONAMI · LIVE</div>`,
		`<div class="badge mtv">MTV REWIND</div>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered HTML missing %q.\nHTML:\n%s", want, html)
		}
	}
	if strings.Contains(html, `<div class="badge mtv">MTV REWIND · LIVE`) {
		t.Errorf("non-live slot should not get LIVE suffix")
	}
}

func TestPresetBankTemplate_DataSlotPopulatedEvenForEmptySlots(t *testing.T) {
	tmpl := parseTemplatesForTest(t)
	data := PresetsData{}
	for i := 0; i < 12; i++ {
		data.Slots[i] = PresetSlot{Slot: i + 1}
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "preset-bank", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	for i := 1; i <= 12; i++ {
		want := fmt.Sprintf(`data-slot="%d"`, i)
		if !strings.Contains(buf.String(), want) {
			t.Errorf("missing %q for empty slot", want)
		}
	}
}

func TestInputRowTemplate_RendersChipKindAttribute(t *testing.T) {
	tmpl := parseTemplatesForTest(t)
	data := InputData{PastePlaceholder: "Paste URL or magnet", DetectedKind: "URL", CastEnabled: false}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "input-row", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	html := buf.String()
	if !strings.Contains(html, `data-chip-kind=`) {
		t.Errorf("missing data-chip-kind attribute.\nHTML:\n%s", html)
	}
}

func TestShellTemplate_LoadsNewScripts(t *testing.T) {
	srv := newServerForTest(t)
	req := httptest.NewRequest(http.MethodGet, "/receiver", nil)
	rec := httptest.NewRecorder()
	srv.handleIndex(rec, req)
	html := rec.Body.String()
	for _, want := range []string{
		`/receiver/static/input-cast.js?v=`,
		`/receiver/static/preset-bank.js?v=`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("shell.html missing %q script tag", want)
		}
	}
}
```

`parseTemplatesForTest` is an existing helper; if not, add a small one that calls `parseTemplates()` (the internal function in `templates.go`).

- [ ] **Step 2: Run tests to confirm they fail**

```bash
go test ./internal/chassis -run "TestPresetBankTemplate|TestInputRowTemplate|TestShellTemplate_LoadsNewScripts" -v
```

Expected: FAIL — template doesn't render the new attributes/classes; new scripts aren't in shell.html.

- [ ] **Step 3: Update `preset-bank.html`**

Replace the contents of [internal/chassis/templates/preset-bank.html](../../../internal/chassis/templates/preset-bank.html) with:

```html
{{define "preset-bank"}}
{{htmlComment "chassis:preset-bank"}}
<div class="preset-strip preset-section">
  <span class="strip-label">Presets</span>
  <div>
    <div class="preset-header">
      <span class="title" id="preset-mode-label">{{.ModeLabel}}</span>
      <span class="count" id="preset-count">{{.Count}}</span>
      <button class="browse-btn" id="browse-toggle" disabled aria-disabled="true">▸ Browse full catalog</button>
    </div>
    <div class="preset-bank">
      {{range $i, $slot := .Slots}}
      <button class="preset{{if not $slot.Filled}} empty{{end}}{{if $slot.Lit}} lit{{end}}{{if $slot.Live}} live{{end}}"
              type="button"
              data-slot="{{$slot.Slot}}"
              data-provider="{{$slot.ProviderID}}"
              data-channel="{{$slot.ChannelID}}">
        <div class="num">{{pad2 (inc $i)}}</div>
        {{if $slot.Filled}}
        <div class="name">{{$slot.Title}}</div>
        <div class="badge {{$slot.BadgeClass}}">{{$slot.Subtitle}}{{if $slot.Live}} · LIVE{{end}}</div>
        {{end}}
      </button>
      {{end}}
    </div>
  </div>
</div>
{{end}}
```

- [ ] **Step 4: Update `input-row.html`**

Replace [internal/chassis/templates/input-row.html](../../../internal/chassis/templates/input-row.html) with:

```html
{{define "input-row"}}
{{htmlComment "chassis:input-row"}}
<div class="section-strip input-section" data-cast-state="idle">
  <span class="strip-label">Input</span>
  <div style="display:flex; gap:6px;">
    <div class="input-panel" style="flex:1">
      <label class="glyph" for="paste-input">&#9656;</label>
      <input class="paste-input" id="paste-input" type="text" autocomplete="off" autocapitalize="off" spellcheck="false" placeholder="{{.PastePlaceholder}}">
      <button class="paste-clear" id="paste-clear" type="button" aria-label="Clear input" style="display:none;">&times;</button>
      <span class="chip" id="paste-chip" data-chip-kind="">{{.DetectedKind}}</span>
    </div>
    <button class="cast-btn{{if not .CastEnabled}} disabled{{end}}" id="cast-btn" type="button"{{if not .CastEnabled}} disabled{{end}}>CAST</button>
    <button class="upload-btn" id="upload-btn" type="button">&uarr; .TORRENT</button>
    <input type="file" id="torrent-file-input" accept=".torrent,application/x-bittorrent" style="display:none;">
  </div>
</div>
{{end}}
```

- [ ] **Step 5: Update `shell.html` to load the new scripts**

In [internal/chassis/templates/shell.html](../../../internal/chassis/templates/shell.html), after the existing `meter.js` script tag, insert:

```html
  <script defer src="/receiver/static/input-cast.js?v={{.Version}}"></script>
  <script defer src="/receiver/static/preset-bank.js?v={{.Version}}"></script>
```

`input-cast.js` and `preset-bank.js` are created in Task 13. Loading their `<script>` tags here even before the files exist won't break — the test for shell.html only checks the `<script>` URL is in the HTML; the static handler returns 404 for the files until Task 13 lands, but the page still renders.

- [ ] **Step 6: Run tests to confirm they pass**

```bash
go test ./internal/chassis -v
```

Expected: all chassis tests PASS, including the new template tests.

- [ ] **Step 7: Commit**

```bash
git add internal/chassis/templates/ internal/chassis/chassis_test.go
git commit -m "feat(chassis): wire preset bank and input row templates for live data"
```

---

## Task 13: Client JS Files + CSS Additions

**Files:**
- Create: `internal/chassis/static/input-cast.js`
- Create: `internal/chassis/static/preset-bank.js`
- Modify: `internal/chassis/static/chassis.css`
- Modify: `internal/chassis/chassis_test.go`

- [ ] **Step 1: Write failing lint + presence tests**

Append to [internal/chassis/chassis_test.go](../../../internal/chassis/chassis_test.go):

```go
func TestInputCastJS_Exists(t *testing.T) {
	src, err := chassisStaticFS.ReadFile("static/input-cast.js")
	if err != nil {
		t.Fatalf("ReadFile input-cast.js: %v", err)
	}
	s := string(src)
	if !strings.Contains(s, "fetch") {
		t.Errorf("input-cast.js missing fetch call")
	}
	for _, forbidden := range []string{"Math.random", "Math.sin", "Math.cos"} {
		if strings.Contains(s, forbidden) {
			t.Errorf("input-cast.js contains forbidden fake-data pattern %q", forbidden)
		}
	}
}

func TestPresetBankJS_Exists(t *testing.T) {
	src, err := chassisStaticFS.ReadFile("static/preset-bank.js")
	if err != nil {
		t.Fatalf("ReadFile preset-bank.js: %v", err)
	}
	s := string(src)
	if !strings.Contains(s, "window.Chassis.events.subscribe") {
		t.Errorf("preset-bank.js does not subscribe to events (missing window.Chassis.events.subscribe)")
	}
	for _, forbidden := range []string{"Math.random", "Math.sin", "Math.cos"} {
		if strings.Contains(s, forbidden) {
			t.Errorf("preset-bank.js contains forbidden fake-data pattern %q", forbidden)
		}
	}
}

func TestChassisCSS_AddsCastRules(t *testing.T) {
	src, err := chassisStaticFS.ReadFile("static/chassis.css")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	s := string(src)
	for _, want := range []string{
		`body.receiver .chip[data-chip-kind="err"]`,
		`body.receiver .badge.mtv`,
		`body.receiver .badge.cartoon`,
		`body.receiver .badge.toonami`,
		`body.receiver .browse-btn[disabled]`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("chassis.css missing selector %q", want)
		}
	}
}
```

- [ ] **Step 2: Run tests to confirm they fail**

```bash
go test ./internal/chassis -run "TestInputCastJS_Exists|TestPresetBankJS_Exists|TestChassisCSS_AddsCastRules" -v
```

Expected: FAIL — files don't exist; CSS doesn't have the new selectors.

- [ ] **Step 3: Create `input-cast.js`**

Create [internal/chassis/static/input-cast.js](../../../internal/chassis/static/input-cast.js):

```javascript
(function () {
  const panel = document.querySelector('.input-section');
  if (!panel) return;
  const input = document.getElementById('paste-input');
  const clearBtn = document.getElementById('paste-clear');
  const chip = document.getElementById('paste-chip');
  const castBtn = document.getElementById('cast-btn');
  const uploadBtn = document.getElementById('upload-btn');
  const fileInput = document.getElementById('torrent-file-input');
  if (!input || !chip || !castBtn || !uploadBtn || !fileInput) return;

  let queuedFile = null;
  let chipTimer = 0;

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
    return input.value.trim() ? 'PASTE URL' : 'PASTE URL';
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
    chip.dataset.chipKind = 'err';
    chip.textContent = text;
    clearTimeout(chipTimer);
    chipTimer = setTimeout(() => {
      const k = queuedFile ? 'file' : detectKind(input.value);
      setChipKind(k);
    }, 4000);
  }

  function updateState() {
    const kind = queuedFile ? 'file' : detectKind(input.value);
    setChipKind(kind);
    const canCast = !!queuedFile || kind === 'url' || kind === 'magnet';
    castBtn.disabled = !canCast;
    castBtn.classList.toggle('disabled', !canCast);
    clearBtn.style.display = (input.value || queuedFile) ? '' : 'none';
  }

  let debounceTimer = 0;
  input.addEventListener('input', () => {
    clearTimeout(debounceTimer);
    debounceTimer = setTimeout(updateState, 120);
  });

  clearBtn.addEventListener('click', () => {
    input.value = '';
    queuedFile = null;
    fileInput.value = '';
    updateState();
  });

  uploadBtn.addEventListener('click', () => fileInput.click());

  fileInput.addEventListener('change', () => {
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
```

- [ ] **Step 4: Create `preset-bank.js`**

Create [internal/chassis/static/preset-bank.js](../../../internal/chassis/static/preset-bank.js):

```javascript
(function () {
  const bank = document.querySelector('.preset-bank');
  if (!bank) return;
  const slots = Array.from(bank.querySelectorAll('.preset'));

  function clearLit() {
    slots.forEach((el) => el.classList.remove('lit'));
  }

  function applyLit(providerId, channelId) {
    clearLit();
    if (!providerId || !channelId) return;
    for (const el of slots) {
      if (el.dataset.provider === providerId && el.dataset.channel === channelId) {
        el.classList.add('lit');
        break;
      }
    }
  }

  function parseAdapterRef(ref) {
    if (!ref || typeof ref !== 'string') return [null, null];
    if (!ref.startsWith('streams:')) return [null, null];
    const parts = ref.split(':');
    if (parts.length < 3) return [null, null];
    return [parts[1], parts[2]];
  }

  function onTransport(ev) {
    let data = {};
    try { data = JSON.parse(ev.data); } catch (_) { return; }
    const [providerId, channelId] = parseAdapterRef(data.adapterRef);
    applyLit(providerId, channelId);
  }

  function reportError(chip) {
    if (window.Chassis && window.Chassis.input && typeof window.Chassis.input.showError === 'function') {
      window.Chassis.input.showError(chip || 'CAST FAILED');
    }
  }

  bank.addEventListener('click', async (e) => {
    const btn = e.target.closest('.preset');
    if (!btn || btn.classList.contains('empty')) return;
    const slot = btn.dataset.slot;
    if (!slot) return;
    try {
      const resp = await fetch('/receiver/preset/' + encodeURIComponent(slot) + '/cast', {
        method: 'POST',
        credentials: 'same-origin',
      });
      const body = await resp.json().catch(() => ({ ok: false, chip: 'CAST FAILED' }));
      if (!body.ok) reportError(body.chip);
    } catch (_) {
      reportError('CAST FAILED');
    }
  });

  if (window.Chassis && window.Chassis.events && typeof window.Chassis.events.subscribe === 'function') {
    window.Chassis.events.subscribe('transport', onTransport);
  }
})();
```

- [ ] **Step 5: Update `chassis.css` with new rules**

Append to [internal/chassis/static/chassis.css](../../../internal/chassis/static/chassis.css):

```css
/* 3A: cast error chip variant */
body.receiver .chip[data-chip-kind="err"] {
  background: #c0392b;
  color: #fff;
  animation: chassis-chip-flash 220ms ease-out;
}

@keyframes chassis-chip-flash {
  0%   { transform: scale(1.0); }
  50%  { transform: scale(1.05); }
  100% { transform: scale(1.0); }
}

/* 3A: preset badge colors (port from v24 mockup) */
body.receiver .preset .badge.mtv     { color: oklch(0.62 0.04 350); }
body.receiver .preset .badge.cartoon { color: oklch(0.62 0.06 80); }
body.receiver .preset .badge.toonami { color: oklch(0.62 0.06 280); }

/* 3A: browse button disabled state (catalog drawer is Phase 3B) */
body.receiver .browse-btn[disabled] {
  opacity: 0.4;
  cursor: not-allowed;
  pointer-events: none;
}
```

- [ ] **Step 6: Run tests to confirm they pass**

```bash
go test ./internal/chassis -v
```

Expected: all chassis tests PASS — the JS existence tests, the CSS rules tests, and the existing CSS scope test (every new selector starts with `body.receiver`).

- [ ] **Step 7: Commit**

```bash
git add internal/chassis/static/
git commit -m "feat(chassis): add input-cast and preset-bank client JS plus cast CSS"
```

---

## Task 14: Wire `main.go`, Integration Test, and Final Verification

**Files:**
- Modify: `cmd/mister-groovy-relay/main.go`
- Modify: `tests/integration/chassis_test.go`

- [ ] **Step 1: Wire the streams adapter through chassis Config**

In [cmd/mister-groovy-relay/main.go](../../../cmd/mister-groovy-relay/main.go), locate the `chassis.New(chassis.Config{...})` call. Find or extract the registered streams adapter (the registry passes through `reg.Get("streams")` returning an `adapters.Adapter`). Type-assert it to the `PresetViewer`/`PresetCaster` interfaces and pass:

```go
var presetViewer adapters.PresetViewer
var presetCaster adapters.PresetCaster
if streamsAdapter, ok := reg.Get("streams"); ok {
    if v, ok := streamsAdapter.(adapters.PresetViewer); ok {
        presetViewer = v
    }
    if c, ok := streamsAdapter.(adapters.PresetCaster); ok {
        presetCaster = c
    }
}

chassisSrv, err := chassis.New(chassis.Config{
    // ... existing fields ...
    PresetViewer: presetViewer,
    PresetCaster: presetCaster,
})
```

Then immediately after `chassis.New` returns successfully, add the startup verification:

```go
if err := chassis.VerifyCastTabBindings(reg); err != nil {
    dieFriendly("chassis cast bindings", err)
}
```

(Promote `verifyCastTabBindings` to exported `VerifyCastTabBindings` in `cast.go` — single line change.)

- [ ] **Step 2: Add integration test**

In [tests/integration/chassis_test.go](../../../tests/integration/chassis_test.go), append a new test function:

```go
//go:build integration

func TestChassisIntegration_CastAndPresetEndToEnd(t *testing.T) {
	// Real chassis + fake registry containing URL + torrent + streams stubs.
	reg, urlCalls, torrentCalls, streamsCalls := buildFakeRegistry(t)

	srv, err := chassis.New(chassis.Config{
		Bridge:       integrationBridge(t),
		Manager:      integrationManager(t),
		Registry:     reg,
		Version:      "test",
		StartedAt:    time.Now(),
		HostIP:       "127.0.0.1",
		PresetViewer: streamsStub{},
		PresetCaster: streamsCalls,
	})
	if err != nil {
		t.Fatalf("chassis.New: %v", err)
	}
	mux := http.NewServeMux()
	srv.Mount(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Sub-test 1: URL paste round-trip
	t.Run("url paste", func(t *testing.T) {
		body := url.Values{"kind": {"url"}, "payload": {"https://example.test/x.mp4"}}.Encode()
		resp := mustPOSTForm(t, ts.URL+"/receiver/cast", body)
		if resp.StatusCode != 200 {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		if got := urlCalls.last(); got.TabID != "url" || got.Values["url"] != "https://example.test/x.mp4" {
			t.Errorf("url adapter received %+v, want TabID=url Values[url]=...", got)
		}
	})

	// Sub-test 2: magnet routes to torrent
	t.Run("magnet paste", func(t *testing.T) {
		body := url.Values{"kind": {"magnet"}, "payload": {"magnet:?xt=urn:btih:abc"}}.Encode()
		resp := mustPOSTForm(t, ts.URL+"/receiver/cast", body)
		if resp.StatusCode != 200 {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		if got := torrentCalls.last(); got.TabID != "torrent-magnet" {
			t.Errorf("torrent adapter received TabID=%q, want torrent-magnet", got.TabID)
		}
	})

	// Sub-test 3: torrent file upload
	t.Run("torrent upload", func(t *testing.T) {
		body, contentType := makeMultipart(t, "torrent_file", "example.torrent", []byte("d8:announce..."))
		resp := mustPOSTRaw(t, ts.URL+"/receiver/cast", contentType, body)
		if resp.StatusCode != 200 {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		if got := torrentCalls.last(); got.TabID != "torrent-file" || got.File == nil || got.File.FieldName != "torrent_file" {
			t.Errorf("torrent adapter file upload incorrect: %+v", got)
		}
	})

	// Sub-test 4: preset click
	t.Run("preset click", func(t *testing.T) {
		resp := mustPOSTForm(t, ts.URL+"/receiver/preset/3/cast", "")
		if resp.StatusCode != 200 {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		if got := streamsCalls.lastSlot(); got != 3 {
			t.Errorf("streams.CastPreset slot = %d, want 3", got)
		}
	})

	// Sub-test 5: preset out of range
	t.Run("preset bad slot", func(t *testing.T) {
		resp := mustPOSTForm(t, ts.URL+"/receiver/preset/0/cast", "")
		if resp.StatusCode != 400 {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
	})
}
```

Add the test helpers `buildFakeRegistry`, `streamsStub`, `mustPOSTForm`, `mustPOSTRaw`, `makeMultipart`, and `integrationBridge`/`integrationManager` either inline in the same test file or in a `testdata_test.go` helper. The streams stub implements `BundledPresets() [12]adapters.PresetEntry` returning the same 12-slot list (or a fixed test fixture) and `CastPreset(ctx, slot int) error` deferring to the calls recorder. URL/torrent stubs implement `adapters.QuickCastProvider` deferring to their recorders.

`mustPOSTForm` and `mustPOSTRaw` set the `Sec-Fetch-Site: same-origin` header.

- [ ] **Step 3: Run all tests**

```bash
go test ./...
go test -race ./...
go test -tags=integration ./tests/integration/...
```

Expected: all PASS. The race detector is important — the snapshot cache refresher reads `PresetViewer.BundledPresets()` concurrently with whatever else runs.

- [ ] **Step 4: Run lint**

```bash
make lint
```

Expected: clean.

- [ ] **Step 5: Verify chassis-package import discipline still holds**

```bash
go test ./internal/chassis -run TestProductionImports_NoCrossPackageCoupling -v
```

Expected: PASS. No new forbidden imports were introduced.

- [ ] **Step 6: Manual smoke (optional, post-merge sanity)**

If a real bridge is available:

- `make build && ./mister-groovy-relay --config testdata/config.toml`
- Open `http://localhost:32500/receiver` in a browser.
- Paste `https://example.test/x.mp4` → chip shows "URL" → click CAST → chip stays "URL" or shows an error chip from the URL adapter.
- Click preset slot 7 (Looney Tunes) → Streams cast attempts to start; chip on input row shows error if blocked.
- Click slot 11 (Toonami East) → preset shows `.live` styling (animated dot) and badge reads "TOONAMI · LIVE".

- [ ] **Step 7: Commit and push**

```bash
git add cmd/mister-groovy-relay/main.go tests/integration/chassis_test.go internal/chassis/cast.go
git commit -m "feat(chassis): wire streams as PresetViewer/PresetCaster and integration test"
```

- [ ] **Step 8: Open PR**

```bash
git push -u origin <branch>
gh pr create --title "feat(chassis): receiver input row and preset bank (Phase 3A)" --body "$(cat <<'EOF'
## Summary

- Wires the chassis input row to existing `adapters.QuickCastProvider` for URL and torrent paste, plus `.torrent` file upload
- Renders the 12-slot preset bank from streams adapter's bundled defaults; click fires a Streams cast
- Adds typed `adapters.QuickCastError` so chassis can extract status + chip text via `errors.As`
- LIT state derives from the existing `transport` SSE event's `AdapterRef`; no new SSE event

## Test plan

- [ ] `go test ./...` passes
- [ ] `go test -race ./...` passes
- [ ] `go test -tags=integration ./tests/integration/...` passes
- [ ] Manual smoke: paste URL → CAST works; paste magnet → CAST works; upload .torrent → CAST works; click preset → Streams cast; click Toonami preset → `.live` styling + "TOONAMI · LIVE" badge

Spec: docs/superpowers/specs/2026-05-25-receiver-chassis-input-and-presets-design.md

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

## Notes for the Implementer

- **Task 1 is load-bearing.** Land it first as a standalone commit. The 3A branch's CI must enforce the chassis-cannot-import-streams rule from line one of new code.
- **`detectCastKind` uses `net/url` not `strings.HasPrefix`.** `url.Parse` correctly handles `magnet:` (with or without `?`), whitespace, and case-insensitive schemes. Don't revert to prefix checks.
- **The two top-level `fmt.Errorf` returns in torrent's `HandleQuickCast`** become typed `*TorrentError` before they go through the wrap helper. Without that conversion, the wrap returns `nil` (because the err isn't a `*TorrentError`), and the chassis sees an untyped error → 500/CAST FAILED, losing the BLOCKED/409 semantics.
- **`Live` is the single source of truth.** Both the `.live` CSS class and the ` · LIVE` badge-text suffix render from the same `PresetSlot.Live` field. Don't bake "· LIVE" into the badge text in the adapter — that would diverge them.
- **`AdapterRef` parser extracts segments 1+2 only.** Segments 3+ (sessionID, itemToken) are intentionally discarded. A future change to the streams ref format must update both `parseStreamsAdapterRef` in `data.go` and `parseAdapterRef` in `preset-bank.js` together.
- **`writeCastJSON` omits `chip` on success.** Tests assert this — the success body is exactly `{"ok": true}`, not `{"ok": true, "chip": ""}`.
- **The chassis must not import streams adapter directly.** All preset wiring goes through `Config.PresetViewer` / `Config.PresetCaster`. If you see yourself reaching for `streams.SomethingDirectly`, stop — the architecture is wrong.
