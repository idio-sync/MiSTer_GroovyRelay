package chassis

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

func TestMeterSamplerFormatsLiveLowRateFields(t *testing.T) {
	sampler := newMeterSampler()
	now := time.Unix(100, 0)
	snap := core.StatusHomeView{
		State:      core.StatePlaying,
		Title:      "Live",
		AdapterRef: "url:live",
		Source:     "url",
		Generation: 4,
		Position:   10 * time.Second,
		Duration:   time.Minute,
		Meter: core.MeterHomeView{
			Source: core.SourceMeterView{
				Width: 1280, Height: 720, FrameRate: 29.97,
				VideoCodec: "h264", AudioCodec: "aac", AudioRate: 48000, AudioChannels: 2,
				DisplayAspectRatioNum: 16, DisplayAspectRatioDen: 9,
				FormatBitrateBPS: 2400000,
			},
			Crop: core.CropMeterView{Mode: "letterbox"},
			Pipeline: core.PipelineMeterView{
				ModelineName: "NTSC_480i", OutputWidth: 720, OutputHeight: 480, FieldHeight: 240,
				FieldRateHz: 59.94, HorizontalKHz: 15.734, InterlacedOutput: true, Standard: "ntsc",
				FieldOrder: "tff", RGBMode: "bt601", LZ4Enabled: true, DeltaLZ4Enabled: true,
				AudioSampleRate: 48000, AudioChannels: 2,
			},
			Runtime: core.RuntimeMeterView{
				BlitsTotal: 100, Underruns: 0, WireBytes: 1_000_000, LastACKAge: 4 * time.Millisecond,
				Generation: 4,
			},
		},
	}
	first := sampler.Sample(snap, adapters.MeterOverlay{}, false, now)
	secondSnap := snap
	secondSnap.Meter.Runtime.BlitsTotal = 130
	secondSnap.Meter.Runtime.Underruns = 3
	secondSnap.Meter.Runtime.WireBytes = 8_000_000
	got := sampler.Sample(secondSnap, adapters.MeterOverlay{HLS: &adapters.HLSMeterOverlay{
		CachedSegments: 6, MaxCachedSegments: 12, CacheBytes: 1234, SelectedVariantBPS: 2600000,
	}}, false, now.Add(500*time.Millisecond))

	if first.SampleSeq == got.SampleSeq {
		t.Fatalf("SampleSeq did not advance: first=%d got=%d", first.SampleSeq, got.SampleSeq)
	}
	if got.SourceStrip.AudioIn != "AAC - STEREO" {
		t.Fatalf("AudioIn = %q", got.SourceStrip.AudioIn)
	}
	if got.SourceStrip.AudioOut != "S16LE - 48k - STEREO" {
		t.Fatalf("AudioOut = %q", got.SourceStrip.AudioOut)
	}
	if got.SourceStrip.HLSBuffer != "6 / 12 SEG" || got.SourceStrip.HLSCachedSegments != 6 {
		t.Fatalf("HLS fields = %+v", got.SourceStrip)
	}
	if got.SourceStrip.Drops != "10.0" || got.SourceStrip.DropsPercent != 10.0 {
		t.Fatalf("drops = %q %.1f", got.SourceStrip.Drops, got.SourceStrip.DropsPercent)
	}
	if got.MidRow.Standard != "ntsc" || !got.MidRow.StandardNTSC || got.MidRow.StandardPAL {
		t.Fatalf("standard lamps = %+v", got.MidRow)
	}
	if got.MidRow.ThroughputSampleMBs <= 0 || len(got.MidRow.ThroughputHistoryMBs) != 1 {
		t.Fatalf("throughput = %+v", got.MidRow)
	}
	if got.Readout.Pipe != "LZ4+D - TFF" || !strings.Contains(got.Readout.Link, "MiSTer") {
		t.Fatalf("readout = %+v", got.Readout)
	}
}

// TestMeterCarriesLinkHealthCounters: the link-distress counters ride the
// meter snapshot into the SSE envelope (same pattern as blitsTotal /
// underrunsTotal — carried in JSON for the front-end, no formatting layer).
func TestMeterCarriesLinkHealthCounters(t *testing.T) {
	sampler := newMeterSampler()
	snap := core.StatusHomeView{
		State:      core.StatePlaying,
		AdapterRef: "url:live",
		Source:     "url",
		Generation: 4,
		Meter: core.MeterHomeView{
			Runtime: core.RuntimeMeterView{
				BlitsTotal: 100, Generation: 4,
				LinkHealth: core.LinkHealth{
					TornPayloadSends: 3,
					ENOBUFTotal:      5,
					AudioRingDrops:   2,
					FramesAhead:      7,
				},
			},
		},
	}
	got := sampler.Sample(snap, adapters.MeterOverlay{}, false, time.Unix(100, 0))
	strip := got.SourceStrip
	if strip.TornPayloadSendsTotal != 3 || strip.EnobufTotal != 5 ||
		strip.AudioRingDropsTotal != 2 || strip.FramesAhead != 7 {
		t.Fatalf("SourceStrip link health = %+v, want torn=3 enobuf=5 ringDrops=2 framesAhead=7", strip)
	}

	raw, err := json.Marshal(meterEnvelopeFrom(got))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		`"tornPayloadSendsTotal":3`,
		`"enobufTotal":5`,
		`"audioRingDropsTotal":2`,
		`"framesAhead":7`,
	} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("meter envelope missing %s:\n%s", key, raw)
		}
	}
}

func TestMeterSamplerPausedFreezesHistoriesAndDisplayValues(t *testing.T) {
	sampler := newMeterSampler()
	now := time.Unix(200, 0)
	snap := core.StatusHomeView{
		State:      core.StatePlaying,
		AdapterRef: "url:live",
		Source:     "url",
		Generation: 9,
		Meter: core.MeterHomeView{
			Source: core.SourceMeterView{
				Width: 720, Height: 480, FrameRate: 29.97,
				VideoCodec: "h264", AudioCodec: "aac", AudioRate: 48000, AudioChannels: 2,
			},
			Pipeline: core.PipelineMeterView{
				FieldRateHz: 59.94, HorizontalKHz: 15.734, Standard: "ntsc", FieldOrder: "tff",
				AudioSampleRate: 48000, AudioChannels: 2,
			},
			Runtime: core.RuntimeMeterView{
				BlitsTotal: 10, WireBytes: 1_000_000, LastACKAge: 4 * time.Millisecond,
				Generation: 9,
			},
		},
	}
	sampler.Sample(snap, adapters.MeterOverlay{}, false, now)

	liveSnap := snap
	liveSnap.Meter.Runtime.BlitsTotal = 40
	liveSnap.Meter.Runtime.WireBytes = 4_000_000
	liveSnap.Meter.Runtime.LastACKAge = 6 * time.Millisecond
	live := sampler.Sample(liveSnap, adapters.MeterOverlay{}, false, now.Add(500*time.Millisecond))

	pausedSnap := liveSnap
	pausedSnap.State = core.StatePaused
	pausedSnap.Meter.Runtime.BlitsTotal = 400
	pausedSnap.Meter.Runtime.WireBytes = 80_000_000
	pausedSnap.Meter.Runtime.LastACKAge = 99 * time.Millisecond
	paused := sampler.Sample(pausedSnap, adapters.MeterOverlay{}, false, now.Add(time.Second))

	if !paused.Paused {
		t.Fatal("paused sample did not set Paused")
	}
	if paused.SampleSeq != live.SampleSeq {
		t.Fatalf("paused SampleSeq = %d, want frozen %d", paused.SampleSeq, live.SampleSeq)
	}
	if paused.MidRow.ThroughputMBs != live.MidRow.ThroughputMBs || paused.MidRow.MSAck != live.MidRow.MSAck || paused.Readout.Speed != live.Readout.Speed {
		t.Fatalf("paused live display changed: live=%+v paused=%+v", live, paused)
	}
	if len(paused.MidRow.ThroughputHistoryMBs) != len(live.MidRow.ThroughputHistoryMBs) ||
		len(paused.MidRow.AckHistoryMS) != len(live.MidRow.AckHistoryMS) ||
		len(paused.MidRow.ThroughputHistoryMBs) == 0 ||
		len(paused.MidRow.AckHistoryMS) == 0 ||
		paused.MidRow.ThroughputHistoryMBs[0] != live.MidRow.ThroughputHistoryMBs[0] ||
		paused.MidRow.AckHistoryMS[0] != live.MidRow.AckHistoryMS[0] {
		t.Fatalf("paused histories changed: live=%+v paused=%+v", live.MidRow, paused.MidRow)
	}
}

// TestMeterSamplerHoldsDisplayValuesBetweenSampleBoundaries is the
// regression test for the readout flicker: MB/S and MS ACK numbers must
// not drop to their idle defaults ("0.0" / "--") on the live frames that
// fall between 500 ms sample boundaries. The sampler advances at 500 ms
// but meter frames emit at the ~250 ms diff cadence, so a non-boundary
// frame must hold the last real sample rather than reverting.
func TestMeterSamplerHoldsDisplayValuesBetweenSampleBoundaries(t *testing.T) {
	sampler := newMeterSampler()
	base := time.Unix(300, 0)
	snap := core.StatusHomeView{
		State: core.StatePlaying, AdapterRef: "url:live", Source: "url", Generation: 5,
		Meter: core.MeterHomeView{
			Source: core.SourceMeterView{
				Width: 1280, Height: 720, FrameRate: 29.97,
				VideoCodec: "h264", AudioCodec: "aac", AudioRate: 48000, AudioChannels: 2,
			},
			Pipeline: core.PipelineMeterView{
				FieldRateHz: 59.94, HorizontalKHz: 15.734, Standard: "ntsc", FieldOrder: "tff",
				AudioSampleRate: 48000, AudioChannels: 2,
			},
			Runtime: core.RuntimeMeterView{
				BlitsTotal: 100, WireBytes: 1_000_000, LastACKAge: 5 * time.Millisecond, Generation: 5,
			},
		},
	}
	// Prime (generation set; this first call is a non-boundary tick).
	sampler.Sample(snap, adapters.MeterOverlay{}, false, base)
	// Boundary tick 500 ms later: real throughput/ACK computed.
	boundarySnap := snap
	boundarySnap.Meter.Runtime.BlitsTotal = 200
	boundarySnap.Meter.Runtime.WireBytes = 5_000_000
	live := sampler.Sample(boundarySnap, adapters.MeterOverlay{}, false, base.Add(500*time.Millisecond))
	if live.MidRow.ThroughputMBs == "0.0" || live.MidRow.MSAck == "--" {
		t.Fatalf("boundary sample must have real values, got thru=%q ack=%q", live.MidRow.ThroughputMBs, live.MidRow.MSAck)
	}
	// Non-boundary tick 250 ms after the boundary: must HOLD the last real
	// values, not revert to idle defaults.
	held := sampler.Sample(boundarySnap, adapters.MeterOverlay{}, false, base.Add(750*time.Millisecond))
	if held.MidRow.ThroughputMBs != live.MidRow.ThroughputMBs {
		t.Errorf("ThroughputMBs flickered: live=%q held=%q", live.MidRow.ThroughputMBs, held.MidRow.ThroughputMBs)
	}
	if held.MidRow.MSAck != live.MidRow.MSAck {
		t.Errorf("MSAck flickered: live=%q held=%q", live.MidRow.MSAck, held.MidRow.MSAck)
	}
	if held.MidRow.ThroughputSampleMBs != live.MidRow.ThroughputSampleMBs {
		t.Errorf("ThroughputSampleMBs flickered: live=%v held=%v", live.MidRow.ThroughputSampleMBs, held.MidRow.ThroughputSampleMBs)
	}
	if held.MidRow.AckSampleMS != live.MidRow.AckSampleMS {
		t.Errorf("AckSampleMS flickered: live=%v held=%v", live.MidRow.AckSampleMS, held.MidRow.AckSampleMS)
	}
}

func TestMeterSamplerIdleDimsBothStandardLamps(t *testing.T) {
	got := newMeterSampler().Sample(core.StatusHomeView{State: core.StateIdle}, adapters.MeterOverlay{}, false, time.Unix(1, 0))
	if got.MidRow.StandardNTSC || got.MidRow.StandardPAL {
		t.Fatalf("idle standard lamps = NTSC %v PAL %v, want both false", got.MidRow.StandardNTSC, got.MidRow.StandardPAL)
	}
}

func TestMeterOverlayPanicRecoveryKeepsBaseFields(t *testing.T) {
	snap := core.StatusHomeView{State: core.StatePlaying, AdapterRef: "url:1", Source: "url", Generation: 1}
	providers := []namedMeterOverlayProvider{
		{name: "panic", provider: panicMeterOverlayProvider{}},
		{name: "ok", provider: staticMeterOverlayProvider{overlay: adapters.MeterOverlay{HLS: &adapters.HLSMeterOverlay{CachedSegments: 2, MaxCachedSegments: 6}}}},
	}
	got := collectMeterOverlays(context.Background(), providers, snap, newOverlayPanicLimiter())
	if got.HLS == nil || got.HLS.CachedSegments != 2 {
		t.Fatalf("overlay after panic recovery = %+v", got)
	}
}

func TestOverlayPanicLimiterPrunesOldGenerations(t *testing.T) {
	l := newOverlayPanicLimiter()
	l.log("a", 1, "boom")
	l.log("b", 1, "boom")
	if got := len(l.seen); got != 2 {
		t.Fatalf("after gen 1 panics: seen size = %d, want 2", got)
	}
	l.log("a", 2, "boom")
	if got := len(l.seen); got != 1 {
		t.Fatalf("after gen 2 panic: seen size = %d, want 1 (gen 1 entries pruned)", got)
	}
	if _, ok := l.seen[panicKey{name: "a", gen: 2}]; !ok {
		t.Fatal("current-gen entry missing after prune")
	}
}

func TestMeterEnvelopeDoesNotLeakHLSSecrets(t *testing.T) {
	m := idleSnapshot(nonZeroConfig(), time.Unix(1, 0)).Meter
	m.SourceStrip.HLSBuffer = "1 / 6 SEG"
	env := meterEnvelopeFrom(m)
	body, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, leak := range []string{"http://", "https://", "://", "/live.m3u8", "token=", "sig=", "secret", "Authorization"} {
		if strings.Contains(string(body), leak) {
			t.Fatalf("meter envelope leaked %q: %s", leak, body)
		}
	}
}

type panicMeterOverlayProvider struct{}

func (panicMeterOverlayProvider) MeterOverlay(context.Context, core.StatusHomeView) (adapters.MeterOverlay, bool) {
	panic("boom")
}

type staticMeterOverlayProvider struct{ overlay adapters.MeterOverlay }

func (p staticMeterOverlayProvider) MeterOverlay(context.Context, core.StatusHomeView) (adapters.MeterOverlay, bool) {
	return p.overlay, true
}

func TestMeterDataFromSnapshot_AudioScopesLiveDiscoveryHook(t *testing.T) {
	snap := core.StatusHomeView{}
	got := meterDataFromSnapshot(snap, adapters.MeterOverlay{}, nil, nil, true)
	if got.AudioScopes.Status != "live" || got.AudioScopes.Via != "audio" || got.AudioScopes.SampleHz != audioEventHz {
		t.Errorf("AudioScopes = %+v, want Status=live Via=audio SampleHz=%d", got.AudioScopes, audioEventHz)
	}
}

func TestMeterDataFromSnapshot_AudioScopesPendingWhenIdle(t *testing.T) {
	snap := core.StatusHomeView{}
	got := meterDataFromSnapshot(snap, adapters.MeterOverlay{}, nil, nil, false)
	if got.AudioScopes.Status != "pending" || got.AudioScopes.Via != "" || got.AudioScopes.SampleHz != 0 {
		t.Errorf("AudioScopes = %+v, want pending only", got.AudioScopes)
	}
}

func TestMeterEnvelopeFrom_DiscoveryHookSerialization(t *testing.T) {
	m := MeterData{AudioScopes: AudioScopesData{Status: "live", Via: "audio", SampleHz: 30}}
	env := meterEnvelopeFrom(m)
	body, _ := json.Marshal(env)
	if !strings.Contains(string(body), `"audioScopes":{"status":"live","via":"audio","sampleHz":30}`) {
		t.Errorf("envelope shape wrong: %s", body)
	}
}

func TestMeterEnvelopeFrom_PendingShapeExact(t *testing.T) {
	m := MeterData{AudioScopes: AudioScopesData{Status: "pending"}}
	env := meterEnvelopeFrom(m)
	body, _ := json.Marshal(env)
	if !strings.Contains(string(body), `"audioScopes":{"status":"pending"}`) {
		t.Errorf("pending envelope must NOT include via/sampleHz: %s", body)
	}
}
