package fakemister

import (
	"bytes"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)

func TestRecorder_Counts(t *testing.T) {
	r := NewRecorder()
	r.Record(Command{Type: groovy.CmdInit})
	r.Record(Command{Type: groovy.CmdSwitchres})
	r.Record(Command{Type: groovy.CmdBlitFieldVSync, Blit: &BlitHeader{}})
	r.Record(Command{Type: groovy.CmdBlitFieldVSync, Blit: &BlitHeader{}})
	// AudioPayload carries reassembled PCM bytes; AudioHeader carries the
	// 3-byte-header soundSize metadata. The recorder counts reassembled bytes.
	r.Record(Command{Type: groovy.CmdAudio, AudioPayload: &AudioPayload{PCM: []byte{0, 0}}})
	r.Record(Command{Type: groovy.CmdClose})

	snap := r.Snapshot()
	if snap.Counts[groovy.CmdBlitFieldVSync] != 2 {
		t.Errorf("blit count = %d, want 2", snap.Counts[groovy.CmdBlitFieldVSync])
	}
	if snap.AudioBytes != 2 {
		t.Errorf("audio bytes = %d", snap.AudioBytes)
	}
}

func TestRecorder_CapturesBlitFields(t *testing.T) {
	r := NewRecorder()
	// Synthesize 4 blit commands with alternating fields (interlaced
	// pattern: top, bottom, top, bottom).
	for i := 0; i < 4; i++ {
		r.Record(Command{
			Type: groovy.CmdBlitFieldVSync,
			Blit: &BlitHeader{
				Frame: uint32(i),
				Field: uint8(i % 2),
				VSync: 0,
			},
			ReceivedAt: time.Now(),
		})
	}
	snap := r.Snapshot()
	if len(snap.BlitFields) != 4 {
		t.Fatalf("BlitFields length = %d, want 4", len(snap.BlitFields))
	}
	want := []uint8{0, 1, 0, 1}
	for i := 0; i < 4; i++ {
		if snap.BlitFields[i] != want[i] {
			t.Errorf("BlitFields[%d] = %d, want %d", i, snap.BlitFields[i], want[i])
		}
	}
}

func TestRecorder_BlitFieldsForProgressivePreset(t *testing.T) {
	r := NewRecorder()
	for i := 0; i < 6; i++ {
		r.Record(Command{
			Type: groovy.CmdBlitFieldVSync,
			Blit: &BlitHeader{Frame: uint32(i), Field: 0, VSync: 0},
		})
	}
	snap := r.Snapshot()
	for i, f := range snap.BlitFields {
		if f != 0 {
			t.Errorf("progressive run: BlitFields[%d] = %d, want 0", i, f)
		}
	}
}

func TestRecorder_CapturesSwitchresRaw(t *testing.T) {
	r := NewRecorder()
	// Synthesize a SWITCHRES command with a known wire payload.
	want := groovy.BuildSwitchres(groovy.NTSC480i60)
	r.Record(Command{
		Type:      groovy.CmdSwitchres,
		Raw:       want,
		Switchres: &SwitchresPayload{}, // payload contents irrelevant for raw-bytes test
	})
	snap := r.Snapshot()
	if len(snap.SwitchresRaw) != 1 {
		t.Fatalf("SwitchresRaw length = %d, want 1", len(snap.SwitchresRaw))
	}
	if !bytes.Equal(snap.SwitchresRaw[0], want) {
		t.Errorf("SwitchresRaw[0] = %x, want %x", snap.SwitchresRaw[0], want)
	}
}
