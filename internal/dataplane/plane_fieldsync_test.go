package dataplane

import (
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)

func TestNextBlitState_InterlacedMatchesUpstreamFieldFormula(t *testing.T) {
	p := &Plane{cfg: PlaneConfig{Modeline: groovy.NTSC480i60}}

	cases := []struct {
		name      string
		prevFrame uint32
		fpgaFrame uint32
		vgaField  bool
		wantFrame uint32
		wantField uint8
	}{
		{
			name:      "first field starts on top parity",
			prevFrame: 0,
			fpgaFrame: 0,
			vgaField:  false,
			wantFrame: 1,
			wantField: 0,
		},
		{
			name:      "alternates with reported fpga field",
			prevFrame: 1,
			fpgaFrame: 1,
			vgaField:  true,
			wantFrame: 2,
			wantField: 1,
		},
		{
			name:      "stays in phase on later top field",
			prevFrame: 2,
			fpgaFrame: 2,
			vgaField:  false,
			wantFrame: 3,
			wantField: 0,
		},
		{
			name:      "catches up when fpga is several blits ahead",
			prevFrame: 1,
			fpgaFrame: 4,
			vgaField:  false,
			wantFrame: 5,
			wantField: 0,
		},
		{
			name:      "when fpga is ahead by one the upstream sender reuses that frame id",
			prevFrame: 4,
			fpgaFrame: 5,
			vgaField:  true,
			wantFrame: 5,
			wantField: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p.fpgaFrame.Store(tc.fpgaFrame)
			p.vgaField.Store(tc.vgaField)

			gotFrame, gotField := p.nextBlitState(tc.prevFrame)
			if gotFrame != tc.wantFrame || gotField != tc.wantField {
				t.Fatalf("nextBlitState(%d) = (%d, %d), want (%d, %d)",
					tc.prevFrame, gotFrame, gotField, tc.wantFrame, tc.wantField)
			}
		})
	}
}

func TestNextBlitState_ProgressiveAlwaysUsesFieldZero(t *testing.T) {
	ml := groovy.NTSC480i60
	ml.Interlace = 0

	p := &Plane{cfg: PlaneConfig{Modeline: ml}}
	p.fpgaFrame.Store(6)
	p.vgaField.Store(true)

	gotFrame, gotField := p.nextBlitState(1)
	if gotFrame != 7 || gotField != 0 {
		t.Fatalf("nextBlitState progressive = (%d, %d), want (7, 0)", gotFrame, gotField)
	}
}
