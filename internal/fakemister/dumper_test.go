package fakemister

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDumpFieldPNG(t *testing.T) {
	dir := t.TempDir()
	d := NewDumper(dir, 100 /* sample every 100th field */)
	// Create a 720x240 BGR24 red field.
	field := make([]byte, 720*240*3)
	for i := 0; i < len(field); i += 3 {
		field[i+2] = 255
	}
	for i := 0; i < 100; i++ {
		d.MaybeDumpField(uint32(i), 720, 240, field)
	}
	pngs, _ := filepath.Glob(filepath.Join(dir, "field_*.png"))
	if len(pngs) == 0 {
		t.Fatal("no PNG files written")
	}
}

func TestDumpFieldPNG_SamplesByDecodedFieldCountNotFrameID(t *testing.T) {
	dir := t.TempDir()
	d := NewDumper(dir, 3)
	field := make([]byte, 4*2*3)
	for i := 0; i < len(field); i += 3 {
		field[i+2] = 255
	}

	// None of these transport frame ids are multiples of 3, so the old
	// frame-id-based sampler would have written nothing. The decoded-field
	// counter should still dump on the third call.
	for _, frame := range []uint32{1, 5, 7} {
		if err := d.MaybeDumpField(frame, 4, 2, field); err != nil {
			t.Fatalf("MaybeDumpField(%d): %v", frame, err)
		}
	}

	pngs, _ := filepath.Glob(filepath.Join(dir, "field_*.png"))
	if len(pngs) != 1 {
		t.Fatalf("expected exactly one sampled PNG, got %d", len(pngs))
	}
	if !strings.HasSuffix(pngs[0], "field_00000007.png") {
		t.Fatalf("expected sampled PNG to use the third call's frame id, got %s", pngs[0])
	}
}

func TestDumpFieldPNGRejectsWrongPayloadSize(t *testing.T) {
	dir := t.TempDir()
	d := NewDumper(dir, 1)

	err := d.MaybeDumpField(0, 720, 480, make([]byte, 720*240*3))
	if err == nil {
		t.Fatal("expected payload size mismatch error")
	}
	if !strings.Contains(err.Error(), "invalid field payload size") {
		t.Fatalf("unexpected error: %v", err)
	}
}
