package fakemister

import (
	"bytes"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)

func TestReassembler_CompleteField(t *testing.T) {
	src := make([]byte, 518400)
	for i := range src {
		src[i] = byte(i % 256)
	}
	r := NewReassembler(uint32(len(src)))
	// Feed in chunks of 1472.
	for i := 0; i < len(src); i += groovy.MaxDatagram {
		end := i + groovy.MaxDatagram
		if end > len(src) {
			end = len(src)
		}
		done := r.Write(src[i:end])
		if i+groovy.MaxDatagram >= len(src) {
			if !done {
				t.Errorf("expected done after last chunk at i=%d", i)
			}
		} else {
			if done {
				t.Errorf("done prematurely at i=%d", i)
			}
		}
	}
	if !bytes.Equal(src, r.Bytes()) {
		t.Error("reassembled payload mismatch")
	}
}

func TestReassembler_Overflow(t *testing.T) {
	r := NewReassembler(10)
	r.Write([]byte{1, 2, 3, 4, 5})
	// Next write pushes past expected size — reassembler should reject.
	if !r.Write([]byte{6, 7, 8, 9, 10, 11}) {
		t.Error("expected done=true when we hit expected size")
	}
	if len(r.Bytes()) != 10 {
		t.Errorf("truncated len = %d, want 10", len(r.Bytes()))
	}
}
