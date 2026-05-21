# Music Visualizer Expansion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `vu_cabinet`, `neon_grid`, `raster_pulse`, `cover_vu`, and `cover_spectrum` music visualizer modes with safe Plex/Jellyfin album-art support.

**Architecture:** Phase 1 adds the three CRT arcade modes end to end through config, core validation, UI exposure, FFmpeg graph construction, required-filter checks, and integration coverage. Phase 2 adds a shared local artwork cache, provider artwork fetches, path/origin/token validation, FFmpeg artwork inputs, and the two cover-art modes in one release slice. Core never performs artwork network I/O; adapters fetch into `<bridge.data_dir>/artwork-cache/`, core validates local paths before mapping to FFmpeg, and FFmpeg stays a pure argv/filtergraph builder.

**Tech Stack:** Go, standard `net/http` / `net/url` / `image` packages, existing Plex and Jellyfin adapters, existing `core.Manager`, existing `internal/ffmpeg` command builder, FFmpeg filters documented at https://ffmpeg.org/ffmpeg-filters.html.

---

## File Structure

- Create `internal/artworkcache/cache.go`: cache-root helpers, same-origin URL anchoring, token redaction, image download/decode bounds, stale reaper, and `OnStop` cleanup wrapper.
- Create `internal/artworkcache/cache_test.go`: unit tests for root creation, path validation, origin pinning, token append ordering, redaction, decode bounds, cleanup, idempotent removal, and stale reaping.
- Modify `internal/config/config.go`: add new visualizer mode string constants and extend the supported mode list in two phases.
- Modify `internal/config/config_test.go`: cover supported mode lists, unknown-mode pass-through normalization, and clear validation failures.
- Modify `internal/config/example.toml`: expose only implemented modes in the example at each phase.
- Modify `internal/ui/bridge_fields.go`: keep the Bridge UI enum sourced from `config.SupportedVisualizerModes()`.
- Modify `internal/ui/bridge_fields_test.go`: assert the UI enum matches the phase-appropriate supported list.
- Modify `internal/core/types.go`: add new core visualizer constants and `VisualizerMetadata.ArtworkPath`.
- Modify `internal/core/manager.go`: validate new modes, preserve snapshotted mode behavior, validate artwork paths before creating `ffmpeg.VisualizerSpec`, and map all supported core modes to FFmpeg modes.
- Modify `internal/core/manager_test.go`: cover mode mapping, validation, same-session snapshot preservation, valid artwork pass-through, invalid artwork fallback, and no probe/network work before validation failures.
- Modify `internal/ffmpeg/pipeline.go`: add new FFmpeg mode constants, required-filter entries, CRT graph builders, artwork input indexing helpers, shared cover-art graph helpers, and cover-mode graph builders.
- Modify `internal/ffmpeg/capabilities.go`: rely on the expanded `RequiredVisualizerFilters` table for preflight and runtime capability checks.
- Modify `internal/ffmpeg/pipeline_test.go`: assert required filters, graph shapes, argv input indexes, placeholder behavior, cover-art image-input behavior, and dual-input audio/artwork mapping.
- Modify `internal/adapters/plex/transcode.go`: decode Plex artwork candidate attributes and add artwork download helper calls for music metadata.
- Modify `internal/adapters/plex/transcode_test.go`: test Plex track/album/artist artwork candidate extraction and same-origin rejection through the helper.
- Modify `internal/adapters/plex/companion.go`: fetch music artwork best-effort before `StartSession`, map `ArtworkPath`, and wrap existing music `OnStop` with artwork cleanup.
- Modify `internal/adapters/plex/companion_test.go`: assert artwork path mapping, fail-open download behavior, and composed `OnStop`.
- Modify `internal/adapters/jellyfin/playback.go`: add artwork path fields to item/playback metadata, derive the primary image URL, and map artwork into music session requests.
- Modify `internal/adapters/jellyfin/playback_test.go`: cover primary-image metadata, artwork fail-open behavior, and cleanup wrapping.
- Modify `internal/adapters/jellyfin/commands.go`: fetch Jellyfin artwork best-effort during music playback and track switching.
- Modify `cmd/mister-groovy-relay/main.go`: run the artwork stale-cache reaper once after config/data-dir preflight, before adapter startup.
- Create `tests/integration/visualizer_modes_test.go`: one real-FFmpeg smoke test per new visualizer mode.
- Modify `README.md`: document shipped modes and intentionally excluded `chiptune_equalizer` and `radial_spectrum`.

Before editing source, run:

```bash
git status --short
```

Expected: any pre-existing user edits remain visible. During execution, stage only paths listed by the task being committed. Do not use wildcard `git add`.

---

### Task 1: Phase 1 CRT Mode Constants, Validation, And UI Exposure

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/config/example.toml`
- Modify: `internal/ui/bridge_fields_test.go`
- Modify: `internal/core/types.go`
- Modify: `internal/core/manager.go`
- Modify: `internal/core/manager_test.go`

- [ ] **Step 1: Write failing config and UI tests**

Add this table to `TestSupportedVisualizerModes_ReturnsDefensiveCopy` in `internal/config/config_test.go`:

```go
want := []string{
	VisualizerModeRetroAnalyzer,
	VisualizerModeOscilloscopeWave,
	VisualizerModeStereoScope,
	VisualizerModeVUCabinet,
	VisualizerModeNeonGrid,
	VisualizerModeRasterPulse,
}
```

Extend `TestSectioned_Validate_VisualizerMode` valid cases with:

```go
VisualizerModeVUCabinet,
VisualizerModeNeonGrid,
VisualizerModeRasterPulse,
```

Add this regression test in the same file:

```go
func TestNormalizeVisualizerMode_PreservesUnknownNonEmptyValue(t *testing.T) {
	got := NormalizeVisualizerMode("future_mode")
	if got != "future_mode" {
		t.Fatalf("NormalizeVisualizerMode(future_mode) = %q, want future_mode", got)
	}
}
```

In `internal/ui/bridge_fields_test.go`, update `TestBridgeFields_HasVisualizerMode` so its `want` enum is:

```go
want := []string{
	config.VisualizerModeRetroAnalyzer,
	config.VisualizerModeOscilloscopeWave,
	config.VisualizerModeStereoScope,
	config.VisualizerModeVUCabinet,
	config.VisualizerModeNeonGrid,
	config.VisualizerModeRasterPulse,
}
```

Run:

```bash
go test ./internal/config ./internal/ui
```

Expected: compile failures for undefined CRT constants.

- [ ] **Step 2: Add config constants and phase-1 support list**

In `internal/config/config.go`, replace the visualizer constants with:

```go
const (
	VisualizerModeRetroAnalyzer    = "retro_analyzer"
	VisualizerModeOscilloscopeWave = "oscilloscope_wave"
	VisualizerModeStereoScope      = "stereo_scope"
	VisualizerModeVUCabinet        = "vu_cabinet"
	VisualizerModeNeonGrid         = "neon_grid"
	VisualizerModeRasterPulse      = "raster_pulse"
	VisualizerModeCoverVU          = "cover_vu"
	VisualizerModeCoverSpectrum    = "cover_spectrum"
)
```

For Phase 1, set `supportedVisualizerModes` to the current modes plus the three CRT modes only:

```go
var supportedVisualizerModes = []string{
	VisualizerModeRetroAnalyzer,
	VisualizerModeOscilloscopeWave,
	VisualizerModeStereoScope,
	VisualizerModeVUCabinet,
	VisualizerModeNeonGrid,
	VisualizerModeRasterPulse,
}
```

Extend the `Sectioned.Validate` visualizer switch with the same three CRT cases:

```go
case VisualizerModeRetroAnalyzer,
	VisualizerModeOscilloscopeWave,
	VisualizerModeStereoScope,
	VisualizerModeVUCabinet,
	VisualizerModeNeonGrid,
	VisualizerModeRasterPulse:
```

In `internal/config/example.toml`, update the mode comment for Phase 1:

```toml
mode = "retro_analyzer"           # retro_analyzer, oscilloscope_wave, stereo_scope, vu_cabinet, neon_grid, raster_pulse
```

Run:

```bash
go test ./internal/config ./internal/ui
```

Expected: config/UI tests pass; core tests still fail once the core tests below are added.

- [ ] **Step 3: Write failing core validation and mapping tests**

In `internal/core/manager_test.go`, extend `TestValidateVisualizerRequestSupportedModes` to include:

```go
VisualizerModeVUCabinet,
VisualizerModeNeonGrid,
VisualizerModeRasterPulse,
```

Extend `TestFFmpegVisualizerSpecMapsModes` with:

```go
{VisualizerModeVUCabinet, ffmpeg.VisualizerModeVUCabinet},
{VisualizerModeNeonGrid, ffmpeg.VisualizerModeNeonGrid},
{VisualizerModeRasterPulse, ffmpeg.VisualizerModeRasterPulse},
```

Run:

```bash
go test ./internal/core
```

Expected: compile failures for undefined core or FFmpeg mode constants.

- [ ] **Step 4: Add core constants and mapping**

In `internal/core/types.go`, extend the visualizer constants:

```go
const (
	VisualizerModeRetroAnalyzer    VisualizerMode = "retro_analyzer"
	VisualizerModeOscilloscopeWave VisualizerMode = "oscilloscope_wave"
	VisualizerModeStereoScope      VisualizerMode = "stereo_scope"
	VisualizerModeVUCabinet        VisualizerMode = "vu_cabinet"
	VisualizerModeNeonGrid         VisualizerMode = "neon_grid"
	VisualizerModeRasterPulse      VisualizerMode = "raster_pulse"
	VisualizerModeCoverVU          VisualizerMode = "cover_vu"
	VisualizerModeCoverSpectrum    VisualizerMode = "cover_spectrum"
)
```

In `internal/core/manager.go`, extend `validateVisualizerRequest`, `coreVisualizerModeFromConfig`, and `ffmpegVisualizerMode` with the three CRT modes. The mapping cases should be:

```go
case config.VisualizerModeVUCabinet:
	return VisualizerModeVUCabinet
case config.VisualizerModeNeonGrid:
	return VisualizerModeNeonGrid
case config.VisualizerModeRasterPulse:
	return VisualizerModeRasterPulse
```

and:

```go
case VisualizerModeVUCabinet:
	return ffmpeg.VisualizerModeVUCabinet
case VisualizerModeNeonGrid:
	return ffmpeg.VisualizerModeNeonGrid
case VisualizerModeRasterPulse:
	return ffmpeg.VisualizerModeRasterPulse
```

Run:

```bash
go test ./internal/config ./internal/ui ./internal/core
```

Expected: core tests still fail only because FFmpeg constants do not exist yet.

- [ ] **Step 5: Commit phase-1 mode plumbing after Task 2 passes**

Do not commit until Task 2 adds working FFmpeg graphs for these same mode IDs. The first commit should include Task 1 and Task 2 together so no accepted UI/config mode lacks a graph.

---

### Task 2: Phase 1 CRT FFmpeg Graphs And Required Filters

**Files:**
- Modify: `internal/ffmpeg/pipeline.go`
- Modify: `internal/ffmpeg/capabilities.go`
- Modify: `internal/ffmpeg/pipeline_test.go`
- Modify: files from Task 1

- [ ] **Step 1: Write failing FFmpeg graph and filter tests**

In `internal/ffmpeg/pipeline_test.go`, extend `TestVisualizerRequiredFilters` with:

```go
{VisualizerModeVUCabinet, []string{"showvolume", "drawbox", "drawgrid"}},
{VisualizerModeNeonGrid, []string{"showfreqs", "drawgrid", "hue"}},
{VisualizerModeRasterPulse, []string{"showwaves", "split", "hflip", "blend"}},
```

Extend `TestBuildVisualizerFilterChain_ModeGraphs` with:

```go
{
	name: "vu cabinet",
	mode: VisualizerModeVUCabinet,
	want: []string{
		"showvolume=",
		"drawbox=x=8:y=8",
		"drawgrid=w=iw/8:h=ih/4",
		"[visualizer_video]",
	},
},
{
	name: "neon grid",
	mode: VisualizerModeNeonGrid,
	want: []string{
		"showfreqs=s=640x480:mode=bar",
		"drawgrid=w=iw/12:h=ih/6",
		"hue=h=2*PI*t:s=1.35",
		"[visualizer_video]",
	},
},
{
	name: "raster pulse",
	mode: VisualizerModeRasterPulse,
	want: []string{
		"showwaves=s=640x480:mode=cline",
		"split[wave_a][wave_b]",
		"hflip[wave_flip]",
		"blend=all_mode=screen:all_opacity=0.70",
		"[visualizer_video]",
	},
},
```

Extend `TestBuildVisualizerFilterChain_AllModesBarsOnlyWhenDrawTextUnavailable` with:

```go
{"vu cabinet", VisualizerModeVUCabinet, "showvolume="},
{"neon grid", VisualizerModeNeonGrid, "showfreqs=s=640x480:mode=bar"},
{"raster pulse", VisualizerModeRasterPulse, "showwaves=s=640x480:mode=cline"},
```

Run:

```bash
go test ./internal/ffmpeg
```

Expected: compile failures for undefined FFmpeg constants.

- [ ] **Step 2: Add FFmpeg mode constants and required filters**

In `internal/ffmpeg/pipeline.go`, extend the constants:

```go
const (
	VisualizerModeRetroAnalyzer    VisualizerMode = "retro_analyzer"
	VisualizerModeOscilloscopeWave VisualizerMode = "oscilloscope_wave"
	VisualizerModeStereoScope      VisualizerMode = "stereo_scope"
	VisualizerModeVUCabinet        VisualizerMode = "vu_cabinet"
	VisualizerModeNeonGrid         VisualizerMode = "neon_grid"
	VisualizerModeRasterPulse      VisualizerMode = "raster_pulse"
	VisualizerModeCoverVU          VisualizerMode = "cover_vu"
	VisualizerModeCoverSpectrum    VisualizerMode = "cover_spectrum"
)
```

Extend `RequiredVisualizerFilters`:

```go
case VisualizerModeVUCabinet:
	return []string{"showvolume", "drawbox", "drawgrid"}
case VisualizerModeNeonGrid:
	return []string{"showfreqs", "drawgrid", "hue"}
case VisualizerModeRasterPulse:
	return []string{"showwaves", "split", "hflip", "blend"}
```

- [ ] **Step 3: Replace the visualizer core graph helper**

Replace `visualizerCoreFilter` with:

```go
func visualizerCoreGraph(mode VisualizerMode, audioMap string, logicalW, logicalH int) (string, string) {
	switch mode {
	case VisualizerModeRetroAnalyzer:
		return fmt.Sprintf("[%s]showfreqs=s=%dx%d:mode=bar:ascale=log:fscale=log:colors=0x70ff70[viz0]", audioMap, logicalW, logicalH), "viz0"
	case VisualizerModeOscilloscopeWave:
		return fmt.Sprintf("[%s]showwaves=s=%dx%d:mode=line:colors=0x58e8ff[viz0]", audioMap, logicalW, logicalH), "viz0"
	case VisualizerModeStereoScope:
		return fmt.Sprintf("[%s]avectorscope=s=%dx%d:mode=lissajous:draw=line:scale=lin:swap=0,format=rgba[viz0]", audioMap, logicalW, logicalH), "viz0"
	case VisualizerModeVUCabinet:
		meterH := logicalH / 5
		if meterH < 24 {
			meterH = 24
		}
		return fmt.Sprintf("[%s]showvolume=w=%d:h=%d:f=0.95:b=4:t=0:v=0:o=h:s=2:p=0.20:m=p:ds=log:dm=0.7:dmc=0xfff06b,scale=w=%d:h=%d:force_original_aspect_ratio=decrease,pad=w=%d:h=%d:x=(ow-iw)/2:y=(oh-ih)/2:color=0x05050a,drawbox=x=8:y=8:w=iw-16:h=ih-16:color=0x29ffc6@0.65:t=2,drawgrid=w=iw/8:h=ih/4:t=1:c=0x29ffc6@0.18[viz0]", audioMap, logicalW-64, meterH, logicalW, logicalH, logicalW, logicalH), "viz0"
	case VisualizerModeNeonGrid:
		return fmt.Sprintf("[%s]showfreqs=s=%dx%d:mode=bar:ascale=log:fscale=log:colors=0xff2bd6|0x28f7ff,drawgrid=w=iw/12:h=ih/6:t=1:c=0x28f7ff@0.22,hue=h=2*PI*t:s=1.35[viz0]", audioMap, logicalW, logicalH), "viz0"
	case VisualizerModeRasterPulse:
		return fmt.Sprintf("[%s]showwaves=s=%dx%d:mode=cline:colors=0x58e8ff|0xff4fd8:scale=sqrt,format=rgba,split[wave_a][wave_b];[wave_b]hflip[wave_flip];[wave_a][wave_flip]blend=all_mode=screen:all_opacity=0.70[viz0]", audioMap, logicalW, logicalH), "viz0"
	default:
		return "", ""
	}
}
```

In `buildVisualizerFilterChain`, replace the initial `parts` construction with:

```go
graph, label := visualizerCoreGraph(s.Visualizer.Mode, audioInputMap(s), logicalW, logicalH)
if graph == "" || label == "" {
	return "", fmt.Errorf("unsupported visualizer mode %q", s.Visualizer.Mode)
}
parts := []string{graph}
```

Run:

```bash
go test ./internal/ffmpeg ./internal/core ./internal/config ./internal/ui
```

Expected: PASS.

- [ ] **Step 4: Commit Phase 1 as one complete change**

```bash
git add internal/config/config.go internal/config/config_test.go internal/config/example.toml internal/ui/bridge_fields_test.go internal/core/types.go internal/core/manager.go internal/core/manager_test.go internal/ffmpeg/pipeline.go internal/ffmpeg/capabilities.go internal/ffmpeg/pipeline_test.go
git commit -m "feat(visualizer): add CRT arcade modes"
```

---

### Task 3: Shared Artwork Cache Package

**Files:**
- Create: `internal/artworkcache/cache.go`
- Create: `internal/artworkcache/cache_test.go`
- Modify: `cmd/mister-groovy-relay/main.go`

- [ ] **Step 1: Write failing cache tests**

Create `internal/artworkcache/cache_test.go` with these concrete helpers and test bodies:

```go
package artworkcache

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: 0x28, G: 0xf7, B: 0xff, A: 0xff})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func TestResolveSameOriginAnchorsRelativeAndRejectsForeignAbsolute(t *testing.T) {
	got, ok := ResolveSameOrigin("https://pms.example:32400", "/library/metadata/42/thumb")
	if !ok || got.String() != "https://pms.example:32400/library/metadata/42/thumb" {
		t.Fatalf("relative candidate = %v/%v", got, ok)
	}
	if got, ok := ResolveSameOrigin("https://pms.example:32400", "https://evil.example/cover.png"); ok {
		t.Fatalf("foreign absolute accepted: %s", got)
	}
}

func TestResolveSameOriginHonorsEffectiveDefaultPorts(t *testing.T) {
	if _, ok := ResolveSameOrigin("https://pms.example", "https://pms.example:443/art.png"); !ok {
		t.Fatal("https default port should match explicit 443")
	}
	if _, ok := ResolveSameOrigin("http://pms.example", "http://pms.example:443/art.png"); ok {
		t.Fatal("http default port must not match explicit 443")
	}
}

func TestAppendTokenAfterResolveSameOrigin(t *testing.T) {
	u, ok := ResolveSameOrigin("http://pms.example:32400", "/art.png")
	if !ok {
		t.Fatal("ResolveSameOrigin rejected same-origin relative URL")
	}
	got := AppendToken(u, "X-Plex-Token", "tok")
	if !strings.Contains(got, "X-Plex-Token=tok") || strings.Contains(got, "evil.example") {
		t.Fatalf("AppendToken = %q", got)
	}
}

func TestRedactURLRemovesUserinfoAndTokenKeys(t *testing.T) {
	raw := "https://user:pass@example.test/art.png?X-Plex-Token=plex&api_key=jf&X-Emby-Token=emby&AccessToken=access&token=plain&keep=1"
	got := RedactURL(raw)
	for _, leaked := range []string{"user", "pass", "plex", "jf", "emby", "access", "plain"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("RedactURL leaked %q in %q", leaked, got)
		}
	}
	if !strings.Contains(got, "keep=1") {
		t.Fatalf("RedactURL removed non-token query: %q", got)
	}
}

func TestValidatePathAcceptsCanonicalDescendant(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "cover.png")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, ok := ValidatePath(root, path)
	if !ok || got == "" {
		t.Fatalf("ValidatePath = %q/%v, want accepted", got, ok)
	}
}

func TestValidatePathRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "cover.png")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "escape.png")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if got, ok := ValidatePath(root, link); ok {
		t.Fatalf("ValidatePath accepted symlink escape: %q", got)
	}
}

func TestFetchToCacheRejectsOversizedContentLength(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "8388609")
		_, _ = w.Write([]byte("x"))
	}))
	defer srv.Close()
	_, err := FetchToCache(context.Background(), FetchOptions{DataDir: t.TempDir(), URL: srv.URL, Client: srv.Client()})
	if err == nil || !strings.Contains(err.Error(), "content length") {
		t.Fatalf("FetchToCache err = %v, want content length rejection", err)
	}
}

func TestFetchToCacheRejectsOversizedDecodedDimensions(t *testing.T) {
	body := pngBytes(t, MaxDimension+1, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	_, err := FetchToCache(context.Background(), FetchOptions{DataDir: t.TempDir(), URL: srv.URL, Client: srv.Client()})
	if err == nil || !strings.Contains(err.Error(), "dimensions") {
		t.Fatalf("FetchToCache err = %v, want dimension rejection", err)
	}
}

func TestFetchToCacheWritesImageUnderArtworkRoot(t *testing.T) {
	body := pngBytes(t, 8, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	dataDir := t.TempDir()
	path, err := FetchToCache(context.Background(), FetchOptions{DataDir: dataDir, URL: srv.URL, Client: srv.Client()})
	if err != nil {
		t.Fatalf("FetchToCache: %v", err)
	}
	if _, ok := ValidatePath(Root(dataDir), path); !ok {
		t.Fatalf("cached path %q not under artwork root %q", path, Root(dataDir))
	}
}

func TestWithCleanupInvokesOriginalAndRemovesPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cover.png")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	called := false
	WithCleanup(path, func(reason string) {
		called = reason == "eof"
	})("eof")
	if !called {
		t.Fatal("wrapped OnStop was not invoked")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("artwork still exists after cleanup: %v", err)
	}
}

func TestWithCleanupRemovesPathWhenOriginalPanics(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cover.png")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("expected original panic to propagate")
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("artwork still exists after panic cleanup: %v", err)
		}
	}()
	WithCleanup(path, func(string) { panic("boom") })("error")
}

func TestRemoveIgnoresEmptyAndMissing(t *testing.T) {
	Remove("")
	Remove(filepath.Join(t.TempDir(), "missing.png"))
}

func TestReapStaleRemovesOldFilesOnly(t *testing.T) {
	root := t.TempDir()
	oldPath := filepath.Join(root, "old.png")
	newPath := filepath.Join(root, "new.png")
	if err := os.WriteFile(oldPath, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := os.Chtimes(oldPath, now.Add(-25*time.Hour), now.Add(-25*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := ReapStale(root, 24*time.Hour, now); err != nil {
		t.Fatalf("ReapStale: %v", err)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("old file still exists: %v", err)
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("new file missing: %v", err)
	}
}
```

Run:

```bash
go test ./internal/artworkcache
```

Expected: package does not exist.

- [ ] **Step 2: Implement `internal/artworkcache/cache.go`**

Create `internal/artworkcache/cache.go`:

```go
package artworkcache

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	DirName      = "artwork-cache"
	MaxBytes     = 8 * 1024 * 1024
	MaxDimension = 4096
)

func Root(dataDir string) string {
	return filepath.Join(dataDir, DirName)
}

func EnsureRoot(dataDir string) (string, error) {
	root := Root(dataDir)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	return root, nil
}

func ValidatePath(root, path string) (string, bool) {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(path) == "" {
		return "", false
	}
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", false
	}
	pathReal, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(rootReal, pathReal)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", false
	}
	return pathReal, true
}

func ResolveSameOrigin(serverURL, candidate string) (*url.URL, bool) {
	base, err := url.Parse(strings.TrimSpace(serverURL))
	if err != nil || base.Scheme == "" || base.Host == "" {
		return nil, false
	}
	raw := strings.TrimSpace(candidate)
	if raw == "" {
		return nil, false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, false
	}
	if !u.IsAbs() {
		u = base.ResolveReference(u)
	}
	if !sameOrigin(base, u) {
		return nil, false
	}
	return u, true
}

func AppendToken(u *url.URL, key, token string) string {
	cp := *u
	q := cp.Query()
	if token != "" && key != "" {
		q.Set(key, token)
	}
	cp.RawQuery = q.Encode()
	return cp.String()
}

func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.User = nil
	q := u.Query()
	for _, key := range []string{"X-Plex-Token", "api_key", "X-Emby-Token", "AccessToken", "token"} {
		if _, ok := q[key]; ok {
			q.Set(key, "REDACTED")
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

type FetchOptions struct {
	DataDir string
	URL     string
	Client  *http.Client
}

func FetchToCache(ctx context.Context, opt FetchOptions) (string, error) {
	if opt.Client == nil {
		opt.Client = http.DefaultClient
	}
	if opt.DataDir == "" {
		return "", fmt.Errorf("artwork cache data dir is empty")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, opt.URL, nil)
	if err != nil {
		return "", err
	}
	resp, err := opt.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("artwork fetch: HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > MaxBytes {
		return "", fmt.Errorf("artwork fetch: content length %d exceeds %d", resp.ContentLength, MaxBytes)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBytes+1))
	if err != nil {
		return "", err
	}
	if len(body) > MaxBytes {
		return "", fmt.Errorf("artwork fetch: body exceeds %d bytes", MaxBytes)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("artwork decode config: %w", err)
	}
	if cfg.Width > MaxDimension || cfg.Height > MaxDimension {
		return "", fmt.Errorf("artwork dimensions %dx%d exceed %d", cfg.Width, cfg.Height, MaxDimension)
	}
	root, err := EnsureRoot(opt.DataDir)
	if err != nil {
		return "", err
	}
	name, err := randomName(format)
	if err != nil {
		return "", err
	}
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func Remove(path string) {
	if strings.TrimSpace(path) == "" {
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		slog.Debug("artwork cache remove", "path", path, "err", err)
	}
}

func WithCleanup(path string, original func(reason string)) func(reason string) {
	return func(reason string) {
		defer Remove(path)
		if original != nil {
			original(reason)
		}
	}
}

func ReapStale(root string, maxAge time.Duration, now time.Time) error {
	if root == "" {
		return nil
	}
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) > maxAge {
			Remove(filepath.Join(root, entry.Name()))
		}
	}
	return nil
}

func sameOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) &&
		strings.EqualFold(a.Hostname(), b.Hostname()) &&
		effectivePort(a) == effectivePort(b)
}

func effectivePort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	switch strings.ToLower(u.Scheme) {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}

func randomName(format string) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	ext := ".img"
	switch format {
	case "jpeg":
		ext = ".jpg"
	case "png":
		ext = ".png"
	case "gif":
		ext = ".gif"
	}
	return hex.EncodeToString(b[:]) + ext, nil
}
```

Run:

```bash
go test ./internal/artworkcache
```

Expected: PASS.

- [ ] **Step 3: Add startup stale reaper**

In `cmd/mister-groovy-relay/main.go`, import:

```go
"github.com/idio-sync/MiSTer_GroovyRelay/internal/artworkcache"
```

After `reapHLSBufferCaches`, add:

```go
if err := artworkcache.ReapStale(artworkcache.Root(sec.Bridge.DataDir), 24*time.Hour, time.Now()); err != nil {
	slog.Warn("artwork cache reap failed", "err", err)
}
```

Run:

```bash
go test ./internal/artworkcache ./cmd/mister-groovy-relay
```

Expected: PASS.

- [ ] **Step 4: Commit artwork cache package**

```bash
git add internal/artworkcache/cache.go internal/artworkcache/cache_test.go cmd/mister-groovy-relay/main.go
git commit -m "feat(visualizer): add artwork cache"
```

---

### Task 4: Artwork Metadata Boundary And Core Path Validation

**Files:**
- Modify: `internal/core/types.go`
- Modify: `internal/core/manager.go`
- Modify: `internal/core/manager_test.go`
- Modify: `internal/ffmpeg/pipeline.go`
- Modify: `internal/ffmpeg/pipeline_test.go`

- [ ] **Step 1: Write failing metadata and validation tests**

In `internal/core/manager_test.go`, add:

```go
func TestFFmpegVisualizerSpecCarriesValidatedArtworkPath(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "cover.png")
	if err := os.WriteFile(path, []byte("png bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := ffmpegVisualizerSpec(root, VisualizerRequest{
		Enabled: true,
		Mode:    VisualizerModeCoverVU,
		Metadata: VisualizerMetadata{
			Title:       "Blue Monday",
			ArtworkPath: path,
		},
	})
	if got.Metadata.ArtworkPath != path {
		t.Fatalf("ArtworkPath = %q, want %q", got.Metadata.ArtworkPath, path)
	}
}

func TestFFmpegVisualizerSpecDropsArtworkPathOutsideCacheRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "cover.png")
	if err := os.WriteFile(outside, []byte("png bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := ffmpegVisualizerSpec(root, VisualizerRequest{
		Enabled: true,
		Mode:    VisualizerModeCoverVU,
		Metadata: VisualizerMetadata{ArtworkPath: outside},
	})
	if got.Metadata.ArtworkPath != "" {
		t.Fatalf("ArtworkPath = %q, want empty for outside-cache path", got.Metadata.ArtworkPath)
	}
}
```

Add `os`, `path/filepath`, and `internal/artworkcache` imports as needed.

In `internal/ffmpeg/pipeline_test.go`, add:

```go
func TestVisualizerArtworkInputIndexesAfterAudioInputs(t *testing.T) {
	cases := []struct {
		name string
		spec PipelineSpec
		wantArgs []string
		wantLabel string
	}{
		{
			name: "single input",
			spec: PipelineSpec{Visualizer: VisualizerSpec{Mode: VisualizerModeCoverVU, Metadata: VisualizerMetadata{ArtworkPath: "cover.png"}}},
			wantArgs: []string{"-loop", "1", "-i", "cover.png"},
			wantLabel: "1:v:0",
		},
		{
			name: "dual audio input",
			spec: PipelineSpec{AudioInputURL: "audio.mp3", Visualizer: VisualizerSpec{Mode: VisualizerModeCoverSpectrum, Metadata: VisualizerMetadata{ArtworkPath: "cover.png"}}},
			wantArgs: []string{"-loop", "1", "-i", "cover.png"},
			wantLabel: "2:v:0",
		},
		{
			name: "missing artwork",
			spec: PipelineSpec{Visualizer: VisualizerSpec{Mode: VisualizerModeCoverVU}},
			wantArgs: nil,
			wantLabel: "",
		},
		{
			name: "non-cover mode ignores artwork path",
			spec: PipelineSpec{Visualizer: VisualizerSpec{Mode: VisualizerModeNeonGrid, Metadata: VisualizerMetadata{ArtworkPath: "cover.png"}}},
			wantArgs: nil,
			wantLabel: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args, label := visualizerArtworkInput(tc.spec)
			if !reflect.DeepEqual(args, tc.wantArgs) || label != tc.wantLabel {
				t.Fatalf("visualizerArtworkInput = %v/%q, want %v/%q", args, label, tc.wantArgs, tc.wantLabel)
			}
		})
	}
}

func TestBuildCommand_NonCoverModeIgnoresArtworkPath(t *testing.T) {
	spec := PipelineSpec{
		InputURL:        "http://pms/music.mp3",
		SourceProbe:     &ProbeResult{AudioRate: 44100, Duration: 180},
		OutputWidth:     720,
		OutputHeight:    480,
		OutputFpsExpr:   "60000/1001",
		AudioSampleRate: 48000,
		AudioChannels:   2,
		VideoPipePath:   "pipe:3",
		AudioPipePath:   "pipe:4",
		Visualizer: VisualizerSpec{
			Enabled:                  true,
			Mode:                     VisualizerModeNeonGrid,
			DrawTextAvailable:        true,
			RequiredFiltersAvailable: true,
			Metadata:                 VisualizerMetadata{ArtworkPath: "cover.png"},
		},
	}
	cmd := BuildCommand(context.Background(), spec)
	joined := strings.Join(cmd.Args, " ")
	if strings.Contains(joined, "-loop 1 -i cover.png") {
		t.Fatalf("non-cover visualizer should ignore ArtworkPath, argv=%s", joined)
	}
	if !strings.Contains(joined, "-map [visualizer_video]") {
		t.Fatalf("visualizer argv missing video map: %s", joined)
	}
}
```

Run:

```bash
go test ./internal/core ./internal/ffmpeg
```

Expected: compile failures for `ArtworkPath`, changed `ffmpegVisualizerSpec`, and `visualizerArtworkInput`.

- [ ] **Step 2: Add metadata fields and validated core mapping**

Add `ArtworkPath string` to both `core.VisualizerMetadata` and `ffmpeg.VisualizerMetadata`:

```go
type VisualizerMetadata struct {
	Title       string
	Artist      string
	Album       string
	Duration    time.Duration
	ArtworkPath string
}
```

In `internal/core/manager.go`, import `internal/artworkcache` and change `ffmpegVisualizerSpec` to:

```go
func ffmpegVisualizerSpec(artworkRoot string, v VisualizerRequest) ffmpeg.VisualizerSpec {
	if !v.Enabled {
		return ffmpeg.VisualizerSpec{}
	}
	artworkPath := ""
	if p, ok := artworkcache.ValidatePath(artworkRoot, v.Metadata.ArtworkPath); ok {
		artworkPath = p
	} else if v.Metadata.ArtworkPath != "" {
		slog.Warn("dropping invalid visualizer artwork path", "path", v.Metadata.ArtworkPath)
	}
	return ffmpeg.VisualizerSpec{
		Enabled: true,
		Mode:    ffmpegVisualizerMode(v.Mode),
		Metadata: ffmpeg.VisualizerMetadata{
			Title:       v.Metadata.Title,
			Artist:      v.Metadata.Artist,
			Album:       v.Metadata.Album,
			Duration:    v.Metadata.Duration,
			ArtworkPath: artworkPath,
		},
	}
}
```

In `startPlaneLocked`, pass the root:

```go
Visualizer: ffmpegVisualizerSpec(artworkcache.Root(m.bridge.DataDir), req.Visualizer),
```

Update direct test calls to `ffmpegVisualizerSpec` by passing `artworkcache.Root(t.TempDir())` or an explicit temp root.

- [ ] **Step 3: Add FFmpeg artwork input helper**

In `internal/ffmpeg/pipeline.go`, add:

```go
func visualizerAudioInputMap(s PipelineSpec) string {
	return audioInputMap(s)
}

func visualizerModeUsesArtwork(mode VisualizerMode) bool {
	return mode == VisualizerModeCoverVU || mode == VisualizerModeCoverSpectrum
}

func visualizerArtworkInput(s PipelineSpec) ([]string, string) {
	if !visualizerModeUsesArtwork(s.Visualizer.Mode) {
		return nil, ""
	}
	path := strings.TrimSpace(s.Visualizer.Metadata.ArtworkPath)
	if path == "" {
		return nil, ""
	}
	idx := 1
	if s.AudioInputURL != "" {
		idx = 2
	}
	return []string{"-loop", "1", "-i", path}, fmt.Sprintf("%d:v:0", idx)
}
```

In `BuildCommand`, inside the `if s.Visualizer.Enabled` branch and before `buildVisualizerFilterChain`, append the artwork input args:

```go
artworkArgs, _ := visualizerArtworkInput(s)
args = append(args, artworkArgs...)
audioMap := visualizerAudioInputMap(s)
```

Run:

```bash
go test ./internal/core ./internal/ffmpeg
```

Expected: PASS.

- [ ] **Step 4: Commit metadata boundary**

```bash
git add internal/core/types.go internal/core/manager.go internal/core/manager_test.go internal/ffmpeg/pipeline.go internal/ffmpeg/pipeline_test.go
git commit -m "feat(visualizer): validate artwork metadata boundary"
```

---

### Task 5: Plex Album-Art Fetching

**Files:**
- Modify: `internal/adapters/plex/transcode.go`
- Modify: `internal/adapters/plex/transcode_test.go`
- Modify: `internal/adapters/plex/companion.go`
- Modify: `internal/adapters/plex/companion_test.go`

- [ ] **Step 1: Write failing Plex metadata and fetch tests**

In `internal/adapters/plex/transcode_test.go`, extend `TestMusicMetadataFor_DecodesTrack` XML fixture with:

```xml
thumb="/library/metadata/42/thumb/123"
parentThumb="/library/metadata/7/thumb/123"
grandparentThumb="/library/metadata/1/thumb/123"
```

Assert:

```go
wantCandidates := []string{
	"/library/metadata/42/thumb/123",
	"/library/metadata/7/thumb/123",
	"/library/metadata/1/thumb/123",
}
if !reflect.DeepEqual(got.ArtworkCandidates, wantCandidates) {
	t.Fatalf("ArtworkCandidates = %#v, want %#v", got.ArtworkCandidates, wantCandidates)
}
```

In `internal/adapters/plex/companion_test.go`, add these helpers and tests. Add `bytes`, `image`, `image/color`, `image/png`, and `os` imports if they are not already present:

```go
func plexTinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{R: 0xff, G: 0xf0, B: 0x6b, A: 0xff})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func TestPlexCompanion_PlayMediaMusicCachesArtwork(t *testing.T) {
	dataDir := t.TempDir()
	pms := newLoopbackServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/library/metadata/42":
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<MediaContainer size="1">
				<Track title="Blue Monday" grandparentTitle="New Order"
					parentTitle="Power Corruption &amp; Lies" duration="449000"
					thumb="/library/metadata/42/thumb/123">
					<Media><Part id="99" key="/library/parts/99/file.mp3"/></Media>
				</Track>
			</MediaContainer>`))
		case "/library/metadata/42/thumb/123":
			if got := r.URL.Query().Get("X-Plex-Token"); got != "tok" {
				t.Fatalf("artwork token = %q, want tok", got)
			}
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(plexTinyPNG(t))
		default:
			t.Errorf("unexpected PMS path %q", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer pms.Close()
	pmsURL, _ := url.Parse(pms.URL)

	fc := &fakeCore{}
	c := NewCompanion(CompanionConfig{DeviceName: "MiSTer", DeviceUUID: "our-uuid", Version: "dev", DataDir: dataDir}, fc)
	ts := newLoopbackServer(t, c.Handler())
	defer ts.Close()

	reqURL := ts.URL + "/player/playback/playMedia?" +
		"protocol=http&address=" + pmsURL.Hostname() + "&port=" + pmsURL.Port() + "&" +
		"key=%2Flibrary%2Fmetadata%2F42&offset=0&token=tok&type=track"
	req, _ := http.NewRequest(http.MethodGet, reqURL, nil)
	req.Header.Set("X-Plex-Target-Client-Identifier", "our-uuid")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body=%s", resp.StatusCode, body)
	}
	artworkPath := fc.lastReq.Visualizer.Metadata.ArtworkPath
	if artworkPath == "" {
		t.Fatal("ArtworkPath empty, want cached artwork")
	}
	if !strings.Contains(artworkPath, "artwork-cache") {
		t.Fatalf("ArtworkPath = %q, want artwork-cache path", artworkPath)
	}
	if _, err := os.Stat(artworkPath); err != nil {
		t.Fatalf("cached artwork missing: %v", err)
	}
	if fc.lastReq.OnStop == nil {
		t.Fatal("OnStop nil, want cleanup wrapper around existing Plex cleanup")
	}
	fc.lastReq.OnStop("eof")
	if _, err := os.Stat(artworkPath); !os.IsNotExist(err) {
		t.Fatalf("artwork not removed by OnStop: %v", err)
	}
}

func TestPlexCompanion_PlayMediaMusicRejectsForeignArtworkAndStillStarts(t *testing.T) {
	pms := newLoopbackServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/library/metadata/42" {
			t.Errorf("unexpected PMS path %q", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<MediaContainer size="1">
			<Track title="Blue Monday" duration="449000" thumb="https://evil.example/cover.png">
				<Media><Part id="99" key="/library/parts/99/file.mp3"/></Media>
			</Track>
		</MediaContainer>`))
	}))
	defer pms.Close()
	pmsURL, _ := url.Parse(pms.URL)

	fc := &fakeCore{}
	c := NewCompanion(CompanionConfig{DeviceName: "MiSTer", DeviceUUID: "our-uuid", Version: "dev", DataDir: t.TempDir()}, fc)
	ts := newLoopbackServer(t, c.Handler())
	defer ts.Close()

	reqURL := ts.URL + "/player/playback/playMedia?" +
		"protocol=http&address=" + pmsURL.Hostname() + "&port=" + pmsURL.Port() + "&" +
		"key=%2Flibrary%2Fmetadata%2F42&offset=0&token=tok&type=track"
	resp, err := http.Get(reqURL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body=%s", resp.StatusCode, body)
	}
	if fc.starts != 1 {
		t.Fatalf("StartSession calls = %d, want 1", fc.starts)
	}
	if fc.lastReq.Visualizer.Metadata.ArtworkPath != "" {
		t.Fatalf("ArtworkPath = %q, want empty for foreign artwork URL", fc.lastReq.Visualizer.Metadata.ArtworkPath)
	}
}
```

Run:

```bash
go test ./internal/adapters/plex
```

Expected: compile failures for `ArtworkCandidates` and empty artwork path assertions.

- [ ] **Step 2: Decode Plex artwork candidates**

In `MusicMetadata`, add:

```go
ArtworkCandidates []string
ArtworkPath       string
```

In `pmsMediaContainer.Track`, add:

```go
Thumb            string `xml:"thumb,attr"`
ParentThumb      string `xml:"parentThumb,attr"`
GrandparentThumb string `xml:"grandparentThumb,attr"`
```

In `MusicMetadataFor`, populate candidates in track, album, artist order:

```go
ArtworkCandidates: uniqueNonEmpty(tr.Thumb, tr.ParentThumb, tr.GrandparentThumb),
```

Add:

```go
func uniqueNonEmpty(values ...string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}
```

- [ ] **Step 3: Fetch Plex artwork best-effort**

In `internal/adapters/plex/companion.go`, import `internal/artworkcache`.

Add:

```go
func (c *Companion) fetchMusicArtwork(ctx context.Context, p PlayMediaRequest, md MusicMetadata) string {
	for _, candidate := range md.ArtworkCandidates {
		u, ok := artworkcache.ResolveSameOrigin(p.serverURL(), candidate)
		if !ok {
			slog.Debug("plex artwork candidate rejected", "url", artworkcache.RedactURL(candidate))
			continue
		}
		reqURL := artworkcache.AppendToken(u, "X-Plex-Token", p.PlexToken)
		artCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		path, err := artworkcache.FetchToCache(artCtx, artworkcache.FetchOptions{
			DataDir: c.cfg.DataDir,
			URL:     reqURL,
			Client:  plexHTTPClient,
		})
		cancel()
		if err != nil {
			slog.Debug("plex artwork fetch failed", "url", artworkcache.RedactURL(reqURL), "err", err)
			continue
		}
		return path
	}
	return ""
}
```

In `musicMetadataForPlay`, after `ok` is true:

```go
md.ArtworkPath = c.fetchMusicArtwork(ctx, p, md)
return md, true
```

In `musicSessionRequestForPlay`, map the path:

```go
ArtworkPath: md.ArtworkPath,
```

After assigning the existing `req.OnStop`, wrap it:

```go
req.OnStop = artworkcache.WithCleanup(md.ArtworkPath, req.OnStop)
```

Run:

```bash
go test ./internal/adapters/plex
```

Expected: PASS.

- [ ] **Step 4: Commit Plex artwork support**

```bash
git add internal/adapters/plex/transcode.go internal/adapters/plex/transcode_test.go internal/adapters/plex/companion.go internal/adapters/plex/companion_test.go
git commit -m "feat(plex): cache music artwork for visualizers"
```

---

### Task 6: Jellyfin Album-Art Fetching

**Files:**
- Modify: `internal/adapters/jellyfin/playback.go`
- Modify: `internal/adapters/jellyfin/playback_test.go`
- Modify: `internal/adapters/jellyfin/commands_test.go`
- Modify: `internal/adapters/jellyfin/commands.go`

- [ ] **Step 1: Write failing Jellyfin artwork tests**

In `internal/adapters/jellyfin/commands_test.go`, add these helpers and tests. Add `bytes`, `image`, `image/color`, `image/png`, and `os` imports if they are not already present:

```go
func jellyfinTinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{R: 0x28, G: 0xf7, B: 0xff, A: 0xff})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func waitForJellyfinStartSession(t *testing.T, mgr *fakeManager) core.SessionRequest {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		req := mgr.lastReq()
		if req.StreamURL != "" {
			return req
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("StartSession was not called")
	return core.SessionRequest{}
}

func startTestAudioArtworkServer(t *testing.T, imageStatus int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/Items/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/Items/song-1/Images/Primary":
			if got := r.URL.Query().Get("api_key"); got != "tok" {
				t.Fatalf("artwork api_key = %q, want tok", got)
			}
			if imageStatus != http.StatusOK {
				w.WriteHeader(imageStatus)
				return
			}
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(jellyfinTinyPNG(t))
		case r.URL.Path == "/Items/song-1":
			_, _ = w.Write([]byte(`{
				"Id":"song-1",
				"Type":"Audio",
				"Name":"Age of Consent",
				"Artists":["New Order"],
				"Album":"Power Corruption & Lies",
				"RunTimeTicks":3150000000
			}`))
		case r.URL.Path == "/Items/song-1/PlaybackInfo":
			_, _ = w.Write([]byte(`{
				"MediaSources":[{"Id":"src-audio","Name":"Audio Source","TranscodingUrl":"/audio/song-1/universal?MediaSourceId=src-audio"}],
				"PlaySessionId":"ps-audio",
				"Item":{
					"Type":"Audio",
					"Name":"Age of Consent",
					"Artists":["New Order"],
					"Album":"Power Corruption & Lies",
					"RunTimeTicks":3150000000
				}
			}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestHandlePlay_AudioItemCachesPrimaryArtwork(t *testing.T) {
	jfSrv := startTestAudioArtworkServer(t, http.StatusOK)
	mgr := &fakeManager{}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}

	a.HandlePlay(mustMarshal(t, map[string]any{
		"ItemIds":     []string{"song-1"},
		"PlayCommand": "PlayNow",
	}))

	req := waitForJellyfinStartSession(t, mgr)
	artworkPath := req.Visualizer.Metadata.ArtworkPath
	if artworkPath == "" {
		t.Fatal("ArtworkPath empty, want cached Jellyfin primary image")
	}
	if !strings.Contains(artworkPath, "artwork-cache") {
		t.Fatalf("ArtworkPath = %q, want artwork-cache path", artworkPath)
	}
	if _, err := os.Stat(artworkPath); err != nil {
		t.Fatalf("cached artwork missing: %v", err)
	}
	if req.OnStop == nil {
		t.Fatal("OnStop nil, want cleanup wrapper around existing Jellyfin OnStop")
	}
	req.OnStop("eof")
	if _, err := os.Stat(artworkPath); !os.IsNotExist(err) {
		t.Fatalf("artwork not removed by OnStop: %v", err)
	}
}

func TestHandlePlay_AudioArtwork404FallsBack(t *testing.T) {
	jfSrv := startTestAudioArtworkServer(t, http.StatusNotFound)
	mgr := &fakeManager{}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}

	a.HandlePlay(mustMarshal(t, map[string]any{
		"ItemIds":     []string{"song-1"},
		"PlayCommand": "PlayNow",
	}))

	req := waitForJellyfinStartSession(t, mgr)
	if req.MediaKind != core.MediaKindMusic || !req.Visualizer.Enabled {
		t.Fatalf("request = %+v, want music visualizer", req)
	}
	if req.Visualizer.Metadata.ArtworkPath != "" {
		t.Fatalf("ArtworkPath = %q, want empty on 404 fallback", req.Visualizer.Metadata.ArtworkPath)
	}
}
```

Run:

```bash
go test ./internal/adapters/jellyfin
```

Expected: failing assertions for empty artwork path.

- [ ] **Step 2: Add artwork path fields**

In `PlaybackInfoResult` and `ItemMetadataResult`, add:

```go
ArtworkPath string
```

In `mergePlaybackMetadata`, add:

```go
if info.ArtworkPath == "" {
	info.ArtworkPath = meta.ArtworkPath
}
```

In `buildSessionRequest`, add to visualizer metadata:

```go
ArtworkPath: in.PlayInfo.ArtworkPath,
```

In `internal/adapters/jellyfin/playback.go`, import `internal/artworkcache`.

Wrap `OnStop` before building the request:

```go
onStop := a.makeOnStop(refKey)
onStop = artworkcache.WithCleanup(in.PlayInfo.ArtworkPath, onStop)
```

and use:

```go
OnStop: onStop,
```

- [ ] **Step 3: Fetch Jellyfin primary artwork best-effort**

In `internal/adapters/jellyfin/commands.go`, import `net/url`, `strings`, and `internal/artworkcache` if not already present.

Add:

```go
func (a *Adapter) fetchArtworkBestEffort(ctx context.Context, cfg Config, tok Token, itemID string) string {
	if strings.TrimSpace(itemID) == "" {
		return ""
	}
	candidate := "/Items/" + url.PathEscape(itemID) + "/Images/Primary"
	u, ok := artworkcache.ResolveSameOrigin(cfg.ServerURL, candidate)
	if !ok {
		return ""
	}
	reqURL := artworkcache.AppendToken(u, "api_key", tok.AccessToken)
	artCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	path, err := artworkcache.FetchToCache(artCtx, artworkcache.FetchOptions{
		DataDir: a.dataDir,
		URL:     reqURL,
		Client:  jfHTTPClient,
	})
	if err != nil {
		slog.Debug("jellyfin artwork fetch failed", "url", artworkcache.RedactURL(reqURL), "err", err)
		return ""
	}
	return path
}
```

In `fetchItemMetadataBestEffort`, after successful metadata:

```go
if meta.MediaKind == core.MediaKindMusic {
	meta.ArtworkPath = a.fetchArtworkBestEffort(ctx, cfg, tok, itemID)
}
```

This covers `startPlayNow`, queued items, and track switches because they already call `fetchItemMetadataBestEffort`.

Run:

```bash
go test ./internal/adapters/jellyfin
```

Expected: PASS.

- [ ] **Step 4: Commit Jellyfin artwork support**

```bash
git add internal/adapters/jellyfin/playback.go internal/adapters/jellyfin/playback_test.go internal/adapters/jellyfin/commands_test.go internal/adapters/jellyfin/commands.go
git commit -m "feat(jellyfin): cache music artwork for visualizers"
```

---

### Task 7: Phase 2 Cover Modes And FFmpeg Artwork Graphs

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/config/example.toml`
- Modify: `internal/ui/bridge_fields_test.go`
- Modify: `internal/core/manager.go`
- Modify: `internal/core/manager_test.go`
- Modify: `internal/ffmpeg/pipeline.go`
- Modify: `internal/ffmpeg/pipeline_test.go`

- [ ] **Step 1: Write failing cover-mode tests**

Extend config/UI/core mode tests with:

```go
VisualizerModeCoverVU,
VisualizerModeCoverSpectrum,
```

and core-to-FFmpeg mapping tests with:

```go
{VisualizerModeCoverVU, ffmpeg.VisualizerModeCoverVU},
{VisualizerModeCoverSpectrum, ffmpeg.VisualizerModeCoverSpectrum},
```

Extend `TestVisualizerRequiredFilters`:

```go
{VisualizerModeCoverVU, []string{"showvolume", "overlay", "scale"}},
{VisualizerModeCoverSpectrum, []string{"showfreqs", "overlay", "scale"}},
```

Add FFmpeg graph tests:

```go
func visualizerCommandSpec(mode VisualizerMode) PipelineSpec {
	return PipelineSpec{
		InputURL:         "http://pms/music.mp3",
		SourceProbe:      &ProbeResult{AudioRate: 44100, Duration: 180},
		OutputWidth:      720,
		OutputHeight:     480,
		OutputFpsExpr:    "60000/1001",
		AudioSampleRate:  48000,
		AudioChannels:    2,
		VideoPipePath:    "pipe:3",
		AudioPipePath:    "pipe:4",
		VideoBitrateKbps: 2500,
		Visualizer: VisualizerSpec{
			Enabled:                  true,
			Mode:                     mode,
			DrawTextAvailable:        true,
			RequiredFiltersAvailable: true,
			Metadata: VisualizerMetadata{
				Title: "Blue Monday",
			},
		},
	}
}

func TestBuildCommand_CoverModeAddsArtworkInputAfterPrimaryInput(t *testing.T) {
	spec := visualizerCommandSpec(VisualizerModeCoverVU)
	spec.Visualizer.Metadata.ArtworkPath = "cover.png"
	cmd := BuildCommand(context.Background(), spec)
	joined := strings.Join(cmd.Args, " ")
	for _, want := range []string{"-i http://pms/music.mp3 -loop 1 -i cover.png", "[1:v:0]fps=60000/1001", "-map [visualizer_video]"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("argv missing %q:\n%s", want, joined)
		}
	}
}

func TestBuildCommand_CoverModeDualAudioAddsArtworkAsInputTwo(t *testing.T) {
	spec := visualizerCommandSpec(VisualizerModeCoverSpectrum)
	spec.AudioInputURL = "http://cdn/audio.mp3"
	spec.Visualizer.Metadata.ArtworkPath = "cover.png"
	cmd := BuildCommand(context.Background(), spec)
	joined := strings.Join(cmd.Args, " ")
	for _, want := range []string{"-i http://cdn/audio.mp3 -loop 1 -i cover.png", "[2:v:0]fps=60000/1001", "-map 1:a:0"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("argv missing %q:\n%s", want, joined)
		}
	}
}

func TestBuildVisualizerFilterChain_CoverModesUsePlaceholderWithoutArtwork(t *testing.T) {
	for _, mode := range []VisualizerMode{VisualizerModeCoverVU, VisualizerModeCoverSpectrum} {
		spec := visualizerCommandSpec(mode)
		graph, err := buildVisualizerFilterChain(spec)
		if err != nil {
			t.Fatalf("%s graph: %v", mode, err)
		}
		if !strings.Contains(graph, "color=c=0x101018") {
			t.Fatalf("%s graph missing placeholder color source:\n%s", mode, graph)
		}
		if strings.Contains(graph, "-loop") {
			t.Fatalf("%s graph contains argv token in filter graph:\n%s", mode, graph)
		}
	}
}
```

Run:

```bash
go test ./internal/config ./internal/ui ./internal/core ./internal/ffmpeg
```

Expected: cover-mode validation and FFmpeg graph tests fail.

- [ ] **Step 2: Expose cover modes in config/UI and core**

Add `VisualizerModeCoverVU` and `VisualizerModeCoverSpectrum` to `supportedVisualizerModes` in `internal/config/config.go`, to the visualizer validation switch, and to the example config comment:

```toml
mode = "retro_analyzer"           # retro_analyzer, oscilloscope_wave, stereo_scope, vu_cabinet, neon_grid, raster_pulse, cover_vu, cover_spectrum
```

In `internal/core/manager.go`, add cover modes to `validateVisualizerRequest`, `coreVisualizerModeFromConfig`, and `ffmpegVisualizerMode`.

- [ ] **Step 3: Add cover-mode required filters and graph helpers**

In `RequiredVisualizerFilters`, add:

```go
case VisualizerModeCoverVU:
	return []string{"showvolume", "overlay", "scale"}
case VisualizerModeCoverSpectrum:
	return []string{"showfreqs", "overlay", "scale"}
```

Add cover helpers to `internal/ffmpeg/pipeline.go`:

```go
func coverSquareSize(logicalW, logicalH int) int {
	size := logicalH * 3 / 5
	if size > logicalW-64 {
		size = logicalW - 64
	}
	if size < 96 {
		size = 96
	}
	if size%2 != 0 {
		size--
	}
	return size
}

func coverArtBranch(label string, coverSize int, fpsExpr string) string {
	if label == "" {
		return fmt.Sprintf("color=c=0x101018:s=%dx%d:r=%s,format=bgr24[cover]", coverSize, coverSize, fpsExpr)
	}
	return fmt.Sprintf("[%s]fps=%s,format=bgr24,scale=w=%d:h=%d:force_original_aspect_ratio=increase:flags=lanczos,crop=%d:%d[cover]", label, fpsExpr, coverSize, coverSize, coverSize, coverSize)
}

func coverModeGraph(s PipelineSpec, mode VisualizerMode, logicalW, logicalH int, fpsExpr string) (string, string, error) {
	_, artLabel := visualizerArtworkInput(s)
	coverSize := coverSquareSize(logicalW, logicalH)
	coverY := 24
	if logicalH <= 288 {
		coverY = 12
	}
	parts := []string{
		fmt.Sprintf("color=c=0x05050a:s=%dx%d:r=%s[cover_bg]", logicalW, logicalH, fpsExpr),
		coverArtBranch(artLabel, coverSize, fpsExpr),
		fmt.Sprintf("[cover_bg][cover]overlay=x=(W-w)/2:y=%d[cover_stage]", coverY),
	}
	switch mode {
	case VisualizerModeCoverVU:
		meterH := logicalH / 6
		if meterH < 20 {
			meterH = 20
		}
		parts = append(parts,
			fmt.Sprintf("[%s]showvolume=w=%d:h=%d:f=0.95:b=3:t=0:v=0:o=h:s=2:p=0.18:m=p:ds=log,format=bgr24,scale=w=%d:h=%d[cover_vu]", visualizerAudioInputMap(s), logicalW-48, meterH, logicalW-48, meterH*2),
			"[cover_stage][cover_vu]overlay=x=(W-w)/2:y=H-h-16[viz0]",
		)
	case VisualizerModeCoverSpectrum:
		barH := logicalH / 4
		if barH < 48 {
			barH = 48
		}
		parts = append(parts,
			fmt.Sprintf("[%s]showfreqs=s=%dx%d:mode=bar:ascale=log:fscale=log:colors=0xfff06b|0x28f7ff,format=bgr24[cover_spectrum]", visualizerAudioInputMap(s), logicalW, barH),
			"[cover_stage][cover_spectrum]overlay=x=0:y=H-h[viz0]",
		)
	default:
		return "", "", fmt.Errorf("unsupported cover visualizer mode %q", mode)
	}
	return strings.Join(parts, ";"), "viz0", nil
}
```

In `buildVisualizerFilterChain`, branch before calling `visualizerCoreGraph`:

```go
var parts []string
label := ""
if s.Visualizer.Mode == VisualizerModeCoverVU || s.Visualizer.Mode == VisualizerModeCoverSpectrum {
	graph, out, err := coverModeGraph(s, s.Visualizer.Mode, logicalW, logicalH, fpsExpr)
	if err != nil {
		return "", err
	}
	parts = []string{graph}
	label = out
} else {
	graph, out := visualizerCoreGraph(s.Visualizer.Mode, visualizerAudioInputMap(s), logicalW, logicalH)
	if graph == "" || out == "" {
		return "", fmt.Errorf("unsupported visualizer mode %q", s.Visualizer.Mode)
	}
	parts = []string{graph}
	label = out
}
```

Run:

```bash
go test ./internal/config ./internal/ui ./internal/core ./internal/ffmpeg
```

Expected: PASS.

- [ ] **Step 4: Commit cover modes**

```bash
git add internal/config/config.go internal/config/config_test.go internal/config/example.toml internal/ui/bridge_fields_test.go internal/core/manager.go internal/core/manager_test.go internal/ffmpeg/pipeline.go internal/ffmpeg/pipeline_test.go
git commit -m "feat(visualizer): add album-art modes"
```

---

### Task 8: Real FFmpeg Integration Coverage

**Files:**
- Create: `tests/integration/visualizer_modes_test.go`
- Modify: `tests/integration/testdata_helper_test.go`

- [ ] **Step 1: Add integration helpers**

In `tests/integration/testdata_helper_test.go`, add:

```go
func ensureSampleWAV(t *testing.T, name string, durationSec int) string {
	t.Helper()
	absDir, err := filepath.Abs(testdataDir)
	if err != nil {
		t.Fatalf("abs testdata dir: %v", err)
	}
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		t.Fatalf("mkdir testdata: %v", err)
	}
	path := filepath.Join(absDir, name)
	if _, err := os.Stat(path); err == nil {
		return path
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skipf("ffmpeg not on PATH: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-y",
		"-f", "lavfi",
		"-i", "sine=frequency=440:duration="+itoa(durationSec)+":sample_rate=48000",
		"-ac", "2",
		path,
	)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("generate %s: %v", path, err)
	}
	return path
}

func ensureSamplePNG(t *testing.T, name string) string {
	t.Helper()
	absDir, err := filepath.Abs(testdataDir)
	if err != nil {
		t.Fatalf("abs testdata dir: %v", err)
	}
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		t.Fatalf("mkdir testdata: %v", err)
	}
	path := filepath.Join(absDir, name)
	if _, err := os.Stat(path); err == nil {
		return path
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skipf("ffmpeg not on PATH: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-y",
		"-f", "lavfi",
		"-i", "testsrc=size=320x320:duration=0.1",
		"-frames:v", "1",
		path,
	)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("generate %s: %v", path, err)
	}
	return path
}
```

- [ ] **Step 2: Add one smoke test per new mode**

Create `tests/integration/visualizer_modes_test.go`:

```go
//go:build integration

package integration

import (
	"context"
	"io"
	"os/exec"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg"
)

func TestVisualizerModesSpawnRealFFmpeg(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skipf("ffmpeg not on PATH: %v", err)
	}
	audio := ensureSampleWAV(t, "visualizer-audio.wav", 1)
	cover := ensureSamplePNG(t, "visualizer-cover.png")
	for _, tc := range []struct {
		name string
		mode ffmpeg.VisualizerMode
		cover bool
	}{
		{"vu cabinet", ffmpeg.VisualizerModeVUCabinet, false},
		{"neon grid", ffmpeg.VisualizerModeNeonGrid, false},
		{"raster pulse", ffmpeg.VisualizerModeRasterPulse, false},
		{"cover vu", ffmpeg.VisualizerModeCoverVU, true},
		{"cover spectrum", ffmpeg.VisualizerModeCoverSpectrum, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ffmpeg.CheckVisualizerFilters(t.Context(), "ffmpeg", tc.mode); err != nil {
				t.Skipf("ffmpeg lacks required filters for %s: %v", tc.mode, err)
			}
			spec := ffmpeg.PipelineSpec{
				InputURL:        audio,
				SourceProbe:     &ffmpeg.ProbeResult{AudioRate: 48000, Duration: time.Second},
				OutputWidth:     720,
				OutputHeight:    480,
				OutputFpsExpr:   "60000/1001",
				AudioSampleRate: 48000,
				AudioChannels:   2,
				Visualizer: ffmpeg.VisualizerSpec{
					Enabled: true,
					Mode:    tc.mode,
					Metadata: ffmpeg.VisualizerMetadata{
						Title:  "Blue Monday",
						Artist: "New Order",
						Album:  "Power Corruption & Lies",
					},
				},
			}
			if tc.cover {
				spec.Visualizer.Metadata.ArtworkPath = cover
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			p, err := ffmpeg.Spawn(ctx, spec)
			if err != nil {
				t.Fatalf("Spawn: %v", err)
			}
			defer p.Stop()

			frame := make([]byte, spec.OutputWidth*spec.OutputHeight*3)
			if _, err := io.ReadFull(p.VideoPipe(), frame); err != nil {
				t.Fatalf("read video frame: %v", err)
			}
			audioBlock := make([]byte, 4096)
			if _, err := io.ReadFull(p.AudioPipe(), audioBlock); err != nil {
				t.Fatalf("read audio block: %v", err)
			}
		})
	}
}
```

Run:

```bash
go test -tags=integration ./tests/integration -run TestVisualizerModesSpawnRealFFmpeg -v
```

Expected: PASS on hosts with required FFmpeg filters; SKIP for missing local FFmpeg/filter support.

- [ ] **Step 3: Commit integration coverage**

```bash
git add tests/integration/visualizer_modes_test.go tests/integration/testdata_helper_test.go
git commit -m "test(visualizer): add integration smoke coverage"
```

---

### Task 9: Documentation And Final Verification

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Update README**

In the existing music visualizer section, list all supported modes:

```markdown
Supported modes:

- `retro_analyzer`: classic spectrum bars.
- `oscilloscope_wave`: horizontal waveform trace.
- `stereo_scope`: stereo/vector-scope display.
- `vu_cabinet`: stereo VU meter cabinet display.
- `neon_grid`: arcade spectrum bars with a neon grid treatment.
- `raster_pulse`: mirrored reactive waveform bands.
- `cover_vu`: cached album art with VU meters.
- `cover_spectrum`: cached album art with spectrum bars.
```

Add a short note:

```markdown
Previously discussed `chiptune_equalizer` and `radial_spectrum` modes are not shipped in this wave. `chiptune_equalizer` overlaps with `retro_analyzer`; `radial_spectrum` is deferred until it can be implemented as a genuine polar/circular visualizer without a fragile FFmpeg graph.
```

Run:

```bash
go test ./internal/config ./internal/ui ./internal/core ./internal/ffmpeg ./internal/artworkcache ./internal/adapters/plex ./internal/adapters/jellyfin
```

Expected: PASS.

- [ ] **Step 2: Run full verification**

Run:

```bash
go test ./...
```

Expected: PASS.

Run:

```bash
make test-integration
```

Expected: PASS on a host with `ffmpeg` and `ffprobe` on `PATH`, or documented skips for missing local binaries/filters.

- [ ] **Step 3: Inspect final diff**

Run:

```bash
git status --short
git diff --name-only
git log --oneline -10
```

Expected: changed implementation paths match this plan. Pre-existing unrelated user edits may still be visible in `git status --short`; they must not be included in visualizer commits.

- [ ] **Step 4: Commit docs**

```bash
git add README.md
git commit -m "docs: document expanded music visualizers"
```

- [ ] **Step 5: Request code review before merge**

Use `superpowers:requesting-code-review` with:

```text
Review the music visualizer expansion implementation.
Spec: docs/superpowers/specs/2026-05-21-music-visualizer-expansion-design.md
Plan: docs/superpowers/plans/2026-05-21-music-visualizer-expansion.md
Base: commit before Task 1
Head: current HEAD
Focus: FFmpeg graph correctness, artwork path/origin/token validation, Plex/Jellyfin fail-open behavior, and integration coverage.
```

Address Critical and Important findings before merging.
