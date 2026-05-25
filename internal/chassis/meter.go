package chassis

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

type meterSampler struct {
	prevGeneration uint64
	prevWireBytes  uint64
	prevBlits      uint64
	prevUnderruns  uint64
	prevSampleTime time.Time
	sampleSeq      uint64
	throughput     []float64
	ack            []float64
	lastLive       MeterData
}

func newMeterSampler() *meterSampler {
	return &meterSampler{}
}

func (s *meterSampler) Sample(snap core.StatusHomeView, overlay adapters.MeterOverlay, audioLive bool, now time.Time) MeterData {
	if snap.State != core.StatePlaying && snap.State != core.StatePaused {
		s.reset()
		idle := idleMeterData()
		idle.AudioScopes = audioScopesData(audioLive)
		return idle
	}
	if snap.Generation != s.prevGeneration {
		s.reset()
		s.prevGeneration = snap.Generation
		s.prevWireBytes = snap.Meter.Runtime.WireBytes
		s.prevBlits = snap.Meter.Runtime.BlitsTotal
		s.prevUnderruns = snap.Meter.Runtime.Underruns
		s.prevSampleTime = now
	}
	current := meterDataFromSnapshot(snap, overlay, s.throughput, s.ack, audioLive)
	current.State = string(StateLive)
	current.Paused = snap.State == core.StatePaused
	current.Generation = snap.Generation
	current.SampleSeq = s.sampleSeq
	if current.Paused {
		if s.lastLive.Generation == snap.Generation && s.lastLive.State != "" {
			paused := current
			paused.MidRow.ThroughputSampleMBs = s.lastLive.MidRow.ThroughputSampleMBs
			paused.MidRow.ThroughputMBs = s.lastLive.MidRow.ThroughputMBs
			paused.MidRow.AckSampleMS = s.lastLive.MidRow.AckSampleMS
			paused.MidRow.MSAck = s.lastLive.MidRow.MSAck
			paused.MidRow.ThroughputHistoryMBs = append([]float64(nil), s.lastLive.MidRow.ThroughputHistoryMBs...)
			paused.MidRow.AckHistoryMS = append([]float64(nil), s.lastLive.MidRow.AckHistoryMS...)
			paused.SourceStrip.DropsPercent = s.lastLive.SourceStrip.DropsPercent
			paused.SourceStrip.Drops = s.lastLive.SourceStrip.Drops
			paused.Readout.SpeedRatio = s.lastLive.Readout.SpeedRatio
			paused.Readout.Speed = s.lastLive.Readout.Speed
			paused.SampleSeq = s.lastLive.SampleSeq
			s.lastLive = paused
			return paused
		}
		s.lastLive = current
		return current
	}
	out := current
	if s.prevSampleTime.IsZero() || now.Sub(s.prevSampleTime) >= 500*time.Millisecond {
		elapsed := now.Sub(s.prevSampleTime).Seconds()
		if elapsed > 0 {
			wireDelta := deltaUint64(snap.Meter.Runtime.WireBytes, s.prevWireBytes)
			blitDelta := deltaUint64(snap.Meter.Runtime.BlitsTotal, s.prevBlits)
			underrunDelta := deltaUint64(snap.Meter.Runtime.Underruns, s.prevUnderruns)
			out.MidRow.ThroughputSampleMBs = float64(wireDelta) / elapsed / 1000000
			out.MidRow.ThroughputMBs = formatOneDecimal(out.MidRow.ThroughputSampleMBs)
			out.SourceStrip.DropsPercent = dropPercent(blitDelta, underrunDelta)
			out.SourceStrip.Drops = formatOneDecimal(out.SourceStrip.DropsPercent)
			out.Readout.SpeedRatio = speedRatioForDelta(blitDelta, elapsed, snap.Meter.Pipeline.FieldRateHz)
			out.Readout.Speed = formatSpeed(out.Readout.SpeedRatio)
			if snap.Meter.Runtime.LastACKAge > 0 {
				out.MidRow.AckSampleMS = float64(snap.Meter.Runtime.LastACKAge) / float64(time.Millisecond)
				out.MidRow.MSAck = formatAckMS(out.MidRow.AckSampleMS)
			}
			s.throughput = appendBoundedFloat(s.throughput, out.MidRow.ThroughputSampleMBs, 60)
			if out.MidRow.AckSampleMS > 0 {
				s.ack = appendBoundedFloat(s.ack, out.MidRow.AckSampleMS, 128)
			}
			out.MidRow.ThroughputHistoryMBs = append([]float64(nil), s.throughput...)
			out.MidRow.AckHistoryMS = append([]float64(nil), s.ack...)
			s.sampleSeq++
			out.SampleSeq = s.sampleSeq
		}
		s.prevWireBytes = snap.Meter.Runtime.WireBytes
		s.prevBlits = snap.Meter.Runtime.BlitsTotal
		s.prevUnderruns = snap.Meter.Runtime.Underruns
		s.prevSampleTime = now
	}
	s.lastLive = out
	return out
}

func (s *meterSampler) reset() {
	s.prevGeneration = 0
	s.prevWireBytes = 0
	s.prevBlits = 0
	s.prevUnderruns = 0
	s.prevSampleTime = time.Time{}
	s.throughput = nil
	s.ack = nil
	s.sampleSeq++
	s.lastLive = MeterData{}
}

func idleMeterData() MeterData {
	return idleSnapshot(Config{Version: "meter", StartedAt: time.Unix(0, 0)}, time.Unix(0, 0)).Meter
}

func meterDataFromSnapshot(snap core.StatusHomeView, overlay adapters.MeterOverlay, throughput []float64, ack []float64, audioLive bool) MeterData {
	base := idleMeterData()
	src := snap.Meter.Source
	pipe := snap.Meter.Pipeline
	base.SourceStrip.AudioIn = formatAudioIn(src)
	base.SourceStrip.AudioOut = formatAudioOut(pipe.AudioSampleRate, pipe.AudioChannels)
	base.SourceStrip.Src = formatSource(src)
	base.SourceStrip.Crop = formatCrop(src, snap.Meter.Crop)
	base.SourceStrip.BlitsTotal = snap.Meter.Runtime.BlitsTotal
	base.SourceStrip.UnderrunsTotal = snap.Meter.Runtime.Underruns
	base.MidRow.BitrateMbps = formatBitrate(src, overlay)
	base.MidRow.FreqKHz = formatKHzFrequency(pipe.HorizontalKHz)
	base.MidRow.Mode = formatMode(pipe)
	base.MidRow.Standard = pipe.Standard
	base.MidRow.StandardNTSC = pipe.Standard == "ntsc"
	base.MidRow.StandardPAL = pipe.Standard == "pal"
	base.MidRow.FieldOrder = pipe.FieldOrder
	base.MidRow.FieldRateHz = pipe.FieldRateHz
	base.MidRow.InterlacedOutput = pipe.InterlacedOutput
	base.MidRow.FieldLock = formatFieldLock(pipe)
	base.MidRow.FieldFlip = base.MidRow.FieldLock
	base.MidRow.ThroughputHistoryMBs = append([]float64(nil), throughput...)
	base.MidRow.AckHistoryMS = append([]float64(nil), ack...)
	base.Readout.Output = formatOutput(pipe)
	base.Readout.Aspect = formatAspect(src, snap.Meter.Crop)
	base.Readout.Pipe = formatPipe(pipe)
	base.Readout.SpeedRatio = 1.0
	base.Readout.Speed = formatSpeed(base.Readout.SpeedRatio)
	base.Readout.Link = formatLink(snap.Meter.Runtime.LastACKAge)
	base.AudioScopes = audioScopesData(audioLive)
	if overlay.HLS != nil {
		h := overlay.HLS
		base.SourceStrip.HLSCachedSegments = h.CachedSegments
		base.SourceStrip.HLSMaxSegments = h.MaxCachedSegments
		base.SourceStrip.HLSCacheBytes = h.CacheBytes
		base.SourceStrip.HLSBuffer = fmt.Sprintf("%d / %d SEG", h.CachedSegments, h.MaxCachedSegments)
	}
	return base
}

// audioScopesData builds the discovery-hook AudioScopesData. When
// audioLive is true, advertises the high-rate audio event via the
// hook fields; otherwise returns pending. audioEventHz is the
// constant defined in events.go (Task 10).
func audioScopesData(audioLive bool) AudioScopesData {
	if audioLive {
		return AudioScopesData{
			Status:   "live",
			Via:      "audio",
			SampleHz: audioEventHz,
		}
	}
	return AudioScopesData{Status: "pending"}
}

func deltaUint64(now, prev uint64) uint64 {
	if now < prev {
		return 0
	}
	return now - prev
}

func dropPercent(deltaBlits, deltaUnderruns uint64) float64 {
	if deltaBlits == 0 {
		return 0
	}
	return 100 * float64(deltaUnderruns) / float64(deltaBlits)
}

func appendBoundedFloat(values []float64, v float64, max int) []float64 {
	values = append(values, v)
	if len(values) > max {
		values = values[len(values)-max:]
	}
	return values
}

func formatOneDecimal(v float64) string {
	if v <= 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return "0.0"
	}
	return fmt.Sprintf("%.1f", v)
}

func formatKHzFrequency(v float64) string {
	if v <= 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return "---"
	}
	return fmt.Sprintf("%.1f", v)
}

func formatAckMS(v float64) string {
	if v <= 0 {
		return "--"
	}
	return fmt.Sprintf("%04.1f", v)
}

func formatAudioIn(src core.SourceMeterView) string {
	if src.AudioCodec == "" && src.AudioChannels == 0 {
		return "---"
	}
	return strings.TrimSpace(formatCodec(src.AudioCodec) + " - " + formatChannels(src.AudioChannels))
}

func formatAudioOut(rate, channels int) string {
	if rate <= 0 || channels <= 0 {
		return "---"
	}
	return fmt.Sprintf("S16LE - %s - %s", formatKHz(rate), formatChannels(channels))
}

func formatCodec(codec string) string {
	switch strings.ToLower(strings.TrimSpace(codec)) {
	case "h264":
		return "H.264"
	case "aac":
		return "AAC"
	case "aac_lc":
		return "AAC LC"
	case "mp3":
		return "MP3"
	case "opus":
		return "OPUS"
	default:
		if codec == "" {
			return "---"
		}
		return strings.ToUpper(codec)
	}
}

func formatChannels(ch int) string {
	switch ch {
	case 1:
		return "MONO"
	case 2:
		return "STEREO"
	default:
		return "---"
	}
}

func formatKHz(rate int) string {
	if rate%1000 == 0 {
		return fmt.Sprintf("%dk", rate/1000)
	}
	return fmt.Sprintf("%.1fk", float64(rate)/1000)
}

func formatSource(src core.SourceMeterView) string {
	if src.Width <= 0 || src.Height <= 0 {
		return "---"
	}
	codec := formatCodec(src.VideoCodec)
	if codec == "---" {
		codec = "VIDEO"
	}
	return fmt.Sprintf("%dx%d@%s - %s", src.Width, src.Height, formatFrameRate(src.FrameRate), codec)
}

func formatFrameRate(rate float64) string {
	if rate <= 0 {
		return "--"
	}
	if math.Abs(rate-math.Round(rate)) < 0.01 {
		return fmt.Sprintf("%.0f", math.Round(rate))
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", rate), "0"), ".")
}

func formatCrop(src core.SourceMeterView, crop core.CropMeterView) string {
	if src.Width <= 0 || src.Height <= 0 {
		return "---"
	}
	aspect := aspectLabel(src)
	mode := strings.ToUpper(strings.TrimSpace(crop.Mode))
	if mode == "" {
		mode = "NATIVE"
	}
	if !crop.Detected {
		return fmt.Sprintf("NONE - %s %s", aspect, mode)
	}
	return fmt.Sprintf("%dx%d+%d+%d - %s %s", crop.W, crop.H, crop.X, crop.Y, aspect, mode)
}

func formatAspect(src core.SourceMeterView, crop core.CropMeterView) string {
	if src.Width <= 0 || src.Height <= 0 {
		return "---"
	}
	mode := strings.ToUpper(strings.TrimSpace(crop.Mode))
	if mode == "" {
		mode = "NATIVE"
	}
	return fmt.Sprintf("%s %s", aspectLabel(src), mode)
}

func aspectLabel(src core.SourceMeterView) string {
	n, d := src.DisplayAspectRatioNum, src.DisplayAspectRatioDen
	if n == 0 || d == 0 {
		n, d = src.SampleAspectRatioNum*src.Width, src.SampleAspectRatioDen*src.Height
	}
	if n == 0 || d == 0 {
		n, d = src.Width, src.Height
	}
	if d == 0 {
		return "---"
	}
	ratio := float64(n) / float64(d)
	switch {
	case math.Abs(ratio-(4.0/3.0)) < 0.04:
		return "4:3"
	case math.Abs(ratio-(16.0/9.0)) < 0.04:
		return "16:9"
	default:
		return fmt.Sprintf("%d:%d", n, d)
	}
}

func formatBitrate(src core.SourceMeterView, overlay adapters.MeterOverlay) string {
	bps := src.FormatBitrateBPS
	if bps == 0 {
		bps = src.VideoBitrateBPS + src.AudioBitrateBPS
	}
	if overlay.HLS != nil && overlay.HLS.SelectedVariantBPS > 0 {
		bps = overlay.HLS.SelectedVariantBPS
	}
	if bps <= 0 {
		return "---"
	}
	return fmt.Sprintf("%.1f", float64(bps)/1000000)
}

func formatMode(pipe core.PipelineMeterView) string {
	if pipe.OutputWidth == 720 && (pipe.OutputHeight == 480 || pipe.OutputHeight == 576) {
		return "704"
	}
	if pipe.OutputWidth > 0 {
		return fmt.Sprintf("%d", pipe.OutputWidth)
	}
	return "---"
}

func formatFieldLock(pipe core.PipelineMeterView) string {
	if !pipe.InterlacedOutput {
		return "PROG"
	}
	if pipe.FieldOrder == "" {
		return "LOCK"
	}
	return strings.ToUpper(pipe.FieldOrder) + " LOCK"
}

func formatOutput(pipe core.PipelineMeterView) string {
	if pipe.OutputHeight <= 0 {
		return "---"
	}
	if pipe.InterlacedOutput {
		return fmt.Sprintf("INTERLACE %di - BT.601", pipe.OutputHeight)
	}
	return fmt.Sprintf("PROGRESSIVE %dp - BT.601", pipe.OutputHeight)
}

func formatPipe(pipe core.PipelineMeterView) string {
	codec := "RAW"
	switch {
	case pipe.LZ4Enabled && pipe.DeltaLZ4Enabled:
		codec = "LZ4+D"
	case pipe.LZ4Enabled:
		codec = "LZ4"
	}
	order := strings.ToUpper(pipe.FieldOrder)
	if order == "" {
		order = "PROG"
	}
	return codec + " - " + order
}

func speedRatioForDelta(deltaBlits uint64, elapsedSeconds float64, expectedFieldRate float64) float64 {
	if elapsedSeconds <= 0 || expectedFieldRate <= 0 {
		return 1
	}
	return (float64(deltaBlits) / elapsedSeconds) / expectedFieldRate
}

func formatSpeed(ratio float64) string {
	if ratio <= 0 || math.IsNaN(ratio) || math.IsInf(ratio, 0) {
		return "---"
	}
	state := "LOCK"
	if ratio < 0.98 {
		state = "SLOW"
	}
	if ratio > 1.02 {
		state = "FAST"
	}
	return fmt.Sprintf("%.2fx %s", ratio, state)
}

func formatLink(ack time.Duration) string {
	if ack <= 0 {
		return "MiSTer - --"
	}
	return fmt.Sprintf("MiSTer - %.0fms", float64(ack)/float64(time.Millisecond))
}

type onePerSecondLimiter struct {
	mu   sync.Mutex
	last time.Time
}

func (l *onePerSecondLimiter) Allow(now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.last.IsZero() || now.Sub(l.last) >= time.Second {
		l.last = now
		return true
	}
	return false
}

type namedMeterOverlayProvider struct {
	name     string
	provider adapters.MeterOverlayProvider
}

type overlayPanicLimiter struct {
	mu      sync.Mutex
	lastGen uint64
	seen    map[panicKey]struct{}
}

type panicKey struct {
	name string
	gen  uint64
}

func newOverlayPanicLimiter() *overlayPanicLimiter {
	return &overlayPanicLimiter{seen: map[panicKey]struct{}{}}
}

// log records one panic per (provider, generation) and emits a warning
// the first time each pair is seen. On generation change, prior-generation
// entries are dropped so the map stays bounded by the active provider set.
func (l *overlayPanicLimiter) log(name string, generation uint64, err any) {
	key := panicKey{name: name, gen: generation}
	l.mu.Lock()
	if generation != l.lastGen {
		for k := range l.seen {
			if k.gen != generation {
				delete(l.seen, k)
			}
		}
		l.lastGen = generation
	}
	_, seen := l.seen[key]
	if !seen {
		l.seen[key] = struct{}{}
	}
	l.mu.Unlock()
	if !seen {
		slog.Warn("chassis: meter overlay panic", "provider", name, "err", err)
	}
}

func (s *Server) meterOverlayProviders() []namedMeterOverlayProvider {
	if s.cfg.Registry == nil {
		return nil
	}
	var out []namedMeterOverlayProvider
	for _, a := range s.cfg.Registry.List() {
		if p, ok := a.(adapters.MeterOverlayProvider); ok {
			out = append(out, namedMeterOverlayProvider{name: a.Name(), provider: p})
		}
	}
	return out
}

func (s *Server) collectMeterOverlay(ctx context.Context, snap core.StatusHomeView) adapters.MeterOverlay {
	return collectMeterOverlays(ctx, s.meterOverlayProviders(), snap, s.overlayPanics)
}

func collectMeterOverlays(ctx context.Context, providers []namedMeterOverlayProvider, snap core.StatusHomeView, limiter *overlayPanicLimiter) adapters.MeterOverlay {
	var out adapters.MeterOverlay
	for _, item := range providers {
		overlay, ok := callMeterOverlayProvider(ctx, item, snap, limiter)
		if !ok {
			continue
		}
		if overlay.HLS != nil && out.HLS == nil {
			out.HLS = overlay.HLS
		}
	}
	return out
}

func callMeterOverlayProvider(ctx context.Context, item namedMeterOverlayProvider, snap core.StatusHomeView, limiter *overlayPanicLimiter) (overlay adapters.MeterOverlay, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			if limiter != nil {
				limiter.log(item.name, snap.Generation, r)
			}
			overlay = adapters.MeterOverlay{}
			ok = false
		}
	}()
	return item.provider.MeterOverlay(ctx, snap)
}

// meterEnvelope is the JSON wire format for the `meter` SSE event. Task
// 8 emits this from handleEvents; defined here so the formatter and
// envelope live next to one another. The shape must never include URLs,
// tokens, or credentials sourced from HLS playlists or origin servers.
type meterEnvelope struct {
	State       string            `json:"state"`
	Paused      bool              `json:"paused"`
	Generation  uint64            `json:"generation"`
	SourceStrip meterSourceStripE `json:"sourceStrip"`
	MidRow      meterMidRowE      `json:"midRow"`
	Readout     meterReadoutE     `json:"readout"`
	AudioScopes meterAudioScopesE `json:"audioScopes"`
}

type meterSourceStripE struct {
	AudioIn           string  `json:"audioIn"`
	AudioOut          string  `json:"audioOut"`
	Src               string  `json:"src"`
	Crop              string  `json:"crop"`
	HLSBuffer         string  `json:"hlsBuffer"`
	HLSCachedSegments int     `json:"hlsCachedSegments"`
	HLSMaxSegments    int     `json:"hlsMaxSegments"`
	HLSCacheBytes     int64   `json:"hlsCacheBytes"`
	Drops             string  `json:"drops"`
	DropsPercent      float64 `json:"dropsPercent"`
	BlitsTotal        uint64  `json:"blitsTotal"`
	UnderrunsTotal    uint64  `json:"underrunsTotal"`
}

type meterMidRowE struct {
	BitrateMbps          string    `json:"bitrateMbps"`
	FreqKHz              string    `json:"freqKHz"`
	Mode                 string    `json:"mode"`
	Standard             string    `json:"standard"`
	FieldOrder           string    `json:"fieldOrder"`
	FieldRateHz          float64   `json:"fieldRateHz"`
	InterlacedOutput     bool      `json:"interlacedOutput"`
	FieldLock            string    `json:"fieldLock"`
	ThroughputMBs        string    `json:"throughputMBs"`
	ThroughputSampleMBs  float64   `json:"throughputSampleMBs"`
	ThroughputHistoryMBs []float64 `json:"throughputHistoryMBs"`
	AckMS                string    `json:"ackMS"`
	AckSampleMS          float64   `json:"ackSampleMS"`
	AckHistoryMS         []float64 `json:"ackHistoryMS"`
}

type meterReadoutE struct {
	Output     string  `json:"output"`
	Aspect     string  `json:"aspect"`
	Pipe       string  `json:"pipe"`
	Speed      string  `json:"speed"`
	SpeedRatio float64 `json:"speedRatio"`
	Link       string  `json:"link"`
}

type meterAudioScopesE struct {
	Status   string `json:"status"`
	Via      string `json:"via,omitempty"`
	SampleHz int    `json:"sampleHz,omitempty"`
}

func meterEnvelopeFrom(m MeterData) meterEnvelope {
	return meterEnvelope{
		State:      m.State,
		Paused:     m.Paused,
		Generation: m.Generation,
		SourceStrip: meterSourceStripE{
			AudioIn:           m.SourceStrip.AudioIn,
			AudioOut:          m.SourceStrip.AudioOut,
			Src:               m.SourceStrip.Src,
			Crop:              m.SourceStrip.Crop,
			HLSBuffer:         m.SourceStrip.HLSBuffer,
			HLSCachedSegments: m.SourceStrip.HLSCachedSegments,
			HLSMaxSegments:    m.SourceStrip.HLSMaxSegments,
			HLSCacheBytes:     m.SourceStrip.HLSCacheBytes,
			Drops:             m.SourceStrip.Drops,
			DropsPercent:      m.SourceStrip.DropsPercent,
			BlitsTotal:        m.SourceStrip.BlitsTotal,
			UnderrunsTotal:    m.SourceStrip.UnderrunsTotal,
		},
		MidRow: meterMidRowE{
			BitrateMbps:          m.MidRow.BitrateMbps,
			FreqKHz:              m.MidRow.FreqKHz,
			Mode:                 m.MidRow.Mode,
			Standard:             m.MidRow.Standard,
			FieldOrder:           m.MidRow.FieldOrder,
			FieldRateHz:          m.MidRow.FieldRateHz,
			InterlacedOutput:     m.MidRow.InterlacedOutput,
			FieldLock:            m.MidRow.FieldLock,
			ThroughputMBs:        m.MidRow.ThroughputMBs,
			ThroughputSampleMBs:  m.MidRow.ThroughputSampleMBs,
			ThroughputHistoryMBs: m.MidRow.ThroughputHistoryMBs,
			AckMS:                m.MidRow.MSAck,
			AckSampleMS:          m.MidRow.AckSampleMS,
			AckHistoryMS:         m.MidRow.AckHistoryMS,
		},
		Readout: meterReadoutE{
			Output:     m.Readout.Output,
			Aspect:     m.Readout.Aspect,
			Pipe:       m.Readout.Pipe,
			Speed:      m.Readout.Speed,
			SpeedRatio: m.Readout.SpeedRatio,
			Link:       m.Readout.Link,
		},
		AudioScopes: meterAudioScopesE{
			Status:   m.AudioScopes.Status,
			Via:      m.AudioScopes.Via,
			SampleHz: m.AudioScopes.SampleHz,
		},
	}
}
