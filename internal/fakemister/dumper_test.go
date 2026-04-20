package fakemister

import (
	"path/filepath"
	"testing"
)

func TestDumpFieldPNG(t *testing.T) {
	dir := t.TempDir()
	d := NewDumper(dir, 100 /* sample every 100th field */)
	// Create a 720x240 RGB888 red field.
	field := make([]byte, 720*240*3)
	for i := 0; i < len(field); i += 3 {
		field[i] = 255
	}
	for i := 0; i < 100; i++ {
		d.MaybeDumpField(uint32(i), 720, 240, field)
	}
	pngs, _ := filepath.Glob(filepath.Join(dir, "field_*.png"))
	if len(pngs) == 0 {
		t.Fatal("no PNG files written")
	}
}
