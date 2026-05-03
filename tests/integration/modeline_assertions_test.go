//go:build integration

package integration

import (
	"bytes"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/fakemister"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)

// assertSwitchresMatches asserts that at least one SWITCHRES wire-byte
// payload in snap.SwitchresRaw matches want byte-for-byte. Reused by
// modeline_ntsc240p_test.go, modeline_pal576i_test.go,
// modeline_pal288p_test.go.
func assertSwitchresMatches(t *testing.T, snap fakemister.RecorderSnapshot, want []byte, presetName string) {
	t.Helper()
	if len(snap.SwitchresRaw) == 0 {
		t.Errorf("%s: no SWITCHRES commands captured", presetName)
		return
	}
	for _, got := range snap.SwitchresRaw {
		if bytes.Equal(got, want) {
			return
		}
	}
	t.Errorf("%s: no SWITCHRES payload matched expected wire bytes\n  want: %x\n  got:  %x",
		presetName, want, snap.SwitchresRaw[len(snap.SwitchresRaw)-1])
}

func assertAudioBytesTrackBlits(t *testing.T, snap fakemister.RecorderSnapshot, ml groovy.Modeline, presetName string) {
	t.Helper()
	gotBlits := snap.Counts[groovy.CmdBlitFieldVSync]
	if gotBlits <= 0 {
		return
	}
	rateNumer, rateDenom := ml.FieldRateRatio()
	if rateNumer <= 0 || rateDenom <= 0 {
		t.Fatalf("%s: invalid field rate ratio %d/%d", presetName, rateNumer, rateDenom)
	}

	const (
		sampleRate     = 48000
		channels       = 2
		bytesPerSample = 2
	)
	wantFrames := int64(gotBlits) * sampleRate * rateDenom / rateNumer
	wantBytes := wantFrames * channels * bytesPerSample
	margin := wantBytes / 10
	if margin < channels*bytesPerSample {
		margin = channels * bytesPerSample
	}
	lo, hi := wantBytes-margin, wantBytes+margin
	if got := int64(snap.AudioBytes); got < lo || got > hi {
		t.Errorf("%s: audio bytes = %d, want %d±10%% for %d blits [%d, %d]",
			presetName, snap.AudioBytes, wantBytes, gotBlits, lo, hi)
	}
}
