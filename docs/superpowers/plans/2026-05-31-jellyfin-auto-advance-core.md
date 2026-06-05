# Jellyfin Auto-Advance Core Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Jellyfin continuous play that advances through the client-supplied Jellyfin queue on clean EOF when `[adapters.jellyfin].auto_advance` is enabled.

**Architecture:** Implement entirely inside `internal/adapters/jellyfin/`, using the adapter's existing local queue and reporter lifecycle. `PlayNow` captures the controller-supplied tail queue, manual `NextTrack` keeps using `StartSession`, and EOF auto-advance uses `StartSessionIfIdle` with non-mutating peek/build/recheck/conditional-commit semantics to avoid double-advance and queue reordering.

**Tech Stack:** Go 1.26, BurntSushi/toml, `net/http/httptest`, `sync`, `time`, existing `core.SessionRequest.OnStop`, existing `core.Manager.StartSessionIfIdle`.

**Spec:** `docs/superpowers/specs/2026-05-31-jellyfin-auto-advance-queue-design.md`

---

## File Structure

- **Modify:** `internal/adapters/jellyfin/config.go` — add `Config.AutoAdvance`.
- **Modify:** `internal/adapters/jellyfin/adapter.go` — expose the field, add stable queue-entry IDs, add `StartSessionIfIdle` to `SessionManager`, add auto-advance test seams.
- **Modify:** `internal/adapters/jellyfin/commands.go` — capture queue tail for multi-item `PlayNow`, route manual `NextTrack` through the shared queued-start helper.
- **Modify:** `internal/adapters/jellyfin/playback.go` — wrap `OnStop`, add compare-based adapter ownership helpers.
- **Create:** `internal/adapters/jellyfin/autoadvance.go` — auto-advance constants, queue peek/commit helpers, EOF wrapper, guarded auto-start flow.
- **Modify:** `internal/adapters/jellyfin/config_test.go` — config default and TOML round-trip coverage.
- **Modify:** `internal/adapters/jellyfin/adapter_interface_test.go` — schema coverage for `auto_advance`.
- **Modify:** `internal/adapters/jellyfin/commands_test.go` — fake core support, PlayNow queue capture, manual NextTrack regression, auto-advance and race tests.
- **Modify:** `internal/adapters/jellyfin/playback_session_test.go` — wrapper preserves reporter wakeup/artwork cleanup.

Use existing Jellyfin tests as patterns; do not touch Plex auto-advance code.

---

## Task 1: Config and Adapter Schema

**Files:**
- Modify: `internal/adapters/jellyfin/config.go`
- Modify: `internal/adapters/jellyfin/adapter.go`
- Modify: `internal/adapters/jellyfin/config_test.go`
- Modify: `internal/adapters/jellyfin/adapter_interface_test.go`

- [ ] **Step 1: Write failing config tests**

In `internal/adapters/jellyfin/config_test.go`, extend `TestConfig_DefaultsWhenSectionAbsent`:

```go
	if cfg.AutoAdvance {
		t.Errorf("default AutoAdvance = true, want false")
	}
```

Extend `TestConfig_TOMLRoundTrip` by adding `auto_advance = true` to `src`:

```toml
enabled                = true
server_url             = "https://jellyfin.example.com"
device_name            = "Living Room MiSTer"
max_video_bitrate_kbps = 8000
auto_advance           = true
```

Then assert:

```go
	if !cfg.AutoAdvance {
		t.Errorf("AutoAdvance = false, want true")
	}
```

- [ ] **Step 2: Write failing schema/current-value tests**

In `internal/adapters/jellyfin/adapter_interface_test.go`, update `wantKeys` in `TestAdapter_FieldsSchema`:

```go
	wantKeys := []string{"enabled", "server_url", "device_name", "max_video_bitrate_kbps", "auto_advance"}
```

Add this test in the same file:

```go
func TestAdapter_AutoAdvanceFieldSchema(t *testing.T) {
	a := New(nil, "/tmp/data", "test-uuid", "", nil)
	fields := a.Fields()
	var found adapters.FieldDef
	for _, f := range fields {
		if f.Key == "auto_advance" {
			found = f
			break
		}
	}
	if found.Key == "" {
		t.Fatal("auto_advance field not found")
	}
	if found.Kind != adapters.KindBool {
		t.Errorf("Kind = %v, want KindBool", found.Kind)
	}
	if found.ApplyScope != adapters.ScopeHotSwap {
		t.Errorf("ApplyScope = %v, want ScopeHotSwap", found.ApplyScope)
	}
	if found.Default != false {
		t.Errorf("Default = %v, want false", found.Default)
	}
	if found.Section != "Playback" {
		t.Errorf("Section = %q, want Playback", found.Section)
	}
}

func TestAdapter_CurrentValuesIncludesAutoAdvance(t *testing.T) {
	a := New(nil, "/tmp/data", "test-uuid", "", nil)
	a.cfg = Config{AutoAdvance: true}
	got := a.CurrentValues()["auto_advance"]
	if got != true {
		t.Fatalf("CurrentValues[auto_advance] = %v, want true", got)
	}
}
```

- [ ] **Step 3: Write failing ApplyConfig scope test**

Add to `internal/adapters/jellyfin/adapter_interface_test.go` imports:

```go
	"github.com/BurntSushi/toml"
```

Add the test:

```go
func TestAdapter_ApplyConfigAutoAdvanceIsHotSwap(t *testing.T) {
	a := New(nil, "/tmp/data", "test-uuid", "", nil)
	a.cfg = Config{ServerURL: "https://jellyfin.example.com", MaxVideoBitrateKbps: 4000}
	raw := `
[adapters.jellyfin]
enabled                = false
server_url             = "https://jellyfin.example.com"
device_name            = ""
max_video_bitrate_kbps = 4000
auto_advance           = true
`
	var envelope struct {
		Adapters map[string]toml.Primitive `toml:"adapters"`
	}
	meta, err := toml.Decode(raw, &envelope)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := a.ApplyConfig(envelope.Adapters["jellyfin"], meta)
	if err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}
	if scope != adapters.ScopeHotSwap {
		t.Fatalf("scope = %v, want ScopeHotSwap", scope)
	}
	if got := a.CurrentValues()["auto_advance"]; got != true {
		t.Fatalf("CurrentValues[auto_advance] = %v, want true", got)
	}
}
```

- [ ] **Step 4: Run tests and verify failure**

Run:

```sh
go test ./internal/adapters/jellyfin -run 'TestConfig_|TestAdapter_(FieldsSchema|AutoAdvanceFieldSchema|CurrentValuesIncludesAutoAdvance|ApplyConfigAutoAdvanceIsHotSwap)' -v
```

Expected: build fails because `Config.AutoAdvance` is undefined and the schema lacks `auto_advance`.

- [ ] **Step 5: Implement config and schema**

In `internal/adapters/jellyfin/config.go`, add the field:

```go
type Config struct {
	Enabled             bool   `toml:"enabled"`
	ServerURL           string `toml:"server_url"`
	DeviceName          string `toml:"device_name"`
	MaxVideoBitrateKbps int    `toml:"max_video_bitrate_kbps"`
	AutoAdvance         bool   `toml:"auto_advance"`
}
```

Add the explicit default:

```go
func DefaultConfig() Config {
	return Config{
		Enabled:             false,
		ServerURL:           "",
		DeviceName:          "",
		MaxVideoBitrateKbps: 4000,
		AutoAdvance:         false,
	}
}
```

In `internal/adapters/jellyfin/adapter.go`, append this field after `max_video_bitrate_kbps`:

```go
		{
			Key:        "auto_advance",
			Label:      "Continuous Play",
			Help:       "When an item ends, automatically play the next item in the Jellyfin queue.",
			Kind:       adapters.KindBool,
			Default:    false,
			ApplyScope: adapters.ScopeHotSwap,
			Section:    "Playback",
		},
```

Add it to `CurrentValues()`:

```go
		"auto_advance":           a.cfg.AutoAdvance,
```

`ApplyConfig` already starts at `ScopeHotSwap`; no extra scope escalation is needed for this bool.

- [ ] **Step 6: Run tests and verify pass**

Run:

```sh
go test ./internal/adapters/jellyfin -run 'TestConfig_|TestAdapter_(FieldsSchema|AutoAdvanceFieldSchema|CurrentValuesIncludesAutoAdvance|ApplyConfigAutoAdvanceIsHotSwap)' -v
```

Expected: all selected tests pass.

- [ ] **Step 7: Commit**

```sh
git add internal/adapters/jellyfin/config.go internal/adapters/jellyfin/adapter.go internal/adapters/jellyfin/config_test.go internal/adapters/jellyfin/adapter_interface_test.go
git commit -m "feat(jellyfin): add auto advance config"
```

---

## Task 2: PlayNow Queue Capture

**Files:**
- Modify: `internal/adapters/jellyfin/adapter.go`
- Modify: `internal/adapters/jellyfin/commands.go`
- Modify: `internal/adapters/jellyfin/commands_test.go`

- [ ] **Step 1: Write failing PlayNow queue capture tests**

Add to `internal/adapters/jellyfin/commands_test.go`:

```go
func TestHandlePlay_PlayNowCapturesTailQueueFromStartIndex(t *testing.T) {
	jfSrv := startTestPlaybackInfoServer(t)
	mgr := &fakeManager{}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}

	a.HandlePlay(mustMarshal(t, map[string]any{
		"ItemIds":       []string{"itm-0", "itm-1", "itm-2", "itm-3"},
		"StartIndex":    1,
		"PlayCommand":   "PlayNow",
		"MediaSourceId": "src-selected",
	}))

	waitForFakeManagerReq(t, mgr)
	req := mgr.lastReq()
	if req.AdapterRef != "itm-1:ps-1" {
		t.Fatalf("started AdapterRef = %q, want itm-1:ps-1", req.AdapterRef)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.queue) != 2 {
		t.Fatalf("queue len = %d, want 2", len(a.queue))
	}
	if a.queue[0].ItemID != "itm-2" || a.queue[1].ItemID != "itm-3" {
		t.Fatalf("queue order = %+v, want itm-2,itm-3", a.queue)
	}
	if a.queue[0].QueueEntryID == 0 || a.queue[1].QueueEntryID == 0 || a.queue[0].QueueEntryID == a.queue[1].QueueEntryID {
		t.Fatalf("QueueEntryID values = %d,%d, want distinct non-zero IDs", a.queue[0].QueueEntryID, a.queue[1].QueueEntryID)
	}
	if a.queue[0].MediaSourceID != "src-selected" || a.queue[1].MediaSourceID != "src-selected" {
		t.Fatalf("MediaSourceID not preserved in queue: %+v", a.queue)
	}
}

func TestHandlePlay_PlayNowInvalidStartIndexUsesZero(t *testing.T) {
	jfSrv := startTestPlaybackInfoServer(t)
	mgr := &fakeManager{}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}

	a.HandlePlay(mustMarshal(t, map[string]any{
		"ItemIds":     []string{"itm-1", "itm-2"},
		"StartIndex":   99,
		"PlayCommand":  "PlayNow",
	}))

	waitForFakeManagerReq(t, mgr)
	if got := mgr.lastReq().AdapterRef; got != "itm-1:ps-1" {
		t.Fatalf("started AdapterRef = %q, want itm-1:ps-1", got)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.queue) != 1 || a.queue[0].ItemID != "itm-2" {
		t.Fatalf("queue = %+v, want [itm-2]", a.queue)
	}
}

func TestHandlePlay_PlayNextMultipleItemsPreservesOrderAheadOfTail(t *testing.T) {
	a := New(&fakeManager{}, t.TempDir(), "dev-1", "", nil)
	a.queue = []QueuedItem{{QueueEntryID: 99, ItemID: "tail-itm"}}
	a.nextQueueEntryID = 99

	a.HandlePlay(mustMarshal(t, map[string]any{
		"ItemIds":       []string{"head-1", "head-2"},
		"PlayCommand":   "PlayNext",
		"MediaSourceId": "src-next",
	}))

	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.queue) != 3 {
		t.Fatalf("queue len = %d, want 3", len(a.queue))
	}
	if a.queue[0].ItemID != "head-1" || a.queue[1].ItemID != "head-2" || a.queue[2].ItemID != "tail-itm" {
		t.Fatalf("queue order = %+v, want [head-1 head-2 tail-itm]", a.queue)
	}
	if a.queue[0].QueueEntryID != 100 || a.queue[1].QueueEntryID != 101 || a.queue[2].QueueEntryID != 99 {
		t.Fatalf("QueueEntryIDs = %d,%d,%d, want 100,101,99", a.queue[0].QueueEntryID, a.queue[1].QueueEntryID, a.queue[2].QueueEntryID)
	}
	if a.queue[0].MediaSourceID != "src-next" || a.queue[1].MediaSourceID != "src-next" {
		t.Fatalf("MediaSourceID not preserved for inserted items: %+v", a.queue)
	}
}
```

- [ ] **Step 2: Run tests and verify failure**

Run:

```sh
go test ./internal/adapters/jellyfin -run 'TestHandlePlay_Play(NowCapturesTailQueueFromStartIndex|NowInvalidStartIndexUsesZero|NextMultipleItemsPreservesOrderAheadOfTail)' -v
```

Expected: tests fail because `QueuedItem` has no `QueueEntryID`, `startPlayNow` starts `ItemIDs[0]`, and `PlayNow` does not replace the queue with the tail.

- [ ] **Step 3: Add stable queue-entry IDs**

In `internal/adapters/jellyfin/adapter.go`, update `Adapter` near `queue`:

```go
	nextQueueEntryID uint64       // monotonically assigned under mu for stable queued-item identity
	queue            []QueuedItem // adapter-local FIFO for PlayNext / PlayLast
```

Update `QueuedItem`:

```go
type QueuedItem struct {
	QueueEntryID        uint64
	ItemID              string
	StartPositionTicks  int64
	MediaSourceID       string
	AudioStreamIndex    *int // pointer because 0 is meaningful
	SubtitleStreamIndex *int
}
```

In `internal/adapters/jellyfin/commands.go`, add near `queueAt`:

```go
func (a *Adapter) nextQueueEntryIDLocked() uint64 {
	a.nextQueueEntryID++
	return a.nextQueueEntryID
}
```

- [ ] **Step 4: Add PlayNow queue helpers**

In `internal/adapters/jellyfin/commands.go`, add these helpers near `queueAt`:

```go
func playNowStartIndex(p playMessageData) int {
	if p.StartIndex >= 0 && p.StartIndex < len(p.ItemIDs) {
		return p.StartIndex
	}
	return 0
}

func (a *Adapter) queuedItemsFromPlayMessageLocked(p playMessageData, ids []string) []QueuedItem {
	items := make([]QueuedItem, 0, len(ids))
	for _, id := range ids {
		items = append(items, QueuedItem{
			QueueEntryID:        a.nextQueueEntryIDLocked(),
			ItemID:              id,
			StartPositionTicks:  0,
			MediaSourceID:       p.MediaSourceID,
			AudioStreamIndex:    p.AudioStreamIndex,
			SubtitleStreamIndex: p.SubtitleStreamIndex,
		})
	}
	return items
}

func (a *Adapter) replaceQueueForPlayNow(p playMessageData) playMessageData {
	idx := playNowStartIndex(p)
	selected := p
	selected.ItemIDs = []string{p.ItemIDs[idx]}
	a.mu.Lock()
	tail := a.queuedItemsFromPlayMessageLocked(p, p.ItemIDs[idx+1:])
	a.queue = tail
	a.mu.Unlock()
	return selected
}
```

- [ ] **Step 5: Use the helpers in HandlePlay**

In `HandlePlay`, replace the `PlayNow` cases:

```go
	case "", "PlayNow":
		a.startPlayNow(a.replaceQueueForPlayNow(p))
```

And:

```go
	case "PlayInstantMix", "PlayShuffle":
		slog.Warn("jellyfin: PlayCommand simplified to PlayNow", "requested", p.PlayCommand)
		a.startPlayNow(a.replaceQueueForPlayNow(p))
```

Update `queueAt` to reuse the common item builder:

```go
func (a *Adapter) queueAt(p playMessageData, pos int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	items := a.queuedItemsFromPlayMessageLocked(p, p.ItemIDs)
	if pos < 0 {
		a.queue = append(a.queue, items...)
	} else {
		a.queue = append(items, a.queue...)
	}
}
```

Update the `startPlayNow` comment:

```go
// startPlayNow runs the PlaybackInfo -> StartSession sequence for ItemIds[0].
// Callers that receive a multi-item PlayNow must collapse the message to the
// selected item first via replaceQueueForPlayNow.
```

- [ ] **Step 6: Run queue tests**

Run:

```sh
go test ./internal/adapters/jellyfin -run 'TestHandlePlay_Play(NowCapturesTailQueueFromStartIndex|NowInvalidStartIndexUsesZero|NextMultipleItemsPreservesOrderAheadOfTail|Last_AppendsToQueue|Next_InsertsAtFront)' -v
```

Expected: all selected tests pass.

- [ ] **Step 7: Commit**

```sh
git add internal/adapters/jellyfin/adapter.go internal/adapters/jellyfin/commands.go internal/adapters/jellyfin/commands_test.go
git commit -m "feat(jellyfin): capture playnow queue tail"
```

---

## Task 3: SessionManager Interface and Test Fake Support

**Files:**
- Modify: `internal/adapters/jellyfin/adapter.go`
- Modify: `internal/adapters/jellyfin/commands_test.go`

- [ ] **Step 1: Write the failing compile check**

No new test body is required. The compile failure will appear after adding `StartSessionIfIdle` to `SessionManager` because `fakeManager` does not implement it yet.

- [ ] **Step 2: Add `StartSessionIfIdle` to the interface**

In `internal/adapters/jellyfin/adapter.go`, update `SessionManager`:

```go
type SessionManager interface {
	StartSession(req core.SessionRequest) error
	StartSessionIfIdle(req core.SessionRequest) (bool, error)
	Pause() error
	Play() error
	Stop() error
	SeekTo(offsetMs int) error
	Status() core.SessionStatus
	VisualizerMode() string
}
```

- [ ] **Step 3: Run tests and verify failure**

Run:

```sh
go test ./internal/adapters/jellyfin -run TestHandlePlay_PlayNow_CallsStartSession -v
```

Expected: build fails because `*fakeManager` lacks `StartSessionIfIdle`.

- [ ] **Step 4: Extend `fakeManager`**

In `internal/adapters/jellyfin/commands_test.go`, update `fakeManager`:

```go
type fakeManager struct {
	mu               sync.Mutex
	reqs             []core.SessionRequest
	idleReqs         []core.SessionRequest
	calls            []string
	st               core.SessionStatus
	err              error
	startIdleStarted bool
	startIdleErr     error
	onStartIdle      func()
	mode             string
	onStop           func()
}
```

Add the method:

```go
func (f *fakeManager) StartSessionIfIdle(req core.SessionRequest) (bool, error) {
	if f.onStartIdle != nil {
		f.onStartIdle()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.idleReqs = append(f.idleReqs, req)
	f.calls = append(f.calls, "StartSessionIfIdle:"+req.StreamURL)
	err := f.startIdleErr
	if err == nil {
		err = f.err
	}
	if err != nil {
		return false, err
	}
	if f.st.State != core.StateIdle || !f.startIdleStarted {
		return false, nil
	}
	f.st = core.SessionStatus{State: core.StatePlaying, AdapterRef: req.AdapterRef}
	return true, nil
}
```

Add helpers:

```go
func (f *fakeManager) lastIdleReq() core.SessionRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.idleReqs) == 0 {
		return core.SessionRequest{}
	}
	return f.idleReqs[len(f.idleReqs)-1]
}
```

- [ ] **Step 5: Run package tests**

Run:

```sh
go test ./internal/adapters/jellyfin -run 'TestHandlePlay_PlayNow_CallsStartSession|TestHandlePlaystate_NextTrack_PopsAndStarts' -v
```

Expected: selected tests pass.

- [ ] **Step 6: Commit**

```sh
git add internal/adapters/jellyfin/adapter.go internal/adapters/jellyfin/commands_test.go
git commit -m "test(jellyfin): add guarded session fake"
```

---

## Task 4: Queued-Item Start Helper for Manual NextTrack

**Files:**
- Modify: `internal/adapters/jellyfin/commands.go`
- Modify: `internal/adapters/jellyfin/commands_test.go`

- [ ] **Step 1: Strengthen the manual NextTrack regression test**

In `TestHandlePlaystate_NextTrack_PopsAndStarts`, after confirming `len(mgr.reqs) > 0`, add:

```go
	if len(mgr.idleReqs) != 0 {
		t.Fatalf("manual NextTrack used StartSessionIfIdle: %d calls", len(mgr.idleReqs))
	}
```

- [ ] **Step 2: Run test before refactor**

Run:

```sh
go test ./internal/adapters/jellyfin -run TestHandlePlaystate_NextTrack_PopsAndStarts -v
```

Expected: pass before refactor; this pins manual `NextTrack` to `StartSession`.

- [ ] **Step 3: Add start strategy types**

In `internal/adapters/jellyfin/commands.go`, near `startQueuedItem`, add:

```go
type queuedStartStrategy func(core.SessionRequest) (bool, error)

func startCoreSession(coreManager SessionManager) queuedStartStrategy {
	return func(req core.SessionRequest) (bool, error) {
		if coreManager == nil {
			return false, fmt.Errorf("core playback manager is not configured")
		}
		return true, coreManager.StartSession(req)
	}
}

type queuedStartOptions struct {
	Starter queuedStartStrategy
}
```

- [ ] **Step 4: Extract `startQueuedItemWithOptions`**

Replace `startQueuedItem` with:

```go
func (a *Adapter) startQueuedItem(qi QueuedItem) {
	a.startQueuedItemWithOptions(qi, queuedStartOptions{
		Starter: startCoreSession(a.core),
	})
}

func (a *Adapter) startQueuedItemWithOptions(qi QueuedItem, opts queuedStartOptions) {
	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()
	tok, err := LoadToken(a.tokenPath())
	if err != nil || tok.AccessToken == "" {
		slog.Error("jellyfin: start queued item: no token", "err", err)
		return
	}
	preset, err := a.currentPreset()
	if err != nil {
		slog.Error("jellyfin: start queued item: modeline", "err", err)
		return
	}
	if opts.Starter == nil {
		slog.Error("jellyfin: start queued item: no core starter")
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		meta := a.fetchItemMetadataBestEffort(ctx, cfg, tok, qi.ItemID)
		info, err := FetchPlaybackInfo(ctx, PlaybackInfoInput{
			ServerURL:           cfg.ServerURL,
			Token:               tok.AccessToken,
			DeviceID:            a.deviceID,
			DeviceName:          cfg.DeviceName,
			Version:             linkVersion,
			ItemID:              qi.ItemID,
			UserID:              tok.UserID,
			MaxVideoBitrateKbps: cfg.MaxVideoBitrateKbps,
			Preset:              preset,
			StartPositionTicks:  qi.StartPositionTicks,
			MediaSourceID:       qi.MediaSourceID,
			AudioStreamIndex:    qi.AudioStreamIndex,
			SubtitleStreamIndex: qi.SubtitleStreamIndex,
			MediaKind:           meta.MediaKind,
		})
		if err != nil {
			artworkcache.Remove(meta.ArtworkPath)
			slog.Error("jellyfin: queued PlaybackInfo failed", "err", err)
			return
		}
		info = mergePlaybackMetadata(info, meta)
		req := a.buildSessionRequest(playRequestInput{
			ItemID:             qi.ItemID,
			StartPositionTicks: qi.StartPositionTicks,
			PlayInfo:           info,
			ServerURL:          cfg.ServerURL,
			Token:              tok.AccessToken,
		})

		prev := a.beginSelfPreempt(req.AdapterRef)
		a.emitEvent(eventlog.SeverityInfo, fmt.Sprintf("cast-requested %s", req.AdapterRef))
		started, err := opts.Starter(req)
		if err != nil {
			a.rollbackSelfPreempt(prev)
			cleanupSessionArtwork(req)
			slog.Error("jellyfin: queued StartSession failed", "err", err)
			return
		}
		if !started {
			a.rollbackSelfPreempt(prev)
			cleanupSessionArtwork(req)
			return
		}
		a.commitSelfPreempt()
		a.spawnReporter(reporterParams{
			ItemID:          qi.ItemID,
			PlaySessionID:   info.PlaySessionID,
			MediaSourceID:   info.MediaSourceID,
			AudioIdx:        qi.AudioStreamIndex,
			SubtitleIdx:     qi.SubtitleStreamIndex,
			NowPlayingQueue: a.snapshotNowPlayingQueue(qi.ItemID),
			Auth: RESTAuth{
				ServerURL: cfg.ServerURL, Token: tok.AccessToken,
				DeviceID: a.deviceID, DeviceName: cfg.DeviceName,
				Version: linkVersion,
			},
		})
	}()
}
```

This helper intentionally matches the current manual `NextTrack` ownership behavior, including pre-reserving adapter ownership before `StartSession`. Auto-advance will add a separate guarded path in Task 6 and will not call this helper because EOF auto-advance must not mutate queue or ownership state before `StartSessionIfIdle` succeeds. The duplicated request-build sequence in Task 6 is intentional; only extract a smaller pure request-builder later if it preserves that no-pre-mutation invariant.

- [ ] **Step 5: Run manual queue tests**

Run:

```sh
go test ./internal/adapters/jellyfin -run 'TestHandlePlaystate_NextTrack_PopsAndStarts|TestHandlePlay_Play(Last_AppendsToQueue|Next_InsertsAtFront)' -v
```

Expected: selected tests pass.

- [ ] **Step 6: Commit**

```sh
git add internal/adapters/jellyfin/commands.go internal/adapters/jellyfin/commands_test.go
git commit -m "refactor(jellyfin): share queued item start path"
```

---

## Task 5: Auto-Advance Wrapper and Basic Gating

**Files:**
- Create: `internal/adapters/jellyfin/autoadvance.go`
- Modify: `internal/adapters/jellyfin/adapter.go`
- Modify: `internal/adapters/jellyfin/playback.go`
- Modify: `internal/adapters/jellyfin/playback_session_test.go`
- Modify: `internal/adapters/jellyfin/commands_test.go`

- [ ] **Step 1: Add test seams to Adapter**

In `internal/adapters/jellyfin/adapter.go`, add fields to `Adapter` near `keepaliveSet`:

```go
	autoAdvanceDelay time.Duration
	// beforeAutoAdvanceCommit is a test seam for deterministic interleaving
	// between a successful guarded core start and adapter queue/ownership commit.
	beforeAutoAdvanceCommit func()
```

In `New`, initialize:

```go
		autoAdvanceDelay: autoAdvanceSettleDelay,
```

This will fail to compile until `autoAdvanceSettleDelay` exists.

- [ ] **Step 2: Write failing wrapper tests**

In `internal/adapters/jellyfin/playback_session_test.go`, add imports:

```go
	"os"
	"path/filepath"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
```

Add tests:

```go
func TestBuildSessionRequest_OnStopEOFStillCleansArtwork(t *testing.T) {
	dir := t.TempDir()
	art := filepath.Join(dir, "cover.png")
	if err := os.WriteFile(art, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := New(&fakeManager{}, t.TempDir(), "device-1", "", nil)
	a.autoAdvanceDelay = 0
	a.cfg = Config{AutoAdvance: false}
	req := a.buildSessionRequest(playRequestInput{
		ItemID: "itm-1",
		PlayInfo: PlaybackInfoResult{
			MediaSourceID:  "src-1",
			PlaySessionID:  "ps-7",
			TranscodingURL: "/videos/itm-1/master.m3u8?MediaSourceId=src-1",
			ArtworkPath:    art,
		},
		ServerURL: "https://jf.example.com",
		Token:     "tok",
	})
	req.OnStop("eof")
	if _, err := os.Stat(art); !os.IsNotExist(err) {
		t.Fatalf("artwork stat err = %v, want missing", err)
	}
}

func TestBuildSessionRequest_OnStopEOFWithToggleOffDoesNotAdvance(t *testing.T) {
	mgr := &fakeManager{st: core.SessionStatus{State: core.StateIdle}}
	a := New(mgr, t.TempDir(), "device-1", "", nil)
	a.autoAdvanceDelay = 0
	a.cfg = Config{AutoAdvance: false}
	a.currentRefKey = "itm-1:ps-7"
	a.queue = []QueuedItem{{QueueEntryID: 1, ItemID: "next-itm"}}
	req := a.buildSessionRequest(playRequestInput{
		ItemID: "itm-1",
		PlayInfo: PlaybackInfoResult{
			MediaSourceID:  "src-1",
			PlaySessionID:  "ps-7",
			TranscodingURL: "/videos/itm-1/master.m3u8?MediaSourceId=src-1",
		},
		ServerURL: "https://jf.example.com",
		Token:     "tok",
	})
	req.OnStop("eof")
	time.Sleep(100 * time.Millisecond)
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if len(mgr.idleReqs) != 0 {
		t.Fatalf("StartSessionIfIdle calls = %d, want 0", len(mgr.idleReqs))
	}
}

func TestBuildSessionRequest_OnStopNonEOFDoesNotAdvance(t *testing.T) {
	mgr := &fakeManager{st: core.SessionStatus{State: core.StateIdle}, startIdleStarted: true}
	a := New(mgr, t.TempDir(), "device-1", "", nil)
	a.autoAdvanceDelay = 0
	a.cfg = Config{AutoAdvance: true}
	a.currentRefKey = "itm-1:ps-7"
	a.queue = []QueuedItem{{QueueEntryID: 1, ItemID: "next-itm"}}
	req := a.buildSessionRequest(playRequestInput{
		ItemID: "itm-1",
		PlayInfo: PlaybackInfoResult{
			MediaSourceID:  "src-1",
			PlaySessionID:  "ps-7",
			TranscodingURL: "/videos/itm-1/master.m3u8?MediaSourceId=src-1",
		},
		ServerURL: "https://jf.example.com",
		Token:     "tok",
	})
	req.OnStop("stopped")
	time.Sleep(100 * time.Millisecond)
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if len(mgr.idleReqs) != 0 {
		t.Fatalf("StartSessionIfIdle calls = %d, want 0", len(mgr.idleReqs))
	}
}

func TestBuildSessionRequest_OnStopWrapperPreservesReporterWakeup(t *testing.T) {
	a := New(&fakeManager{}, t.TempDir(), "device-1", "", nil)
	a.autoAdvanceDelay = 0
	a.cfg = Config{AutoAdvance: false}
	req := a.buildSessionRequest(playRequestInput{
		ItemID: "itm-1",
		PlayInfo: PlaybackInfoResult{
			MediaSourceID:  "src-1",
			PlaySessionID:  "ps-7",
			TranscodingURL: "/videos/itm-1/master.m3u8?MediaSourceId=src-1",
		},
		ServerURL: "https://jf.example.com",
		Token:     "tok",
	})
	wakeup := make(chan struct{}, 1)
	a.mu.Lock()
	a.reporters["itm-1:ps-7"] = &reporter{capturedRefKey: "itm-1:ps-7", wakeup: wakeup}
	a.mu.Unlock()

	req.OnStop("error")

	select {
	case <-wakeup:
	case <-time.After(time.Second):
		t.Fatal("reporter wakeup not received")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if got := a.reporters["itm-1:ps-7"].errReason; got != "error" {
		t.Fatalf("errReason = %q, want error", got)
	}
}
```

- [ ] **Step 3: Run tests and verify failure**

Run:

```sh
go test ./internal/adapters/jellyfin -run 'TestBuildSessionRequest_OnStop(EOFStillCleansArtwork|EOFWithToggleOffDoesNotAdvance|NonEOFDoesNotAdvance|WrapperPreservesReporterWakeup)' -v
```

Expected: build fails because `autoAdvanceSettleDelay` / `withAutoAdvance` do not exist.

- [ ] **Step 4: Create `autoadvance.go` with basic helpers**

Create `internal/adapters/jellyfin/autoadvance.go`:

```go
package jellyfin

import (
	"log/slog"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

const (
	autoAdvanceStopReason  = "eof"
	autoAdvanceSettleDelay = 1 * time.Second
)

func (a *Adapter) autoAdvanceEnabled() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg.AutoAdvance
}

func (a *Adapter) withAutoAdvance(refKey string, base func(string)) func(string) {
	return func(reason string) {
		if base != nil {
			base(reason)
		}
		if reason != autoAdvanceStopReason {
			return
		}
		go a.advanceAfterEOF(refKey)
	}
}

func (a *Adapter) advanceAfterEOF(refKey string) {
	delay := a.autoAdvanceDelay
	if delay > 0 {
		time.Sleep(delay)
	}
	if !a.autoAdvanceEnabled() {
		return
	}
	if a.core == nil {
		return
	}
	if st := a.core.Status(); st.State != core.StateIdle {
		return
	}
	if a.snapshotCurrentRefKey() != refKey {
		return
	}
	if _, ok := a.peekQueueHead(); !ok {
		slog.Debug("jellyfin: auto-advance reached end of queue")
		return
	}
}

func (a *Adapter) peekQueueHead() (QueuedItem, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.queue) == 0 {
		return QueuedItem{}, false
	}
	return a.queue[0], true
}
```

- [ ] **Step 5: Wrap OnStop in `buildSessionRequest`**

In `internal/adapters/jellyfin/playback.go`, replace:

```go
		OnStop:          artworkcache.WithCleanup(in.PlayInfo.ArtworkPath, a.makeOnStop(refKey)),
```

with:

```go
		OnStop:          a.withAutoAdvance(refKey, artworkcache.WithCleanup(in.PlayInfo.ArtworkPath, a.makeOnStop(refKey))),
```

- [ ] **Step 6: Run wrapper tests**

Run:

```sh
go test ./internal/adapters/jellyfin -run 'TestBuildSessionRequest_OnStop(EOFStillCleansArtwork|EOFWithToggleOffDoesNotAdvance|NonEOFDoesNotAdvance|WrapperPreservesReporterWakeup)' -v
```

Expected: selected tests pass.

- [ ] **Step 7: Commit**

```sh
git add internal/adapters/jellyfin/adapter.go internal/adapters/jellyfin/autoadvance.go internal/adapters/jellyfin/playback.go internal/adapters/jellyfin/playback_session_test.go
git commit -m "feat(jellyfin): wrap eof for auto advance"
```

---

## Task 6: Guarded Auto-Advance Success and Failure Semantics

**Files:**
- Modify: `internal/adapters/jellyfin/autoadvance.go`
- Modify: `internal/adapters/jellyfin/commands_test.go`

- [ ] **Step 1: Add a flexible playback-info test server**

In `internal/adapters/jellyfin/commands_test.go`, add:

```go
func startMappedPlaybackInfoServer(t *testing.T, failPlaybackInfoFor map[string]bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/Items/", func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) >= 2 && parts[0] == "Items" && parts[len(parts)-1] == "PlaybackInfo" {
			itemID := parts[1]
			if failPlaybackInfoFor[itemID] {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"MediaSources":[{"Id":"src-` + itemID + `","TranscodingUrl":"/videos/` + itemID + `/master.m3u8?MediaSourceId=src-` + itemID + `"}],
				"PlaySessionId":"ps-` + itemID + `",
				"Item":{"Name":"` + itemID + `"}
			}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Id":"metadata","Name":"metadata"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}
```

- [ ] **Step 2: Write failing success test**

Add:

```go
func TestAutoAdvanceEOF_StartsNextQueuedItemIfIdle(t *testing.T) {
	jfSrv := startMappedPlaybackInfoServer(t, nil)
	mgr := &fakeManager{
		st:               core.SessionStatus{State: core.StateIdle},
		startIdleStarted: true,
	}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.autoAdvanceDelay = 0
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true, AutoAdvance: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}
	a.currentRefKey = "itm-1:ps-itm-1"
	a.queue = []QueuedItem{{QueueEntryID: 1, ItemID: "next-itm"}, {QueueEntryID: 2, ItemID: "tail-itm"}}

	a.advanceAfterEOF("itm-1:ps-itm-1")

	if got := mgr.lastIdleReq().AdapterRef; got != "next-itm:ps-next-itm" {
		t.Fatalf("idle AdapterRef = %q, want next-itm:ps-next-itm", got)
	}
	if got := a.snapshotCurrentRefKey(); got != "next-itm:ps-next-itm" {
		t.Fatalf("currentRefKey = %q, want next-itm:ps-next-itm", got)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.queue) != 1 || a.queue[0].ItemID != "tail-itm" {
		t.Fatalf("queue after auto advance = %+v, want [tail-itm]", a.queue)
	}
	if _, ok := a.reporters["next-itm:ps-next-itm"]; !ok {
		t.Fatalf("reporter for next item not registered")
	}
}
```

- [ ] **Step 3: Write failing guard/failure tests**

Add:

```go
func TestAutoAdvanceEOF_GuardMissLeavesQueueAndRefUnchanged(t *testing.T) {
	jfSrv := startMappedPlaybackInfoServer(t, nil)
	mgr := &fakeManager{st: core.SessionStatus{State: core.StateIdle}, startIdleStarted: false}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.autoAdvanceDelay = 0
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true, AutoAdvance: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}
	a.currentRefKey = "itm-1:ps-itm-1"
	a.queue = []QueuedItem{{QueueEntryID: 1, ItemID: "next-itm"}}

	a.advanceAfterEOF("itm-1:ps-itm-1")

	if got := a.snapshotCurrentRefKey(); got != "itm-1:ps-itm-1" {
		t.Fatalf("currentRefKey = %q, want old ref", got)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.queue) != 1 || a.queue[0].ItemID != "next-itm" {
		t.Fatalf("queue = %+v, want unchanged [next-itm]", a.queue)
	}
	if len(a.reporters) != 0 {
		t.Fatalf("reporters = %d, want 0", len(a.reporters))
	}
}

func TestAutoAdvanceEOF_PlaybackInfoFailureLeavesQueueAndRefUnchanged(t *testing.T) {
	jfSrv := startMappedPlaybackInfoServer(t, map[string]bool{"bad-itm": true})
	mgr := &fakeManager{st: core.SessionStatus{State: core.StateIdle}, startIdleStarted: true}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.autoAdvanceDelay = 0
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true, AutoAdvance: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}
	a.currentRefKey = "itm-1:ps-itm-1"
	a.queue = []QueuedItem{{QueueEntryID: 1, ItemID: "bad-itm"}, {QueueEntryID: 2, ItemID: "tail-itm"}}

	a.advanceAfterEOF("itm-1:ps-itm-1")

	mgr.mu.Lock()
	idleCalls := len(mgr.idleReqs)
	mgr.mu.Unlock()
	if idleCalls != 0 {
		t.Fatalf("StartSessionIfIdle calls = %d, want 0", idleCalls)
	}
	if got := a.snapshotCurrentRefKey(); got != "itm-1:ps-itm-1" {
		t.Fatalf("currentRefKey = %q, want old ref", got)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.queue) != 2 || a.queue[0].ItemID != "bad-itm" || a.queue[1].ItemID != "tail-itm" {
		t.Fatalf("queue = %+v, want unchanged [bad-itm tail-itm]", a.queue)
	}
	if len(a.reporters) != 0 {
		t.Fatalf("reporters = %d, want 0", len(a.reporters))
	}
}

func TestAutoAdvanceEOF_StartSessionIfIdleErrorLeavesQueueAndRefUnchanged(t *testing.T) {
	jfSrv := startMappedPlaybackInfoServer(t, nil)
	mgr := &fakeManager{
		st:               core.SessionStatus{State: core.StateIdle},
		startIdleStarted: true,
		startIdleErr:     errors.New("probe failed"),
	}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.autoAdvanceDelay = 0
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true, AutoAdvance: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}
	a.currentRefKey = "itm-1:ps-itm-1"
	a.queue = []QueuedItem{{QueueEntryID: 1, ItemID: "next-itm"}, {QueueEntryID: 2, ItemID: "tail-itm"}}

	a.advanceAfterEOF("itm-1:ps-itm-1")

	if got := a.snapshotCurrentRefKey(); got != "itm-1:ps-itm-1" {
		t.Fatalf("currentRefKey = %q, want old ref", got)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.queue) != 2 || a.queue[0].ItemID != "next-itm" || a.queue[1].ItemID != "tail-itm" {
		t.Fatalf("queue = %+v, want unchanged [next-itm tail-itm]", a.queue)
	}
	if len(a.reporters) != 0 {
		t.Fatalf("reporters = %d, want 0", len(a.reporters))
	}
}

func TestAutoAdvanceEOF_ControllerStartsDuringIdleGuardStandsDown(t *testing.T) {
	jfSrv := startMappedPlaybackInfoServer(t, nil)
	mgr := &fakeManager{st: core.SessionStatus{State: core.StateIdle}, startIdleStarted: true}
	mgr.onStartIdle = func() {
		mgr.mu.Lock()
		mgr.st = core.SessionStatus{State: core.StatePlaying, AdapterRef: "controller-itm:ps-controller"}
		mgr.mu.Unlock()
	}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.autoAdvanceDelay = 0
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true, AutoAdvance: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}
	a.currentRefKey = "itm-1:ps-itm-1"
	a.queue = []QueuedItem{{QueueEntryID: 1, ItemID: "next-itm"}}

	a.advanceAfterEOF("itm-1:ps-itm-1")

	if got := a.snapshotCurrentRefKey(); got != "itm-1:ps-itm-1" {
		t.Fatalf("currentRefKey = %q, want old ref", got)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.queue) != 1 || a.queue[0].ItemID != "next-itm" {
		t.Fatalf("queue = %+v, want unchanged [next-itm]", a.queue)
	}
	if len(a.reporters) != 0 {
		t.Fatalf("reporters = %d, want 0", len(a.reporters))
	}
}
```

- [ ] **Step 4: Run tests and verify failure**

Run:

```sh
go test ./internal/adapters/jellyfin -run 'TestAutoAdvanceEOF_(StartsNextQueuedItemIfIdle|GuardMissLeavesQueueAndRefUnchanged|PlaybackInfoFailureLeavesQueueAndRefUnchanged|StartSessionIfIdleErrorLeavesQueueAndRefUnchanged|ControllerStartsDuringIdleGuardStandsDown)' -v
```

Expected: tests fail because `advanceAfterEOF` only peeks and does not build/start/commit.

- [ ] **Step 5: Implement queue identity helpers**

In `internal/adapters/jellyfin/autoadvance.go`, add:

```go
func sameQueueEntry(a, b QueuedItem) bool {
	return a.QueueEntryID != 0 && a.QueueEntryID == b.QueueEntryID
}

func (a *Adapter) queueHeadStillMatches(qi QueuedItem) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.queue) > 0 && sameQueueEntry(a.queue[0], qi)
}

func (a *Adapter) commitAutoAdvance(stoppedRef string, nextRef string, started QueuedItem) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.currentRefKey != stoppedRef {
		return false
	}
	removeIndex := -1
	for i, qi := range a.queue {
		if sameQueueEntry(qi, started) {
			removeIndex = i
			break
		}
	}
	if removeIndex < 0 {
		return false
	}
	a.currentRefKey = nextRef
	a.pendingRollback = ""
	a.queue = append(a.queue[:removeIndex], a.queue[removeIndex+1:]...)
	return true
}
```

- [ ] **Step 6: Implement guarded auto-start**

Still in `autoadvance.go`, update imports to include:

```go
	"context"
	"fmt"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/artworkcache"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/eventlog"
```

Replace `advanceAfterEOF` with:

```go
func (a *Adapter) advanceAfterEOF(refKey string) {
	delay := a.autoAdvanceDelay
	if delay > 0 {
		time.Sleep(delay)
	}
	if !a.autoAdvanceEnabled() || a.core == nil {
		return
	}
	if st := a.core.Status(); st.State != core.StateIdle {
		return
	}
	if a.snapshotCurrentRefKey() != refKey {
		return
	}
	qi, ok := a.peekQueueHead()
	if !ok {
		slog.Debug("jellyfin: auto-advance reached end of queue")
		return
	}
	a.startQueuedItemAfterEOF(refKey, qi)
}

func (a *Adapter) startQueuedItemAfterEOF(stoppedRef string, qi QueuedItem) {
	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()
	tok, err := LoadToken(a.tokenPath())
	if err != nil || tok.AccessToken == "" {
		slog.Error("jellyfin: auto-advance: no token", "err", err)
		return
	}
	preset, err := a.currentPreset()
	if err != nil {
		slog.Error("jellyfin: auto-advance: modeline", "err", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	meta := a.fetchItemMetadataBestEffort(ctx, cfg, tok, qi.ItemID)
	info, err := FetchPlaybackInfo(ctx, PlaybackInfoInput{
		ServerURL:           cfg.ServerURL,
		Token:               tok.AccessToken,
		DeviceID:            a.deviceID,
		DeviceName:          cfg.DeviceName,
		Version:             linkVersion,
		ItemID:              qi.ItemID,
		UserID:              tok.UserID,
		MaxVideoBitrateKbps: cfg.MaxVideoBitrateKbps,
		Preset:              preset,
		StartPositionTicks:  qi.StartPositionTicks,
		MediaSourceID:       qi.MediaSourceID,
		AudioStreamIndex:    qi.AudioStreamIndex,
		SubtitleStreamIndex: qi.SubtitleStreamIndex,
		MediaKind:           meta.MediaKind,
	})
	if err != nil {
		artworkcache.Remove(meta.ArtworkPath)
		slog.Error("jellyfin: auto-advance PlaybackInfo failed", "err", err)
		return
	}
	info = mergePlaybackMetadata(info, meta)
	req := a.buildSessionRequest(playRequestInput{
		ItemID:             qi.ItemID,
		StartPositionTicks: qi.StartPositionTicks,
		PlayInfo:           info,
		ServerURL:          cfg.ServerURL,
		Token:              tok.AccessToken,
	})

	if st := a.core.Status(); st.State != core.StateIdle {
		cleanupSessionArtwork(req)
		return
	}
	if a.snapshotCurrentRefKey() != stoppedRef || !a.queueHeadStillMatches(qi) {
		cleanupSessionArtwork(req)
		return
	}

	started, err := a.core.StartSessionIfIdle(req)
	if err != nil {
		cleanupSessionArtwork(req)
		slog.Error("jellyfin: auto-advance StartSessionIfIdle failed", "err", err)
		return
	}
	if !started {
		cleanupSessionArtwork(req)
		return
	}
	if a.beforeAutoAdvanceCommit != nil {
		a.beforeAutoAdvanceCommit()
	}
	if !a.commitAutoAdvance(stoppedRef, req.AdapterRef, qi) {
		cleanupSessionArtwork(req)
		return
	}
	a.emitEvent(eventlog.SeverityInfo, fmt.Sprintf("auto-advance %s", req.AdapterRef))
	a.spawnReporter(reporterParams{
		ItemID:          qi.ItemID,
		PlaySessionID:   info.PlaySessionID,
		MediaSourceID:   info.MediaSourceID,
		AudioIdx:        qi.AudioStreamIndex,
		SubtitleIdx:     qi.SubtitleStreamIndex,
		NowPlayingQueue: a.snapshotNowPlayingQueue(qi.ItemID),
		Auth: RESTAuth{
			ServerURL: cfg.ServerURL, Token: tok.AccessToken,
			DeviceID: a.deviceID, DeviceName: cfg.DeviceName,
			Version: linkVersion,
		},
	})
}
```

- [ ] **Step 7: Run success/failure tests**

Run:

```sh
go test ./internal/adapters/jellyfin -run 'TestAutoAdvanceEOF_(StartsNextQueuedItemIfIdle|GuardMissLeavesQueueAndRefUnchanged|PlaybackInfoFailureLeavesQueueAndRefUnchanged|StartSessionIfIdleErrorLeavesQueueAndRefUnchanged|ControllerStartsDuringIdleGuardStandsDown)' -v
```

Expected: selected tests pass.

- [ ] **Step 8: Commit**

```sh
git add internal/adapters/jellyfin/autoadvance.go internal/adapters/jellyfin/commands_test.go
git commit -m "feat(jellyfin): auto advance queued items on eof"
```

---

## Task 7: Auto-Advance Race Hardening

**Files:**
- Modify: `internal/adapters/jellyfin/autoadvance.go`
- Modify: `internal/adapters/jellyfin/commands_test.go`

> **Implementation update:** Task 6 review tightened the commit invariant. Auto-advance may commit only when the auto-started queued entry is still the queue head. If a controller command inserts or replaces queue entries before adapter commit, auto-advance stands down, leaves adapter ownership unchanged, preserves the controller-mutated queue, and stops the exact core session started by auto-advance with `StopIfSession`.

- [ ] **Step 1: Write stale-session race tests**

Add:

```go
func TestAutoAdvanceEOF_StaleCurrentRefDoesNotMutateQueue(t *testing.T) {
	jfSrv := startMappedPlaybackInfoServer(t, nil)
	mgr := &fakeManager{st: core.SessionStatus{State: core.StateIdle}, startIdleStarted: true}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.autoAdvanceDelay = 0
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true, AutoAdvance: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}
	a.currentRefKey = "new-itm:ps-new"
	a.queue = []QueuedItem{{QueueEntryID: 1, ItemID: "next-itm"}}

	a.advanceAfterEOF("old-itm:ps-old")

	mgr.mu.Lock()
	idleCalls := len(mgr.idleReqs)
	mgr.mu.Unlock()
	if idleCalls != 0 {
		t.Fatalf("StartSessionIfIdle calls = %d, want 0", idleCalls)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.queue) != 1 || a.queue[0].ItemID != "next-itm" {
		t.Fatalf("queue = %+v, want unchanged [next-itm]", a.queue)
	}
}
```

- [ ] **Step 2: Write post-guard queue-head race tests**

Add:

```go
func TestAutoAdvanceEOF_PlayNextBeforeCommitStopsStaleStart(t *testing.T) {
	jfSrv := startMappedPlaybackInfoServer(t, nil)
	mgr := &fakeManager{
		st:               core.SessionStatus{State: core.StateIdle},
		startIdleStarted: true,
	}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.autoAdvanceDelay = 0
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true, AutoAdvance: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}
	a.currentRefKey = "itm-1:ps-itm-1"
	a.queue = []QueuedItem{{QueueEntryID: 1, ItemID: "next-itm"}, {QueueEntryID: 2, ItemID: "tail-itm"}}
	a.nextQueueEntryID = 2
	a.beforeAutoAdvanceCommit = func() {
		a.queueAt(playMessageData{ItemIDs: []string{"inserted-itm"}, PlayCommand: "PlayNext"}, 0)
	}

	a.advanceAfterEOF("itm-1:ps-itm-1")

	if got := a.snapshotCurrentRefKey(); got != "itm-1:ps-itm-1" {
		t.Fatalf("currentRefKey = %q, want old ref", got)
	}
	a.mu.Lock()
	queue := append([]QueuedItem(nil), a.queue...)
	reporters := len(a.reporters)
	history := len(a.history)
	a.mu.Unlock()
	if len(queue) != 3 || queue[0].ItemID != "inserted-itm" || queue[1].ItemID != "next-itm" || queue[2].ItemID != "tail-itm" {
		t.Fatalf("queue = %+v, want [inserted-itm next-itm tail-itm]", queue)
	}
	if reporters != 0 {
		t.Fatalf("reporters = %d, want 0", reporters)
	}
	if history != 0 {
		t.Fatalf("history len = %d, want 0", history)
	}

	mgr.mu.Lock()
	stopIfCalls := mgr.stopIfCalls
	stopIfRef := mgr.stopIfRef
	stopIfGeneration := mgr.stopIfGeneration
	status := mgr.st
	mgr.mu.Unlock()
	if stopIfCalls != 1 {
		t.Fatalf("StopIfSession calls = %d, want 1", stopIfCalls)
	}
	if stopIfRef != "next-itm:ps-next-itm" {
		t.Fatalf("StopIfSession ref = %q, want next-itm:ps-next-itm", stopIfRef)
	}
	if stopIfGeneration == 0 {
		t.Fatal("StopIfSession generation = 0, want started generation")
	}
	if status.State == core.StatePlaying && status.AdapterRef == "next-itm:ps-next-itm" {
		t.Fatalf("core still playing auto-started session: %+v", status)
	}
}

func TestAutoAdvanceEOF_DuplicateQueuedItemsInsertedAheadStopsStaleStart(t *testing.T) {
	jfSrv := startMappedPlaybackInfoServer(t, nil)
	mgr := &fakeManager{
		st:               core.SessionStatus{State: core.StateIdle},
		startIdleStarted: true,
	}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.autoAdvanceDelay = 0
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true, AutoAdvance: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}
	a.currentRefKey = "itm-1:ps-itm-1"
	a.queue = []QueuedItem{{QueueEntryID: 1, ItemID: "dup-itm"}, {QueueEntryID: 2, ItemID: "dup-itm"}}
	a.nextQueueEntryID = 2
	a.beforeAutoAdvanceCommit = func() {
		a.queueAt(playMessageData{ItemIDs: []string{"dup-itm"}, PlayCommand: "PlayNext"}, 0)
	}

	a.advanceAfterEOF("itm-1:ps-itm-1")

	if got := a.snapshotCurrentRefKey(); got != "itm-1:ps-itm-1" {
		t.Fatalf("currentRefKey = %q, want old ref", got)
	}
	a.mu.Lock()
	queue := append([]QueuedItem(nil), a.queue...)
	reporters := len(a.reporters)
	history := len(a.history)
	a.mu.Unlock()
	if len(queue) != 3 {
		t.Fatalf("queue = %+v, want inserted duplicate plus original two duplicates", queue)
	}
	if queue[0].QueueEntryID != 3 || queue[1].QueueEntryID != 1 || queue[2].QueueEntryID != 2 {
		t.Fatalf("QueueEntryIDs = %d,%d,%d, want inserted 3 then originals 1,2", queue[0].QueueEntryID, queue[1].QueueEntryID, queue[2].QueueEntryID)
	}
	if reporters != 0 {
		t.Fatalf("reporters = %d, want 0", reporters)
	}
	if history != 0 {
		t.Fatalf("history len = %d, want 0", history)
	}

	mgr.mu.Lock()
	stopIfCalls := mgr.stopIfCalls
	stopIfRef := mgr.stopIfRef
	stopIfGeneration := mgr.stopIfGeneration
	status := mgr.st
	mgr.mu.Unlock()
	if stopIfCalls != 1 {
		t.Fatalf("StopIfSession calls = %d, want 1", stopIfCalls)
	}
	if stopIfRef != "dup-itm:ps-dup-itm" {
		t.Fatalf("StopIfSession ref = %q, want dup-itm:ps-dup-itm", stopIfRef)
	}
	if stopIfGeneration == 0 {
		t.Fatal("StopIfSession generation = 0, want started generation")
	}
	if status.State == core.StatePlaying && status.AdapterRef == "dup-itm:ps-dup-itm" {
		t.Fatalf("core still playing auto-started session: %+v", status)
	}
}
```

- [ ] **Step 3: Verify pre-commit controller preemption coverage**

Use the existing stronger pre-commit adapter-state race test:

```go
func TestAutoAdvanceEOF_CommitFailureStopsAutoStartedSession(t *testing.T) {
	jfSrv := startMappedPlaybackInfoServer(t, nil)
	mgr := &fakeManager{
		st:               core.SessionStatus{State: core.StateIdle},
		startIdleStarted: true,
	}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.autoAdvanceDelay = 0
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true, AutoAdvance: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}
	a.currentRefKey = "itm-1:ps-itm-1"
	a.queue = []QueuedItem{{QueueEntryID: 1, ItemID: "next-itm"}}
	a.beforeAutoAdvanceCommit = func() {
		a.mu.Lock()
		a.currentRefKey = "controller-itm:ps-controller"
		a.mu.Unlock()
	}

	a.advanceAfterEOF("itm-1:ps-itm-1")

	if got := a.snapshotCurrentRefKey(); got != "controller-itm:ps-controller" {
		t.Fatalf("currentRefKey = %q, want controller-owned ref", got)
	}
	a.mu.Lock()
	queue := append([]QueuedItem(nil), a.queue...)
	reporters := len(a.reporters)
	a.mu.Unlock()
	if len(queue) != 1 || queue[0].ItemID != "next-itm" {
		t.Fatalf("queue = %+v, want unchanged [next-itm]", queue)
	}
	if reporters != 0 {
		t.Fatalf("reporters = %d, want 0", reporters)
	}

	mgr.mu.Lock()
	stopIfCalls := mgr.stopIfCalls
	stopIfRef := mgr.stopIfRef
	stopIfGeneration := mgr.stopIfGeneration
	status := mgr.st
	mgr.mu.Unlock()
	if stopIfCalls != 1 {
		t.Fatalf("StopIfSession calls = %d, want 1", stopIfCalls)
	}
	if stopIfRef != "next-itm:ps-next-itm" {
		t.Fatalf("StopIfSession ref = %q, want next-itm:ps-next-itm", stopIfRef)
	}
	if stopIfGeneration == 0 {
		t.Fatal("StopIfSession generation = 0, want started generation")
	}
	if status.State == core.StatePlaying && status.AdapterRef == "next-itm:ps-next-itm" {
		t.Fatalf("core still playing auto-started session: %+v", status)
	}
}

func TestAutoAdvanceEOF_PlayNowQueueReplacementBeforeCommitFailsCleanly(t *testing.T) {
	jfSrv := startMappedPlaybackInfoServer(t, nil)
	mgr := &fakeManager{st: core.SessionStatus{State: core.StateIdle}, startIdleStarted: true}
	a := New(mgr, t.TempDir(), "dev-1", "", nil)
	a.autoAdvanceDelay = 0
	a.cfg = Config{ServerURL: jfSrv.URL, MaxVideoBitrateKbps: 4000, Enabled: true, AutoAdvance: true}
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "tok", UserID: "uid", ServerURL: jfSrv.URL}); err != nil {
		t.Fatal(err)
	}
	a.currentRefKey = "itm-1:ps-itm-1"
	a.queue = []QueuedItem{{QueueEntryID: 1, ItemID: "next-itm"}, {QueueEntryID: 2, ItemID: "tail-itm"}}
	a.nextQueueEntryID = 2
	a.beforeAutoAdvanceCommit = func() {
		_ = a.replaceQueueForPlayNow(playMessageData{
			ItemIDs:     []string{"replacement-now", "replacement-tail"},
			PlayCommand: "PlayNow",
		})
	}

	a.advanceAfterEOF("itm-1:ps-itm-1")

	if got := a.snapshotCurrentRefKey(); got != "itm-1:ps-itm-1" {
		t.Fatalf("currentRefKey = %q, want old ref because commit should fail", got)
	}
	a.mu.Lock()
	queue := append([]QueuedItem(nil), a.queue...)
	reporters := len(a.reporters)
	history := len(a.history)
	a.mu.Unlock()
	if len(queue) != 1 || queue[0].ItemID != "replacement-tail" {
		t.Fatalf("queue = %+v, want replacement tail only", queue)
	}
	if reporters != 0 {
		t.Fatalf("reporters = %d, want 0", reporters)
	}
	if history != 0 {
		t.Fatalf("history len = %d, want 0", history)
	}

	mgr.mu.Lock()
	stopIfCalls := mgr.stopIfCalls
	stopIfRef := mgr.stopIfRef
	stopIfGeneration := mgr.stopIfGeneration
	status := mgr.st
	mgr.mu.Unlock()
	if stopIfCalls != 1 {
		t.Fatalf("StopIfSession calls = %d, want 1", stopIfCalls)
	}
	if stopIfRef != "next-itm:ps-next-itm" {
		t.Fatalf("StopIfSession ref = %q, want next-itm:ps-next-itm", stopIfRef)
	}
	if stopIfGeneration == 0 {
		t.Fatal("StopIfSession generation = 0, want started generation")
	}
	if status.State == core.StatePlaying && status.AdapterRef == "next-itm:ps-next-itm" {
		t.Fatalf("core still playing auto-started session: %+v", status)
	}
}
```

- [ ] **Step 4: Run race hardening tests**

Run:

```sh
go test ./internal/adapters/jellyfin -run 'TestAutoAdvanceEOF_(StaleCurrentRefDoesNotMutateQueue|PlayNextBeforeCommitStopsStaleStart|DuplicateQueuedItemsInsertedAheadStopsStaleStart|CommitFailureStopsAutoStartedSession|PlayNowQueueReplacementBeforeCommitFailsCleanly)' -v
```

Expected: selected tests pass.

- [ ] **Step 5: Run broader auto-advance tests**

Run:

```sh
go test ./internal/adapters/jellyfin -run 'TestAutoAdvanceEOF_|TestBuildSessionRequest_OnStop' -v
```

Expected: all selected tests pass.

- [ ] **Step 6: Commit**

```sh
git add internal/adapters/jellyfin/autoadvance.go internal/adapters/jellyfin/commands_test.go
git commit -m "test(jellyfin): harden auto advance races"
```

---

## Task 8: Full Verification

**Files:**
- Verify only.

- [ ] **Step 1: Run Jellyfin adapter tests**

Run:

```sh
go test ./internal/adapters/jellyfin/...
```

Expected: pass.

- [ ] **Step 2: Run adjacent adapter/core tests**

Run:

```sh
go test ./internal/adapters/... ./internal/core/...
```

Expected: pass.

- [ ] **Step 3: Run race test for Jellyfin package**

Run:

```sh
go test -race ./internal/adapters/jellyfin/...
```

Expected: pass.

- [ ] **Step 4: Run vet**

Run:

```sh
go vet ./...
```

Expected: pass.

- [ ] **Step 5: Run full repository tests**

Run:

```sh
go test ./...
```

Expected: pass.

- [ ] **Step 6: Commit any verification-only fixes**

If verification exposed small compile/test fixes, commit them:

```sh
git add internal/adapters/jellyfin
git commit -m "fix(jellyfin): stabilize auto advance tests"
```

If no fixes were needed, do not create an empty commit.

---

## Completion Criteria

- `[adapters.jellyfin].auto_advance` exists, defaults off, and is hot-swappable.
- Multi-item Jellyfin `PlayNow` starts the selected item and stores only following items in `Adapter.queue`.
- Multi-item `PlayNext` and `PlayLast` preserve controller order while inserting/prepending or appending.
- Each queued item has a stable non-zero `QueueEntryID`.
- Manual `NextTrack` still uses `StartSession`, pops immediately, and behaves as before.
- EOF auto-advance only runs for `OnStop("eof")`.
- EOF auto-advance uses `StartSessionIfIdle` and never pre-pops the queue.
- Guard miss, PlaybackInfo failure, StartSessionIfIdle error, stale EOF, and pre-commit controller races leave queue and ownership state intact.
- A successful auto-advance removes the started item only when that `QueueEntryID` is still the queue head.
- Reporter wakeup and artwork cleanup still run through the wrapped `OnStop`.
- `go test ./internal/adapters/jellyfin/...`, `go test -race ./internal/adapters/jellyfin/...`, `go vet ./...`, and `go test ./...` pass.
