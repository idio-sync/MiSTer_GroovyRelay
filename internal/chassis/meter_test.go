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
	first := sampler.Sample(snap, adapters.MeterOverlay{}, now)
	secondSnap := snap
	secondSnap.Meter.Runtime.BlitsTotal = 130
	secondSnap.Meter.Runtime.Underruns = 3
	secondSnap.Meter.Runtime.WireBytes = 8_000_000
	got := sampler.Sample(secondSnap, adapters.MeterOverlay{HLS: &adapters.HLSMeterOverlay{
		CachedSegments: 6, MaxCachedSegments: 12, CacheBytes: 1234, SelectedVariantBPS: 2600000,
	}}, now.Add(500*time.Millisecond))

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
	sampler.Sample(snap, adapters.MeterOverlay{}, now)

	liveSnap := snap
	liveSnap.Meter.Runtime.BlitsTotal = 40
	liveSnap.Meter.Runtime.WireBytes = 4_000_000
	liveSnap.Meter.Runtime.LastACKAge = 6 * time.Millisecond
	live := sampler.Sample(liveSnap, adapters.MeterOverlay{}, now.Add(500*time.Millisecond))

	pausedSnap := liveSnap
	pausedSnap.State = core.StatePaused
	pausedSnap.Meter.Runtime.BlitsTotal = 400
	pausedSnap.Meter.Runtime.WireBytes = 80_000_000
	pausedSnap.Meter.Runtime.LastACKAge = 99 * time.Millisecond
	paused := sampler.Sample(pausedSnap, adapters.MeterOverlay{}, now.Add(time.Second))

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

func TestMeterSamplerIdleDimsBothStandardLamps(t *testing.T) {
	got := newMeterSampler().Sample(core.StatusHomeView{State: core.StateIdle}, adapters.MeterOverlay{}, time.Unix(1, 0))
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
