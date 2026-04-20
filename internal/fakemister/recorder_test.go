package fakemister

import (
	"testing"

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
