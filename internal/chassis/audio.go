package chassis

import (
	"bytes"
	"math"
	"strconv"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// AudioScopeViewer is the read-only source for the latest audio-analysis
// snapshot. *core.Manager satisfies this structurally via AudioScopes().
// Tests use fakes. When the viewer is nil or returns nil, the chassis
// emits a pending audio frame. The returned pointer is read-only.
type AudioScopeViewer interface {
	AudioScopes() *core.AudioScopeSnapshot
}

// audioPendingEnvelope is the SSE payload for the `audio` event when no
// session is active. Encodes as {"status":"pending"} exactly.
type audioPendingEnvelope struct {
	Status string `json:"status"`
}

// audioLiveEnvelope is the SSE payload for the `audio` event when a
// session is active. Encoded via a custom MarshalJSON to pin float
// precision (5 sig figs, float32) and clamp NaN/Inf to safe values
// before serialization. No `omitempty` — every field is always emitted,
// including legitimate zeros (phaseCorr: 0.0 for uncorrelated stereo).
type audioLiveEnvelope struct {
	Status     string
	Generation uint64
	SampleRate int
	Channels   int
	VU         vuEnvelope
	PhaseCorr  float32
	LUFSShort  float32
	Spectrum   []float32
	Goniometer [][2]float32
}

type vuEnvelope struct {
	Left  channelLevel `json:"left"`
	Right channelLevel `json:"right"`
}

type channelLevel struct {
	Peak float32 `json:"peak"`
	RMS  float32 `json:"rms"`
}

// audioEnvelopeFromViewer returns *audioPendingEnvelope or
// *audioLiveEnvelope based on the viewer's current snapshot.
func audioEnvelopeFromViewer(v AudioScopeViewer) any {
	if v == nil {
		return &audioPendingEnvelope{Status: "pending"}
	}
	snap := v.AudioScopes()
	if snap == nil || snap.Generation == 0 {
		return &audioPendingEnvelope{Status: "pending"}
	}
	return &audioLiveEnvelope{
		Status:     "live",
		Generation: snap.Generation,
		SampleRate: snap.SampleRate,
		Channels:   snap.Channels,
		VU: vuEnvelope{
			Left:  channelLevel{Peak: snap.Peak[0], RMS: snap.RMS[0]},
			Right: channelLevel{Peak: snap.Peak[1], RMS: snap.RMS[1]},
		},
		PhaseCorr:  snap.PhaseCorr,
		LUFSShort:  snap.LUFSShort,
		Spectrum:   snap.SpectrumBands[:],
		Goniometer: snap.Goniometer[:],
	}
}

// audioShouldEmit is the single source of truth for "should I emit?".
// Always emits live frames (preserves 30 Hz cadence even on identical
// payloads). Suppresses only pending→pending so idle wire is quiet.
func audioShouldEmit(prev, curr any) bool {
	_, prevPending := prev.(*audioPendingEnvelope)
	_, currPending := curr.(*audioPendingEnvelope)
	if prevPending && currPending {
		return false
	}
	return true
}

// MarshalJSON pins float formatting and clamps NaN/Inf at the
// serialization boundary. This is the single chokepoint for those
// policies; do not duplicate the logic elsewhere.
func (e *audioLiveEnvelope) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	buf.WriteString(`"status":"`)
	buf.WriteString(e.Status)
	buf.WriteString(`",`)
	buf.WriteString(`"generation":`)
	buf.WriteString(strconv.FormatUint(e.Generation, 10))
	buf.WriteByte(',')
	buf.WriteString(`"sampleRate":`)
	buf.WriteString(strconv.Itoa(e.SampleRate))
	buf.WriteByte(',')
	buf.WriteString(`"channels":`)
	buf.WriteString(strconv.Itoa(e.Channels))
	buf.WriteByte(',')
	buf.WriteString(`"vu":{"left":{"peak":`)
	writeFloat(&buf, e.VU.Left.Peak, peakClamp)
	buf.WriteString(`,"rms":`)
	writeFloat(&buf, e.VU.Left.RMS, peakClamp)
	buf.WriteString(`},"right":{"peak":`)
	writeFloat(&buf, e.VU.Right.Peak, peakClamp)
	buf.WriteString(`,"rms":`)
	writeFloat(&buf, e.VU.Right.RMS, peakClamp)
	buf.WriteString(`}},`)
	buf.WriteString(`"phaseCorr":`)
	writeFloat(&buf, e.PhaseCorr, corrClamp)
	buf.WriteByte(',')
	buf.WriteString(`"lufsShort":`)
	writeFloat(&buf, e.LUFSShort, lufsClamp)
	buf.WriteByte(',')
	buf.WriteString(`"spectrum":[`)
	for i, v := range e.Spectrum {
		if i > 0 {
			buf.WriteByte(',')
		}
		writeFloat(&buf, v, spectrumClamp)
	}
	buf.WriteString(`],"goniometer":[`)
	for i, pair := range e.Goniometer {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.WriteByte('[')
		writeFloat(&buf, pair[0], peakClamp)
		buf.WriteByte(',')
		writeFloat(&buf, pair[1], peakClamp)
		buf.WriteByte(']')
	}
	buf.WriteString(`]}`)
	return buf.Bytes(), nil
}

type clampPolicy func(float32) float32

// peakClamp covers both peak (signed amplitude [-1, +1]) and RMS
// (non-negative [0, +1]) fields. RMS can never legitimately go
// negative, so the x < 0 branch is unreachable for RMS but kept for
// symmetry with peak. If the spec ever distinguishes peak from RMS
// in terms of clamp behavior, split this into a dedicated rmsClamp
// returning 0.0 for any negative or NaN input.
func peakClamp(x float32) float32 {
	if isBadFloat32(x) {
		if x > 0 {
			return 1.0
		}
		if x < 0 {
			return -1.0
		}
		return 0
	}
	return x
}

func corrClamp(x float32) float32 {
	if isBadFloat32(x) {
		if x > 0 {
			return 1.0
		}
		if x < 0 {
			return -1.0
		}
		return 0
	}
	return x
}

func lufsClamp(x float32) float32 {
	if isBadFloat32(x) {
		return -100.0
	}
	return x
}

func spectrumClamp(x float32) float32 {
	if isBadFloat32(x) {
		return -90.0
	}
	return x
}

func isBadFloat32(x float32) bool {
	x64 := float64(x)
	return math.IsNaN(x64) || math.IsInf(x64, 0)
}

func writeFloat(buf *bytes.Buffer, x float32, clamp clampPolicy) {
	v := clamp(x)
	buf.WriteString(strconv.FormatFloat(float64(v), 'g', 5, 32))
}
