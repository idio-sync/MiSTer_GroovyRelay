package streams

import (
	"math/rand"
	"slices"
	"testing"
)

func TestBuildQueueSequentialShuffleAndFirstThenShuffle(t *testing.T) {
	items := []StreamItem{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}, {ID: "e"}}

	seq, err := buildQueue("mtv-rewind", Channel{ID: "seq", Name: "Sequential", PlayMode: PlaySequential, Items: items}, rand.New(rand.NewSource(1)))
	if err != nil {
		t.Fatalf("buildQueue sequential: %v", err)
	}
	if got := itemIDs(seq.Items); !slices.Equal(got, []string{"a", "b", "c", "d", "e"}) {
		t.Fatalf("sequential order = %v", got)
	}
	if seq.loopMode != loopSequential {
		t.Fatalf("sequential loopMode = %v, want %v", seq.loopMode, loopSequential)
	}

	shuffleA, err := buildQueue("mtv-rewind", Channel{ID: "shuffle", Name: "Shuffle", PlayMode: PlayShuffle, Items: items}, rand.New(rand.NewSource(7)))
	if err != nil {
		t.Fatalf("buildQueue shuffle A: %v", err)
	}
	shuffleB, err := buildQueue("mtv-rewind", Channel{ID: "shuffle", Name: "Shuffle", PlayMode: PlayShuffle, Items: items}, rand.New(rand.NewSource(7)))
	if err != nil {
		t.Fatalf("buildQueue shuffle B: %v", err)
	}
	if gotA, gotB := itemIDs(shuffleA.Items), itemIDs(shuffleB.Items); !slices.Equal(gotA, gotB) {
		t.Fatalf("shuffle not deterministic: %v vs %v", gotA, gotB)
	}
	if got := itemIDs(shuffleA.Items); slices.Equal(got, []string{"a", "b", "c", "d", "e"}) {
		t.Fatalf("shuffle preserved provider order with test seed: %v", got)
	}
	if shuffleA.loopMode != loopShuffle {
		t.Fatalf("shuffle loopMode = %v, want %v", shuffleA.loopMode, loopShuffle)
	}

	first, err := buildQueue("mtv-rewind", Channel{ID: "first", Name: "First", PlayMode: PlayFirstThenShuffle, Items: items}, rand.New(rand.NewSource(7)))
	if err != nil {
		t.Fatalf("buildQueue first_then_shuffle: %v", err)
	}
	if first.Items[0].ID != "a" {
		t.Fatalf("first_then_shuffle first item = %q, want a", first.Items[0].ID)
	}
	if got := itemIDs(first.Items[1:]); !sameSet(got, []string{"b", "c", "d", "e"}) {
		t.Fatalf("first_then_shuffle tail = %v", got)
	}
	if first.loopMode != loopFirstThenShuffle {
		t.Fatalf("first_then_shuffle loopMode = %v, want %v", first.loopMode, loopFirstThenShuffle)
	}
}

func TestBuildQueueFirstThenShuffleSingleItem(t *testing.T) {
	ch := Channel{ID: "intro", PlayMode: PlayFirstThenShuffle, Items: []StreamItem{{ID: "a"}}}
	q, err := buildQueue("mtv-rewind", ch, rand.New(rand.NewSource(1)))
	if err != nil {
		t.Fatalf("buildQueue: %v", err)
	}
	if q.Items[0].ID != "a" || q.loopMode != loopSequential {
		t.Fatalf("queue = %+v", q)
	}
}

func TestReplaySequentialResetsToFirstItem(t *testing.T) {
	q := &ActiveQueue{
		Items: []StreamItem{
			{ID: "first"},
			{ID: "second"},
		},
		Index:    1,
		loopMode: loopSequential,
	}
	q.resetForReplay(rand.New(rand.NewSource(1)))
	if q.Index != 0 {
		t.Fatalf("replay sequential index = %d, want 0", q.Index)
	}
}

func TestPreviousFirstThenShuffleWrapsWithinTail(t *testing.T) {
	q := &ActiveQueue{
		Items: []StreamItem{
			{ID: "intro"},
			{ID: "tail-a"},
			{ID: "tail-b"},
		},
		Index:    1,
		loopMode: loopFirstThenShuffle,
	}
	if !q.advancePrevious() {
		t.Fatal("Previous should move within first_then_shuffle tail")
	}
	if got := q.Items[q.Index].ID; got != "tail-b" {
		t.Fatalf("Previous from first tail item landed on %q, want tail-b", got)
	}
	if !q.advancePrevious() {
		t.Fatal("Previous should continue within first_then_shuffle tail")
	}
	if got := q.Items[q.Index].ID; got != "tail-a" {
		t.Fatalf("Previous from tail-b landed on %q, want tail-a", got)
	}
}

func TestBuildQueueEmptyFails(t *testing.T) {
	_, err := buildQueue("mtv-rewind", Channel{ID: "empty", PlayMode: PlayShuffle}, rand.New(rand.NewSource(1)))
	if err == nil {
		t.Fatal("empty queue should fail")
	}
}

func TestStaleOnStopDoesNotAdvanceNewQueue(t *testing.T) {
	a, _ := newTestAdapterWithFakeCore(t)
	a.active = &ActiveQueue{ProviderID: "mtv-rewind", ChannelID: "metal", Generation: 2, ItemToken: 9, SessionID: "new", Items: []StreamItem{{ID: "new"}}}
	cb := a.makeOnStop(queueCapture{Generation: 1, ItemToken: 8, SessionID: "old", ItemID: "old"})
	cb("eof")
	if a.active.SessionID != "new" {
		t.Fatalf("stale callback mutated active queue: %+v", a.active)
	}
}

func TestManualControlsIncrementGenerationAndCancelResolve(t *testing.T) {
	a, core := newTestAdapterWithFakeCore(t)
	cancelled := false
	a.active = &ActiveQueue{
		SessionID:     "s1",
		ProviderID:    "mtv-rewind",
		ProviderName:  "MTV Rewind",
		ChannelID:     "metal",
		ChannelName:   "Metal",
		Generation:    4,
		ItemToken:     1,
		Items:         []StreamItem{{ID: "a", URL: "https://youtu.be/a"}, {ID: "b", URL: "https://youtu.be/b"}},
		loopMode:      loopSequential,
		cancelResolve: func() { cancelled = true },
	}
	core.status.AdapterRef = queueAdapterRef(a.active, a.active.ItemToken)
	if err := a.Next(t.Context()); err != nil {
		t.Fatalf("Next: %v", err)
	}
	if !cancelled {
		t.Fatal("Next did not cancel in-flight resolution")
	}
	if a.active.Generation != 5 {
		t.Fatalf("generation = %d, want 5", a.active.Generation)
	}
	if a.active.Index != 1 {
		t.Fatalf("index = %d, want 1", a.active.Index)
	}
	if core.startCalls != 1 {
		t.Fatalf("core start calls = %d, want 1", core.startCalls)
	}
}

func TestNextDoesNotPreemptForeignOwner(t *testing.T) {
	a, core := newTestAdapterWithFakeCore(t)
	a.active = &ActiveQueue{
		SessionID:    "s1",
		ProviderID:   "mtv-rewind",
		ProviderName: "MTV Rewind",
		ChannelID:    "metal",
		ChannelName:  "Metal",
		Generation:   4,
		ItemToken:    1,
		Items:        []StreamItem{{ID: "a", URL: "https://youtu.be/a"}, {ID: "b", URL: "https://youtu.be/b"}},
		loopMode:     loopSequential,
	}
	core.status.AdapterRef = "url:foreign"

	if err := a.Next(t.Context()); err == nil {
		t.Fatal("Next with foreign core owner should fail")
	}
	if a.active.Generation != 4 {
		t.Fatalf("generation = %d, want 4", a.active.Generation)
	}
	if a.active.Index != 0 {
		t.Fatalf("index = %d, want 0", a.active.Index)
	}
	if core.stopCalls != 0 {
		t.Fatalf("guarded core stop calls = %d, want 0", core.stopCalls)
	}
	if core.startCalls != 0 {
		t.Fatalf("core start calls = %d, want 0", core.startCalls)
	}
}

func TestQueueSnapshotSurvivesCatalogRefresh(t *testing.T) {
	a, _ := newTestAdapterWithFakeCore(t)
	a.active = &ActiveQueue{
		ProviderID: "mtv-rewind",
		ChannelID:  "metal",
		Items:      []StreamItem{{ID: "old"}},
	}
	a.replaceCatalogsForTest([]ProviderCatalog{{
		ProviderID: "mtv-rewind",
		Channels:   []Channel{{ID: "metal", Items: []StreamItem{{ID: "new"}}}},
	}})
	if got := a.active.Items[0].ID; got != "old" {
		t.Fatalf("active queue item = %q, want old snapshot", got)
	}
}

func TestAdhocSingleItemStopsOnEOF(t *testing.T) {
	a, _ := newTestAdapterWithFakeCore(t)
	a.active = &ActiveQueue{
		ProviderID: "mtv-rewind",
		ChannelID:  "adhoc",
		SessionID:  "s1",
		ItemToken:  1,
		Items:      []StreamItem{{ID: "dQw4w9WgXcQ"}},
		loopMode:   loopNone,
	}
	cb := a.makeOnStop(queueCapture{Generation: 0, ItemToken: 1, SessionID: "s1", ItemID: "dQw4w9WgXcQ"})
	cb("eof")
	if a.active != nil {
		t.Fatalf("adhoc EOF should clear queue, got %+v", a.active)
	}
}

func TestAdhocPauseAfterEOFIsNoOp(t *testing.T) {
	a, _ := newTestAdapterWithFakeCore(t)
	a.active = &ActiveQueue{
		ProviderID: "mtv-rewind",
		ChannelID:  "adhoc",
		SessionID:  "s1",
		ItemToken:  1,
		Items:      []StreamItem{{ID: "dQw4w9WgXcQ"}},
		loopMode:   loopNone,
	}
	cb := a.makeOnStop(queueCapture{Generation: 0, ItemToken: 1, SessionID: "s1", ItemID: "dQw4w9WgXcQ"})
	cb("eof")
	if a.active != nil {
		t.Fatal("precondition: EOF should clear adhoc queue")
	}
	if err := a.Pause(t.Context()); err == nil {
		t.Fatal("Pause on cleared adhoc queue should report no-active-session error")
	}
}

func TestPauseDoesNotCallCoreForForeignOwner(t *testing.T) {
	a, core := newTestAdapterWithFakeCore(t)
	a.active = &ActiveQueue{
		ProviderID: "mtv-rewind",
		ChannelID:  "metal",
		SessionID:  "s1",
		ItemToken:  1,
		Items:      []StreamItem{{ID: "dQw4w9WgXcQ"}},
	}
	core.status.AdapterRef = "url:foreign"

	if err := a.Pause(t.Context()); err == nil {
		t.Fatal("Pause with foreign core owner should fail")
	}
	if core.pauseCalls != 0 {
		t.Fatalf("guarded core pause calls = %d, want 0", core.pauseCalls)
	}
	if core.rawPauseCalls != 0 {
		t.Fatalf("raw core pause calls = %d, want 0", core.rawPauseCalls)
	}
}

func TestPauseUsesGuardedCoreControl(t *testing.T) {
	a, core := newTestAdapterWithFakeCore(t)
	a.active = &ActiveQueue{
		ProviderID: "mtv-rewind",
		ChannelID:  "metal",
		SessionID:  "s1",
		ItemToken:  1,
		Items:      []StreamItem{{ID: "dQw4w9WgXcQ"}},
	}
	core.status.AdapterRef = queueAdapterRef(a.active, a.active.ItemToken)

	if err := a.Pause(t.Context()); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if core.pauseCalls != 1 {
		t.Fatalf("guarded core pause calls = %d, want 1", core.pauseCalls)
	}
	if core.rawPauseCalls != 0 {
		t.Fatalf("raw core pause calls = %d, want 0", core.rawPauseCalls)
	}
}

func TestPauseStaleSameChannelQueueDoesNotPauseNewerSession(t *testing.T) {
	a, core := newTestAdapterWithFakeCore(t)
	old := &ActiveQueue{
		ProviderID: "mtv-rewind",
		ChannelID:  "metal",
		SessionID:  "old-session",
		ItemToken:  1,
		Items:      []StreamItem{{ID: "old", URL: "https://youtu.be/old"}},
	}
	newer := &ActiveQueue{
		ProviderID: "mtv-rewind",
		ChannelID:  "metal",
		SessionID:  "new-session",
		ItemToken:  1,
		Items:      []StreamItem{{ID: "new", URL: "https://youtu.be/new"}},
	}
	a.active = old
	oldRef := queueAdapterRef(old, old.ItemToken)
	newRef := queueAdapterRef(newer, newer.ItemToken)
	core.status.AdapterRef = oldRef
	core.pauseIfHook = func(ref string) {
		if ref != oldRef {
			t.Errorf("Pause captured ref = %q, want %q", ref, oldRef)
		}
		a.mu.Lock()
		a.active = newer
		a.mu.Unlock()
		core.mu.Lock()
		core.status.AdapterRef = newRef
		core.mu.Unlock()
	}

	if err := a.Pause(t.Context()); err == nil {
		t.Fatal("stale Pause should not pause newer same-channel session")
	}
	if core.pauseCalls != 0 {
		t.Fatalf("guarded core pause calls = %d, want 0", core.pauseCalls)
	}
	if a.active == nil || a.active.SessionID != "new-session" {
		t.Fatalf("newer queue was not preserved: %+v", a.active)
	}
}

func TestAdhocFailedNextDoesNotInvalidateEOFCallback(t *testing.T) {
	a, _ := newTestAdapterWithFakeCore(t)
	a.active = &ActiveQueue{
		ProviderID: "mtv-rewind",
		ChannelID:  "adhoc",
		SessionID:  "s1",
		ItemToken:  1,
		Items:      []StreamItem{{ID: "dQw4w9WgXcQ"}},
		loopMode:   loopNone,
	}
	capture := queueCapture{Generation: 0, ItemToken: 1, SessionID: "s1", ItemID: "dQw4w9WgXcQ", AdapterRef: queueAdapterRef(a.active, 1)}

	if err := a.Next(t.Context()); err == nil {
		t.Fatal("Next on single-item adhoc queue should fail")
	}
	a.makeOnStop(capture)("eof")
	if a.active != nil {
		t.Fatalf("matching EOF should clear queue after failed Next, got %+v", a.active)
	}
}

func TestForeignPreemptClearsOnlyMatchingQueue(t *testing.T) {
	a, _ := newTestAdapterWithFakeCore(t)
	a.active = &ActiveQueue{ProviderID: "mtv-rewind", ChannelID: "metal", Generation: 2, ItemToken: 3, SessionID: "current", Items: []StreamItem{{ID: "x"}}}
	stale := a.makeOnStop(queueCapture{Generation: 1, ItemToken: 3, SessionID: "old", ItemID: "x"})
	stale("preempted")
	if a.active == nil {
		t.Fatal("stale preempt cleared current queue")
	}
	matching := a.makeOnStop(queueCapture{Generation: 2, ItemToken: 3, SessionID: "current", ItemID: "x"})
	matching("preempted")
	if a.active != nil {
		t.Fatal("matching preempt should clear queue")
	}
}

func TestEOFContinuationDoesNotStartNewerQueue(t *testing.T) {
	a, core := newTestAdapterWithFakeCore(t)
	a.active = &ActiveQueue{
		SessionID:     "old-session",
		ProviderID:    "mtv-rewind",
		ChannelID:     "metal",
		Generation:    0,
		ItemToken:     1,
		Items:         []StreamItem{{ID: "old-1", URL: "https://youtu.be/old1"}, {ID: "old-2", URL: "https://youtu.be/old2"}},
		loopMode:      loopSequential,
		cancelResolve: nil,
	}
	newer := &ActiveQueue{
		SessionID:  "new-session",
		ProviderID: "mtv-rewind",
		ChannelID:  "metal",
		Generation: 9,
		ItemToken:  4,
		Items:      []StreamItem{{ID: "new", URL: "https://youtu.be/new"}},
	}
	a.beforeQueueContinuation = func() {
		a.beforeQueueContinuation = nil
		a.mu.Lock()
		a.active = newer
		a.mu.Unlock()
	}

	a.makeOnStop(queueCapture{
		Generation: 0,
		ItemToken:  1,
		SessionID:  "old-session",
		ItemID:     "old-1",
		AdapterRef: queueAdapterRef(a.active, 1),
	})("eof")

	if core.startCalls != 0 {
		t.Fatalf("stale EOF continuation started newer queue: start calls = %d", core.startCalls)
	}
	if a.active == nil || a.active.SessionID != "new-session" {
		t.Fatalf("newer queue was not preserved: %+v", a.active)
	}
}

func TestStopQueueClearsBeforeMatchingEOFCanAdvance(t *testing.T) {
	a, core := newTestAdapterWithFakeCore(t)
	a.active = &ActiveQueue{
		SessionID:  "s1",
		ProviderID: "mtv-rewind",
		ChannelID:  "metal",
		Generation: 0,
		ItemToken:  1,
		Items: []StreamItem{
			{ID: "old-1", URL: "https://youtu.be/old1"},
			{ID: "old-2", URL: "https://youtu.be/old2"},
		},
		loopMode: loopSequential,
	}
	core.status.AdapterRef = queueAdapterRef(a.active, a.active.ItemToken)
	a.beforeStopQueuePlaybackLock = func(capture queueCapture) {
		a.makeOnStop(capture)("eof")
	}

	if err := a.StopQueue(t.Context()); err != nil {
		t.Fatalf("StopQueue: %v", err)
	}
	if a.active != nil {
		t.Fatalf("StopQueue should clear active before EOF can advance it, got %+v", a.active)
	}
	if core.startCalls != 0 {
		t.Fatalf("matching EOF raced StopQueue and started playback: start calls = %d", core.startCalls)
	}
}

func TestStopQueuePreservesNewerQueueInstalledDuringCoreStop(t *testing.T) {
	a, core := newTestAdapterWithFakeCore(t)
	a.active = &ActiveQueue{
		SessionID:    "old-session",
		ProviderID:   "mtv-rewind",
		ProviderName: "MTV Rewind",
		ChannelID:    "metal",
		ChannelName:  "Metal",
		Generation:   7,
		ItemToken:    3,
		Items:        []StreamItem{{ID: "old"}},
	}
	core.status.AdapterRef = queueAdapterRef(a.active, a.active.ItemToken)
	newer := &ActiveQueue{
		SessionID:    "new-session",
		ProviderID:   "mtv-rewind",
		ProviderName: "MTV Rewind",
		ChannelID:    "metal",
		ChannelName:  "Metal",
		Generation:   1,
		ItemToken:    1,
		Items:        []StreamItem{{ID: "new"}},
	}
	core.stopHook = func() {
		a.mu.Lock()
		a.active = newer
		a.mu.Unlock()
	}

	if err := a.StopQueue(t.Context()); err != nil {
		t.Fatalf("StopQueue: %v", err)
	}
	if core.stopCalls != 1 {
		t.Fatalf("guarded core stop calls = %d, want 1", core.stopCalls)
	}
	if core.rawStopCalls != 0 {
		t.Fatalf("raw core stop calls = %d, want 0", core.rawStopCalls)
	}
	if a.active == nil || a.active.SessionID != "new-session" {
		t.Fatalf("newer queue was not preserved: %+v", a.active)
	}
}

func TestStopQueueDoesNotMutateForeignOwner(t *testing.T) {
	a, core := newTestAdapterWithFakeCore(t)
	a.active = &ActiveQueue{
		ProviderID: "mtv-rewind",
		ChannelID:  "metal",
		SessionID:  "s1",
		ItemToken:  1,
		Items:      []StreamItem{{ID: "dQw4w9WgXcQ"}},
	}
	core.status.AdapterRef = "url:foreign"

	if err := a.StopQueue(t.Context()); err == nil {
		t.Fatal("StopQueue with foreign core owner should fail")
	}
	if core.stopCalls != 0 {
		t.Fatalf("guarded core stop calls = %d, want 0", core.stopCalls)
	}
	if core.rawStopCalls != 0 {
		t.Fatalf("raw core stop calls = %d, want 0", core.rawStopCalls)
	}
	if a.active == nil || a.active.SessionID != "s1" {
		t.Fatalf("StopQueue with foreign owner should preserve streams active queue, got %+v", a.active)
	}
}

func TestStopQueueUsesGuardedCoreControl(t *testing.T) {
	a, core := newTestAdapterWithFakeCore(t)
	a.active = &ActiveQueue{
		ProviderID: "mtv-rewind",
		ChannelID:  "metal",
		SessionID:  "s1",
		ItemToken:  1,
		Items:      []StreamItem{{ID: "dQw4w9WgXcQ"}},
	}
	core.status.AdapterRef = queueAdapterRef(a.active, a.active.ItemToken)

	if err := a.StopQueue(t.Context()); err != nil {
		t.Fatalf("StopQueue: %v", err)
	}
	if core.stopCalls != 1 {
		t.Fatalf("guarded core stop calls = %d, want 1", core.stopCalls)
	}
	if core.rawStopCalls != 0 {
		t.Fatalf("raw core stop calls = %d, want 0", core.rawStopCalls)
	}
	if a.active != nil {
		t.Fatalf("StopQueue should clear streams active queue, got %+v", a.active)
	}
}

func itemIDs(items []StreamItem) []string {
	ids := make([]string, len(items))
	for i, item := range items {
		ids[i] = item.ID
	}
	return ids
}

func sameSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	got = slices.Clone(got)
	want = slices.Clone(want)
	slices.Sort(got)
	slices.Sort(want)
	return slices.Equal(got, want)
}
