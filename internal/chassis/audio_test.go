package chassis

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

type fakeAudioViewer struct {
	snap *core.AudioScopeSnapshot
}

func (f *fakeAudioViewer) AudioScopes() *core.AudioScopeSnapshot { return f.snap }

func TestAudioEnvelopeFromViewer_NilViewerReturnsPending(t *testing.T) {
	got := audioEnvelopeFromViewer(nil)
	pending, ok := got.(*audioPendingEnvelope)
	if !ok || pending.Status != "pending" {
		t.Errorf("got %#v, want *audioPendingEnvelope{Status:pending}", got)
	}
}

func TestAudioEnvelopeFromViewer_NilSnapshotReturnsPending(t *testing.T) {
	got := audioEnvelopeFromViewer(&fakeAudioViewer{snap: nil})
	if _, ok := got.(*audioPendingEnvelope); !ok {
		t.Errorf("got %#v, want pending", got)
	}
}

func TestAudioEnvelopeFromViewer_ZeroGenerationReturnsPending(t *testing.T) {
	snap := &core.AudioScopeSnapshot{Generation: 0, SampleRate: 48000, Channels: 2}
	got := audioEnvelopeFromViewer(&fakeAudioViewer{snap: snap})
	if _, ok := got.(*audioPendingEnvelope); !ok {
		t.Errorf("got %#v, want pending", got)
	}
}

func TestAudioEnvelopeFromViewer_LiveSnapshotReturnsLive(t *testing.T) {
	snap := &core.AudioScopeSnapshot{
		Generation: 42, SampleRate: 48000, Channels: 2,
		Peak: [2]float32{0.8, 0.7}, RMS: [2]float32{0.5, 0.4},
		PhaseCorr: 0.9, LUFSShort: -14.2,
	}
	got := audioEnvelopeFromViewer(&fakeAudioViewer{snap: snap})
	live, ok := got.(*audioLiveEnvelope)
	if !ok {
		t.Fatalf("got %#v, want *audioLiveEnvelope", got)
	}
	if live.Status != "live" || live.Generation != 42 {
		t.Errorf("status/generation = %q/%d, want live/42", live.Status, live.Generation)
	}
	if live.VU.Left.Peak != 0.8 || live.VU.Right.RMS != 0.4 {
		t.Errorf("VU = %+v, want Peak=0.8 RMS=0.4", live.VU)
	}
}

func TestAudioLiveEnvelope_MarshalJSONIncludesLegitimateZeros(t *testing.T) {
	snap := &core.AudioScopeSnapshot{
		Generation: 1, SampleRate: 48000, Channels: 2,
		PhaseCorr: 0.0,
		LUFSShort: 0.0,
	}
	got := audioEnvelopeFromViewer(&fakeAudioViewer{snap: snap})
	body, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	s := string(body)
	if !strings.Contains(s, `"phaseCorr":0,`) {
		t.Errorf("phaseCorr=0 omitted: %s", s)
	}
	if !strings.Contains(s, `"lufsShort":0,`) {
		t.Errorf("lufsShort=0 omitted: %s", s)
	}
}

func TestAudioLiveEnvelope_MarshalJSONClampsNaNInf(t *testing.T) {
	snap := &core.AudioScopeSnapshot{
		Generation: 1, SampleRate: 48000, Channels: 2,
		Peak:      [2]float32{float32(math.NaN()), float32(math.Inf(1))},
		PhaseCorr: float32(math.Inf(-1)),
		LUFSShort: float32(math.NaN()),
	}
	got := audioEnvelopeFromViewer(&fakeAudioViewer{snap: snap})
	body, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	s := string(body)
	if strings.Contains(s, "NaN") || strings.Contains(s, "Inf") {
		t.Errorf("NaN/Inf leaked: %s", s)
	}
}

func TestAudioShouldEmit_PendingToPendingFalse(t *testing.T) {
	a := &audioPendingEnvelope{Status: "pending"}
	b := &audioPendingEnvelope{Status: "pending"}
	if audioShouldEmit(a, b) {
		t.Error("pending→pending should not emit")
	}
}

func TestAudioShouldEmit_LiveAlwaysTrue(t *testing.T) {
	a := &audioLiveEnvelope{Status: "live", Generation: 1}
	b := &audioLiveEnvelope{Status: "live", Generation: 1}
	if !audioShouldEmit(a, b) {
		t.Error("live→live (identical) should emit (cadence preservation)")
	}
}

func TestAudioShouldEmit_TransitionsTrue(t *testing.T) {
	pending := &audioPendingEnvelope{Status: "pending"}
	live := &audioLiveEnvelope{Status: "live", Generation: 1}
	if !audioShouldEmit(pending, live) {
		t.Error("pending→live should emit")
	}
	if !audioShouldEmit(live, pending) {
		t.Error("live→pending should emit")
	}
}
